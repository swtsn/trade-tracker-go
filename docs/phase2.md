# Phase 2 — Business Logic

Build the strategy classifier and service layer. No gRPC, no broker file parsing. At the
end of this phase: a fully tested business logic layer that can import normalized
transactions, match lots, compute P&L, and classify strategies.

---

## Scope

- Strategy classifier: `internal/strategy/`
- Broker parsers: `internal/broker/tastytrade/`, `internal/broker/schwab/`
- Services: `internal/service/`
- All services operate exclusively on `domain.*` types — no storage models, no broker-specific
  formats cross this boundary

### Status

| Component | Status |
|---|---|
| Strategy classifier | ✅ done |
| Tastytrade parser | ✅ done |
| Schwab parser | ✅ done |
| Import service | ✅ done |
| Contract spec table | ✅ done |
| Position service | ✅ done |
| Chain service | ✅ done |
| Analytics service | 🔲 upcoming |

---

## Importer Boundary

The broker parsers are solely responsible for parsing a broker file and producing
`[]domain.Transaction`. The import service is solely responsible for everything after that.

```
Broker Parsers                Phase 2 Service Layer
──────────────────            ────────────────────────────────────────
tastytrade/parser.go  ──┐
schwab/parser.go      ──┤──► []domain.Transaction ──► ImportService.Import()
(future: gRPC client) ──┘
```

This separation means the import service is fully testable without file I/O, and the
parsers are fully testable without a database. If importers become background jobs calling
the gRPC API in the future, the boundary stays identical — the gRPC handler maps
proto → domain and calls `ImportService.Import()`.

---

## Strategy Classifier (`internal/strategy/`)

### Files

| File | Contents |
|---|---|
| `leg_shape.go` | `LegShape` struct; `FromTransactions()` normalizer |
| `classifier.go` | `Classifier`, `Rule` types; `NewClassifier()`, `Classify()` |
| `rules.go` | One function per strategy returning a `Rule` |
| `classifier_test.go` | Table-driven tests for every strategy + edge cases |

### `LegShape`

```go
// LegShape is a normalized view of one opening transaction leg.
// All classifier rules operate on []LegShape — never on domain.Transaction directly.
// Only opening legs are included (PositionEffectOpening). Closing legs are excluded.
type LegShape struct {
    AssetClass domain.AssetClass
    OptionType domain.OptionType   // "" for non-options
    Strike     decimal.Decimal     // zero for non-options
    Expiration time.Time           // zero for non-options
    Direction  int                 // +1 long (BTO/BUY), -1 short (STO/SELL)
    Quantity   decimal.Decimal     // always positive; sign is in Direction
}
```

### `Classifier`

```go
// Classifier checks rules in registration order and returns the first match.
// Rules must be mutually exclusive by construction — if two rules match the same
// leg shape that is a bug in the rules, not a tie to be resolved by priority.
// Returns StrategyUnknown if no rule matches — this is valid state, not an error.
type Classifier struct{ rules []Rule }

func NewClassifier() *Classifier
func (c *Classifier) Classify(legs []LegShape) domain.StrategyType

type Rule struct {
    Name  domain.StrategyType
    Match func(legs []LegShape) bool // pure function — no DB, no I/O, no side effects
}
```

### Strategy Rules

Rules are mutually exclusive by construction. The only structural overlap is
IronButterfly ⊂ IronCondor — resolved by requiring IronCondor's rule to assert
`shortPut.Strike != shortCall.Strike`, making the two shapes disjoint.

| Strategy | Legs | Key test |
|---|---|---|
| IronButterfly | 4 | put spread + call spread, same expiry, shortPut.Strike == shortCall.Strike |
| IronCondor | 4 | put spread + call spread, same expiry, shortPut.Strike != shortCall.Strike |
| BrokenHeartButterfly | 4 | all same option type, same expiry, 2 longs (outer) + 2 shorts (inner) at 4 distinct strikes |
| Butterfly | 3 | all same option type, same expiry, 3 strikes, equidistant wings, 1-2-1 directions |
| BrokenWingButterfly | 3 | all same option type, same expiry, 3 strikes, asymmetric wings, 1-2-1 directions |
| CoveredCall | 2 | equity long + call short, same order |
| Ratio | 2 | same option type + expiry, opposite directions, qty(short) > qty(long) |
| BackRatio | 2 | same option type + expiry, opposite directions, qty(long) > qty(short) |
| Straddle | 2 | put+call, same strike+expiry, same direction |
| Strangle | 2 | put+call, different strikes, same expiry, same direction |
| Vertical | 2 | same option type, same expiry, different strikes, opposite directions |
| Calendar | 2 | same option type + strike, different expiry, opposite directions |
| Diagonal | 2 | same option type, different strike + expiry, opposite directions |
| CSP | 1 | short put, equity option |
| Single | 1 | any single option |
| Stock | 1 | equity (non-option) |
| Future | 1 | future (non-option) |

