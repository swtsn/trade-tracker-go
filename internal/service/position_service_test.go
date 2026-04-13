package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/repository/sqlite"
	"trade-tracker-go/internal/service"
)

// --- test helpers ---

func openTestDB(t *testing.T) *sqlite.Repos {
	t.Helper()
	repos, err := sqlite.OpenRepos(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = repos.Close() })
	return repos
}

func newPositionService(repos *sqlite.Repos) *service.PositionService {
	return service.NewPositionService(repos.Positions)
}

func mustDecimal(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return d
}

var (
	baseTime = time.Date(2025, 1, 10, 10, 0, 0, 0, time.UTC)

	equityInst = domain.Instrument{
		Symbol:     "SPY",
		AssetClass: domain.AssetClassEquity,
	}

	optionInst = domain.Instrument{
		Symbol:     "SPY",
		AssetClass: domain.AssetClassEquityOption,
		Option: &domain.OptionDetails{
			Expiration: time.Date(2025, 3, 21, 0, 0, 0, 0, time.UTC),
			Strike:     mustDecimal("500"),
			OptionType: domain.OptionTypePut,
			Multiplier: decimal.NewFromInt(100),
			OSI:        "SPY 032125P00500000",
		},
	}
)

// testCtx holds DB-seeded entities required as FK targets for position_lots.
// The import service creates these rows before calling PositionService; tests
// must do the same.
type testCtx struct {
	accountID string
}

// seedCtx creates an account, seeds both instruments, and returns the context.
func seedCtx(t *testing.T, ctx context.Context, repos *sqlite.Repos) testCtx {
	t.Helper()
	acc := &domain.Account{
		ID:            uuid.New().String(),
		Broker:        "tastytrade",
		AccountNumber: "TEST123",
		Name:          "Test Account",
		CreatedAt:     baseTime,
	}
	require.NoError(t, repos.Accounts.Create(ctx, acc))
	require.NoError(t, repos.Instruments.Upsert(ctx, &equityInst))
	require.NoError(t, repos.Instruments.Upsert(ctx, &optionInst))
	return testCtx{accountID: acc.ID}
}

// makeTx builds a domain.Transaction and persists the required parent trade and
// transaction rows so position_lots FK constraints are satisfied.
// price and fees are decimal.Decimal to avoid float representation issues with
// financial values.
func makeTx(t *testing.T, ctx context.Context, repos *sqlite.Repos, tc testCtx, inst domain.Instrument, action domain.Action, qty float64, price, fees decimal.Decimal, effect domain.PositionEffect, at time.Time) domain.Transaction {
	t.Helper()

	trade := &domain.Trade{
		ID:           uuid.New().String(),
		AccountID:    tc.accountID,
		Broker:       "tastytrade",
		StrategyType: domain.StrategyUnknown,
		OpenedAt:     at,
	}
	require.NoError(t, repos.Trades.Create(ctx, trade))

	tx := domain.Transaction{
		ID:             uuid.New().String(),
		TradeID:        trade.ID,
		BrokerTxID:     uuid.New().String(),
		Broker:         "tastytrade",
		AccountID:      tc.accountID,
		Instrument:     inst,
		Action:         action,
		Quantity:       decimal.NewFromFloat(qty),
		FillPrice:      price,
		Fees:           fees,
		ExecutedAt:     at,
		PositionEffect: effect,
	}
	require.NoError(t, repos.Transactions.Create(ctx, &tx))
	return tx
}

// --- OpenLot ---

func TestOpenLot_Long(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	tc := seedCtx(t, ctx, repos)
	svc := newPositionService(repos)

	tx := makeTx(t, ctx, repos, tc, equityInst, domain.ActionBuy, 10, mustDecimal("450"), decimal.Zero, domain.PositionEffectOpening, baseTime)

	lot, err := svc.OpenLot(ctx, tx)
	require.NoError(t, err)

	assert.Equal(t, tx.ID, lot.OpeningTxID)
	assert.Equal(t, tx.TradeID, lot.TradeID)
	assert.True(t, lot.OpenQuantity.Equal(decimal.NewFromInt(10)), "long lot should have positive quantity")
	assert.True(t, lot.RemainingQuantity.Equal(decimal.NewFromInt(10)))
	assert.True(t, lot.OpenPrice.Equal(mustDecimal("450")))
	assert.Nil(t, lot.ClosedAt)

	// Verify persisted.
	persisted, err := repos.Positions.GetLot(ctx, lot.ID)
	require.NoError(t, err)
	assert.True(t, persisted.OpenQuantity.Equal(lot.OpenQuantity))
}

