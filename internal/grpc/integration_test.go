package grpc_test

// TestIntegration_SmokeTest exercises all six gRPC services in a single ordered
// sequence. Subtests share state (tradeID, positionID, chainID) and MUST NOT be
// run individually — they depend on earlier subtests having populated the database.
// Running a subset with -run will cause downstream subtests to be skipped.

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/domain"
	grpchandler "trade-tracker-go/internal/grpc"
	"trade-tracker-go/internal/repository/sqlite"
	"trade-tracker-go/internal/service"
	"trade-tracker-go/internal/strategy"
)

// testLogger discards all log output in tests.
var testLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// smokeCSV is a minimal Tastytrade CSV with one SELL_TO_OPEN transaction.
// Duplicated from import_handler_test.go (same package) to keep the integration
// test self-contained; the unit test file owns the canonical copy.
const smokeCSV = "Date,Type,Sub Type,Action,Symbol,Instrument Type,Description,Value,Quantity,Average Price,Commissions,Fees,Multiplier,Root Symbol,Underlying Symbol,Expiration Date,Strike Price,Call or Put,Order #,Total,Currency\n" +
	"2024-01-15T10:00:00-0500,Trade,Sell to Open,SELL_TO_OPEN,SPY   240119C00480000,Equity Option,Sold 1 SPY Call @ 1.50,150.00,1,1.50,0.00,-0.10,100,SPY,SPY,1/19/24,480,CALL,ORD001001,149.90,USD\n"

// smokeStack bundles clients for all six services with the underlying repos for
// direct account seeding in the legacy flow and for CreateAccount verification.
type smokeStack struct {
	account   pb.AccountServiceClient
	importer  pb.ImportServiceClient
	trades    pb.TradeServiceClient
	positions pb.PositionServiceClient
	chains    pb.ChainServiceClient
	analytics pb.AnalyticsServiceClient
	repos     *sqlite.Repos
}

// startSmokeServer starts a real gRPC server over an in-process bufconn listener,
// backed by an in-memory SQLite database with all migrations applied.
// Server options mirror production (cmd/trade-tracker/main.go).
// All resources are registered for cleanup with t.Cleanup.
func startSmokeServer(t *testing.T) *smokeStack {
	t.Helper()

	repos, err := sqlite.OpenRepos(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = repos.Close() })

	chainSvc := service.NewChainService(repos.Chains, repos.Trades, repos.Transactions)
	positionSvc := service.NewPositionService(repos.Positions, testLogger)
	importSvc := service.NewImportService(
		repos.Trades,
		repos.Transactions,
		repos.Instruments,
		strategy.NewClassifier(),
		chainSvc,
		service.PostImportHook{Name: "position", Run: positionSvc.ProcessTrade},
	)
	analyticsSvc := service.NewAnalyticsService(repos.DB())

	// MaxRecvMsgSize matches production (main.go).
	srv := grpc.NewServer(grpc.MaxRecvMsgSize(2 << 20))
	pb.RegisterAccountServiceServer(srv, grpchandler.NewAccountHandler(repos.Accounts, repos.Accounts, testLogger))
	pb.RegisterImportServiceServer(srv, grpchandler.NewImportHandler(importSvc, testLogger))
	pb.RegisterTradeServiceServer(srv, grpchandler.NewTradeHandler(repos.Trades, testLogger))
	pb.RegisterPositionServiceServer(srv, grpchandler.NewPositionHandler(positionSvc, testLogger))
	pb.RegisterChainServiceServer(srv, grpchandler.NewChainHandler(chainSvc, testLogger))
	pb.RegisterAnalyticsServiceServer(srv, grpchandler.NewAnalyticsHandler(analyticsSvc, testLogger))

	lis := bufconn.Listen(1 << 20) // 1 MiB in-process buffer
	t.Cleanup(func() { _ = lis.Close() })
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return &smokeStack{
		account:   pb.NewAccountServiceClient(conn),
		importer:  pb.NewImportServiceClient(conn),
		trades:    pb.NewTradeServiceClient(conn),
		positions: pb.NewPositionServiceClient(conn),
		chains:    pb.NewChainServiceClient(conn),
		analytics: pb.NewAnalyticsServiceClient(conn),
		repos:     repos,
	}
}

