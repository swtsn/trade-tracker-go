// Package service implements the business logic layer for trade-tracker.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/repository"
)

// PositionService manages position lot state and the materialized position cache.
// It is called by ImportService during trade processing; it is not exposed externally.
type PositionService struct {
	positions repository.PositionRepository
}

// NewPositionService creates a PositionService backed by the given repository.
func NewPositionService(positions repository.PositionRepository) *PositionService {
	return &PositionService{positions: positions}
}

// OpenLot creates a new PositionLot for an opening transaction.
// The lot's quantity is signed: positive for long (BTO/BUY), negative for short (STO/SELL).
func (s *PositionService) OpenLot(ctx context.Context, tx domain.Transaction) (*domain.PositionLot, error) {
	qty, err := signedQty(tx)
	if err != nil {
		return nil, fmt.Errorf("open lot: %w", err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("open lot: generate id: %w", err)
	}
	lot := &domain.PositionLot{
		ID:                id.String(),
		AccountID:         tx.AccountID,
		Instrument:        tx.Instrument,
		TradeID:           tx.TradeID,
		OpeningTxID:       tx.ID,
		OpenQuantity:      qty,
		RemainingQuantity: qty,
		OpenPrice:         tx.FillPrice,
		OpenFees:          tx.Fees,
		OpenedAt:          tx.ExecutedAt,
		ChainID:           tx.ChainID,
	}
	if err := s.positions.CreateLot(ctx, lot); err != nil {
		return nil, fmt.Errorf("open lot: %w", err)
	}
	return lot, nil
}

// CloseLots FIFO-matches a closing transaction against open lots for the instrument.
// Lots are consumed in opened_at ASC order (oldest first); id ASC breaks ties.
// Returns one LotClosing per lot consumed and updates each lot's remaining quantity.
// Closing fees are prorated across lots by their share of the total closing quantity.
// After all lot closings are recorded, the position's RealizedPnL is incremented.
//
// Callers must ensure tx.Quantity is strictly positive (the domain model convention).
// Broker parsers that emit signed quantities for sell/short transactions must strip the
// sign before passing the transaction to this method.
//
// NOTE: each CloseLot repo call commits its own DB transaction independently.
// Partial failure can leave position.realized_pnl inconsistent with lot_closings.
// See docs/future.md "Transaction propagation across service calls" for the failure
// mode, detection query, and remediation plan.
func (s *PositionService) CloseLots(ctx context.Context, tx domain.Transaction) ([]domain.LotClosing, error) {
	instrumentID := tx.Instrument.InstrumentID()

	lots, err := s.positions.ListOpenLotsByInstrument(ctx, tx.AccountID, instrumentID)
	if err != nil {
		return nil, fmt.Errorf("close lots: list open lots: %w", err)
	}

	toClose := tx.Quantity
	if toClose.IsZero() || toClose.IsNegative() {
		return nil, fmt.Errorf("close lots: closing quantity must be positive, got %s", toClose)
	}

	// Validate that the closing action is directionally consistent with the open lots
	// before mutating anything — prevents silently mismatching BTC against a long lot.
	if len(lots) > 0 {
		if err := validateClosingDirection(tx.Action, lots[0]); err != nil {
			return nil, fmt.Errorf("close lots: %w", err)
		}
	}

	// Pre-check total available quantity before any DB writes. Without this guard,
	// a partial set of lots would be committed before the error is returned, leaving
	// the DB in an inconsistent state with no rollback path.
	var totalAvailable decimal.Decimal
	for _, lot := range lots {
		totalAvailable = totalAvailable.Add(lot.RemainingQuantity.Abs())
	}
	if totalAvailable.LessThan(toClose) {
		return nil, fmt.Errorf("close lots: insufficient open quantity: have %s, want %s", totalAvailable, toClose)
	}

	var closings []domain.LotClosing
	for _, lot := range lots {
		if toClose.IsZero() {
			break
		}

		absRemaining := lot.RemainingQuantity.Abs()
		closedQty := decimal.Min(toClose, absRemaining)

		// Prorate closing fees by this lot's share of the total closing quantity.
		// Denominator is tx.Quantity (the full closing order size), not absRemaining,
		// so that fees are split in proportion to how much of the order each lot absorbs.
		closeFees := tx.Fees.Mul(closedQty).Div(tx.Quantity)

		pnl := calcPnL(lot, closedQty, tx.FillPrice, closeFees)

		// Decrement remaining_quantity preserving its sign.
		var newRemaining decimal.Decimal
		if lot.RemainingQuantity.IsPositive() {
			newRemaining = lot.RemainingQuantity.Sub(closedQty)
		} else {
			newRemaining = lot.RemainingQuantity.Add(closedQty)
		}

		var fullyClosedAt *time.Time
		if newRemaining.IsZero() {
			t := tx.ExecutedAt
			fullyClosedAt = &t
		}

		closingID, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("close lots: generate closing id: %w", err)
		}
		closing := domain.LotClosing{
			ID:             closingID.String(),
			LotID:          lot.ID,
			ClosingTxID:    tx.ID,
			ClosedQuantity: closedQty,
			ClosePrice:     tx.FillPrice,
			CloseFees:      closeFees,
			RealizedPnL:    pnl,
			ClosedAt:       tx.ExecutedAt,
		}
		if err := s.positions.CloseLot(ctx, &closing, newRemaining, fullyClosedAt); err != nil {
			return nil, fmt.Errorf("close lots: lot %s: %w", lot.ID, err)
		}

		closings = append(closings, closing)
		toClose = toClose.Sub(closedQty)
	}

	if !toClose.IsZero() {
		// Unreachable: the pre-check above guarantees totalAvailable >= toClose.
		panic(fmt.Sprintf("close lots: post-loop quantity invariant violated: %s remaining", toClose))
	}

	// Accumulate the total realized P&L onto the materialized position.
	var totalPnL decimal.Decimal
	for _, c := range closings {
		totalPnL = totalPnL.Add(c.RealizedPnL)
	}
	pos, err := s.positions.GetPosition(ctx, tx.AccountID, instrumentID)
	if err != nil {
		return nil, fmt.Errorf("close lots: get position for pnl update: %w", err)
	}
	pos.RealizedPnL = pos.RealizedPnL.Add(totalPnL)
	pos.UpdatedAt = time.Now().UTC()
	if err := s.positions.UpsertPosition(ctx, pos); err != nil {
		return nil, fmt.Errorf("close lots: update position realized pnl: %w", err)
	}

	return closings, nil
}