---

## Adding a New Strategy

Follow these four steps — no other files need changing.

**Step 1** — Add the constant in `internal/domain/trade.go`:
```go
StrategyMyNew StrategyType = "my_new"
```

**Step 2** — Write the rule in `internal/strategy/rules.go`:
```go
func ruleMyNew() Rule {
    return Rule{
        Name:  domain.StrategyMyNew,
        Match: func(legs []LegShape) bool {
            // Pure function — no DB, no I/O, no side effects.
        },
    }
}
```

**Step 3** — Register the rule in `internal/strategy/classifier.go` `NewClassifier()`:
```go
func NewClassifier() *Classifier {
    return &Classifier{rules: []Rule{
        ...
        ruleMyNew(),
    }}
}
```

**Step 4** — Add a test in `internal/strategy/classifier_test.go`. Cover at minimum: an
exact match, a near-miss that should not match, and legs in a different order (rules must
be order-independent — sort before inspecting).

> **Rules must be pure functions.** If classification requires position context (e.g.,
> knowing there is an existing long stock lot to detect CoveredCall when only a call was
> sold), that logic lives in a future service layer upgrade step — not in the rule. The
> base classifier never touches the database.

---

## Import Service (`internal/service/import_service.go`)

Pipeline orchestration. Receives already-normalized domain transactions from a broker
parser and persists them.

```go
type ImportResult struct {
    Imported int          // trade groups fully persisted with all hooks successful
    Skipped  int          // transactions skipped due to duplicate BrokerTxID
    Failed   int          // trade groups where a DB write or hook failed
    Errors   []ImportError
}
// Invariant: Imported + Failed + Skipped == total input transaction groups (after dedup).

type ImportError struct {
    TradeID  string
    HookName string // empty for DB errors; set to hook name for hook errors
    Err      error
}

type PostImportHook struct {
    Name string
    Run  func(ctx context.Context, trade *domain.Trade, txns []domain.Transaction) error
}
```

**`Import(ctx, []domain.Transaction) (*ImportResult, error)`** — steps in order:

1. **Dedup** — single bulk `FilterExistingBrokerTxIDs` query; skip already-seen transactions
2. **Upsert instruments** — one upsert per unique `InstrumentID` across fresh transactions
3. **Group by `TradeID`** — preserving first-seen order; `TradeID` is set by the parser
4. **Per group:**
   - Classify strategy via `StrategyClassifier.Classify(strategy.FromTransactions(group))`
   - Create `Trade` record
   - Create `Transaction` records — closing legs first, then opening legs
   - Run post-import hooks (e.g. position updates); hook failure counts as Failed

A top-level error is returned only for fatal infrastructure failures (e.g. lost DB
connection). Per-group failures are recorded in `result.Errors` and processing continues.

**Known atomicity gap:** trade and transaction rows are written in separate DB calls with
no wrapping SQL transaction. A mid-group failure leaves an orphaned trade row. Drift
detection query:
```sql
SELECT t.id FROM trades t
LEFT JOIN transactions tx ON tx.trade_id = t.id
WHERE tx.id IS NULL;
```
Full fix deferred to repository-layer transaction propagation.

---

## Broker Parsers (`internal/broker/`)

Both parsers implement the `broker.Parser` interface:
```go
type Parser interface {
    Parse(r io.Reader, accountID string) ([]domain.Transaction, error)
}
```

**Tastytrade** (`tastytrade/parser.go`): Parses the "Transactions" CSV export. Supports
equities, equity options, and future options. Order # is used as the group key for
deterministic TradeID generation; rows without an Order # (expirations, etc.) each become
their own trade.

**Schwab** (`schwab/parser.go`): Parses the "Account Trade History" CSV export. Supports
stocks, ETFs, equity options, futures, and future options (including 32nds price notation
for Treasury futures). Multi-leg orders are identified by the leading `Exec Time` column —
continuation rows have an empty time field. Fees are not present in this export.

Both parsers:
- Generate deterministic `TradeID` and `BrokerTxID` via `brokerutil.HashKey` (SHA-256) so
  re-importing the same file produces identical IDs
- Return an error on malformed instrument data; short rows are silently skipped

---

## Testing

