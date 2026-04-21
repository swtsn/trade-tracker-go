package grpc

import (
	"context"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	pb "trade-tracker-go/gen/tradetracker/v1"
	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/repository"
)

// maxTradePageSize is the server-side cap on ListTrades results per request.
const maxTradePageSize = 500

// TradeHandler implements pb.TradeServiceServer.
type TradeHandler struct {
	pb.UnimplementedTradeServiceServer
	trades repository.TradeReader
	logger *slog.Logger
}

// NewTradeHandler creates a TradeHandler backed by the given reader.
func NewTradeHandler(trades repository.TradeReader, logger *slog.Logger) *TradeHandler {
	return &TradeHandler{trades: trades, logger: logger}
}

func (h *TradeHandler) ListTrades(ctx context.Context, req *pb.ListTradesRequest) (*pb.ListTradesResponse, error) {
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	if req.OpenOnly && req.ClosedOnly {
		return nil, status.Error(codes.InvalidArgument, "open_only and closed_only are mutually exclusive")
	}

	limit := int(req.PageSize)
	if limit <= 0 || limit > maxTradePageSize {
		limit = maxTradePageSize
	}

	opts := repository.ListTradesOptions{
		Limit:        limit,
		OpenOnly:     req.OpenOnly,
		ClosedOnly:   req.ClosedOnly,
		Symbol:       req.Symbol,
		StrategyType: protoToStrategyType(req.StrategyType),
	}
	if req.OpenedAfter != nil {
		opts.OpenedAfter = req.OpenedAfter.AsTime()
	}
	if req.OpenedBefore != nil {
		opts.OpenedBefore = req.OpenedBefore.AsTime()
	}

	trades, total, err := h.trades.ListByAccountWithTransactions(ctx, req.AccountId, opts)
	if err != nil {
		return nil, toGRPCError(h.logger, err)
	}

	resp := &pb.ListTradesResponse{
		Trades: make([]*pb.Trade, len(trades)),
		Total:  int64(total),
	}
	for i := range trades {
		resp.Trades[i] = tradeToProto(&trades[i])
	}
	return resp, nil
}

func (h *TradeHandler) GetTrade(ctx context.Context, req *pb.GetTradeRequest) (*pb.GetTradeResponse, error) {
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	t, err := h.trades.GetByIDAndAccount(ctx, req.AccountId, req.Id)
	if err != nil {
		return nil, toGRPCError(h.logger, err)
	}
	return &pb.GetTradeResponse{Trade: tradeToProto(t)}, nil
}

func tradeToProto(t *domain.Trade) *pb.Trade {
	p := &pb.Trade{
		Id:               t.ID,
		AccountId:        t.AccountID,
		Broker:           t.Broker,
		StrategyType:     strategyTypeToProto(t.StrategyType),
		UnderlyingSymbol: t.UnderlyingSymbol,
		OpenedAt:         timestamppb.New(t.OpenedAt),
		Notes:            t.Notes,
		Transactions:     make([]*pb.Transaction, len(t.Transactions)),
	}
	if t.ClosedAt != nil {
		p.ClosedAt = timestamppb.New(*t.ClosedAt)
	}
	for i := range t.Transactions {
		p.Transactions[i] = transactionToProto(&t.Transactions[i])
	}
	return p
}

func transactionToProto(tx *domain.Transaction) *pb.Transaction {
	return &pb.Transaction{
		Id:             tx.ID,
		TradeId:        tx.TradeID,
		BrokerTxId:     tx.BrokerTxID,
		BrokerOrderId:  tx.BrokerOrderID,
		Broker:         tx.Broker,
		AccountId:      tx.AccountID,
		Instrument:     instrumentToProto(&tx.Instrument),
		Action:         actionToProto(tx.Action),
		Quantity:       tx.Quantity.String(),
		FillPrice:      tx.FillPrice.String(),
		Fees:           tx.Fees.String(),
		ExecutedAt:     timestamppb.New(tx.ExecutedAt),
		PositionEffect: positionEffectToProto(tx.PositionEffect),
	}
}

