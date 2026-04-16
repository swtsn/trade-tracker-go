-- Restructure positions: one row per chain (or originating trade if unchained).
-- Drops instrument_id and quantity (per-leg concerns); adds underlying_symbol
-- and originating_trade_id. Nothing currently writes positions so no data migration needed.

CREATE TABLE positions_new (
    id                   TEXT PRIMARY KEY,
    account_id           TEXT NOT NULL REFERENCES accounts(id),
    chain_id             TEXT REFERENCES chains(id),
    originating_trade_id TEXT NOT NULL REFERENCES trades(id),
    underlying_symbol    TEXT NOT NULL,
    strategy_type        TEXT NOT NULL DEFAULT 'unknown',
    cost_basis           TEXT NOT NULL,
    realized_pnl         TEXT NOT NULL,
    opened_at            TEXT NOT NULL,
    updated_at           TEXT NOT NULL,
    closed_at            TEXT
);

-- No data migration: nothing wrote to positions before this migration.
DROP TABLE positions;

ALTER TABLE positions_new RENAME TO positions;

CREATE INDEX idx_positions_account_symbol
    ON positions(account_id, underlying_symbol);

CREATE INDEX idx_positions_account_chain
    ON positions(account_id, chain_id);

CREATE INDEX idx_positions_account_trade
    ON positions(account_id, originating_trade_id);