- `go test ./internal/strategy/...` — no DB required; all tests are pure function calls
- `go test ./internal/service/...` — uses `:memory:` SQLite

Key service test scenarios covered:
- Basic import: transactions persisted, `Imported` count correct
- Dedup: re-importing the same file yields `Skipped` count, no duplicates
- Strategy classification: vertical spread classified as `StrategyVertical`
- Closing-first ordering: closing legs written before opening legs
- Multiple trade groups in one import batch
- Post-import hook invocation
- Hook failure: group counted as `Failed`, not `Imported`
- Partial transaction failure: orphaned trade documented and tested

---

## Done When (phase 2 complete)

- `go build ./...` passes
- `go test ./...` passes
- All strategy shapes in the table above have at least one passing test
- ImportService processes manually-constructed domain transactions and persists correct
  trades and transactions
- Position service, chain service, and analytics service implemented and tested

---

## Chain Service (`internal/service/chain_service.go`)

Detects chains from the transaction log and back-fills `chain_id` onto lots and positions.
Reads from `trades`, `transactions`, `chains`, and `chain_links`. Writes `chain_id` onto
`position_lots` and `positions` via `PositionRepository`.

Two entry points:
- `ProcessTrade(ctx, tradeID)` — single-trade variant called in the import hook, immediately
  after `PositionService.ProcessTrade`. No orphan state: chain_id is stamped in the same
  import pass that creates the lots and position row.
- `DetectChains(ctx, accountID)` — full account scan for replaying chain detection over
  historical data. Must be run after position service has processed all trades.

### Chain lifecycle

- **Starts:** opening-only trade (all legs `PositionEffectOpening`) with no prior chain context.
- **Continues:** mixed trade (at least one closing + at least one opening leg) — a roll or adjustment.
- **Ends:** close-only trade AND no open balance remains in the chain.

### Attribution heuristic

`GetOpenChainForInstrument(accountID, instrumentID)` finds the open chain holding the
closing leg's instrument via transaction arithmetic: sums `quantity × direction_sign` across
all transactions linked to each chain and returns the chain with a net positive opening
balance for the instrument. No lot data is read.

### `DetectChains(ctx, accountID)`

Processes all trades chronologically. Per trade:

1. **Opening-only** → create `Chain` with `UnderlyingSymbol` from the first opening leg.
2. **Mixed** → resolve `chain_id` from closing legs via `GetOpenChainForInstrument` →
   create `ChainLink` (LinkType: roll/assignment/exercise).
3. **Close-only** → resolve `chain_id` from closing legs → record close `ChainLink` →
   call `ChainIsOpen`; if no open balance remains, stamp `chains.closed_at`.

Idempotent: `GetChainByTradeID` checks whether the trade already appears in
`chains.original_trade_id` or any `chain_links` row. Trades already assigned are skipped.

### Open/closed state

`ChainIsOpen` and `GetOpenChainForInstrument` both use **transaction arithmetic** —
summing `fill_price × quantity × direction_sign` across all transactions belonging to the
chain's trades. `position_lots.remaining_quantity` is not consulted.

### `chain_id` on `position_lots` and `positions`

The chain service writes `chain_id` onto both `position_lots` and `positions` via two
`PositionRepository` methods (`SetLotChainID`, `SetPositionChainID`) called during
`startChain` and `extendChain`. This requires `ChainService` to take a `PositionRepository`
dependency.

`position_lots.chain_id` — set on all lots opened by the chain's original trade, and on
any new lots opened by rolls/extensions. Used as the lot-level grouping key for portfolio
queries and lot-level P&L attribution.

`positions.chain_id` — set on the position row when a chain is assigned. Before chain
detection runs, positions are grouped by `originating_trade_id`; afterwards by `chain_id`.

The `chain_id` column on `transactions` was dropped in migration 008.

### `GetChainPnL`

Computes net realized P&L from transaction data: `SUM(fill_price × quantity × multiplier ×
direction_sign − fees)` across all transactions in the chain's trades (original + all link
trades). Uses a `UNION` (not `UNION ALL`) to deduplicate trade IDs, since a roll stores the
same trade in both `opening_trade_id` and `closing_trade_id` of its link.

## Position Service

### Lifecycle

A position has two states: **open** (at least one lot with `remaining_quantity != 0`) and
**closed** (all lots fully closed). There is no partially-closed state at the position level;
partial closes are a lot-level concern recorded in `lot_closings`.

### Identity

A position is **one row per chain, or one row per originating trade if unchained.**

