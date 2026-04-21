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
		ID:           tradeID,
		AccountID:    acc.ID,
		Broker:       acc.Broker,
		StrategyType: domain.StrategyUnknown,
		OpenedAt:     openedAt,
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

	require.NoError(t, svc.ProcessTrade(ctx, tradeID, []domain.Transaction{tx}, chainID))

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
	require.NoError(t, svc.ProcessTrade(ctx, tradeID, []domain.Transaction{stoTx, btoTx}, chainID))

	pos, err := repos.Positions.GetPositionByTradeID(ctx, acc.ID, tradeID)
	require.NoError(t, err)

	// STO 490P: +1 × 3.00 × 1 × 100 = +300 credit
	// BTO 480P: -1 × 1.50 × 1 × 100 = -150 debit
	// net cost_basis = 300 − 150 = 150 (net credit)
	expected := decimal.NewFromFloat(150)
	assert.True(t, expected.Equal(pos.CostBasis), "got %s", pos.CostBasis)
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
	require.NoError(t, svc.ProcessTrade(ctx, trade1ID, []domain.Transaction{openTx}, chainID))

	// Trade 2: BTC 1 contract at $0.50, fees $0.65.
	trade2ID := uuid.New().String()
	closeTx := makeTransaction(trade2ID, "cl-002", acc.ID, acc.Broker, inst, domain.ActionBTC, domain.PositionEffectClosing, 1, t2)
	closeTx.FillPrice = decimal.NewFromFloat(0.50)
	closeTx.Fees = decimal.NewFromFloat(0.65)
	seedPositionTrade(t, ctx, repos, acc, trade2ID, t2, closeTx)
	require.NoError(t, svc.ProcessTrade(ctx, trade2ID, []domain.Transaction{closeTx}, chainID))

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
	require.NoError(t, svc.ProcessTrade(ctx, trade1ID, []domain.Transaction{openTx}, chainID))

	// BTC 1 contract (partial).
	trade2ID := uuid.New().String()
	closeTx := makeTransaction(trade2ID, "pc-002", acc.ID, acc.Broker, inst, domain.ActionBTC, domain.PositionEffectClosing, 1, t2)
	closeTx.FillPrice = decimal.NewFromFloat(1.00)
	closeTx.Fees = decimal.NewFromFloat(0.65)
	seedPositionTrade(t, ctx, repos, acc, trade2ID, t2, closeTx)
	require.NoError(t, svc.ProcessTrade(ctx, trade2ID, []domain.Transaction{closeTx}, chainID))

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
	require.NoError(t, svc.ProcessTrade(ctx, trade1ID, []domain.Transaction{tx1}, chainID1))

	// Trade 2: STO 1 at $4.00 (newer lot).
	trade2ID := uuid.New().String()
	tx2 := makeTransaction(trade2ID, "fifo-002", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 1, t2)
	tx2.FillPrice = decimal.NewFromFloat(4.00)
	tx2.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade2ID, t2, tx2)
	chainID2 := seedPositionChain(t, ctx, repos, acc, trade2ID)
	require.NoError(t, svc.ProcessTrade(ctx, trade2ID, []domain.Transaction{tx2}, chainID2))

	// Trade 3: BTC 1 — should close the oldest lot (price $3.00) first.
	trade3ID := uuid.New().String()
	closeTx := makeTransaction(trade3ID, "fifo-003", acc.ID, acc.Broker, inst, domain.ActionBTC, domain.PositionEffectClosing, 1, t3)
	closeTx.FillPrice = decimal.NewFromFloat(0.50)
	closeTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade3ID, t3, closeTx)
	require.NoError(t, svc.ProcessTrade(ctx, trade3ID, []domain.Transaction{closeTx}, chainID1))

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
	require.NoError(t, svc.ProcessTrade(ctx, trade1ID, []domain.Transaction{openTx}, chainID))

	// EXPIRATION at price 0.
	trade2ID := uuid.New().String()
	expTx := makeTransaction(trade2ID, "exp-002", acc.ID, acc.Broker, inst, domain.ActionExpiration, domain.PositionEffectClosing, 1, t2)
	expTx.FillPrice = decimal.Zero
	expTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade2ID, t2, expTx)
	require.NoError(t, svc.ProcessTrade(ctx, trade2ID, []domain.Transaction{expTx}, chainID))

	// Position closed; P&L = close_cf + open_cf - fees
	// close_cf = 0 (price 0); open_cf = +1 × 2.00 × 1 × 100 = 200; fees = 0 + 0.65
	// pnl = 200 - 0.65 = 199.35
	pos, err := repos.Positions.GetPositionByTradeID(ctx, acc.ID, trade1ID)
	require.NoError(t, err)
	assert.NotNil(t, pos.ClosedAt)
	expected := decimal.NewFromFloat(199.35)
	assert.True(t, expected.Equal(pos.RealizedPnL), "got %s", pos.RealizedPnL)
}

