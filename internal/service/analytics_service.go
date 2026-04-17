package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"trade-tracker-go/internal/domain"
)

// PnLSummary is the aggregate P&L result for an account over a date range.
type PnLSummary struct {
	// RealizedPnL is the net realized P&L from all lot closings in the period (fees already deducted).
	RealizedPnL decimal.Decimal
	// CloseFees is the sum of closing-transaction fees paid during the period.
	// Opening fees are already netted into RealizedPnL and are not surfaced separately.
	CloseFees decimal.Decimal
	// WinRate is the fraction of positions closed during the period with positive realized P&L (0–1).
	// Zero if no positions were closed in the range.
	WinRate decimal.Decimal
	// PositionsClosed is the count of positions whose closed_at falls within the date range.
	PositionsClosed int
}

// StrategyStats holds aggregate performance metrics for one strategy type.
type StrategyStats struct {
	StrategyType domain.StrategyType
	Count        int
	WinRate      decimal.Decimal
	AveragePnL   decimal.Decimal
	TotalPnL     decimal.Decimal
}

// AnalyticsService computes P&L and win-rate aggregates from persisted lot and position data.
// All methods are read-only; no writes are performed.
type AnalyticsService struct {
	db *sql.DB
}

// NewAnalyticsService creates an AnalyticsService backed by the given database.
func NewAnalyticsService(db *sql.DB) *AnalyticsService {
	return &AnalyticsService{db: db}
}

// GetSymbolPnL returns total net realized P&L for an underlying symbol over a date range.
// Symbol is matched against instruments.symbol (the underlying root, e.g. "SPY" for all SPY options).
// The date range is applied to lot_closings.closed_at.
func (s *AnalyticsService) GetSymbolPnL(ctx context.Context, accountID, symbol string, from, to time.Time) (decimal.Decimal, error) {
	const q = `
		SELECT lc.realized_pnl
		FROM lot_closings lc
		JOIN position_lots pl ON pl.id = lc.lot_id
		JOIN instruments i    ON i.id   = pl.instrument_id
		WHERE pl.account_id = ?
		  AND i.symbol      = ?
		  AND lc.closed_at  >= ?
		  AND lc.closed_at  <= ?`

	rows, err := s.db.QueryContext(ctx, q, accountID, symbol,
		from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	if err != nil {
		return decimal.Zero, fmt.Errorf("analytics: get symbol pnl: %w", err)
	}
	defer func() { _ = rows.Close() }()

	total := decimal.Zero
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return decimal.Zero, fmt.Errorf("analytics: scan symbol pnl row: %w", err)
		}
		v, err := decimal.NewFromString(raw)
		if err != nil {
			return decimal.Zero, fmt.Errorf("analytics: parse symbol pnl %q: %w", raw, err)
		}
		total = total.Add(v)
	}
	if err := rows.Err(); err != nil {
		return decimal.Zero, fmt.Errorf("analytics: symbol pnl rows: %w", err)
	}
	return total, nil
}

