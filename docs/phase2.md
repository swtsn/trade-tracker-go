# Phase 2 — Business Logic

Build the strategy classifier and service layer. No gRPC, no broker file parsing. At the
end of this phase: a fully tested business logic layer that can import normalized
transactions, match lots, compute P&L, and classify strategies.

---

## Scope

- Strategy classifier: `internal/strategy/`
- Services: `internal/service/`
- All services operate exclusively on `domain.*` types — no storage models, no broker-specific
  formats cross this boundary

---

## Importer Boundary

The importer (Phase 3) is solely responsible for parsing a broker file and producing
`[]domain.Transaction`. The import service is solely responsible for everything after that.

```
Phase 3 Importer              Phase 2 Service Layer
──────────────────            ────────────────────────────────────────
tastytrade/parser.go  ──┐
schwab/parser.go      ──┤──► []domain.Transaction ──► ImportService.Import()
(future: gRPC client) ──┘    []domain.MoneyMovement ► ImportService.ImportMoneyMovements()
```

This separation means the import service is fully testable without file I/O, and the
importer is fully testable without a database. If importers become background jobs calling
the gRPC API in the future, the boundary stays identical — the gRPC handler maps
proto → domain and calls `ImportService.Import()`.

---

## Strategy Classifier (`internal/strategy/`)

### Files

| File | Contents |
|---|---|
| `leg_shape.go` | `LegShape` struct; `FromTransactions()` normalizer |
| `classifier.go` | `Classifier`, `Rule` types; `New()`, `Classify()` |
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
// Classifier applies rules in ascending Priority order (lower number = checked first).
// Returns StrategyUnknown if no rule matches — this is valid state, not an error.
type Classifier struct{ rules []Rule }

func New() *Classifier
func (c *Classifier) Classify(legs []LegShape) domain.StrategyType

type Rule struct {
    Name     domain.StrategyType
    Priority int                        // lower = checked sooner; more specific rules get lower numbers
    Match    func(legs []LegShape) bool // pure function — no DB, no I/O, no side effects
}
```

### Strategy Rules (priority order, lower = checked first)

| Priority | Strategy | Legs | Key test |
|---|---|---|---|
| 5 | IronButterfly | 4 | IC shape + shortPut.Strike == shortCall.Strike |
| 10 | IronCondor | 4 | put spread + call spread, same expiry |
| 15 | CallButterfly | 3 | all calls, same expiry, equidistant strikes, 1-2-1 directions |
| 15 | PutButterfly | 3 | all puts, same expiry, equidistant strikes, 1-2-1 directions |
| 20 | CoveredCall | 2 | equity long + call short |
| 25 | BackRatio | 2 | same type + expiry + direction, different quantities |
| 30 | Straddle | 2 | put+call, same strike+expiry, same direction |
| 30 | Strangle | 2 | put+call, different strikes, same expiry, same direction |
| 30 | CallVertical | 2 | 2 calls, same expiry, different strikes, opposite directions |
| 30 | PutVertical | 2 | 2 puts, same expiry, different strikes, opposite directions |
| 35 | Calendar | 2 | same type+strike, different expiry, opposite directions |
| 35 | Diagonal | 2 | same type, different strike+expiry, opposite directions |
| 40 | CSP | 1 | put, equity_option, short |
| 45 | Single | 1 | any option |
| 45 | Stock | 1 | equity |
| 45 | Future | 1 | future (non-option) |

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
        Name:     domain.StrategyMyNew,
        Priority: 25, // set relative to existing rules; lower = checked first
        Match: func(legs []LegShape) bool {
            // Pure function — no DB, no I/O, no side effects.
            // Return true if legs match this strategy shape.
            // Use helper functions like allSameExpiry(), allOptions(),
            // countByOptionType(), sortByStrike() from rules.go.
        },
    }
}
```

**Step 3** — Register the rule in `internal/strategy/classifier.go` `New()`:
```go
func New() *Classifier {
    rules := []Rule{
        ...
        ruleMyNew(),
    }
    // Slice is sorted by Priority at construction — order here doesn't matter.
    ...
}
```

**Step 4** — Add a test in `internal/strategy/classifier_test.go`:
```go
{
    name:   "MyNew — exact match",
    legs:   []LegShape{ /* ... */ },
    expect: domain.StrategyMyNew,
},
{
    name:   "MyNew — not matched when similar but wrong structure",
    legs:   []LegShape{ /* ... */ },
    expect: domain.StrategyUnknown,
},
```

Tests should cover at minimum: an exact match, a near-miss that should not match, and
legs in a different order (rules must be order-independent — sort before inspecting).

