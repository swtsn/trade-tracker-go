// Package model defines storage representations (flat, SQL-scannable structs)
// that bridge between domain types and database tables.
package model

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"trade-tracker-go/internal/domain"
)

// Instrument holds the flat, SQL-scannable fields for an instruments row.
// Embedded (by composition) in other storage models that JOIN with instruments.
type Instrument struct {
	ID                 string
	Symbol             string
	AssetClass         string
	Expiration         sql.NullString
	Strike             sql.NullString
	OptionType         sql.NullString
	Multiplier         string
	OSISymbol          sql.NullString
	FuturesExpiryMonth sql.NullString
	ExchangeCode       sql.NullString
}

// ScanDest returns the pointers to scan destinations in SELECT column order:
// id, symbol, asset_class, expiration, strike, option_type,
// multiplier, osi_symbol, futures_expiry_month, exchange_code.
func (i *Instrument) ScanDest() []any {
	return []any{
		&i.ID, &i.Symbol, &i.AssetClass,
		&i.Expiration, &i.Strike, &i.OptionType,
		&i.Multiplier, &i.OSISymbol, &i.FuturesExpiryMonth, &i.ExchangeCode,
	}
}

// ToDomain converts to a domain.Instrument.
func (i Instrument) ToDomain() (domain.Instrument, error) {
	multiplier, err := decimal.NewFromString(i.Multiplier)
	if err != nil {
		return domain.Instrument{}, fmt.Errorf("instrument multiplier: %w", err)
	}

	inst := domain.Instrument{
		Symbol:     i.Symbol,
		AssetClass: domain.AssetClass(i.AssetClass),
	}

	switch inst.AssetClass {
	case domain.AssetClassEquityOption, domain.AssetClassFutureOption:
		if !i.Expiration.Valid || !i.Strike.Valid || !i.OptionType.Valid {
			return domain.Instrument{}, fmt.Errorf("%w: option missing required fields", domain.ErrInvalidInstrument)
		}
		exp, err := time.Parse(time.RFC3339, i.Expiration.String)
		if err != nil {
			return domain.Instrument{}, fmt.Errorf("instrument expiration: %w", err)
		}
		strike, err := decimal.NewFromString(i.Strike.String)
		if err != nil {
			return domain.Instrument{}, fmt.Errorf("instrument strike: %w", err)
		}
		inst.Option = &domain.OptionDetails{
			Expiration: exp,
			Strike:     strike,
			OptionType: domain.OptionType(i.OptionType.String),
			Multiplier: multiplier,
			OSI:        i.OSISymbol.String,
		}

	case domain.AssetClassFuture:
		var expiryMonth time.Time
		if i.FuturesExpiryMonth.Valid {
			expiryMonth, err = time.Parse(time.RFC3339, i.FuturesExpiryMonth.String)
			if err != nil {
				return domain.Instrument{}, fmt.Errorf("instrument futures_expiry_month: %w", err)
			}
		}
		inst.Future = &domain.FutureDetails{
			ExpiryMonth:  expiryMonth,
			ExchangeCode: i.ExchangeCode.String,
		}
	}

	return inst, nil
}

// InstrumentToStorage converts a domain.Instrument to its flat storage representation,
// computing the deterministic instrument ID.
func InstrumentToStorage(inst domain.Instrument) Instrument {
	s := Instrument{
		ID:         inst.InstrumentID(),
		Symbol:     inst.Symbol,
		AssetClass: string(inst.AssetClass),
		Multiplier: "1",
	}

	if inst.Option != nil {
		s.Expiration = sql.NullString{String: inst.Option.Expiration.UTC().Format(time.RFC3339), Valid: true}
		s.Strike = sql.NullString{String: inst.Option.Strike.String(), Valid: true}
		s.OptionType = sql.NullString{String: string(inst.Option.OptionType), Valid: true}
		s.Multiplier = inst.Option.Multiplier.String()
		if inst.Option.OSI != "" {
			s.OSISymbol = sql.NullString{String: inst.Option.OSI, Valid: true}
		}
	}

	if inst.Future != nil {
		// Stored as RFC3339 for full precision, but InstrumentID() hashes only the
		// "2006-01" month portion. This is intentional: two timestamps in the same
		// month produce the same instrument ID, which is the correct identity for
		// futures contracts regardless of the precision of the source data.
		s.FuturesExpiryMonth = sql.NullString{
			String: inst.Future.ExpiryMonth.UTC().Format(time.RFC3339),
			Valid:  !inst.Future.ExpiryMonth.IsZero(),
		}
		if inst.Future.ExchangeCode != "" {
			s.ExchangeCode = sql.NullString{String: inst.Future.ExchangeCode, Valid: true}
		}
	}

	return s
}