// GetPnLSummary returns aggregate P&L statistics for an account over a date range.
// RealizedPnL and CloseFees are derived from lot_closings whose closed_at falls in range.
// WinRate and PositionsClosed are derived from positions whose closed_at falls in range.
func (s *AnalyticsService) GetPnLSummary(ctx context.Context, accountID string, from, to time.Time) (*PnLSummary, error) {
	fromStr := from.UTC().Format(time.RFC3339)
	toStr := to.UTC().Format(time.RFC3339)

	// Scan lot_closings for net P&L and close fees.
	const lotQ = `
		SELECT lc.realized_pnl, lc.close_fees
		FROM lot_closings lc
		JOIN position_lots pl ON pl.id = lc.lot_id
		WHERE pl.account_id = ?
		  AND lc.closed_at  >= ?
		  AND lc.closed_at  <= ?`

	lotRows, err := s.db.QueryContext(ctx, lotQ, accountID, fromStr, toStr)
	if err != nil {
		return nil, fmt.Errorf("analytics: pnl summary: lot query: %w", err)
	}
	defer func() { _ = lotRows.Close() }()

	var totalPnL, totalFees decimal.Decimal
	for lotRows.Next() {
		var rawPnL, rawFees string
		if err := lotRows.Scan(&rawPnL, &rawFees); err != nil {
			return nil, fmt.Errorf("analytics: pnl summary: scan lot row: %w", err)
		}
		pnl, err := decimal.NewFromString(rawPnL)
		if err != nil {
			return nil, fmt.Errorf("analytics: pnl summary: parse pnl %q: %w", rawPnL, err)
		}
		fees, err := decimal.NewFromString(rawFees)
		if err != nil {
			return nil, fmt.Errorf("analytics: pnl summary: parse fees %q: %w", rawFees, err)
		}
		totalPnL = totalPnL.Add(pnl)
		totalFees = totalFees.Add(fees)
	}
	if err := lotRows.Err(); err != nil {
		return nil, fmt.Errorf("analytics: pnl summary: lot rows: %w", err)
	}

	// Count total and winning closed positions for the win rate.
	const posQ = `
		SELECT realized_pnl
		FROM positions
		WHERE account_id = ?
		  AND closed_at IS NOT NULL
		  AND closed_at >= ?
		  AND closed_at <= ?`

	posRows, err := s.db.QueryContext(ctx, posQ, accountID, fromStr, toStr)
	if err != nil {
		return nil, fmt.Errorf("analytics: pnl summary: position query: %w", err)
	}
	defer func() { _ = posRows.Close() }()

	var total, wins int
	for posRows.Next() {
		var rawPnL string
		if err := posRows.Scan(&rawPnL); err != nil {
			return nil, fmt.Errorf("analytics: pnl summary: scan position row: %w", err)
		}
		pnl, err := decimal.NewFromString(rawPnL)
		if err != nil {
			return nil, fmt.Errorf("analytics: pnl summary: parse position pnl %q: %w", rawPnL, err)
		}
		total++
		if pnl.IsPositive() {
			wins++
		}
	}
	if err := posRows.Err(); err != nil {
		return nil, fmt.Errorf("analytics: pnl summary: position rows: %w", err)
	}

	winRate := decimal.Zero
	if total > 0 {
		winRate = decimal.NewFromInt(int64(wins)).Div(decimal.NewFromInt(int64(total)))
	}

	return &PnLSummary{
		RealizedPnL:     totalPnL,
		CloseFees:       totalFees,
		WinRate:         winRate,
		PositionsClosed: total,
	}, nil
}

// GetStrategyPerformance returns per-strategy P&L and win-rate for closed positions.
// Strategy type is taken from the originating trade (positions.originating_trade_id → trades.strategy_type).
// The date range is applied to positions.closed_at.
// Results are returned in first-seen order (deterministic for a given dataset).
func (s *AnalyticsService) GetStrategyPerformance(ctx context.Context, accountID string, from, to time.Time) ([]StrategyStats, error) {
	const q = `
		SELECT p.realized_pnl, t.strategy_type
		FROM positions p
		JOIN trades t ON t.id = p.originating_trade_id
		WHERE p.account_id = ?
		  AND p.closed_at IS NOT NULL
		  AND p.closed_at >= ?
		  AND p.closed_at <= ?`

	rows, err := s.db.QueryContext(ctx, q, accountID,
		from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("analytics: get strategy performance: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type stratAcc struct {
		count int
		wins  int
		total decimal.Decimal
	}
	byStrategy := make(map[domain.StrategyType]*stratAcc)
	var order []domain.StrategyType // first-seen order for deterministic output

	for rows.Next() {
		var rawPnL, stratType string
		if err := rows.Scan(&rawPnL, &stratType); err != nil {
			return nil, fmt.Errorf("analytics: scan strategy row: %w", err)
		}
		pnl, err := decimal.NewFromString(rawPnL)
		if err != nil {
			return nil, fmt.Errorf("analytics: parse strategy pnl %q: %w", rawPnL, err)
		}
		st := domain.StrategyType(stratType)
		if _, ok := byStrategy[st]; !ok {
			byStrategy[st] = &stratAcc{}
			order = append(order, st)
		}
		a := byStrategy[st]
		a.count++
		a.total = a.total.Add(pnl)
		if pnl.IsPositive() {
			a.wins++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analytics: strategy performance rows: %w", err)
	}

	out := make([]StrategyStats, 0, len(order))
	for _, st := range order {
		a := byStrategy[st]
		var wr, avg decimal.Decimal
		if a.count > 0 {
			n := decimal.NewFromInt(int64(a.count))
			wr = decimal.NewFromInt(int64(a.wins)).Div(n)
			avg = a.total.Div(n)
		}
		out = append(out, StrategyStats{
			StrategyType: st,
			Count:        a.count,
			WinRate:      wr,
			AveragePnL:   avg,
			TotalPnL:     a.total,
		})
	}
	return out, nil
}

// GetWinRate returns the fraction of closed positions with positive realized P&L
// whose closed_at falls within the date range. Returns zero if no positions were closed.
func (s *AnalyticsService) GetWinRate(ctx context.Context, accountID string, from, to time.Time) (decimal.Decimal, error) {
	summary, err := s.GetPnLSummary(ctx, accountID, from, to)
	if err != nil {
		return decimal.Zero, fmt.Errorf("analytics: get win rate: %w", err)
	}
	return summary.WinRate, nil
}
