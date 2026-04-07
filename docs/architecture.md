# Trade Tracker Go — Architecture Plan

## Context

Greenfield Go project for tracking options-focused trades across multiple brokerages. The user makes ~10 trades/day with 1–4 legs each (~10–40 transactions/day). Primary goals: import trades from Tastytrade and Schwab, identify options strategies, calculate P&L, detect and track rolling, and provide rich analytics. The system is personal infrastructure: gRPC server on Ubuntu, TUI client on macOS laptop, SQLite for storage (gRPC server is the sole DB accessor).

---

## Architecture Overview

Layered architecture: `domain` → `repository` → `service` → `grpc`/`tui`. Nothing in inner layers knows about outer layers.

### Package Structure

```
trade-tracker-go/
├── buf.yaml / buf.gen.yaml           # ConnectRPC proto generation
├── Makefile                          # proto gen, build, test, migrate targets
├── docs/                             # architecture, future work
├── proto/tradetracker/v1/
│   ├── trade.proto
│   ├── position.proto
│   ├── analytics.proto
│   └── import.proto
├── gen/tradetracker/v1/              # buf-generated code (gitignored or committed)
├── cmd/
│   ├── trade-tracker-server/main.go  # gRPC server binary
│   └── trade-tracker-tui/main.go     # TUI client binary
└── internal/
    ├── domain/                       # Pure types, no external deps
    │   ├── instrument.go             # Instrument, OptionDetails, FutureDetails
    │   ├── transaction.go            # Transaction, Action, PositionEffect
    │   ├── trade.go                  # Trade (group of simultaneous transactions)
    │   ├── position.go               # Position (materialized cache), PositionLot, LotClosing
    │   ├── chain.go                  # Chain, ChainLink
    │   ├── strategy.go               # StrategyType enum
    │   ├── pnl.go                    # PnL value object
    │   └── errors.go
    ├── repository/
    │   ├── interfaces.go             # Repository interfaces (for testing)
    │   └── sqlite/
    │       ├── db.go                 # Open/close, WAL config, migration runner
    │       ├── migrations/           # Embedded SQL files (go:embed)
    │       │   ├── 001_initial_schema.sql
    │       │   ├── 002_chains.sql
    │       │   └── 003_import_history.sql
    │       ├── model/                # Storage models: flat SQL-scannable structs
    │       │   ├── transaction.go    # sqlTransaction + toStorage()/toDomain() mappers
    │       │   ├── trade.go
    │       │   ├── position.go       # sqlPosition, sqlPositionLot, sqlLotClosing + mappers
    │       │   └── chain.go          # sqlChain, sqlChainLink + mappers
    │       ├── transaction_repo.go   # Hand-written SQL; scans into model/, converts to domain
    │       ├── trade_repo.go
    │       ├── position_repo.go      # positions + position_lots + lot_closings
    │       └── chain_repo.go
    ├── service/
    │   ├── import_service.go         # Pipeline orchestration
    │   ├── position_service.go       # Position + lot state management
    │   ├── chain_service.go          # Chain and chain_link management
    │   ├── strategy_service.go       # Strategy classification with position context
    │   └── analytics_service.go
    ├── importer/
    │   ├── common.go                 # RawTransaction + Importer interface
    │   ├── tastytrade/parser.go
    │   └── schwab/parser.go
    ├── strategy/
    │   ├── classifier.go             # Pure rule engine
    │   ├── rules.go                  # One rule per strategy shape
    │   └── rules_test.go
    ├── grpc/
    │   ├── server.go
    │   ├── trade_handler.go
    │   ├── position_handler.go
    │   ├── analytics_handler.go
    │   └── import_handler.go
    └── tui/                          # Bubbletea app (Phase 4)
```

---

## Storage vs. Domain Model Separation

Domain types in `internal/domain/` are pure business structs — nested, typed, no SQL concerns. Storage types in `internal/repository/sqlite/model/` are flat, SQL-scannable structs with `TEXT` fields for decimals and nullable primitives for optional values.

