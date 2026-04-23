package service

import (
	"context"
	"fmt"

	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/repository"
	"trade-tracker-go/internal/strategy"
)

// PostImportHook is invoked after each trade is successfully persisted and chained.
// Name identifies the hook in ImportResult.Errors when the hook fails.
type PostImportHook struct {
	Name string
	Run  func(ctx context.Context, tradeID string, txns []domain.Transaction, chainID string, strategyType domain.StrategyType) error
}

// ImportResult summarizes the outcome of an Import call.
// Imported and Failed count trade groups (one entry per TradeID).
// Skipped counts individual transactions (not groups) skipped due to duplicate BrokerTxID.
// There is no single invariant across all three counters because they measure different units.
type ImportResult struct {
	Imported int // trade groups fully persisted with all hooks successful
	Skipped  int // individual transactions skipped due to duplicate BrokerTxID
	Failed   int // trade groups (or hooks) that errored
	Errors   []ImportError
}

// ImportError records a failure for one trade group or hook invocation.
// HookName is empty for trade processing errors; set to the hook's Name for hook errors.
type ImportError struct {
	TradeID  string
	HookName string
	Err      error
}

// ImportService orchestrates persisting normalized transactions into the database.
// It operates on domain.Transaction slices produced by broker parsers; it knows
// nothing about broker file formats.
type ImportService struct {
	trades      repository.TradeRepository
	txns        repository.TransactionRepository
	instruments repository.InstrumentRepository
	classifier  StrategyClassifier
	chainer     TradeChainer
	hooks       []PostImportHook
}

// NewImportService creates an ImportService with the given dependencies.
// chainer is called for every trade to create or extend its chain before hooks run.
// Optional hooks are run after each trade is persisted and chained.
func NewImportService(
	trades repository.TradeRepository,
	txns repository.TransactionRepository,
	instruments repository.InstrumentRepository,
	classifier StrategyClassifier,
	chainer TradeChainer,
	hooks ...PostImportHook,
) *ImportService {
	return &ImportService{
		trades:      trades,
		txns:        txns,
		instruments: instruments,
		classifier:  classifier,
		chainer:     chainer,
		hooks:       hooks,
	}
}

// Import processes a batch of normalized domain transactions.
//
// Steps:
//  1. Dedup by BrokerTxID — skip already-imported transactions (single round-trip).
//  2. Upsert instruments for all fresh transactions.
//  3. Group fresh transactions by TradeID (set by the broker parser).
//  4. Per group: classify strategy → create Trade → create Transactions
//     (closing legs first, then opening) → run hooks.
//
// Failures are per-trade-group. A failing group is recorded in ImportResult.Errors
// and processing continues. A top-level error is returned only when the import
// cannot proceed at all (e.g. DB connection lost).
func (s *ImportService) Import(ctx context.Context, txs []domain.Transaction) (*ImportResult, error) {
	result := &ImportResult{}

	// 1. Dedup — single bulk query instead of one SELECT per transaction.
	keys := make([]repository.BrokerTxKey, len(txs))
	for i, tx := range txs {
		keys[i] = repository.BrokerTxKey{BrokerTxID: tx.BrokerTxID, Broker: tx.Broker, AccountID: tx.AccountID}
	}
	existing, err := s.txns.FilterExistingBrokerTxIDs(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("import: dedup check: %w", err)
	}

	var fresh []domain.Transaction
	for _, tx := range txs {
		k := repository.BrokerTxKey{BrokerTxID: tx.BrokerTxID, Broker: tx.Broker, AccountID: tx.AccountID}
		if existing[k] {
			result.Skipped++
		} else {
			fresh = append(fresh, tx)
		}
	}

	if len(fresh) == 0 {
		return result, nil
	}

	// 2. Upsert instruments (deduplicated by InstrumentID).
	seen := make(map[string]struct{})
	for _, tx := range fresh {
		id := tx.Instrument.InstrumentID()
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if err := s.instruments.Upsert(ctx, &tx.Instrument); err != nil {
			return nil, fmt.Errorf("import: upsert instrument %s: %w", tx.Instrument.Symbol, err)
		}
	}

	// 3. Group transactions by TradeID, preserving first-seen order for deterministic processing.
	trades := make(map[string][]domain.Transaction)
	var tradeOrder []string
	for _, tx := range fresh {
		if _, exists := trades[tx.TradeID]; !exists {
			tradeOrder = append(tradeOrder, tx.TradeID)
		}
		trades[tx.TradeID] = append(trades[tx.TradeID], tx)
	}

	// 4. Process each trade.
	for _, tradeID := range tradeOrder {
		if fatal := s.processTrade(ctx, tradeID, trades[tradeID], result); fatal != nil {
			return nil, fatal
		}
	}

	return result, nil
}

