package model

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"trade-tracker-go/internal/domain"
)

// Chain holds the flat, SQL-scannable fields for a chains row.
type Chain struct {
	ID               string
	AccountID        string
	UnderlyingSymbol string
	OriginalTradeID  string
	CreatedAt        string
	ClosedAt         sql.NullString
}

// ToDomain converts to a domain.Chain (without Links — caller loads those).
func (s Chain) ToDomain() (domain.Chain, error) {
	createdAt, err := time.Parse(time.RFC3339, s.CreatedAt)
	if err != nil {
		return domain.Chain{}, fmt.Errorf("chain created_at: %w", err)
	}

	chain := domain.Chain{
		ID:               s.ID,
		AccountID:        s.AccountID,
		UnderlyingSymbol: s.UnderlyingSymbol,
		OriginalTradeID:  s.OriginalTradeID,
		CreatedAt:        createdAt,
	}
	if s.ClosedAt.Valid {
		t, err := time.Parse(time.RFC3339, s.ClosedAt.String)
		if err != nil {
			return domain.Chain{}, fmt.Errorf("chain closed_at: %w", err)
		}
		chain.ClosedAt = &t
	}
	return chain, nil
}

// ChainToStorage converts a domain.Chain to its flat storage struct.
func ChainToStorage(chain domain.Chain) Chain {
	s := Chain{
		ID:               chain.ID,
		AccountID:        chain.AccountID,
		UnderlyingSymbol: chain.UnderlyingSymbol,
		OriginalTradeID:  chain.OriginalTradeID,
		CreatedAt:        chain.CreatedAt.UTC().Format(time.RFC3339),
	}
	if chain.ClosedAt != nil {
		s.ClosedAt = sql.NullString{String: chain.ClosedAt.UTC().Format(time.RFC3339), Valid: true}
	}
	return s
}

// ChainLink holds the flat, SQL-scannable fields for a chain_links row.
type ChainLink struct {
	ID               string
	ChainID          string
	Sequence         int64
	LinkType         string
	ClosingTradeID   string
	OpeningTradeID   string
	LinkedAt         string
	StrikeChange     string
	ExpirationChange int64
	CreditDebit      string
}

// ToDomain converts to a domain.ChainLink.
func (s ChainLink) ToDomain() (domain.ChainLink, error) {
	linkedAt, err := time.Parse(time.RFC3339, s.LinkedAt)
	if err != nil {
		return domain.ChainLink{}, fmt.Errorf("chain_link linked_at: %w", err)
	}
	strikeChange, err := decimal.NewFromString(s.StrikeChange)
	if err != nil {
		return domain.ChainLink{}, fmt.Errorf("chain_link strike_change: %w", err)
	}
	creditDebit, err := decimal.NewFromString(s.CreditDebit)
	if err != nil {
		return domain.ChainLink{}, fmt.Errorf("chain_link credit_debit: %w", err)
	}

	return domain.ChainLink{
		ID:               s.ID,
		ChainID:          s.ChainID,
		Sequence:         int(s.Sequence),
		LinkType:         domain.LinkType(s.LinkType),
		ClosingTradeID:   s.ClosingTradeID,
		OpeningTradeID:   s.OpeningTradeID,
		LinkedAt:         linkedAt,
		StrikeChange:     strikeChange,
		ExpirationChange: int(s.ExpirationChange),
		CreditDebit:      creditDebit,
	}, nil
}

// ChainLinkToStorage converts a domain.ChainLink to its flat storage struct.
func ChainLinkToStorage(link domain.ChainLink) ChainLink {
	return ChainLink{
		ID:               link.ID,
		ChainID:          link.ChainID,
		Sequence:         int64(link.Sequence),
		LinkType:         string(link.LinkType),
		ClosingTradeID:   link.ClosingTradeID,
		OpeningTradeID:   link.OpeningTradeID,
		LinkedAt:         link.LinkedAt.UTC().Format(time.RFC3339),
		StrikeChange:     link.StrikeChange.String(),
		ExpirationChange: int64(link.ExpirationChange),
		CreditDebit:      link.CreditDebit.String(),
	}
}
