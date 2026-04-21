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

// fakeChainReader is a test double for service.ChainReader.
type fakeChainReader struct {
	detail *domain.ChainDetail
	err    error
}

func (f *fakeChainReader) GetChainDetail(_ context.Context, accountID, chainID string) (*domain.ChainDetail, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.detail == nil || f.detail.Chain.ID != chainID || f.detail.Chain.AccountID != accountID {
		return nil, domain.ErrNotFound
	}
	return f.detail, nil
}

func makeTestChainDetail(chainID, accountID string) *domain.ChainDetail {
	exp := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	openedAt := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	chain := &domain.Chain{
		ID:               chainID,
		AccountID:        accountID,
		UnderlyingSymbol: "SPY",
		OriginalTradeID:  "trade1",
		CreatedAt:        openedAt,
	}
	return &domain.ChainDetail{
		Chain: chain,
		PnL:   decimal.NewFromFloat(1.50),
		Events: []domain.ChainEvent{
			{
				TradeID:     "trade1",
				EventType:   domain.LinkTypeOpen,
				CreditDebit: decimal.NewFromFloat(3.50),
				ExecutedAt:  openedAt,
				Legs: []domain.ChainEventLeg{
					{
						Action: domain.ActionSTO,
						Instrument: domain.Instrument{
							Symbol:     "SPY",
							AssetClass: domain.AssetClassEquityOption,
							Option: &domain.OptionDetails{
								Expiration: exp,
								Strike:     decimal.NewFromFloat(450.0),
								OptionType: domain.OptionTypePut,
								Multiplier: decimal.NewFromInt(100),
							},
						},
						Quantity: decimal.NewFromInt(1),
					},
				},
			},
		},
	}
}

func TestGetChain_RequiresAccountID(t *testing.T) {
	h := grpchandler.NewChainHandler(&fakeChainReader{}, testLogger)
	_, err := h.GetChain(context.Background(), &pb.GetChainRequest{Id: "chain1"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetChain_RequiresID(t *testing.T) {
	h := grpchandler.NewChainHandler(&fakeChainReader{}, testLogger)
	_, err := h.GetChain(context.Background(), &pb.GetChainRequest{AccountId: "acc1"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetChain_Found(t *testing.T) {
	detail := makeTestChainDetail("chain1", "acc1")
	h := grpchandler.NewChainHandler(&fakeChainReader{detail: detail}, testLogger)

	resp, err := h.GetChain(context.Background(), &pb.GetChainRequest{AccountId: "acc1", Id: "chain1"})
	require.NoError(t, err)

	c := resp.Chain
	assert.Equal(t, "chain1", c.Id)
	assert.Equal(t, "acc1", c.AccountId)
	assert.Equal(t, "SPY", c.UnderlyingSymbol)
	assert.Nil(t, c.ClosedAt)
	assert.Equal(t, "1.5", c.RealizedPnl)

	require.Len(t, c.Events, 1)
	ev := c.Events[0]
	assert.Equal(t, "trade1", ev.TradeId)
	assert.Equal(t, pb.LinkType_LINK_TYPE_OPEN, ev.EventType)
	assert.Equal(t, "3.5", ev.CreditDebit)

	require.Len(t, ev.Legs, 1)
	leg := ev.Legs[0]
	assert.Equal(t, pb.Action_ACTION_STO, leg.Action)
	assert.Equal(t, "1", leg.Quantity)
	require.NotNil(t, leg.Instrument.Option)
	assert.Equal(t, "450", leg.Instrument.Option.Strike)
	assert.Equal(t, pb.OptionType_OPTION_TYPE_PUT, leg.Instrument.Option.OptionType)
}

func TestGetChain_ClosedChain(t *testing.T) {
	detail := makeTestChainDetail("chain1", "acc1")
	closedAt := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	detail.Chain.ClosedAt = &closedAt

	h := grpchandler.NewChainHandler(&fakeChainReader{detail: detail}, testLogger)

	resp, err := h.GetChain(context.Background(), &pb.GetChainRequest{AccountId: "acc1", Id: "chain1"})
	require.NoError(t, err)
	assert.NotNil(t, resp.Chain.ClosedAt)
}

func TestGetChain_NotFound(t *testing.T) {
	h := grpchandler.NewChainHandler(&fakeChainReader{}, testLogger)
	_, err := h.GetChain(context.Background(), &pb.GetChainRequest{AccountId: "acc1", Id: "missing"})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestGetChain_WrongAccount(t *testing.T) {
	detail := makeTestChainDetail("chain1", "acc1")
	h := grpchandler.NewChainHandler(&fakeChainReader{detail: detail}, testLogger)

	_, err := h.GetChain(context.Background(), &pb.GetChainRequest{AccountId: "other", Id: "chain1"})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}
