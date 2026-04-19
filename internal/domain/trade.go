package domain

import "time"

// StrategyType represents the type of trading strategy for a trade.
type StrategyType string

const (
	StrategyUnknown              StrategyType = "unknown"
	StrategyIronButterfly        StrategyType = "iron_butterfly"
	StrategyIronCondor           StrategyType = "iron_condor"
	StrategyBrokenHeartButterfly StrategyType = "broken_heart_butterfly"
	StrategyButterfly            StrategyType = "butterfly"
	StrategyBrokenWingButterfly  StrategyType = "broken_wing_butterfly"
	StrategyCoveredCall          StrategyType = "covered_call"
	StrategyRatio                StrategyType = "ratio"
	StrategyBackRatio            StrategyType = "back_ratio"
	StrategyStraddle             StrategyType = "straddle"
	StrategyStrangle             StrategyType = "strangle"
	StrategyVertical             StrategyType = "vertical"
	StrategyCalendar             StrategyType = "calendar"
	StrategyDiagonal             StrategyType = "diagonal"
	StrategySingle               StrategyType = "single"
	StrategyStock                StrategyType = "stock"
	StrategyFuture               StrategyType = "future"
)

// Trade represents a single trade comprising one or more transactions.
// A trade may be long (buy to open, sell to close) or short (sell to open, buy to close).
// It may also include options with associated strikes and expirations.
type Trade struct {
	ID               string
	AccountID        string
	Broker           string
	Transactions     []Transaction
	StrategyType     StrategyType
	UnderlyingSymbol string
	OpenedAt         time.Time
	ClosedAt         *time.Time
	Notes            string
}
