package grpc_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/domain"
	grpchandler "trade-tracker-go/internal/grpc"
)

// fakePositionReader is a test double for service.PositionReader.
type fakePositionReader struct {
	positions []domain.Position
	err       error
}

func (f *fakePositionReader) GetPosition(_ context.Context, accountID, id string) (*domain.Position, error) {
	if f.err != nil {
		return nil, f.err
	}
	for _, p := range f.positions {
		if p.ID == id && p.AccountID == accountID {
			return &p, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakePositionReader) ListPositions(_ context.Context, accountID string, openOnly, closedOnly bool) ([]domain.Position, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []domain.Position
	for _, p := range f.positions {
		if p.AccountID != accountID {
			continue
		}
		if openOnly && p.ClosedAt != nil {
			continue
		}
		if closedOnly && p.ClosedAt == nil {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func makeTestPosition(id, accountID, chainID string, closedAt *time.Time) domain.Position {
	pos := domain.Position{
		ID:               id,
		AccountID:        accountID,
		ChainID:          chainID,
		UnderlyingSymbol: "SPY",
		StrategyType:     domain.StrategySingle,
		CostBasis:        decimal.NewFromFloat(-3.50),
		RealizedPnL:      decimal.Zero,
		OpenedAt:         time.Now().UTC().Truncate(time.Second),
		ClosedAt:         closedAt,
	}
	if closedAt != nil {
		pos.RealizedPnL = decimal.NewFromFloat(2.00)
	}
	return pos
}

func TestListPositions_RequiresAccountID(t *testing.T) {
	h := grpchandler.NewPositionHandler(&fakePositionReader{}, testLogger)
	_, err := h.ListPositions(context.Background(), &pb.ListPositionsRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestListPositions_ReturnsAll(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fake := &fakePositionReader{
		positions: []domain.Position{
			makeTestPosition("p1", "acc1", "chain1", nil),
			makeTestPosition("p2", "acc1", "chain2", &now),
		},
	}
	h := grpchandler.NewPositionHandler(fake, testLogger)

	resp, err := h.ListPositions(context.Background(), &pb.ListPositionsRequest{AccountId: "acc1"})
	require.NoError(t, err)
	assert.Len(t, resp.Positions, 2)
}

func TestListPositions_OpenFilter(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fake := &fakePositionReader{
		positions: []domain.Position{
			makeTestPosition("open1", "acc1", "chain1", nil),
			makeTestPosition("closed1", "acc1", "chain2", &now),
		},
	}
	h := grpchandler.NewPositionHandler(fake, testLogger)

	resp, err := h.ListPositions(context.Background(), &pb.ListPositionsRequest{
		AccountId: "acc1",
		Status:    pb.PositionStatus_POSITION_STATUS_OPEN,
	})
	require.NoError(t, err)
	require.Len(t, resp.Positions, 1)
	assert.Equal(t, "open1", resp.Positions[0].Id)
	assert.Nil(t, resp.Positions[0].ClosedAt)
	assert.Equal(t, "0", resp.Positions[0].RealizedPnl) // open position has zero realized P&L
}

func TestListPositions_ClosedFilter(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fake := &fakePositionReader{
		positions: []domain.Position{
			makeTestPosition("open1", "acc1", "chain1", nil),
			makeTestPosition("closed1", "acc1", "chain2", &now),
		},
	}
	h := grpchandler.NewPositionHandler(fake, testLogger)

	resp, err := h.ListPositions(context.Background(), &pb.ListPositionsRequest{
		AccountId: "acc1",
		Status:    pb.PositionStatus_POSITION_STATUS_CLOSED,
	})
	require.NoError(t, err)
	require.Len(t, resp.Positions, 1)
	assert.Equal(t, "closed1", resp.Positions[0].Id)
	assert.NotNil(t, resp.Positions[0].ClosedAt)
	assert.Equal(t, "2", resp.Positions[0].RealizedPnl) // closed: P&L included
}

func TestListPositions_Empty(t *testing.T) {
	h := grpchandler.NewPositionHandler(&fakePositionReader{}, testLogger)
	resp, err := h.ListPositions(context.Background(), &pb.ListPositionsRequest{AccountId: "acc1"})
	require.NoError(t, err)
	assert.NotNil(t, resp.Positions)
	assert.Empty(t, resp.Positions)
}

func TestGetPosition_RequiresAccountID(t *testing.T) {
	h := grpchandler.NewPositionHandler(&fakePositionReader{}, testLogger)
	_, err := h.GetPosition(context.Background(), &pb.GetPositionRequest{Id: "p1"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetPosition_RequiresID(t *testing.T) {
	h := grpchandler.NewPositionHandler(&fakePositionReader{}, testLogger)
	_, err := h.GetPosition(context.Background(), &pb.GetPositionRequest{AccountId: "acc1"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetPosition_Found(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fake := &fakePositionReader{
		positions: []domain.Position{
			makeTestPosition("p1", "acc1", "chain1", &now),
		},
	}
	h := grpchandler.NewPositionHandler(fake, testLogger)

	resp, err := h.GetPosition(context.Background(), &pb.GetPositionRequest{AccountId: "acc1", Id: "p1"})
	require.NoError(t, err)
	assert.Equal(t, "p1", resp.Position.Id)
	assert.Equal(t, "chain1", resp.Position.ChainId)
	assert.NotNil(t, resp.Position.ClosedAt)
	assert.Equal(t, "2", resp.Position.RealizedPnl)
}

func TestGetPosition_NotFound(t *testing.T) {
	h := grpchandler.NewPositionHandler(&fakePositionReader{}, testLogger)
	_, err := h.GetPosition(context.Background(), &pb.GetPositionRequest{AccountId: "acc1", Id: "missing"})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestGetPosition_WrongAccount(t *testing.T) {
	fake := &fakePositionReader{
		positions: []domain.Position{makeTestPosition("p1", "acc1", "chain1", nil)},
	}
	h := grpchandler.NewPositionHandler(fake, testLogger)

	_, err := h.GetPosition(context.Background(), &pb.GetPositionRequest{AccountId: "other", Id: "p1"})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}
