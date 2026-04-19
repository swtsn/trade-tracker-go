package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/repository"
	"trade-tracker-go/internal/repository/sqlite/model"
)

// instrumentRepo implements the InstrumentRepository interface.
type instrumentRepo struct {
	db *sql.DB
}

// NewInstrumentRepository creates a new instrumentRepo backed by the given database.
func NewInstrumentRepository(db *sql.DB) repository.InstrumentRepository {
	return &instrumentRepo{db: db}
}

// Upsert inserts the instrument or ignores if already present (deterministic ID).
func (r *instrumentRepo) Upsert(ctx context.Context, instrument *domain.Instrument) error {
	s := model.InstrumentToStorage(*instrument)
	_, err := r.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO instruments
			(id, symbol, asset_class, expiration, strike, option_type, multiplier, osi_symbol, futures_expiry_month, exchange_code)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Symbol, s.AssetClass,
		s.Expiration, s.Strike, s.OptionType,
		s.Multiplier, s.OSISymbol, s.FuturesExpiryMonth, s.ExchangeCode,
	)
	if err != nil {
		return fmt.Errorf("upsert instrument: %w", err)
	}
	return nil
}

// GetByID retrieves an instrument by its deterministic ID.
// Returns domain.ErrNotFound if the instrument does not exist.
func (r *instrumentRepo) GetByID(ctx context.Context, id string) (*domain.Instrument, error) {
	var s model.Instrument
	row := r.db.QueryRowContext(ctx,
		`SELECT id, symbol, asset_class, expiration, strike, option_type, multiplier, osi_symbol, futures_expiry_month, exchange_code
		 FROM instruments WHERE id = ?`, id)
	if err := row.Scan(s.ScanDest()...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get instrument: %w", err)
	}
	inst, err := s.ToDomain()
	if err != nil {
		return nil, err
	}
	return &inst, nil
}
