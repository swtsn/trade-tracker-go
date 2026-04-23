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

func TestChainRepository(t *testing.T) {
	ctx := context.Background()

	t.Run("create chain and get by id with links", func(t *testing.T) {
		repos := openTestDB(t)
		acc := seedAccount(t, ctx, repos)
		trade1 := seedTrade(t, ctx, repos, acc, time.Now())
		trade2 := seedTrade(t, ctx, repos, acc, time.Now().Add(time.Hour))

		chain := &domain.Chain{
			ID:               uuid.New().String(),
			AccountID:        acc.ID,
			UnderlyingSymbol: "SPY",
			OriginalTradeID:  trade1.ID,
			CreatedAt:        time.Now().UTC().Truncate(time.Second),
		}
		require.NoError(t, repos.Chains.CreateChain(ctx, chain))

		// Add a chain link (roll).
		link := &domain.ChainLink{
			ID:               uuid.New().String(),
			ChainID:          chain.ID,
			Sequence:         1,
			LinkType:         domain.LinkTypeRoll,
			ClosingTradeID:   trade1.ID,
			OpeningTradeID:   trade2.ID,
			LinkedAt:         time.Now().UTC().Truncate(time.Second),
			StrikeChange:     decimal.NewFromFloat(-5),
			ExpirationChange: 30,
			CreditDebit:      decimal.NewFromFloat(0.50),
		}
		require.NoError(t, repos.Chains.CreateChainLink(ctx, link))

		got, err := repos.Chains.GetChainByID(ctx, chain.ID)
		require.NoError(t, err)
		assert.Equal(t, chain.ID, got.ID)
		assert.Equal(t, "SPY", got.UnderlyingSymbol)
		assert.Nil(t, got.ClosedAt)
		require.Len(t, got.Links, 1)
		assert.Equal(t, link.ID, got.Links[0].ID)
		assert.Equal(t, 1, got.Links[0].Sequence)
		assert.Equal(t, domain.LinkTypeRoll, got.Links[0].LinkType)
		assert.True(t, decimal.NewFromFloat(-5).Equal(got.Links[0].StrikeChange))
		assert.Equal(t, 30, got.Links[0].ExpirationChange)
	})

	t.Run("chain link sequence uniqueness", func(t *testing.T) {
		repos := openTestDB(t)
		acc := seedAccount(t, ctx, repos)
		trade := seedTrade(t, ctx, repos, acc, time.Now())
		chain := &domain.Chain{
			ID:               uuid.New().String(),
			AccountID:        acc.ID,
			UnderlyingSymbol: "QQQ",
			OriginalTradeID:  trade.ID,
			CreatedAt:        time.Now().UTC().Truncate(time.Second),
		}
		require.NoError(t, repos.Chains.CreateChain(ctx, chain))

		link1 := &domain.ChainLink{
			ID:             uuid.New().String(),
			ChainID:        chain.ID,
			Sequence:       1,
			LinkType:       domain.LinkTypeRoll,
			ClosingTradeID: trade.ID,
			OpeningTradeID: trade.ID,
			LinkedAt:       time.Now().UTC().Truncate(time.Second),
			StrikeChange:   decimal.Zero,
			CreditDebit:    decimal.Zero,
		}
		require.NoError(t, repos.Chains.CreateChainLink(ctx, link1))

		// Duplicate sequence should fail.
		link2 := *link1
		link2.ID = uuid.New().String()
		err := repos.Chains.CreateChainLink(ctx, &link2)
		assert.ErrorIs(t, err, domain.ErrDuplicate)
	})

	t.Run("list chains by account open only", func(t *testing.T) {
		repos := openTestDB(t)
		acc := seedAccount(t, ctx, repos)
		trade := seedTrade(t, ctx, repos, acc, time.Now())

		openChain := &domain.Chain{ID: uuid.New().String(), AccountID: acc.ID, UnderlyingSymbol: "SPY", OriginalTradeID: trade.ID, CreatedAt: time.Now().UTC().Truncate(time.Second)}
		closedAt := time.Now().UTC().Truncate(time.Second).Add(time.Hour)
		closedChain := &domain.Chain{ID: uuid.New().String(), AccountID: acc.ID, UnderlyingSymbol: "QQQ", OriginalTradeID: trade.ID, CreatedAt: time.Now().UTC().Truncate(time.Second), ClosedAt: &closedAt}

		require.NoError(t, repos.Chains.CreateChain(ctx, openChain))
		require.NoError(t, repos.Chains.CreateChain(ctx, closedChain))

		open, err := repos.Chains.ListChainsByAccount(ctx, acc.ID, true)
		require.NoError(t, err)
		require.Len(t, open, 1)
		assert.Equal(t, openChain.ID, open[0].ID)

		all, err := repos.Chains.ListChainsByAccount(ctx, acc.ID, false)
		require.NoError(t, err)
		assert.Len(t, all, 2)
	})

	t.Run("update chain closed", func(t *testing.T) {
		repos := openTestDB(t)
		acc := seedAccount(t, ctx, repos)
		trade := seedTrade(t, ctx, repos, acc, time.Now())
		chain := &domain.Chain{
			ID:               uuid.New().String(),
			AccountID:        acc.ID,
			UnderlyingSymbol: "IWM",
			OriginalTradeID:  trade.ID,
			CreatedAt:        time.Now().UTC().Truncate(time.Second),
		}
		require.NoError(t, repos.Chains.CreateChain(ctx, chain))

		closedAt := time.Now().UTC().Truncate(time.Second).Add(time.Hour)
		require.NoError(t, repos.Chains.UpdateChainClosed(ctx, chain.ID, closedAt))

		got, err := repos.Chains.GetChainByID(ctx, chain.ID)
		require.NoError(t, err)
		require.NotNil(t, got.ClosedAt)
		assert.Equal(t, closedAt, got.ClosedAt.UTC())
	})
}
