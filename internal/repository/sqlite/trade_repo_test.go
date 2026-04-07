package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/repository"
)

func TestTradeRepository(t *testing.T) {
	ctx := context.Background()

	t.Run("create and get by id with transactions", func(t *testing.T) {
		repos := openTestDB(t)
		acc := seedAccount(t, ctx, repos)
		inst := seedEquityInstrument(t, ctx, repos, "AAPL")
		trade := seedTrade(t, ctx, repos, acc, domain.StrategyStock, time.Now())
		seedTransaction(t, ctx, repos, acc, trade, inst, domain.ActionBuy, 10, 175, domain.PositionEffectOpening, time.Now())
		seedTransaction(t, ctx, repos, acc, trade, inst, domain.ActionSell, 10, 180, domain.PositionEffectClosing, time.Now().Add(time.Hour))

		got, err := repos.Trades.GetByID(ctx, trade.ID)
		require.NoError(t, err)
		assert.Equal(t, trade.ID, got.ID)
		assert.Equal(t, domain.StrategyStock, got.StrategyType)
		assert.Len(t, got.Transactions, 2)
		assert.Nil(t, got.ClosedAt)
	})

	t.Run("get not found", func(t *testing.T) {
		repos := openTestDB(t)
		_, err := repos.Trades.GetByID(ctx, "nonexistent")
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("list by account with pagination", func(t *testing.T) {
		repos := openTestDB(t)
		acc := seedAccount(t, ctx, repos)
		t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		for i := range 5 {
			seedTrade(t, ctx, repos, acc, domain.StrategyStock, t0.Add(time.Duration(i)*24*time.Hour))
		}

		trades, total, err := repos.Trades.ListByAccount(ctx, acc.ID, repository.ListTradesOptions{Limit: 3, Offset: 0})
		require.NoError(t, err)
		assert.Equal(t, 5, total)
		assert.Len(t, trades, 3)

		trades2, total2, err := repos.Trades.ListByAccount(ctx, acc.ID, repository.ListTradesOptions{Limit: 3, Offset: 3})
		require.NoError(t, err)
		assert.Equal(t, 5, total2)
		assert.Len(t, trades2, 2)
	})

	t.Run("list open only filter", func(t *testing.T) {
		repos := openTestDB(t)
		acc := seedAccount(t, ctx, repos)
		t0 := time.Now()

		open1 := seedTrade(t, ctx, repos, acc, domain.StrategyStock, t0)
		open2 := seedTrade(t, ctx, repos, acc, domain.StrategyCSP, t0.Add(time.Hour))
		closed := seedTrade(t, ctx, repos, acc, domain.StrategySingle, t0.Add(2*time.Hour))
		require.NoError(t, repos.Trades.UpdateClosedAt(ctx, closed.ID, t0.Add(3*time.Hour)))

		trades, total, err := repos.Trades.ListByAccount(ctx, acc.ID, repository.ListTradesOptions{OpenOnly: true})
		require.NoError(t, err)
		assert.Equal(t, 2, total)
		ids := []string{trades[0].ID, trades[1].ID}
		assert.Contains(t, ids, open1.ID)
		assert.Contains(t, ids, open2.ID)
	})

	t.Run("update strategy", func(t *testing.T) {
		repos := openTestDB(t)
		acc := seedAccount(t, ctx, repos)
		trade := seedTrade(t, ctx, repos, acc, domain.StrategyUnknown, time.Now())

		require.NoError(t, repos.Trades.UpdateStrategy(ctx, trade.ID, domain.StrategyCSP))
		got, err := repos.Trades.GetByID(ctx, trade.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.StrategyCSP, got.StrategyType)
	})

	t.Run("update closed at", func(t *testing.T) {
		repos := openTestDB(t)
		acc := seedAccount(t, ctx, repos)
		trade := seedTrade(t, ctx, repos, acc, domain.StrategyStock, time.Now())

		closedAt := time.Now().UTC().Truncate(time.Second).Add(time.Hour)
		require.NoError(t, repos.Trades.UpdateClosedAt(ctx, trade.ID, closedAt))
		got, err := repos.Trades.GetByID(ctx, trade.ID)
		require.NoError(t, err)
		require.NotNil(t, got.ClosedAt)
		assert.Equal(t, closedAt, got.ClosedAt.UTC())
	})
}
