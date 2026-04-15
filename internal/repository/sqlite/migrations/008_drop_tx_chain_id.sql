-- transactions.chain_id was added in the initial schema but was never written
-- by the application. Chain membership is tracked via chains.original_trade_id
-- and chain_links (closing_trade_id, opening_trade_id). This column is redundant
-- and is safe to drop — no data is lost.
ALTER TABLE transactions DROP COLUMN chain_id;
