package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// LinkType represents the kind of event that connects trades within a chain.
type LinkType string

const (
	LinkTypeRoll       LinkType = "roll"
	LinkTypeAssignment LinkType = "assignment"
	LinkTypeExercise   LinkType = "exercise"
)

// Chain represents the full lifecycle of a position — spans rolls, assignments,
// and related trades on the same underlying.
type Chain struct {
	ID               string
	AccountID        string
	UnderlyingSymbol string
	OriginalTradeID  string
	CreatedAt        time.Time
	ClosedAt         *time.Time
	Links            []ChainLink
}

// ChainLink records one event within a chain.
type ChainLink struct {
	ID               string
	ChainID          string
	Sequence         int
	LinkType         LinkType
	ClosingTradeID   string
	OpeningTradeID   string
	LinkedAt         time.Time
	StrikeChange     decimal.Decimal // new - old (rolls only)
	ExpirationChange int             // calendar days forward (rolls only)
	CreditDebit      decimal.Decimal // net premium from the event
}