// TestPositionService_LongPositionPnL: BTO then SELL realizes correct P&L.
func TestPositionService_LongPositionPnL(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newPositionSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	inst := makeEquity("AAPL")

	// BUY 10 shares at $170, fees $0.
	trade1ID := uuid.New().String()
	buyTx := makeTransaction(trade1ID, "long-001", acc.ID, acc.Broker, inst, domain.ActionBuy, domain.PositionEffectOpening, 10, t1)
	buyTx.FillPrice = decimal.NewFromFloat(170)
	buyTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade1ID, t1, buyTx)
	chainID := seedPositionChain(t, ctx, repos, acc, trade1ID)
	require.NoError(t, svc.ProcessTrade(ctx, trade1ID, []domain.Transaction{buyTx}, chainID))

	// SELL 10 shares at $180, fees $0.
	trade2ID := uuid.New().String()
	sellTx := makeTransaction(trade2ID, "long-002", acc.ID, acc.Broker, inst, domain.ActionSell, domain.PositionEffectClosing, 10, t2)
	sellTx.FillPrice = decimal.NewFromFloat(180)
	sellTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade2ID, t2, sellTx)
	require.NoError(t, svc.ProcessTrade(ctx, trade2ID, []domain.Transaction{sellTx}, chainID))

	// P&L: close_cf = +1 × 180 × 10 × 1 = 1800
	//       open_cf = -1 × 170 × 10 × 1 = -1700
	//       pnl = 1800 - 1700 = 100
	pos, err := repos.Positions.GetPositionByTradeID(ctx, acc.ID, trade1ID)
	require.NoError(t, err)
	assert.NotNil(t, pos.ClosedAt)
	expected := decimal.NewFromFloat(100)
	assert.True(t, expected.Equal(pos.RealizedPnL), "got %s", pos.RealizedPnL)
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
	require.NoError(t, svc.ProcessTrade(ctx, trade1ID, []domain.Transaction{openTx}, chainID1))

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
	require.NoError(t, svc.ProcessTrade(ctx, trade2ID, []domain.Transaction{closeTx, openTx2}, chainID2))

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
	require.NoError(t, svc.ProcessTrade(ctx, trade1ID, []domain.Transaction{openTx}, chainID1))

	// Close it.
	trade2ID := uuid.New().String()
	closeTx := makeTransaction(trade2ID, "opl-002", acc.ID, acc.Broker, inst, domain.ActionBTC, domain.PositionEffectClosing, 1, t2)
	closeTx.FillPrice = decimal.NewFromFloat(0.50)
	closeTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade2ID, t2, closeTx)
	require.NoError(t, svc.ProcessTrade(ctx, trade2ID, []domain.Transaction{closeTx}, chainID1))

	// Open another trade (AAPL equity).
	aaplInst := makeEquity("AAPL")
	trade3ID := uuid.New().String()
	buyTx := makeTransaction(trade3ID, "opl-003", acc.ID, acc.Broker, aaplInst, domain.ActionBuy, domain.PositionEffectOpening, 10, t1)
	buyTx.FillPrice = decimal.NewFromFloat(170)
	buyTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade3ID, t1, buyTx)
	chainID3 := seedPositionChain(t, ctx, repos, acc, trade3ID)
	require.NoError(t, svc.ProcessTrade(ctx, trade3ID, []domain.Transaction{buyTx}, chainID3))

	open, err := repos.Positions.ListPositions(ctx, acc.ID, true, false)
	require.NoError(t, err)
	require.Len(t, open, 1, "only AAPL position should be open")
	assert.Equal(t, "AAPL", open[0].UnderlyingSymbol)
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
	require.NoError(t, svc.ProcessTrade(ctx, trade1ID, []domain.Transaction{openTx}, chainID))

	// Trade 2: BTC 1 contract at $0.50.
	trade2ID := uuid.New().String()
	closeTx := makeTransaction(trade2ID, "chp-002", acc.ID, acc.Broker, inst, domain.ActionBTC, domain.PositionEffectClosing, 1, t2)
	closeTx.FillPrice = decimal.NewFromFloat(0.50)
	closeTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade2ID, t2, closeTx)
	require.NoError(t, svc.ProcessTrade(ctx, trade2ID, []domain.Transaction{closeTx}, chainID))

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
	require.NoError(t, svc.ProcessTrade(ctx, trade1ID, []domain.Transaction{openTx1}, chainID))

	// Trade 2: STO 1 contract of inst2 (extension of the same chain).
	trade2ID := uuid.New().String()
	openTx2 := makeTransaction(trade2ID, "coc-002", acc.ID, acc.Broker, inst2, domain.ActionSTO, domain.PositionEffectOpening, 1, t2)
	openTx2.FillPrice = decimal.NewFromFloat(2.50)
	openTx2.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade2ID, t2, openTx2)
	require.NoError(t, svc.ProcessTrade(ctx, trade2ID, []domain.Transaction{openTx2}, chainID))

	// Trade 3: BTC 1 contract of inst1 — closes trade1's lot only.
	trade3ID := uuid.New().String()
	closeTx := makeTransaction(trade3ID, "coc-003", acc.ID, acc.Broker, inst1, domain.ActionBTC, domain.PositionEffectClosing, 1, t3)
	closeTx.FillPrice = decimal.NewFromFloat(0.50)
	closeTx.Fees = decimal.Zero
	seedPositionTrade(t, ctx, repos, acc, trade3ID, t3, closeTx)
	require.NoError(t, svc.ProcessTrade(ctx, trade3ID, []domain.Transaction{closeTx}, chainID))

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
func seedPositionChain(t *testing.T, ctx context.Context, repos *sqlite.Repos, acc *domain.Account, anchorTradeID string) string {
	t.Helper()
	chainID := uuid.New().String()
	chain := &domain.Chain{
		ID:               chainID,
		AccountID:        acc.ID,
		UnderlyingSymbol: "SPY",
		OriginalTradeID:  anchorTradeID,
		CreatedAt:        time.Now().UTC(),
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
