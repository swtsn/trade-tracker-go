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
| Position service | 🔲 upcoming |
| Chain service | 🔲 upcoming |
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

## Position Service — Upcoming

The position service (lot tracking, FIFO matching, P&L) will be designed and implemented
as the next step in phase 2. No design is recorded here yet.
