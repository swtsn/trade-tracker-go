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
	analytics service.AccountSummaryReader
}

// NewAnalyticsHandler creates an AnalyticsHandler backed by the given reader.
func NewAnalyticsHandler(analytics service.AccountSummaryReader) *AnalyticsHandler {
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
