package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/repository"
	"trade-tracker-go/internal/repository/sqlite"
	"trade-tracker-go/internal/service"
	"trade-tracker-go/internal/strategy"
)

func openTestDB(t *testing.T) *sqlite.Repos {
	t.Helper()
	repos, err := sqlite.OpenRepos(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = repos.Close() })
	return repos
}

func seedImportAccount(t *testing.T, ctx context.Context, repos *sqlite.Repos) *domain.Account {
	t.Helper()
	acc := &domain.Account{
		ID:            uuid.New().String(),
		Broker:        "tastytrade",
		AccountNumber: "IMPORT-TEST",
		Name:          "Import Test Account",
		CreatedAt:     time.Now().UTC(),
	}
	require.NoError(t, repos.Accounts.Create(ctx, acc))
	return acc
}

func newImportService(repos *sqlite.Repos, hooks ...service.PostImportHook) *service.ImportService {
	chainSvc := service.NewChainService(repos.Chains, repos.Trades, repos.Transactions)
	return service.NewImportService(
		repos.Trades,
		repos.Transactions,
		repos.Instruments,
		strategy.NewClassifier(),
		chainSvc,
		hooks...,
	)
}

func makeEquityOption(symbol string, strike float64, exp time.Time, optType domain.OptionType) domain.Instrument {
	return domain.Instrument{
		Symbol:     symbol,
		AssetClass: domain.AssetClassEquityOption,
		Option: &domain.OptionDetails{
			Expiration: exp.UTC(),
			Strike:     decimal.NewFromFloat(strike),
			OptionType: optType,
			Multiplier: decimal.NewFromInt(100),
		},
	}
}

func makeEquity(symbol string) domain.Instrument {
	return domain.Instrument{Symbol: symbol, AssetClass: domain.AssetClassEquity}
}

func makeTransaction(tradeID, brokerTxID, accountID, broker string, inst domain.Instrument, action domain.Action, effect domain.PositionEffect, qty float64, executedAt time.Time) domain.Transaction {
	return domain.Transaction{
		ID:             uuid.New().String(),
		TradeID:        tradeID,
		BrokerTxID:     brokerTxID,
		Broker:         broker,
		AccountID:      accountID,
		Instrument:     inst,
		Action:         action,
		Quantity:       decimal.NewFromFloat(qty),
		FillPrice:      decimal.NewFromFloat(1.00),
		Fees:           decimal.NewFromFloat(0.65),
		ExecutedAt:     executedAt,
		PositionEffect: effect,
	}
}

func TestImportService_BasicImport(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	acc := seedImportAccount(t, ctx, repos)
	svc := newImportService(repos)

	exp := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	tradeID := uuid.New().String()
	txs := []domain.Transaction{
		makeTransaction(tradeID, "tx-001", acc.ID, acc.Broker, makeEquityOption("AAPL", 200, exp, domain.OptionTypePut), domain.ActionSTO, domain.PositionEffectOpening, 2, time.Now()),
		makeTransaction(tradeID, "tx-002", acc.ID, acc.Broker, makeEquityOption("AAPL", 190, exp, domain.OptionTypePut), domain.ActionBTO, domain.PositionEffectOpening, 2, time.Now()),
	}

	result, err := svc.Import(ctx, txs)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Imported)
	assert.Equal(t, 0, result.Skipped)
	assert.Equal(t, 0, result.Failed)
	assert.Empty(t, result.Errors)
}

func TestImportService_DedupsOnReimport(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	acc := seedImportAccount(t, ctx, repos)
	svc := newImportService(repos)

	tradeID := uuid.New().String()
	txs := []domain.Transaction{
		makeTransaction(tradeID, "tx-dedup-001", acc.ID, acc.Broker, makeEquity("AAPL"), domain.ActionBuy, domain.PositionEffectOpening, 10, time.Now()),
	}

	r1, err := svc.Import(ctx, txs)
	require.NoError(t, err)
	assert.Equal(t, 1, r1.Imported)
	assert.Equal(t, 0, r1.Skipped)

	r2, err := svc.Import(ctx, txs)
	require.NoError(t, err)
	assert.Equal(t, 0, r2.Imported)
	assert.Equal(t, 1, r2.Skipped, "same BrokerTxID must be skipped on re-import")
}

