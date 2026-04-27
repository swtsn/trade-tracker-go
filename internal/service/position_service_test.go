package service_test

import (
	"context"
	"io"
	"log/slog"
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

func newPositionSvc(repos *sqlite.Repos) *service.PositionService {
	return service.NewPositionService(repos.Positions, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// seedPositionTrade seeds a trade + instruments; returns the trade and transactions.
func seedPositionTrade(t *testing.T, ctx context.Context, repos *sqlite.Repos, acc *domain.Account, tradeID string, openedAt time.Time, txns ...domain.Transaction) {
	t.Helper()
	trade := &domain.Trade{
		ID:         tradeID,
		AccountID:  acc.ID,
		Broker:     acc.Broker,
		ExecutedAt: openedAt,
	}
	require.NoError(t, repos.Trades.Create(ctx, trade))
	for i := range txns {
		require.NoError(t, repos.Instruments.Upsert(ctx, &txns[i].Instrument))
		require.NoError(t, repos.Transactions.Create(ctx, &txns[i]))
	}
}

// TestPositionService_OpeningCreatesLotAndPosition: an opening transaction creates
// one lot and one position row keyed on the trade ID.
func TestPositionService_OpeningCreatesLotAndPosition(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	openedAt := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	tradeID := uuid.New().String()
	inst := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)
	tx := makeTransaction(tradeID, "pos-001", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 1, openedAt)
	tx.FillPrice = decimal.NewFromFloat(3.50)
	tx.Fees = decimal.NewFromFloat(0.65)

	seedPositionTrade(t, ctx, repos, acc, tradeID, openedAt, tx)
	chainID := seedPositionChain(t, ctx, repos, acc, tradeID)

	require.NoError(t, svc.ProcessTrade(ctx, tradeID, []domain.Transaction{tx}, chainID, domain.StrategyUnknown))

	// Lot created: short position (STO → negative open_quantity).
	lots, err := repos.Positions.ListOpenLotsByInstrument(ctx, acc.ID, inst.InstrumentID())
	require.NoError(t, err)
	require.Len(t, lots, 1)
	assert.True(t, decimal.NewFromInt(-1).Equal(lots[0].OpenQuantity), "STO lot must be negative")
	assert.True(t, decimal.NewFromFloat(3.50).Equal(lots[0].OpenPrice))

	// Position created for tradeID.
	pos, err := repos.Positions.GetPositionByTradeID(ctx, acc.ID, tradeID)
	require.NoError(t, err)
	assert.Equal(t, "SPY", pos.UnderlyingSymbol)
	// cost_basis = CashFlowSign(STO) × price × qty × multiplier − fees
	//            = +1 × 3.50 × 1 × 100 − 0.65 = 349.35
	expected := decimal.NewFromFloat(349.35)
	assert.True(t, expected.Equal(pos.CostBasis), "got %s", pos.CostBasis)
	assert.Nil(t, pos.ClosedAt)
}

// TestPositionService_StrategyBurnedInOnCreate verifies that the strategy from the
// opening trade is written to the position on creation, not StrategyUnknown.
func TestPositionService_StrategyBurnedInOnCreate(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	openedAt := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	tradeID := uuid.New().String()
	inst := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)
	tx := makeTransaction(tradeID, "strat-001", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 1, openedAt)

	seedPositionTrade(t, ctx, repos, acc, tradeID, openedAt, tx)
	chainID := seedPositionChain(t, ctx, repos, acc, tradeID)

	require.NoError(t, svc.ProcessTrade(ctx, tradeID, []domain.Transaction{tx}, chainID, domain.StrategySingle))

	pos, err := repos.Positions.GetPositionByTradeID(ctx, acc.ID, tradeID)
	require.NoError(t, err)
	assert.Equal(t, domain.StrategySingle, pos.StrategyType)
}

// TestPositionService_MultiLegOpeningAccumulatesCostBasis: two opening legs in the
// same trade accumulate into one position row.
func TestPositionService_MultiLegOpeningAccumulatesCostBasis(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	openedAt := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	tradeID := uuid.New().String()

	shortPut := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)
	longPut := makeEquityOption("SPY", 480, exp, domain.OptionTypePut)

	// Vertical spread: STO 490P at $3.00, BTO 480P at $1.50, 1 contract each, no fees.
	stoTx := makeTransaction(tradeID, "vt-001", acc.ID, acc.Broker, shortPut, domain.ActionSTO, domain.PositionEffectOpening, 1, openedAt)
	stoTx.FillPrice = decimal.NewFromFloat(3.00)
	stoTx.Fees = decimal.Zero

	btoTx := makeTransaction(tradeID, "vt-002", acc.ID, acc.Broker, longPut, domain.ActionBTO, domain.PositionEffectOpening, 1, openedAt)
	btoTx.FillPrice = decimal.NewFromFloat(1.50)
	btoTx.Fees = decimal.Zero

	seedPositionTrade(t, ctx, repos, acc, tradeID, openedAt, stoTx, btoTx)
	chainID := seedPositionChain(t, ctx, repos, acc, tradeID)

	// Import service sends closing-first; here both are opening so order doesn't matter.
	require.NoError(t, svc.ProcessTrade(ctx, tradeID, []domain.Transaction{stoTx, btoTx}, chainID, domain.StrategyVertical))

	pos, err := repos.Positions.GetPositionByTradeID(ctx, acc.ID, tradeID)
	require.NoError(t, err)

	// STO 490P: +1 × 3.00 × 1 × 100 = +300 credit
	// BTO 480P: -1 × 1.50 × 1 × 100 = -150 debit
	// net cost_basis = 300 − 150 = 150 (net credit)
	expected := decimal.NewFromFloat(150)
	assert.True(t, expected.Equal(pos.CostBasis), "got %s", pos.CostBasis)
	// Strategy burned in on first leg must survive the second-leg UpdatePosition call.
	assert.Equal(t, domain.StrategyVertical, pos.StrategyType)
}

