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

const positionSelect = `
	SELECT id, account_id, chain_id, originating_trade_id, underlying_symbol,
	       strategy_type, cost_basis, realized_pnl, opened_at, updated_at, closed_at
	FROM positions`

const lotJoinSelect = `
	SELECT
		pl.id, pl.account_id, pl.instrument_id, pl.trade_id, pl.opening_tx_id,
		pl.open_quantity, pl.remaining_quantity, pl.open_price, pl.open_fees,
		pl.opened_at, pl.closed_at, pl.chain_id,
		i.id, i.symbol, i.asset_class, i.expiration, i.strike, i.option_type,
		i.multiplier, i.osi_symbol, i.futures_expiry_month, i.exchange_code
	FROM position_lots pl
	JOIN instruments i ON pl.instrument_id = i.id`

// positionRepo implements the PositionRepository interface.
type positionRepo struct {
	db *sql.DB
}

// NewPositionRepository creates a new positionRepo backed by the given database.
func NewPositionRepository(db *sql.DB) repository.PositionRepository {
	return &positionRepo{db: db}
}

// CreatePosition inserts a new position row.
func (r *positionRepo) CreatePosition(ctx context.Context, pos *domain.Position) error {
	s := model.PositionToStorage(*pos)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO positions
			(id, account_id, chain_id, originating_trade_id, underlying_symbol,
			 strategy_type, cost_basis, realized_pnl, opened_at, updated_at, closed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.AccountID, s.ChainID, s.OriginatingTradeID, s.UnderlyingSymbol,
		s.StrategyType, s.CostBasis, s.RealizedPnL, s.OpenedAt, s.UpdatedAt, s.ClosedAt,
	)
	if err != nil {
		return fmt.Errorf("create position: %w", err)
	}
	return nil
}

// UpdatePosition updates mutable fields: cost_basis, realized_pnl, strategy_type, updated_at, closed_at.
func (r *positionRepo) UpdatePosition(ctx context.Context, pos *domain.Position) error {
	s := model.PositionToStorage(*pos)
	res, err := r.db.ExecContext(ctx,
		`UPDATE positions SET
			cost_basis    = ?,
			realized_pnl  = ?,
			strategy_type = ?,
			updated_at    = ?,
			closed_at     = ?
		 WHERE id = ?`,
		s.CostBasis, s.RealizedPnL, s.StrategyType, s.UpdatedAt, s.ClosedAt, s.ID,
	)
	if err != nil {
		return fmt.Errorf("update position: %w", err)
	}
	return requireOneRow(res, "position", pos.ID)
}

// GetPositionByTradeID finds a position by its originating_trade_id.
// Returns domain.ErrNotFound if no position exists.
func (r *positionRepo) GetPositionByTradeID(ctx context.Context, accountID, originatingTradeID string) (*domain.Position, error) {
	var row model.Position
	err := r.db.QueryRowContext(ctx,
		positionSelect+` WHERE account_id = ? AND originating_trade_id = ?`,
		accountID, originatingTradeID,
	).Scan(row.ScanDest()...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get position by trade id: %w", err)
	}
	pos, err := row.ToDomain()
	if err != nil {
		return nil, err
	}
	return &pos, nil
}

// GetPositionByChainID returns the position for a chain.
// The unique constraint on chain_id guarantees at most one result.
// Returns domain.ErrNotFound if no position exists for that chain.
func (r *positionRepo) GetPositionByChainID(ctx context.Context, accountID, chainID string) (*domain.Position, error) {
	var row model.Position
	err := r.db.QueryRowContext(ctx,
		positionSelect+` WHERE account_id = ? AND chain_id = ?`,
		accountID, chainID,
	).Scan(row.ScanDest()...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get position by chain id: %w", err)
	}
	pos, err := row.ToDomain()
	if err != nil {
		return nil, err
	}
	return &pos, nil
}