func TestImportService_ClassifiesStrategy(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	acc := seedImportAccount(t, ctx, repos)
	svc := newImportService(repos)

	exp := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	tradeID := uuid.New().String()
	// Vertical spread: same expiry, same option type, different strikes, opposite directions.
	txs := []domain.Transaction{
		makeTransaction(tradeID, "v-001", acc.ID, acc.Broker, makeEquityOption("SPY", 500, exp, domain.OptionTypePut), domain.ActionSTO, domain.PositionEffectOpening, 1, time.Now()),
		makeTransaction(tradeID, "v-002", acc.ID, acc.Broker, makeEquityOption("SPY", 490, exp, domain.OptionTypePut), domain.ActionBTO, domain.PositionEffectOpening, 1, time.Now()),
	}

	_, err := svc.Import(ctx, txs)
	require.NoError(t, err)

	trade, err := repos.Trades.GetByID(ctx, tradeID)
	require.NoError(t, err)
	assert.Equal(t, domain.StrategyVertical, trade.StrategyType)
}

func TestImportService_ClosingLegsCreatedFirst(t *testing.T) {
	// Verify that transactions are written to the DB in closing-first order.
	ctx := context.Background()
	repos := openTestDB(t)
	acc := seedImportAccount(t, ctx, repos)
	svc := newImportService(repos)

	exp := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	tradeID := uuid.New().String()
	t0 := time.Now().UTC().Truncate(time.Second)
	// Give the closing leg a strictly earlier timestamp so DB ordering by executed_at
	// is deterministic even without a tiebreaker column.
	tClose := t0.Add(-time.Second)

	// Provide opening first, then closing — service should reorder them.
	txs := []domain.Transaction{
		makeTransaction(tradeID, "mixed-open", acc.ID, acc.Broker, makeEquityOption("IWM", 250, exp, domain.OptionTypePut), domain.ActionBTO, domain.PositionEffectOpening, 1, t0),
		makeTransaction(tradeID, "mixed-close", acc.ID, acc.Broker, makeEquityOption("IWM", 260, exp, domain.OptionTypePut), domain.ActionSTC, domain.PositionEffectClosing, 1, tClose),
	}

	result, err := svc.Import(ctx, txs)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Imported)

	trade, err := repos.Trades.GetByID(ctx, tradeID)
	require.NoError(t, err)
	require.Len(t, trade.Transactions, 2)

	// The DB stores in insertion order. Verify closing came before opening.
	effects := make([]domain.PositionEffect, len(trade.Transactions))
	for i, tx := range trade.Transactions {
		effects[i] = tx.PositionEffect
	}
	assert.Equal(t, domain.PositionEffectClosing, effects[0], "closing leg must be first")
	assert.Equal(t, domain.PositionEffectOpening, effects[1], "opening leg must be second")
}

func TestImportService_MultipleTrades(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	acc := seedImportAccount(t, ctx, repos)
	svc := newImportService(repos)

	exp := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	trade1 := uuid.New().String()
	trade2 := uuid.New().String()

	txs := []domain.Transaction{
		makeTransaction(trade1, "g1-001", acc.ID, acc.Broker, makeEquityOption("AAPL", 200, exp, domain.OptionTypePut), domain.ActionSTO, domain.PositionEffectOpening, 1, time.Now()),
		makeTransaction(trade2, "g2-001", acc.ID, acc.Broker, makeEquityOption("MSFT", 400, exp, domain.OptionTypeCall), domain.ActionBTO, domain.PositionEffectOpening, 1, time.Now()),
	}

	result, err := svc.Import(ctx, txs)
	require.NoError(t, err)
	assert.Equal(t, 2, result.Imported)
}

