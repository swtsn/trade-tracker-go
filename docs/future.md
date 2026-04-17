# Future Work

Items explicitly deferred from current phases. Revisit when the core system is stable.

## Phase 1 / Data Model
- **Corporate actions** — stock splits, mergers, spin-offs would require lot cost basis adjustments. No schema support yet.
- **Crypto** — asset class placeholder exists but no broker parsers or instrument handling.
- **Position audit log** — positions can change over their lifetime (reclassification, manual edits, roll detection). An append-only audit log of changes would make that history inspectable.
- **Pairs positions** — modeling two-legged positions across different underlyings (e.g. long AAPL / short MSFT) would be interesting but is very far out. Requires cross-underlying position grouping, which the current model does not support.

## Phase 2 / Business Logic
- **Strategy rule refactor** — the rule constructors in `internal/strategy/rules.go` are candidates for refactoring. No specific direction decided yet; revisit after more rules are in place and patterns emerge.
- **Complex calendar strategies** — ratio calendars, straddle swaps, strangle swaps, and similar multi-leg time-spread structures are not yet classified. Add rules once the core calendar/diagonal shapes are validated in production.
- **Classifier mutual-exclusion test** — the classifier's stated invariant is that rules must be mutually exclusive by construction, but this is only verified by inspection today. Investigate whether a property-based or exhaustive test can assert that no two rules match the same leg shape (e.g. using fuzzing or a synthetic corpus of known shapes).
- **Chain auto-detection** — automatically linking covered calls (and similar) to an existing chain when opened on an underlying with an active chain. Deferred in favor of manual linking only.
- **CoveredCall strategy reclassification** — when a call is sold against equity held in a prior order, the classifier sees only a single short call and correctly labels it CSP or Single. It cannot detect the covered relationship without position context (knowing an open long equity lot exists for the same underlying). For now, an operator must manually reclassify the trade. A future `StrategyService.Upgrade()` pass could inspect open lots and promote lone short calls to CoveredCall where a matching equity lot exists.
- **Chain service closed-chain skip** — question: what is the cheapest way to skip closed chains during `DetectChains`? Options include an in-memory set of closed chain IDs loaded at startup or a covering index on `chains(closed_at)`. Needs investigation.
- **Chain P&L incremental update** — chain P&L is currently computed on read via transaction arithmetic (sum across all transactions in the chain's trades). A performance improvement would be to maintain a running total on the `chains` row, updated each time a chain link is created, avoiding the full aggregation query on every read.
- **Roll auto-detection scoring** — the rule-scoring algorithm for detecting rolls from raw transaction data. Will be designed as part of the rolling phase.
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

## Infrastructure
- **Analytics data access layer** — `AnalyticsService` currently holds a raw `*sql.DB` and issues queries inline, bypassing the repository abstraction used everywhere else. As query complexity grows this will become hard to test and maintain in isolation. Refactor to a dedicated `AnalyticsRepository` interface (or a set of read-model query methods on existing repositories) so that the service layer stays free of SQL and the queries can be tested or swapped independently.

- **PostgreSQL migration** — SQLite is the initial storage layer. If analytics performance becomes a concern at scale, PostgreSQL is the upgrade path.
- **Multi-user support** — currently designed as a personal single-user tool.
- **TLS on gRPC server** — plain TCP for local network use. Add TLS if exposed beyond local network.
