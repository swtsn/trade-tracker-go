-- Enforce at most one position per (account, originating trade).
-- Without this constraint, a bug or retry in processOpening could insert duplicate
-- position rows; GetPositionByTradeID would silently return only the first, leaving
-- duplicates invisible and P&L permanently incorrect.
CREATE UNIQUE INDEX idx_positions_account_trade_unique
    ON positions(account_id, originating_trade_id);