> **Rules must be pure functions.** If classification requires position context (e.g.,
> knowing there is an existing long stock lot to detect CoveredCall when only a call was
> sold), that upgrade step lives in a future `StrategyService.Upgrade()` — not in the
> rule. The base classifier never touches the database.

---

## Services (`internal/service/`)

### `import_service.go`

Pipeline orchestration. Receives already-normalized domain transactions.

```go
type ImportResult struct {
    Imported int
    Skipped  int           // duplicates by BrokerTxID
    Errors   []ImportError
}

type ImportError struct {
    BrokerTxID string
    Err        error
}
```

**`Import(ctx, []domain.Transaction) (*ImportResult, error)`** — steps in order:
1. Dedup: `txRepo.ExistsByBrokerTxID` — skip already-seen transactions
2. Upsert instruments for all non-duped transactions
3. Group non-duped transactions by `TradeID` (pre-grouped by the importer)
4. For each trade group:
   a. Classify strategy: `classifier.Classify(strategy.FromTransactions(group))`
   b. Create Trade record
   c. Create Transaction records
   d. For each opening leg: `positionSvc.OpenLot(tx)`
   e. For each closing leg: `positionSvc.CloseLots(tx)`
5. `positionSvc.RefreshPosition` for each affected (accountID, instrumentID)

**`ImportMoneyMovements(ctx, []domain.MoneyMovement) (*ImportResult, error)`** — deduplicates
by `BrokerEventID` and persists.

Trade grouping is the importer's responsibility. Each `domain.Transaction` arrives with
`TradeID` already set to a consistent value for co-legs of the same order.

### `position_service.go`

Lot state management. Called by `ImportService`; not called directly from outside.

**`OpenLot(ctx, tx domain.Transaction) (*domain.PositionLot, error)`**
Creates a new `PositionLot`. Signed quantity: BTO/BUY = positive, STO/SELL = negative.

**`CloseLots(ctx, tx domain.Transaction) ([]domain.LotClosing, error)`**
FIFO-matches a closing transaction against open lots for (accountID, instrumentID).
Returns one `LotClosing` per lot consumed. Updates lot remaining quantities.
Also updates the position's `RealizedPnL` incrementally.

P&L formula per closed lot:
```
multiplier = instrument multiplier (1 for equity, 100 for equity_option, etc.)
proportion = closedQty / |openQuantity|   // for prorating open fees

long  lot: pnl = (closePrice - openPrice) × closedQty × multiplier − closeFees − openFees×proportion
short lot: pnl = (openPrice - closePrice) × closedQty × multiplier − closeFees − openFees×proportion
```

**`RefreshPosition(ctx, accountID, instrumentID string) error`**
Recalculates and upserts the materialized `Position`:
- `Quantity` = sum of `remaining_quantity` across all open lots (signed)
- `CostBasis` = sum of (`remaining_quantity × open_price`) across all open lots
- `RealizedPnL` unchanged (accumulated incrementally by `CloseLots`)

### `chain_service.go`

Manual chain lifecycle management. Chain linking is never automatic.

```go
CreateChain(ctx, accountID, broker, underlyingSymbol, originalTradeID string) (*domain.Chain, error)
AddLink(ctx, chainID string, link domain.ChainLink) (*domain.ChainLink, error)  // auto-sequences
CloseChain(ctx, chainID string) error
GetPnL(ctx, chainID string) (domain.PnL, error)  // sum of lot_closings.realized_pnl for chain's lots
```

### `analytics_service.go`

Aggregation over persisted data. Unrealized P&L requires market prices, which are not
tracked in Phase 2 — `Unrealized` is always 0.

```go
GetSymbolPnL(ctx, accountID, symbol string) (*SymbolPnL, error)
GetStrategyPerformance(ctx, accountID string) ([]StrategyPerformance, error)
GetAccountSummary(ctx, accountID string) (*AccountSummary, error)
```

---

## Testing

- `go test ./internal/strategy/...` — no DB required; all tests are pure function calls
- `go test ./internal/service/...` — use `:memory:` SQLite (same pattern as repo tests)

Key service test scenarios:
- 3 opening transactions → 3 open lots created, positions upserted
- 1 closing transaction → FIFO matches oldest lot, lot_closing created, P&L correct
- Closing transaction spanning 2 lots → partial close of lot[0], full close of lot[1]
- Duplicate BrokerTxID on re-import → skipped, no error, Skipped count = 1
- Chain P&L = sum of lot_closings.realized_pnl

---

## Done When

- `go build ./...` passes
- `go test ./internal/strategy/... ./internal/service/...` passes
- All strategy shapes in the table above have at least one passing test
- ImportService can process a set of manually-constructed domain transactions and produce
  correct lots, lot_closings, and positions