// TestPositionService_ClosingFullLotRealizedPnL: closing a full lot computes correct
// realized P&L, stamps lot.closed_at, and stamps position.closed_at.
func TestPositionService_ClosingFullLotRealizedPnL(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)

	inst := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)

	// Trade 1: STO 1 contract at $3.50, fees $0.65.
	trade1ID := uuid.New().String()
	openTx := makeTransaction(trade1ID, "cl-001", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 1, t1)
	openTx.FillPrice = decimal.NewFromFloat(3.50)
	openTx.Fees = decimal.NewFromFloat(0.65)
	seedPositionTrade(t, ctx, repos, acc, trade1ID, t1, openTx)
	chainID := seedPositionChain(t, ctx, repos, acc, trade1ID)
	require.NoError(t, svc.ProcessTrade(ctx, trade1ID, []domain.Transaction{openTx}, chainID, domain.StrategyUnknown))

	// Trade 2: BTC 1 contract at $0.50, fees $0.65.
	trade2ID := uuid.New().String()
	closeTx := makeTransaction(trade2ID, "cl-002", acc.ID, acc.Broker, inst, domain.ActionBTC, domain.PositionEffectClosing, 1, t2)
	closeTx.FillPrice = decimal.NewFromFloat(0.50)
	closeTx.Fees = decimal.NewFromFloat(0.65)
	seedPositionTrade(t, ctx, repos, acc, trade2ID, t2, closeTx)
	require.NoError(t, svc.ProcessTrade(ctx, trade2ID, []domain.Transaction{closeTx}, chainID, domain.StrategyUnknown))

	// Realized P&L:
	// close_cf = CashFlowSign(BTC) × 0.50 × 1 × 100 = -1 × 50 = -50
	// open_cf  = CashFlowSign(STO) × 3.50 × 1 × 100 = +1 × 350 = 350
	// pnl = -50 + 350 - 0.65 (close fees) - 0.65 (open fees prorated 1/1) = 298.70
	lots, err := repos.Positions.ListOpenLotsByInstrument(ctx, acc.ID, inst.InstrumentID())
	require.NoError(t, err)
	assert.Empty(t, lots, "lot should be fully closed")

	closings, err := repos.Positions.ListLotClosings(ctx, findLotID(t, ctx, repos, acc.ID, inst.InstrumentID()))
	require.NoError(t, err)
	require.Len(t, closings, 1)
	expected := decimal.NewFromFloat(298.70)
	assert.True(t, expected.Equal(closings[0].RealizedPnL), "got %s", closings[0].RealizedPnL)

	// Position for trade1 should now be closed (all lots closed).
	pos, err := repos.Positions.GetPositionByTradeID(ctx, acc.ID, trade1ID)
	require.NoError(t, err)
	assert.NotNil(t, pos.ClosedAt)
	assert.True(t, expected.Equal(pos.RealizedPnL), "got %s", pos.RealizedPnL)
}

// TestPositionService_PartialClose: closing part of a lot leaves position open.
func TestPositionService_PartialClose(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	inst := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)

	// STO 2 contracts.
	trade1ID := uuid.New().String()
	openTx := makeTransaction(trade1ID, "pc-001", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 2, t1)
	openTx.FillPrice = decimal.NewFromFloat(3.00)
	openTx.Fees = decimal.NewFromFloat(1.30)
	seedPositionTrade(t, ctx, repos, acc, trade1ID, t1, openTx)
	chainID := seedPositionChain(t, ctx, repos, acc, trade1ID)
	require.NoError(t, svc.ProcessTrade(ctx, trade1ID, []domain.Transaction{openTx}, chainID, domain.StrategyUnknown))

	// BTC 1 contract (partial).
	trade2ID := uuid.New().String()
	closeTx := makeTransaction(trade2ID, "pc-002", acc.ID, acc.Broker, inst, domain.ActionBTC, domain.PositionEffectClosing, 1, t2)
	closeTx.FillPrice = decimal.NewFromFloat(1.00)
	closeTx.Fees = decimal.NewFromFloat(0.65)
	seedPositionTrade(t, ctx, repos, acc, trade2ID, t2, closeTx)
	require.NoError(t, svc.ProcessTrade(ctx, trade2ID, []domain.Transaction{closeTx}, chainID, domain.StrategyUnknown))

	// Lot should still have 1 contract remaining (short, so -1).
	lots, err := repos.Positions.ListOpenLotsByInstrument(ctx, acc.ID, inst.InstrumentID())
	require.NoError(t, err)
	require.Len(t, lots, 1)
	assert.True(t, decimal.NewFromInt(-1).Equal(lots[0].RemainingQuantity))

	// Position for trade1 should still be open.
	pos, err := repos.Positions.GetPositionByTradeID(ctx, acc.ID, trade1ID)
	require.NoError(t, err)
	assert.Nil(t, pos.ClosedAt)
}

// TestPositionService_FIFOOrder: when multiple lots exist, they are closed oldest-first.
func TestPositionService_FIFOOrder(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	inst := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)

	// Trade 1: STO 1 at $3.00.
	trade1ID := uuid.New().String()
	tx1 := makeTransaction(trade1ID, "fifo-001", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 1, t1)
	tx1.FillPrice = decimal.NewFromFloat(3.00)
	tx1.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade1ID, t1, tx1)
	chainID1 := seedPositionChain(t, ctx, repos, acc, trade1ID)
	require.NoError(t, svc.ProcessTrade(ctx, trade1ID, []domain.Transaction{tx1}, chainID1, domain.StrategyUnknown))

	// Trade 2: STO 1 at $4.00 (newer lot).
	trade2ID := uuid.New().String()
	tx2 := makeTransaction(trade2ID, "fifo-002", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 1, t2)
	tx2.FillPrice = decimal.NewFromFloat(4.00)
	tx2.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade2ID, t2, tx2)
	chainID2 := seedPositionChain(t, ctx, repos, acc, trade2ID)
	require.NoError(t, svc.ProcessTrade(ctx, trade2ID, []domain.Transaction{tx2}, chainID2, domain.StrategyUnknown))

	// Trade 3: BTC 1 — should close the oldest lot (price $3.00) first.
	trade3ID := uuid.New().String()
	closeTx := makeTransaction(trade3ID, "fifo-003", acc.ID, acc.Broker, inst, domain.ActionBTC, domain.PositionEffectClosing, 1, t3)
	closeTx.FillPrice = decimal.NewFromFloat(0.50)
	closeTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade3ID, t3, closeTx)
	require.NoError(t, svc.ProcessTrade(ctx, trade3ID, []domain.Transaction{closeTx}, chainID1, domain.StrategyUnknown))

	// One lot should remain (the newer $4.00 one).
	lots, err := repos.Positions.ListOpenLotsByInstrument(ctx, acc.ID, inst.InstrumentID())
	require.NoError(t, err)
	require.Len(t, lots, 1)
	assert.True(t, decimal.NewFromFloat(4.00).Equal(lots[0].OpenPrice), "newest lot should remain; got %s", lots[0].OpenPrice)

	// The $3.00 lot's closing should reflect P&L from closing that lot.
	// close_cf = -1 × 0.50 × 1 × 100 = -50; open_cf = +1 × 3.00 × 1 × 100 = 300; pnl = 250
	lotID := findLotID(t, ctx, repos, acc.ID, inst.InstrumentID())
	closings, err := repos.Positions.ListLotClosings(ctx, lotID)
	require.NoError(t, err)
	require.Len(t, closings, 1)
	expected := decimal.NewFromFloat(250)
	assert.True(t, expected.Equal(closings[0].RealizedPnL), "lot closing pnl: got %s", closings[0].RealizedPnL)

	// Position for chainID1 (the closed lot's chain) should reflect the same P&L.
	pos1, err := repos.Positions.GetPositionByChainID(ctx, acc.ID, chainID1)
	require.NoError(t, err)
	assert.True(t, expected.Equal(pos1.RealizedPnL), "position realized_pnl: got %s", pos1.RealizedPnL)
}

