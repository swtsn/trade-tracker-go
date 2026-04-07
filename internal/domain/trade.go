package domain

import "time"

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
