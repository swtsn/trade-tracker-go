package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/repository"
	"trade-tracker-go/internal/strategy"
)

// ChainService runs chain detection over an account's trade history.
type ChainService struct {
	chains     repository.ChainRepository
	trades     repository.TradeRepository
	txns       repository.TransactionRepository
	classifier StrategyClassifier
}

// NewChainService creates a ChainService with the given dependencies.
func NewChainService(
	chains repository.ChainRepository,
	trades repository.TradeRepository,
	txns repository.TransactionRepository,
	classifier StrategyClassifier,
) *ChainService {
	return &ChainService{
		chains:     chains,
		trades:     trades,
		txns:       txns,
		classifier: classifier,
	}
}

// DetectChains runs the chain detection pass for an account.
//
// Processes all trades in chronological order and applies three rules:
//   - Opening-only trade: starts a new chain.
//   - Mixed trade (close+open): extends the existing chain; records a ChainLink for the event.
//   - Close-only trade: records a close ChainLink; if no open balance remains, marks chain closed.
//
// Idempotent: trades already assigned to a chain (via original_trade_id or chain_links) are skipped.
// Unattributable close-only trades (no matching open chain) are skipped with no error.
// Unattributable mixed trades (close+open, no matching open chain) start a new chain from
// the opening legs and emit a warning log; the closing legs' P&L is not attributed.
//
// Not concurrent-safe: must not be called simultaneously for the same account.
// Loads all trades into memory; not suitable for accounts with very large trade histories.
func (s *ChainService) DetectChains(ctx context.Context, accountID string) error {
	trades, _, err := s.trades.ListByAccount(ctx, accountID, repository.ListTradesOptions{})
	if err != nil {
		return fmt.Errorf("detect chains: list trades: %w", err)
	}

	// Process oldest-first so opening trades create chains before mixed/close trades
	// try to attribute to them. SliceStable + secondary sort by ID ensures deterministic
	// ordering when two trades share the same timestamp.
	sort.SliceStable(trades, func(i, j int) bool {
		if trades[i].ExecutedAt.Equal(trades[j].ExecutedAt) {
			return trades[i].ID < trades[j].ID
		}
		return trades[i].ExecutedAt.Before(trades[j].ExecutedAt)
	})

	for i := range trades {
		if _, _, err := s.processTrade(ctx, &trades[i]); err != nil {
			return fmt.Errorf("detect chains: trade %s: %w", trades[i].ID, err)
		}
	}
	return nil
}

// ProcessTrade runs chain detection for a single trade and returns the chain ID and
// strategy type burned in at chain creation. Called by ImportService as a core write step.
func (s *ChainService) ProcessTrade(ctx context.Context, tradeID string) (string, domain.StrategyType, error) {
	trade, err := s.trades.GetByID(ctx, tradeID)
	if err != nil {
		return "", "", fmt.Errorf("chain service process trade %s: %w", tradeID, err)
	}
	return s.processTrade(ctx, trade)
}

// GetChain returns a chain with its Links populated.
func (s *ChainService) GetChain(ctx context.Context, chainID string) (*domain.Chain, error) {
	return s.chains.GetChainByID(ctx, chainID)
}

