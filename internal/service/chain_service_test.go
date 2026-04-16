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

func newChainSvc(repos *sqlite.Repos) *service.ChainService {
	return service.NewChainService(repos.Chains, repos.Trades, repos.Transactions, repos.Positions)
}

// TestChainService_OpeningOnlyStartsChain: an opening-only trade creates a chain.
func TestChainService_OpeningOnlyStartsChain(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newChainSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	openedAt := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)

	tradeID := uuid.New().String()
	putInst := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)
	callInst := makeEquityOption("SPY", 510, exp, domain.OptionTypeCall)
	putTx := makeTransaction(tradeID, "btx-001", acc.ID, acc.Broker, putInst, domain.ActionSTO, domain.PositionEffectOpening, 2, openedAt)
	callTx := makeTransaction(tradeID, "btx-002", acc.ID, acc.Broker, callInst, domain.ActionSTO, domain.PositionEffectOpening, 2, openedAt)

	seedChainTrade(t, ctx, repos, acc, tradeID, openedAt, putTx, callTx)

	require.NoError(t, svc.DetectChains(ctx, acc.ID))

	chains, err := repos.Chains.ListChainsByAccount(ctx, acc.ID, false)
	require.NoError(t, err)
	require.Len(t, chains, 1)
	assert.Equal(t, tradeID, chains[0].OriginalTradeID)
	assert.Equal(t, "SPY", chains[0].UnderlyingSymbol)
	assert.Nil(t, chains[0].ClosedAt)
}

// TestChainService_MixedTradeExtendsChain: a roll (mixed trade) creates a ChainLink.
// The chain remains open.
func TestChainService_MixedTradeExtendsChain(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newChainSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp1 := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	exp2 := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)

	// Trade 1: open a short put.
	trade1ID := uuid.New().String()
	putInst1 := makeEquityOption("SPY", 490, exp1, domain.OptionTypePut)
	openTx := makeTransaction(trade1ID, "btx-101", acc.ID, acc.Broker, putInst1, domain.ActionSTO, domain.PositionEffectOpening, 1, t1)
	seedChainTrade(t, ctx, repos, acc, trade1ID, t1, openTx)

	// Trade 2: roll the put down and out (close old, open new).
	trade2ID := uuid.New().String()
	putInst2 := makeEquityOption("SPY", 480, exp2, domain.OptionTypePut)
	closeTx := makeTransaction(trade2ID, "btx-102", acc.ID, acc.Broker, putInst1, domain.ActionBTC, domain.PositionEffectClosing, 1, t2)
	rollOpenTx := makeTransaction(trade2ID, "btx-103", acc.ID, acc.Broker, putInst2, domain.ActionSTO, domain.PositionEffectOpening, 1, t2)
	seedChainTrade(t, ctx, repos, acc, trade2ID, t2, closeTx, rollOpenTx)

	require.NoError(t, svc.DetectChains(ctx, acc.ID))

	chains, err := repos.Chains.ListChainsByAccount(ctx, acc.ID, false)
	require.NoError(t, err)
	require.Len(t, chains, 1)
	chainID := chains[0].ID

	// A ChainLink must have been created for the roll.
	links, err := repos.Chains.ListChainLinks(ctx, chainID)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, domain.LinkTypeRoll, links[0].LinkType)
	assert.Equal(t, trade2ID, links[0].ClosingTradeID)
	assert.Equal(t, 1, links[0].Sequence)

	// Chain still open (putInst2 has open balance).
	assert.Nil(t, chains[0].ClosedAt)
}

