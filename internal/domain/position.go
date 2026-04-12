package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// Position is a materialized cache of current open state per (account, instrument).
// Written in the same DB transaction as lot changes. Never written independently.
type Position struct {
	ID           string
	AccountID    string
	Instrument   Instrument
	Quantity     decimal.Decimal // signed: negative = short; 0 = closed
	CostBasis    decimal.Decimal
	RealizedPnL  decimal.Decimal
	OpenedAt     time.Time
	UpdatedAt    time.Time
	ClosedAt     *time.Time
	ChainID      *string
	StrategyType StrategyType  // classified from the position's own legs; independent of any trade
	Lots         []PositionLot // open lots only
}

// PositionLot is the source of truth. One row per opening transaction.
// Quantity is signed (negative = short). FIFO matching on close.
type PositionLot struct {
	ID                string
	AccountID         string
	Instrument        Instrument
	TradeID           string
	OpeningTxID       string
	OpenQuantity      decimal.Decimal // signed
	RemainingQuantity decimal.Decimal // decremented on each close
	OpenPrice         decimal.Decimal
	OpenFees          decimal.Decimal
	OpenedAt          time.Time
	ClosedAt          *time.Time
	ChainID           *string
}

// LotClosing records one close event against a lot.
type LotClosing struct {
	ID             string
	LotID          string
	ClosingTxID    string
	ClosedQuantity decimal.Decimal
	ClosePrice     decimal.Decimal
	CloseFees      decimal.Decimal
	RealizedPnL    decimal.Decimal
	ClosedAt       time.Time
	ResultingLotID *string // set for assignment/exercise: points to the new stock/futures lot
}
