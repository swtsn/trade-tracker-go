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

type tradeRepo struct {
	db *sql.DB
}

func NewTradeRepository(db *sql.DB) *tradeRepo {
	return &tradeRepo{db: db}
}

func (r *tradeRepo) Create(ctx context.Context, trade *domain.Trade) error {
	s := model.TradeToStorage(*trade, time.Now())
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO trades (id, account_id, broker, strategy_type, opened_at, closed_at, notes, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.AccountID, s.Broker, s.StrategyType, s.OpenedAt, s.ClosedAt, s.Notes, s.CreatedAt,
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return fmt.Errorf("%w: trade %s", domain.ErrDuplicate, trade.ID)
		}
		return fmt.Errorf("create trade: %w", err)
	}
	return nil
}

func (r *tradeRepo) GetByID(ctx context.Context, id string) (*domain.Trade, error) {
	var s model.Trade
	err := r.db.QueryRowContext(ctx,
		`SELECT id, account_id, broker, strategy_type, opened_at, closed_at, notes, created_at
		 FROM trades WHERE id = ?`, id,
	).Scan(&s.ID, &s.AccountID, &s.Broker, &s.StrategyType, &s.OpenedAt, &s.ClosedAt, &s.Notes, &s.CreatedAt)
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

func (r *tradeRepo) ListByAccount(ctx context.Context, accountID string, opts repository.ListTradesOptions) ([]domain.Trade, int, error) {
	if opts.OpenOnly && opts.ClosedOnly {
		return nil, 0, fmt.Errorf("ListTradesOptions: OpenOnly and ClosedOnly are mutually exclusive")
	}

	// Build WHERE clause.
	where := `WHERE account_id = ?`
	args := []any{accountID}
	if opts.OpenOnly {
		where += ` AND closed_at IS NULL`
	} else if opts.ClosedOnly {
		where += ` AND closed_at IS NOT NULL`
	}

	// Total count.
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM trades `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count trades: %w", err)
	}

	// Paginated query.
	query := `SELECT id, account_id, broker, strategy_type, opened_at, closed_at, notes, created_at
	          FROM trades ` + where + ` ORDER BY opened_at DESC`
	if opts.Limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, opts.Limit, opts.Offset)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list trades: %w", err)
	}
	defer rows.Close()

	var trades []domain.Trade
	for rows.Next() {
		var s model.Trade
		if err := rows.Scan(&s.ID, &s.AccountID, &s.Broker, &s.StrategyType, &s.OpenedAt, &s.ClosedAt, &s.Notes, &s.CreatedAt); err != nil {
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

func (r *tradeRepo) UpdateStrategy(ctx context.Context, id string, strategy domain.StrategyType) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE trades SET strategy_type = ? WHERE id = ?`, string(strategy), id)
	if err != nil {
		return fmt.Errorf("update trade strategy: %w", err)
	}
	return requireOneRow(res, "trade", id)
}

func (r *tradeRepo) UpdateClosedAt(ctx context.Context, id string, closedAt time.Time) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE trades SET closed_at = ? WHERE id = ?`,
		closedAt.UTC().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("update trade closed_at: %w", err)
	}
	return requireOneRow(res, "trade", id)
}

// loadTransactionsForTrade fetches transactions with their instruments for a given trade.
func loadTransactionsForTrade(ctx context.Context, db *sql.DB, tradeID string) ([]domain.Transaction, error) {
	rows, err := db.QueryContext(ctx,
		transactionJoinSelect+` WHERE t.trade_id = ? ORDER BY t.executed_at`, tradeID)
	if err != nil {
		return nil, fmt.Errorf("load transactions for trade: %w", err)
	}
	defer rows.Close()
	return scanTransactionRows(rows)
}

// requireOneRow returns ErrNotFound if the result affected zero rows.
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