// TestPositionService_ExpirationAtZero: expiration is treated as a close at price=0.
func TestPositionService_ExpirationAtZero(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp := time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 18, 16, 0, 0, 0, time.UTC) // expiration day
	inst := makeEquityOption("SPY", 400, exp, domain.OptionTypePut)

	// STO 1 at $2.00.
	trade1ID := uuid.New().String()
	openTx := makeTransaction(trade1ID, "exp-001", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 1, t1)
	openTx.FillPrice = decimal.NewFromFloat(2.00)
	openTx.Fees = decimal.NewFromFloat(0.65)
	seedPositionTrade(t, ctx, repos, acc, trade1ID, t1, openTx)
	chainID := seedPositionChain(t, ctx, repos, acc, trade1ID)
	require.NoError(t, svc.ProcessTrade(ctx, trade1ID, []domain.Transaction{openTx}, chainID, domain.StrategyUnknown))

	// EXPIRATION at price 0.
	trade2ID := uuid.New().String()
	expTx := makeTransaction(trade2ID, "exp-002", acc.ID, acc.Broker, inst, domain.ActionExpiration, domain.PositionEffectClosing, 1, t2)
	expTx.FillPrice = decimal.Zero
	expTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade2ID, t2, expTx)
	require.NoError(t, svc.ProcessTrade(ctx, trade2ID, []domain.Transaction{expTx}, chainID, domain.StrategyUnknown))

	// Position closed; P&L = close_cf + open_cf - fees
	// close_cf = 0 (price 0); open_cf = +1 × 2.00 × 1 × 100 = 200; fees = 0 + 0.65
	// pnl = 200 - 0.65 = 199.35
	pos, err := repos.Positions.GetPositionByTradeID(ctx, acc.ID, trade1ID)
	require.NoError(t, err)
	assert.NotNil(t, pos.ClosedAt)
	expected := decimal.NewFromFloat(199.35)
	assert.True(t, expected.Equal(pos.RealizedPnL), "got %s", pos.RealizedPnL)
}

// TestPositionService_EquityCreatesStockPosition: equity (stock) transactions are handled by
// processEquityTrade inside PositionService — no lots are created; a stock position is.
func TestPositionService_EquityCreatesStockPosition(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	inst := makeEquity("AAPL")

	tradeID := uuid.New().String()
	buyTx := makeTransaction(tradeID, "eq-stock-001", acc.ID, acc.Broker, inst, domain.ActionBuy, domain.PositionEffectOpening, 10, t1)
	buyTx.FillPrice = decimal.NewFromFloat(170)
	buyTx.Fees = decimal.NewFromFloat(0.65)
	seedPositionTrade(t, ctx, repos, acc, tradeID, t1, buyTx)
	chainID := seedPositionChain(t, ctx, repos, acc, tradeID, domain.StrategyStock)

	err := svc.ProcessTrade(ctx, tradeID, []domain.Transaction{buyTx}, chainID, domain.StrategyStock)
	require.NoError(t, err)

	// No lots — equity WAC does not use the lot table.
	lots, err := repos.Positions.ListOpenLotsByInstrument(ctx, acc.ID, inst.InstrumentID())
	require.NoError(t, err)
	assert.Empty(t, lots)

	// A stock position should have been created with WAC fields set.
	positions, err := repos.Positions.ListPositions(ctx, acc.ID, false, false)
	require.NoError(t, err)
	require.Len(t, positions, 1)
	pos := positions[0]
	assert.Equal(t, domain.StrategyStock, pos.StrategyType)
	assert.True(t, decimal.NewFromFloat(10).Equal(pos.NetQuantity), "net_quantity: %s", pos.NetQuantity)
	// avg_cost = (10*170 + 0.65) / 10 = 170.065
	expectedAvg := decimal.NewFromFloat(170.065)
	assert.True(t, expectedAvg.Equal(pos.AvgCostPerShare), "avg_cost_per_share: %s", pos.AvgCostPerShare)
}

// TestPositionService_EquityWACMultipleBuys: buying at different prices accumulates WAC correctly.
func TestPositionService_EquityWACMultipleBuys(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)
	inst := makeEquity("AAPL")

	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)

	// First buy: 10 shares at $100, no fees.
	tradeID1 := uuid.New().String()
	buy1 := makeTransaction(tradeID1, "wb1-001", acc.ID, acc.Broker, inst, domain.ActionBuy, domain.PositionEffectOpening, 10, t1)
	buy1.FillPrice = decimal.NewFromFloat(100)
	buy1.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID1, t1, buy1)
	chainID := seedPositionChain(t, ctx, repos, acc, tradeID1, domain.StrategyStock)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID1, []domain.Transaction{buy1}, chainID, domain.StrategyStock))

	// Second buy: 10 shares at $120, no fees. New WAC = (10×100 + 10×120) / 20 = 110.
	tradeID2 := uuid.New().String()
	buy2 := makeTransaction(tradeID2, "wb1-002", acc.ID, acc.Broker, inst, domain.ActionBuy, domain.PositionEffectOpening, 10, t2)
	buy2.FillPrice = decimal.NewFromFloat(120)
	buy2.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID2, t2, buy2)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID2, []domain.Transaction{buy2}, chainID, domain.StrategyStock))

	pos, err := repos.Positions.GetPositionByChainID(ctx, acc.ID, chainID)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromFloat(20).Equal(pos.NetQuantity), "qty: %s", pos.NetQuantity)
	assert.True(t, decimal.NewFromFloat(110).Equal(pos.AvgCostPerShare), "wac: %s", pos.AvgCostPerShare)
}

