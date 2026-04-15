package domain

import "errors"

var (
	ErrNotFound          = errors.New("not found")
	ErrDuplicate         = errors.New("duplicate")
	ErrInvalidInstrument = errors.New("invalid instrument")
	// ErrInvalidAction and ErrInvalidStrategy are reserved for domain-level
	// validation in constructors; no call sites exist yet.
	ErrInvalidAction   = errors.New("invalid action")
	ErrInvalidStrategy = errors.New("invalid strategy")
	// ErrUnattributableTrade is returned by chain detection when a closing or
	// mixed trade cannot be matched to any open chain (e.g. out-of-order import
	// where the open trade has not yet been processed). Callers may skip the
	// trade and continue rather than aborting the detection run.
	ErrUnattributableTrade = errors.New("unattributable trade")
)