func TestOpenLot_Short(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	tc := seedCtx(t, ctx, repos)
	svc := newPositionService(repos)

	tx := makeTx(t, ctx, repos, tc, optionInst, domain.ActionSTO, 5, mustDecimal("1.50"), mustDecimal("3.25"), domain.PositionEffectOpening, baseTime)

	lot, err := svc.OpenLot(ctx, tx)
	require.NoError(t, err)

	assert.True(t, lot.OpenQuantity.Equal(decimal.NewFromInt(-5)), "short lot should have negative quantity")
	assert.True(t, lot.RemainingQuantity.Equal(decimal.NewFromInt(-5)))
}

// --- RefreshPosition ---

func TestRefreshPosition_CreatesPositionFromLots(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	tc := seedCtx(t, ctx, repos)
	svc := newPositionService(repos)

	instrumentID := equityInst.InstrumentID()

	tx1 := makeTx(t, ctx, repos, tc, equityInst, domain.ActionBuy, 5, mustDecimal("440"), decimal.Zero, domain.PositionEffectOpening, baseTime)
	tx2 := makeTx(t, ctx, repos, tc, equityInst, domain.ActionBuy, 3, mustDecimal("445"), decimal.Zero, domain.PositionEffectOpening, baseTime.Add(time.Hour))

	_, err := svc.OpenLot(ctx, tx1)
	require.NoError(t, err)
	_, err = svc.OpenLot(ctx, tx2)
	require.NoError(t, err)

	require.NoError(t, svc.RefreshPosition(ctx, tc.accountID, instrumentID))

	pos, err := repos.Positions.GetPosition(ctx, tc.accountID, instrumentID)
	require.NoError(t, err)

	// Quantity = 5 + 3 = 8
	assert.True(t, pos.Quantity.Equal(decimal.NewFromInt(8)))
	// CostBasis = 5×440 + 3×445 = 2200 + 1335 = 3535
	assert.True(t, pos.CostBasis.Equal(mustDecimal("3535")), "got %s", pos.CostBasis)
	assert.True(t, pos.RealizedPnL.IsZero())
	assert.Nil(t, pos.ClosedAt)
}

func TestRefreshPosition_ShortOption(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	tc := seedCtx(t, ctx, repos)
	svc := newPositionService(repos)

	instrumentID := optionInst.InstrumentID()

	tx := makeTx(t, ctx, repos, tc, optionInst, domain.ActionSTO, 5, mustDecimal("1.50"), mustDecimal("3.25"), domain.PositionEffectOpening, baseTime)
	_, err := svc.OpenLot(ctx, tx)
	require.NoError(t, err)

	require.NoError(t, svc.RefreshPosition(ctx, tc.accountID, instrumentID))

	pos, err := repos.Positions.GetPosition(ctx, tc.accountID, instrumentID)
	require.NoError(t, err)

	// Quantity = -5 (short)
	assert.True(t, pos.Quantity.Equal(decimal.NewFromInt(-5)))
	// CostBasis = -5 × 1.50 = -7.50
	assert.True(t, pos.CostBasis.Equal(mustDecimal("-7.50")), "got %s", pos.CostBasis)
}

func TestRefreshPosition_IdempotentOnSecondCall(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	tc := seedCtx(t, ctx, repos)
	svc := newPositionService(repos)

	instrumentID := equityInst.InstrumentID()

	tx := makeTx(t, ctx, repos, tc, equityInst, domain.ActionBuy, 10, mustDecimal("450"), decimal.Zero, domain.PositionEffectOpening, baseTime)
	_, err := svc.OpenLot(ctx, tx)
	require.NoError(t, err)

	require.NoError(t, svc.RefreshPosition(ctx, tc.accountID, instrumentID))
	require.NoError(t, svc.RefreshPosition(ctx, tc.accountID, instrumentID)) // second call must not duplicate

	pos, err := repos.Positions.GetPosition(ctx, tc.accountID, instrumentID)
	require.NoError(t, err)
	assert.True(t, pos.Quantity.Equal(decimal.NewFromInt(10)))
}