- Chained: `positions.chain_id` is set; the position spans all trades in that chain.
- Unchained: `positions.chain_id` is NULL; the position is keyed on `originating_trade_id`.
  When `DetectChains` later assigns a chain to that trade, the position row is updated to
  set `chain_id`. Partial-uniqueness (`chain_id` when set; `originating_trade_id` when not)
  is enforced in the service layer, not by a DB constraint.

### `positions` table — migration 009

The current schema has `UNIQUE(account_id, instrument_id)` — one row per instrument. That
changes in migration 009:

**Columns removed:**
- `instrument_id` — belongs on `position_lots` only; a position spans multiple instruments
- `quantity` — ambiguous for multi-leg positions; per-leg quantities live on `position_lots`
- `UNIQUE(account_id, instrument_id)` constraint

**Columns added:**
- `underlying_symbol TEXT NOT NULL` — drives the symbol-level portfolio grouping
- `originating_trade_id TEXT NOT NULL REFERENCES trades(id)` — the trade that opened this
  position; the grouping key when unchained

**Columns unchanged:** `id`, `account_id`, `chain_id`, `cost_basis`, `realized_pnl`,
`opened_at`, `updated_at`, `closed_at`, `strategy_type`

`cost_basis` is the net debit or credit to establish the position: sum of
`open_price × |open_quantity| × multiplier × direction_sign + open_fees` across all opening
lots. Positive = net credit received; negative = net debit paid.

**New indexes:**
- `(account_id, underlying_symbol)` — symbol-level portfolio query
- `(account_id, chain_id)` — chain lookup
- `(account_id, originating_trade_id)` — unchained position lookup

Since nothing currently writes `positions`, migration 009 has no data to preserve.

### Portfolio view

Three query levels:

**Symbol summary** — one row per underlying symbol, all open positions:
```
underlying_symbol | open_positions | realized_pnl
SPY               | 2              | 340.00
AAPL              | 1              | 0.00
```

**Position list for a symbol** — one row per chain or trade under that symbol:
```
strategy    | cost_basis | realized_pnl | opened_at
Vertical    | -120.00    | 0.00         | 2025-01-10
ShortPut    |  340.00    | 0.00         | 2025-01-15
```

**Legs for a position** — open `position_lots` rows under the position:
```
instrument         | quantity | open_price | opened_at
SPY 450P Jan 2025  | -1       | 3.40       | 2025-01-15
SPY 445P Jan 2025  | +1       | 1.20       | 2025-01-15
```

### Import hook ordering

The position service and chain service are both called as post-import hooks per trade,
in sequence:

```
ImportService.Import()
  └─ per trade group:
       1. persist Trade + Transactions
       2. PositionService.ProcessTrade(ctx, tradeID, txns)  ← lots + position row
       3. ChainService.ProcessTrade(ctx, tradeID, txns)     ← chain + back-fill chain_id
```

`ChainService.ProcessTrade` is a single-trade variant of `DetectChains`. It processes one
trade in isolation: creates or extends a chain, then immediately back-fills `chain_id` onto
the lots and position row just written by the position service. No orphan state.

`DetectChains(ctx, accountID)` (the full account scan) is retained for replaying chain
detection over historical data.

### Responsibilities

`PositionService.ProcessTrade(ctx context.Context, tradeID string, txns []domain.Transaction) error`

The import service passes the already-persisted trade ID and its transactions. The position
service processes each leg in the order the import service delivers them (closing legs first,
then opening legs — the import service already guarantees this ordering).

**On opening transaction (BTO, STO, BUY):**
1. Create `PositionLot`:
   - `open_quantity = remaining_quantity = signed quantity` (negative for short)
   - `open_price = fill_price`, `open_fees = fees`, `opened_at = executed_at`
2. Upsert `Position`: look up by `originating_trade_id = tradeID` (chain_id is NULL at
   this point). If none exists, insert with `underlying_symbol` derived from the
   instrument, `cost_basis` from this leg, `opened_at = executed_at`. If one exists,
   add this leg's contribution to `cost_basis` and update `updated_at`.

`cost_basis` per leg = `CashFlowSign(action) × fill_price × |quantity| × multiplier − fees`

**On closing transaction (BTC, STC, SELL, EXPIRATION):**
1. FIFO: load open lots for `(account_id, instrument_id)` ordered by `opened_at ASC`
2. Walk lots oldest-first, consuming `remaining_quantity` until the closing quantity is satisfied
3. Per lot consumed: compute `realized_pnl` and write a `LotClosing`; decrement
   `remaining_quantity`; if it reaches zero, stamp `lot.closed_at`
