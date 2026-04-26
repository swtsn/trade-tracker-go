// Package domain defines the core business domain types for the trade tracker.
// These types represent accounts, instruments, trades, transactions, positions,
// chains, and related concepts used throughout the application.
package domain

import (
	"fmt"
	"time"
)

// Account represents a trading account at a broker.
type Account struct {
	ID            string
	Broker        string
	AccountNumber string
	Name          string
	CreatedAt     time.Time
}

func (a Account) String() string {
	if a.Name != "" {
		return a.Name
	}
	num := a.AccountNumber
	if len(num) > 4 {
		num = num[len(num)-4:]
	}
	return fmt.Sprintf("%s-%s", a.Broker, num)
}
