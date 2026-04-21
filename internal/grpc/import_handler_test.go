package grpc_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/domain"
	grpchandler "trade-tracker-go/internal/grpc"
	"trade-tracker-go/internal/service"
)

// fakeImporter is a test double for service.Importer.
type fakeImporter struct {
	result *service.ImportResult
	err    error
	// captured holds the transactions passed to Import for inspection.
	captured []domain.Transaction
}

func (f *fakeImporter) Import(_ context.Context, txns []domain.Transaction) (*service.ImportResult, error) {
	f.captured = txns
	return f.result, f.err
}

// fakeImportStream captures the single Send call made by ImportHandler.
type fakeImportStream struct {
	sent []*pb.ImportTransactionsResponse
	ctx  context.Context
}

func newFakeImportStream() *fakeImportStream {
	return &fakeImportStream{ctx: context.Background()}
}

func (s *fakeImportStream) Send(resp *pb.ImportTransactionsResponse) error {
	s.sent = append(s.sent, resp)
	return nil
}

func (s *fakeImportStream) Context() context.Context { return s.ctx }

// fakeImportStream must satisfy grpc.ServerStreamingServer[pb.ImportTransactionsResponse].
// The methods below are no-ops required by the grpc.ServerStream interface.
func (s *fakeImportStream) SendMsg(m any) error             { return nil }
func (s *fakeImportStream) RecvMsg(m any) error             { return nil }
func (s *fakeImportStream) SetHeader(md metadata.MD) error  { return nil }
func (s *fakeImportStream) SendHeader(md metadata.MD) error { return nil }
func (s *fakeImportStream) SetTrailer(md metadata.MD)       {}

// tastytradeHeader is the canonical 21-column Tastytrade CSV header.
const tastytradeHeader = "Date,Type,Sub Type,Action,Symbol,Instrument Type,Description,Value,Quantity,Average Price,Commissions,Fees,Multiplier,Root Symbol,Underlying Symbol,Expiration Date,Strike Price,Call or Put,Order #,Total,Currency\n"

// minimalTastytradeCSV is a single-row Tastytrade CSV that the parser accepts and
// converts into one domain.Transaction (equity option, sell-to-open).
// Column count (21) matches numCols in the tastytrade parser.
// Action value (SELL_TO_OPEN) matches what mapAction accepts.
// NOTE: this CSV data is also duplicated in integration_test.go as smokeCSV.
// If you change the format here, update that copy too.
const minimalTastytradeCSV = tastytradeHeader +
	"2024-01-15T10:00:00-0500,Trade,Sell to Open,SELL_TO_OPEN,SPY   240119C00480000,Equity Option,Sold 1 SPY Call @ 1.50,150.00,1,1.50,0.00,-0.10,100,SPY,SPY,1/19/24,480,CALL,ORD001001,149.90,USD\n"

func TestImportTransactions_Success(t *testing.T) {
	imp := &fakeImporter{
		result: &service.ImportResult{Imported: 1, Skipped: 0, Failed: 0},
	}
	h := grpchandler.NewImportHandler(imp, testLogger)
	stream := newFakeImportStream()

	err := h.ImportTransactions(&pb.ImportTransactionsRequest{
		AccountId: "acct-1",
		Broker:    pb.Broker_BROKER_TASTYTRADE,
		CsvData:   []byte(minimalTastytradeCSV),
	}, stream)

	require.NoError(t, err)
	require.Len(t, stream.sent, 1)
	resp := stream.sent[0]
	assert.Equal(t, uint32(1), resp.Imported)
	assert.Equal(t, uint32(0), resp.Skipped)
	assert.Equal(t, uint32(0), resp.Failed)
	assert.Empty(t, resp.Errors)

	// Verify the parser actually ran and forwarded transactions to the importer.
	require.NotEmpty(t, imp.captured)
	assert.Equal(t, "acct-1", imp.captured[0].AccountID)
	assert.Equal(t, "tastytrade", imp.captured[0].Broker)
}

