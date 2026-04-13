package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"trade-tracker-go/internal/domain"
)

// contractSpecRepo implements the ContractSpecRepository interface.
type contractSpecRepo struct {
	db *sql.DB
}

// NewContractSpecRepository creates a new contractSpecRepo backed by the given database.
func NewContractSpecRepository(db *sql.DB) *contractSpecRepo {
	return &contractSpecRepo{db: db}
}

// Get returns the spec string for a futures root symbol.
// Returns domain.ErrNotFound if the root symbol has no registered spec.
func (r *contractSpecRepo) Get(ctx context.Context, rootSymbol string) (string, error) {
	var spec string
	err := r.db.QueryRowContext(ctx,
		`SELECT spec FROM contract_specs WHERE root_symbol = ?`, rootSymbol,
	).Scan(&spec)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: contract spec for %s", domain.ErrNotFound, rootSymbol)
	}
	if err != nil {
		return "", fmt.Errorf("get contract spec: %w", err)
	}
	return spec, nil
}
