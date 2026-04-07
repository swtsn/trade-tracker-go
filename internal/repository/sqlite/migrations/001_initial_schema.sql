CREATE TABLE accounts (
    id             TEXT PRIMARY KEY,
    broker         TEXT NOT NULL,
    account_number TEXT NOT NULL,
    name           TEXT NOT NULL,
    created_at     TEXT NOT NULL
);

CREATE TABLE instruments (
    id                   TEXT PRIMARY KEY,
    symbol               TEXT NOT NULL,
    asset_class          TEXT NOT NULL,
    expiration           TEXT,
    strike               TEXT,
    option_type          TEXT,
    multiplier           TEXT NOT NULL,
    osi_symbol           TEXT,
    futures_expiry_month TEXT,
    exchange_code        TEXT,
    UNIQUE(symbol, asset_class, expiration, strike, option_type)
);

CREATE TABLE trades (
    id            TEXT PRIMARY KEY,
    account_id    TEXT NOT NULL REFERENCES accounts(id),
    broker        TEXT NOT NULL,
    strategy_type TEXT NOT NULL,
    opened_at     TEXT NOT NULL,
    closed_at     TEXT,
    notes         TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL
);

CREATE TABLE transactions (
    id              TEXT PRIMARY KEY,
    trade_id        TEXT NOT NULL REFERENCES trades(id),
    broker_tx_id    TEXT NOT NULL,
    broker          TEXT NOT NULL,
    account_id      TEXT NOT NULL REFERENCES accounts(id),
    instrument_id   TEXT NOT NULL REFERENCES instruments(id),
    action          TEXT NOT NULL,
    quantity        TEXT NOT NULL,
    fill_price      TEXT NOT NULL,
    fees            TEXT NOT NULL,
    executed_at     TEXT NOT NULL,
    position_effect TEXT NOT NULL,
    chain_id        TEXT,
    created_at      TEXT NOT NULL,
    UNIQUE(broker_tx_id, broker, account_id)
);

CREATE TABLE position_lots (
    id                  TEXT PRIMARY KEY,
    account_id          TEXT NOT NULL REFERENCES accounts(id),
    instrument_id       TEXT NOT NULL REFERENCES instruments(id),
    trade_id            TEXT NOT NULL REFERENCES trades(id),
    opening_tx_id       TEXT NOT NULL REFERENCES transactions(id),
    open_quantity       TEXT NOT NULL,
    remaining_quantity  TEXT NOT NULL,
    open_price          TEXT NOT NULL,
    open_fees           TEXT NOT NULL,
    opened_at           TEXT NOT NULL,
    closed_at           TEXT,
    chain_id            TEXT
);

CREATE TABLE lot_closings (
    id               TEXT PRIMARY KEY,
    lot_id           TEXT NOT NULL REFERENCES position_lots(id),
    closing_tx_id    TEXT NOT NULL REFERENCES transactions(id),
    closed_quantity  TEXT NOT NULL,
    close_price      TEXT NOT NULL,
    close_fees       TEXT NOT NULL,
    realized_pnl     TEXT NOT NULL,
    closed_at        TEXT NOT NULL,
    resulting_lot_id TEXT REFERENCES position_lots(id)
);

CREATE TABLE positions (
    id            TEXT PRIMARY KEY,
    account_id    TEXT NOT NULL REFERENCES accounts(id),
    instrument_id TEXT NOT NULL REFERENCES instruments(id),
    quantity      TEXT NOT NULL,
    cost_basis    TEXT NOT NULL,
    realized_pnl  TEXT NOT NULL,
    opened_at     TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    chain_id      TEXT,
    UNIQUE(account_id, instrument_id)
);
