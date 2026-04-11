package model

import (
	"database/sql"
	"fmt"
	"time"

	"trade-tracker-go/internal/domain"
)

// Trade holds the flat, SQL-scannable fields for a trades row.
type Trade struct {
	ID           string
	AccountID    string
	Broker       string
	StrategyType string
	OpenedAt     string
	ClosedAt     sql.NullString
	Notes        string
	CreatedAt    string
}

// ToDomain converts to a domain.Trade (without Transactions — caller loads those).
func (s Trade) ToDomain() (domain.Trade, error) {
	openedAt, err := time.Parse(time.RFC3339, s.OpenedAt)
	if err != nil {
		return domain.Trade{}, fmt.Errorf("trade opened_at: %w", err)
	}

	trade := domain.Trade{
		ID:           s.ID,
		AccountID:    s.AccountID,
		Broker:       s.Broker,
		StrategyType: domain.StrategyType(s.StrategyType),
		OpenedAt:     openedAt,
		Notes:        s.Notes,
	}

	if s.ClosedAt.Valid {
		t, err := time.Parse(time.RFC3339, s.ClosedAt.String)
		if err != nil {
			return domain.Trade{}, fmt.Errorf("trade closed_at: %w", err)
		}
		trade.ClosedAt = &t
	}

	return trade, nil
}

// TradeToStorage converts a domain.Trade to its flat storage struct,
// recording the current time as created_at.
func TradeToStorage(trade domain.Trade, now time.Time) Trade {
	s := Trade{
		ID:           trade.ID,
		AccountID:    trade.AccountID,
		Broker:       trade.Broker,
		StrategyType: string(trade.StrategyType),
		OpenedAt:     trade.OpenedAt.UTC().Format(time.RFC3339),
		Notes:        trade.Notes,
		CreatedAt:    now.UTC().Format(time.RFC3339),
	}
	if trade.ClosedAt != nil {
		s.ClosedAt = sql.NullString{String: trade.ClosedAt.UTC().Format(time.RFC3339), Valid: true}
	}
	return s
}
