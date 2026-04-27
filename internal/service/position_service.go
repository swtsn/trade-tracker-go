package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/repository"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ErrNoOpenLots is returned by processClosing when a closing transaction finds no
// matching open lots for the instrument. Callers decide whether to suppress or
// propagate; ProcessTrade logs and continues because this is expected for historical
// imports where prior positions were not tracked.
var ErrNoOpenLots = errors.New("no open lots for instrument")

// PositionService manages position lots and tracks realized P&L.
// It is called as a post-import hook after ChainService has assigned a chain ID.
type PositionService struct {
	positions repository.PositionRepository
	logger    *slog.Logger
}

// NewPositionService creates a PositionService with the given repository.
func NewPositionService(positions repository.PositionRepository, logger *slog.Logger) *PositionService {
	return &PositionService{positions: positions, logger: logger}
}

// GetPosition returns a position by ID.
// Returns domain.ErrNotFound if no position exists or accountID does not match.
func (s *PositionService) GetPosition(ctx context.Context, accountID, positionID string) (*domain.Position, error) {
	return s.positions.GetPositionByIDAndAccount(ctx, accountID, positionID)
}

// ListPositions returns positions for an account.
// openOnly and closedOnly are mutually exclusive; both false returns all positions.
func (s *PositionService) ListPositions(ctx context.Context, accountID string, openOnly, closedOnly bool) ([]domain.Position, error) {
	return s.positions.ListPositions(ctx, accountID, openOnly, closedOnly)
}

// ProcessTrade processes all transactions for a trade, creating/updating lots and positions.
// chainID must be the chain this trade belongs to (returned by ChainService.ProcessTrade).
// strategyType is the classified strategy for the trade, burned in on position creation.
// txns must be in closing-first order (guaranteed by ImportService).
// Opening legs create new lots and upsert the position row for the trade.
// Closing legs FIFO-match against open lots, compute realized P&L, and stamp
// position.closed_at when all lots under the position are fully closed.
func (s *PositionService) ProcessTrade(ctx context.Context, tradeID string, txns []domain.Transaction, chainID string, strategyType domain.StrategyType) error {
	if allEquity(txns) {
		return s.processEquityTrade(ctx, tradeID, txns, chainID)
	}
	for _, tx := range txns {
		if tx.TradeID != tradeID {
			return fmt.Errorf("position service: transaction %s has trade_id %q, expected %q", tx.ID, tx.TradeID, tradeID)
		}
		switch tx.PositionEffect {
		case domain.PositionEffectClosing:
			if err := s.processClosing(ctx, tx); err != nil {
				if errors.Is(err, ErrNoOpenLots) {
					s.logger.Warn("closing tx skipped: no open lots",
						"tx_id", tx.ID,
						"broker_tx_id", tx.BrokerTxID,
						"symbol", tx.Instrument.Symbol,
						"action", tx.Action,
						"quantity", tx.Quantity,
						"executed_at", tx.ExecutedAt,
					)
					continue
				}
				return fmt.Errorf("position service: closing tx %s: %w", tx.ID, err)
			}
		case domain.PositionEffectOpening:
			if err := s.processOpening(ctx, tradeID, chainID, strategyType, tx); err != nil {
				return fmt.Errorf("position service: opening tx %s: %w", tx.ID, err)
			}
		}
	}
	return nil
}