// TestPositionService_EquityPartialSellThenRebuy: partial sell updates position; rebuy updates WAC.
func TestPositionService_EquityPartialSellThenRebuy(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)
	inst := makeEquity("AAPL")

	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)

	// Buy 10 shares at $100.
	tradeID1 := uuid.New().String()
	buy1 := makeTransaction(tradeID1, "psr-001", acc.ID, acc.Broker, inst, domain.ActionBuy, domain.PositionEffectOpening, 10, t1)
	buy1.FillPrice = decimal.NewFromFloat(100)
	buy1.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID1, t1, buy1)
	chainID := seedPositionChain(t, ctx, repos, acc, tradeID1, domain.StrategyStock)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID1, []domain.Transaction{buy1}, chainID, domain.StrategyStock))

	// Sell 5 shares at $110: realized = 5×110 − 5×100 = +50.
	tradeID2 := uuid.New().String()
	sell1 := makeTransaction(tradeID2, "psr-002", acc.ID, acc.Broker, inst, domain.ActionSell, domain.PositionEffectClosing, 5, t2)
	sell1.FillPrice = decimal.NewFromFloat(110)
	sell1.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID2, t2, sell1)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID2, []domain.Transaction{sell1}, chainID, domain.StrategyStock))

	pos, err := repos.Positions.GetPositionByChainID(ctx, acc.ID, chainID)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromFloat(5).Equal(pos.NetQuantity), "qty after partial sell: %s", pos.NetQuantity)
	assert.True(t, decimal.NewFromFloat(50).Equal(pos.RealizedPnL), "realized after partial sell: %s", pos.RealizedPnL)
	assert.Nil(t, pos.ClosedAt, "position still open after partial sell")

	// Buy 5 more shares at $90. New WAC = (5×100 + 5×90) / 10 = 95.
	tradeID3 := uuid.New().String()
	buy2 := makeTransaction(tradeID3, "psr-003", acc.ID, acc.Broker, inst, domain.ActionBuy, domain.PositionEffectOpening, 5, t3)
	buy2.FillPrice = decimal.NewFromFloat(90)
	buy2.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID3, t3, buy2)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID3, []domain.Transaction{buy2}, chainID, domain.StrategyStock))

	pos, err = repos.Positions.GetPositionByChainID(ctx, acc.ID, chainID)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromFloat(10).Equal(pos.NetQuantity), "qty after rebuy: %s", pos.NetQuantity)
	assert.True(t, decimal.NewFromFloat(95).Equal(pos.AvgCostPerShare), "wac after rebuy: %s", pos.AvgCostPerShare)
}

// TestPositionService_EquityMultiCyclePnL: sell-all → re-buy → sell-all accumulates both cycles' P&L.
func TestPositionService_EquityMultiCyclePnL(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)
	inst := makeEquity("AAPL")

	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)
	t4 := time.Date(2026, 4, 4, 10, 0, 0, 0, time.UTC)

	// Cycle 1: buy 10 @ $100, sell all @ $110. Realized = 10×(110−100) = +100.
	tradeID1 := uuid.New().String()
	buy1 := makeTransaction(tradeID1, "mc-001", acc.ID, acc.Broker, inst, domain.ActionBuy, domain.PositionEffectOpening, 10, t1)
	buy1.FillPrice = decimal.NewFromFloat(100)
	buy1.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID1, t1, buy1)
	chainID := seedPositionChain(t, ctx, repos, acc, tradeID1, domain.StrategyStock)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID1, []domain.Transaction{buy1}, chainID, domain.StrategyStock))

	tradeID2 := uuid.New().String()
	sell1 := makeTransaction(tradeID2, "mc-002", acc.ID, acc.Broker, inst, domain.ActionSell, domain.PositionEffectClosing, 10, t2)
	sell1.FillPrice = decimal.NewFromFloat(110)
	sell1.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID2, t2, sell1)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID2, []domain.Transaction{sell1}, chainID, domain.StrategyStock))

	pos, err := repos.Positions.GetPositionByChainID(ctx, acc.ID, chainID)
	require.NoError(t, err)
	assert.NotNil(t, pos.ClosedAt, "position should be closed after sell-all")
	assert.True(t, decimal.NewFromFloat(100).Equal(pos.RealizedPnL), "cycle 1 pnl: %s", pos.RealizedPnL)

	// Cycle 2: re-buy 5 @ $90, sell all @ $95. Realized = 5×(95−90) = +25. Cumulative = +125.
	tradeID3 := uuid.New().String()
	buy2 := makeTransaction(tradeID3, "mc-003", acc.ID, acc.Broker, inst, domain.ActionBuy, domain.PositionEffectOpening, 5, t3)
	buy2.FillPrice = decimal.NewFromFloat(90)
	buy2.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID3, t3, buy2)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID3, []domain.Transaction{buy2}, chainID, domain.StrategyStock))

	tradeID4 := uuid.New().String()
	sell2 := makeTransaction(tradeID4, "mc-004", acc.ID, acc.Broker, inst, domain.ActionSell, domain.PositionEffectClosing, 5, t4)
	sell2.FillPrice = decimal.NewFromFloat(95)
	sell2.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID4, t4, sell2)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID4, []domain.Transaction{sell2}, chainID, domain.StrategyStock))

	pos, err = repos.Positions.GetPositionByChainID(ctx, acc.ID, chainID)
	require.NoError(t, err)
	assert.NotNil(t, pos.ClosedAt, "position should be closed after second sell-all")
	assert.True(t, decimal.NewFromFloat(125).Equal(pos.RealizedPnL), "cumulative pnl: %s", pos.RealizedPnL)
}