// TestChainService_CloseOnlyClosesChain: when the final position is closed by a
// close-only trade, the chain is marked closed.
func TestChainService_CloseOnlyClosesChain(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newChainSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)

	trade1ID := uuid.New().String()
	putInst := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)
	openTx := makeTransaction(trade1ID, "btx-201", acc.ID, acc.Broker, putInst, domain.ActionSTO, domain.PositionEffectOpening, 1, t1)
	seedChainTrade(t, ctx, repos, acc, trade1ID, t1, openTx)

	trade2ID := uuid.New().String()
	closeTx := makeTransaction(trade2ID, "btx-202", acc.ID, acc.Broker, putInst, domain.ActionBTC, domain.PositionEffectClosing, 1, t2)
	seedChainTrade(t, ctx, repos, acc, trade2ID, t2, closeTx)

	require.NoError(t, svc.DetectChains(ctx, acc.ID))

	chains, err := repos.Chains.ListChainsByAccount(ctx, acc.ID, false)
	require.NoError(t, err)
	require.Len(t, chains, 1)
	assert.NotNil(t, chains[0].ClosedAt)
}

// TestChainService_CloseOnlyPartialLeavesChainOpen: a close-only trade that partially
// closes the chain's position leaves the chain open.
func TestChainService_CloseOnlyPartialLeavesChainOpen(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newChainSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)

	// Open 2 contracts.
	trade1ID := uuid.New().String()
	putInst := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)
	openTx := makeTransaction(trade1ID, "btx-301", acc.ID, acc.Broker, putInst, domain.ActionSTO, domain.PositionEffectOpening, 2, t1)
	seedChainTrade(t, ctx, repos, acc, trade1ID, t1, openTx)

	// Close 1 of 2 contracts only.
	trade2ID := uuid.New().String()
	closeTx := makeTransaction(trade2ID, "btx-302", acc.ID, acc.Broker, putInst, domain.ActionBTC, domain.PositionEffectClosing, 1, t2)
	seedChainTrade(t, ctx, repos, acc, trade2ID, t2, closeTx)

	require.NoError(t, svc.DetectChains(ctx, acc.ID))

	chains, err := repos.Chains.ListChainsByAccount(ctx, acc.ID, false)
	require.NoError(t, err)
	require.Len(t, chains, 1)
	assert.Nil(t, chains[0].ClosedAt, "chain should remain open with 1 contract still active")
}

// TestChainService_Idempotent: running DetectChains twice produces no duplicate chains.
func TestChainService_Idempotent(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newChainSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	openedAt := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)

	tradeID := uuid.New().String()
	inst := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)
	tx := makeTransaction(tradeID, "btx-401", acc.ID, acc.Broker, inst, domain.ActionSTO, domain.PositionEffectOpening, 1, openedAt)
	seedChainTrade(t, ctx, repos, acc, tradeID, openedAt, tx)

	require.NoError(t, svc.DetectChains(ctx, acc.ID))
	require.NoError(t, svc.DetectChains(ctx, acc.ID))

	chains, err := repos.Chains.ListChainsByAccount(ctx, acc.ID, false)
	require.NoError(t, err)
	assert.Len(t, chains, 1, "second run must not create a duplicate chain")
}

// TestChainService_GetChainPnL: P&L equals net premium computed from transaction data.
func TestChainService_GetChainPnL(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newChainSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)

	putInst := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)
	trade1ID := uuid.New().String()
	// STO 2 contracts at $1.50: credit = 2 × $1.50 × 100 = $300.00, fees = $0.65
	openTx := domain.Transaction{
		ID:             uuid.New().String(),
		TradeID:        trade1ID,
		BrokerTxID:     "btx-501",
		Broker:         acc.Broker,
		AccountID:      acc.ID,
		Instrument:     putInst,
		Action:         domain.ActionSTO,
		Quantity:       decimal.NewFromInt(2),
		FillPrice:      decimal.NewFromFloat(1.50),
		Fees:           decimal.NewFromFloat(0.65),
		ExecutedAt:     t1,
		PositionEffect: domain.PositionEffectOpening,
	}
	seedChainTrade(t, ctx, repos, acc, trade1ID, t1, openTx)

	trade2ID := uuid.New().String()
	// BTC 2 contracts at $0.15: debit = 2 × $0.15 × 100 = $30.00, fees = $0.65
	closeTx := domain.Transaction{
		ID:             uuid.New().String(),
		TradeID:        trade2ID,
		BrokerTxID:     "btx-502",
		Broker:         acc.Broker,
		AccountID:      acc.ID,
		Instrument:     putInst,
		Action:         domain.ActionBTC,
		Quantity:       decimal.NewFromInt(2),
		FillPrice:      decimal.NewFromFloat(0.15),
		Fees:           decimal.NewFromFloat(0.65),
		ExecutedAt:     t2,
		PositionEffect: domain.PositionEffectClosing,
	}
	seedChainTrade(t, ctx, repos, acc, trade2ID, t2, closeTx)

	require.NoError(t, svc.DetectChains(ctx, acc.ID))

	chains, err := repos.Chains.ListChainsByAccount(ctx, acc.ID, false)
	require.NoError(t, err)
	require.Len(t, chains, 1)

	// Expected: $300.00 credit - $0.65 fees - $30.00 debit - $0.65 fees = $268.70
	pnl, err := svc.GetChainPnL(ctx, chains[0].ID)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromFloat(268.70).Equal(pnl), "got %s", pnl)
}