func TestRefreshPosition_NoLotsNoPosition(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionService(repos)

	// Should be a no-op, no error.
	require.NoError(t, svc.RefreshPosition(ctx, uuid.New().String(), equityInst.InstrumentID()))
}

// --- CloseLots ---

func TestCloseLots_SingleLotExact(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	tc := seedCtx(t, ctx, repos)
	svc := newPositionService(repos)

	instrumentID := optionInst.InstrumentID()

	// Open: STO 5 contracts at $1.50
	openTx := makeTx(t, ctx, repos, tc, optionInst, domain.ActionSTO, 5, mustDecimal("1.50"), mustDecimal("3.25"), domain.PositionEffectOpening, baseTime)
	lot, err := svc.OpenLot(ctx, openTx)
	require.NoError(t, err)
	require.NoError(t, svc.RefreshPosition(ctx, tc.accountID, instrumentID))

	// Close: BTC 5 contracts at $0.50
	closeTx := makeTx(t, ctx, repos, tc, optionInst, domain.ActionBTC, 5, mustDecimal("0.50"), mustDecimal("3.25"), domain.PositionEffectClosing, baseTime.Add(24*time.Hour))
	closings, err := svc.CloseLots(ctx, closeTx)
	require.NoError(t, err)
	require.Len(t, closings, 1)

	c := closings[0]
	assert.Equal(t, lot.ID, c.LotID)
	assert.True(t, c.ClosedQuantity.Equal(decimal.NewFromInt(5)))
	assert.True(t, c.ClosePrice.Equal(mustDecimal("0.50")))

	// P&L: short lot, (openPrice - closePrice) × qty × multiplier - closeFees - openFeesProportion
	// = (1.50 - 0.50) × 5 × 100 - 3.25 - 3.25 = 500 - 6.50 = 493.50
	expectedPnL := mustDecimal("493.50")
	assert.True(t, c.RealizedPnL.Equal(expectedPnL), "expected P&L %s, got %s", expectedPnL, c.RealizedPnL)

	// Lot should be fully closed.
	persisted, err := repos.Positions.GetLot(ctx, lot.ID)
	require.NoError(t, err)
	assert.True(t, persisted.RemainingQuantity.IsZero())
	assert.NotNil(t, persisted.ClosedAt)

	// Position realized P&L should be updated.
	pos, err := repos.Positions.GetPosition(ctx, tc.accountID, instrumentID)
	require.NoError(t, err)
	assert.True(t, pos.RealizedPnL.Equal(expectedPnL), "position realized_pnl mismatch")
}

func TestCloseLots_SingleLotPartial(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	tc := seedCtx(t, ctx, repos)
	svc := newPositionService(repos)

	instrumentID := optionInst.InstrumentID()

	// Open: STO 5 contracts at $2.00
	openTx := makeTx(t, ctx, repos, tc, optionInst, domain.ActionSTO, 5, mustDecimal("2.00"), mustDecimal("5.00"), domain.PositionEffectOpening, baseTime)
	lot, err := svc.OpenLot(ctx, openTx)
	require.NoError(t, err)
	require.NoError(t, svc.RefreshPosition(ctx, tc.accountID, instrumentID))

	// Close: BTC 3 contracts at $1.00
	closeTx := makeTx(t, ctx, repos, tc, optionInst, domain.ActionBTC, 3, mustDecimal("1.00"), mustDecimal("3.00"), domain.PositionEffectClosing, baseTime.Add(time.Hour))
	closings, err := svc.CloseLots(ctx, closeTx)
	require.NoError(t, err)
	require.Len(t, closings, 1)

	c := closings[0]
	assert.True(t, c.ClosedQuantity.Equal(decimal.NewFromInt(3)))

	// P&L: (2.00 - 1.00) × 3 × 100 - 3.00 - 5.00×(3/5) = 300 - 3.00 - 3.00 = 294.00
	expectedPnL := mustDecimal("294")
	assert.True(t, c.RealizedPnL.Equal(expectedPnL), "expected P&L %s, got %s", expectedPnL, c.RealizedPnL)

	// Lot should have -2 remaining (was -5, closed 3).
	persisted, err := repos.Positions.GetLot(ctx, lot.ID)
	require.NoError(t, err)
	assert.True(t, persisted.RemainingQuantity.Equal(decimal.NewFromInt(-2)))
	assert.Nil(t, persisted.ClosedAt, "lot should still be open")
}