**Example:** `domain.Transaction` has `Instrument Instrument` (nested struct with `*OptionDetails`). The storage model `sqlTransaction` has flat fields: `InstrumentID string`, `AssetClass string`, `Strike sql.NullString`, `Expiration sql.NullTime`, `OptionType sql.NullString`. Each `model/` file exposes `toStorage(domain.X) sqlX` and `toDomain(sqlX) (domain.X, error)` mapper functions. Repo methods scan SQL rows into storage models, then call `toDomain()` before returning. No ORM — hand-written SQL with `database/sql`-style scanning against `modernc.org/sqlite`.

---

## Domain Model

### Instrument (`internal/domain/instrument.go`)
```go
type AssetClass string  // equity | equity_option | future | future_option

type Instrument struct {
    Symbol     string
    AssetClass AssetClass
    Option     *OptionDetails  // nil for non-options
    Future     *FutureDetails  // nil for non-futures
}

type OptionDetails struct {
    Expiration  time.Time
    Strike      decimal.Decimal   // shopspring/decimal — never float64 for money
    OptionType  OptionType        // "C" | "P"
    Multiplier  int               // 100 for equity; contract-specific for futures options
    OSI         string            // full OCC symbol
}

type FutureDetails struct {
    ExpiryMonth  time.Time
    ExchangeCode string
}
```

### Transaction (`internal/domain/transaction.go`)
```go
type Action string  // BTO | STO | BTC | STC | BUY | SELL | ASSIGNMENT | EXPIRATION | EXERCISE

type Transaction struct {
    ID             TransactionID
    TradeID        TradeID           // groups legs of same multi-leg order
    BrokerTxID     string            // broker's own ID (for dedup)
    Broker         string
    AccountID      string
    Instrument     Instrument
    Action         Action
    Quantity       decimal.Decimal   // always positive; direction in Action
    FillPrice      decimal.Decimal
    Fees           decimal.Decimal   // per leg
    ExecutedAt     time.Time
    ChainID        *ChainID          // nil if not part of a chain
    PositionEffect PositionEffect    // opening | closing | neutral
}
```

### Trade (`internal/domain/trade.go`)
Groups simultaneous transactions (one broker order). Unit for strategy identification. Considered closed when all its opening lots reach `remaining_quantity = 0`.

```go
type Trade struct {
    ID           TradeID
    AccountID    string
    Broker       string
    Transactions []Transaction
    StrategyType StrategyType
    OpenedAt     time.Time
    ClosedAt     *time.Time
    Notes        string
}
```

### Position, PositionLot, LotClosing (`internal/domain/position.go`)

```go
// Position is a materialized cache of current open state per (account, instrument).
// Written in the same DB transaction as lot changes. Never written independently.
type Position struct {
    ID           PositionID
    AccountID    string
    Instrument   Instrument
    Quantity     decimal.Decimal   // signed: negative = short; 0 = closed
    CostBasis    decimal.Decimal
    RealizedPnL  decimal.Decimal
    OpenedAt     time.Time
    UpdatedAt    time.Time
    ChainID      *ChainID
}

// PositionLot is the source of truth. One row per opening transaction.
// Quantity is signed (negative = short). FIFO matching on close.
type PositionLot struct {
    ID                LotID
    AccountID         string
    Instrument        Instrument
    TradeID           TradeID
    OpeningTxID       TransactionID
    OpenQuantity      decimal.Decimal   // signed
    RemainingQuantity decimal.Decimal   // decremented on each close
    OpenPrice         decimal.Decimal
    OpenFees          decimal.Decimal
    OpenedAt          time.Time
    ClosedAt          *time.Time        // set when RemainingQuantity reaches 0
    ChainID           *ChainID
}

// LotClosing records one close event against a lot.
type LotClosing struct {
    ID              LotClosingID
    LotID           LotID
    ClosingTxID     TransactionID
    ClosedQuantity  decimal.Decimal
    ClosePrice      decimal.Decimal
    CloseFees       decimal.Decimal
    RealizedPnL     decimal.Decimal
    ClosedAt        time.Time
    ResultingLotID  *LotID            // set for assignment/exercise: points to the new stock/futures lot
}
```

### Chain, ChainLink (`internal/domain/chain.go`)

A chain represents the full lifecycle of a position — spans rolls, assignments, and related trades on the same underlying. Example: STO put → assigned → long stock → STO covered calls is one chain.

