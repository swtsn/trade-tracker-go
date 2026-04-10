package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/repository/sqlite/model"
)

const transactionJoinSelect = `
	SELECT
		t.id, t.trade_id, t.broker_tx_id, t.broker, t.account_id, t.instrument_id,
		t.action, t.quantity, t.fill_price, t.fees, t.executed_at, t.position_effect,
		t.chain_id, t.created_at,
		i.id, i.symbol, i.asset_class, i.expiration, i.strike, i.option_type,
		i.multiplier, i.osi_symbol, i.futures_expiry_month, i.exchange_code
	FROM transactions t
	JOIN instruments i ON t.instrument_id = i.id`

type transactionRepo struct {
	db *sql.DB
}

// NewTransactionRepository returns a TransactionRepository backed by the given SQLite database.
func NewTransactionRepository(db *sql.DB) *transactionRepo {
	return &transactionRepo{db: db}
}

// Create inserts a new transaction row. Returns ErrDuplicate if BrokerTxID already exists.
func (r *transactionRepo) Create(ctx context.Context, tx *domain.Transaction) error {
	s := model.TransactionToStorage(*tx, time.Now())
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO transactions
			(id, trade_id, broker_tx_id, broker, account_id, instrument_id, action,
			 quantity, fill_price, fees, executed_at, position_effect, chain_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.TradeID, s.BrokerTxID, s.Broker, s.AccountID, s.InstrumentID, s.Action,
		s.Quantity, s.FillPrice, s.Fees, s.ExecutedAt, s.PositionEffect, s.ChainID, s.CreatedAt,
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return fmt.Errorf("%w: transaction %s", domain.ErrDuplicate, tx.BrokerTxID)
		}
		return fmt.Errorf("create transaction: %w", err)
	}
	return nil
}

// GetByID returns the transaction with the given ID, or ErrNotFound.
func (r *transactionRepo) GetByID(ctx context.Context, id string) (*domain.Transaction, error) {
	var row model.FullTransaction
	err := r.db.QueryRowContext(ctx,
		transactionJoinSelect+` WHERE t.id = ?`, id,
	).Scan(row.ScanDest()...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get transaction: %w", err)
	}
	tx, err := row.ToDomain()
	if err != nil {
		return nil, err
	}
	return &tx, nil
}

// ListByTrade returns all transactions for a trade, ordered by executed_at.
func (r *transactionRepo) ListByTrade(ctx context.Context, tradeID string) ([]domain.Transaction, error) {
	rows, err := r.db.QueryContext(ctx,
		transactionJoinSelect+` WHERE t.trade_id = ? ORDER BY t.executed_at`, tradeID)
	if err != nil {
		return nil, fmt.Errorf("list transactions by trade: %w", err)
	}
	defer rows.Close()
	return scanTransactionRows(rows)
}

// ListByAccountAndTimeRange returns all transactions for an account within [from, to], ordered by executed_at.
func (r *transactionRepo) ListByAccountAndTimeRange(ctx context.Context, accountID string, from, to time.Time) ([]domain.Transaction, error) {
	rows, err := r.db.QueryContext(ctx,
		transactionJoinSelect+` WHERE t.account_id = ? AND t.executed_at >= ? AND t.executed_at <= ? ORDER BY t.executed_at`,
		accountID, from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("list transactions by time range: %w", err)
	}
	defer rows.Close()
	return scanTransactionRows(rows)
}

// ExistsByBrokerTxID reports whether a transaction with the given broker-assigned ID already exists
// for the specified broker and account, enabling idempotent import.
func (r *transactionRepo) ExistsByBrokerTxID(ctx context.Context, brokerTxID, broker, accountID string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM transactions WHERE broker_tx_id = ? AND broker = ? AND account_id = ?`,
		brokerTxID, broker, accountID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("exists by broker_tx_id: %w", err)
	}
	return count > 0, nil
}

// scanTransactionRows reads all joined transaction+instrument rows from an open *sql.Rows cursor.
func scanTransactionRows(rows *sql.Rows) ([]domain.Transaction, error) {
	var txs []domain.Transaction
	for rows.Next() {
		var row model.FullTransaction
		if err := rows.Scan(row.ScanDest()...); err != nil {
			return nil, fmt.Errorf("scan transaction: %w", err)
		}
		tx, err := row.ToDomain()
		if err != nil {
			return nil, err
		}
		txs = append(txs, tx)
	}
	return txs, rows.Err()
}