// processOpening creates a lot and upserts the position row for the chain (or trade if unchained).
func (s *PositionService) processOpening(ctx context.Context, tradeID, chainID string, strategyType domain.StrategyType, tx domain.Transaction) error {
	lotID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate lot id: %w", err)
	}
	lot := &domain.PositionLot{
		ID:                lotID.String(),
		AccountID:         tx.AccountID,
		Instrument:        tx.Instrument,
		TradeID:           tradeID,
		OpeningTxID:       tx.ID,
		OpenQuantity:      lotSignedQty(tx),
		RemainingQuantity: lotSignedQty(tx),
		OpenPrice:         tx.FillPrice,
		OpenFees:          tx.Fees,
		OpenedAt:          tx.ExecutedAt,
		ChainID:           chainID,
	}
	if err := s.positions.CreateLot(ctx, lot); err != nil {
		return fmt.Errorf("create lot: %w", err)
	}

	multiplier := optionMultiplier(tx.Instrument)
	legCostBasis := domain.CashFlowSign(tx.Action).
		Mul(tx.FillPrice).
		Mul(tx.Quantity.Abs()).
		Mul(multiplier).
		Sub(tx.Fees)

	// Look up the position: by chainID if set (handles multi-trade chains and multi-leg
	// trades in the same chain), otherwise by tradeID (legacy unchained positions).
	var existing *domain.Position
	var lookupErr error
	if chainID != "" {
		existing, lookupErr = s.positions.GetPositionByChainID(ctx, tx.AccountID, chainID)
	} else {
		existing, lookupErr = s.positions.GetPositionByTradeID(ctx, tx.AccountID, tradeID)
	}

	if lookupErr != nil {
		if !errors.Is(lookupErr, domain.ErrNotFound) {
			return fmt.Errorf("get position: %w", lookupErr)
		}
		// No position yet — create one.
		posID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate position id: %w", err)
		}
		pos := &domain.Position{
			ID:                 posID.String(),
			AccountID:          tx.AccountID,
			ChainID:            chainID,
			OriginatingTradeID: tradeID,
			UnderlyingSymbol:   tx.Instrument.Symbol,
			CostBasis:          legCostBasis,
			RealizedPnL:        decimal.Zero,
			OpenedAt:           tx.ExecutedAt,
			UpdatedAt:          tx.ExecutedAt,
			StrategyType:       strategyType,
		}
		return s.positions.CreatePosition(ctx, pos)
	}

	// Position exists — accumulate this leg's cost basis.
	existing.CostBasis = existing.CostBasis.Add(legCostBasis)
	existing.UpdatedAt = tx.ExecutedAt
	return s.positions.UpdatePosition(ctx, existing)
}

// processClosing FIFO-matches open lots for the closing transaction's instrument,
// records LotClosing entries, and updates the associated position's realized P&L.
// When all lots under a position are closed, position.closed_at is stamped.
func (s *PositionService) processClosing(ctx context.Context, tx domain.Transaction) error {
	lots, err := s.positions.ListOpenLotsByInstrument(ctx, tx.AccountID, tx.Instrument.InstrumentID())
	if err != nil {
		return fmt.Errorf("list open lots: %w", err)
	}
	if len(lots) == 0 {
		return fmt.Errorf("%w: instrument %s account %s", ErrNoOpenLots, tx.Instrument, tx.AccountID)
	}

	multiplier := optionMultiplier(tx.Instrument)
	// CashFlowSign returns 0 for ASSIGNMENT and EXERCISE. This is intentional: the
	// option's close cash-flow is zero (the premium collected at open is the P&L);
	// the resulting stock/futures lot should be created via ResultingLotID (not yet
	// implemented). For EXPIRATION, fill_price is always 0, so the result is the same.
	closeSign := domain.CashFlowSign(tx.Action)
	totalCloseQty := tx.Quantity.Abs() // total absolute quantity being closed

	remaining := totalCloseQty
	for _, lot := range lots {
		if remaining.IsZero() {
			break
		}

		lotOpen := lot.RemainingQuantity.Abs()
		var closeQty decimal.Decimal
		if remaining.GreaterThanOrEqual(lotOpen) {
			closeQty = lotOpen
		} else {
			closeQty = remaining
		}
		remaining = remaining.Sub(closeQty)

		// Pro-rate closing fees by fraction of the total close quantity.
		closeFees := tx.Fees.Mul(closeQty).Div(totalCloseQty)

		// Open cash-flow sign is derived from the lot's signed quantity:
		//   positive open_quantity → BTO/BUY (debit, sign = -1)
		//   negative open_quantity → STO/SELL (credit, sign = +1)
		openSign := lotCashFlowSign(lot)

		// Guard against division by zero from bad import data.
		if lot.OpenQuantity.IsZero() {
			return fmt.Errorf("lot %s has zero open_quantity; cannot pro-rate fees", lot.ID)
		}
		// openFeesProrated uses lot.OpenQuantity (the original full size) as the denominator,
		// NOT the current remaining quantity. This means fees are matched to the fraction of
		// the original lot being closed: if 1 of 2 contracts is closed, half the open fees
		// are attributed to this closing. Over all partial closings the full OpenFees are
		// recovered exactly. This differs from closeFees (which divides by totalCloseQty,
		// the current transaction's close size), but both denominators are internally
		// consistent with their respective totals.
		openFeesProrated := lot.OpenFees.Mul(closeQty).Div(lot.OpenQuantity.Abs())

		closeCF := closeSign.Mul(tx.FillPrice).Mul(closeQty).Mul(multiplier)
		openCF := openSign.Mul(lot.OpenPrice).Mul(closeQty).Mul(multiplier)
		realizedPnL := closeCF.Add(openCF).Sub(closeFees).Sub(openFeesProrated)

		// New remaining_quantity: move toward zero by closeQty in the direction of the lot.
		// Short lots are negative; closing reduces the magnitude → remaining increases.
		// Long lots are positive; closing → remaining decreases.
		lotDirection := decimal.NewFromInt(1)
		if lot.OpenQuantity.IsNegative() {
			lotDirection = decimal.NewFromInt(-1)
		}
		newRemaining := lot.RemainingQuantity.Sub(lotDirection.Mul(closeQty))

		var lotClosedAt *time.Time
		if newRemaining.IsZero() {
			t := tx.ExecutedAt
			lotClosedAt = &t
		}

		closingID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate closing id: %w", err)
		}
		closing := &domain.LotClosing{
			ID:             closingID.String(),
			LotID:          lot.ID,
			ClosingTxID:    tx.ID,
			ClosedQuantity: closeQty,
			ClosePrice:     tx.FillPrice,
			CloseFees:      closeFees,
			RealizedPnL:    realizedPnL,
			ClosedAt:       tx.ExecutedAt,
		}
		pos, err := s.findPositionForLot(ctx, tx.AccountID, lot)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				s.logger.Warn("lot has no position row; realized_pnl not updated", "lot_id", lot.ID, "chain_id", lot.ChainID, "trade_id", lot.TradeID)
				if err := s.positions.CloseLot(ctx, closing, newRemaining, lotClosedAt); err != nil {
					return fmt.Errorf("close lot %s: %w", lot.ID, err)
				}
				continue
			}
			return fmt.Errorf("find position for lot %s: %w", lot.ID, err)
		}
		if err := s.positions.CloseAndUpdatePosition(ctx, closing, newRemaining, lotClosedAt, pos, realizedPnL, tx.ExecutedAt); err != nil {
			return fmt.Errorf("close lot %s: %w", lot.ID, err)
		}
	}
	if !remaining.IsZero() {
		return fmt.Errorf("closing quantity %s exceeds open lots for instrument %s: %s unmatched",
			totalCloseQty, tx.Instrument.InstrumentID(), remaining)
	}
	return nil
}

