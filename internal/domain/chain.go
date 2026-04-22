package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// LinkType represents the kind of event that connects trades within a chain.
type LinkType string

const (
	LinkTypeOpen       LinkType = "open" // originating trade event; never stored in chain_links
	LinkTypeRoll       LinkType = "roll"
	LinkTypeAssignment LinkType = "assignment"
	LinkTypeExercise   LinkType = "exercise"
	LinkTypeClose      LinkType = "close" // records a close-only trade in the chain; the chain may or may not close after this event
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
	// AttributionGap is true when this chain was started from a mixed trade whose
	// closing legs could not be attributed to an existing open chain.
	AttributionGap bool
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

// ChainDetail is the enriched view of a chain returned by ChainService.GetChainDetail.
// Events are ordered chronologically: the originating trade first, then each
// ChainLink in sequence order.
type ChainDetail struct {
	Chain  *Chain
	Events []ChainEvent
	PnL    decimal.Decimal // net realized P&L; meaningful only when Chain.ClosedAt is non-nil
}

// ChainEvent represents one trade event within a chain's lifecycle.
type ChainEvent struct {
	TradeID     string
	EventType   LinkType        // LinkTypeOpen for the originating trade; otherwise from ChainLink
	CreditDebit decimal.Decimal // gross premium, fees excluded; positive = credit received; negative = debit paid
	ExecutedAt  time.Time
	Legs        []ChainEventLeg
}

// ChainEventLeg is one transaction leg within a chain event.
type ChainEventLeg struct {
	Action     Action
	Instrument Instrument
	Quantity   decimal.Decimal // lot size (always positive)
}