// TestChainService_MultiLegRollZeroDeltas: a roll with >1 closing or >1 opening option
// produces a ChainLink with StrikeChange=0 and ExpirationChange=0.
func TestChainService_MultiLegRollZeroDeltas(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newChainSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp1 := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	exp2 := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)

	// Trade 1: open a short strangle (put + call).
	trade1ID := uuid.New().String()
	putInst1 := makeEquityOption("SPY", 480, exp1, domain.OptionTypePut)
	callInst1 := makeEquityOption("SPY", 520, exp1, domain.OptionTypeCall)
	openPutTx := makeTransaction(trade1ID, "ml-001", acc.ID, acc.Broker, putInst1, domain.ActionSTO, domain.PositionEffectOpening, 1, t1)
	openCallTx := makeTransaction(trade1ID, "ml-002", acc.ID, acc.Broker, callInst1, domain.ActionSTO, domain.PositionEffectOpening, 1, t1)
	seedChainTrade(t, ctx, repos, acc, trade1ID, t1, openPutTx, openCallTx)

	// Trade 2: roll both legs (2 closing + 2 opening — multi-leg).
	trade2ID := uuid.New().String()
	putInst2 := makeEquityOption("SPY", 475, exp2, domain.OptionTypePut)
	callInst2 := makeEquityOption("SPY", 525, exp2, domain.OptionTypeCall)
	closePutTx := makeTransaction(trade2ID, "ml-003", acc.ID, acc.Broker, putInst1, domain.ActionBTC, domain.PositionEffectClosing, 1, t2)
	closeCallTx := makeTransaction(trade2ID, "ml-004", acc.ID, acc.Broker, callInst1, domain.ActionBTC, domain.PositionEffectClosing, 1, t2)
	openPutTx2 := makeTransaction(trade2ID, "ml-005", acc.ID, acc.Broker, putInst2, domain.ActionSTO, domain.PositionEffectOpening, 1, t2)
	openCallTx2 := makeTransaction(trade2ID, "ml-006", acc.ID, acc.Broker, callInst2, domain.ActionSTO, domain.PositionEffectOpening, 1, t2)
	seedChainTrade(t, ctx, repos, acc, trade2ID, t2, closePutTx, closeCallTx, openPutTx2, openCallTx2)

	require.NoError(t, svc.DetectChains(ctx, acc.ID))

	chains, err := repos.Chains.ListChainsByAccount(ctx, acc.ID, false)
	require.NoError(t, err)
	require.Len(t, chains, 1)

	links, err := repos.Chains.ListChainLinks(ctx, chains[0].ID)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, domain.LinkTypeRoll, links[0].LinkType)
	assert.True(t, decimal.Zero.Equal(links[0].StrikeChange), "multi-leg roll: StrikeChange must be zero, got %s", links[0].StrikeChange)
	assert.Equal(t, 0, links[0].ExpirationChange, "multi-leg roll: ExpirationChange must be zero")
}