// findPositionForLot resolves the position for a lot using chain_id (if set) or trade_id.
// Falls back to trade_id for legacy lots that predate chain assignment.
func (s *PositionService) findPositionForLot(ctx context.Context, accountID string, lot domain.PositionLot) (*domain.Position, error) {
	if lot.ChainID != "" {
		return s.positions.GetPositionByChainID(ctx, accountID, lot.ChainID)
	}
	return s.positions.GetPositionByTradeID(ctx, accountID, lot.TradeID)
}

// lotSignedQty returns the signed open quantity for a lot derived from the action:
//   - BTO/BUY → positive (long)
//   - STO/SELL → negative (short)
func lotSignedQty(tx domain.Transaction) decimal.Decimal {
	// CashFlowSign: sells = +1, buys = -1. Lot sign is the opposite of cash flow.
	if domain.CashFlowSign(tx.Action).IsPositive() {
		return tx.Quantity.Neg()
	}
	return tx.Quantity
}

// lotCashFlowSign returns the opening cash-flow sign for a lot from its signed quantity:
//   - Positive open_quantity → was a buy (debit) → sign = -1
//   - Negative open_quantity → was a sell (credit) → sign = +1
func lotCashFlowSign(lot domain.PositionLot) decimal.Decimal {
	if lot.OpenQuantity.IsPositive() {
		return decimal.NewFromInt(-1)
	}
	return decimal.NewFromInt(1)
}

// optionMultiplier returns the contract multiplier for an instrument (100 for equity
// options; 1 for equities and futures).
func optionMultiplier(inst domain.Instrument) decimal.Decimal {
	if inst.Option != nil {
		return inst.Option.Multiplier
	}
	return decimal.NewFromInt(1)
}

