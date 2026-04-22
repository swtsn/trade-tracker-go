package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/repository"
	"trade-tracker-go/internal/repository/sqlite/model"
)

// chainRepo implements the ChainRepository interface.
type chainRepo struct {
	db *sql.DB
}

// NewChainRepository creates a new chainRepo backed by the given database.
func NewChainRepository(db *sql.DB) repository.ChainRepository {
	return &chainRepo{db: db}
}

// CreateChain inserts a new chain into the database.
// Returns domain.ErrDuplicate if a chain with the same ID already exists.
func (r *chainRepo) CreateChain(ctx context.Context, chain *domain.Chain) error {
	s := model.ChainToStorage(*chain)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO chains (id, account_id, underlying_symbol, original_trade_id, created_at, closed_at, attribution_gap)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.AccountID, s.UnderlyingSymbol, s.OriginalTradeID, s.CreatedAt, s.ClosedAt, s.AttributionGap,
	)
	if err != nil {
		if isUniqueConstraint(err) {
			return fmt.Errorf("%w: chain %s", domain.ErrDuplicate, chain.ID)
		}
		return fmt.Errorf("create chain: %w", err)
	}
	return nil
}

// GetChainByID retrieves a chain by its ID with all associated links loaded.
// Returns domain.ErrNotFound if the chain does not exist.
func (r *chainRepo) GetChainByID(ctx context.Context, id string) (*domain.Chain, error) {
	var s model.Chain
	err := r.db.QueryRowContext(ctx,
		`SELECT id, account_id, underlying_symbol, original_trade_id, created_at, closed_at, attribution_gap
		 FROM chains WHERE id = ?`, id,
	).Scan(&s.ID, &s.AccountID, &s.UnderlyingSymbol, &s.OriginalTradeID, &s.CreatedAt, &s.ClosedAt, &s.AttributionGap)
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

// GetChainByTradeID returns the chain that owns the given trade, checking
// chains.original_trade_id, chain_links.closing_trade_id, and
// chain_links.opening_trade_id. Returns domain.ErrNotFound when no chain
// owns the trade.
func (r *chainRepo) GetChainByTradeID(ctx context.Context, tradeID string) (*domain.Chain, error) {
	var s model.Chain
	err := r.db.QueryRowContext(ctx,
		// UNION (not UNION ALL) deduplicates: a roll stores trade.ID in both
		// closing_trade_id and opening_trade_id, so the same chain row must not
		// appear twice.
		// args: tradeID (original_trade_id), tradeID (closing_trade_id), tradeID (opening_trade_id)
		`SELECT id, account_id, underlying_symbol, original_trade_id, created_at, closed_at, attribution_gap
		 FROM chains WHERE original_trade_id = ?
		 UNION
		 SELECT c.id, c.account_id, c.underlying_symbol, c.original_trade_id, c.created_at, c.closed_at, c.attribution_gap
		 FROM chains c
		 JOIN chain_links cl ON cl.chain_id = c.id
		 WHERE cl.closing_trade_id = ?
		 UNION
		 SELECT c.id, c.account_id, c.underlying_symbol, c.original_trade_id, c.created_at, c.closed_at, c.attribution_gap
		 FROM chains c
		 JOIN chain_links cl ON cl.chain_id = c.id
		 WHERE cl.opening_trade_id = ?
		 LIMIT 1`,
		tradeID, tradeID, tradeID,
	).Scan(&s.ID, &s.AccountID, &s.UnderlyingSymbol, &s.OriginalTradeID, &s.CreatedAt, &s.ClosedAt, &s.AttributionGap)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get chain by trade id: %w", err)
	}
	chain, err := s.ToDomain()
	if err != nil {
		return nil, err
	}
	return &chain, nil
}

