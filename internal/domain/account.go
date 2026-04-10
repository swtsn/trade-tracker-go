package domain

import "time"

// Account represents a brokerage account that holds trades and positions.
type Account struct {
	ID            string
	Broker        string
	AccountNumber string
	Name          string
	CreatedAt     time.Time
}
