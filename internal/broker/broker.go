// Package broker defines the shared interface for all broker CSV parsers.
package broker

import (
	"io"

	"trade-tracker-go/internal/domain"
)

// Parser parses a broker transaction history export into normalized domain transactions.
// accountID is the internal account identifier to stamp on each transaction.
type Parser interface {
	Parse(r io.Reader, accountID string) ([]domain.Transaction, error)
}