// processEquityTrade processes all equity transactions for a stock position using WAC
// (Weighted Average Cost). Transactions are sorted chronologically before processing.
// Not concurrent-safe: the guards in equitySell and equityShortCover and their subsequent
// UpdatePosition calls are not atomic; concurrent or overlapping imports for the same
// account may produce incorrect results.
func (s *PositionService) processEquityTrade(ctx context.Context, tradeID string, txns []domain.Transaction, chainID string) error {
	for _, tx := range txns {
		if tx.TradeID != tradeID {
			return fmt.Errorf("position service: transaction %s has trade_id %q, expected %q", tx.ID, tx.TradeID, tradeID)
		}
	}
	sorted := make([]domain.Transaction, len(txns))
	copy(sorted, txns)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ExecutedAt.Before(sorted[j].ExecutedAt)
	})
	for _, tx := range sorted {
		switch tx.Action {
		case domain.ActionBTO, domain.ActionBuy:
			if err := s.equityBuy(ctx, tradeID, chainID, tx); err != nil {
				return fmt.Errorf("equity buy tx %s: %w", tx.ID, err)
			}
		case domain.ActionSTC, domain.ActionSell:
			if err := s.equitySell(ctx, chainID, tx); err != nil {
				return fmt.Errorf("equity sell tx %s: %w", tx.ID, err)
			}
		case domain.ActionSTO:
			if err := s.equityShortOpen(ctx, tradeID, chainID, tx); err != nil {
				return fmt.Errorf("equity short open tx %s: %w", tx.ID, err)
			}
		case domain.ActionBTC:
			if err := s.equityShortCover(ctx, chainID, tx); err != nil {
				return fmt.Errorf("equity short cover tx %s: %w", tx.ID, err)
			}
		default:
			s.logger.Warn("position service: unhandled equity action; skipping",
				"action", tx.Action, "tx_id", tx.ID)
		}
	}
	return nil
}

// equityBuy creates or updates the stock position for a buy using WAC (Weighted Average Cost).
//
//	new_avg = (held_qty × old_avg + buy_qty × price + fees) / new_total_qty
func (s *PositionService) equityBuy(ctx context.Context, tradeID, chainID string, tx domain.Transaction) error {
	qty := tx.Quantity.Abs()
	if qty.IsZero() {
		return fmt.Errorf("equity buy tx %s has zero quantity", tx.ID)
	}
	avgCost := tx.FillPrice.Add(tx.Fees.Div(qty))

	existing, err := s.positions.GetPositionByChainID(ctx, tx.AccountID, chainID)
	if errors.Is(err, domain.ErrNotFound) {
		posID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate position id: %w", err)
		}
		return s.positions.CreatePosition(ctx, &domain.Position{
			ID:                 posID.String(),
			AccountID:          tx.AccountID,
			ChainID:            chainID,
			OriginatingTradeID: tradeID,
			UnderlyingSymbol:   tx.Instrument.Symbol,
			NetQuantity:        qty,
			AvgCostPerShare:    avgCost,
			OpenedAt:           tx.ExecutedAt,
			UpdatedAt:          tx.ExecutedAt,
			StrategyType:       domain.StrategyStock,
		})
	}
	if err != nil {
		return fmt.Errorf("get position: %w", err)
	}

	if existing.ClosedAt != nil {
		// Re-opening: reset open fields, retain realized P&L.
		existing.NetQuantity = qty
		existing.AvgCostPerShare = avgCost
		existing.ClosedAt = nil
		existing.OpenedAt = tx.ExecutedAt
		existing.UpdatedAt = tx.ExecutedAt
		return s.positions.UpdatePosition(ctx, existing)
	}

	newQty := existing.NetQuantity.Add(qty)
	existing.AvgCostPerShare = existing.NetQuantity.Mul(existing.AvgCostPerShare).
		Add(qty.Mul(tx.FillPrice)).
		Add(tx.Fees).
		Div(newQty)
	existing.NetQuantity = newQty
	existing.UpdatedAt = tx.ExecutedAt
	return s.positions.UpdatePosition(ctx, existing)
}

// equitySell updates the stock position for a sell.
//
//	realized = qty × sell_price − sell_fees − qty × avg_cost_per_share
func (s *PositionService) equitySell(ctx context.Context, chainID string, tx domain.Transaction) error {
	existing, err := s.positions.GetPositionByChainID(ctx, tx.AccountID, chainID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("no long position found for %s", tx.Instrument.Symbol)
		}
		return fmt.Errorf("get position: %w", err)
	}

	qty := tx.Quantity.Abs()
	if qty.GreaterThan(existing.NetQuantity) {
		return fmt.Errorf("sell %s shares of %s but only %s held", qty, tx.Instrument.Symbol, existing.NetQuantity)
	}

	realized := qty.Mul(tx.FillPrice).Sub(tx.Fees).Sub(qty.Mul(existing.AvgCostPerShare))
	existing.NetQuantity = existing.NetQuantity.Sub(qty)
	existing.RealizedPnL = existing.RealizedPnL.Add(realized)
	existing.UpdatedAt = tx.ExecutedAt
	if existing.NetQuantity.IsZero() {
		existing.ClosedAt = &tx.ExecutedAt
	}
	return s.positions.UpdatePosition(ctx, existing)
}

