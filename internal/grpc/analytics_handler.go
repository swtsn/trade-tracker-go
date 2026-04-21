package grpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/service"
)

// AnalyticsHandler implements pb.AnalyticsServiceServer.
type AnalyticsHandler struct {
	pb.UnimplementedAnalyticsServiceServer
	analytics service.Analytics
}

// NewAnalyticsHandler creates an AnalyticsHandler backed by the given analytics service.
func NewAnalyticsHandler(analytics service.Analytics) *AnalyticsHandler {
	return &AnalyticsHandler{analytics: analytics}
}

func (h *AnalyticsHandler) GetAccountSummary(ctx context.Context, req *pb.GetAccountSummaryRequest) (*pb.GetAccountSummaryResponse, error) {
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	if req.From == nil {
		return nil, status.Error(codes.InvalidArgument, "from is required")
	}
	if req.To == nil {
		return nil, status.Error(codes.InvalidArgument, "to is required")
	}
	from := req.From.AsTime()
	to := req.To.AsTime()
	if to.Before(from) {
		return nil, status.Error(codes.InvalidArgument, "to must not be before from")
	}

	summary, err := h.analytics.GetPnLSummary(ctx, req.AccountId, from, to)
	if err != nil {
		return nil, toGRPCError(err)
	}

	return &pb.GetAccountSummaryResponse{
		RealizedPnl:     summary.RealizedPnL.String(),
		CloseFees:       summary.CloseFees.String(),
		WinRate:         summary.WinRate.String(),
		PositionsClosed: summary.PositionsClosed,
	}, nil
}

func (h *AnalyticsHandler) GetSymbolPerformance(ctx context.Context, req *pb.GetSymbolPerformanceRequest) (*pb.GetSymbolPerformanceResponse, error) {
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	if req.Symbol == "" {
		return nil, status.Error(codes.InvalidArgument, "symbol is required")
	}
	if req.From == nil {
		return nil, status.Error(codes.InvalidArgument, "from is required")
	}
	if req.To == nil {
		return nil, status.Error(codes.InvalidArgument, "to is required")
	}
	if req.To.AsTime().Before(req.From.AsTime()) {
		return nil, status.Error(codes.InvalidArgument, "to must not be before from")
	}

	pnl, err := h.analytics.GetSymbolPnL(ctx, req.AccountId, req.Symbol, req.From.AsTime(), req.To.AsTime())
	if err != nil {
		return nil, toGRPCError(err)
	}

	return &pb.GetSymbolPerformanceResponse{RealizedPnl: pnl.String()}, nil
}

func (h *AnalyticsHandler) GetStrategyPerformance(ctx context.Context, req *pb.GetStrategyPerformanceRequest) (*pb.GetStrategyPerformanceResponse, error) {
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	if req.From == nil {
		return nil, status.Error(codes.InvalidArgument, "from is required")
	}
	if req.To == nil {
		return nil, status.Error(codes.InvalidArgument, "to is required")
	}
	if req.To.AsTime().Before(req.From.AsTime()) {
		return nil, status.Error(codes.InvalidArgument, "to must not be before from")
	}

	stats, err := h.analytics.GetStrategyPerformance(ctx, req.AccountId, req.From.AsTime(), req.To.AsTime())
	if err != nil {
		return nil, toGRPCError(err)
	}

	resp := &pb.GetStrategyPerformanceResponse{
		Stats: make([]*pb.StrategyStats, len(stats)),
	}
	for i, s := range stats {
		resp.Stats[i] = &pb.StrategyStats{
			StrategyType: string(s.StrategyType),
			Count:        int32(s.Count),
			WinRate:      s.WinRate.String(),
			AveragePnl:   s.AveragePnL.String(),
			TotalPnl:     s.TotalPnL.String(),
		}
	}
	return resp, nil
}
