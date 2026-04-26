package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// Position represents one open or closed position in an account.
// A position always belongs to a chain; ChainID is set at creation time.
// Written by PositionService.ProcessTrade; never written independently.
// For StrategyStock positions, NetQuantity and AvgCostPerShare track WAC state;
// those fields are zero for all other strategy types.
type Position struct {
	ID                 string
	AccountID          string
	ChainID            string
	OriginatingTradeID string
	UnderlyingSymbol   string
	CostBasis          decimal.Decimal // positive = net credit received; negative = net debit paid (options/futures only)
	RealizedPnL        decimal.Decimal
	NetQuantity        decimal.Decimal // net shares held; only meaningful for StrategyStock
	AvgCostPerShare    decimal.Decimal // WAC including buy fees; only meaningful for StrategyStock
	OpenedAt           time.Time
	UpdatedAt          time.Time
	ClosedAt           *time.Time
	StrategyType       StrategyType
	Lots               []PositionLot // open lots only; not persisted — currently unpopulated, reserved for future use
	// ChainAttributionGap mirrors the chain's attribution_gap flag.
	// True when the chain was started from a mixed trade with unattributed closing P&L.
	ChainAttributionGap bool
}

// PositionLot is the source of truth for one opening transaction.
// Quantity is signed (negative = short). FIFO matching on close.
type PositionLot struct {
	ID                string
	AccountID         string
	Instrument        Instrument
	TradeID           string
	OpeningTxID       string
	OpenQuantity      decimal.Decimal // signed: negative = short
	RemainingQuantity decimal.Decimal // decremented on each close
	OpenPrice         decimal.Decimal
	OpenFees          decimal.Decimal
	OpenedAt          time.Time
	ClosedAt          *time.Time
	ChainID           string
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
