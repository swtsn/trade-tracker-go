# Future Work

Items explicitly deferred from current phases. Revisit when the core system is stable.

## Phase 1 / Data Model
- **Corporate actions** — stock splits, mergers, spin-offs would require lot cost basis adjustments. No schema support yet.
- **Crypto** — asset class placeholder exists but no broker parsers or instrument handling.
- **Position audit log** — positions can change over their lifetime (reclassification, manual edits, roll detection). An append-only audit log of changes would make that history inspectable.
- **Pairs positions** — modeling two-legged positions across different underlyings (e.g. long AAPL / short MSFT) would be interesting but is very far out. Requires cross-underlying position grouping, which the current model does not support.

## Design Questions (revisit after ImportService is built)
- **Trade vs Position — overlapping concepts** — both `Trade` and `Position` carry `StrategyType` and `ChainID`. From a user perspective they can feel like the same thing. Key distinction: `Trade` is a fixed order event; `Position` is current holding state that changes as legs close. The redundancy in `StrategyType` is intentional (they can diverge after partial closes) but the boundary between what belongs on each entity, and which layer owns what, got muddy during ImportService design. Revisit once ImportService is built and the full data flow is visible end-to-end.
- **Strategy rule priority registration** — how rules are registered and whether priority needs to be explicit (currently implicit by slice order in `NewClassifier`). Revisit before Phase 3.

## Phase 2 / Business Logic
- **Strategy rule refactor** — the rule constructors in `internal/strategy/rules.go` are candidates for refactoring. No specific direction decided yet; revisit after more rules are in place and patterns emerge.
- **Richer `GetAccountSummary`** — currently returns only total realized P&L and symbols with open positions. Candidates for expansion: per-symbol breakdown, win rate, average P&L per trade, max drawdown, annualized return, and rolling performance windows. Design when there is enough data to validate which metrics are actually useful.
- **Complex calendar strategies** — ratio calendars, straddle swaps, strangle swaps, and similar multi-leg time-spread structures are not yet classified. Add rules once the core calendar/diagonal shapes are validated in production.
- **Classifier mutual-exclusion test** — the classifier's stated invariant is that rules must be mutually exclusive by construction, but this is only verified by inspection today. Investigate whether a property-based or exhaustive test can assert that no two rules match the same leg shape (e.g. using fuzzing or a synthetic corpus of known shapes).
- **Chain auto-detection** — automatically linking covered calls (and similar) to an existing chain when opened on an underlying with an active chain. Deferred in favor of manual linking only.
- **CoveredCall strategy reclassification** — when a call is sold against equity held in a prior order, the classifier sees only a single short call and correctly labels it CSP or Single. It cannot detect the covered relationship without position context (knowing an open long equity lot exists for the same underlying). For now, an operator must manually reclassify the trade. A future `StrategyService.Upgrade()` pass could inspect open lots and promote lone short calls to CoveredCall where a matching equity lot exists.
- **Roll auto-detection scoring** — the rule-scoring algorithm for detecting rolls from raw transaction data. Will be designed as part of the rolling phase.
- **Schwab CSV parser** — build a broker parser for Schwab trade history exports. See Phase 3 importer boundary in phase2.md for the parser contract.
- **API-based import** — Tastytrade and Schwab both have APIs. Day-one import is CSV only.
- **LIFO / average cost lot matching** — FIFO is the initial implementation. Other methods deferred.
- **Expiration action mapping** — decide whether Tastytrade `BTC`/`STC` + Sub Type "Expiration"
  rows map to `Action.EXPIRATION` or stay as `BTC`/`STC`; finalize in the Tastytrade parser.
- **ACAT initial position import** — Schwab `RAD` block on account transfer date contains
  pre-existing positions at $0 cost basis. Decide whether to import as $0-basis opening lots
  or skip; finalize in the Schwab parser.

## Supporting account balance

These items are deferred until account-level tracking is added. Currently the system is
concerned with positions and P&L only.

- **Fund transfers / ACAT** — cash and position transfers between brokers or accounts.
  Schwab: `CRC`/`JRN` rows in Cash Balance section. Tastytrade: not yet observed.
- **Regulatory fee adjustments** — small periodic regulatory fees (e.g., -$0.03) that adjust
  cash balance but aren't associated with any trade. Tastytrade: `Money Movement / Balance
  Adjustment` rows.

## Before gRPC / Before Production Use
- **Transaction propagation across service calls** — `CloseLots` commits each `CloseLot` repo call in its own DB transaction. If a failure occurs after one or more lots have committed but before `UpsertPosition` completes, `position.realized_pnl` will be permanently understated relative to the actual sum of `lot_closings.realized_pnl` for the instrument. This is not a bug in the happy path, but there is no automatic compensation on partial failure. Full atomicity requires passing a `*sql.Tx` through the repo layer (or a unit-of-work pattern) so that `ImportService` can wrap an entire trade group — lot closings, lot openings, and position refresh — in a single DB transaction. Address before production use. To detect existing drift:
  ```sql
  SELECT p.id, p.realized_pnl, COALESCE(SUM(lc.realized_pnl), 0) AS lot_sum
  FROM positions p
  LEFT JOIN position_lots pl ON pl.instrument_id = p.instrument_id
                             AND pl.account_id   = p.account_id
  LEFT JOIN lot_closings lc ON lc.lot_id = pl.id
  GROUP BY p.id
  HAVING p.realized_pnl != lot_sum;
  ```

## Performance / Scalability
- **Pre-computed analytics aggregates** — analytics queries currently aggregate over raw `lot_closings` and `position_lots` at read time. This will eventually not scale: a user with years of history and hundreds of chains will see slow dashboard loads. The right time to make this call is when the UX phase starts and real query patterns are visible. Options include materialized summary tables updated on import, a separate read model, or query-time caching at the gRPC layer. Don't pre-optimize before the access patterns are known.

## Infrastructure
- **PostgreSQL migration** — SQLite is the initial storage layer. If analytics performance becomes a concern at scale, PostgreSQL is the upgrade path.
- **Multi-user support** — currently designed as a personal single-user tool.
- **TLS on gRPC server** — plain TCP for local network use. Add TLS if exposed beyond local network.