// ListOpenPositions retrieves all open positions for an account (closed_at IS NULL),
// ordered by opened_at.
func (r *positionRepo) ListOpenPositions(ctx context.Context, accountID string) ([]domain.Position, error) {
	rows, err := r.db.QueryContext(ctx,
		positionSelect+` WHERE account_id = ? AND closed_at IS NULL ORDER BY opened_at`,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("list open positions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var positions []domain.Position
	for rows.Next() {
		var row model.Position
		if err := rows.Scan(row.ScanDest()...); err != nil {
			return nil, fmt.Errorf("scan position: %w", err)
		}
		pos, err := row.ToDomain()
		if err != nil {
			return nil, err
		}
		positions = append(positions, pos)
	}
	return positions, rows.Err()
}

// CreateLot inserts a new position lot (opening transaction).
func (r *positionRepo) CreateLot(ctx context.Context, lot *domain.PositionLot) error {
	s := model.LotToStorage(*lot)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO position_lots
			(id, account_id, instrument_id, trade_id, opening_tx_id,
			 open_quantity, remaining_quantity, open_price, open_fees,
			 opened_at, closed_at, chain_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.AccountID, s.InstrumentID, s.TradeID, s.OpeningTxID,
		s.OpenQuantity, s.RemainingQuantity, s.OpenPrice, s.OpenFees,
		s.OpenedAt, s.ClosedAt, s.ChainID,
	)
	if err != nil {
		return fmt.Errorf("create lot: %w", err)
	}
	return nil
}

// GetLot retrieves a position lot by its ID, including its instrument details.
// Returns domain.ErrNotFound if the lot does not exist.
func (r *positionRepo) GetLot(ctx context.Context, id string) (*domain.PositionLot, error) {
	var row model.PositionLot
	err := r.db.QueryRowContext(ctx,
		lotJoinSelect+` WHERE pl.id = ?`, id,
	).Scan(row.ScanDest()...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get lot: %w", err)
	}
	lot, err := row.ToDomain()
	if err != nil {
		return nil, err
	}
	return &lot, nil
}

// ListOpenLotsByInstrument returns open lots ordered by opened_at ASC (FIFO order).
func (r *positionRepo) ListOpenLotsByInstrument(ctx context.Context, accountID, instrumentID string) ([]domain.PositionLot, error) {
	rows, err := r.db.QueryContext(ctx,
		lotJoinSelect+` WHERE pl.account_id = ? AND pl.instrument_id = ? AND CAST(pl.remaining_quantity AS REAL) != 0
		 ORDER BY pl.opened_at ASC`,
		accountID, instrumentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list open lots by instrument: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanLotRows(rows)
}

// ListOpenLotsByTrade returns open lots opened by the given trade, FIFO ordered.
func (r *positionRepo) ListOpenLotsByTrade(ctx context.Context, accountID, tradeID string) ([]domain.PositionLot, error) {
	rows, err := r.db.QueryContext(ctx,
		lotJoinSelect+` WHERE pl.account_id = ? AND pl.trade_id = ? AND CAST(pl.remaining_quantity AS REAL) != 0
		 ORDER BY pl.opened_at ASC`,
		accountID, tradeID,
	)
	if err != nil {
		return nil, fmt.Errorf("list open lots by trade: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanLotRows(rows)
}

// ListOpenLotsByChain returns all open lots in the chain (any trade), FIFO ordered.
func (r *positionRepo) ListOpenLotsByChain(ctx context.Context, accountID, chainID string) ([]domain.PositionLot, error) {
	rows, err := r.db.QueryContext(ctx,
		lotJoinSelect+` WHERE pl.account_id = ? AND pl.chain_id = ? AND CAST(pl.remaining_quantity AS REAL) != 0
		 ORDER BY pl.opened_at ASC`,
		accountID, chainID,
	)
	if err != nil {
		return nil, fmt.Errorf("list open lots by chain: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanLotRows(rows)
}

// CloseLot atomically records a lot closing and updates the lot's remaining quantity.
// If closedAt is non-nil, the lot is marked as fully closed.
func (r *positionRepo) CloseLot(ctx context.Context, closing *domain.LotClosing, remaining decimal.Decimal, closedAt *time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("close lot: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	s := model.LotClosingToStorage(*closing)
	_, err = tx.ExecContext(ctx,
		`INSERT INTO lot_closings
			(id, lot_id, closing_tx_id, closed_quantity, close_price, close_fees,
			 realized_pnl, closed_at, resulting_lot_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.LotID, s.ClosingTxID, s.ClosedQuantity, s.ClosePrice, s.CloseFees,
		s.RealizedPnL, s.ClosedAt, s.ResultingLotID,
	)
	if err != nil {
		return fmt.Errorf("close lot: insert closing: %w", err)
	}

	var closedAtStr sql.NullString
	if closedAt != nil {
		closedAtStr = sql.NullString{String: closedAt.UTC().Format(time.RFC3339), Valid: true}
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE position_lots SET remaining_quantity = ?, closed_at = ? WHERE id = ?`,
		remaining.String(), closedAtStr, closing.LotID,
	)
	if err != nil {
		return fmt.Errorf("close lot: update remaining: %w", err)
	}
	if err := requireOneRow(res, "lot", closing.LotID); err != nil {
		return err
	}

	return tx.Commit()
}

// ListLotClosings retrieves all closing events for a lot, ordered by closed_at.
func (r *positionRepo) ListLotClosings(ctx context.Context, lotID string) ([]domain.LotClosing, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, lot_id, closing_tx_id, closed_quantity, close_price, close_fees,
		        realized_pnl, closed_at, resulting_lot_id
		 FROM lot_closings WHERE lot_id = ? ORDER BY closed_at`,
		lotID,
	)
	if err != nil {
		return nil, fmt.Errorf("list lot closings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var closings []domain.LotClosing
	for rows.Next() {
		var s model.LotClosing
		if err := rows.Scan(
			&s.ID, &s.LotID, &s.ClosingTxID, &s.ClosedQuantity, &s.ClosePrice,
			&s.CloseFees, &s.RealizedPnL, &s.ClosedAt, &s.ResultingLotID,
		); err != nil {
			return nil, fmt.Errorf("scan lot closing: %w", err)
		}
		lc, err := s.ToDomain()
		if err != nil {
			return nil, err
		}
		closings = append(closings, lc)
	}
	return closings, rows.Err()
}

// scanLotRows scans position lot rows into domain.PositionLot objects.
func scanLotRows(rows *sql.Rows) ([]domain.PositionLot, error) {
	var lots []domain.PositionLot
	for rows.Next() {
		var row model.PositionLot
		if err := rows.Scan(row.ScanDest()...); err != nil {
			return nil, fmt.Errorf("scan lot: %w", err)
		}
		lot, err := row.ToDomain()
		if err != nil {
			return nil, err
		}
		lots = append(lots, lot)
	}
	return lots, rows.Err()
}
