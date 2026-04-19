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
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "trade-tracker-go/gen/tradetracker/v1"
	grpchandler "trade-tracker-go/internal/grpc"
	"trade-tracker-go/internal/service"
)

// fakeAccountSummaryReader is a test double for service.AccountSummaryReader.
type fakeAccountSummaryReader struct {
	summary *service.PnLSummary
	err     error
}

func (f *fakeAccountSummaryReader) GetPnLSummary(_ context.Context, _ string, _, _ time.Time) (*service.PnLSummary, error) {
	return f.summary, f.err
}

func TestGetAccountSummary_RequiresAccountID(t *testing.T) {
	h := grpchandler.NewAnalyticsHandler(&fakeAccountSummaryReader{})
	_, err := h.GetAccountSummary(context.Background(), &pb.GetAccountSummaryRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetAccountSummary_ReturnsSummary(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	fake := &fakeAccountSummaryReader{
		summary: &service.PnLSummary{
			RealizedPnL:     decimal.NewFromFloat(1250.50),
			CloseFees:       decimal.NewFromFloat(45.20),
			WinRate:         decimal.NewFromFloat(0.72),
			PositionsClosed: 18,
		},
	}
	h := grpchandler.NewAnalyticsHandler(fake)

	resp, err := h.GetAccountSummary(context.Background(), &pb.GetAccountSummaryRequest{
		AccountId: "acc1",
		From:      timestamppb.New(from),
		To:        timestamppb.New(to),
	})
	require.NoError(t, err)
	assert.Equal(t, "1250.5", resp.RealizedPnl)
	assert.Equal(t, "45.2", resp.CloseFees)
	assert.Equal(t, "0.72", resp.WinRate)
	assert.Equal(t, int32(18), resp.PositionsClosed)
}

func TestGetAccountSummary_ZeroSummary(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	fake := &fakeAccountSummaryReader{
		summary: &service.PnLSummary{
			RealizedPnL:     decimal.Zero,
			CloseFees:       decimal.Zero,
			WinRate:         decimal.Zero,
			PositionsClosed: 0,
		},
	}
	h := grpchandler.NewAnalyticsHandler(fake)

	resp, err := h.GetAccountSummary(context.Background(), &pb.GetAccountSummaryRequest{
		AccountId: "acc1",
		From:      timestamppb.New(from),
		To:        timestamppb.New(to),
	})
	require.NoError(t, err)
	assert.Equal(t, "0", resp.RealizedPnl)
	assert.Equal(t, int32(0), resp.PositionsClosed)
}

func TestGetAccountSummary_MissingFrom(t *testing.T) {
	h := grpchandler.NewAnalyticsHandler(&fakeAccountSummaryReader{})
	_, err := h.GetAccountSummary(context.Background(), &pb.GetAccountSummaryRequest{
		AccountId: "acc1",
		To:        timestamppb.New(time.Now()),
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetAccountSummary_MissingTo(t *testing.T) {
	h := grpchandler.NewAnalyticsHandler(&fakeAccountSummaryReader{})
	_, err := h.GetAccountSummary(context.Background(), &pb.GetAccountSummaryRequest{
		AccountId: "acc1",
		From:      timestamppb.New(time.Now()),
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}
