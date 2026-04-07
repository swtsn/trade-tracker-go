package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"trade-tracker-go/internal/domain"
	"trade-tracker-go/internal/repository/sqlite/model"
)

const positionJoinSelect = `
	SELECT
		p.id, p.account_id, p.instrument_id, p.quantity, p.cost_basis, p.realized_pnl,
		p.opened_at, p.updated_at, p.closed_at, p.chain_id,
		i.id, i.symbol, i.asset_class, i.expiration, i.strike, i.option_type,
		i.multiplier, i.osi_symbol, i.futures_expiry_month, i.exchange_code
	FROM positions p
	JOIN instruments i ON p.instrument_id = i.id`

const lotJoinSelect = `
	SELECT
		pl.id, pl.account_id, pl.instrument_id, pl.trade_id, pl.opening_tx_id,
		pl.open_quantity, pl.remaining_quantity, pl.open_price, pl.open_fees,
		pl.opened_at, pl.closed_at, pl.chain_id,
		i.id, i.symbol, i.asset_class, i.expiration, i.strike, i.option_type,
		i.multiplier, i.osi_symbol, i.futures_expiry_month, i.exchange_code
	FROM position_lots pl
	JOIN instruments i ON pl.instrument_id = i.id`

type positionRepo struct {
	db *sql.DB
}

func NewPositionRepository(db *sql.DB) *positionRepo {
	return &positionRepo{db: db}
}

func (r *positionRepo) UpsertPosition(ctx context.Context, pos *domain.Position) error {
	s := model.PositionToStorage(*pos)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO positions (id, account_id, instrument_id, quantity, cost_basis, realized_pnl, opened_at, updated_at, closed_at, chain_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(account_id, instrument_id) DO UPDATE SET
		   quantity     = excluded.quantity,
		   cost_basis   = excluded.cost_basis,
		   realized_pnl = excluded.realized_pnl,
		   updated_at   = excluded.updated_at,
		   closed_at    = excluded.closed_at,
		   chain_id     = excluded.chain_id`,
		s.ID, s.AccountID, s.InstrumentID, s.Quantity, s.CostBasis, s.RealizedPnL,
		s.OpenedAt, s.UpdatedAt, s.ClosedAt, s.ChainID,
	)
	if err != nil {
		return fmt.Errorf("upsert position: %w", err)
	}
	return nil
}

func (r *positionRepo) GetPosition(ctx context.Context, accountID, instrumentID string) (*domain.Position, error) {
	var row model.Position
	err := r.db.QueryRowContext(ctx,
		positionJoinSelect+` WHERE p.account_id = ? AND p.instrument_id = ?`,
		accountID, instrumentID,
	).Scan(row.ScanDest()...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get position: %w", err)
	}
	pos, err := row.ToDomain()
	if err != nil {
		return nil, err
	}
	return &pos, nil
}

func (r *positionRepo) ListOpenPositions(ctx context.Context, accountID string) ([]domain.Position, error) {
	rows, err := r.db.QueryContext(ctx,
		positionJoinSelect+` WHERE p.account_id = ? AND p.closed_at IS NULL ORDER BY p.opened_at`,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("list open positions: %w", err)
	}
	defer rows.Close()

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
		lotJoinSelect+` WHERE pl.account_id = ? AND pl.instrument_id = ? AND pl.remaining_quantity != '0'
		 ORDER BY pl.opened_at ASC`,
		accountID, instrumentID,
	)
	if err != nil {
		return nil, fmt.Errorf("list open lots: %w", err)
	}
	defer rows.Close()
	return scanLotRows(rows)
}

func (r *positionRepo) CloseLot(ctx context.Context, closing *domain.LotClosing, remaining decimal.Decimal, closedAt *time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("close lot: begin: %w", err)
	}
	defer tx.Rollback()

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
	defer rows.Close()

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