func TestImportTransactions_WithFailures(t *testing.T) {
	imp := &fakeImporter{
		result: &service.ImportResult{
			Imported: 2,
			Skipped:  1,
			Failed:   1,
			Errors: []service.ImportError{
				{TradeID: "t-bad", HookName: "position", Err: assert.AnError},
			},
		},
	}
	h := grpchandler.NewImportHandler(imp, testLogger)
	stream := newFakeImportStream()

	err := h.ImportTransactions(&pb.ImportTransactionsRequest{
		AccountId: "acct-1",
		Broker:    pb.Broker_BROKER_TASTYTRADE,
		CsvData:   []byte(minimalTastytradeCSV),
	}, stream)

	require.NoError(t, err)
	require.Len(t, stream.sent, 1)
	resp := stream.sent[0]
	assert.Equal(t, uint32(2), resp.Imported)
	assert.Equal(t, uint32(1), resp.Skipped)
	assert.Equal(t, uint32(1), resp.Failed)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "t-bad", resp.Errors[0].TradeId)
	assert.Equal(t, "position", resp.Errors[0].HookName)
	assert.NotEmpty(t, resp.Errors[0].Message)
}

func TestImportTransactions_ImporterFatalError(t *testing.T) {
	imp := &fakeImporter{err: errors.New("db connection lost")}
	h := grpchandler.NewImportHandler(imp, testLogger)

	err := h.ImportTransactions(&pb.ImportTransactionsRequest{
		AccountId: "acct-1",
		Broker:    pb.Broker_BROKER_TASTYTRADE,
		CsvData:   []byte(minimalTastytradeCSV),
	}, newFakeImportStream())

	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestImportTransactions_MissingAccountID(t *testing.T) {
	h := grpchandler.NewImportHandler(&fakeImporter{result: &service.ImportResult{}}, testLogger)

	err := h.ImportTransactions(&pb.ImportTransactionsRequest{
		Broker:  pb.Broker_BROKER_TASTYTRADE,
		CsvData: []byte(minimalTastytradeCSV),
	}, newFakeImportStream())

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestImportTransactions_MissingBroker(t *testing.T) {
	h := grpchandler.NewImportHandler(&fakeImporter{result: &service.ImportResult{}}, testLogger)

	// Omitting broker leaves it at the zero value (BROKER_UNSPECIFIED).
	err := h.ImportTransactions(&pb.ImportTransactionsRequest{
		AccountId: "acct-1",
		CsvData:   []byte(minimalTastytradeCSV),
	}, newFakeImportStream())

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestImportTransactions_MissingCSVData(t *testing.T) {
	h := grpchandler.NewImportHandler(&fakeImporter{result: &service.ImportResult{}}, testLogger)

	err := h.ImportTransactions(&pb.ImportTransactionsRequest{
		AccountId: "acct-1",
		Broker:    pb.Broker_BROKER_TASTYTRADE,
	}, newFakeImportStream())

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestImportTransactions_CSVDataTooLarge(t *testing.T) {
	h := grpchandler.NewImportHandler(&fakeImporter{result: &service.ImportResult{}}, testLogger)
	oversized := []byte(strings.Repeat("a", (1<<20)+1))

	err := h.ImportTransactions(&pb.ImportTransactionsRequest{
		AccountId: "acct-1",
		Broker:    pb.Broker_BROKER_TASTYTRADE,
		CsvData:   oversized,
	}, newFakeImportStream())

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestImportTransactions_UnsupportedBroker(t *testing.T) {
	h := grpchandler.NewImportHandler(&fakeImporter{result: &service.ImportResult{}}, testLogger)

	// Use an out-of-range numeric value to simulate a broker the server doesn't handle.
	// Proto3 preserves unknown enum values as their numeric representation.
	err := h.ImportTransactions(&pb.ImportTransactionsRequest{
		AccountId: "acct-1",
		Broker:    pb.Broker(99),
		CsvData:   []byte("some,csv,data"),
	}, newFakeImportStream())

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}
