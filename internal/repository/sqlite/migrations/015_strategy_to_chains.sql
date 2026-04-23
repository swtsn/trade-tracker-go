-- Migrate strategy_type from trades to chains.
-- Strategy is classified once at chain creation using the opening trade's legs.

ALTER TABLE chains ADD COLUMN strategy_type TEXT NOT NULL DEFAULT 'unknown';

UPDATE chains
SET strategy_type = (
    SELECT t.strategy_type FROM trades t WHERE t.id = chains.original_trade_id
);

ALTER TABLE trades DROP COLUMN strategy_type;
