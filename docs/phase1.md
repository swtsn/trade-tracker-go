# Phase 1 — Foundation

Build the domain types and database layer. No business logic, no gRPC. At the end of this phase: a fully tested data layer that can persist all core entities.

---

## Scope

- All domain types in `internal/domain/`
- SQLite setup: WAL mode, migration runner, embedded SQL
- Storage models (flat SQL-scannable structs) in `internal/repository/sqlite/model/`
- Repository implementations in `internal/repository/sqlite/`
- Repository interfaces in `internal/repository/interfaces.go`
- Unit tests using `:memory:` SQLite

---

## Domain Types (`internal/domain/`)

### `instrument.go`
- `AssetClass` string enum: `equity | equity_option | future | future_option`
- `OptionType` string enum: `C | P`
- `Instrument` struct: `Symbol`, `AssetClass`, `*OptionDetails`, `*FutureDetails`
- `OptionDetails`: `Expiration`, `Strike decimal.Decimal`, `OptionType`, `Multiplier decimal.Decimal`, `OSI string`
- `FutureDetails`: `ExpiryMonth`, `ExchangeCode string`
- `InstrumentID() string` — returns the deterministic SHA-256 hash ID for this instrument

### `transaction.go`
- `Action` string enum: `BTO | STO | BTC | STC | BUY | SELL | ASSIGNMENT | EXPIRATION | EXERCISE`
- `PositionEffect` string enum: `opening | closing | neutral`
- `TransactionID`, `TradeID`, `ChainID` type aliases for `string`
- `Transaction` struct (see architecture.md for full fields)

### `trade.go`
- `StrategyType` string enum (see architecture.md for full list)
- `Trade` struct: `ID`, `AccountID`, `Broker`, `Transactions []Transaction`, `StrategyType`, `OpenedAt`, `ClosedAt *time.Time`, `Notes`

### `position.go`
- `LotID`, `LotClosingID`, `PositionID` type aliases for `string`
- `Position` struct — materialized cache
- `PositionLot` struct — source of truth; signed quantity (negative = short)
- `LotClosing` struct — includes `ResultingLotID *LotID` for assignment/exercise transitions

### `chain.go`
- `ChainID`, `ChainLinkID` type aliases for `string`
- `LinkType` string enum: `roll | assignment | exercise`
- `Chain` struct
- `ChainLink` struct

### `pnl.go`
- `PnL` struct: `Realized`, `Fees decimal.Decimal`
- `NetRealized() decimal.Decimal`

### `errors.go`
- Sentinel domain errors: `ErrNotFound`, `ErrDuplicate`, `ErrInvalidInstrument`, etc.

---

## Database Schema

Managed by `golang-migrate/migrate/v4` with SQL files embedded via `//go:embed`.

### Migration files

**`001_initial_schema.sql`**
- `accounts`
- `instruments` (deterministic hash ID)
- `trades`
- `transactions`
- `position_lots`
- `lot_closings`
- `positions`

**`002_chains.sql`**
- `chains`
- `chain_links`

**`003_import_history.sql`**
- `import_jobs`

See `architecture.md` for full column definitions per table.

### Connection setup (`internal/repository/sqlite/db.go`)
```go
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;
PRAGMA foreign_keys=ON;
```
Migrations run at startup before the server accepts connections.

---

## Storage Models (`internal/repository/sqlite/model/`)

Each file defines:
1. A flat SQL-scannable struct (all fields primitive types: `string`, `sql.NullString`, `sql.NullTime`, `int64`, etc.)
2. `toStorage(domain.X) sqlX` — converts domain type to storage type
3. `toDomain(sqlX) (domain.X, error)` — converts storage type to domain type; parses decimal strings, resolves nulls

Files:
- `model/transaction.go` — `sqlTransaction`
- `model/trade.go` — `sqlTrade`
- `model/position.go` — `sqlPosition`, `sqlPositionLot`, `sqlLotClosing`
- `model/chain.go` — `sqlChain`, `sqlChainLink`

---

## Repository Interfaces (`internal/repository/interfaces.go`)

```go
type AccountRepository interface {
    Create(ctx, *domain.Account) error
    GetByID(ctx, id string) (*domain.Account, error)
    List(ctx) ([]domain.Account, error)
}

type InstrumentRepository interface {
    Upsert(ctx, *domain.Instrument) error   // insert or ignore (deterministic ID)
    GetByID(ctx, id string) (*domain.Instrument, error)
}

type TransactionRepository interface {
    Create(ctx, *domain.Transaction) error
    GetByID(ctx, id domain.TransactionID) (*domain.Transaction, error)
    ListByTrade(ctx, tradeID domain.TradeID) ([]domain.Transaction, error)
    ListByAccountAndTimeRange(ctx, accountID string, from, to time.Time) ([]domain.Transaction, error)
    ExistsByBrokerTxID(ctx, brokerTxID, broker, accountID string) (bool, error)
}

type TradeRepository interface {
    Create(ctx, *domain.Trade) error
    GetByID(ctx, id domain.TradeID) (*domain.Trade, error)
    ListByAccount(ctx, accountID string, opts ListTradesOptions) ([]domain.Trade, int, error)
    UpdateStrategy(ctx, id domain.TradeID, strategy domain.StrategyType) error
    UpdateClosedAt(ctx, id domain.TradeID, closedAt time.Time) error
}

type PositionRepository interface {
    UpsertPosition(ctx, *domain.Position) error
    GetPosition(ctx, accountID, instrumentID string) (*domain.Position, error)
    ListOpenPositions(ctx, accountID string) ([]domain.Position, error)

    CreateLot(ctx, *domain.PositionLot) error
    GetLot(ctx, id domain.LotID) (*domain.PositionLot, error)
    ListOpenLotsByInstrument(ctx, accountID, instrumentID string) ([]domain.PositionLot, error)
    UpdateLotRemaining(ctx, id domain.LotID, remaining decimal.Decimal, closedAt *time.Time) error

    CreateLotClosing(ctx, *domain.LotClosing) error
    ListLotClosings(ctx, lotID domain.LotID) ([]domain.LotClosing, error)
}

type ChainRepository interface {
    CreateChain(ctx, *domain.Chain) error
    GetChainByID(ctx, id domain.ChainID) (*domain.Chain, error)
    ListChainsByAccount(ctx, accountID string, openOnly bool) ([]domain.Chain, error)
    UpdateChainClosed(ctx, id domain.ChainID, closedAt time.Time) error

    CreateChainLink(ctx, *domain.ChainLink) error
    ListChainLinks(ctx, chainID domain.ChainID) ([]domain.ChainLink, error)
}
```

---

## Testing

- All repo tests use `modernc.org/sqlite` with `:memory:` DSN — no mocks, real SQL
- Run migrations before each test suite
- Test happy paths and key edge cases: dedup on `broker_tx_id`, FIFO lot ordering, `resulting_lot_id` assignment linkage, chain link sequencing

---

## Done When

- `go build ./...` passes
- `go test ./internal/domain/... ./internal/repository/...` passes
- All tables created correctly by migrations
- Each repo can round-trip a domain type to SQLite and back with no data loss