// TestPositionService_EquitySellExceedsHeld: selling more than held returns an error.
func TestPositionService_EquitySellExceedsHeld(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)
	inst := makeEquity("AAPL")

	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)

	tradeID1 := uuid.New().String()
	buy := makeTransaction(tradeID1, "seh-001", acc.ID, acc.Broker, inst, domain.ActionBuy, domain.PositionEffectOpening, 5, t1)
	buy.FillPrice = decimal.NewFromFloat(100)
	buy.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID1, t1, buy)
	chainID := seedPositionChain(t, ctx, repos, acc, tradeID1, domain.StrategyStock)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID1, []domain.Transaction{buy}, chainID, domain.StrategyStock))

	tradeID2 := uuid.New().String()
	sell := makeTransaction(tradeID2, "seh-002", acc.ID, acc.Broker, inst, domain.ActionSell, domain.PositionEffectClosing, 10, t2)
	sell.FillPrice = decimal.NewFromFloat(110)
	sell.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID2, t2, sell)
	err := svc.ProcessTrade(ctx, tradeID2, []domain.Transaction{sell}, chainID, domain.StrategyStock)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only 5 held")
}

// TestPositionService_EquityZeroQuantityReturnsError: a buy with zero quantity returns an error
// rather than panicking on division-by-zero in the WAC formula.
func TestPositionService_EquityZeroQuantityReturnsError(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)
	inst := makeEquity("AAPL")

	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)

	tradeID := uuid.New().String()
	buy := makeTransaction(tradeID, "zq-001", acc.ID, acc.Broker, inst, domain.ActionBuy, domain.PositionEffectOpening, 0, t1)
	buy.FillPrice = decimal.NewFromFloat(100)
	buy.Fees = decimal.Zero
	buy.Quantity = decimal.Zero // explicitly zero
	seedPositionTrade(t, ctx, repos, acc, tradeID, t1, buy)
	chainID := seedPositionChain(t, ctx, repos, acc, tradeID, domain.StrategyStock)
	err := svc.ProcessTrade(ctx, tradeID, []domain.Transaction{buy}, chainID, domain.StrategyStock)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zero quantity")
}

// TestPositionService_EquityShortOpen: selling short creates a negative-quantity position.
func TestPositionService_EquityShortOpen(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)
	inst := makeEquity("TSLA")

	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)

	tradeID := uuid.New().String()
	shortTx := makeTransaction(tradeID, "short-001", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 10, t1)
	shortTx.FillPrice = decimal.NewFromFloat(200)
	shortTx.Fees = decimal.NewFromFloat(1.00)
	seedPositionTrade(t, ctx, repos, acc, tradeID, t1, shortTx)
	chainID := seedPositionChain(t, ctx, repos, acc, tradeID, domain.StrategyStock)

	require.NoError(t, svc.ProcessTrade(ctx, tradeID, []domain.Transaction{shortTx}, chainID, domain.StrategyStock))

	pos, err := repos.Positions.GetPositionByChainID(ctx, acc.ID, chainID)
	require.NoError(t, err)
	assert.Equal(t, domain.StrategyStock, pos.StrategyType)
	// net_quantity is negative for a short position.
	assert.True(t, decimal.NewFromFloat(-10).Equal(pos.NetQuantity), "net_quantity: %s", pos.NetQuantity)
	// avg_cost = fill_price - fees/qty = 200 - 1/10 = 199.9
	expectedAvg := decimal.NewFromFloat(199.9)
	assert.True(t, expectedAvg.Equal(pos.AvgCostPerShare), "avg_cost: %s", pos.AvgCostPerShare)
	assert.Nil(t, pos.ClosedAt)
}

// TestPositionService_EquityShortOpenAccumulation: adding to an existing short blends WAC correctly.
func TestPositionService_EquityShortOpenAccumulation(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)
	inst := makeEquity("TSLA")

	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)

	// First short: 10 shares at $200, no fees. avg = 200.
	tradeID1 := uuid.New().String()
	short1 := makeTransaction(tradeID1, "soa-001", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 10, t1)
	short1.FillPrice = decimal.NewFromFloat(200)
	short1.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID1, t1, short1)
	chainID := seedPositionChain(t, ctx, repos, acc, tradeID1, domain.StrategyStock)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID1, []domain.Transaction{short1}, chainID, domain.StrategyStock))

	// Second short: 5 more shares at $210, no fees.
	// WAC = (10×200 + 5×210) / 15 = (2000 + 1050) / 15 = 203.333...
	tradeID2 := uuid.New().String()
	short2 := makeTransaction(tradeID2, "soa-002", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 5, t2)
	short2.FillPrice = decimal.NewFromFloat(210)
	short2.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID2, t2, short2)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID2, []domain.Transaction{short2}, chainID, domain.StrategyStock))

	pos, err := repos.Positions.GetPositionByChainID(ctx, acc.ID, chainID)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromFloat(-15).Equal(pos.NetQuantity), "net_quantity: %s", pos.NetQuantity)
	// 3050 / 15 = 203.3333...
	expectedAvg := decimal.NewFromFloat(3050).Div(decimal.NewFromFloat(15))
	assert.True(t, expectedAvg.Equal(pos.AvgCostPerShare), "wac: %s (expected %s)", pos.AvgCostPerShare, expectedAvg)
}