// RefreshPosition recalculates and upserts the materialized Position for
// (accountID, instrumentID). It recomputes Quantity and CostBasis from open lots and
// leaves RealizedPnL unchanged (maintained incrementally by CloseLots).
// Creates a new Position if none exists yet. Sets ClosedAt when all lots are exhausted.
//
// CostBasis is signed: negative for net short positions (premium received).
func (s *PositionService) RefreshPosition(ctx context.Context, accountID, instrumentID string) error {
	lots, err := s.positions.ListOpenLotsByInstrument(ctx, accountID, instrumentID)
	if err != nil {
		return fmt.Errorf("refresh position: list lots: %w", err)
	}

	var totalQty, costBasis decimal.Decimal
	var earliest time.Time
	for i, lot := range lots {
		totalQty = totalQty.Add(lot.RemainingQuantity)
		costBasis = costBasis.Add(lot.RemainingQuantity.Mul(lot.OpenPrice))
		if i == 0 || lot.OpenedAt.Before(earliest) {
			earliest = lot.OpenedAt
		}
	}

	pos, err := s.positions.GetPosition(ctx, accountID, instrumentID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("refresh position: get position: %w", err)
	}

	now := time.Now().UTC()

	if errors.Is(err, domain.ErrNotFound) {
		if len(lots) == 0 {
			// No lots, no position: nothing to materialize.
			return nil
		}
		pos = &domain.Position{
			ID:          uuid.New().String(),
			AccountID:   accountID,
			Instrument:  lots[0].Instrument,
			RealizedPnL: decimal.Zero,
			OpenedAt:    earliest,
		}
	}

	pos.Quantity = totalQty
	pos.CostBasis = costBasis
	pos.UpdatedAt = now
	// Use len(lots) == 0, not totalQty.IsZero(), to avoid marking a position closed when
	// rounding or partial lot errors cause signed lots to net to zero while lots remain open.
	if len(lots) == 0 {
		pos.ClosedAt = &now
	} else {
		pos.ClosedAt = nil
	}

	return s.positions.UpsertPosition(ctx, pos)
}

