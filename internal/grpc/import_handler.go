package grpc

import (
	"bytes"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/broker/schwab"
	"trade-tracker-go/internal/broker/tastytrade"
	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/service"
)

// maxCSVBytes is the maximum accepted size for csv_data (1 MiB).
// Files produced by Tastytrade and Schwab exports are well under this limit.
const maxCSVBytes = 1 << 20

// errUnsupportedBroker is returned by parseBrokerCSV when the broker value is not
// recognised. It is distinct from parse errors so the handler can produce a
// more specific status message for each failure mode.
var errUnsupportedBroker = errors.New("unsupported broker")

// ImportHandler implements pb.ImportServiceServer.
// It dispatches CSV data to the appropriate broker parser then calls the Importer.
type ImportHandler struct {
	pb.UnimplementedImportServiceServer
	importer service.Importer
}

// NewImportHandler creates an ImportHandler backed by the given importer.
func NewImportHandler(importer service.Importer) *ImportHandler {
	return &ImportHandler{importer: importer}
}

func (h *ImportHandler) ImportTransactions(
	req *pb.ImportTransactionsRequest,
	stream pb.ImportService_ImportTransactionsServer,
) error {
	if req.AccountId == "" {
		return status.Error(codes.InvalidArgument, "account_id is required")
	}
	if req.Broker == pb.Broker_BROKER_UNSPECIFIED {
		return status.Error(codes.InvalidArgument, "broker is required")
	}
	if len(req.CsvData) == 0 {
		return status.Error(codes.InvalidArgument, "csv_data is required")
	}
	if len(req.CsvData) > maxCSVBytes {
		return status.Errorf(codes.InvalidArgument, "csv_data exceeds maximum size of %d bytes", maxCSVBytes)
	}

	txns, err := parseBrokerCSV(req.Broker, req.AccountId, req.CsvData)
	if err != nil {
		if errors.Is(err, errUnsupportedBroker) {
			return status.Error(codes.InvalidArgument, err.Error())
		}
		return status.Errorf(codes.InvalidArgument, "parse csv: %s", err.Error())
	}

	result, err := h.importer.Import(stream.Context(), txns)
	if err != nil {
		return toGRPCError(err)
	}

	resp := &pb.ImportTransactionsResponse{
		Imported: uint32(result.Imported),
		Skipped:  uint32(result.Skipped),
		Failed:   uint32(result.Failed),
	}
	for _, ie := range result.Errors {
		resp.Errors = append(resp.Errors, &pb.ImportError{
			TradeId:  ie.TradeID,
			HookName: ie.HookName,
			Message:  ie.Err.Error(),
		})
	}

	return stream.Send(resp)
}

// parseBrokerCSV selects the appropriate broker parser and returns normalized transactions.
// Returns errUnsupportedBroker (detectable via errors.Is) when the broker value is unknown.
func parseBrokerCSV(broker pb.Broker, accountID string, csvData []byte) ([]domain.Transaction, error) {
	r := bytes.NewReader(csvData)
	switch broker {
	case pb.Broker_BROKER_TASTYTRADE:
		return (&tastytrade.Parser{}).Parse(r, accountID)
	case pb.Broker_BROKER_SCHWAB:
		return (&schwab.Parser{}).Parse(r, accountID)
	default:
		return nil, fmt.Errorf("%w: %v", errUnsupportedBroker, broker)
	}
}
