-- Remove vestigial lifecycle fields from trades and add attribution gap flag to chains.
--
-- closed_at was never written in production; all rows are NULL.
-- opened_at is renamed to executed_at — it holds the earliest ExecutedAt across the
-- trade's transactions (a grouping timestamp, not a lifecycle state).
-- chains.attribution_gap marks chains created from mixed trades whose closing legs
-- could not be attributed to an existing open chain.
ALTER TABLE trades RENAME COLUMN opened_at TO executed_at;
ALTER TABLE trades DROP COLUMN closed_at;
ALTER TABLE chains ADD COLUMN attribution_gap INTEGER NOT NULL DEFAULT 0;
