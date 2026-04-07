CREATE TABLE chains (
    id                TEXT PRIMARY KEY,
    account_id        TEXT NOT NULL REFERENCES accounts(id),
    underlying_symbol TEXT NOT NULL,
    original_trade_id TEXT NOT NULL REFERENCES trades(id),
    created_at        TEXT NOT NULL,
    closed_at         TEXT
);

CREATE TABLE chain_links (
    id                TEXT PRIMARY KEY,
    chain_id          TEXT NOT NULL REFERENCES chains(id),
    sequence          INTEGER NOT NULL,
    link_type         TEXT NOT NULL,
    closing_trade_id  TEXT NOT NULL REFERENCES trades(id),
    opening_trade_id  TEXT NOT NULL REFERENCES trades(id),
    linked_at         TEXT NOT NULL,
    strike_change     TEXT NOT NULL DEFAULT '0',
    expiration_change INTEGER NOT NULL DEFAULT 0,
    credit_debit      TEXT NOT NULL DEFAULT '0',
    UNIQUE(chain_id, sequence)
);
