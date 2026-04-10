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

type chainRepo struct {
	db *sql.DB
}

// NewChainRepository returns a ChainRepository backed by the given SQLite database.
func NewChainRepository(db *sql.DB) *chainRepo {
	return &chainRepo{db: db}
}

// CreateChain inserts a new chain row. Returns ErrDuplicate if the ID already exists.
func (r *chainRepo) CreateChain(ctx context.Context, chain *domain.Chain) error {
	s := model.ChainToStorage(*chain)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO chains (id, account_id, underlying_symbol, original_trade_id, created_at, closed_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		s.ID, s.AccountID, s.UnderlyingSymbol, s.OriginalTradeID, s.CreatedAt, s.ClosedAt,
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return fmt.Errorf("%w: chain %s", domain.ErrDuplicate, chain.ID)
		}
		return fmt.Errorf("create chain: %w", err)
	}
	return nil
}

// GetChainByID returns the chain with its Links populated, or ErrNotFound.
func (r *chainRepo) GetChainByID(ctx context.Context, id string) (*domain.Chain, error) {
	var s model.Chain
	err := r.db.QueryRowContext(ctx,
		`SELECT id, account_id, underlying_symbol, original_trade_id, created_at, closed_at
		 FROM chains WHERE id = ?`, id,
	).Scan(&s.ID, &s.AccountID, &s.UnderlyingSymbol, &s.OriginalTradeID, &s.CreatedAt, &s.ClosedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get chain: %w", err)
	}
	chain, err := s.ToDomain()
	if err != nil {
		return nil, err
	}

	links, err := r.ListChainLinks(ctx, id)
	if err != nil {
		return nil, err
	}
	chain.Links = links

	return &chain, nil
}

// ListChainsByAccount returns chains for an account ordered by created_at DESC.
// When openOnly is true, only chains with no closed_at are returned.
func (r *chainRepo) ListChainsByAccount(ctx context.Context, accountID string, openOnly bool) ([]domain.Chain, error) {
	query := `SELECT id, account_id, underlying_symbol, original_trade_id, created_at, closed_at
	          FROM chains WHERE account_id = ?`
	if openOnly {
		query += ` AND closed_at IS NULL`
	}
	query += ` ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, query, accountID)
	if err != nil {
		return nil, fmt.Errorf("list chains: %w", err)
	}
	defer rows.Close()

	var chains []domain.Chain
	for rows.Next() {
		var s model.Chain
		if err := rows.Scan(&s.ID, &s.AccountID, &s.UnderlyingSymbol, &s.OriginalTradeID, &s.CreatedAt, &s.ClosedAt); err != nil {
			return nil, fmt.Errorf("scan chain: %w", err)
		}
		chain, err := s.ToDomain()
		if err != nil {
			return nil, err
		}
		chains = append(chains, chain)
	}
	return chains, rows.Err()
}

// UpdateChainClosed records the close timestamp for the given chain.
func (r *chainRepo) UpdateChainClosed(ctx context.Context, id string, closedAt time.Time) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE chains SET closed_at = ? WHERE id = ?`,
		closedAt.UTC().Format(time.RFC3339), id,
	)
	if err != nil {
		return fmt.Errorf("update chain closed: %w", err)
	}
	return requireOneRow(res, "chain", id)
}

// CreateChainLink inserts a new chain_links row. Returns ErrDuplicate on (chain_id, sequence) conflict.
func (r *chainRepo) CreateChainLink(ctx context.Context, link *domain.ChainLink) error {
	s := model.ChainLinkToStorage(*link)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO chain_links
			(id, chain_id, sequence, link_type, closing_trade_id, opening_trade_id,
			 linked_at, strike_change, expiration_change, credit_debit)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.ChainID, s.Sequence, s.LinkType, s.ClosingTradeID, s.OpeningTradeID,
		s.LinkedAt, s.StrikeChange, s.ExpirationChange, s.CreditDebit,
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return fmt.Errorf("%w: chain_link (chain=%s seq=%d)", domain.ErrDuplicate, link.ChainID, link.Sequence)
		}
		return fmt.Errorf("create chain link: %w", err)
	}
	return nil
}

// ListChainLinks returns all links for a chain ordered by sequence.
func (r *chainRepo) ListChainLinks(ctx context.Context, chainID string) ([]domain.ChainLink, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, chain_id, sequence, link_type, closing_trade_id, opening_trade_id,
		        linked_at, strike_change, expiration_change, credit_debit
		 FROM chain_links WHERE chain_id = ? ORDER BY sequence`,
		chainID,
	)
	if err != nil {
		return nil, fmt.Errorf("list chain links: %w", err)
	}
	defer rows.Close()

	var links []domain.ChainLink
	for rows.Next() {
		var s model.ChainLink
		if err := rows.Scan(
			&s.ID, &s.ChainID, &s.Sequence, &s.LinkType, &s.ClosingTradeID, &s.OpeningTradeID,
			&s.LinkedAt, &s.StrikeChange, &s.ExpirationChange, &s.CreditDebit,
		); err != nil {
			return nil, fmt.Errorf("scan chain link: %w", err)
		}
		link, err := s.ToDomain()
		if err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, rows.Err()
}