// TestPositionService_EquityShortMultiCycle: cover-all → re-short → cover-all preserves realized P&L.
func TestPositionService_EquityShortMultiCycle(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)
	inst := makeEquity("TSLA")

	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)
	t4 := time.Date(2026, 4, 4, 10, 0, 0, 0, time.UTC)

	// Cycle 1: short 10 @ $200, cover all @ $180. Realized = 10×(200−180) = +200.
	tradeID1 := uuid.New().String()
	short1 := makeTransaction(tradeID1, "smc-001", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 10, t1)
	short1.FillPrice = decimal.NewFromFloat(200)
	short1.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID1, t1, short1)
	chainID := seedPositionChain(t, ctx, repos, acc, tradeID1, domain.StrategyStock)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID1, []domain.Transaction{short1}, chainID, domain.StrategyStock))

	tradeID2 := uuid.New().String()
	cover1 := makeTransaction(tradeID2, "smc-002", acc.ID, acc.Broker, inst, domain.ActionBTC, domain.PositionEffectClosing, 10, t2)
	cover1.FillPrice = decimal.NewFromFloat(180)
	cover1.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID2, t2, cover1)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID2, []domain.Transaction{cover1}, chainID, domain.StrategyStock))

	pos, err := repos.Positions.GetPositionByChainID(ctx, acc.ID, chainID)
	require.NoError(t, err)
	assert.NotNil(t, pos.ClosedAt, "position should be closed after covering all")
	assert.True(t, decimal.NewFromFloat(200).Equal(pos.RealizedPnL), "cycle 1 pnl: %s", pos.RealizedPnL)

	// Cycle 2: re-short 5 @ $190, cover all @ $170. Realized = 5×(190−170) = +100. Cumulative = +300.
	tradeID3 := uuid.New().String()
	short2 := makeTransaction(tradeID3, "smc-003", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 5, t3)
	short2.FillPrice = decimal.NewFromFloat(190)
	short2.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID3, t3, short2)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID3, []domain.Transaction{short2}, chainID, domain.StrategyStock))

	pos, err = repos.Positions.GetPositionByChainID(ctx, acc.ID, chainID)
	require.NoError(t, err)
	assert.Nil(t, pos.ClosedAt, "ClosedAt must be cleared on re-short")
	assert.True(t, decimal.NewFromFloat(-5).Equal(pos.NetQuantity), "re-shorted: %s", pos.NetQuantity)

	tradeID4 := uuid.New().String()
	cover2 := makeTransaction(tradeID4, "smc-004", acc.ID, acc.Broker, inst, domain.ActionBTC, domain.PositionEffectClosing, 5, t4)
	cover2.FillPrice = decimal.NewFromFloat(170)
	cover2.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID4, t4, cover2)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID4, []domain.Transaction{cover2}, chainID, domain.StrategyStock))

	pos, err = repos.Positions.GetPositionByChainID(ctx, acc.ID, chainID)
	require.NoError(t, err)
	assert.NotNil(t, pos.ClosedAt, "position should be closed after second cover-all")
	assert.True(t, decimal.NewFromFloat(300).Equal(pos.RealizedPnL), "cumulative pnl: %s", pos.RealizedPnL)
}

// TestPositionService_EquityShortCoverProfit: covering a short at a lower price realizes profit.
func TestPositionService_EquityShortCoverProfit(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)
	inst := makeEquity("TSLA")

	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC)

	// Short 10 shares at $200, no fees.
	tradeID1 := uuid.New().String()
	shortTx := makeTransaction(tradeID1, "sc-001", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 10, t1)
	shortTx.FillPrice = decimal.NewFromFloat(200)
	shortTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID1, t1, shortTx)
	chainID := seedPositionChain(t, ctx, repos, acc, tradeID1, domain.StrategyStock)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID1, []domain.Transaction{shortTx}, chainID, domain.StrategyStock))

	// Cover 10 shares at $150, no fees: realized = 10 × (200 − 150) = +500.
	tradeID2 := uuid.New().String()
	coverTx := makeTransaction(tradeID2, "sc-002", acc.ID, acc.Broker, inst, domain.ActionBTC, domain.PositionEffectClosing, 10, t2)
	coverTx.FillPrice = decimal.NewFromFloat(150)
	coverTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID2, t2, coverTx)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID2, []domain.Transaction{coverTx}, chainID, domain.StrategyStock))

	pos, err := repos.Positions.GetPositionByChainID(ctx, acc.ID, chainID)
	require.NoError(t, err)
	assert.True(t, decimal.Zero.Equal(pos.NetQuantity), "fully covered: %s", pos.NetQuantity)
	assert.NotNil(t, pos.ClosedAt)
	assert.True(t, decimal.NewFromFloat(500).Equal(pos.RealizedPnL), "realized pnl: %s", pos.RealizedPnL)
}

// TestPositionService_EquityShortCoverLoss: covering a short at a higher price realizes a loss.
func TestPositionService_EquityShortCoverLoss(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)
	inst := makeEquity("TSLA")

	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC)

	tradeID1 := uuid.New().String()
	shortTx := makeTransaction(tradeID1, "scl-001", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 5, t1)
	shortTx.FillPrice = decimal.NewFromFloat(100)
	shortTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID1, t1, shortTx)
	chainID := seedPositionChain(t, ctx, repos, acc, tradeID1, domain.StrategyStock)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID1, []domain.Transaction{shortTx}, chainID, domain.StrategyStock))

	// Cover at $120: realized = 5 × (100 − 120) = −100.
	tradeID2 := uuid.New().String()
	coverTx := makeTransaction(tradeID2, "scl-002", acc.ID, acc.Broker, inst, domain.ActionBTC, domain.PositionEffectClosing, 5, t2)
	coverTx.FillPrice = decimal.NewFromFloat(120)
	coverTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID2, t2, coverTx)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID2, []domain.Transaction{coverTx}, chainID, domain.StrategyStock))

	pos, err := repos.Positions.GetPositionByChainID(ctx, acc.ID, chainID)
	require.NoError(t, err)
	assert.NotNil(t, pos.ClosedAt)
	assert.True(t, decimal.NewFromFloat(-100).Equal(pos.RealizedPnL), "realized pnl: %s", pos.RealizedPnL)
}

// TestPositionService_EquityShortPartialCover: covering only part of a short leaves the position open.
func TestPositionService_EquityShortPartialCover(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)
	inst := makeEquity("TSLA")

	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC)

	tradeID1 := uuid.New().String()
	shortTx := makeTransaction(tradeID1, "spc-001", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 10, t1)
	shortTx.FillPrice = decimal.NewFromFloat(100)
	shortTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID1, t1, shortTx)
	chainID := seedPositionChain(t, ctx, repos, acc, tradeID1, domain.StrategyStock)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID1, []domain.Transaction{shortTx}, chainID, domain.StrategyStock))

	// Cover 4 of 10: realized = 4 × (100 − 80) = +80.
	tradeID2 := uuid.New().String()
	coverTx := makeTransaction(tradeID2, "spc-002", acc.ID, acc.Broker, inst, domain.ActionBTC, domain.PositionEffectClosing, 4, t2)
	coverTx.FillPrice = decimal.NewFromFloat(80)
	coverTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID2, t2, coverTx)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID2, []domain.Transaction{coverTx}, chainID, domain.StrategyStock))

	pos, err := repos.Positions.GetPositionByChainID(ctx, acc.ID, chainID)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromFloat(-6).Equal(pos.NetQuantity), "remaining short: %s", pos.NetQuantity)
	assert.Nil(t, pos.ClosedAt, "position still open")
	assert.True(t, decimal.NewFromFloat(80).Equal(pos.RealizedPnL), "partial realized: %s", pos.RealizedPnL)
}