func instrumentToProto(inst *domain.Instrument) *pb.Instrument {
	p := &pb.Instrument{
		Symbol:     inst.Symbol,
		AssetClass: assetClassToProto(inst.AssetClass),
	}
	if inst.Option != nil {
		p.Option = &pb.OptionDetails{
			Expiration: timestamppb.New(inst.Option.Expiration),
			Strike:     inst.Option.Strike.String(),
			OptionType: optionTypeToProto(inst.Option.OptionType),
			Multiplier: inst.Option.Multiplier.String(),
			Osi:        inst.Option.OSI,
		}
	}
	if inst.Future != nil {
		p.Future = &pb.FutureDetails{
			ExpiryMonth:  timestamppb.New(inst.Future.ExpiryMonth),
			ExchangeCode: inst.Future.ExchangeCode,
		}
	}
	return p
}

func strategyTypeToProto(s domain.StrategyType) pb.StrategyType {
	switch s {
	case domain.StrategyIronButterfly:
		return pb.StrategyType_STRATEGY_TYPE_IRON_BUTTERFLY
	case domain.StrategyIronCondor:
		return pb.StrategyType_STRATEGY_TYPE_IRON_CONDOR
	case domain.StrategyBrokenHeartButterfly:
		return pb.StrategyType_STRATEGY_TYPE_BROKEN_HEART_BUTTERFLY
	case domain.StrategyButterfly:
		return pb.StrategyType_STRATEGY_TYPE_BUTTERFLY
	case domain.StrategyBrokenWingButterfly:
		return pb.StrategyType_STRATEGY_TYPE_BROKEN_WING_BUTTERFLY
	case domain.StrategyCoveredCall:
		return pb.StrategyType_STRATEGY_TYPE_COVERED_CALL
	case domain.StrategyRatio:
		return pb.StrategyType_STRATEGY_TYPE_RATIO
	case domain.StrategyBackRatio:
		return pb.StrategyType_STRATEGY_TYPE_BACK_RATIO
	case domain.StrategyStraddle:
		return pb.StrategyType_STRATEGY_TYPE_STRADDLE
	case domain.StrategyStrangle:
		return pb.StrategyType_STRATEGY_TYPE_STRANGLE
	case domain.StrategyVertical:
		return pb.StrategyType_STRATEGY_TYPE_VERTICAL
	case domain.StrategyCalendar:
		return pb.StrategyType_STRATEGY_TYPE_CALENDAR
	case domain.StrategyDiagonal:
		return pb.StrategyType_STRATEGY_TYPE_DIAGONAL
	case domain.StrategySingle:
		return pb.StrategyType_STRATEGY_TYPE_SINGLE
	case domain.StrategyStock:
		return pb.StrategyType_STRATEGY_TYPE_STOCK
	case domain.StrategyFuture:
		return pb.StrategyType_STRATEGY_TYPE_FUTURE
	case domain.StrategyUnknown:
		return pb.StrategyType_STRATEGY_TYPE_UNKNOWN
	default:
		// Empty string means unclassified (no strategy assigned yet).
		return pb.StrategyType_STRATEGY_TYPE_UNSPECIFIED
	}
}