// TestIntegration_SmokeTest exercises all six gRPC services against a real in-memory
// SQLite backend. It runs the canonical import → query flow end-to-end:
//
//  1. AccountService  — CreateAccount / UpdateAccount / ListAccounts / GetAccount.
//  2. ImportService   — ImportTransactions with a minimal Tastytrade CSV (1 STO).
//  3. TradeService    — ListTrades / GetTrade for the imported trade.
//  4. PositionService — ListPositions / GetPosition (one open position after import).
//  5. ChainService    — GetChain via the chain_id from the position.
//  6. AnalyticsService — GetAccountSummary (zero realized P&L; position still open).
func TestIntegration_SmokeTest(t *testing.T) {
	ctx := context.Background()
	s := startSmokeServer(t)

	// Create an account via the RPC (no longer seeded directly).
	var accID string
	t.Run("AccountService/CreateAccount", func(t *testing.T) {
		resp, err := s.account.CreateAccount(ctx, &pb.CreateAccountRequest{
			Broker:        "tastytrade",
			AccountNumber: "SMOKE123",
			Name:          "Smoke Test Account",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, resp.Account.Id)
		assert.Equal(t, "tastytrade", resp.Account.Broker)
		assert.Equal(t, "SMOKE123", resp.Account.AccountNumber)
		assert.Equal(t, "Smoke Test Account", resp.Account.Name)
		accID = resp.Account.Id
	})
	require.NotEmpty(t, accID, "CreateAccount must succeed before other subtests can run")
	acc := &domain.Account{ID: accID, AccountNumber: "SMOKE123"}

	// --- AccountService ---

	t.Run("AccountService/ListAccounts", func(t *testing.T) {
		resp, err := s.account.ListAccounts(ctx, &pb.ListAccountsRequest{})
		require.NoError(t, err)
		require.Len(t, resp.Accounts, 1)
		assert.Equal(t, acc.ID, resp.Accounts[0].Id)
		assert.Equal(t, "SMOKE123", resp.Accounts[0].AccountNumber)
	})

	t.Run("AccountService/GetAccount", func(t *testing.T) {
		resp, err := s.account.GetAccount(ctx, &pb.GetAccountRequest{Id: acc.ID})
		require.NoError(t, err)
		assert.Equal(t, acc.ID, resp.Account.Id)
		assert.Equal(t, "Smoke Test Account", resp.Account.Name)
	})

	t.Run("AccountService/UpdateAccount", func(t *testing.T) {
		resp, err := s.account.UpdateAccount(ctx, &pb.UpdateAccountRequest{
			Id:   acc.ID,
			Name: "Renamed Account",
		})
		require.NoError(t, err)
		assert.Equal(t, acc.ID, resp.Account.Id)
		assert.Equal(t, "Renamed Account", resp.Account.Name)

		// Verify the rename persisted.
		list, err := s.account.ListAccounts(ctx, &pb.ListAccountsRequest{})
		require.NoError(t, err)
		require.Len(t, list.Accounts, 1)
		assert.Equal(t, "Renamed Account", list.Accounts[0].Name)
	})

	// --- ImportService ---
	// smokeCSV contains a single SELL_TO_OPEN for SPY.
	// After this, the DB has one trade, one chain, and one open position.

	t.Run("ImportService/ImportTransactions", func(t *testing.T) {
		stream, err := s.importer.ImportTransactions(ctx, &pb.ImportTransactionsRequest{
			AccountId: acc.ID,
			Broker:    pb.Broker_BROKER_TASTYTRADE,
			CsvData:   []byte(smokeCSV),
		})
		require.NoError(t, err)

		resp, err := stream.Recv()
		require.NoError(t, err)
		assert.Equal(t, uint32(1), resp.Imported)
		assert.Equal(t, uint32(0), resp.Failed)
		assert.Empty(t, resp.Errors)

		_, err = stream.Recv()
		require.ErrorIs(t, err, io.EOF) // server sent exactly one summary message
	})

	// --- TradeService ---

	var tradeID string

	t.Run("TradeService/ListTrades", func(t *testing.T) {
		resp, err := s.trades.ListTrades(ctx, &pb.ListTradesRequest{AccountId: acc.ID})
		require.NoError(t, err)
		require.Len(t, resp.Trades, 1)
		tradeID = resp.Trades[0].Id
		assert.Equal(t, "SPY", resp.Trades[0].UnderlyingSymbol)
	})

	t.Run("TradeService/GetTrade", func(t *testing.T) {
		require.NotEmpty(t, tradeID, "TradeService/ListTrades must pass first")
		resp, err := s.trades.GetTrade(ctx, &pb.GetTradeRequest{AccountId: acc.ID, Id: tradeID})
		require.NoError(t, err)
		assert.Equal(t, tradeID, resp.Trade.Id)
		require.NotEmpty(t, resp.Trade.Transactions)
		tx := resp.Trade.Transactions[0]
		assert.Equal(t, pb.Action_ACTION_STO, tx.Action)
		assert.Equal(t, pb.AssetClass_ASSET_CLASS_EQUITY_OPTION, tx.Instrument.AssetClass)
		assert.NotNil(t, tx.Instrument.Option)
	})

	// --- PositionService ---

	var positionID, chainID string

	t.Run("PositionService/ListPositions", func(t *testing.T) {
		resp, err := s.positions.ListPositions(ctx, &pb.ListPositionsRequest{
			AccountId: acc.ID,
			Status:    pb.PositionStatus_POSITION_STATUS_OPEN,
		})
		require.NoError(t, err)
		require.Len(t, resp.Positions, 1)
		positionID = resp.Positions[0].Id
		chainID = resp.Positions[0].ChainId
		assert.Equal(t, "SPY", resp.Positions[0].UnderlyingSymbol)
		assert.Nil(t, resp.Positions[0].ClosedAt)
	})

	t.Run("PositionService/GetPosition", func(t *testing.T) {
		require.NotEmpty(t, positionID, "PositionService/ListPositions must pass first")
		resp, err := s.positions.GetPosition(ctx, &pb.GetPositionRequest{AccountId: acc.ID, Id: positionID})
		require.NoError(t, err)
		assert.Equal(t, positionID, resp.Position.Id)
	})

	// --- ChainService ---

	t.Run("ChainService/GetChain", func(t *testing.T) {
		require.NotEmpty(t, chainID, "PositionService/ListPositions must pass first")
		resp, err := s.chains.GetChain(ctx, &pb.GetChainRequest{AccountId: acc.ID, Id: chainID})
		require.NoError(t, err)
		assert.Equal(t, chainID, resp.Chain.Id)
		assert.Equal(t, "SPY", resp.Chain.UnderlyingSymbol)
		assert.Nil(t, resp.Chain.ClosedAt)
		require.NotEmpty(t, resp.Chain.Events)
		ev := resp.Chain.Events[0]
		assert.Equal(t, pb.LinkType_LINK_TYPE_OPEN, ev.EventType)
		require.NotEmpty(t, ev.Legs)
		assert.Equal(t, pb.Action_ACTION_STO, ev.Legs[0].Action)
	})

	// --- AnalyticsService ---

	t.Run("AnalyticsService/GetAccountSummary", func(t *testing.T) {
		from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		to := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC) // fixed far-future; avoids clock coupling
		resp, err := s.analytics.GetAccountSummary(ctx, &pb.GetAccountSummaryRequest{
			AccountId: acc.ID,
			From:      timestamppb.New(from),
			To:        timestamppb.New(to),
		})
		require.NoError(t, err)
		// Position is still open; realized P&L and closed-position count are zero.
		assert.Equal(t, "0", resp.RealizedPnl)
		assert.Equal(t, int32(0), resp.PositionsClosed)
	})
}

