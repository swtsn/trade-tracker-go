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
		trade := seedTrade(t, ctx, repos, acc, time.Now())
		seedTransaction(t, ctx, repos, acc, trade, inst, domain.ActionBuy, 10, 175, domain.PositionEffectOpening, time.Now())
		seedTransaction(t, ctx, repos, acc, trade, inst, domain.ActionSell, 10, 180, domain.PositionEffectClosing, time.Now().Add(time.Hour))

		got, err := repos.Trades.GetByID(ctx, trade.ID)
		require.NoError(t, err)
		assert.Equal(t, trade.ID, got.ID)
		assert.Len(t, got.Transactions, 2)
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
			seedTrade(t, ctx, repos, acc, t0.Add(time.Duration(i)*24*time.Hour))
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

}
