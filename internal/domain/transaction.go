package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

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

type PositionEffect string

const (
	PositionEffectOpening PositionEffect = "opening"
	PositionEffectClosing PositionEffect = "closing"
)

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
