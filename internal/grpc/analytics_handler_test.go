package grpc_test

import (
	"context"
	"testing"
	"time"

	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/domain"
	grpchandler "trade-tracker-go/internal/grpc"
	"trade-tracker-go/internal/service"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeAnalytics is a test double for service.Analytics.
type fakeAnalytics struct {
	summary       *service.PnLSummary
	symbolPnL     decimal.Decimal
	strategyStats []service.StrategyStats
	err           error
}

func (f *fakeAnalytics) GetPnLSummary(_ context.Context, _ string, _, _ time.Time) (*service.PnLSummary, error) {
	return f.summary, f.err
}

func (f *fakeAnalytics) GetSymbolPnL(_ context.Context, _, _ string, _, _ time.Time) (decimal.Decimal, error) {
	return f.symbolPnL, f.err
}

func (f *fakeAnalytics) GetStrategyPerformance(_ context.Context, _ string, _, _ time.Time) ([]service.StrategyStats, error) {
	return f.strategyStats, f.err
}

var (
	testFrom = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	testTo   = time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
)

// --- GetAccountSummary ---

func TestGetAccountSummary_RequiresAccountID(t *testing.T) {
	h := grpchandler.NewAnalyticsHandler(&fakeAnalytics{}, testLogger)
	_, err := h.GetAccountSummary(context.Background(), &pb.GetAccountSummaryRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetAccountSummary_MissingFrom(t *testing.T) {
	h := grpchandler.NewAnalyticsHandler(&fakeAnalytics{}, testLogger)
	_, err := h.GetAccountSummary(context.Background(), &pb.GetAccountSummaryRequest{
		AccountId: "acc1",
		To:        timestamppb.New(testTo),
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetAccountSummary_MissingTo(t *testing.T) {
	h := grpchandler.NewAnalyticsHandler(&fakeAnalytics{}, testLogger)
	_, err := h.GetAccountSummary(context.Background(), &pb.GetAccountSummaryRequest{
		AccountId: "acc1",
		From:      timestamppb.New(testFrom),
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetAccountSummary_ReturnsSummary(t *testing.T) {
	fake := &fakeAnalytics{
		summary: &service.PnLSummary{
			RealizedPnL:     decimal.NewFromFloat(1250.50),
			CloseFees:       decimal.NewFromFloat(45.20),
			WinRate:         decimal.NewFromFloat(0.72),
			PositionsClosed: 18,
		},
	}
	h := grpchandler.NewAnalyticsHandler(fake, testLogger)

	resp, err := h.GetAccountSummary(context.Background(), &pb.GetAccountSummaryRequest{
		AccountId: "acc1",
		From:      timestamppb.New(testFrom),
		To:        timestamppb.New(testTo),
	})
	require.NoError(t, err)
	assert.Equal(t, "1250.5", resp.RealizedPnl)
	assert.Equal(t, "45.2", resp.CloseFees)
	assert.Equal(t, "0.72", resp.WinRate)
	assert.Equal(t, int32(18), resp.PositionsClosed)
}

func TestGetAccountSummary_ZeroSummary(t *testing.T) {
	fake := &fakeAnalytics{
		summary: &service.PnLSummary{},
	}
	h := grpchandler.NewAnalyticsHandler(fake, testLogger)

	resp, err := h.GetAccountSummary(context.Background(), &pb.GetAccountSummaryRequest{
		AccountId: "acc1",
		From:      timestamppb.New(testFrom),
		To:        timestamppb.New(testTo),
	})
	require.NoError(t, err)
	assert.Equal(t, "0", resp.RealizedPnl)
	assert.Equal(t, int32(0), resp.PositionsClosed)
}

// --- GetSymbolPerformance ---

func TestGetAccountSummary_InvertedRange(t *testing.T) {
	h := grpchandler.NewAnalyticsHandler(&fakeAnalytics{}, testLogger)
	_, err := h.GetAccountSummary(context.Background(), &pb.GetAccountSummaryRequest{
		AccountId: "acc1",
		From:      timestamppb.New(testTo),
		To:        timestamppb.New(testFrom),
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetSymbolPerformance_RequiresAccountID(t *testing.T) {
	h := grpchandler.NewAnalyticsHandler(&fakeAnalytics{}, testLogger)
	_, err := h.GetSymbolPerformance(context.Background(), &pb.GetSymbolPerformanceRequest{
		Symbol: "SPY",
		From:   timestamppb.New(testFrom),
		To:     timestamppb.New(testTo),
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetSymbolPerformance_RequiresSymbol(t *testing.T) {
	h := grpchandler.NewAnalyticsHandler(&fakeAnalytics{}, testLogger)
	_, err := h.GetSymbolPerformance(context.Background(), &pb.GetSymbolPerformanceRequest{
		AccountId: "acc1",
		From:      timestamppb.New(testFrom),
		To:        timestamppb.New(testTo),
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetSymbolPerformance_MissingFrom(t *testing.T) {
	h := grpchandler.NewAnalyticsHandler(&fakeAnalytics{}, testLogger)
	_, err := h.GetSymbolPerformance(context.Background(), &pb.GetSymbolPerformanceRequest{
		AccountId: "acc1",
		Symbol:    "SPY",
		To:        timestamppb.New(testTo),
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetSymbolPerformance_MissingTo(t *testing.T) {
	h := grpchandler.NewAnalyticsHandler(&fakeAnalytics{}, testLogger)
	_, err := h.GetSymbolPerformance(context.Background(), &pb.GetSymbolPerformanceRequest{
		AccountId: "acc1",
		Symbol:    "SPY",
		From:      timestamppb.New(testFrom),
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetSymbolPerformance_InvertedRange(t *testing.T) {
	h := grpchandler.NewAnalyticsHandler(&fakeAnalytics{}, testLogger)
	_, err := h.GetSymbolPerformance(context.Background(), &pb.GetSymbolPerformanceRequest{
		AccountId: "acc1",
		Symbol:    "SPY",
		From:      timestamppb.New(testTo),
		To:        timestamppb.New(testFrom),
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetSymbolPerformance_ReturnsPnL(t *testing.T) {
	fake := &fakeAnalytics{symbolPnL: decimal.NewFromFloat(500.25)}
	h := grpchandler.NewAnalyticsHandler(fake, testLogger)

	resp, err := h.GetSymbolPerformance(context.Background(), &pb.GetSymbolPerformanceRequest{
		AccountId: "acc1",
		Symbol:    "SPY",
		From:      timestamppb.New(testFrom),
		To:        timestamppb.New(testTo),
	})
	require.NoError(t, err)
	assert.Equal(t, "500.25", resp.RealizedPnl)
}

func TestGetSymbolPerformance_ZeroPnL(t *testing.T) {
	fake := &fakeAnalytics{symbolPnL: decimal.Zero}
	h := grpchandler.NewAnalyticsHandler(fake, testLogger)

	resp, err := h.GetSymbolPerformance(context.Background(), &pb.GetSymbolPerformanceRequest{
		AccountId: "acc1",
		Symbol:    "SPY",
		From:      timestamppb.New(testFrom),
		To:        timestamppb.New(testTo),
	})
	require.NoError(t, err)
	assert.Equal(t, "0", resp.RealizedPnl)
}

// --- GetStrategyPerformance ---

func TestGetStrategyPerformance_RequiresAccountID(t *testing.T) {
	h := grpchandler.NewAnalyticsHandler(&fakeAnalytics{}, testLogger)
	_, err := h.GetStrategyPerformance(context.Background(), &pb.GetStrategyPerformanceRequest{
		From: timestamppb.New(testFrom),
		To:   timestamppb.New(testTo),
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetStrategyPerformance_MissingFrom(t *testing.T) {
	h := grpchandler.NewAnalyticsHandler(&fakeAnalytics{}, testLogger)
	_, err := h.GetStrategyPerformance(context.Background(), &pb.GetStrategyPerformanceRequest{
		AccountId: "acc1",
		To:        timestamppb.New(testTo),
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetStrategyPerformance_MissingTo(t *testing.T) {
	h := grpchandler.NewAnalyticsHandler(&fakeAnalytics{}, testLogger)
	_, err := h.GetStrategyPerformance(context.Background(), &pb.GetStrategyPerformanceRequest{
		AccountId: "acc1",
		From:      timestamppb.New(testFrom),
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetStrategyPerformance_InvertedRange(t *testing.T) {
	h := grpchandler.NewAnalyticsHandler(&fakeAnalytics{}, testLogger)
	_, err := h.GetStrategyPerformance(context.Background(), &pb.GetStrategyPerformanceRequest{
		AccountId: "acc1",
		From:      timestamppb.New(testTo),
		To:        timestamppb.New(testFrom),
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetStrategyPerformance_ReturnsStats(t *testing.T) {
	fake := &fakeAnalytics{
		strategyStats: []service.StrategyStats{
			{
				StrategyType: domain.StrategyType("vertical"),
				Count:        10,
				WinRate:      decimal.NewFromFloat(0.8),
				AveragePnL:   decimal.NewFromFloat(125.50),
				TotalPnL:     decimal.NewFromFloat(1255.00),
			},
			{
				StrategyType: domain.StrategyType("single"),
				Count:        5,
				WinRate:      decimal.NewFromFloat(0.6),
				AveragePnL:   decimal.NewFromFloat(-50.00),
				TotalPnL:     decimal.NewFromFloat(-250.00),
			},
		},
	}
	h := grpchandler.NewAnalyticsHandler(fake, testLogger)

	resp, err := h.GetStrategyPerformance(context.Background(), &pb.GetStrategyPerformanceRequest{
		AccountId: "acc1",
		From:      timestamppb.New(testFrom),
		To:        timestamppb.New(testTo),
	})
	require.NoError(t, err)
	require.Len(t, resp.Stats, 2)

	assert.Equal(t, "vertical", resp.Stats[0].StrategyType)
	assert.Equal(t, int32(10), resp.Stats[0].Count)
	assert.Equal(t, "0.8", resp.Stats[0].WinRate)
	assert.Equal(t, "125.5", resp.Stats[0].AveragePnl)
	assert.Equal(t, "1255", resp.Stats[0].TotalPnl)

	assert.Equal(t, "single", resp.Stats[1].StrategyType)
	assert.Equal(t, int32(5), resp.Stats[1].Count)
	assert.Equal(t, "-250", resp.Stats[1].TotalPnl)
}

func TestGetStrategyPerformance_EmptyReturnsEmptyStats(t *testing.T) {
	fake := &fakeAnalytics{strategyStats: nil}
	h := grpchandler.NewAnalyticsHandler(fake, testLogger)

	resp, err := h.GetStrategyPerformance(context.Background(), &pb.GetStrategyPerformanceRequest{
		AccountId: "acc1",
		From:      timestamppb.New(testFrom),
		To:        timestamppb.New(testTo),
	})
	require.NoError(t, err)
	assert.Empty(t, resp.Stats)
}
