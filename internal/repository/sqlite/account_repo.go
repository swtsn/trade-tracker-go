package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"trade-tracker-go/internal/domain"
)

type accountRepo struct {
	db *sql.DB
}

// NewAccountRepository returns an AccountRepository backed by the given SQLite database.
func NewAccountRepository(db *sql.DB) *accountRepo {
	return &accountRepo{db: db}
}

// Create inserts a new account row. Returns ErrDuplicate if the ID already exists.
func (r *accountRepo) Create(ctx context.Context, account *domain.Account) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO accounts (id, broker, account_number, name, created_at) VALUES (?, ?, ?, ?, ?)`,
		account.ID, account.Broker, account.AccountNumber, account.Name,
		account.CreatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return fmt.Errorf("%w: account %s", domain.ErrDuplicate, account.ID)
		}
		return fmt.Errorf("create account: %w", err)
	}
	return nil
}

// GetByID returns the account with the given ID, or ErrNotFound if it doesn't exist.
func (r *accountRepo) GetByID(ctx context.Context, id string) (*domain.Account, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, broker, account_number, name, created_at FROM accounts WHERE id = ?`, id)
	return scanAccount(row)
}

// List returns all accounts ordered by creation time.
func (r *accountRepo) List(ctx context.Context) ([]domain.Account, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, broker, account_number, name, created_at FROM accounts ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()

	var accounts []domain.Account
	for rows.Next() {
		var createdAt string
		var a domain.Account
		if err := rows.Scan(&a.ID, &a.Broker, &a.AccountNumber, &a.Name, &createdAt); err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		var parseErr error
		a.CreatedAt, parseErr = time.Parse(time.RFC3339, createdAt)
		if parseErr != nil {
			return nil, fmt.Errorf("account created_at: %w", parseErr)
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

// scanAccount reads one account from a query row, returning ErrNotFound for sql.ErrNoRows.
func scanAccount(row *sql.Row) (*domain.Account, error) {
	var a domain.Account
	var createdAt string
	err := row.Scan(&a.ID, &a.Broker, &a.AccountNumber, &a.Name, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scan account: %w", err)
	}
	a.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, fmt.Errorf("account created_at: %w", err)
	}
	return &a, nil
}
