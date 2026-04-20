// Package client provides a typed wrapper around the trade-tracker gRPC stubs.
// Views import this package and depend on the Client interface; they never
// import the generated proto packages directly, which keeps them testable.
package client

import (
	"context"
	"io"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "trade-tracker-go/gen/tradetracker/v1"
)

// ListTradesParams mirrors the optional filters accepted by TradeService.ListTrades.
type ListTradesParams struct {
	AccountID    string
	Symbol       string
	StrategyType pb.StrategyType
	From         *time.Time
	To           *time.Time
	OpenOnly     bool
	ClosedOnly   bool
}

// ImportParams holds the inputs for a single ImportTransactions call.
type ImportParams struct {
	AccountID string
	Broker    pb.Broker
	CSVData   []byte
}

// ImportEvent is one progress update from the streaming ImportTransactions RPC.
type ImportEvent struct {
	Response *pb.ImportTransactionsResponse
	Err      error // non-nil on stream error; signals end of stream
}

// Client is the interface views use to talk to the server.
// All methods accept a context so callers can cancel in-flight requests.
type Client interface {
	ListAccounts(ctx context.Context) ([]*pb.Account, error)
	CreateAccount(ctx context.Context, broker, accountNumber, name string) (*pb.Account, error)
	UpdateAccount(ctx context.Context, id, name string) (*pb.Account, error)
	ListPositions(ctx context.Context, accountID string, status pb.PositionStatus) ([]*pb.Position, error)
	GetPosition(ctx context.Context, accountID, positionID string) (*pb.Position, error)
	GetChain(ctx context.Context, accountID, chainID string) (*pb.ChainDetail, error)
	ListTrades(ctx context.Context, p ListTradesParams) ([]*pb.Trade, error)
	GetAccountSummary(ctx context.Context, accountID string, from, to time.Time) (*pb.GetAccountSummaryResponse, error)
	ImportTransactions(ctx context.Context, p ImportParams) (<-chan ImportEvent, error)
	Close() error
}

// grpcClient is the production implementation of Client.
type grpcClient struct {
	conn      *grpc.ClientConn
	accounts  pb.AccountServiceClient
	positions pb.PositionServiceClient
	chains    pb.ChainServiceClient
	trades    pb.TradeServiceClient
	analytics pb.AnalyticsServiceClient
	imports   pb.ImportServiceClient
}

// New dials addr and returns a Client. The caller must call Close() when done.
func New(addr string) (Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &grpcClient{
		conn:      conn,
		accounts:  pb.NewAccountServiceClient(conn),
		positions: pb.NewPositionServiceClient(conn),
		chains:    pb.NewChainServiceClient(conn),
		trades:    pb.NewTradeServiceClient(conn),
		analytics: pb.NewAnalyticsServiceClient(conn),
		imports:   pb.NewImportServiceClient(conn),
	}, nil
}

func (c *grpcClient) Close() error {
	return c.conn.Close()
}

func (c *grpcClient) ListAccounts(ctx context.Context) ([]*pb.Account, error) {
	resp, err := c.accounts.ListAccounts(ctx, &pb.ListAccountsRequest{})
	if err != nil {
		return nil, err
	}
	return resp.Accounts, nil
}

func (c *grpcClient) CreateAccount(ctx context.Context, broker, accountNumber, name string) (*pb.Account, error) {
	resp, err := c.accounts.CreateAccount(ctx, &pb.CreateAccountRequest{
		Broker:        broker,
		AccountNumber: accountNumber,
		Name:          name,
	})
	if err != nil {
		return nil, err
	}
	return resp.Account, nil
}

func (c *grpcClient) UpdateAccount(ctx context.Context, id, name string) (*pb.Account, error) {
	resp, err := c.accounts.UpdateAccount(ctx, &pb.UpdateAccountRequest{
		Id:   id,
		Name: name,
	})
	if err != nil {
		return nil, err
	}
	return resp.Account, nil
}

func (c *grpcClient) ListPositions(ctx context.Context, accountID string, status pb.PositionStatus) ([]*pb.Position, error) {
	resp, err := c.positions.ListPositions(ctx, &pb.ListPositionsRequest{
		AccountId: accountID,
		Status:    status,
	})
	if err != nil {
		return nil, err
	}
	return resp.Positions, nil
}

func (c *grpcClient) GetPosition(ctx context.Context, accountID, positionID string) (*pb.Position, error) {
	resp, err := c.positions.GetPosition(ctx, &pb.GetPositionRequest{
		AccountId: accountID,
		Id:        positionID,
	})
	if err != nil {
		return nil, err
	}
	return resp.Position, nil
}

func (c *grpcClient) GetChain(ctx context.Context, accountID, chainID string) (*pb.ChainDetail, error) {
	resp, err := c.chains.GetChain(ctx, &pb.GetChainRequest{
		AccountId: accountID,
		Id:        chainID,
	})
	if err != nil {
		return nil, err
	}
	return resp.Chain, nil
}

func (c *grpcClient) ListTrades(ctx context.Context, p ListTradesParams) ([]*pb.Trade, error) {
	req := &pb.ListTradesRequest{
		AccountId:    p.AccountID,
		Symbol:       p.Symbol,
		StrategyType: p.StrategyType,
		OpenOnly:     p.OpenOnly,
		ClosedOnly:   p.ClosedOnly,
	}
	if p.From != nil {
		req.OpenedAfter = timestamppb.New(*p.From)
	}
	if p.To != nil {
		req.OpenedBefore = timestamppb.New(*p.To)
	}
	resp, err := c.trades.ListTrades(ctx, req)
	if err != nil {
		return nil, err
	}
	return resp.Trades, nil
}

func (c *grpcClient) GetAccountSummary(ctx context.Context, accountID string, from, to time.Time) (*pb.GetAccountSummaryResponse, error) {
	return c.analytics.GetAccountSummary(ctx, &pb.GetAccountSummaryRequest{
		AccountId: accountID,
		From:      timestamppb.New(from),
		To:        timestamppb.New(to),
	})
}

func (c *grpcClient) ImportTransactions(ctx context.Context, p ImportParams) (<-chan ImportEvent, error) {
	stream, err := c.imports.ImportTransactions(ctx, &pb.ImportTransactionsRequest{
		AccountId: p.AccountID,
		Broker:    p.Broker,
		CsvData:   p.CSVData,
	})
	if err != nil {
		return nil, err
	}
	ch := make(chan ImportEvent, 32)
	go func() {
		defer close(ch)
		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				// Normal stream termination — close the channel without sending.
				return
			}
			if err != nil {
				// Real transport or server error.
				ch <- ImportEvent{Err: err}
				return
			}
			ch <- ImportEvent{Response: resp}
		}
	}()
	return ch, nil
}
