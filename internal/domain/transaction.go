package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// Action represents the type of order action executed in a transaction.
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

// Transaction records a single fill event: one action on one instrument at one price.
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
