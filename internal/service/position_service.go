package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/repository"
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
}

// NewPositionService creates a PositionService with the given repository.
func NewPositionService(positions repository.PositionRepository) *PositionService {
	return &PositionService{positions: positions}
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
// txns must be in closing-first order (guaranteed by ImportService).
// Opening legs create new lots and upsert the position row for the trade.
// Closing legs FIFO-match against open lots, compute realized P&L, and stamp
// position.closed_at when all lots under the position are fully closed.
func (s *PositionService) ProcessTrade(ctx context.Context, tradeID string, txns []domain.Transaction, chainID string) error {
	for _, tx := range txns {
		if tx.TradeID != tradeID {
			return fmt.Errorf("position service: transaction %s has trade_id %q, expected %q", tx.ID, tx.TradeID, tradeID)
		}
		switch tx.PositionEffect {
		case domain.PositionEffectClosing:
			if err := s.processClosing(ctx, tx); err != nil {
				if errors.Is(err, ErrNoOpenLots) {
					log.Printf("position service: closing tx %s: %v; skipping", tx.ID, err)
					continue
				}
				return fmt.Errorf("position service: closing tx %s: %w", tx.ID, err)
			}
		case domain.PositionEffectOpening:
			if err := s.processOpening(ctx, tradeID, chainID, tx); err != nil {
				return fmt.Errorf("position service: opening tx %s: %w", tx.ID, err)
			}
		}
	}
	return nil
}

// processOpening creates a lot and upserts the position row for tradeID.
func (s *PositionService) processOpening(ctx context.Context, tradeID, chainID string, tx domain.Transaction) error {
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
			StrategyType:       domain.StrategyUnknown,
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
		return fmt.Errorf("%w: instrument %s account %s", ErrNoOpenLots, tx.Instrument.InstrumentID(), tx.AccountID)
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
		if err := s.positions.CloseLot(ctx, closing, newRemaining, lotClosedAt); err != nil {
			return fmt.Errorf("close lot %s: %w", lot.ID, err)
		}

		// TODO(tracked): CloseLot and accumulatePnL (UpdatePosition) must eventually run
		// in the same DB transaction. The current gap is not a concurrency issue
		// (MaxOpenConns=1 prevents concurrent writers), but a crash between the two
		// calls leaves the lot permanently closed while positions.realized_pnl is never
		// updated — silently wrong totals with no audit trail.
		//
		// To detect divergence, run:
		//   SELECT lc.lot_id, SUM(lc.realized_pnl) AS lot_pnl,
		//          p.realized_pnl AS pos_pnl
		//   FROM lot_closings lc
		//   JOIN position_lots pl ON pl.id = lc.lot_id
		//   JOIN positions p ON p.chain_id = pl.chain_id
		//   GROUP BY lc.lot_id, p.realized_pnl
		//   HAVING ABS(lot_pnl - pos_pnl) > 0.001;
		//
		// Fix requires transaction-scoped repository operations (BeginTx on Repos).
		if err := s.accumulatePnL(ctx, lot, tx.AccountID, realizedPnL, tx.ExecutedAt); err != nil {
			return err
		}
	}
	if !remaining.IsZero() {
		return fmt.Errorf("closing quantity %s exceeds open lots for instrument %s: %s unmatched",
			totalCloseQty, tx.Instrument.InstrumentID(), remaining)
	}
	return nil
}

// accumulatePnL adds realizedPnL to the position associated with the lot and
// stamps position.closed_at if all lots under the position are now closed.
// The position is located via lot.ChainID (if set) or lot.TradeID.
func (s *PositionService) accumulatePnL(
	ctx context.Context,
	lot domain.PositionLot,
	accountID string,
	pnl decimal.Decimal,
	updatedAt time.Time,
) error {
	pos, err := s.findPositionForLot(ctx, accountID, lot)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// A lot exists but has no matching position row. This can happen for
			// historical imports where position tracking was not yet active, but it
			// also masks bugs (e.g. prior hook failure that skipped CreatePosition).
			// Log so the discrepancy is detectable; realized_pnl will not be updated.
			log.Printf("position service: lot %s (chain %s, trade %s) has no position row; realized_pnl not updated",
				lot.ID, lot.ChainID, lot.TradeID)
			return nil
		}
		return fmt.Errorf("find position for lot %s: %w", lot.ID, err)
	}

	pos.RealizedPnL = pos.RealizedPnL.Add(pnl)
	pos.UpdatedAt = updatedAt

	// Determine whether all lots under this position are now closed.
	// For chained positions the check must span all trades in the chain; using
	// the originating trade alone would prematurely close the position when the
	// first trade's lots are exhausted while sibling trades still hold open lots.
	var open []domain.PositionLot
	if lot.ChainID != "" {
		open, err = s.positions.ListOpenLotsByChain(ctx, accountID, lot.ChainID)
	} else {
		open, err = s.positions.ListOpenLotsByTrade(ctx, accountID, pos.OriginatingTradeID)
	}
	if err != nil {
		return fmt.Errorf("list open lots for position: %w", err)
	}
	if len(open) == 0 {
		t := updatedAt
		pos.ClosedAt = &t
	}

	return s.positions.UpdatePosition(ctx, pos)
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
