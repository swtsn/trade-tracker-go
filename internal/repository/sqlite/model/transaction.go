package model

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"trade-tracker-go/internal/domain"
)

// Transaction is the flat storage struct for an INSERT into transactions.
type Transaction struct {
	ID             string
	TradeID        string
	BrokerTxID     string
	BrokerOrderID  string
	Broker         string
	AccountID      string
	InstrumentID   string
	Action         string
	Quantity       string
	FillPrice      string
	Fees           string
	ExecutedAt     string
	PositionEffect string
	CreatedAt      string
}

// FullTransaction is a joined row used when scanning transactions + instruments.
type FullTransaction struct {
	Transaction
	Inst Instrument
}

// ScanDest returns the pointers to scan destinations matching the SELECT column order:
// all transaction columns, then all instrument columns.
func (r *FullTransaction) ScanDest() []any {
	return append(
		[]any{
			&r.ID, &r.TradeID, &r.BrokerTxID, &r.BrokerOrderID, &r.Broker, &r.AccountID,
			&r.InstrumentID, &r.Action, &r.Quantity, &r.FillPrice,
			&r.Fees, &r.ExecutedAt, &r.PositionEffect, &r.CreatedAt,
		},
		r.Inst.ScanDest()...,
	)
}

// ToDomain converts the joined row to a domain.Transaction.
func (r FullTransaction) ToDomain() (domain.Transaction, error) {
	inst, err := r.Inst.ToDomain()
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("transaction instrument: %w", err)
	}
	qty, err := decimal.NewFromString(r.Quantity)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("transaction quantity: %w", err)
	}
	fillPrice, err := decimal.NewFromString(r.FillPrice)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("transaction fill_price: %w", err)
	}
	fees, err := decimal.NewFromString(r.Fees)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("transaction fees: %w", err)
	}
	executedAt, err := time.Parse(time.RFC3339, r.ExecutedAt)
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("transaction executed_at: %w", err)
	}

	tx := domain.Transaction{
		ID:             r.ID,
		TradeID:        r.TradeID,
		BrokerTxID:     r.BrokerTxID,
		BrokerOrderID:  r.BrokerOrderID,
		Broker:         r.Broker,
		AccountID:      r.AccountID,
		Instrument:     inst,
		Action:         domain.Action(r.Action),
		Quantity:       qty,
		FillPrice:      fillPrice,
		Fees:           fees,
		ExecutedAt:     executedAt,
		PositionEffect: domain.PositionEffect(r.PositionEffect),
	}
	return tx, nil
}

// TransactionToStorage converts a domain.Transaction to its flat storage struct,
// recording the current time as created_at.
func TransactionToStorage(tx domain.Transaction, now time.Time) Transaction {
	return Transaction{
		ID:             tx.ID,
		TradeID:        tx.TradeID,
		BrokerTxID:     tx.BrokerTxID,
		BrokerOrderID:  tx.BrokerOrderID,
		Broker:         tx.Broker,
		AccountID:      tx.AccountID,
		InstrumentID:   tx.Instrument.InstrumentID(),
		Action:         string(tx.Action),
		Quantity:       tx.Quantity.String(),
		FillPrice:      tx.FillPrice.String(),
		Fees:           tx.Fees.String(),
		ExecutedAt:     tx.ExecutedAt.UTC().Format(time.RFC3339),
		PositionEffect: string(tx.PositionEffect),
		CreatedAt:      now.UTC().Format(time.RFC3339),
	}
}
