// Package domain defines the core business domain types for the trade tracker.
// These types represent accounts, instruments, trades, transactions, positions,
// chains, and related concepts used throughout the application.
package domain

import "time"

// Account represents a trading account at a broker.
type Account struct {
	ID            string
	Broker        string
	AccountNumber string
	Name          string
	CreatedAt     time.Time
}
