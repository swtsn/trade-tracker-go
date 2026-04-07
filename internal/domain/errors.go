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
)
