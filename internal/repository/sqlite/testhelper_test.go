package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/repository/sqlite"
)

// openTestDB opens an in-memory SQLite database with all migrations applied.
func openTestDB(t *testing.T) *sqlite.Repos {
	t.Helper()
	repos, err := sqlite.OpenRepos(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = repos.Close() })
	return repos
}

// seedAccount inserts and returns a test account.
func seedAccount(t *testing.T, ctx context.Context, repos *sqlite.Repos) *domain.Account {
	t.Helper()
	acc := &domain.Account{
		ID:            uuid.New().String(),
		Broker:        "tastytrade",
		AccountNumber: "ABC123",
		Name:          "Test Account",
		CreatedAt:     time.Now().UTC().Truncate(time.Second),
	}
	require.NoError(t, repos.Accounts.Create(ctx, acc))
	return acc
}

// seedEquityInstrument creates and upserts an equity instrument.
func seedEquityInstrument(t *testing.T, ctx context.Context, repos *sqlite.Repos, symbol string) domain.Instrument {
	t.Helper()
	inst := domain.Instrument{
		Symbol:     symbol,
		AssetClass: domain.AssetClassEquity,
		Option:     nil,
		Future:     nil,
	}
	require.NoError(t, repos.Instruments.Upsert(ctx, &inst))
	return inst
}

// seedOptionInstrument creates and upserts an equity option instrument.
func seedOptionInstrument(t *testing.T, ctx context.Context, repos *sqlite.Repos, symbol string, strike float64, optType domain.OptionType, exp time.Time) domain.Instrument {
	t.Helper()
	inst := domain.Instrument{
		Symbol:     symbol,
		AssetClass: domain.AssetClassEquityOption,
		Option: &domain.OptionDetails{
			Expiration: exp.UTC().Truncate(time.Second),
			Strike:     decimal.NewFromFloat(strike),
			OptionType: optType,
			Multiplier: decimal.NewFromInt(100),
			OSI:        symbol + " " + exp.Format("010206") + string(optType) + "00150000",
		},
	}
	require.NoError(t, repos.Instruments.Upsert(ctx, &inst))
	return inst
}

// seedTrade creates a trade (without transactions).
func seedTrade(t *testing.T, ctx context.Context, repos *sqlite.Repos, acc *domain.Account, strategy domain.StrategyType, openedAt time.Time) *domain.Trade {
	t.Helper()
	trade := &domain.Trade{
		ID:           uuid.New().String(),
		AccountID:    acc.ID,
		Broker:       acc.Broker,
		StrategyType: strategy,
		OpenedAt:     openedAt.UTC().Truncate(time.Second),
		Notes:        "",
	}
	require.NoError(t, repos.Trades.Create(ctx, trade))
	return trade
}

// seedChain creates a chain anchored to the given trade.
func seedChain(t *testing.T, ctx context.Context, repos *sqlite.Repos, acc *domain.Account, anchorTrade *domain.Trade) *domain.Chain {
	t.Helper()
	chain := &domain.Chain{
		ID:               uuid.New().String(),
		AccountID:        acc.ID,
		UnderlyingSymbol: "TEST",
		OriginalTradeID:  anchorTrade.ID,
		CreatedAt:        anchorTrade.OpenedAt,
	}
	require.NoError(t, repos.Chains.CreateChain(ctx, chain))
	return chain
}

// seedTransaction creates a transaction for a trade.
func seedTransaction(t *testing.T, ctx context.Context, repos *sqlite.Repos, acc *domain.Account, trade *domain.Trade, inst domain.Instrument, action domain.Action, qty, price float64, effect domain.PositionEffect, executedAt time.Time) *domain.Transaction {
	t.Helper()
	tx := &domain.Transaction{
		ID:             uuid.New().String(),
		TradeID:        trade.ID,
		BrokerTxID:     uuid.New().String(),
		Broker:         acc.Broker,
		AccountID:      acc.ID,
		Instrument:     inst,
		Action:         action,
		Quantity:       decimal.NewFromFloat(qty),
		FillPrice:      decimal.NewFromFloat(price),
		Fees:           decimal.NewFromFloat(0.65),
		ExecutedAt:     executedAt.UTC().Truncate(time.Second),
		PositionEffect: effect,
	}
	require.NoError(t, repos.Transactions.Create(ctx, tx))
	return tx
}
