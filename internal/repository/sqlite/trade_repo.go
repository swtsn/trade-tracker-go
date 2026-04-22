package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/repository"
	"trade-tracker-go/internal/repository/sqlite/model"
)

// tradeRepo implements the TradeRepository interface.
type tradeRepo struct {
	db   *sql.DB
	txns repository.TransactionRepository
}

// NewTradeRepository creates a new tradeRepo backed by the given database and transaction repo.
func NewTradeRepository(db *sql.DB, txns repository.TransactionRepository) repository.TradeRepository {
	return &tradeRepo{db: db, txns: txns}
}

// Create inserts a new trade into the database.
// Returns domain.ErrDuplicate if a trade with the same ID already exists.
func (r *tradeRepo) Create(ctx context.Context, trade *domain.Trade) error {
	s := model.TradeToStorage(*trade, time.Now())
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO trades (id, account_id, broker, strategy_type, underlying_symbol, executed_at, notes, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.AccountID, s.Broker, s.StrategyType, s.UnderlyingSymbol, s.ExecutedAt, s.Notes, s.CreatedAt,
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return fmt.Errorf("%w: trade %s", domain.ErrDuplicate, trade.ID)
		}
		return fmt.Errorf("create trade: %w", err)
	}
	return nil
}

// GetByID retrieves a trade by its ID with all associated transactions loaded.
// Returns domain.ErrNotFound if the trade does not exist.
func (r *tradeRepo) GetByID(ctx context.Context, id string) (*domain.Trade, error) {
	var s model.Trade
	err := r.db.QueryRowContext(ctx,
		`SELECT id, account_id, broker, strategy_type, underlying_symbol, executed_at, notes, created_at
		 FROM trades WHERE id = ?`, id,
	).Scan(&s.ID, &s.AccountID, &s.Broker, &s.StrategyType, &s.UnderlyingSymbol, &s.ExecutedAt, &s.Notes, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get trade: %w", err)
	}
	trade, err := s.ToDomain()
	if err != nil {
		return nil, err
	}

	// Load transactions for this trade.
	txs, err := loadTransactionsForTrade(ctx, r.db, id)
	if err != nil {
		return nil, err
	}
	trade.Transactions = txs

	return &trade, nil
}

// GetByIDAndAccount retrieves a trade by ID only if it belongs to the given account.
// Returns domain.ErrNotFound when the trade does not exist or belongs to a different account.
func (r *tradeRepo) GetByIDAndAccount(ctx context.Context, accountID, id string) (*domain.Trade, error) {
	var s model.Trade
	err := r.db.QueryRowContext(ctx,
		`SELECT id, account_id, broker, strategy_type, underlying_symbol, executed_at, notes, created_at
		 FROM trades WHERE id = ? AND account_id = ?`, id, accountID,
	).Scan(&s.ID, &s.AccountID, &s.Broker, &s.StrategyType, &s.UnderlyingSymbol, &s.ExecutedAt, &s.Notes, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get trade by account: %w", err)
	}
	trade, err := s.ToDomain()
	if err != nil {
		return nil, err
	}
	txs, err := loadTransactionsForTrade(ctx, r.db, id)
	if err != nil {
		return nil, err
	}
	trade.Transactions = txs
	return &trade, nil
}

// ListByAccount retrieves trades for an account with optional filtering and pagination.
// Returns both the matching trades and the total count of trades satisfying the filters.
// Transactions are not loaded; use GetByID for full trade detail.
func (r *tradeRepo) ListByAccount(ctx context.Context, accountID string, opts repository.ListTradesOptions) ([]domain.Trade, int, error) {
	// Build WHERE clause.
	where := `WHERE account_id = ?`
	args := []any{accountID}
	if opts.Symbol != "" {
		where += ` AND underlying_symbol = ?`
		args = append(args, opts.Symbol)
	}
	if opts.StrategyType != "" {
		where += ` AND strategy_type = ?`
		args = append(args, string(opts.StrategyType))
	}
	if !opts.ExecutedAfter.IsZero() {
		where += ` AND executed_at >= ?`
		args = append(args, opts.ExecutedAfter.UTC().Format(time.RFC3339))
	}
	if !opts.ExecutedBefore.IsZero() {
		where += ` AND executed_at <= ?`
		args = append(args, opts.ExecutedBefore.UTC().Format(time.RFC3339))
	}

	// Total count.
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM trades `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count trades: %w", err)
	}

	// Paginated query.
	query := `SELECT id, account_id, broker, strategy_type, underlying_symbol, executed_at, notes, created_at
	          FROM trades ` + where + ` ORDER BY executed_at DESC, id DESC`
	if opts.Limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, opts.Limit, opts.Offset)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list trades: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var trades []domain.Trade
	for rows.Next() {
		var s model.Trade
		if err := rows.Scan(&s.ID, &s.AccountID, &s.Broker, &s.StrategyType, &s.UnderlyingSymbol, &s.ExecutedAt, &s.Notes, &s.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan trade: %w", err)
		}
		trade, err := s.ToDomain()
		if err != nil {
			return nil, 0, err
		}
		trades = append(trades, trade)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return trades, total, nil
}

// ListByAccountWithTransactions is like ListByAccount but populates each trade's
// Transactions slice using a single batch query (no N+1).
func (r *tradeRepo) ListByAccountWithTransactions(ctx context.Context, accountID string, opts repository.ListTradesOptions) ([]domain.Trade, int, error) {
	trades, total, err := r.ListByAccount(ctx, accountID, opts)
	if err != nil || len(trades) == 0 {
		return trades, total, err
	}

	ids := make([]string, len(trades))
	for i, t := range trades {
		ids[i] = t.ID
	}

	txsByTrade, err := r.txns.ListByTradeIDs(ctx, ids)
	if err != nil {
		return nil, 0, err
	}

	for i := range trades {
		trades[i].Transactions = txsByTrade[trades[i].ID]
	}
	return trades, total, nil
}

// UpdateStrategy updates the strategy type of a trade.
// Returns domain.ErrNotFound if the trade does not exist.
func (r *tradeRepo) UpdateStrategy(ctx context.Context, id string, strategy domain.StrategyType) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE trades SET strategy_type = ? WHERE id = ?`, string(strategy), id)
	if err != nil {
		return fmt.Errorf("update trade strategy: %w", err)
	}
	return requireOneRow(res, "trade", id)
}

// loadTransactionsForTrade fetches all transactions with their instruments for a given trade,
// ordered by execution time.
func loadTransactionsForTrade(ctx context.Context, db *sql.DB, tradeID string) ([]domain.Transaction, error) {
	rows, err := db.QueryContext(ctx,
		transactionJoinSelect+` WHERE t.trade_id = ? ORDER BY t.executed_at`, tradeID)
	if err != nil {
		return nil, fmt.Errorf("load transactions for trade: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanTransactionRows(rows)
}

// requireOneRow checks that an UPDATE or DELETE result affected exactly one row.
// Returns domain.ErrNotFound if no rows were affected.
func requireOneRow(res sql.Result, entity, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s %s", domain.ErrNotFound, entity, id)
	}
	return nil
}