// ListChainsByAccount retrieves chains for an account with optional filtering for open chains.
// Links are not loaded; use GetChainByID for full chain detail.
func (r *chainRepo) ListChainsByAccount(ctx context.Context, accountID string, openOnly bool) ([]domain.Chain, error) {
	query := `SELECT id, account_id, underlying_symbol, original_trade_id, created_at, closed_at, attribution_gap
	          FROM chains WHERE account_id = ?`
	if openOnly {
		query += ` AND closed_at IS NULL`
	}
	query += ` ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, query, accountID)
	if err != nil {
		return nil, fmt.Errorf("list chains: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var chains []domain.Chain
	for rows.Next() {
		var s model.Chain
		if err := rows.Scan(&s.ID, &s.AccountID, &s.UnderlyingSymbol, &s.OriginalTradeID, &s.CreatedAt, &s.ClosedAt, &s.AttributionGap); err != nil {
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

// UpdateChainClosed sets closed_at on an open chain.
// Returns domain.ErrNotFound if the chain does not exist.
// No-ops if the chain is already closed (idempotent).
func (r *chainRepo) UpdateChainClosed(ctx context.Context, id string, closedAt time.Time) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE chains SET closed_at = ? WHERE id = ? AND closed_at IS NULL`,
		closedAt.UTC().Format(time.RFC3339), id,
	)
	if err != nil {
		return fmt.Errorf("update chain closed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update chain closed rows: %w", err)
	}
	if n > 0 {
		return nil
	}
	// 0 rows: chain may not exist or is already closed.
	var count int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chains WHERE id = ?`, id).Scan(&count); err != nil {
		return fmt.Errorf("check chain existence: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("%w: chain %s", domain.ErrNotFound, id)
	}
	return nil // already closed — idempotent
}

// GetOpenChainForInstrument returns the open chain in the account that has a net positive
// opening balance for the given instrument, derived from transaction arithmetic across all
// trades linked to the chain. Returns domain.ErrNotFound when no such chain exists.
func (r *chainRepo) GetOpenChainForInstrument(ctx context.Context, accountID, instrumentID string) (*domain.Chain, error) {
	var s model.Chain
	err := r.db.QueryRowContext(ctx,
		// UNION (not UNION ALL) deduplicates trade IDs: a roll stores trade.ID in
		// both opening_trade_id and closing_trade_id so the inner set must not
		// double-count that trade's transactions in the SUM.
		// ORDER BY created_at ASC: when multiple open chains hold the same
		// instrument, attribute to the oldest (first-opened) chain.
		// args: accountID, instrumentID
		`SELECT c.id, c.account_id, c.underlying_symbol, c.original_trade_id, c.created_at, c.closed_at, c.attribution_gap
		 FROM chains c
		 WHERE c.account_id = ?
		   AND c.closed_at IS NULL
		   AND (
		     SELECT SUM(CASE
		         WHEN t.position_effect = 'opening' THEN CAST(t.quantity AS REAL)
		         WHEN t.position_effect = 'closing' THEN -CAST(t.quantity AS REAL)
		         ELSE 0
		     END)
		     FROM transactions t
		     WHERE t.instrument_id = ?
		       AND t.trade_id IN (
		           SELECT c2.original_trade_id FROM chains c2 WHERE c2.id = c.id
		           UNION
		           SELECT cl.opening_trade_id FROM chain_links cl WHERE cl.chain_id = c.id
		           UNION
		           SELECT cl.closing_trade_id FROM chain_links cl WHERE cl.chain_id = c.id
		       )
		   ) > 0
		 ORDER BY c.created_at ASC
		 LIMIT 1`,
		accountID, instrumentID,
	).Scan(&s.ID, &s.AccountID, &s.UnderlyingSymbol, &s.OriginalTradeID, &s.CreatedAt, &s.ClosedAt, &s.AttributionGap)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get open chain for instrument: %w", err)
	}
	chain, err := s.ToDomain()
	if err != nil {
		return nil, err
	}
	return &chain, nil
}

// ChainIsOpen reports whether any instrument in the chain has a net positive
// opening quantity across all trades linked to the chain (transaction arithmetic).
func (r *chainRepo) ChainIsOpen(ctx context.Context, chainID string) (bool, error) {
	var exists int
	err := r.db.QueryRowContext(ctx,
		// UNION (not UNION ALL) deduplicates trade IDs: a roll stores chainID in
		// both opening_trade_id and closing_trade_id, so the set must not include
		// that trade's transactions twice.
		// args: chainID (original_trade_id), chainID (opening_trade_id), chainID (closing_trade_id)
		`SELECT EXISTS (
		     SELECT 1
		     FROM (
		         SELECT t.instrument_id,
		                SUM(CASE
		                    WHEN t.position_effect = 'opening' THEN CAST(t.quantity AS REAL)
		                    WHEN t.position_effect = 'closing' THEN -CAST(t.quantity AS REAL)
		                    ELSE 0
		                END) AS net_qty
		         FROM transactions t
		         WHERE t.trade_id IN (
		             SELECT original_trade_id FROM chains WHERE id = ?
		             UNION
		             SELECT opening_trade_id FROM chain_links WHERE chain_id = ?
		             UNION
		             SELECT closing_trade_id FROM chain_links WHERE chain_id = ?
		         )
		         GROUP BY t.instrument_id
		     ) balances
		     WHERE net_qty > 0
		 )`,
		chainID, chainID, chainID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("chain has open balance: %w", err)
	}
	return exists == 1, nil
}

// CreateChainLink inserts a new link within a chain.
// Returns domain.ErrDuplicate if a link with the same chain_id and sequence already exists.
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

// GetChainPnL returns the net realized P&L for the chain computed from transaction data:
// sum of (fill_price × quantity × multiplier × direction_sign − fees) across all trades.
// direction_sign: STO/STC/SELL = +1 (credit received), BTO/BTC/BUY = -1 (debit paid),
// ASSIGNMENT/EXPIRATION/EXERCISE = 0.
func (r *chainRepo) GetChainPnL(ctx context.Context, chainID string) (decimal.Decimal, error) {
	rows, err := r.db.QueryContext(ctx,
		// UNION (not UNION ALL) deduplicates trade IDs: a roll stores chainID in
		// both opening_trade_id and closing_trade_id, so the set must not include
		// that trade's transactions twice (which would double-count the P&L).
		// args: chainID (original_trade_id), chainID (opening_trade_id), chainID (closing_trade_id)
		`SELECT t.action, t.quantity, t.fill_price, t.fees, i.multiplier
		 FROM transactions t
		 JOIN instruments i ON t.instrument_id = i.id
		 WHERE t.trade_id IN (
		     SELECT original_trade_id FROM chains WHERE id = ?
		     UNION
		     SELECT opening_trade_id FROM chain_links WHERE chain_id = ?
		     UNION
		     SELECT closing_trade_id FROM chain_links WHERE chain_id = ?
		 )`,
		chainID, chainID, chainID,
	)
	if err != nil {
		return decimal.Zero, fmt.Errorf("get chain pnl: %w", err)
	}
	defer func() { _ = rows.Close() }()

	total := decimal.Zero
	for rows.Next() {
		var action string
		var qty, price, fees, mult decimal.Decimal
		if err := rows.Scan(&action, &qty, &price, &fees, &mult); err != nil {
			return decimal.Zero, fmt.Errorf("scan chain pnl row: %w", err)
		}
		sign := domain.CashFlowSign(domain.Action(action))
		total = total.Add(sign.Mul(price).Mul(qty).Mul(mult)).Sub(fees)
	}
	return total, rows.Err()
}

// ListChainLinks retrieves all links for a chain, ordered by sequence.
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
	defer func() { _ = rows.Close() }()

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