// TestChainService_AssignmentLinkType: a mixed trade with ActionAssignment produces a
// chain link with LinkTypeAssignment.
func TestChainService_AssignmentLinkType(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newChainSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)

	// Trade 1: short put.
	trade1ID := uuid.New().String()
	putInst := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)
	openTx := makeTransaction(trade1ID, "asgn-001", acc.ID, acc.Broker, putInst, domain.ActionSTO, domain.PositionEffectOpening, 1, t1)
	seedChainTrade(t, ctx, repos, acc, trade1ID, t1, openTx)

	// Trade 2: assignment — closes the put, opens an equity lot.
	trade2ID := uuid.New().String()
	equityInst := makeEquity("SPY")
	assignTx := makeTransaction(trade2ID, "asgn-002", acc.ID, acc.Broker, putInst, domain.ActionAssignment, domain.PositionEffectClosing, 1, t2)
	openEquityTx := makeTransaction(trade2ID, "asgn-003", acc.ID, acc.Broker, equityInst, domain.ActionBuy, domain.PositionEffectOpening, 100, t2)
	seedChainTrade(t, ctx, repos, acc, trade2ID, t2, assignTx, openEquityTx)

	require.NoError(t, svc.DetectChains(ctx, acc.ID))

	chains, err := repos.Chains.ListChainsByAccount(ctx, acc.ID, false)
	require.NoError(t, err)
	require.Len(t, chains, 1)

	links, err := repos.Chains.ListChainLinks(ctx, chains[0].ID)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, domain.LinkTypeAssignment, links[0].LinkType)
}

// TestChainService_RollIdempotent: running DetectChains twice on a roll does not
// duplicate the chain link.
func TestChainService_RollIdempotent(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newChainSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp1 := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	exp2 := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)

	trade1ID := uuid.New().String()
	putInst1 := makeEquityOption("SPY", 490, exp1, domain.OptionTypePut)
	openTx := makeTransaction(trade1ID, "ri-001", acc.ID, acc.Broker, putInst1, domain.ActionSTO, domain.PositionEffectOpening, 1, t1)
	seedChainTrade(t, ctx, repos, acc, trade1ID, t1, openTx)

	trade2ID := uuid.New().String()
	putInst2 := makeEquityOption("SPY", 480, exp2, domain.OptionTypePut)
	closeTx := makeTransaction(trade2ID, "ri-002", acc.ID, acc.Broker, putInst1, domain.ActionBTC, domain.PositionEffectClosing, 1, t2)
	rollOpenTx := makeTransaction(trade2ID, "ri-003", acc.ID, acc.Broker, putInst2, domain.ActionSTO, domain.PositionEffectOpening, 1, t2)
	seedChainTrade(t, ctx, repos, acc, trade2ID, t2, closeTx, rollOpenTx)

	require.NoError(t, svc.DetectChains(ctx, acc.ID))
	require.NoError(t, svc.DetectChains(ctx, acc.ID))

	chains, err := repos.Chains.ListChainsByAccount(ctx, acc.ID, false)
	require.NoError(t, err)
	require.Len(t, chains, 1, "second run must not create a duplicate chain")

	links, err := repos.Chains.ListChainLinks(ctx, chains[0].ID)
	require.NoError(t, err)
	assert.Len(t, links, 1, "second run must not create a duplicate chain link")
}

// TestChainService_EmptyAccount: DetectChains on an account with no trades returns
// nil and creates no chains.
func TestChainService_EmptyAccount(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newChainSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	require.NoError(t, svc.DetectChains(ctx, acc.ID))

	chains, err := repos.Chains.ListChainsByAccount(ctx, acc.ID, false)
	require.NoError(t, err)
	assert.Empty(t, chains)
}

