package domain

import "time"

type Account struct {
	ID            string
	Broker        string
	AccountNumber string
	Name          string
	CreatedAt     time.Time
}
