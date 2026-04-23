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

func newAnalyticsSvc(repos *sqlite.Repos) *service.AnalyticsService {
	return service.NewAnalyticsService(repos.DB())
}

// openAndClose is a helper that creates an opening trade+transaction, processes it, then
// creates a closing trade+transaction and processes it. Returns the opening tradeID and chainID.
//
// open/close prices and fees are in dollars. All transactions are for equity options with
// multiplier 100 unless inst is an equity.
func openAndClose(
	t *testing.T,
	ctx context.Context,
	repos *sqlite.Repos,
	svc *service.PositionService,
	acc *domain.Account,
	inst domain.Instrument,
	openBTID, closeBTID string,
	openAction domain.Action,
	closeAction domain.Action,
	qty, openPrice, openFees, closePrice, closeFees float64,
	openedAt, closedAt time.Time,
) (tradeID string, chainID string) {
	t.Helper()

	tradeID = uuid.New().String()
	openTx := makeTransaction(tradeID, openBTID, acc.ID, acc.Broker, inst, openAction, domain.PositionEffectOpening, qty, openedAt)
	openTx.FillPrice = decimal.NewFromFloat(openPrice)
	openTx.Fees = decimal.NewFromFloat(openFees)
	seedPositionTrade(t, ctx, repos, acc, tradeID, openedAt, openTx)
	chainID = seedPositionChain(t, ctx, repos, acc, tradeID)
	require.NoError(t, svc.ProcessTrade(ctx, tradeID, []domain.Transaction{openTx}, chainID, domain.StrategyUnknown))

	closeTxTradeID := uuid.New().String()
	closeTx := makeTransaction(closeTxTradeID, closeBTID, acc.ID, acc.Broker, inst, closeAction, domain.PositionEffectClosing, qty, closedAt)
	closeTx.FillPrice = decimal.NewFromFloat(closePrice)
	closeTx.Fees = decimal.NewFromFloat(closeFees)
	seedPositionTrade(t, ctx, repos, acc, closeTxTradeID, closedAt, closeTx)
	require.NoError(t, svc.ProcessTrade(ctx, closeTxTradeID, []domain.Transaction{closeTx}, chainID, domain.StrategyUnknown))

	return tradeID, chainID
}

// TestAnalyticsService_EmptyRange: querying an account with no closed positions returns zero values.
func TestAnalyticsService_EmptyRange(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	acc := seedImportAccount(t, ctx, repos)
	asvc := newAnalyticsSvc(repos)

	allTime := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Now().UTC()

	pnl, err := asvc.GetSymbolPnL(ctx, acc.ID, "SPY", allTime, now)
	require.NoError(t, err)
	assert.True(t, decimal.Zero.Equal(pnl), "no closings → zero symbol pnl")

	summary, err := asvc.GetPnLSummary(ctx, acc.ID, allTime, now)
	require.NoError(t, err)
	assert.Equal(t, int32(0), summary.PositionsClosed)
	assert.True(t, decimal.Zero.Equal(summary.WinRate))
	assert.True(t, decimal.Zero.Equal(summary.RealizedPnL))

	stats, err := asvc.GetStrategyPerformance(ctx, acc.ID, allTime, now)
	require.NoError(t, err)
	assert.Empty(t, stats)
}

// TestAnalyticsService_WinningPosition: a single winning trade → win rate 1.0 and correct P&L.
//
// STO 1 SPY 490P at $3.50, BTC at $0.50, fees $0 on both sides.
// open_cf = +1 × 3.50 × 1 × 100 = 350
// close_cf = -1 × 0.50 × 1 × 100 = -50
// realized_pnl = 350 − 50 = 300
func TestAnalyticsService_WinningPosition(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	acc := seedImportAccount(t, ctx, repos)
	psvc := newPositionSvc(repos)
	asvc := newAnalyticsSvc(repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	openedAt := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	closedAt := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	inst := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)

	openAndClose(t, ctx, repos, psvc, acc, inst,
		"an-win-001", "an-win-002",
		domain.ActionSTO, domain.ActionBTC,
		1, 3.50, 0, 0.50, 0,
		openedAt, closedAt,
	)

	allTime := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	future := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

	pnl, err := asvc.GetSymbolPnL(ctx, acc.ID, "SPY", allTime, future)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(300).Equal(pnl), "got %s", pnl)

	summary, err := asvc.GetPnLSummary(ctx, acc.ID, allTime, future)
	require.NoError(t, err)
	assert.Equal(t, int32(1), summary.PositionsClosed)
	assert.True(t, decimal.NewFromFloat(1).Equal(summary.WinRate), "win rate should be 1.0")
	assert.True(t, decimal.NewFromInt(300).Equal(summary.RealizedPnL), "got %s", summary.RealizedPnL)
}

