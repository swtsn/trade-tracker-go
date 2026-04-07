package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trade-tracker-go/internal/domain"
)

func TestInstrumentRepository(t *testing.T) {
	ctx := context.Background()
	repos := openTestDB(t)

	t.Run("upsert and get equity", func(t *testing.T) {
		inst := domain.Instrument{
			Symbol:     "AAPL",
			AssetClass: domain.AssetClassEquity,
		}
		require.NoError(t, repos.Instruments.Upsert(ctx, &inst))

		got, err := repos.Instruments.GetByID(ctx, inst.InstrumentID())
		require.NoError(t, err)
		assert.Equal(t, "AAPL", got.Symbol)
		assert.Equal(t, domain.AssetClassEquity, got.AssetClass)
		assert.Nil(t, got.Option)
		assert.Nil(t, got.Future)
	})

	t.Run("upsert and get equity option", func(t *testing.T) {
		exp := time.Date(2025, 12, 19, 0, 0, 0, 0, time.UTC)
		inst := domain.Instrument{
			Symbol:     "SPY",
			AssetClass: domain.AssetClassEquityOption,
			Option: &domain.OptionDetails{
				Expiration: exp,
				Strike:     decimal.NewFromFloat(500),
				OptionType: domain.OptionTypeCall,
				Multiplier: decimal.NewFromInt(100),
				OSI:        "SPY   251219C00500000",
			},
		}
		require.NoError(t, repos.Instruments.Upsert(ctx, &inst))

		got, err := repos.Instruments.GetByID(ctx, inst.InstrumentID())
		require.NoError(t, err)
		assert.Equal(t, "SPY", got.Symbol)
		assert.Equal(t, domain.AssetClassEquityOption, got.AssetClass)
		require.NotNil(t, got.Option)
		assert.Equal(t, exp, got.Option.Expiration)
		assert.True(t, decimal.NewFromFloat(500).Equal(got.Option.Strike))
		assert.Equal(t, domain.OptionTypeCall, got.Option.OptionType)
		assert.Equal(t, "SPY   251219C00500000", got.Option.OSI)
	})

	t.Run("upsert is idempotent", func(t *testing.T) {
		inst := domain.Instrument{Symbol: "TSLA", AssetClass: domain.AssetClassEquity}
		require.NoError(t, repos.Instruments.Upsert(ctx, &inst))
		// Second upsert should not error (INSERT OR IGNORE).
		require.NoError(t, repos.Instruments.Upsert(ctx, &inst))
	})

	t.Run("get not found", func(t *testing.T) {
		_, err := repos.Instruments.GetByID(ctx, "nonexistent-id")
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("deterministic id is stable", func(t *testing.T) {
		inst := domain.Instrument{Symbol: "MSFT", AssetClass: domain.AssetClassEquity}
		id1 := inst.InstrumentID()
		id2 := inst.InstrumentID()
		assert.Equal(t, id1, id2)
	})
}