// TestPositionService_EquityShortCoverExceedsHeld: covering more than held returns an error.
func TestPositionService_EquityShortCoverExceedsHeld(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)
	inst := makeEquity("TSLA")

	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC)

	tradeID1 := uuid.New().String()
	shortTx := makeTransaction(tradeID1, "sce-001", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 5, t1)
	shortTx.FillPrice = decimal.NewFromFloat(100)
	shortTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID1, t1, shortTx)
	chainID := seedPositionChain(t, ctx, repos, acc, tradeID1, domain.StrategyStock)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID1, []domain.Transaction{shortTx}, chainID, domain.StrategyStock))

	tradeID2 := uuid.New().String()
	coverTx := makeTransaction(tradeID2, "sce-002", acc.ID, acc.Broker, inst, domain.ActionBTC, domain.PositionEffectClosing, 10, t2)
	coverTx.FillPrice = decimal.NewFromFloat(90)
	coverTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, tradeID2, t2, coverTx)
	err := svc.ProcessTrade(ctx, tradeID2, []domain.Transaction{coverTx}, chainID, domain.StrategyStock)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only 5 short")
}

// TestPositionService_RollDoesNotClosePosition: a mixed trade (roll) should close the
// old lot but not the position, since new lots are added in the same trade.
func TestPositionService_RollDoesNotClosePosition(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp1 := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	exp2 := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)

	putInst1 := makeEquityOption("SPY", 490, exp1, domain.OptionTypePut)
	putInst2 := makeEquityOption("SPY", 480, exp2, domain.OptionTypePut)

	// Trade 1: STO 1 contract.
	trade1ID := uuid.New().String()
	openTx := makeTransaction(trade1ID, "roll-001", acc.ID, acc.Broker, putInst1, domain.ActionSTO, domain.PositionEffectOpening, 1, t1)
	openTx.FillPrice = decimal.NewFromFloat(3.00)
	openTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade1ID, t1, openTx)
	chainID1 := seedPositionChain(t, ctx, repos, acc, trade1ID)
	require.NoError(t, svc.ProcessTrade(ctx, trade1ID, []domain.Transaction{openTx}, chainID1, domain.StrategyUnknown))

	// Trade 2: roll — close old + open new (mixed). The roll starts a new chain position.
	trade2ID := uuid.New().String()
	closeTx := makeTransaction(trade2ID, "roll-002", acc.ID, acc.Broker, putInst1, domain.ActionBTC, domain.PositionEffectClosing, 1, t2)
	closeTx.FillPrice = decimal.NewFromFloat(1.00)
	closeTx.Fees = decimal.Zero
	openTx2 := makeTransaction(trade2ID, "roll-003", acc.ID, acc.Broker, putInst2, domain.ActionSTO, domain.PositionEffectOpening, 1, t2)
	openTx2.FillPrice = decimal.NewFromFloat(2.50)
	openTx2.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade2ID, t2, closeTx, openTx2)
	chainID2 := seedPositionChain(t, ctx, repos, acc, trade2ID)

	// ProcessTrade receives closing-first order.
	require.NoError(t, svc.ProcessTrade(ctx, trade2ID, []domain.Transaction{closeTx, openTx2}, chainID2, domain.StrategyUnknown))

	// Old lot (putInst1) fully closed.
	oldLots, err := repos.Positions.ListOpenLotsByInstrument(ctx, acc.ID, putInst1.InstrumentID())
	require.NoError(t, err)
	assert.Empty(t, oldLots)

	// New lot (putInst2) created and open.
	newLots, err := repos.Positions.ListOpenLotsByInstrument(ctx, acc.ID, putInst2.InstrumentID())
	require.NoError(t, err)
	require.Len(t, newLots, 1)

	// Position for trade1 should be closed (its single lot is closed; no openings in this trade's original processing).
	pos1, err := repos.Positions.GetPositionByTradeID(ctx, acc.ID, trade1ID)
	require.NoError(t, err)
	assert.NotNil(t, pos1.ClosedAt, "original position should be closed once its lots are all closed")

	// Position for trade2 created and open.
	pos2, err := repos.Positions.GetPositionByTradeID(ctx, acc.ID, trade2ID)
	require.NoError(t, err)
	assert.Nil(t, pos2.ClosedAt, "rolled position should be open")
	// Roll trade is BTC+STO — classifier returns Unknown; re-derive from live lots is a future backlog item.
	assert.Equal(t, domain.StrategyUnknown, pos2.StrategyType)
}

// TestPositionService_OpenPositionsListing: ListOpenPositions returns only open positions.
func TestPositionService_OpenPositionsListing(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	inst := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)

	// Open trade.
	trade1ID := uuid.New().String()
	openTx := makeTransaction(trade1ID, "opl-001", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 1, t1)
	openTx.FillPrice = decimal.NewFromFloat(3.00)
	openTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade1ID, t1, openTx)
	chainID1 := seedPositionChain(t, ctx, repos, acc, trade1ID)
	require.NoError(t, svc.ProcessTrade(ctx, trade1ID, []domain.Transaction{openTx}, chainID1, domain.StrategyUnknown))

	// Close it.
	trade2ID := uuid.New().String()
	closeTx := makeTransaction(trade2ID, "opl-002", acc.ID, acc.Broker, inst, domain.ActionBTC, domain.PositionEffectClosing, 1, t2)
	closeTx.FillPrice = decimal.NewFromFloat(0.50)
	closeTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade2ID, t2, closeTx)
	require.NoError(t, svc.ProcessTrade(ctx, trade2ID, []domain.Transaction{closeTx}, chainID1, domain.StrategyUnknown))

	// Equity trades are handled by StockPositionService; PositionService sees zero open positions.
	open, err := repos.Positions.ListPositions(ctx, acc.ID, true, false)
	require.NoError(t, err)
	require.Empty(t, open, "SPY options position was closed; no open options positions remain")
}