// TestAnalyticsService_LosingPosition: a single losing trade → win rate 0, negative P&L.
//
// STO 1 SPY 490P at $1.00, BTC at $3.00, fees $0.
// open_cf = +100; close_cf = -300 → pnl = -200
func TestAnalyticsService_LosingPosition(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	acc := seedImportAccount(t, ctx, repos)
	psvc := newPositionSvc(repos)
	asvc := newAnalyticsSvc(repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	openedAt := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	closedAt := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	inst := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)

	openAndClose(t, ctx, repos, psvc, acc, inst,
		"an-loss-001", "an-loss-002",
		domain.ActionSTO, domain.ActionBTC,
		1, 1.00, 0, 3.00, 0,
		openedAt, closedAt,
	)

	allTime := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	future := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

	summary, err := asvc.GetPnLSummary(ctx, acc.ID, allTime, future)
	require.NoError(t, err)
	assert.Equal(t, int32(1), summary.PositionsClosed)
	assert.True(t, decimal.Zero.Equal(summary.WinRate), "win rate should be 0")
	assert.True(t, summary.RealizedPnL.IsNegative(), "P&L should be negative, got %s", summary.RealizedPnL)
}

// TestAnalyticsService_MixedWinRate: two positions — one win, one loss → win rate 0.5.
func TestAnalyticsService_MixedWinRate(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	acc := seedImportAccount(t, ctx, repos)
	psvc := newPositionSvc(repos)
	asvc := newAnalyticsSvc(repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)

	instA := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)
	instB := makeEquityOption("SPY", 480, exp, domain.OptionTypePut)

	// Win: STO at $3.50, BTC at $0.50 → pnl = +300
	openAndClose(t, ctx, repos, psvc, acc, instA,
		"mix-001", "mix-002",
		domain.ActionSTO, domain.ActionBTC,
		1, 3.50, 0, 0.50, 0,
		t1, t2,
	)
	// Loss: STO at $1.00, BTC at $3.00 → pnl = -200
	openAndClose(t, ctx, repos, psvc, acc, instB,
		"mix-003", "mix-004",
		domain.ActionSTO, domain.ActionBTC,
		1, 1.00, 0, 3.00, 0,
		t1, t3,
	)

	allTime := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	future := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

	summary, err := asvc.GetPnLSummary(ctx, acc.ID, allTime, future)
	require.NoError(t, err)
	assert.Equal(t, int32(2), summary.PositionsClosed)
	half := decimal.NewFromFloat(0.5)
	assert.True(t, half.Equal(summary.WinRate), "got %s", summary.WinRate)
	// Net P&L: 300 − 200 = 100
	assert.True(t, decimal.NewFromInt(100).Equal(summary.RealizedPnL), "got %s", summary.RealizedPnL)
}

// TestAnalyticsService_GetSymbolPnL_SymbolFilter: P&L for different underlyings is reported
// separately; querying one symbol does not include the other.
func TestAnalyticsService_GetSymbolPnL_SymbolFilter(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	acc := seedImportAccount(t, ctx, repos)
	psvc := newPositionSvc(repos)
	asvc := newAnalyticsSvc(repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	openedAt := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	closedAt := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)

	spyInst := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)
	qqqInst := makeEquityOption("QQQ", 450, exp, domain.OptionTypePut)

	// SPY: win $300
	openAndClose(t, ctx, repos, psvc, acc, spyInst,
		"sym-001", "sym-002",
		domain.ActionSTO, domain.ActionBTC,
		1, 3.50, 0, 0.50, 0,
		openedAt, closedAt,
	)
	// QQQ: loss $-200
	openAndClose(t, ctx, repos, psvc, acc, qqqInst,
		"sym-003", "sym-004",
		domain.ActionSTO, domain.ActionBTC,
		1, 1.00, 0, 3.00, 0,
		openedAt, closedAt,
	)

	allTime := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	future := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

	spyPnL, err := asvc.GetSymbolPnL(ctx, acc.ID, "SPY", allTime, future)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(300).Equal(spyPnL), "SPY pnl got %s", spyPnL)

	qqqPnL, err := asvc.GetSymbolPnL(ctx, acc.ID, "QQQ", allTime, future)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(-200).Equal(qqqPnL), "QQQ pnl got %s", qqqPnL)
}