// calcPnL computes the realized P&L for closing closedQty contracts of lot at closePrice.
//
//	long  lot: pnl = (closePrice − openPrice) × closedQty × multiplier − closeFees − openFees×proportion
//	short lot: pnl = (openPrice − closePrice) × closedQty × multiplier − closeFees − openFees×proportion
//
// proportion = closedQty / |openQuantity| prorates the opening fees for the closed slice.
// Summed across all partial closes of a lot the proportions total 1, so 100% of opening
// fees are attributed exactly once.
func calcPnL(lot domain.PositionLot, closedQty, closePrice, closeFees decimal.Decimal) decimal.Decimal {
	multiplier := instrumentMultiplier(lot.Instrument)
	proportion := closedQty.Div(lot.OpenQuantity.Abs())
	openFeesPortion := lot.OpenFees.Mul(proportion)

	var priceDiff decimal.Decimal
	if lot.OpenQuantity.IsPositive() {
		priceDiff = closePrice.Sub(lot.OpenPrice)
	} else {
		priceDiff = lot.OpenPrice.Sub(closePrice)
	}

	gross := priceDiff.Mul(closedQty).Mul(multiplier)
	return gross.Sub(closeFees).Sub(openFeesPortion)
}

// instrumentMultiplier returns the contract multiplier for P&L calculations.
// Equity options use the multiplier from OptionDetails; all other instruments use 1.
func instrumentMultiplier(inst domain.Instrument) decimal.Decimal {
	if inst.Option != nil {
		return inst.Option.Multiplier
	}
	return decimal.NewFromInt(1)
}

// signedQty returns the signed lot quantity for an opening transaction.
// Long actions (BTO, BUY, ASSIGNMENT, EXERCISE) produce positive quantity.
// Short actions (STO, SELL) produce negative quantity.
// Returns an error for any action not recognized as an opening action, preventing
// unrecognized broker actions from silently producing incorrect lot signs.
func signedQty(tx domain.Transaction) (decimal.Decimal, error) {
	switch tx.Action {
	case domain.ActionBTO, domain.ActionBuy, domain.ActionAssignment, domain.ActionExercise:
		return tx.Quantity, nil
	case domain.ActionSTO, domain.ActionSell:
		return tx.Quantity.Neg(), nil
	default:
		return decimal.Zero, fmt.Errorf("unrecognized opening action %q", tx.Action)
	}
}

// validateClosingDirection returns an error if the closing action is directionally
// inconsistent with the lot being closed.
//   - BTC / BUY closes short lots (negative remaining quantity)
//   - STC / SELL closes long lots (positive remaining quantity)
//
// ASSIGNMENT, EXPIRATION, and EXERCISE are context-dependent and not validated here.
func validateClosingDirection(action domain.Action, lot domain.PositionLot) error {
	switch action {
	case domain.ActionBTC, domain.ActionBuy:
		if lot.RemainingQuantity.IsPositive() {
			return fmt.Errorf("action %s targets a long lot (expected short)", action)
		}
	case domain.ActionSTC, domain.ActionSell:
		if lot.RemainingQuantity.IsNegative() {
			return fmt.Errorf("action %s targets a short lot (expected long)", action)
		}
	}
	return nil
}
