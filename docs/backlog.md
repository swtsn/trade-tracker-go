# Backlog
Items explicitly deferred from current phases.

## Unprioritized Backlog

### Data Model

- **Corporate actions** — stock splits, mergers, spin-offs would require lot cost basis adjustments. No schema support yet.
- **Crypto** — asset class placeholder exists but no broker parsers or instrument handling.
- **Position audit log** — positions can change over their lifetime (reclassification, manual edits, roll detection). An append-only audit log of changes would make that history inspectable.
- **Pairs positions** — modeling two-legged positions across different underlyings (e.g. long AAPL / short MSFT) would be interesting but is very far out. Requires cross-underlying position grouping, which the current model does not support.

### Business Logic

- **Trade purpose / playbook classification** — a user-defined label applied to a position or chain that captures *why* the trade was put on, orthogonal to the auto-detected strategy type. Examples: "income ladder", "long earnings straddle", "LEAPS hedge". Strategy type is derived mechanically from the leg structure; trade purpose is a higher-level intent that only the user knows.

  Design notes:
  - Purposes are user-created strings (initially free-form; possibly a managed list later). No fixed enum — what counts as a "purpose" will vary by user.
  - The most natural attachment point is the **chain**, since a purpose typically describes a recurring pattern across rolls and adjustments. But a position-level label may also be useful for one-off trades that never grow into a chain.
  - The UI flow (TUI initially): user selects a chain or position and assigns a purpose label inline, similar to how chain linking works today. Purposes should be quick to assign — ideally a single keypress on a pre-existing label or a short type-ahead input.
  - Analytics wiring: `GetSymbolPerformance` and `GetStrategyPerformance` (Phase 7) should gain a `GetPurposePerformance` counterpart. The analytics dashboard needs a fourth panel (after account summary, per-symbol, per-strategy) breaking down P&L and win rate by purpose.
  - Storage: a `purposes` reference table (id, name, created_at) and a `chain_purpose_id` FK on `chains` (nullable). A chain can have at most one purpose; a purpose can apply to many chains. Position-level labeling can be deferred until it's clear the chain-level attachment is insufficient.