// GetChainDetail returns an enriched chain view with per-event leg details.
// Returns domain.ErrNotFound when the chain does not exist or belongs to a different account.
func (s *ChainService) GetChainDetail(ctx context.Context, accountID, chainID string) (*domain.ChainDetail, error) {
	chain, err := s.chains.GetChainByID(ctx, chainID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("chain detail: get chain: %w", err)
	}
	if chain.AccountID != accountID {
		return nil, domain.ErrNotFound
	}

	// Collect trade IDs in order: originating trade first, then each link's trade.
	// Dedup defensively — data integrity violations (e.g. a link pointing at the original
	// trade) would cause ListByTradeIDs to overwrite the map entry, not double-count, but
	// deduplication makes the intent explicit and avoids unnecessary work.
	seen := make(map[string]struct{}, 1+len(chain.Links))
	seen[chain.OriginalTradeID] = struct{}{}
	tradeIDs := []string{chain.OriginalTradeID}
	for _, link := range chain.Links {
		if _, ok := seen[link.ClosingTradeID]; !ok {
			seen[link.ClosingTradeID] = struct{}{}
			tradeIDs = append(tradeIDs, link.ClosingTradeID)
		}
	}

	txnsByTrade, err := s.txns.ListByTradeIDs(ctx, tradeIDs)
	if err != nil {
		return nil, fmt.Errorf("chain detail: list transactions: %w", err)
	}

	pnl, err := s.chains.GetChainPnL(ctx, chainID)
	if err != nil {
		return nil, fmt.Errorf("chain detail: get pnl: %w", err)
	}

	events := make([]domain.ChainEvent, 0, 1+len(chain.Links))
	events = append(events, buildChainEvent(chain.OriginalTradeID, domain.LinkTypeOpen, txnsByTrade[chain.OriginalTradeID]))
	for _, link := range chain.Links {
		events = append(events, buildChainEvent(link.ClosingTradeID, link.LinkType, txnsByTrade[link.ClosingTradeID]))
	}

	return &domain.ChainDetail{
		Chain:  chain,
		Events: events,
		PnL:    pnl,
	}, nil
}

// buildChainEvent constructs a ChainEvent from a trade's transactions.
// CreditDebit is gross-of-fees (same formula as computeCreditDebit); fees are excluded.
// ChainDetail.PnL is net-of-fees and will not reconcile by summing event CreditDebit values.
// All legs of a trade share the same ExecutedAt (one fill per order).
func buildChainEvent(tradeID string, eventType domain.LinkType, txns []domain.Transaction) domain.ChainEvent {
	ev := domain.ChainEvent{
		TradeID:   tradeID,
		EventType: eventType,
	}
	for _, tx := range txns {
		if ev.ExecutedAt.IsZero() {
			ev.ExecutedAt = tx.ExecutedAt
		}
		multiplier := decimal.NewFromInt(1)
		if tx.Instrument.Option != nil {
			multiplier = tx.Instrument.Option.Multiplier
		}
		sign := domain.CashFlowSign(tx.Action)
		ev.CreditDebit = ev.CreditDebit.Add(sign.Mul(tx.FillPrice).Mul(tx.Quantity.Abs()).Mul(multiplier))
		ev.Legs = append(ev.Legs, domain.ChainEventLeg{
			Action:     tx.Action,
			Instrument: tx.Instrument,
			Quantity:   tx.Quantity.Abs(),
		})
	}
	return ev
}

// ListChains returns chains for an account. Links are not populated; use GetChain for detail.
func (s *ChainService) ListChains(ctx context.Context, accountID string, openOnly bool) ([]domain.Chain, error) {
	return s.chains.ListChainsByAccount(ctx, accountID, openOnly)
}

// GetChainPnL returns the total net P&L for the chain computed from transaction data.
func (s *ChainService) GetChainPnL(ctx context.Context, chainID string) (decimal.Decimal, error) {
	return s.chains.GetChainPnL(ctx, chainID)
}

