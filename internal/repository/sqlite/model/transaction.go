package model

import (
	"database/sql"
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
	Broker         string
	AccountID      string
	InstrumentID   string
	Action         string
	Quantity       string
	FillPrice      string
	Fees           string
	ExecutedAt     string
	PositionEffect string
	ChainID        sql.NullString
	CreatedAt      string
}

// FullTransaction is a joined row used when scanning transactions + instruments.
type FullTransaction struct {
	Transaction
	Inst Instrument
}

// ScanDest returns scan destinations matching the SELECT column order:
// all transaction columns, then all instrument columns.
func (r *FullTransaction) ScanDest() []any {
	return append(
		[]any{
			&r.ID, &r.TradeID, &r.BrokerTxID, &r.Broker, &r.AccountID,
			&r.InstrumentID, &r.Action, &r.Quantity, &r.FillPrice,
			&r.Fees, &r.ExecutedAt, &r.PositionEffect, &r.ChainID, &r.CreatedAt,
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
	if r.ChainID.Valid {
		chainID := r.ChainID.String
		tx.ChainID = &chainID
	}
	return tx, nil
}

// TransactionToStorage converts a domain.Transaction to its flat storage struct.
func TransactionToStorage(tx domain.Transaction, now time.Time) Transaction {
	s := Transaction{
		ID:             tx.ID,
		TradeID:        tx.TradeID,
		BrokerTxID:     tx.BrokerTxID,
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
	if tx.ChainID != nil {
		s.ChainID = sql.NullString{String: *tx.ChainID, Valid: true}
	}
	return s
}