// equityShortOpen creates or extends a short stock position using WAC.
//
//	avg_short = (held × old_avg + qty × price − fees) / new_total
//
// Fees are subtracted (not added as in equityBuy) because they reduce the proceeds
// received on a short sale rather than increasing the cost.
func (s *PositionService) equityShortOpen(ctx context.Context, tradeID, chainID string, tx domain.Transaction) error {
	qty := tx.Quantity.Abs()
	if qty.IsZero() {
		return fmt.Errorf("equity short open tx %s has zero quantity", tx.ID)
	}
	// AvgCostPerShare = effective short price net of fees (what we actually received per share).
	avgCost := tx.FillPrice.Sub(tx.Fees.Div(qty))

	existing, err := s.positions.GetPositionByChainID(ctx, tx.AccountID, chainID)
	if errors.Is(err, domain.ErrNotFound) {
		posID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate position id: %w", err)
		}
		return s.positions.CreatePosition(ctx, &domain.Position{
			ID:                 posID.String(),
			AccountID:          tx.AccountID,
			ChainID:            chainID,
			OriginatingTradeID: tradeID,
			UnderlyingSymbol:   tx.Instrument.Symbol,
			NetQuantity:        qty.Neg(),
			AvgCostPerShare:    avgCost,
			OpenedAt:           tx.ExecutedAt,
			UpdatedAt:          tx.ExecutedAt,
			StrategyType:       domain.StrategyStock,
		})
	}
	if err != nil {
		return fmt.Errorf("get position: %w", err)
	}

	if existing.ClosedAt != nil {
		// Re-opening a short after the previous short was fully covered.
		existing.NetQuantity = qty.Neg()
		existing.AvgCostPerShare = avgCost
		existing.ClosedAt = nil
		existing.OpenedAt = tx.ExecutedAt
		existing.UpdatedAt = tx.ExecutedAt
		return s.positions.UpdatePosition(ctx, existing)
	}

	// Accumulate into existing short — WAC on absolute quantities, then re-negate.
	heldAbs := existing.NetQuantity.Abs()
	newQty := heldAbs.Add(qty)
	existing.AvgCostPerShare = heldAbs.Mul(existing.AvgCostPerShare).
		Add(qty.Mul(tx.FillPrice)).
		Sub(tx.Fees).
		Div(newQty)
	existing.NetQuantity = newQty.Neg()
	existing.UpdatedAt = tx.ExecutedAt
	return s.positions.UpdatePosition(ctx, existing)
}

// equityShortCover reduces a short stock position and computes realized P&L.
//
//	realized = qty × (avg_short_price − cover_price) − cover_fees
func (s *PositionService) equityShortCover(ctx context.Context, chainID string, tx domain.Transaction) error {
	existing, err := s.positions.GetPositionByChainID(ctx, tx.AccountID, chainID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("no short position found for %s", tx.Instrument.Symbol)
		}
		return fmt.Errorf("get position: %w", err)
	}

	qty := tx.Quantity.Abs()
	if qty.IsZero() {
		return fmt.Errorf("equity short cover tx %s has zero quantity", tx.ID)
	}
	if existing.NetQuantity.GreaterThanOrEqual(decimal.Zero) {
		return fmt.Errorf("cover short: position for %s is long or flat (%s shares), not short",
			tx.Instrument.Symbol, existing.NetQuantity)
	}
	heldShort := existing.NetQuantity.Abs()
	if qty.GreaterThan(heldShort) {
		return fmt.Errorf("cover %s shares of %s but only %s short", qty, tx.Instrument.Symbol, heldShort)
	}

	realized := qty.Mul(existing.AvgCostPerShare).Sub(qty.Mul(tx.FillPrice)).Sub(tx.Fees)
	existing.NetQuantity = existing.NetQuantity.Add(qty) // toward zero
	existing.RealizedPnL = existing.RealizedPnL.Add(realized)
	existing.UpdatedAt = tx.ExecutedAt
	if existing.NetQuantity.IsZero() {
		existing.ClosedAt = &tx.ExecutedAt
	}
	return s.positions.UpdatePosition(ctx, existing)
}

// allEquity reports whether every transaction in txns is an equity (stock) trade.
// Returns false for an empty slice: an empty trade should not be routed to the equity path.
func allEquity(txns []domain.Transaction) bool {
	if len(txns) == 0 {
		return false
	}
	for _, tx := range txns {
		if tx.Instrument.AssetClass != domain.AssetClassEquity {
			return false
		}
	}
	return true
}