func TestCloseLots_SpansTwoLots(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	tc := seedCtx(t, ctx, repos)
	svc := newPositionService(repos)

	instrumentID := optionInst.InstrumentID()

	// Open lot A: STO 3 contracts at $2.00, fees $3.00 (older)
	txA := makeTx(t, ctx, repos, tc, optionInst, domain.ActionSTO, 3, mustDecimal("2.00"), mustDecimal("3.00"), domain.PositionEffectOpening, baseTime)
	lotA, err := svc.OpenLot(ctx, txA)
	require.NoError(t, err)

	// Open lot B: STO 4 contracts at $1.50, fees $4.00 (newer)
	txB := makeTx(t, ctx, repos, tc, optionInst, domain.ActionSTO, 4, mustDecimal("1.50"), mustDecimal("4.00"), domain.PositionEffectOpening, baseTime.Add(time.Hour))
	lotB, err := svc.OpenLot(ctx, txB)
	require.NoError(t, err)

	require.NoError(t, svc.RefreshPosition(ctx, tc.accountID, instrumentID))

	// Close: BTC 5 contracts at $0.50, fees $5.00 — exhausts lot A (3) and partially closes lot B (2).
	closeTx := makeTx(t, ctx, repos, tc, optionInst, domain.ActionBTC, 5, mustDecimal("0.50"), mustDecimal("5.00"), domain.PositionEffectClosing, baseTime.Add(2*time.Hour))
	closings, err := svc.CloseLots(ctx, closeTx)
	require.NoError(t, err)
	require.Len(t, closings, 2, "should produce one closing per lot consumed")

	// First closing: lot A fully consumed (3 contracts).
	assert.Equal(t, lotA.ID, closings[0].LotID)
	assert.True(t, closings[0].ClosedQuantity.Equal(decimal.NewFromInt(3)))

	// Second closing: lot B partially consumed (2 contracts).
	assert.Equal(t, lotB.ID, closings[1].LotID)
	assert.True(t, closings[1].ClosedQuantity.Equal(decimal.NewFromInt(2)))

	// Lot A should be fully closed.
	persistedA, err := repos.Positions.GetLot(ctx, lotA.ID)
	require.NoError(t, err)
	assert.True(t, persistedA.RemainingQuantity.IsZero())
	assert.NotNil(t, persistedA.ClosedAt)

	// Lot B should have -2 remaining (was -4, closed 2).
	persistedB, err := repos.Positions.GetLot(ctx, lotB.ID)
	require.NoError(t, err)
	assert.True(t, persistedB.RemainingQuantity.Equal(decimal.NewFromInt(-2)))
	assert.Nil(t, persistedB.ClosedAt)

	// Position RealizedPnL must reflect both closings.
	// Lot A: (2.00 - 0.50) × 3 × 100 - 5.00×(3/5) - 3.00×(3/3) = 450 - 3.00 - 3.00 = 444
	// Lot B: (1.50 - 0.50) × 2 × 100 - 5.00×(2/5) - 4.00×(2/4) = 200 - 2.00 - 2.00 = 196
	// Total: 640
	pos, err := repos.Positions.GetPosition(ctx, tc.accountID, instrumentID)
	require.NoError(t, err)
	assert.True(t, pos.RealizedPnL.Equal(mustDecimal("640")), "got %s", pos.RealizedPnL)
}