// processTrade applies the appropriate chain action and returns the chain ID and strategy type.
// Returns empty strings for neutral-only trades or close-only trades with no matching open chain.
func (s *ChainService) processTrade(ctx context.Context, trade *domain.Trade) (string, domain.StrategyType, error) {
	// Idempotency: if this trade is already assigned to a chain, return its ID and strategy.
	existing, err := s.chains.GetChainByTradeID(ctx, trade.ID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return "", "", fmt.Errorf("idempotency check: %w", err)
	}
	if existing != nil {
		return existing.ID, existing.StrategyType, nil
	}

	txns, err := s.txns.ListByTrade(ctx, trade.ID)
	if err != nil {
		return "", "", fmt.Errorf("list transactions: %w", err)
	}

	// Equity-only trades get a durable per-symbol chain instead of an episode chain.
	if allEquityTxns(txns) {
		return s.findOrCreateStockChain(ctx, trade, txns)
	}

	var hasOpening, hasClosing bool
	for _, tx := range txns {
		switch tx.PositionEffect {
		case domain.PositionEffectOpening:
			hasOpening = true
		case domain.PositionEffectClosing:
			hasClosing = true
		}
	}

	if !hasClosing {
		if !hasOpening {
			return "", "", nil // neutral-only trade (e.g. dividend); nothing to chain
		}
		return s.startChain(ctx, trade, txns, false)
	}

	chainID, err := s.attributeChain(ctx, txns)
	if err != nil {
		if errors.Is(err, domain.ErrUnattributableTrade) {
			if !hasOpening {
				// Close-only trade with no matching open chain — skip entirely.
				// Typical for out-of-order imports; re-run DetectChains once the
				// opening trade is present.
				return "", "", nil
			}
			// Mixed trade (close + open) with no open chain for the closing legs.
			// This is expected on first imports that start mid-history: the original
			// opening trade was never imported, so there is no chain to attribute to.
			// Start a new chain from this trade so the opening legs are not orphaned.
			// The closing legs' P&L is unattributed; chain.AttributionGap flags this.
			// Manual chain stitching or a re-run of DetectChains after importing
			// earlier history is required to repair this.
			slog.Warn("chain service: mixed trade has no open chain for closing legs; starting new chain — closing P&L unattributed",
				"trade_id", trade.ID)
			return s.startChain(ctx, trade, txns, true)
		}
		return "", "", err
	}

	if hasOpening {
		chainID, err = s.extendChain(ctx, trade, txns, chainID)
	} else {
		chainID, err = s.maybeCloseChain(ctx, trade, txns, chainID)
	}
	if err != nil {
		return "", "", err
	}
	if chainID == "" {
		return "", "", nil
	}
	chain, err := s.chains.GetChainByID(ctx, chainID)
	if err != nil {
		return "", "", fmt.Errorf("get chain strategy: %w", err)
	}
	return chainID, chain.StrategyType, nil
}

// startChain creates a new Chain for an opening trade and returns the new chain ID and strategy.
// Strategy is classified from the opening legs of txns.
// attributionGap should be true when the trade also has closing legs that could not be
// attributed to an existing open chain.
func (s *ChainService) startChain(ctx context.Context, trade *domain.Trade, txns []domain.Transaction, attributionGap bool) (string, domain.StrategyType, error) {
	chainID, err := uuid.NewV7()
	if err != nil {
		return "", "", fmt.Errorf("generate chain id: %w", err)
	}
	legs := strategy.FromTransactions(txns)
	strategyType := s.classifier.Classify(legs)
	if strategyType == domain.StrategyUnknown {
		legStrs := make([]string, len(legs))
		for i, l := range legs {
			legStrs[i] = l.String()
		}
		slog.Error("chain service: unclassified strategy",
			"trade_id", trade.ID,
			"account_id", trade.AccountID,
			"symbol", trade.UnderlyingSymbol,
			"legs", legStrs,
		)
	}
	chain := &domain.Chain{
		ID:               chainID.String(),
		AccountID:        trade.AccountID,
		UnderlyingSymbol: underlyingSymbol(txns),
		OriginalTradeID:  trade.ID,
		CreatedAt:        trade.ExecutedAt,
		StrategyType:     strategyType,
		AttributionGap:   attributionGap,
	}
	if err := s.chains.CreateChain(ctx, chain); err != nil {
		return "", "", fmt.Errorf("start chain: %w", err)
	}
	return chain.ID, strategyType, nil
}

// extendChain records a roll or adjustment link for a mixed trade and returns the chain ID.
func (s *ChainService) extendChain(ctx context.Context, trade *domain.Trade, txns []domain.Transaction, chainID string) (string, error) {
	existingLinks, err := s.chains.ListChainLinks(ctx, chainID)
	if err != nil {
		return "", fmt.Errorf("list chain links: %w", err)
	}
	linkID, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate link id: %w", err)
	}
	strikeChange, expirationChange := computeStrikeExpChange(txns)
	link := &domain.ChainLink{
		ID:               linkID.String(),
		ChainID:          chainID,
		Sequence:         len(existingLinks) + 1,
		LinkType:         detectLinkType(txns),
		ClosingTradeID:   trade.ID,
		OpeningTradeID:   trade.ID,
		LinkedAt:         trade.ExecutedAt,
		StrikeChange:     strikeChange,
		ExpirationChange: expirationChange,
		CreditDebit:      computeCreditDebit(txns),
	}
	if err := s.chains.CreateChainLink(ctx, link); err != nil {
		return "", fmt.Errorf("extend chain: %w", err)
	}
	return chainID, nil
}

