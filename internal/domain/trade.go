// Package domain defines the core business entities and value objects for the trade tracker.
package domain

import "time"

// StrategyType identifies the options or stock strategy used in a trade.
type StrategyType string

const (
	StrategyUnknown       StrategyType = "unknown"
	StrategyIronButterfly      StrategyType = "iron_butterfly"
	StrategyIronCondor         StrategyType = "iron_condor"
	StrategyButterfly          StrategyType = "butterfly"
	StrategyBrokenWingButterfly StrategyType = "broken_wing_butterfly"
	StrategyBrokenHeartButterfly StrategyType = "broken_heart_butterfly"
	StrategyCoveredCall        StrategyType = "covered_call"
	StrategyRatio              StrategyType = "ratio"
	StrategyBackRatio          StrategyType = "back_ratio"
	StrategyStraddle           StrategyType = "straddle"
	StrategyStrangle           StrategyType = "strangle"
	StrategyVertical           StrategyType = "vertical"
	StrategyCalendar           StrategyType = "calendar"
	StrategyDiagonal           StrategyType = "diagonal"
	StrategyCSP                StrategyType = "csp"
	StrategySingle             StrategyType = "single"
	StrategyStock              StrategyType = "stock"
	StrategyFuture             StrategyType = "future"
)

// Trade represents a single trading event, grouping all transactions that belong to
// the same opening position.
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