// TestAnalyticsService_DateRangeFilter: positions closed outside the query range are excluded.
func TestAnalyticsService_DateRangeFilter(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	acc := seedImportAccount(t, ctx, repos)
	psvc := newPositionSvc(repos)
	asvc := newAnalyticsSvc(repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	openedAt := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	closedInRange := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)
	closedOutOfRange := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

	instA := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)
	instB := makeEquityOption("SPY", 480, exp, domain.OptionTypePut)

	// Closed within range → included in query.
	openAndClose(t, ctx, repos, psvc, acc, instA,
		"dr-001", "dr-002",
		domain.ActionSTO, domain.ActionBTC,
		1, 3.50, 0, 0.50, 0,
		openedAt, closedInRange,
	)
	// Closed outside range → must NOT appear.
	openAndClose(t, ctx, repos, psvc, acc, instB,
		"dr-003", "dr-004",
		domain.ActionSTO, domain.ActionBTC,
		1, 2.00, 0, 0.50, 0,
		openedAt, closedOutOfRange,
	)

	from := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 4, 30, 23, 59, 59, 0, time.UTC)

	summary, err := asvc.GetPnLSummary(ctx, acc.ID, from, to)
	require.NoError(t, err)
	assert.Equal(t, int32(1), summary.PositionsClosed, "only the April position should be counted")
	assert.True(t, decimal.NewFromInt(1).Equal(summary.WinRate))

	pnl, err := asvc.GetSymbolPnL(ctx, acc.ID, "SPY", from, to)
	require.NoError(t, err)
	// Only the April close: +300.
	assert.True(t, decimal.NewFromInt(300).Equal(pnl), "got %s", pnl)
}

// TestAnalyticsService_GetStrategyPerformance: closed positions are grouped by their originating
// trade's strategy_type; metrics are correct for each group.
func TestAnalyticsService_GetStrategyPerformance(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	acc := seedImportAccount(t, ctx, repos)
	psvc := newPositionSvc(repos)
	asvc := newAnalyticsSvc(repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)

	instA := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)
	instB := makeEquityOption("SPY", 480, exp, domain.OptionTypePut)

	// CSP trade: win $300.
	cspTradeID, _ := openAndClose(t, ctx, repos, psvc, acc, instA,
		"strat-001", "strat-002",
		domain.ActionSTO, domain.ActionBTC,
		1, 3.50, 0, 0.50, 0,
		t1, t2,
	)
	require.NoError(t, repos.Trades.UpdateStrategy(ctx, cspTradeID, domain.StrategySingle))

	// Vertical trade: loss $-200.
	vertTradeID, _ := openAndClose(t, ctx, repos, psvc, acc, instB,
		"strat-003", "strat-004",
		domain.ActionSTO, domain.ActionBTC,
		1, 1.00, 0, 3.00, 0,
		t1, t2,
	)
	require.NoError(t, repos.Trades.UpdateStrategy(ctx, vertTradeID, domain.StrategyVertical))

	allTime := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	future := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

	stats, err := asvc.GetStrategyPerformance(ctx, acc.ID, allTime, future)
	require.NoError(t, err)
	require.Len(t, stats, 2)

	// Build a lookup by strategy type for assertion order-independence.
	byType := make(map[domain.StrategyType]service.StrategyStats)
	for _, s := range stats {
		byType[s.StrategyType] = s
	}

	single := byType[domain.StrategySingle]
	assert.Equal(t, 1, single.Count)
	assert.True(t, decimal.NewFromInt(1).Equal(single.WinRate), "Single win rate got %s", single.WinRate)
	assert.True(t, decimal.NewFromInt(300).Equal(single.TotalPnL), "Single total pnl got %s", single.TotalPnL)
	assert.True(t, decimal.NewFromInt(300).Equal(single.AveragePnL), "Single avg pnl got %s", single.AveragePnL)

	vert := byType[domain.StrategyVertical]
	assert.Equal(t, 1, vert.Count)
	assert.True(t, decimal.Zero.Equal(vert.WinRate), "Vertical win rate got %s", vert.WinRate)
	assert.True(t, decimal.NewFromInt(-200).Equal(vert.TotalPnL), "Vertical total pnl got %s", vert.TotalPnL)
}

// TestAnalyticsService_CloseFees: GetPnLSummary includes closing fees.
func TestAnalyticsService_CloseFees(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	acc := seedImportAccount(t, ctx, repos)
	psvc := newPositionSvc(repos)
	asvc := newAnalyticsSvc(repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	openedAt := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	closedAt := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	inst := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)

	// STO at $3.50 (open fees $0.65), BTC at $0.50 (close fees $0.65).
	// realized_pnl = 350 − 50 − 0.65 (close) − 0.65 (open, prorated 1/1) = 298.70
	// CloseFees reported = 0.65
	openAndClose(t, ctx, repos, psvc, acc, inst,
		"fee-001", "fee-002",
		domain.ActionSTO, domain.ActionBTC,
		1, 3.50, 0.65, 0.50, 0.65,
		openedAt, closedAt,
	)

	allTime := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	future := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

	summary, err := asvc.GetPnLSummary(ctx, acc.ID, allTime, future)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromFloat(0.65).Equal(summary.CloseFees), "close fees got %s", summary.CloseFees)
	expected := decimal.NewFromFloat(298.70)
	assert.True(t, expected.Equal(summary.RealizedPnL), "realized pnl got %s", summary.RealizedPnL)
}