- **Strategy rule refactor** — the rule constructors in `internal/strategy/rules.go` are candidates for refactoring. No specific direction decided yet; revisit after more rules are in place and patterns emerge.
- **Complex calendar strategies** — ratio calendars, straddle swaps, strangle swaps, and similar multi-leg time-spread structures are not yet classified. Add rules once the core calendar/diagonal shapes are validated in production.
- **Classifier mutual-exclusion test** — the classifier's stated invariant is that rules must be mutually exclusive by construction, but this is only verified by inspection today. Investigate whether a property-based or exhaustive test can assert that no two rules match the same leg shape (e.g. using fuzzing or a synthetic corpus of known shapes).
- **`computeStrikeExpChange` DST comment** — in `internal/service/chain_service.go`, the expiration-change day arithmetic truncates both timestamps to UTC midnight before subtracting, which makes DST a non-issue. This is not obvious and should get a one-line comment confirming it once the function is next touched.
- **Chain auto-detection** — automatically linking covered calls (and similar) to an existing chain when opened on an underlying with an active chain. Deferred in favor of manual linking only.
- **Manual chain stitching** — when a mixed trade (close+open) is imported with no matching open chain (e.g. first import starting mid-history), `ChainService` starts a new chain from the opening legs and logs a warning. The closing legs' P&L remains unattributed. Two recovery paths are needed: (1) a UI or admin RPC to manually merge two chains (stitching the orphaned chain onto the original), and (2) re-running `DetectChains` after importing earlier history that contains the original opening trade. For path (2), `DetectChains` needs to handle the case where the trade already has a chain (currently idempotent-skipped) but that chain was started in error — possibly by comparing chain `created_at` against imported trades whose `opened_at` predates the chain, to detect retroactive attribution opportunities.
- **Strategy re-derivation from live lot state** — a position's strategy is burned in from the opening trade and never revised. For positions that are rolled significantly over their lifetime (e.g. a single put that becomes a vertical, or a short call added to existing stock), the stored strategy type will lag the actual current shape. `strategy.FromLots()` already converts open lots to leg shapes; a periodic or on-demand re-classification pass could compare the current lot shape against the stored strategy and update if it has changed. A specific case: when a call is sold against equity held in a prior order, the classifier sees only a single short call (CoveredCall cannot be detected without position context). A re-classification pass could inspect open lots and promote lone short calls to CoveredCall where a matching equity lot exists for the same underlying.
- **Chain service closed-chain skip** — question: what is the cheapest way to skip closed chains during `DetectChains`? Options include an in-memory set of closed chain IDs loaded at startup or a covering index on `chains(closed_at)`. Needs investigation.
- **Chain P&L incremental update** — chain P&L is currently computed on read via transaction arithmetic (sum across all transactions in the chain's trades). A performance improvement would be to maintain a running total on the `chains` row, updated each time a chain link is created, avoiding the full aggregation query on every read.
- **`GetWinRate` implementation** — `GetWinRate` currently delegates to `GetPnLSummary`, which issues two DB queries (lot closings for P&L/fees, positions for win-rate counts) and discards most of the result. `GetWinRate` only needs the positions query. Refactor so each analytics method issues only the queries it requires, rather than composing through a heavier aggregate method.
- **Roll auto-detection scoring** — the rule-scoring algorithm for detecting rolls from raw transaction data. Will be designed as part of the rolling phase.
- **API-based import** — Tastytrade and Schwab both have APIs. Day-one import is CSV only.
- **LIFO / average cost lot matching** — FIFO is the initial implementation. Other methods deferred.
- **Expiration action mapping** — decide whether Tastytrade `BTC`/`STC` + Sub Type "Expiration"
  rows map to `Action.EXPIRATION` or stay as `BTC`/`STC`; finalize in the Tastytrade parser.
- **ACAT initial position import** — Schwab `RAD` block on account transfer date contains
  pre-existing positions at $0 cost basis. Decide whether to import as $0-basis opening lots
  or skip; finalize in the Schwab parser.

### Account Balance

These items are deferred until account-level tracking is added. Currently the system is
concerned with positions and P&L only.

- **Fund transfers / ACAT** — cash and position transfers between brokers or accounts.
  Schwab: `CRC`/`JRN` rows in Cash Balance section. Tastytrade: not yet observed.
- **Regulatory fee adjustments** — small periodic regulatory fees (e.g., -$0.03) that adjust
  cash balance but aren't associated with any trade. Tastytrade: `Money Movement / Balance
  Adjustment` rows.

### TUI

- **Server-side error surfacing** — the TUI has no structured way to surface server-side warnings or non-fatal errors to the user. The server logs these (e.g. skipped transactions, missing position rows) but the TUI client only receives gRPC status codes. A clean solution would propagate actionable server warnings through the RPC response (e.g. a repeated `warnings` field on affected responses) so the TUI can display them inline rather than silently dropping them.

### Admin / Operations

- **Admin API** — long-term, an admin section is needed for operational tasks that fall outside the normal user-facing API. Initial candidate: ad-hoc chain detection (`ChainService.DetectChains`) for backfilling or reprocessing an account's history. Other candidates include manual trade reclassification and position recalculation. These should be separate RPCs (or a separate service) with restricted access, not mixed into the user-facing API.

### Infrastructure

- **CloseLot / UpdatePosition atomicity** — `PositionService.processClosing` calls `CloseLot` (inserts a `lot_closings` row and updates `remaining_quantity`) and then `accumulatePnL` (calls `UpdatePosition`) as two separate DB operations with no enclosing transaction. A crash between them leaves a lot permanently closed while `positions.realized_pnl` is never updated — silently wrong totals with no audit trail. The fix requires transaction-scoped repository operations (`BeginTx` propagation on `Repos`). To detect divergence before the fix lands, run:
  ```sql
  SELECT lc.lot_id, SUM(lc.realized_pnl) AS lot_pnl, p.realized_pnl AS pos_pnl
  FROM lot_closings lc
  JOIN position_lots pl ON pl.id = lc.lot_id
  JOIN positions p ON p.chain_id = pl.chain_id
  GROUP BY lc.lot_id, p.realized_pnl
  HAVING ABS(lot_pnl - pos_pnl) > 0.001;
  ```

- **`internal/testutil` shared test helpers** — `openTestDB` is duplicated across `internal/service` and `internal/repository/sqlite` test packages. As more packages need DB-backed tests, this will drift further. Consolidate into an `internal/testutil` package (e.g. `testutil.OpenRepos(t)`) so schema changes and helper logic only need updating in one place.

- **Analytics data access layer** — `AnalyticsService` currently holds a raw `*sql.DB` and issues queries inline, bypassing the repository abstraction used everywhere else. As query complexity grows this will become hard to test and maintain in isolation. Refactor to a dedicated `AnalyticsRepository` interface (or a set of read-model query methods on existing repositories) so that the service layer stays free of SQL and the queries can be tested or swapped independently.

- **Data migration runner** — as bugs are fixed and new contract multipliers are added (futures, futures options), it will become untenable to start from scratch each import. A migration runner would allow reprocessing existing data in-place — re-running parsing, lot matching, or chain detection steps against already-imported transactions without requiring a full re-import. Design should account for partial reruns (e.g. re-derive multipliers only) and idempotency.

- **PostgreSQL migration** — SQLite is the initial storage layer. If analytics performance becomes a concern at scale, PostgreSQL is the upgrade path.
- **Multi-user support** — currently designed as a personal single-user tool.
- **TLS on gRPC server** — plain TCP for local network use. Add TLS if exposed beyond local network.