4. Locate the position via the lot's `chain_id` (if set) or `trade_id`, then accumulate
   `realized_pnl` on the position. If all lots under the position are now closed, stamp
   `position.closed_at`.

**Realized P&L per lot closing:**
```
close_cf   = CashFlowSign(close_action) × close_price × closed_qty × multiplier
open_cf    = CashFlowSign(open_action)  × open_price  × closed_qty × multiplier
open_fees_prorated = lot.open_fees × (closed_qty / |lot.open_quantity|)

realized_pnl = close_cf + open_cf − close_fees − open_fees_prorated
```
Opening fees are pro-rated by the fraction of the lot being closed. Closing fees are
charged in full to the closing event.

**On assignment / exercise:**
- Option lot closed as above
- New equity or futures lot opened; linked via `LotClosing.ResultingLotID`
- New lot inherits `chain_id` from the closing lot (if set)

**Expiration** is a close at `close_price = 0`. No special handling beyond action type.

### Chain service — single-trade variant

`ChainService.ProcessTrade(ctx, tradeID)` mirrors the per-trade logic inside `DetectChains`:
classifies the trade (opening/mixed/close-only), applies the appropriate chain action, then
back-fills `chain_id` via:

```go
// SetLotChainID stamps chain_id on all lots whose opening_tx_id is in openingTxIDs.
SetLotChainID(ctx context.Context, openingTxIDs []string, chainID string) error

// SetPositionChainID sets chain_id on the position whose originating_trade_id matches.
SetPositionChainID(ctx context.Context, originatingTradeID, chainID string) error
```

Both methods are added to `PositionRepository`. `ChainService` gains a `PositionRepository`
dependency.

---

## Initial Import — Special Handling

TODO: The initial import (bulk load of historical data into an empty DB) needs dedicated
design discussion before implementation. Key questions include ordering, lot-match
correctness across the full history, and chain detection sequencing.

---

## Analytics Service (`internal/service/analytics_service.go`) — Next

Aggregates realized P&L and win-rate statistics from `lot_closings` and `positions`. No
writes — read-only queries. All monetary outputs use `decimal.Decimal`.

### Entry points

```go
type AnalyticsService struct {
    db *sql.DB  // direct query access; no repo indirection needed for read-only aggregates
}

// GetSymbolPnL returns total realized P&L for a symbol over a date range.
func (s *AnalyticsService) GetSymbolPnL(ctx context.Context, accountID, symbol string, from, to time.Time) (decimal.Decimal, error)

// GetPnLSummary returns aggregate stats for an account over a date range.
func (s *AnalyticsService) GetPnLSummary(ctx context.Context, accountID string, from, to time.Time) (*PnLSummary, error)

// GetStrategyPerformance returns per-strategy win rate and average P&L.
func (s *AnalyticsService) GetStrategyPerformance(ctx context.Context, accountID string, from, to time.Time) ([]StrategyStats, error)

// GetWinRate returns the fraction of closed positions with realized_pnl > 0.
func (s *AnalyticsService) GetWinRate(ctx context.Context, accountID string, from, to time.Time) (decimal.Decimal, error)
```

### Output types

```go
type PnLSummary struct {
    TotalRealized   decimal.Decimal
    TotalFees       decimal.Decimal
    NetPnL          decimal.Decimal   // TotalRealized − TotalFees
    WinRate         decimal.Decimal   // fraction 0–1
    PositionsClosed int
}

type StrategyStats struct {
    StrategyType  domain.StrategyType
    Count         int
    WinRate       decimal.Decimal
    AveragePnL    decimal.Decimal
    TotalPnL      decimal.Decimal
}
```

### Query design

- `GetSymbolPnL`: `SUM(lc.realized_pnl)` joining `lot_closings` → `position_lots` →
  `instruments` filtered on `symbol` and `lc.closed_at` within range.
- `GetPnLSummary`: aggregate `lot_closings` for the account and date range; win rate =
  positions with `SUM(realized_pnl) > 0` / total closed positions in range.
- `GetStrategyPerformance`: group by `trades.strategy_type`; join through
  `lot_closings.closing_tx_id` → `transactions.trade_id` → `trades.strategy_type`.
- `GetWinRate`: count distinct closed positions (via `positions.closed_at`) where
  `realized_pnl > 0` divided by total closed.

### Testing

All tests use `:memory:` SQLite. Seed a small set of trades, lots, and lot_closings;
assert aggregate outputs. Cover:
- Single closed position, profit
- Single closed position, loss
- Mix: win rate = 0.5
- Multiple strategies
- Date range filtering (exclude out-of-range closings)