// processTrade persists one trade and fires post-import hooks.
// Returns a non-nil error only for fatal infrastructure failures.
//
// NOTE: the trade row and transaction rows are written in separate DB calls with no
// wrapping SQL transaction. If a txns.Create call fails after trades.Create has
// succeeded, the trade row remains in the DB with a partial transaction list.
// To detect orphaned trades run:
//
//	SELECT t.id FROM trades t
//	LEFT JOIN transactions tx ON tx.trade_id = t.id
//	WHERE tx.id IS NULL;
//
// Full cross-operation atomicity requires transaction propagation at the repository
// layer (deferred — see docs/future.md).
func (s *ImportService) processTrade(ctx context.Context, tradeID string, txs []domain.Transaction, result *ImportResult) error {
	if !allSameUnderlying(txs) {
		result.Failed++
		result.Errors = append(result.Errors, ImportError{
			TradeID: tradeID,
			Err:     fmt.Errorf("mixed underlying symbols in trade group — possible CSV grouping error"),
		})
		return nil
	}

	strategyType := s.classifier.Classify(strategy.FromTransactions(txs))
	trade := buildTrade(tradeID, txs, strategyType)

	if err := s.trades.Create(ctx, trade); err != nil {
		result.Failed++
		result.Errors = append(result.Errors, ImportError{
			TradeID: tradeID,
			Err:     fmt.Errorf("create trade: %w", err),
		})
		return nil
	}

	// Create transactions: closing legs first, then opening legs.
	// orderedTxs is also passed to hooks below so closingFirst is called only once.
	orderedTxs := closingFirst(txs)
	for _, tx := range orderedTxs {
		if err := s.txns.Create(ctx, &tx); err != nil {
			result.Failed++
			result.Errors = append(result.Errors, ImportError{
				TradeID: tradeID,
				Err:     fmt.Errorf("create transaction %s: %w", tx.BrokerTxID, err),
			})
			return nil
		}
	}

	// Create or extend the chain for this trade. This is a core write step, not a hook.
	chainID, err := s.chainer.ProcessTrade(ctx, tradeID)
	if err != nil {
		result.Failed++
		result.Errors = append(result.Errors, ImportError{
			TradeID: tradeID,
			Err:     fmt.Errorf("chain trade: %w", err),
		})
		return nil
	}

	// Run hooks with the resolved chain ID. If any hook fails, the trade is counted as
	// Failed (not Imported). The trade, transactions, and chain have already been
	// persisted; hook failures do not roll back DB writes.
	//
	// Hooks receive transactions in closing-first order, matching the order they were
	// written to the DB. This satisfies PositionService.ProcessTrade's contract that
	// txns are in closing-first order.
	//
	// Hooks run sequentially and stop at the first failure. A later hook may depend on
	// state written by an earlier hook (e.g. PositionService creates lots that a
	// reporting hook reads), so continuing after a failure would observe corrupt state.
	hookFailed := false
	for _, hook := range s.hooks {
		if err := hook.Run(ctx, trade.ID, orderedTxs, chainID, strategyType); err != nil {
			hookFailed = true
			result.Failed++
			result.Errors = append(result.Errors, ImportError{
				TradeID:  tradeID,
				HookName: hook.Name,
				Err:      fmt.Errorf("hook %q: %w", hook.Name, err),
			})
			break
		}
	}

	if !hookFailed {
		result.Imported++
	}

	return nil
}

// buildTrade constructs a domain.Trade from a group of transactions.
// AccountID, Broker, and UnderlyingSymbol are taken from the first transaction (all legs
// share them). UnderlyingSymbol prefers the first opening leg; falls back to txs[0].
// ExecutedAt is the earliest ExecutedAt across the group.
// txs must not be empty.
func buildTrade(tradeID string, txs []domain.Transaction, strategyType domain.StrategyType) *domain.Trade {
	if len(txs) == 0 {
		panic("buildTrade: txs must not be empty")
	}
	earliest := txs[0].ExecutedAt
	for _, tx := range txs[1:] {
		if tx.ExecutedAt.Before(earliest) {
			earliest = tx.ExecutedAt
		}
	}
	return &domain.Trade{
		ID:               tradeID,
		AccountID:        txs[0].AccountID,
		Broker:           txs[0].Broker,
		StrategyType:     strategyType,
		UnderlyingSymbol: underlyingSymbol(txs),
		ExecutedAt:       earliest,
	}
}

// allSameUnderlying reports whether every transaction in the group shares the same
// Instrument.Symbol. A mismatch indicates a CSV grouping error or unsupported
// multi-underlying trade and should fail loudly rather than silently store a wrong symbol.
func allSameUnderlying(txs []domain.Transaction) bool {
	if len(txs) == 0 {
		return true
	}
	sym := txs[0].Instrument.Symbol
	for _, tx := range txs[1:] {
		if tx.Instrument.Symbol != sym {
			return false
		}
	}
	return true
}

// closingFirst returns a copy of txs with closing legs before opening legs,
// preserving relative order within each group. Transactions with an unrecognized
// PositionEffect are appended last rather than dropped.
func closingFirst(txs []domain.Transaction) []domain.Transaction {
	out := make([]domain.Transaction, 0, len(txs))
	for _, tx := range txs {
		if tx.PositionEffect == domain.PositionEffectClosing {
			out = append(out, tx)
		}
	}
	for _, tx := range txs {
		if tx.PositionEffect == domain.PositionEffectOpening {
			out = append(out, tx)
		}
	}
	// Append unrecognized effects last rather than silently dropping them.
	for _, tx := range txs {
		if tx.PositionEffect != domain.PositionEffectClosing && tx.PositionEffect != domain.PositionEffectOpening {
			out = append(out, tx)
		}
	}
	return out
}