// TestPositionService_ChainedPositionPnL: when a lot has a chain_id the position is
// found via GetPositionByChainID and P&L is accumulated correctly.
func TestPositionService_ChainedPositionPnL(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	inst := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)

	// Trade 1: STO 1 contract at $3.50.
	trade1ID := uuid.New().String()
	openTx := makeTransaction(trade1ID, "chp-001", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 1, t1)
	openTx.FillPrice = decimal.NewFromFloat(3.50)
	openTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade1ID, t1, openTx)
	chainID := seedPositionChain(t, ctx, repos, acc, trade1ID)
	require.NoError(t, svc.ProcessTrade(ctx, trade1ID, []domain.Transaction{openTx}, chainID, domain.StrategyUnknown))

	// Trade 2: BTC 1 contract at $0.50.
	trade2ID := uuid.New().String()
	closeTx := makeTransaction(trade2ID, "chp-002", acc.ID, acc.Broker, inst, domain.ActionBTC, domain.PositionEffectClosing, 1, t2)
	closeTx.FillPrice = decimal.NewFromFloat(0.50)
	closeTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade2ID, t2, closeTx)
	require.NoError(t, svc.ProcessTrade(ctx, trade2ID, []domain.Transaction{closeTx}, chainID, domain.StrategyUnknown))

	// P&L: close_cf = -1 × 0.50 × 1 × 100 = -50; open_cf = +1 × 3.50 × 1 × 100 = 350; pnl = 300
	pos, err := repos.Positions.GetPositionByTradeID(ctx, acc.ID, trade1ID)
	require.NoError(t, err)
	assert.NotNil(t, pos.ClosedAt, "position should be stamped closed")
	expected := decimal.NewFromFloat(300)
	assert.True(t, expected.Equal(pos.RealizedPnL), "got %s", pos.RealizedPnL)
}

// TestPositionService_ChainedPositionOpenLotsCheckedByChain: when a chain spans two
// trades, closing the first trade's lot must not prematurely close the position — the
// open-lot check must use chain_id, not originating_trade_id.
func TestPositionService_ChainedPositionOpenLotsCheckedByChain(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp1 := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	exp2 := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)

	inst1 := makeEquityOption("SPY", 490, exp1, domain.OptionTypePut) // original leg
	inst2 := makeEquityOption("SPY", 480, exp2, domain.OptionTypePut) // rolled-to leg

	// Both trades belong to the same chain. GetPositionByChainID returns the anchor
	// (oldest) position, so P&L and the open-lot check always target position1.

	// Trade 1: STO 1 contract of inst1.
	trade1ID := uuid.New().String()
	openTx1 := makeTransaction(trade1ID, "coc-001", acc.ID, acc.Broker, inst1, domain.ActionSTO, domain.PositionEffectOpening, 1, t1)
	openTx1.FillPrice = decimal.NewFromFloat(3.00)
	openTx1.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade1ID, t1, openTx1)
	chainID := seedPositionChain(t, ctx, repos, acc, trade1ID)
	require.NoError(t, svc.ProcessTrade(ctx, trade1ID, []domain.Transaction{openTx1}, chainID, domain.StrategyUnknown))

	// Trade 2: STO 1 contract of inst2 (extension of the same chain).
	trade2ID := uuid.New().String()
	openTx2 := makeTransaction(trade2ID, "coc-002", acc.ID, acc.Broker, inst2, domain.ActionSTO, domain.PositionEffectOpening, 1, t2)
	openTx2.FillPrice = decimal.NewFromFloat(2.50)
	openTx2.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade2ID, t2, openTx2)
	require.NoError(t, svc.ProcessTrade(ctx, trade2ID, []domain.Transaction{openTx2}, chainID, domain.StrategyUnknown))

	// Trade 3: BTC 1 contract of inst1 — closes trade1's lot only.
	trade3ID := uuid.New().String()
	closeTx := makeTransaction(trade3ID, "coc-003", acc.ID, acc.Broker, inst1, domain.ActionBTC, domain.PositionEffectClosing, 1, t3)
	closeTx.FillPrice = decimal.NewFromFloat(0.50)
	closeTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade3ID, t3, closeTx)
	require.NoError(t, svc.ProcessTrade(ctx, trade3ID, []domain.Transaction{closeTx}, chainID, domain.StrategyUnknown))

	// inst1 lot is now closed; inst2 lot (from trade2) is still open.
	// The chain's position must NOT be stamped closed — ListOpenLotsByChain still
	// finds inst2's lot. Without the fix (using ListOpenLotsByTrade instead), the
	// check would see 0 lots for trade1 and incorrectly stamp ClosedAt.
	pos1, err := repos.Positions.GetPositionByTradeID(ctx, acc.ID, trade1ID)
	require.NoError(t, err)
	assert.Nil(t, pos1.ClosedAt, "position should stay open while chain has an open lot from trade2")

	// P&L from closing inst1 should be accumulated on position1.
	// close_cf = -1 × 0.50 × 1 × 100 = -50; open_cf = +1 × 3.00 × 1 × 100 = 300; pnl = 250
	expected := decimal.NewFromFloat(250)
	assert.True(t, expected.Equal(pos1.RealizedPnL), "got %s", pos1.RealizedPnL)
}

// seedPositionChain creates a chain anchored to anchorTradeID and returns its ID.
// Used in position service tests to satisfy the FK constraint on positions.chain_id.
// An optional strategyType may be provided as the last argument; defaults to StrategyUnknown.
func seedPositionChain(t *testing.T, ctx context.Context, repos *sqlite.Repos, acc *domain.Account, anchorTradeID string, strategyType ...domain.StrategyType) string {
	t.Helper()
	st := domain.StrategyUnknown
	if len(strategyType) > 0 {
		st = strategyType[0]
	}
	chainID := uuid.New().String()
	chain := &domain.Chain{
		ID:               chainID,
		AccountID:        acc.ID,
		UnderlyingSymbol: "SPY",
		OriginalTradeID:  anchorTradeID,
		CreatedAt:        time.Now().UTC(),
		StrategyType:     st,
	}
	require.NoError(t, repos.Chains.CreateChain(ctx, chain))
	return chainID
}

// --- helpers ---

// findLotID returns the ID of a closed lot for the given instrument (for retrieving closings).
// remaining_quantity is stored as TEXT via decimal.String(); '0' is the canonical zero value.
// The repository API does not expose a method to query by closed state, so raw SQL is required here.
func findLotID(t *testing.T, ctx context.Context, repos *sqlite.Repos, accountID, instrumentID string) string {
	t.Helper()
	rows, err := repos.DB().QueryContext(ctx,
		`SELECT id FROM position_lots WHERE account_id = ? AND instrument_id = ? AND remaining_quantity = '0'`,
		accountID, instrumentID,
	)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	require.True(t, rows.Next(), "expected at least one closed lot")
	var id string
	require.NoError(t, rows.Scan(&id))
	return id
}
