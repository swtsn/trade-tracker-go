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

func TestPositionRepository(t *testing.T) {
	ctx := context.Background()

	t.Run("create and get lot", func(t *testing.T) {
		repos := openTestDB(t)
		acc := seedAccount(t, ctx, repos)
		inst := seedEquityInstrument(t, ctx, repos, "AAPL")
		trade := seedTrade(t, ctx, repos, acc, domain.StrategyStock, time.Now())
		tx := seedTransaction(t, ctx, repos, acc, trade, inst, domain.ActionBuy, 10, 175, domain.PositionEffectOpening, time.Now())

		lot := &domain.PositionLot{
			ID:                uuid.New().String(),
			AccountID:         acc.ID,
			Instrument:        inst,
			TradeID:           trade.ID,
			OpeningTxID:       tx.ID,
			OpenQuantity:      decimal.NewFromInt(10),
			RemainingQuantity: decimal.NewFromInt(10),
			OpenPrice:         decimal.NewFromFloat(175),
			OpenFees:          decimal.NewFromFloat(0.65),
			OpenedAt:          tx.ExecutedAt,
		}
		require.NoError(t, repos.Positions.CreateLot(ctx, lot))

		got, err := repos.Positions.GetLot(ctx, lot.ID)
		require.NoError(t, err)
		assert.Equal(t, lot.ID, got.ID)
		assert.Equal(t, "AAPL", got.Instrument.Symbol)
		assert.True(t, decimal.NewFromInt(10).Equal(got.RemainingQuantity))
		assert.Nil(t, got.ClosedAt)
	})

	t.Run("list open lots by instrument - fifo order", func(t *testing.T) {
		repos := openTestDB(t)
		acc := seedAccount(t, ctx, repos)
		inst := seedEquityInstrument(t, ctx, repos, "SPY")
		trade := seedTrade(t, ctx, repos, acc, domain.StrategyStock, time.Now())

		t0 := time.Date(2025, 1, 1, 9, 0, 0, 0, time.UTC)
		var lotIDs []string
		for i := range 3 {
			tx := seedTransaction(t, ctx, repos, acc, trade, inst, domain.ActionBuy, 1, float64(400+i), domain.PositionEffectOpening, t0.Add(time.Duration(i)*time.Hour))
			lot := &domain.PositionLot{
				ID:                uuid.New().String(),
				AccountID:         acc.ID,
				Instrument:        inst,
				TradeID:           trade.ID,
				OpeningTxID:       tx.ID,
				OpenQuantity:      decimal.NewFromInt(1),
				RemainingQuantity: decimal.NewFromInt(1),
				OpenPrice:         decimal.NewFromFloat(float64(400 + i)),
				OpenFees:          decimal.NewFromFloat(0.65),
				OpenedAt:          t0.Add(time.Duration(i) * time.Hour),
			}
			require.NoError(t, repos.Positions.CreateLot(ctx, lot))
			lotIDs = append(lotIDs, lot.ID)
		}

		lots, err := repos.Positions.ListOpenLotsByInstrument(ctx, acc.ID, inst.InstrumentID())
		require.NoError(t, err)
		require.Len(t, lots, 3)
		// Must be in FIFO (oldest first) order.
		assert.Equal(t, lotIDs[0], lots[0].ID)
		assert.Equal(t, lotIDs[1], lots[1].ID)
		assert.Equal(t, lotIDs[2], lots[2].ID)
	})

	t.Run("close lot - partial then full", func(t *testing.T) {
		repos := openTestDB(t)
		acc := seedAccount(t, ctx, repos)
		inst := seedEquityInstrument(t, ctx, repos, "NVDA")
		trade := seedTrade(t, ctx, repos, acc, domain.StrategyStock, time.Now())
		openTx := seedTransaction(t, ctx, repos, acc, trade, inst, domain.ActionBuy, 10, 900, domain.PositionEffectOpening, time.Now())
		closeTx1 := seedTransaction(t, ctx, repos, acc, trade, inst, domain.ActionSell, 4, 950, domain.PositionEffectClosing, time.Now().Add(time.Hour))
		closeTx2 := seedTransaction(t, ctx, repos, acc, trade, inst, domain.ActionSell, 6, 960, domain.PositionEffectClosing, time.Now().Add(2*time.Hour))

		lot := &domain.PositionLot{
			ID:                uuid.New().String(),
			AccountID:         acc.ID,
			Instrument:        inst,
			TradeID:           trade.ID,
			OpeningTxID:       openTx.ID,
			OpenQuantity:      decimal.NewFromInt(10),
			RemainingQuantity: decimal.NewFromInt(10),
			OpenPrice:         decimal.NewFromFloat(900),
			OpenFees:          decimal.NewFromFloat(0.65),
			OpenedAt:          openTx.ExecutedAt,
		}
		require.NoError(t, repos.Positions.CreateLot(ctx, lot))

		// Partial close: 4 of 10 shares.
		closing1 := &domain.LotClosing{
			ID:             uuid.New().String(),
			LotID:          lot.ID,
			ClosingTxID:    closeTx1.ID,
			ClosedQuantity: decimal.NewFromInt(4),
			ClosePrice:     decimal.NewFromFloat(950),
			CloseFees:      decimal.NewFromFloat(0.65),
			RealizedPnL:    decimal.NewFromFloat(199.35),
			ClosedAt:       closeTx1.ExecutedAt,
		}
		require.NoError(t, repos.Positions.CloseLot(ctx, closing1, decimal.NewFromInt(6), nil))

		got, err := repos.Positions.GetLot(ctx, lot.ID)
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(6).Equal(got.RemainingQuantity))
		assert.Nil(t, got.ClosedAt)

		// Full close: remaining = 0, set closed_at.
		closedAt := closeTx2.ExecutedAt.Truncate(time.Second)
		closing2 := &domain.LotClosing{
			ID:             uuid.New().String(),
			LotID:          lot.ID,
			ClosingTxID:    closeTx2.ID,
			ClosedQuantity: decimal.NewFromInt(6),
			ClosePrice:     decimal.NewFromFloat(960),
			CloseFees:      decimal.NewFromFloat(0.65),
			RealizedPnL:    decimal.NewFromFloat(359.35),
			ClosedAt:       closeTx2.ExecutedAt,
		}
		require.NoError(t, repos.Positions.CloseLot(ctx, closing2, decimal.NewFromInt(0), &closedAt))

		got2, err := repos.Positions.GetLot(ctx, lot.ID)
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(0).Equal(got2.RemainingQuantity))
		require.NotNil(t, got2.ClosedAt)
		assert.Equal(t, closedAt, got2.ClosedAt.UTC())

		closings, err := repos.Positions.ListLotClosings(ctx, lot.ID)
		require.NoError(t, err)
		assert.Len(t, closings, 2)

		// Fully closed lot should not appear in open lots.
		open, err := repos.Positions.ListOpenLotsByInstrument(ctx, acc.ID, inst.InstrumentID())
		require.NoError(t, err)
		assert.Empty(t, open)
	})

	t.Run("upsert position and get", func(t *testing.T) {
		repos := openTestDB(t)
		acc := seedAccount(t, ctx, repos)
		inst := seedEquityInstrument(t, ctx, repos, "TSLA")

		pos := &domain.Position{
			ID:          uuid.New().String(),
			AccountID:   acc.ID,
			Instrument:  inst,
			Quantity:    decimal.NewFromInt(5),
			CostBasis:   decimal.NewFromFloat(1000),
			RealizedPnL: decimal.NewFromInt(0),
			OpenedAt:    time.Now().UTC().Truncate(time.Second),
			UpdatedAt:   time.Now().UTC().Truncate(time.Second),
		}
		require.NoError(t, repos.Positions.UpsertPosition(ctx, pos))

		got, err := repos.Positions.GetPosition(ctx, acc.ID, inst.InstrumentID())
		require.NoError(t, err)
		assert.Equal(t, pos.ID, got.ID)
		assert.True(t, decimal.NewFromInt(5).Equal(got.Quantity))
		assert.Equal(t, "TSLA", got.Instrument.Symbol)

		// Update via second upsert.
		pos.Quantity = decimal.NewFromInt(10)
		pos.CostBasis = decimal.NewFromFloat(2000)
		pos.UpdatedAt = time.Now().UTC().Truncate(time.Second).Add(time.Minute)
		require.NoError(t, repos.Positions.UpsertPosition(ctx, pos))

		got2, err := repos.Positions.GetPosition(ctx, acc.ID, inst.InstrumentID())
		require.NoError(t, err)
		assert.True(t, decimal.NewFromInt(10).Equal(got2.Quantity))
	})

	t.Run("list open positions", func(t *testing.T) {
		repos := openTestDB(t)
		acc := seedAccount(t, ctx, repos)
		i1 := seedEquityInstrument(t, ctx, repos, "AAPL")
		i2 := seedEquityInstrument(t, ctx, repos, "GOOG")

		now := time.Now().UTC().Truncate(time.Second)
		closedAt := now.Add(time.Hour)
		p1 := &domain.Position{ID: uuid.New().String(), AccountID: acc.ID, Instrument: i1, Quantity: decimal.NewFromInt(10), CostBasis: decimal.Zero, RealizedPnL: decimal.Zero, OpenedAt: now, UpdatedAt: now}
		p2 := &domain.Position{ID: uuid.New().String(), AccountID: acc.ID, Instrument: i2, Quantity: decimal.NewFromInt(0), CostBasis: decimal.Zero, RealizedPnL: decimal.Zero, OpenedAt: now, UpdatedAt: now, ClosedAt: &closedAt}
		require.NoError(t, repos.Positions.UpsertPosition(ctx, p1))
		require.NoError(t, repos.Positions.UpsertPosition(ctx, p2))

		open, err := repos.Positions.ListOpenPositions(ctx, acc.ID)
		require.NoError(t, err)
		assert.Len(t, open, 1)
		assert.Equal(t, "AAPL", open[0].Instrument.Symbol)
	})

	t.Run("lot closing round-trip with resulting_lot_id", func(t *testing.T) {
		repos := openTestDB(t)
		acc := seedAccount(t, ctx, repos)
		inst := seedOptionInstrument(t, ctx, repos, "SPY", 500, domain.OptionTypePut, time.Date(2025, 12, 19, 0, 0, 0, 0, time.UTC))
		stockInst := seedEquityInstrument(t, ctx, repos, "SPY")
		trade := seedTrade(t, ctx, repos, acc, domain.StrategyCSP, time.Now())

		openTx := seedTransaction(t, ctx, repos, acc, trade, inst, domain.ActionSTO, 1, 3.50, domain.PositionEffectOpening, time.Now())
		closeTx := seedTransaction(t, ctx, repos, acc, trade, stockInst, domain.ActionAssignment, 100, 500, domain.PositionEffectClosing, time.Now().Add(time.Hour))

		optionLot := &domain.PositionLot{
			ID:                uuid.New().String(),
			AccountID:         acc.ID,
			Instrument:        inst,
			TradeID:           trade.ID,
			OpeningTxID:       openTx.ID,
			OpenQuantity:      decimal.NewFromInt(-1),
			RemainingQuantity: decimal.NewFromInt(-1),
			OpenPrice:         decimal.NewFromFloat(3.50),
			OpenFees:          decimal.NewFromFloat(1.30),
			OpenedAt:          openTx.ExecutedAt,
		}
		require.NoError(t, repos.Positions.CreateLot(ctx, optionLot))

		stockLot := &domain.PositionLot{
			ID:                uuid.New().String(),
			AccountID:         acc.ID,
			Instrument:        stockInst,
			TradeID:           trade.ID,
			OpeningTxID:       closeTx.ID,
			OpenQuantity:      decimal.NewFromInt(100),
			RemainingQuantity: decimal.NewFromInt(100),
			OpenPrice:         decimal.NewFromFloat(500),
			OpenFees:          decimal.NewFromFloat(0),
			OpenedAt:          closeTx.ExecutedAt,
		}
		require.NoError(t, repos.Positions.CreateLot(ctx, stockLot))

		// Create a lot closing that links option lot → stock lot via ResultingLotID.
		stockLotID := stockLot.ID
		closing := &domain.LotClosing{
			ID:             uuid.New().String(),
			LotID:          optionLot.ID,
			ClosingTxID:    closeTx.ID,
			ClosedQuantity: decimal.NewFromInt(1),
			ClosePrice:     decimal.NewFromFloat(500),
			CloseFees:      decimal.NewFromFloat(0),
			RealizedPnL:    decimal.NewFromFloat(350), // (3.50 * 100) - fees
			ClosedAt:       closeTx.ExecutedAt,
			ResultingLotID: &stockLotID,
		}
		closedAt := closeTx.ExecutedAt
		require.NoError(t, repos.Positions.CloseLot(ctx, closing, decimal.NewFromInt(0), &closedAt))

		closings, err := repos.Positions.ListLotClosings(ctx, optionLot.ID)
		require.NoError(t, err)
		require.Len(t, closings, 1)
		assert.Equal(t, closing.ID, closings[0].ID)
		require.NotNil(t, closings[0].ResultingLotID)
		assert.Equal(t, stockLot.ID, *closings[0].ResultingLotID)
		assert.True(t, decimal.NewFromFloat(350).Equal(closings[0].RealizedPnL))
	})
}