func TestCloseLots_InsufficientOpenQuantity(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	tc := seedCtx(t, ctx, repos)
	svc := newPositionService(repos)

	instrumentID := optionInst.InstrumentID()

	// Open 2 contracts only.
	openTx := makeTx(t, ctx, repos, tc, optionInst, domain.ActionSTO, 2, mustDecimal("1.50"), mustDecimal("2.00"), domain.PositionEffectOpening, baseTime)
	lot, err := svc.OpenLot(ctx, openTx)
	require.NoError(t, err)
	require.NoError(t, svc.RefreshPosition(ctx, tc.accountID, instrumentID))

	// Try to close 5 contracts — should fail with no DB mutations.
	closeTx := makeTx(t, ctx, repos, tc, optionInst, domain.ActionBTC, 5, mustDecimal("0.50"), mustDecimal("5.00"), domain.PositionEffectClosing, baseTime.Add(time.Hour))
	_, err = svc.CloseLots(ctx, closeTx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient open quantity")

	// The lot must be unchanged — the pre-check fires before any DB writes.
	unchanged, err := repos.Positions.GetLot(ctx, lot.ID)
	require.NoError(t, err)
	assert.True(t, unchanged.RemainingQuantity.Equal(decimal.NewFromInt(-2)), "lot should be unmodified")
	assert.Nil(t, unchanged.ClosedAt, "lot should still be open")
}

func TestCloseLots_LongEquityPnL(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	tc := seedCtx(t, ctx, repos)
	svc := newPositionService(repos)

	instrumentID := equityInst.InstrumentID()

	// Open: BUY 10 shares at $450
	openTx := makeTx(t, ctx, repos, tc, equityInst, domain.ActionBuy, 10, mustDecimal("450"), mustDecimal("1.00"), domain.PositionEffectOpening, baseTime)
	_, err := svc.OpenLot(ctx, openTx)
	require.NoError(t, err)
	require.NoError(t, svc.RefreshPosition(ctx, tc.accountID, instrumentID))

	// Close: SELL 10 shares at $460
	closeTx := makeTx(t, ctx, repos, tc, equityInst, domain.ActionSell, 10, mustDecimal("460"), mustDecimal("1.00"), domain.PositionEffectClosing, baseTime.Add(time.Hour))
	closings, err := svc.CloseLots(ctx, closeTx)
	require.NoError(t, err)
	require.Len(t, closings, 1)

	// P&L: long equity, multiplier=1
	// (460 - 450) × 10 × 1 - 1.00 (close fees) - 1.00 (open fees, full proportion) = 100 - 2 = 98
	expectedPnL := mustDecimal("98")
	assert.True(t, closings[0].RealizedPnL.Equal(expectedPnL), "expected %s got %s", expectedPnL, closings[0].RealizedPnL)
}

func TestRefreshPosition_ClosedWhenQuantityZero(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	tc := seedCtx(t, ctx, repos)
	svc := newPositionService(repos)

	instrumentID := optionInst.InstrumentID()

	// Open and close all contracts.
	openTx := makeTx(t, ctx, repos, tc, optionInst, domain.ActionSTO, 5, mustDecimal("1.50"), mustDecimal("3.25"), domain.PositionEffectOpening, baseTime)
	_, err := svc.OpenLot(ctx, openTx)
	require.NoError(t, err)
	require.NoError(t, svc.RefreshPosition(ctx, tc.accountID, instrumentID))

	closeTx := makeTx(t, ctx, repos, tc, optionInst, domain.ActionBTC, 5, mustDecimal("0.50"), mustDecimal("3.25"), domain.PositionEffectClosing, baseTime.Add(24*time.Hour))
	_, err = svc.CloseLots(ctx, closeTx)
	require.NoError(t, err)

	require.NoError(t, svc.RefreshPosition(ctx, tc.accountID, instrumentID))

	pos, err := repos.Positions.GetPosition(ctx, tc.accountID, instrumentID)
	require.NoError(t, err)
	assert.True(t, pos.Quantity.IsZero())
	assert.NotNil(t, pos.ClosedAt, "position should be marked closed")
	// RealizedPnL must be preserved after close.
	assert.False(t, pos.RealizedPnL.IsZero(), "realized pnl should be non-zero after full close")
}
