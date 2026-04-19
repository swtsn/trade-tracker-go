-- Enforce at most one position per (account, originating trade).
-- Without this constraint, a bug or retry in processOpening could insert duplicate
-- position rows; GetPositionByTradeID would silently return only the first, leaving
-- duplicates invisible and P&L permanently incorrect.
--
-- If this migration fails with a UNIQUE constraint violation, a duplicate position row
-- exists in the database. Deduplicate with:
--   DELETE FROM positions WHERE rowid NOT IN (
--       SELECT MIN(rowid) FROM positions GROUP BY account_id, originating_trade_id
--   );
CREATE UNIQUE INDEX idx_positions_account_trade_unique
    ON positions(account_id, originating_trade_id);

-- Denormalized underlying symbol column on trades for join-free queries.
-- Populated at import time from the first opening transaction's instrument symbol.
ALTER TABLE trades ADD COLUMN underlying_symbol TEXT NOT NULL DEFAULT '';

-- Backfill existing rows from their first opening transaction's instrument symbol.
-- Trades with no opening transactions (e.g. assignment-only) keep '' and can be
-- corrected by re-importing the relevant CSV.
UPDATE trades
SET underlying_symbol = (
    SELECT i.symbol
    FROM transactions tx
    JOIN instruments i ON i.id = tx.instrument_id
    WHERE tx.trade_id = trades.id
      AND tx.position_effect = 'opening'
    LIMIT 1
)
WHERE underlying_symbol = '';