// TestChainService_TwoIndependentChains: two separate positions on the same account
// (different underlyings) produce exactly two chains with correct attribution.
func TestChainService_TwoIndependentChains(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newChainSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	t4 := time.Date(2026, 4, 21, 10, 0, 0, 0, time.UTC)

	// Chain 1: SPY short put, opened and closed.
	spy1ID := uuid.New().String()
	spyInst := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)
	seedChainTrade(t, ctx, repos, acc, spy1ID, t1,
		makeTransaction(spy1ID, "tc-001", acc.ID, acc.Broker, spyInst, domain.ActionSTO, domain.PositionEffectOpening, 1, t1))
	spy2ID := uuid.New().String()
	seedChainTrade(t, ctx, repos, acc, spy2ID, t3,
		makeTransaction(spy2ID, "tc-002", acc.ID, acc.Broker, spyInst, domain.ActionBTC, domain.PositionEffectClosing, 1, t3))

	// Chain 2: QQQ short put, opened and closed.
	qqq1ID := uuid.New().String()
	qqqInst := makeEquityOption("QQQ", 400, exp, domain.OptionTypePut)
	seedChainTrade(t, ctx, repos, acc, qqq1ID, t2,
		makeTransaction(qqq1ID, "tc-003", acc.ID, acc.Broker, qqqInst, domain.ActionSTO, domain.PositionEffectOpening, 1, t2))
	qqq2ID := uuid.New().String()
	seedChainTrade(t, ctx, repos, acc, qqq2ID, t4,
		makeTransaction(qqq2ID, "tc-004", acc.ID, acc.Broker, qqqInst, domain.ActionBTC, domain.PositionEffectClosing, 1, t4))

	require.NoError(t, svc.DetectChains(ctx, acc.ID))

	chains, err := repos.Chains.ListChainsByAccount(ctx, acc.ID, false)
	require.NoError(t, err)
	require.Len(t, chains, 2)

	symbols := map[string]bool{}
	for _, c := range chains {
		symbols[c.UnderlyingSymbol] = true
		assert.NotNil(t, c.ClosedAt, "both chains should be closed")
	}
	assert.True(t, symbols["SPY"], "expected SPY chain")
	assert.True(t, symbols["QQQ"], "expected QQQ chain")
}

// TestChainService_UnattributableCloseIsSkipped: a close-only trade with no matching
// open chain (e.g. out-of-order import where timestamp is wrong) is skipped without
// error. DetectChains succeeds and the open trade still gets its own chain.
func TestChainService_UnattributableCloseIsSkipped(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	svc := newChainSvc(repos)
	acc := seedImportAccount(t, ctx, repos)

	exp := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	// The close trade has an earlier timestamp than the open: DetectChains processes
	// the close first and finds no open chain to attribute it to.
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)  // close
	t2 := time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC) // open

	putInst := makeEquityOption("SPY", 490, exp, domain.OptionTypePut)

	closeID := uuid.New().String()
	seedChainTrade(t, ctx, repos, acc, closeID, t1,
		makeTransaction(closeID, "oo-001", acc.ID, acc.Broker, putInst, domain.ActionBTC, domain.PositionEffectClosing, 1, t1))

	openID := uuid.New().String()
	seedChainTrade(t, ctx, repos, acc, openID, t2,
		makeTransaction(openID, "oo-002", acc.ID, acc.Broker, putInst, domain.ActionSTO, domain.PositionEffectOpening, 1, t2))

	// Must not error; the unattributable close is silently skipped.
	require.NoError(t, svc.DetectChains(ctx, acc.ID))

	chains, err := repos.Chains.ListChainsByAccount(ctx, acc.ID, false)
	require.NoError(t, err)
	// The open trade produces a chain; the skipped close does not.
	require.Len(t, chains, 1)
	assert.Equal(t, openID, chains[0].OriginalTradeID)
	assert.Nil(t, chains[0].ClosedAt, "chain remains open — close was not attributed")
}

// --- test helpers ---

// seedChainTrade creates a trade and its transactions in the DB.
func seedChainTrade(t *testing.T, ctx context.Context, repos *sqlite.Repos, acc *domain.Account, tradeID string, openedAt time.Time, txns ...domain.Transaction) {
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
