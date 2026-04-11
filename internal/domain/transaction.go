package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// Action represents the type of transaction action (buy, sell, assignment, etc.).
type Action string

const (
	ActionBTO        Action = "BTO"
	ActionSTO        Action = "STO"
	ActionBTC        Action = "BTC"
	ActionSTC        Action = "STC"
	ActionBuy        Action = "BUY"
	ActionSell       Action = "SELL"
	ActionAssignment Action = "ASSIGNMENT"
	ActionExpiration Action = "EXPIRATION"
	ActionExercise   Action = "EXERCISE"
)

// PositionEffect indicates whether a transaction opens or closes a position.
type PositionEffect string

const (
	PositionEffectOpening PositionEffect = "opening"
	PositionEffectClosing PositionEffect = "closing"
)

// Transaction represents a single execution event within a trade.
// It is the atomic unit of trading activity, recording quantity, price, and fees.
type Transaction struct {
	ID             string
	TradeID        string
	BrokerTxID     string
	Broker         string
	AccountID      string
	Instrument     Instrument
	Action         Action
	Quantity       decimal.Decimal
	FillPrice      decimal.Decimal
	Fees           decimal.Decimal
	ExecutedAt     time.Time
	ChainID        *string
	PositionEffect PositionEffect
}