Total chain P&L = `SUM(lot_closings.realized_pnl)` for all lots with this `chain_id`.

```go
type Chain struct {
    ID               ChainID
    AccountID        string
    UnderlyingSymbol string
    OriginalTradeID  TradeID
    CreatedAt        time.Time
    ClosedAt         *time.Time
    Links            []ChainLink
}

type ChainLink struct {
    ID               ChainLinkID
    ChainID          ChainID
    Sequence         int
    LinkType         LinkType          // roll | assignment | exercise
    ClosingTradeID   TradeID
    OpeningTradeID   TradeID           // may equal ClosingTradeID for single-order rolls
    LinkedAt         time.Time
    StrikeChange     decimal.Decimal   // new - old (rolls only)
    ExpirationChange int               // calendar days forward (rolls only)
    CreditDebit      decimal.Decimal   // net premium from the event
}

type LinkType string
const (
    LinkTypeRoll       LinkType = "roll"
    LinkTypeAssignment LinkType = "assignment"
    LinkTypeExercise   LinkType = "exercise"
)
```

Chain linking is **manual only** in the initial implementation. Auto-detection is deferred. See `docs/future.md`.

---

## gRPC Services (ConnectRPC)

**`proto/tradetracker/v1/trade.proto`** — `TradeService`: CreateTrade, GetTrade, ListTrades, UpdateTradeNotes, DeleteTrade

**`proto/tradetracker/v1/position.proto`** — `PositionService`: ListPositions, GetPosition, GetPositionHistory, ListLots, GetChain, ListChains, LinkChain, UnlinkChain

**`proto/tradetracker/v1/analytics.proto`** — `AnalyticsService`: GetSymbolPnL, GetPnLSummary, GetStrategyPerformance, GetWinRate, GetTimeSeriesPnL (streaming)

**`proto/tradetracker/v1/import.proto`** — `ImportService`: ImportCSV, GetImportStatus, ListImportHistory, PreviewImport (dry-run)

Proto conventions:
- Monetary values as `string` in proto (parsed to `decimal.Decimal` in handlers)
- Times as `google.protobuf.Timestamp`
- All list RPCs: `page_token` + `page_size` pagination

---

## Import Pipeline (`internal/service/import_service.go`)

1. **Parse** — broker-specific `Importer.Parse(r io.Reader) ([]RawTransaction, error)`
2. **Normalize** — map `RawTransaction` → `domain.Transaction` (symbol parsing, action enum mapping, decimal parsing)
3. **Dedup** — skip rows where `(broker_tx_id, broker, account_id)` already exists
4. **Group into Trades** — by broker order ID (Tastytrade) or 5-second time-window heuristic (Schwab, which has no order ID)
5. **Classify Strategy** — call `strategy.Classifier.Classify(legs)`; `strategy_service` upgrades (e.g., `ShortCall` → `CoveredCall`) using existing open positions
6. **Update Lots + Positions** — open new lots (opening transactions); FIFO match and record `lot_closings` (closing transactions); update materialized `positions` in the same DB transaction
7. **Persist** — single DB transaction: instruments, transactions, trades, lots, lot_closings, positions

Chain linking is not part of the import pipeline — it is done manually via the API after import.

### Broker Parsers

**Tastytrade** (`internal/importer/tastytrade/parser.go`): CSV columns map directly to `RawTransaction`. Provides order ID, underlying symbol, expiration, strike, option type, multiplier.

**Schwab** (`internal/importer/schwab/parser.go`): Requires regex-based description parser (e.g. `"SPY 12/19/2025 500.00 C"`) to extract structured option fields. No order ID — time-window grouping only.

---

## Strategy Identification (`internal/strategy/`)

### LegShape normalization
```go
type LegShape struct {
    AssetClass domain.AssetClass
    OptionType domain.OptionType   // "" for stock/futures
    Strike     decimal.Decimal
    Expiration time.Time
    Direction  int                 // +1 long, -1 short
    Quantity   decimal.Decimal
}
```

### Rule-based classifier
```go
type Rule struct {
    Name     domain.StrategyType
    Priority int
    Match    func(legs []LegShape) bool
}
```
Rules checked in priority order (most specific first). Pure functions, no side effects. Key rules:

| Strategy | Legs | Logic |
|---|---|---|
| CoveredCall | 2 | 1 stock long + 1 call short |
| CSP | 1 | 1 put short, opening |
| Vertical (any) | 2 | same type + expiry, diff strikes, one long one short |
| Straddle | 2 | put + call, same strike + expiry, same direction |
| Strangle | 2 | put + call, diff strikes, same expiry, same direction |
| Calendar | 2 | same type + strike, diff expiry, one long one short |
| Diagonal | 2 | same type, diff strike + diff expiry, one long one short |
| Iron Condor | 4 | put vertical + call vertical, same underlying + expiry |
| Iron Butterfly | 4 | iron condor where inner strikes are equal |
| Butterfly | 3 | 3 same-type same-expiry options, equidistant strikes |

---

## Chain Tracking (`internal/service/chain_service.go`)

A **chain** represents the full lifecycle of a position — not just rolling, but also assignment and exercise transitions. Example: STO put → assigned → long stock → STO covered calls is one chain.

### Link types
- `roll` — option leg closed and reopened at different strike/expiry
- `assignment` — short option assigned; option lot closes, stock lot opens (`lot_closings.resulting_lot_id` links them)
- `exercise` — long option exercised; option lot closes, stock lot opens

### Chain P&L
`SUM(lot_closings.realized_pnl)` for all `position_lots` with the same `chain_id`. Captures total P&L across the full lifecycle regardless of instrument transitions.

### Linking
Chain linking is **manual only** — user explicitly calls `PositionService.LinkChain` / `UnlinkChain`. Auto-detection deferred. See `docs/future.md`.

---

## Database Schema (SQLite, WAL mode)

Connection pragmas on every open: `PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL; PRAGMA foreign_keys=ON;`

### Key design decisions
- All monetary values and strikes stored as `TEXT` (decimal strings), never `REAL`
- Primary keys are UUIDv7 (`github.com/google/uuid`) — time-sortable, RFC 9562
- Instrument IDs are deterministic SHA-256 hashes of their unique fields — no DB round-trip needed during import
- Short lots use signed quantity (negative = short); P&L math is uniform across long and short
- FIFO lot matching
- `positions` is a materialized cache written in the same DB transaction as lot changes
- Migrations managed by `golang-migrate/migrate/v4` with embedded SQL files

### Tables

**`accounts`** — one row per brokerage account
```
id, broker, account_number, name, created_at
```

**`instruments`** — deduplicated; ID = SHA-256 of unique fields
```
id (hash), symbol, asset_class,
expiration, strike (TEXT), option_type, multiplier, osi_symbol,
futures_expiry_month, exchange_code
UNIQUE(symbol, asset_class, expiration, strike, option_type)
```

**`trades`** — logical grouping of simultaneous transactions (one broker order)
```
id, account_id, broker, strategy_type, opened_at, closed_at, notes, created_at
```

**`transactions`** — individual fills; one row per execution leg
```
id, trade_id, broker_tx_id, broker, account_id, instrument_id,
action, quantity (TEXT), fill_price (TEXT), fees (TEXT),
executed_at, position_effect, chain_id (nullable), created_at
UNIQUE(broker_tx_id, broker, account_id)
```

**`position_lots`** — source of truth; one row per opening transaction
```
id, account_id, instrument_id, trade_id, opening_tx_id,
open_quantity (TEXT, signed), remaining_quantity (TEXT),
open_price (TEXT), open_fees (TEXT),
opened_at, closed_at (nullable), chain_id (nullable)
```

**`lot_closings`** — one row per close event; stores realized P&L explicitly
```
id, lot_id, closing_tx_id, closed_quantity (TEXT),
close_price (TEXT), close_fees (TEXT), realized_pnl (TEXT),
closed_at, resulting_lot_id (nullable FK → position_lots)
```

**`positions`** — materialized cache of current open state
```
id, account_id, instrument_id, quantity (TEXT), cost_basis (TEXT),
realized_pnl (TEXT), opened_at, updated_at, chain_id (nullable)
UNIQUE(account_id, instrument_id)
```

