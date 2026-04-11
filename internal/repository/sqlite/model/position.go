package model

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"trade-tracker-go/internal/domain"
)

// Position is a joined row: positions + instruments.
type Position struct {
	ID           string
	AccountID    string
	InstrumentID string
	Quantity     string
	CostBasis    string
	RealizedPnL  string
	OpenedAt     string
	UpdatedAt    string
	ClosedAt     sql.NullString
	ChainID      sql.NullString
	Inst         Instrument
}

// ScanDest returns the pointers to scan destinations matching the SELECT column order.
func (r *Position) ScanDest() []any {
	return append(
		[]any{
			&r.ID, &r.AccountID, &r.InstrumentID,
			&r.Quantity, &r.CostBasis, &r.RealizedPnL,
			&r.OpenedAt, &r.UpdatedAt, &r.ClosedAt, &r.ChainID,
		},
		r.Inst.ScanDest()...,
	)
}

// ToDomain converts to a domain.Position.
func (r Position) ToDomain() (domain.Position, error) {
	inst, err := r.Inst.ToDomain()
	if err != nil {
		return domain.Position{}, fmt.Errorf("position instrument: %w", err)
	}
	qty, err := decimal.NewFromString(r.Quantity)
	if err != nil {
		return domain.Position{}, fmt.Errorf("position quantity: %w", err)
	}
	costBasis, err := decimal.NewFromString(r.CostBasis)
	if err != nil {
		return domain.Position{}, fmt.Errorf("position cost_basis: %w", err)
	}
	realizedPnL, err := decimal.NewFromString(r.RealizedPnL)
	if err != nil {
		return domain.Position{}, fmt.Errorf("position realized_pnl: %w", err)
	}
	openedAt, err := time.Parse(time.RFC3339, r.OpenedAt)
	if err != nil {
		return domain.Position{}, fmt.Errorf("position opened_at: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339, r.UpdatedAt)
	if err != nil {
		return domain.Position{}, fmt.Errorf("position updated_at: %w", err)
	}

	p := domain.Position{
		ID:          r.ID,
		AccountID:   r.AccountID,
		Instrument:  inst,
		Quantity:    qty,
		CostBasis:   costBasis,
		RealizedPnL: realizedPnL,
		OpenedAt:    openedAt,
		UpdatedAt:   updatedAt,
	}
	if r.ClosedAt.Valid {
		t, err := time.Parse(time.RFC3339, r.ClosedAt.String)
		if err != nil {
			return domain.Position{}, fmt.Errorf("position closed_at: %w", err)
		}
		p.ClosedAt = &t
	}
	if r.ChainID.Valid {
		chainID := r.ChainID.String
		p.ChainID = &chainID
	}
	return p, nil
}

// PositionToStorage converts a domain.Position to its flat storage struct.
func PositionToStorage(pos domain.Position) Position {
	s := Position{
		ID:           pos.ID,
		AccountID:    pos.AccountID,
		InstrumentID: pos.Instrument.InstrumentID(),
		Quantity:     pos.Quantity.String(),
		CostBasis:    pos.CostBasis.String(),
		RealizedPnL:  pos.RealizedPnL.String(),
		OpenedAt:     pos.OpenedAt.UTC().Format(time.RFC3339),
		UpdatedAt:    pos.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if pos.ClosedAt != nil {
		s.ClosedAt = sql.NullString{String: pos.ClosedAt.UTC().Format(time.RFC3339), Valid: true}
	}
	if pos.ChainID != nil {
		s.ChainID = sql.NullString{String: *pos.ChainID, Valid: true}
	}
	return s
}

// PositionLot is a joined row: position_lots + instruments.
type PositionLot struct {
	ID                string
	AccountID         string
	InstrumentID      string
	TradeID           string
	OpeningTxID       string
	OpenQuantity      string
	RemainingQuantity string
	OpenPrice         string
	OpenFees          string
	OpenedAt          string
	ClosedAt          sql.NullString
	ChainID           sql.NullString
	Inst              Instrument
}

// ScanDest returns the pointers to scan destinations matching the SELECT column order.
func (r *PositionLot) ScanDest() []any {
	return append(
		[]any{
			&r.ID, &r.AccountID, &r.InstrumentID, &r.TradeID, &r.OpeningTxID,
			&r.OpenQuantity, &r.RemainingQuantity, &r.OpenPrice, &r.OpenFees,
			&r.OpenedAt, &r.ClosedAt, &r.ChainID,
		},
		r.Inst.ScanDest()...,
	)
}

// ToDomain converts to a domain.PositionLot.
func (r PositionLot) ToDomain() (domain.PositionLot, error) {
	inst, err := r.Inst.ToDomain()
	if err != nil {
		return domain.PositionLot{}, fmt.Errorf("lot instrument: %w", err)
	}
	openQty, err := decimal.NewFromString(r.OpenQuantity)
	if err != nil {
		return domain.PositionLot{}, fmt.Errorf("lot open_quantity: %w", err)
	}
	remainingQty, err := decimal.NewFromString(r.RemainingQuantity)
	if err != nil {
		return domain.PositionLot{}, fmt.Errorf("lot remaining_quantity: %w", err)
	}
	openPrice, err := decimal.NewFromString(r.OpenPrice)
	if err != nil {
		return domain.PositionLot{}, fmt.Errorf("lot open_price: %w", err)
	}
	openFees, err := decimal.NewFromString(r.OpenFees)
	if err != nil {
		return domain.PositionLot{}, fmt.Errorf("lot open_fees: %w", err)
	}
	openedAt, err := time.Parse(time.RFC3339, r.OpenedAt)
	if err != nil {
		return domain.PositionLot{}, fmt.Errorf("lot opened_at: %w", err)
	}

	lot := domain.PositionLot{
		ID:                r.ID,
		AccountID:         r.AccountID,
		Instrument:        inst,
		TradeID:           r.TradeID,
		OpeningTxID:       r.OpeningTxID,
		OpenQuantity:      openQty,
		RemainingQuantity: remainingQty,
		OpenPrice:         openPrice,
		OpenFees:          openFees,
		OpenedAt:          openedAt,
	}
	if r.ClosedAt.Valid {
		t, err := time.Parse(time.RFC3339, r.ClosedAt.String)
		if err != nil {
			return domain.PositionLot{}, fmt.Errorf("lot closed_at: %w", err)
		}
		lot.ClosedAt = &t
	}
	if r.ChainID.Valid {
		chainID := r.ChainID.String
		lot.ChainID = &chainID
	}
	return lot, nil
}

// LotToStorage converts a domain.PositionLot to its flat storage struct.
func LotToStorage(lot domain.PositionLot) PositionLot {
	s := PositionLot{
		ID:                lot.ID,
		AccountID:         lot.AccountID,
		InstrumentID:      lot.Instrument.InstrumentID(),
		TradeID:           lot.TradeID,
		OpeningTxID:       lot.OpeningTxID,
		OpenQuantity:      lot.OpenQuantity.String(),
		RemainingQuantity: lot.RemainingQuantity.String(),
		OpenPrice:         lot.OpenPrice.String(),
		OpenFees:          lot.OpenFees.String(),
		OpenedAt:          lot.OpenedAt.UTC().Format(time.RFC3339),
	}
	if lot.ClosedAt != nil {
		s.ClosedAt = sql.NullString{String: lot.ClosedAt.UTC().Format(time.RFC3339), Valid: true}
	}
	if lot.ChainID != nil {
		s.ChainID = sql.NullString{String: *lot.ChainID, Valid: true}
	}
	return s
}

// LotClosing holds the flat, SQL-scannable fields for a lot_closings row.
type LotClosing struct {
	ID             string
	LotID          string
	ClosingTxID    string
	ClosedQuantity string
	ClosePrice     string
	CloseFees      string
	RealizedPnL    string
	ClosedAt       string
	ResultingLotID sql.NullString
}

// ToDomain converts to a domain.LotClosing.
func (s LotClosing) ToDomain() (domain.LotClosing, error) {
	closedQty, err := decimal.NewFromString(s.ClosedQuantity)
	if err != nil {
		return domain.LotClosing{}, fmt.Errorf("lot_closing closed_quantity: %w", err)
	}
	closePrice, err := decimal.NewFromString(s.ClosePrice)
	if err != nil {
		return domain.LotClosing{}, fmt.Errorf("lot_closing close_price: %w", err)
	}
	closeFees, err := decimal.NewFromString(s.CloseFees)
	if err != nil {
		return domain.LotClosing{}, fmt.Errorf("lot_closing close_fees: %w", err)
	}
	realizedPnL, err := decimal.NewFromString(s.RealizedPnL)
	if err != nil {
		return domain.LotClosing{}, fmt.Errorf("lot_closing realized_pnl: %w", err)
	}
	closedAt, err := time.Parse(time.RFC3339, s.ClosedAt)
	if err != nil {
		return domain.LotClosing{}, fmt.Errorf("lot_closing closed_at: %w", err)
	}

	lc := domain.LotClosing{
		ID:             s.ID,
		LotID:          s.LotID,
		ClosingTxID:    s.ClosingTxID,
		ClosedQuantity: closedQty,
		ClosePrice:     closePrice,
		CloseFees:      closeFees,
		RealizedPnL:    realizedPnL,
		ClosedAt:       closedAt,
	}
	if s.ResultingLotID.Valid {
		id := s.ResultingLotID.String
		lc.ResultingLotID = &id
	}
	return lc, nil
}

// LotClosingToStorage converts a domain.LotClosing to its flat storage struct.
func LotClosingToStorage(lc domain.LotClosing) LotClosing {
	s := LotClosing{
		ID:             lc.ID,
		LotID:          lc.LotID,
		ClosingTxID:    lc.ClosingTxID,
		ClosedQuantity: lc.ClosedQuantity.String(),
		ClosePrice:     lc.ClosePrice.String(),
		CloseFees:      lc.CloseFees.String(),
		RealizedPnL:    lc.RealizedPnL.String(),
		ClosedAt:       lc.ClosedAt.UTC().Format(time.RFC3339),
	}
	if lc.ResultingLotID != nil {
		s.ResultingLotID = sql.NullString{String: *lc.ResultingLotID, Valid: true}
	}
	return s
}
