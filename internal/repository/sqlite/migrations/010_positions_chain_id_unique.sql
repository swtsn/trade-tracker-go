-- Enforce one position per chain.
-- A partial unique index is used so that unchained positions (chain_id IS NULL) are
-- not constrained against each other — SQLite treats NULL != NULL for unique checks,
-- but a partial index makes the intent explicit.
CREATE UNIQUE INDEX idx_positions_chain_id_unique
    ON positions(chain_id)
    WHERE chain_id IS NOT NULL;
