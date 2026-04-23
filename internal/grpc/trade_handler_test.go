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
	"trade-tracker-go/internal/repository"
)

// fakeTradeReader is a test double for repository.TradeReader.
type fakeTradeReader struct {
	trades []domain.Trade
	err    error
}

func (f *fakeTradeReader) GetByIDAndAccount(_ context.Context, accountID, id string) (*domain.Trade, error) {
	if f.err != nil {
		return nil, f.err
	}
	for _, t := range f.trades {
		if t.ID == id && t.AccountID == accountID {
			return &t, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeTradeReader) ListByAccountWithTransactions(_ context.Context, accountID string, opts repository.ListTradesOptions) ([]domain.Trade, int, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	var out []domain.Trade
	for _, t := range f.trades {
		if t.AccountID != accountID {
			continue
		}
		if opts.Symbol != "" && t.UnderlyingSymbol != opts.Symbol {
			continue
		}
		out = append(out, t)
	}
	return out, len(out), nil
}

func makeTestTrade(id, accountID, symbol string) domain.Trade {
	return domain.Trade{
		ID:               id,
		AccountID:        accountID,
		Broker:           "tastytrade",
		UnderlyingSymbol: symbol,
		ExecutedAt:       time.Now().UTC().Truncate(time.Second),
		Transactions:     []domain.Transaction{},
	}
}

func TestListTrades_RequiresAccountID(t *testing.T) {
	h := grpchandler.NewTradeHandler(&fakeTradeReader{}, testLogger)
	_, err := h.ListTrades(context.Background(), &pb.ListTradesRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestListTrades_ReturnsTrades(t *testing.T) {
	fake := &fakeTradeReader{
		trades: []domain.Trade{
			makeTestTrade("t1", "acc1", "SPY"),
			makeTestTrade("t2", "acc1", "AAPL"),
		},
	}
	h := grpchandler.NewTradeHandler(fake, testLogger)

	resp, err := h.ListTrades(context.Background(), &pb.ListTradesRequest{AccountId: "acc1"})
	require.NoError(t, err)
	require.Len(t, resp.Trades, 2)
	assert.Equal(t, "t1", resp.Trades[0].Id)
	assert.Equal(t, "SPY", resp.Trades[0].UnderlyingSymbol)
	assert.Equal(t, "t2", resp.Trades[1].Id)
}

func TestListTrades_SymbolFilter(t *testing.T) {
	fake := &fakeTradeReader{
		trades: []domain.Trade{
			makeTestTrade("t1", "acc1", "SPY"),
			makeTestTrade("t2", "acc1", "AAPL"),
		},
	}
	h := grpchandler.NewTradeHandler(fake, testLogger)

	resp, err := h.ListTrades(context.Background(), &pb.ListTradesRequest{
		AccountId: "acc1",
		Symbol:    "SPY",
	})
	require.NoError(t, err)
	require.Len(t, resp.Trades, 1)
	assert.Equal(t, "t1", resp.Trades[0].Id)
}

func TestListTrades_Empty(t *testing.T) {
	h := grpchandler.NewTradeHandler(&fakeTradeReader{}, testLogger)
	resp, err := h.ListTrades(context.Background(), &pb.ListTradesRequest{AccountId: "acc1"})
	require.NoError(t, err)
	assert.NotNil(t, resp.Trades)
	assert.Empty(t, resp.Trades)
}

func TestGetTrade_RequiresAccountID(t *testing.T) {
	h := grpchandler.NewTradeHandler(&fakeTradeReader{}, testLogger)
	_, err := h.GetTrade(context.Background(), &pb.GetTradeRequest{Id: "t1"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetTrade_RequiresID(t *testing.T) {
	h := grpchandler.NewTradeHandler(&fakeTradeReader{}, testLogger)
	_, err := h.GetTrade(context.Background(), &pb.GetTradeRequest{AccountId: "acc1"})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetTrade_Found(t *testing.T) {
	exp := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	strike := decimal.NewFromFloat(450.0)
	trade := domain.Trade{
		ID:               "t1",
		AccountID:        "acc1",
		Broker:           "tastytrade",
		UnderlyingSymbol: "SPY",
		ExecutedAt:       time.Now().UTC().Truncate(time.Second),
		Transactions: []domain.Transaction{
			{
				ID:        "tx1",
				TradeID:   "t1",
				AccountID: "acc1",
				Broker:    "tastytrade",
				Instrument: domain.Instrument{
					Symbol:     "SPY",
					AssetClass: domain.AssetClassEquityOption,
					Option: &domain.OptionDetails{
						Expiration: exp,
						Strike:     strike,
						OptionType: domain.OptionTypePut,
						Multiplier: decimal.NewFromInt(100),
					},
				},
				Action:         domain.ActionSTO,
				Quantity:       decimal.NewFromInt(1),
				FillPrice:      decimal.NewFromFloat(3.50),
				Fees:           decimal.NewFromFloat(0.65),
				ExecutedAt:     time.Now().UTC().Truncate(time.Second),
				PositionEffect: domain.PositionEffectOpening,
			},
		},
	}
	h := grpchandler.NewTradeHandler(&fakeTradeReader{trades: []domain.Trade{trade}}, testLogger)

	resp, err := h.GetTrade(context.Background(), &pb.GetTradeRequest{AccountId: "acc1", Id: "t1"})
	require.NoError(t, err)
	assert.Equal(t, "t1", resp.Trade.Id)
	assert.Equal(t, "SPY", resp.Trade.UnderlyingSymbol)
	require.Len(t, resp.Trade.Transactions, 1)
	assert.Equal(t, "tx1", resp.Trade.Transactions[0].Id)
	require.NotNil(t, resp.Trade.Transactions[0].Instrument.Option)
	assert.Equal(t, "450", resp.Trade.Transactions[0].Instrument.Option.Strike)
	assert.Equal(t, pb.OptionType_OPTION_TYPE_PUT, resp.Trade.Transactions[0].Instrument.Option.OptionType)
}

func TestGetTrade_NotFound(t *testing.T) {
	h := grpchandler.NewTradeHandler(&fakeTradeReader{}, testLogger)
	_, err := h.GetTrade(context.Background(), &pb.GetTradeRequest{AccountId: "acc1", Id: "missing"})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestGetTrade_WrongAccount(t *testing.T) {
	trade := makeTestTrade("t1", "acc1", "SPY")
	h := grpchandler.NewTradeHandler(&fakeTradeReader{trades: []domain.Trade{trade}}, testLogger)

	_, err := h.GetTrade(context.Background(), &pb.GetTradeRequest{AccountId: "other-acc", Id: "t1"})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}
