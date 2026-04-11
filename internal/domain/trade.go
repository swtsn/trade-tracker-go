package domain

import "time"

// StrategyType represents the type of trading strategy for a trade.
type StrategyType string

const (
	StrategyUnknown       StrategyType = "unknown"
	StrategyIronButterfly StrategyType = "iron_butterfly"
	StrategyIronCondor    StrategyType = "iron_condor"
	StrategyCallButterfly StrategyType = "call_butterfly"
	StrategyPutButterfly  StrategyType = "put_butterfly"
	StrategyCoveredCall   StrategyType = "covered_call"
	StrategyBackRatio     StrategyType = "back_ratio"
	StrategyStraddle      StrategyType = "straddle"
	StrategyStrangle      StrategyType = "strangle"
	StrategyCallVertical  StrategyType = "call_vertical"
	StrategyPutVertical   StrategyType = "put_vertical"
	StrategyCalendar      StrategyType = "calendar"
	StrategyDiagonal      StrategyType = "diagonal"
	StrategyCSP           StrategyType = "csp"
	StrategySingle        StrategyType = "single"
	StrategyStock         StrategyType = "stock"
	StrategyFuture        StrategyType = "future"
)

// Trade represents a single trade comprising one or more transactions.
// A trade may be long (buy to open, sell to close) or short (sell to open, buy to close).
// It may also include options with associated strikes and expirations.
type Trade struct {
	ID           string
	AccountID    string
	Broker       string
	Transactions []Transaction
	StrategyType StrategyType
	OpenedAt     time.Time
	ClosedAt     *time.Time
	Notes        string
}