**`chains`** — position lifecycle across rolls, assignments, and related trades
```
id, account_id, underlying_symbol, original_trade_id, created_at, closed_at
```

**`chain_links`** — one row per event within a chain
```
id, chain_id, sequence, link_type (roll | assignment | exercise),
closing_trade_id, opening_trade_id,
linked_at, strike_change (TEXT), expiration_change (INTEGER days), credit_debit (TEXT)
UNIQUE(chain_id, sequence)
```

**`import_jobs`** — audit trail for CSV imports
```
id, account_id, broker, filename, row_count, imported_count,
skipped_count, error_count, status, error_detail, started_at, completed_at
```

Full SQL in `internal/repository/sqlite/migrations/`.

---

## Dependencies

| Package | Use |
|---|---|
| `connectrpc.com/connect` | gRPC transport (HTTP/1.1 + HTTP/2, cleaner than raw grpc) |
| `google.golang.org/protobuf` | Proto runtime |
| `modernc.org/sqlite` | Pure-Go SQLite — no CGO, works on macOS dev + Linux deploy |
| `github.com/golang-migrate/migrate/v4` | DB migrations with embedded SQL files |
| `github.com/shopspring/decimal` | Decimal arithmetic — float64 is unsafe for money; options prices have sub-cent precision so integer-cents doesn't work either |
| `github.com/google/uuid` | UUIDv7 (RFC 9562) — time-sortable; `ORDER BY id` gives chronological order without a secondary column |
| `github.com/charmbracelet/bubbletea` | TUI framework (Phase 4) |
| `github.com/charmbracelet/lipgloss` | TUI styling (Phase 4) |
| `github.com/charmbracelet/bubbles` | TUI components: table, list, textinput (Phase 4) |
| `github.com/alecthomas/kong` | CLI subcommands + config (serve, migrate, version) |
| `github.com/stretchr/testify` | Test assertions |
| `log/slog` | Structured logging (stdlib, Go 1.21+) |
| `buf` CLI | Proto linting + code generation (dev tool, not a Go dep) |

---

## Implementation Sequence

**Phase 1 — Foundation**
1. `internal/domain/` — all types (no external deps)
2. `internal/repository/sqlite/` — schema, migrations, WAL setup, storage models, repo implementations
3. Repo unit tests using `:memory:` SQLite

**Phase 2 — Business Logic**
4. `internal/strategy/` — pure classifier + tests
5. `internal/importer/tastytrade/` and `internal/importer/schwab/`
6. `internal/service/import_service.go` — full pipeline
7. `internal/service/position_service.go` — lot tracking, FIFO matching, position cache
8. `internal/service/chain_service.go` — chain and chain_link management
9. `internal/service/analytics_service.go`

**Phase 3 — gRPC Transport**
10. Write proto files, run `buf generate`
11. `internal/grpc/` handlers
12. `cmd/trade-tracker-server/main.go` — wiring, HTTP/2 listener, signal handling

**Phase 4 — TUI Client**
13. `cmd/trade-tracker-tui/main.go` + `internal/tui/`

---

## Critical Files

- `internal/domain/instrument.go` — foundational types for all downstream packages
- `internal/domain/chain.go` — Chain/ChainLink structs drive the DB schema
- `internal/domain/position.go` — PositionLot/LotClosing are the source of truth for all P&L
- `internal/repository/sqlite/migrations/001_initial_schema.sql` — hardest to change later
- `internal/service/position_service.go` — FIFO lot matching logic
- `internal/strategy/classifier.go` — clean interface for adding strategies and isolated rule tests

---

## Verification

- `go build ./...` — compiles all packages
- `go test ./...` — all unit tests pass (domain types, strategy rules, importer parsers, lot matching)
- Start server: `./bin/trade-tracker-server serve --db ./test.db`
- Import a Tastytrade CSV: gRPC call to `ImportService.ImportCSV`
- Verify transactions, trades, lots, lot_closings, positions created correctly
- Verify strategy classification on known multi-leg trades
- Manually link a chain and verify chain P&L = sum of lot_closings
- Query `AnalyticsService.GetSymbolPnL` and verify P&L matches manual calculation