// mixedUnattributableCSV is a roll with a BTC leg for a put that was never imported —
// the closing leg has no matching open chain, triggering attribution_gap on the new chain.
// Both rows share Order # ORD002001 so the parser groups them into one trade.
const mixedUnattributableCSV = "Date,Type,Sub Type,Action,Symbol,Instrument Type,Description,Value,Quantity,Average Price,Commissions,Fees,Multiplier,Root Symbol,Underlying Symbol,Expiration Date,Strike Price,Call or Put,Order #,Total,Currency\n" +
	"2024-03-15T10:00:00-0500,Trade,Buy to Close,BUY_TO_CLOSE,SPY   240419P00490000,Equity Option,Bought 1 SPY Put @ 2.00,-200.00,1,2.00,0.00,-0.10,100,SPY,SPY,4/19/24,490,PUT,ORD002001,-200.10,USD\n" +
	"2024-03-15T10:00:01-0500,Trade,Sell to Open,SELL_TO_OPEN,SPY   240621P00480000,Equity Option,Sold 1 SPY Put @ 3.00,300.00,1,3.00,0.00,-0.10,100,SPY,SPY,6/21/24,480,PUT,ORD002001,299.90,USD\n"

// TestIntegration_AttributionGap verifies that a mixed trade (close+open) with no prior
// open chain sets attribution_gap = true on the newly created chain, and that the flag
// propagates through the full gRPC write-read path.
func TestIntegration_AttributionGap(t *testing.T) {
	ctx := context.Background()
	s := startSmokeServer(t)

	resp, err := s.account.CreateAccount(ctx, &pb.CreateAccountRequest{
		Broker:        "tastytrade",
		AccountNumber: "GAP001",
		Name:          "Attribution Gap Test",
	})
	require.NoError(t, err)
	accID := resp.Account.Id

	stream, err := s.importer.ImportTransactions(ctx, &pb.ImportTransactionsRequest{
		AccountId: accID,
		Broker:    pb.Broker_BROKER_TASTYTRADE,
		CsvData:   []byte(mixedUnattributableCSV),
	})
	require.NoError(t, err)
	importResp, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, uint32(1), importResp.Imported) // two CSV rows → one grouped trade

	posResp, err := s.positions.ListPositions(ctx, &pb.ListPositionsRequest{
		AccountId: accID,
		Status:    pb.PositionStatus_POSITION_STATUS_OPEN,
	})
	require.NoError(t, err)
	require.Len(t, posResp.Positions, 1)
	assert.True(t, posResp.Positions[0].ChainAttributionGap, "position must reflect chain attribution_gap")

	chainResp, err := s.chains.GetChain(ctx, &pb.GetChainRequest{
		AccountId: accID,
		Id:        posResp.Positions[0].ChainId,
	})
	require.NoError(t, err)
	assert.True(t, chainResp.Chain.AttributionGap, "chain must have attribution_gap set")
}
