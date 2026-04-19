package grpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/service"
)

// ChainHandler implements pb.ChainServiceServer.
type ChainHandler struct {
	pb.UnimplementedChainServiceServer
	chains service.ChainReader
}

// NewChainHandler creates a ChainHandler backed by the given reader.
func NewChainHandler(chains service.ChainReader) *ChainHandler {
	return &ChainHandler{chains: chains}
}

func (h *ChainHandler) GetChain(ctx context.Context, req *pb.GetChainRequest) (*pb.GetChainResponse, error) {
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	detail, err := h.chains.GetChainDetail(ctx, req.AccountId, req.Id)
	if err != nil {
		return nil, toGRPCError(err)
	}
	return &pb.GetChainResponse{Chain: chainDetailToProto(detail)}, nil
}

func chainDetailToProto(d *domain.ChainDetail) *pb.ChainDetail {
	c := &pb.ChainDetail{
		Id:               d.Chain.ID,
		AccountId:        d.Chain.AccountID,
		UnderlyingSymbol: d.Chain.UnderlyingSymbol,
		CreatedAt:        timestamppb.New(d.Chain.CreatedAt),
		RealizedPnl:      d.PnL.String(),
		Events:           make([]*pb.ChainEvent, len(d.Events)),
	}
	if d.Chain.ClosedAt != nil {
		c.ClosedAt = timestamppb.New(*d.Chain.ClosedAt)
	}
	for i := range d.Events {
		c.Events[i] = chainEventToProto(&d.Events[i])
	}
	return c
}

func chainEventToProto(ev *domain.ChainEvent) *pb.ChainEvent {
	p := &pb.ChainEvent{
		TradeId:     ev.TradeID,
		EventType:   linkTypeToProto(ev.EventType),
		CreditDebit: ev.CreditDebit.String(),
		ExecutedAt:  timestamppb.New(ev.ExecutedAt),
		Legs:        make([]*pb.ChainEventLeg, len(ev.Legs)),
	}
	for i := range ev.Legs {
		p.Legs[i] = chainEventLegToProto(&ev.Legs[i])
	}
	return p
}

func chainEventLegToProto(leg *domain.ChainEventLeg) *pb.ChainEventLeg {
	return &pb.ChainEventLeg{
		Action:     actionToProto(leg.Action),
		Instrument: instrumentToProto(&leg.Instrument),
		Quantity:   leg.Quantity.String(),
	}
}

func linkTypeToProto(lt domain.LinkType) pb.LinkType {
	switch lt {
	case domain.LinkTypeOpen:
		return pb.LinkType_LINK_TYPE_OPEN
	case domain.LinkTypeRoll:
		return pb.LinkType_LINK_TYPE_ROLL
	case domain.LinkTypeAssignment:
		return pb.LinkType_LINK_TYPE_ASSIGNMENT
	case domain.LinkTypeExercise:
		return pb.LinkType_LINK_TYPE_EXERCISE
	case domain.LinkTypeClose:
		return pb.LinkType_LINK_TYPE_CLOSE
	default:
		return pb.LinkType_LINK_TYPE_UNSPECIFIED
	}
}
