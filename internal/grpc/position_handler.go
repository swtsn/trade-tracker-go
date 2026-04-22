package grpc

import (
	"context"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/service"
)

// PositionHandler implements pb.PositionServiceServer.
type PositionHandler struct {
	pb.UnimplementedPositionServiceServer
	positions service.PositionReader
	logger    *slog.Logger
}

// NewPositionHandler creates a PositionHandler backed by the given reader.
func NewPositionHandler(positions service.PositionReader, logger *slog.Logger) *PositionHandler {
	return &PositionHandler{positions: positions, logger: logger}
}

func (h *PositionHandler) ListPositions(ctx context.Context, req *pb.ListPositionsRequest) (*pb.ListPositionsResponse, error) {
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}

	var openOnly, closedOnly bool
	switch req.Status {
	case pb.PositionStatus_POSITION_STATUS_OPEN:
		openOnly = true
	case pb.PositionStatus_POSITION_STATUS_CLOSED:
		closedOnly = true
	}

	positions, err := h.positions.ListPositions(ctx, req.AccountId, openOnly, closedOnly)
	if err != nil {
		return nil, toGRPCError(h.logger, err)
	}

	resp := &pb.ListPositionsResponse{
		Positions: make([]*pb.Position, len(positions)),
	}
	for i := range positions {
		resp.Positions[i] = positionToProto(&positions[i])
	}
	return resp, nil
}

func (h *PositionHandler) GetPosition(ctx context.Context, req *pb.GetPositionRequest) (*pb.GetPositionResponse, error) {
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	pos, err := h.positions.GetPosition(ctx, req.AccountId, req.Id)
	if err != nil {
		return nil, toGRPCError(h.logger, err)
	}
	return &pb.GetPositionResponse{Position: positionToProto(pos)}, nil
}

func positionToProto(pos *domain.Position) *pb.Position {
	p := &pb.Position{
		Id:                  pos.ID,
		AccountId:           pos.AccountID,
		ChainId:             pos.ChainID,
		UnderlyingSymbol:    pos.UnderlyingSymbol,
		StrategyType:        strategyTypeToProto(pos.StrategyType),
		CostBasis:           pos.CostBasis.String(),
		OpenedAt:            timestamppb.New(pos.OpenedAt),
		ChainAttributionGap: pos.ChainAttributionGap,
	}
	if pos.ClosedAt != nil {
		p.ClosedAt = timestamppb.New(*pos.ClosedAt)
		p.RealizedPnl = pos.RealizedPnL.String()
	}
	return p
}
