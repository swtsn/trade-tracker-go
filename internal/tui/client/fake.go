package client

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "trade-tracker-go/gen/tradetracker/v1"
)

// Fake is an in-memory Client for use in tests.
type Fake struct {
	Accounts     []*pb.Account
	Positions    map[string][]*pb.Position // keyed by accountID
	Trades       map[string][]*pb.Trade    // keyed by accountID
	Summaries    map[string]*pb.GetAccountSummaryResponse
	ChainDetails map[string]*pb.ChainDetail // keyed by chainID
	ImportEvents []ImportEvent

	// Err overrides all calls to return this error if non-nil.
	Err error
}

func (f *Fake) ListAccounts(_ context.Context) ([]*pb.Account, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Accounts, nil
}

func (f *Fake) CreateAccount(_ context.Context, broker, accountNumber, name string) (*pb.Account, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	a := &pb.Account{
		Id:            fmt.Sprintf("fake-%d", len(f.Accounts)+1),
		Broker:        broker,
		AccountNumber: accountNumber,
		Name:          name,
	}
	f.Accounts = append(f.Accounts, a)
	return a, nil
}

func (f *Fake) UpdateAccount(_ context.Context, id, name string) (*pb.Account, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	for _, a := range f.Accounts {
		if a.Id == id {
			a.Name = name
			return a, nil
		}
	}
	return nil, status.Errorf(codes.NotFound, "account %q not found", id)
}

func (f *Fake) ListPositions(_ context.Context, accountID string, status pb.PositionStatus) ([]*pb.Position, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	all := f.Positions[accountID]
	if status == pb.PositionStatus_POSITION_STATUS_UNSPECIFIED {
		return all, nil
	}
	var out []*pb.Position
	for _, p := range all {
		open := p.ClosedAt == nil
		if status == pb.PositionStatus_POSITION_STATUS_OPEN && open {
			out = append(out, p)
		} else if status == pb.PositionStatus_POSITION_STATUS_CLOSED && !open {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *Fake) GetPosition(_ context.Context, accountID, positionID string) (*pb.Position, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	for _, p := range f.Positions[accountID] {
		if p.Id == positionID {
			return p, nil
		}
	}
	return nil, status.Errorf(codes.NotFound, "position %q not found", positionID)
}

func (f *Fake) GetChain(_ context.Context, _, chainID string) (*pb.ChainDetail, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	return f.ChainDetails[chainID], nil
}

func (f *Fake) ListTrades(_ context.Context, p ListTradesParams) ([]*pb.Trade, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	all := f.Trades[p.AccountID]
	if p.Symbol == "" {
		return all, nil
	}
	var out []*pb.Trade
	for _, t := range all {
		if t.UnderlyingSymbol == p.Symbol {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *Fake) GetAccountSummary(_ context.Context, accountID string, _, _ time.Time) (*pb.GetAccountSummaryResponse, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Summaries[accountID], nil
}

func (f *Fake) ImportTransactions(_ context.Context, _ ImportParams) (<-chan ImportEvent, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	ch := make(chan ImportEvent, len(f.ImportEvents))
	for _, ev := range f.ImportEvents {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func (f *Fake) Close() error { return nil }

// Compile-time check that Fake implements Client.
var _ Client = (*Fake)(nil)