// protoToStrategyType maps a proto StrategyType back to a domain.StrategyType for filtering.
// Returns empty string for UNSPECIFIED (meaning no filter applied).
func protoToStrategyType(s pb.StrategyType) domain.StrategyType {
	switch s {
	case pb.StrategyType_STRATEGY_TYPE_IRON_BUTTERFLY:
		return domain.StrategyIronButterfly
	case pb.StrategyType_STRATEGY_TYPE_IRON_CONDOR:
		return domain.StrategyIronCondor
	case pb.StrategyType_STRATEGY_TYPE_BROKEN_HEART_BUTTERFLY:
		return domain.StrategyBrokenHeartButterfly
	case pb.StrategyType_STRATEGY_TYPE_BUTTERFLY:
		return domain.StrategyButterfly
	case pb.StrategyType_STRATEGY_TYPE_BROKEN_WING_BUTTERFLY:
		return domain.StrategyBrokenWingButterfly
	case pb.StrategyType_STRATEGY_TYPE_COVERED_CALL:
		return domain.StrategyCoveredCall
	case pb.StrategyType_STRATEGY_TYPE_RATIO:
		return domain.StrategyRatio
	case pb.StrategyType_STRATEGY_TYPE_BACK_RATIO:
		return domain.StrategyBackRatio
	case pb.StrategyType_STRATEGY_TYPE_STRADDLE:
		return domain.StrategyStraddle
	case pb.StrategyType_STRATEGY_TYPE_STRANGLE:
		return domain.StrategyStrangle
	case pb.StrategyType_STRATEGY_TYPE_VERTICAL:
		return domain.StrategyVertical
	case pb.StrategyType_STRATEGY_TYPE_CALENDAR:
		return domain.StrategyCalendar
	case pb.StrategyType_STRATEGY_TYPE_DIAGONAL:
		return domain.StrategyDiagonal
	case pb.StrategyType_STRATEGY_TYPE_SINGLE:
		return domain.StrategySingle
	case pb.StrategyType_STRATEGY_TYPE_STOCK:
		return domain.StrategyStock
	case pb.StrategyType_STRATEGY_TYPE_FUTURE:
		return domain.StrategyFuture
	case pb.StrategyType_STRATEGY_TYPE_UNKNOWN:
		return domain.StrategyUnknown
	default:
		return ""
	}
}

func actionToProto(a domain.Action) pb.Action {
	switch a {
	case domain.ActionBTO:
		return pb.Action_ACTION_BTO
	case domain.ActionSTO:
		return pb.Action_ACTION_STO
	case domain.ActionBTC:
		return pb.Action_ACTION_BTC
	case domain.ActionSTC:
		return pb.Action_ACTION_STC
	case domain.ActionBuy:
		return pb.Action_ACTION_BUY
	case domain.ActionSell:
		return pb.Action_ACTION_SELL
	case domain.ActionAssignment:
		return pb.Action_ACTION_ASSIGNMENT
	case domain.ActionExpiration:
		return pb.Action_ACTION_EXPIRATION
	case domain.ActionExercise:
		return pb.Action_ACTION_EXERCISE
	default:
		return pb.Action_ACTION_UNSPECIFIED
	}
}

func positionEffectToProto(pe domain.PositionEffect) pb.PositionEffect {
	switch pe {
	case domain.PositionEffectOpening:
		return pb.PositionEffect_POSITION_EFFECT_OPENING
	case domain.PositionEffectClosing:
		return pb.PositionEffect_POSITION_EFFECT_CLOSING
	default:
		return pb.PositionEffect_POSITION_EFFECT_UNSPECIFIED
	}
}

func assetClassToProto(ac domain.AssetClass) pb.AssetClass {
	switch ac {
	case domain.AssetClassEquity:
		return pb.AssetClass_ASSET_CLASS_EQUITY
	case domain.AssetClassEquityOption:
		return pb.AssetClass_ASSET_CLASS_EQUITY_OPTION
	case domain.AssetClassFuture:
		return pb.AssetClass_ASSET_CLASS_FUTURE
	case domain.AssetClassFutureOption:
		return pb.AssetClass_ASSET_CLASS_FUTURE_OPTION
	default:
		return pb.AssetClass_ASSET_CLASS_UNSPECIFIED
	}
}

func optionTypeToProto(ot domain.OptionType) pb.OptionType {
	switch ot {
	case domain.OptionTypeCall:
		return pb.OptionType_OPTION_TYPE_CALL
	case domain.OptionTypePut:
		return pb.OptionType_OPTION_TYPE_PUT
	default:
		return pb.OptionType_OPTION_TYPE_UNSPECIFIED
	}
}
