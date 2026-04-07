package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trade-tracker-go/internal/domain"
)

func TestTransactionRepository(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)
	acc := seedAccount(t, ctx, repos)
	inst := seedEquityInstrument(t, ctx, repos, "AAPL")
	trade := seedTrade(t, ctx, repos, acc, domain.StrategyStock, time.Now())

	t.Run("create and get by id", func(t *testing.T) {
		tx := seedTransaction(t, ctx, repos, acc, trade, inst, domain.ActionBuy, 10, 175.50, domain.PositionEffectOpening, time.Now())

		got, err := repos.Transactions.GetByID(ctx, tx.ID)
		require.NoError(t, err)
		assert.Equal(t, tx.ID, got.ID)
		assert.Equal(t, tx.BrokerTxID, got.BrokerTxID)
		assert.Equal(t, tx.Action, got.Action)
		assert.True(t, tx.Quantity.Equal(got.Quantity))
		assert.True(t, tx.FillPrice.Equal(got.FillPrice))
		assert.Equal(t, tx.Instrument.Symbol, got.Instrument.Symbol)
		assert.Equal(t, tx.Instrument.AssetClass, got.Instrument.AssetClass)
		assert.Equal(t, tx.PositionEffect, got.PositionEffect)
		assert.Nil(t, got.ChainID)
	})

	t.Run("get not found", func(t *testing.T) {
		_, err := repos.Transactions.GetByID(ctx, "nonexistent")
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("list by trade", func(t *testing.T) {
		r2 := openTestDB(t)
		a := seedAccount(t, ctx, r2)
		i := seedEquityInstrument(t, ctx, r2, "NVDA")
		tr := seedTrade(t, ctx, r2, a, domain.StrategyStock, time.Now())

		seedTransaction(t, ctx, r2, a, tr, i, domain.ActionBuy, 5, 100, domain.PositionEffectOpening, time.Now())
		seedTransaction(t, ctx, r2, a, tr, i, domain.ActionSell, 5, 110, domain.PositionEffectClosing, time.Now().Add(time.Hour))

		txs, err := r2.Transactions.ListByTrade(ctx, tr.ID)
		require.NoError(t, err)
		assert.Len(t, txs, 2)
	})

	t.Run("dedup by broker_tx_id", func(t *testing.T) {
		r2 := openTestDB(t)
		a := seedAccount(t, ctx, r2)
		i := seedEquityInstrument(t, ctx, r2, "META")
		tr := seedTrade(t, ctx, r2, a, domain.StrategyStock, time.Now())
		tx := seedTransaction(t, ctx, r2, a, tr, i, domain.ActionBuy, 1, 500, domain.PositionEffectOpening, time.Now())

		// ExistsByBrokerTxID should return true.
		exists, err := r2.Transactions.ExistsByBrokerTxID(ctx, tx.BrokerTxID, tx.Broker, tx.AccountID)
		require.NoError(t, err)
		assert.True(t, exists)

		// Different broker_tx_id should return false.
		exists, err = r2.Transactions.ExistsByBrokerTxID(ctx, "unknown", tx.Broker, tx.AccountID)
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("list by account and time range", func(t *testing.T) {
		r2 := openTestDB(t)
		a := seedAccount(t, ctx, r2)
		i := seedEquityInstrument(t, ctx, r2, "GOOG")
		tr := seedTrade(t, ctx, r2, a, domain.StrategyStock, time.Now())

		t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		seedTransaction(t, ctx, r2, a, tr, i, domain.ActionBuy, 1, 100, domain.PositionEffectOpening, t0)
		seedTransaction(t, ctx, r2, a, tr, i, domain.ActionBuy, 1, 101, domain.PositionEffectOpening, t0.Add(24*time.Hour))
		seedTransaction(t, ctx, r2, a, tr, i, domain.ActionBuy, 1, 102, domain.PositionEffectOpening, t0.Add(48*time.Hour))

		txs, err := r2.Transactions.ListByAccountAndTimeRange(ctx, a.ID, t0, t0.Add(25*time.Hour))
		require.NoError(t, err)
		assert.Len(t, txs, 2)
	})

	t.Run("chain id round-trips", func(t *testing.T) {
		r2 := openTestDB(t)
		a := seedAccount(t, ctx, r2)
		i := seedEquityInstrument(t, ctx, r2, "SPY")
		tr := seedTrade(t, ctx, r2, a, domain.StrategyStock, time.Now())

		// chain_id has no FK constraint on transactions, so we can store any string.
		chainID := "chain-abc"
		txWithChain := &domain.Transaction{
			ID:             uuid.New().String(),
			TradeID:        tr.ID,
			BrokerTxID:     uuid.New().String(),
			Broker:         a.Broker,
			AccountID:      a.ID,
			Instrument:     i,
			Action:         domain.ActionBuy,
			Quantity:       decimal.NewFromInt(1),
			FillPrice:      decimal.NewFromInt(400),
			Fees:           decimal.NewFromFloat(0.65),
			ExecutedAt:     time.Now().UTC().Truncate(time.Second),
			PositionEffect: domain.PositionEffectOpening,
			ChainID:        &chainID,
		}
		require.NoError(t, r2.Transactions.Create(ctx, txWithChain))

		got, err := r2.Transactions.GetByID(ctx, txWithChain.ID)
		require.NoError(t, err)
		require.NotNil(t, got.ChainID)
		assert.Equal(t, chainID, *got.ChainID)
	})
}
