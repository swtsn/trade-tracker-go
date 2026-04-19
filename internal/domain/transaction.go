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

// CashFlowSign returns +1 for sell/credit actions (STO, STC, SELL) and -1 for
// buy/debit actions (BTO, BTC, BUY). Returns 0 for neutral actions such as
// ASSIGNMENT, EXPIRATION, and EXERCISE.
func CashFlowSign(action Action) decimal.Decimal {
	switch action {
	case ActionSTO, ActionSTC, ActionSell:
		return decimal.NewFromInt(1)
	case ActionBTO, ActionBTC, ActionBuy:
		return decimal.NewFromInt(-1)
	default:
		return decimal.Zero
	}
}

// Transaction represents a single execution event within a trade.
// It is the atomic unit of trading activity, recording quantity, price, and fees.
type Transaction struct {
	ID             string
	TradeID        string
	BrokerTxID     string // synthetic stable identifier derived from broker-specific fields (e.g. order ID + leg index); used for idempotent import dedup.
	BrokerOrderID  string // broker's order ID; groups legs of the same multi-leg order. Support is broker-specific — may be empty if the broker does not expose an order ID.
	Broker         string
	AccountID      string
	Instrument     Instrument
	Action         Action
	Quantity       decimal.Decimal // always non-negative; direction is encoded in Action
	FillPrice      decimal.Decimal
	Fees           decimal.Decimal
	ExecutedAt     time.Time
	PositionEffect PositionEffect
}