// maybeCloseChain records a terminal close link for a close-only trade, marks the
// chain closed if no open balance remains, and returns the chain ID.
func (s *ChainService) maybeCloseChain(ctx context.Context, trade *domain.Trade, txns []domain.Transaction, chainID string) (string, error) {
	// Record the closing trade in the chain so ChainIsOpen includes its transactions.
	existingLinks, err := s.chains.ListChainLinks(ctx, chainID)
	if err != nil {
		return "", fmt.Errorf("list chain links: %w", err)
	}
	linkID, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate link id: %w", err)
	}
	link := &domain.ChainLink{
		ID:             linkID.String(),
		ChainID:        chainID,
		Sequence:       len(existingLinks) + 1,
		LinkType:       domain.LinkTypeClose,
		ClosingTradeID: trade.ID,
		OpeningTradeID: trade.ID, // NOT NULL in schema; set to same for terminal links
		LinkedAt:       trade.ExecutedAt,
		CreditDebit:    computeCreditDebit(txns),
	}
	if err := s.chains.CreateChainLink(ctx, link); err != nil {
		return "", fmt.Errorf("record close link: %w", err)
	}

	hasOpen, err := s.chains.ChainIsOpen(ctx, chainID)
	if err != nil {
		return "", fmt.Errorf("check open balance: %w", err)
	}
	if hasOpen {
		return chainID, nil
	}
	if err := s.chains.UpdateChainClosed(ctx, chainID, trade.ExecutedAt); err != nil {
		return "", fmt.Errorf("mark chain closed: %w", err)
	}
	return chainID, nil
}

// attributeChain finds the open chain in the account that holds the instrument from
// the first closing transaction. Returns an error when no matching open chain exists.
func (s *ChainService) attributeChain(ctx context.Context, txns []domain.Transaction) (string, error) {
	for _, tx := range txns {
		if tx.PositionEffect != domain.PositionEffectClosing {
			continue
		}
		chain, err := s.chains.GetOpenChainForInstrument(ctx, tx.AccountID, tx.Instrument.InstrumentID())
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			return "", fmt.Errorf("attribute chain for instrument %s: %w", tx.Instrument.InstrumentID(), err)
		}
		return chain.ID, nil
	}
	var tried []string
	for _, tx := range txns {
		if tx.PositionEffect == domain.PositionEffectClosing {
			tried = append(tried, tx.Instrument.InstrumentID())
		}
	}
	return "", fmt.Errorf("%w: no open chain found for instruments %v", domain.ErrUnattributableTrade, tried)
}

// underlyingSymbol returns the underlying symbol for a chain from the trade's transactions.
// Uses the first opening leg's symbol; falls back to the first transaction.
func underlyingSymbol(txns []domain.Transaction) string {
	for _, tx := range txns {
		if tx.PositionEffect == domain.PositionEffectOpening {
			return tx.Instrument.Symbol
		}
	}
	if len(txns) > 0 {
		return txns[0].Instrument.Symbol
	}
	return ""
}

// detectLinkType returns the link type for a mixed trade.
// Assignment and exercise are detected from action; everything else is a roll.
func detectLinkType(txns []domain.Transaction) domain.LinkType {
	for _, tx := range txns {
		if tx.Action == domain.ActionAssignment {
			return domain.LinkTypeAssignment
		}
		if tx.Action == domain.ActionExercise {
			return domain.LinkTypeExercise
		}
	}
	return domain.LinkTypeRoll
}

// computeCreditDebit returns the gross premium across all legs of the trade (fees excluded).
// Positive = net credit received; negative = net debit paid.
// Transaction.Quantity is always non-negative; direction is encoded in Action via CashFlowSign.
func computeCreditDebit(txns []domain.Transaction) decimal.Decimal {
	total := decimal.Zero
	for _, tx := range txns {
		multiplier := decimal.NewFromInt(1)
		if tx.Instrument.Option != nil {
			multiplier = tx.Instrument.Option.Multiplier
		}
		sign := domain.CashFlowSign(tx.Action)
		total = total.Add(sign.Mul(tx.FillPrice).Mul(tx.Quantity.Abs()).Mul(multiplier))
	}
	return total
}

