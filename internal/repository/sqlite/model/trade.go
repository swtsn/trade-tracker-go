package model

import (
	"fmt"
	"time"

	"trade-tracker-go/internal/domain"
)

// Trade holds the flat, SQL-scannable fields for a trades row.
type Trade struct {
	ID               string
	AccountID        string
	Broker           string
	StrategyType     string
	UnderlyingSymbol string
	ExecutedAt       string
	Notes            string
	CreatedAt        string
}

// ToDomain converts to a domain.Trade (without Transactions — caller loads those).
func (s Trade) ToDomain() (domain.Trade, error) {
	executedAt, err := time.Parse(time.RFC3339, s.ExecutedAt)
	if err != nil {
		return domain.Trade{}, fmt.Errorf("trade executed_at: %w", err)
	}

	return domain.Trade{
		ID:               s.ID,
		AccountID:        s.AccountID,
		Broker:           s.Broker,
		StrategyType:     domain.StrategyType(s.StrategyType),
		UnderlyingSymbol: s.UnderlyingSymbol,
		ExecutedAt:       executedAt,
		Notes:            s.Notes,
	}, nil
}

// TradeToStorage converts a domain.Trade to its flat storage struct,
// recording the current time as created_at.
func TradeToStorage(trade domain.Trade, now time.Time) Trade {
	return Trade{
		ID:               trade.ID,
		AccountID:        trade.AccountID,
		Broker:           trade.Broker,
		StrategyType:     string(trade.StrategyType),
		UnderlyingSymbol: trade.UnderlyingSymbol,
		ExecutedAt:       trade.ExecutedAt.UTC().Format(time.RFC3339),
		Notes:            trade.Notes,
		CreatedAt:        now.UTC().Format(time.RFC3339),
	}
}