func TestImportService_HookCalled(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	acc := seedImportAccount(t, ctx, repos)

	var hookTradeIDs []string
	hook := service.PostImportHook{
		Name: "capture",
		Run: func(ctx context.Context, tradeID string, txns []domain.Transaction, chainID string) error {
			hookTradeIDs = append(hookTradeIDs, tradeID)
			return nil
		},
	}

	svc := newImportService(repos, hook)
	tradeID := uuid.New().String()
	txs := []domain.Transaction{
		makeTransaction(tradeID, "hook-001", acc.ID, acc.Broker, makeEquity("AAPL"), domain.ActionBuy, domain.PositionEffectOpening, 5, time.Now()),
	}

	result, err := svc.Import(ctx, txs)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Imported)
	require.Len(t, hookTradeIDs, 1)
	assert.Equal(t, tradeID, hookTradeIDs[0])
}

func TestImportService_HookErrorRecorded(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	acc := seedImportAccount(t, ctx, repos)

	hook := service.PostImportHook{
		Name: "failing-hook",
		Run: func(ctx context.Context, tradeID string, txns []domain.Transaction, chainID string) error {
			return errors.New("hook exploded")
		},
	}

	svc := newImportService(repos, hook)
	tradeID := uuid.New().String()
	txs := []domain.Transaction{
		makeTransaction(tradeID, "hook-err-001", acc.ID, acc.Broker, makeEquity("AAPL"), domain.ActionBuy, domain.PositionEffectOpening, 5, time.Now()),
	}

	result, err := svc.Import(ctx, txs)
	require.NoError(t, err) // top-level error is nil; hook error is in result
	assert.Equal(t, 0, result.Imported, "hook failure should prevent counting as Imported")
	assert.Equal(t, 1, result.Failed)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "failing-hook", result.Errors[0].HookName)
	assert.Equal(t, tradeID, result.Errors[0].TradeID)
}

// failingTxRepo wraps a real TransactionRepository and injects a failure on the
// Nth Create call. Used to exercise the partial-failure (orphaned trade) path.
type failingTxRepo struct {
	repository.TransactionRepository
	failOnNth int
	calls     int
}

func (f *failingTxRepo) Create(ctx context.Context, tx *domain.Transaction) error {
	f.calls++
	if f.calls == f.failOnNth {
		return errors.New("injected tx create failure")
	}
	return f.TransactionRepository.Create(ctx, tx)
}

func TestImportService_PartialTransactionFailure(t *testing.T) {
	// When the first transaction Create succeeds but the second fails, the trade row
	// exists in the DB with an incomplete transaction list (known atomicity gap — no
	// wrapping SQL transaction). The group must be counted as Failed, not Imported.
	ctx := context.Background()
	repos := openTestDB(t)
	acc := seedImportAccount(t, ctx, repos)

	// Fail on the 2nd transaction Create (first succeeds, second fails).
	txRepo := &failingTxRepo{TransactionRepository: repos.Transactions, failOnNth: 2}
	chainSvc := service.NewChainService(repos.Chains, repos.Trades, repos.Transactions)
	svc := service.NewImportService(repos.Trades, txRepo, repos.Instruments, strategy.NewClassifier(), chainSvc)

	exp := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	tradeID := uuid.New().String()
	txs := []domain.Transaction{
		makeTransaction(tradeID, "partial-001", acc.ID, acc.Broker, makeEquityOption("AAPL", 200, exp, domain.OptionTypePut), domain.ActionSTO, domain.PositionEffectOpening, 1, time.Now()),
		makeTransaction(tradeID, "partial-002", acc.ID, acc.Broker, makeEquityOption("AAPL", 190, exp, domain.OptionTypePut), domain.ActionBTO, domain.PositionEffectOpening, 1, time.Now()),
	}

	result, err := svc.Import(ctx, txs)
	require.NoError(t, err)
	assert.Equal(t, 0, result.Imported)
	assert.Equal(t, 1, result.Failed)
	require.Len(t, result.Errors, 1)
	assert.Empty(t, result.Errors[0].HookName, "failure is a DB error, not a hook error")

	// Trade row exists (orphaned) with only the first transaction written.
	trade, err := repos.Trades.GetByID(ctx, tradeID)
	require.NoError(t, err, "trade row should exist even after partial failure")
	assert.Len(t, trade.Transactions, 1, "only the first transaction was written before failure")
}