// findOrCreateStockChain returns the durable stock chain for the trade's symbol,
// creating it on first encounter. Subsequent trades for the same (account, symbol)
// are recorded as chain links so that GetChainByTradeID finds them on re-runs.
func (s *ChainService) findOrCreateStockChain(ctx context.Context, trade *domain.Trade, txns []domain.Transaction) (string, domain.StrategyType, error) {
	symbol := underlyingSymbol(txns)
	existing, err := s.chains.GetStockChainBySymbol(ctx, trade.AccountID, symbol)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return "", "", fmt.Errorf("find stock chain for %s: %w", symbol, err)
	}

	if errors.Is(err, domain.ErrNotFound) {
		// First trade for this symbol — create the durable stock chain.
		chainID, err := uuid.NewV7()
		if err != nil {
			return "", "", fmt.Errorf("generate chain id: %w", err)
		}
		chain := &domain.Chain{
			ID:               chainID.String(),
			AccountID:        trade.AccountID,
			UnderlyingSymbol: symbol,
			OriginalTradeID:  trade.ID,
			CreatedAt:        trade.ExecutedAt,
			StrategyType:     domain.StrategyStock,
		}
		if err := s.chains.CreateChain(ctx, chain); err != nil {
			return "", "", fmt.Errorf("create stock chain: %w", err)
		}
		return chain.ID, domain.StrategyStock, nil
	}

	// Chain already exists — record a link so GetChainByTradeID finds this trade.
	existingLinks, err := s.chains.ListChainLinks(ctx, existing.ID)
	if err != nil {
		return "", "", fmt.Errorf("list stock chain links: %w", err)
	}
	linkID, err := uuid.NewV7()
	if err != nil {
		return "", "", fmt.Errorf("generate link id: %w", err)
	}
	link := &domain.ChainLink{
		ID:             linkID.String(),
		ChainID:        existing.ID,
		Sequence:       len(existingLinks) + 1,
		LinkType:       domain.LinkTypeRoll,
		ClosingTradeID: trade.ID,
		OpeningTradeID: trade.ID,
		LinkedAt:       trade.ExecutedAt,
		CreditDebit:    computeCreditDebit(txns),
	}
	if err := s.chains.CreateChainLink(ctx, link); err != nil {
		return "", "", fmt.Errorf("record stock chain link: %w", err)
	}
	return existing.ID, domain.StrategyStock, nil
}

// allEquityTxns reports whether every transaction is an equity (stock) trade.
func allEquityTxns(txns []domain.Transaction) bool {
	if len(txns) == 0 {
		return false
	}
	for _, tx := range txns {
		if tx.Instrument.AssetClass != domain.AssetClassEquity {
			return false
		}
	}
	return true
}

// computeStrikeExpChange computes strike and expiration deltas for a single-leg roll.
// Returns zeros for multi-leg rolls or non-option trades.
func computeStrikeExpChange(txns []domain.Transaction) (strikeChange decimal.Decimal, expirationChangeDays int) {
	var closingOpts, openingOpts []domain.Transaction
	for _, tx := range txns {
		if tx.Instrument.Option == nil {
			continue
		}
		switch tx.PositionEffect {
		case domain.PositionEffectClosing:
			closingOpts = append(closingOpts, tx)
		case domain.PositionEffectOpening:
			openingOpts = append(openingOpts, tx)
		}
	}
	if len(closingOpts) != 1 || len(openingOpts) != 1 {
		return decimal.Zero, 0
	}
	closing := closingOpts[0]
	opening := openingOpts[0]
	strikeChange = opening.Instrument.Option.Strike.Sub(closing.Instrument.Option.Strike)
	openExp := opening.Instrument.Option.Expiration.Truncate(24 * time.Hour)
	closeExp := closing.Instrument.Option.Expiration.Truncate(24 * time.Hour)
	days := int(openExp.Sub(closeExp) / (24 * time.Hour))
	return strikeChange, days
}
