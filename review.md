# Code Review — `local-dev` vs `main`

Reviewer: Claude Code (claude-sonnet-4-6)
Date: 2026-04-12
Scope: All files added/changed on `local-dev` relative to `main`.

---

## 1. Quality

### 🟡 Medium — `CloseLots` proration divides by `tx.Quantity` not `lot.OpenQuantity.Abs()`

File: `internal/service/position_service.go`, line 84

```go
closeFees := tx.Fees.Mul(closedQty).Div(tx.Quantity)
```

This is correct for prorating closing fees across lots (each lot gets `closedQty/totalCloseQty` of the fee), and it matches the documented comment on line 83. However, the variable name `closeFees` is used for the prorated slice passed to `calcPnL`, while `calcPnL` also deducts a prorated share of the *opening* fees (`openFeesPortion`). The naming is internally consistent but the two fee-proration formulas use different denominators on purpose — this is not immediately obvious and should have an inline note explaining why `tx.Quantity` is the correct denominator here (not `absRemaining` or `lot.OpenQuantity`).

**nit** — The proportional-open-fee computation lives inside `calcPnL` rather than at the call site in `CloseLots`, making it hard to see the full cost picture in one place. Consider moving the open-fee prorating to the call site alongside the close-fee proration.

---

### 🟡 Medium — Migration version gap (4 is missing) is silently accepted

File: `internal/repository/sqlite/db.go`, lines 63–68

Versions jump from `3` to `5`. There is no migration `004_*.sql`. The migration runner does not enforce sequential or contiguous version numbering, so a future developer could add a `004` that will be silently applied out of order if an existing DB already has version 5. A comment explaining the deliberate gap (or a guard that prevents out-of-order application) would prevent confusion and accidental schema corruption.

---

### nit — `signedQty` uses `default:` to mean "long"

File: `internal/service/position_service.go`, lines 230–237

`ActionAssignment` and `ActionExercise` fall into the `default` branch, silently producing a positive quantity. The comment acknowledges this, but the `default` branch is still a risk: any new action value (e.g. a broker-specific action) that happens to pass through `OpenLot` will silently produce a long lot instead of returning an error. Enumerate the long actions explicitly (`ActionBTO`, `ActionBuy`, `ActionAssignment`, `ActionExercise`) and return an error for unrecognized actions.

---

### nit — `PnL` value object in `domain/pnl.go` is unused

File: `internal/domain/pnl.go`

`PnL` and `NetRealized()` are defined but have no call sites. Either wire this into the service layer or remove it to avoid dead code drift.

---

### nit — `ListOpenLotsByInstrument` filters on string `'0'`, not numeric zero

File: `internal/repository/sqlite/position_repo.go`, line 157–158

```sql
WHERE ... AND pl.remaining_quantity != '0'
```

`remaining_quantity` is stored as `TEXT` (decimal string). SQLite will compare this as a text value. `"-0"`, `"0.0"`, `"0.00"` would all slip through the filter and appear as "open" lots. This is safe today only because `shopspring/decimal.String()` always produces `"0"` for zero. Add a comment documenting this invariant, or store using a canonical check (e.g., `CAST(remaining_quantity AS REAL) != 0`).

---

### nit — `model.Instrument.ToDomain()` reads `multiplier` but ignores it for equities

File: `internal/repository/sqlite/model/instrument.go`, lines 42–44

`multiplier` is parsed unconditionally for all asset classes but only used when `inst.Option != nil`. For equities and futures it is parsed and discarded. This is harmless but wasteful; the parse could be moved inside the `switch` cases where it is needed.

---

## 2. Correctness

### 🔴 Critical — `calcPnL` uses `lot.OpenQuantity.Abs()` as the proportion denominator, but the correct denominator after a partial close is `lot.RemainingQuantity.Abs()` (the original open qty)

Wait — re-reading: `OpenQuantity` is the original quantity at lot creation and is never mutated. `RemainingQuantity` shrinks with each close. The formula is:

```
proportion = closedQty / |OpenQuantity|
openFeesPortion = OpenFees × proportion
```

This is correct for **first close**: it gives the right fraction of opening fees.
This is **incorrect for a second partial close of the same lot**: if a lot opens at 5, closes 3 (proportion = 3/5), then closes 2 more (proportion = 2/5), the total open-fees attribution = 3/5 + 2/5 = 5/5 = 100%. So across multiple partial closes the sum is correct.

However — the same `OpenFees` field is used for each computation. If the first partial close deducts 3/5 of the opening fees, the next close will also compute from `OpenFees` (the original full fee), not the *remaining* unattributed portion. The math still totals correctly across all closings only because the proportions sum to 1. **This is correct as written.** No bug here; remove this entry from the critical list.

---

### 🔴 Critical — `CloseLots` is non-atomic across multiple lots: partial DB writes on failure leave data inconsistent

File: `internal/service/position_service.go`, lines 61–139

The comment on line 57–60 documents this, but it is worth marking critical in a review: if `CloseLots` processes lot A successfully (`CloseLot` commits its own transaction) and then `GetPosition` or `UpsertPosition` fails for lot B (lines 129–137), the DB now has:
- Lot A: `remaining_quantity` decremented, `lot_closings` row inserted ✓
- Position `realized_pnl`: not updated ✗

The position's cumulative `RealizedPnL` is permanently inconsistent with its `lot_closings` history. There is no compensating repair path. This should be tracked as a known defect with a reconciliation query defined (e.g., re-sum `lot_closings.realized_pnl` for the instrument to detect drift). The existing comment only mentions future atomicity as a deferral; it should also specify the failure mode clearly.

---

### 🟡 Medium — `RefreshPosition` computes `CostBasis` using `RemainingQuantity × OpenPrice`, not `ClosedQuantity`-adjusted cost

File: `internal/service/position_service.go`, lines 154–160

```go
costBasis = costBasis.Add(lot.RemainingQuantity.Mul(lot.OpenPrice))
```

For a short lot with `RemainingQuantity = -3` and `OpenPrice = 1.50`, cost basis contributes `-4.50`. This is consistent with the short position semantics (negative cost basis for short positions), and `TestRefreshPosition_ShortOption` verifies it. This is intentional and correct as long as downstream consumers understand negative cost basis = short premium received. **No bug**, but there is no domain-level documentation on what `CostBasis` means for short positions (it is a signed value, unlike the conventional definition).

---

### 🟡 Medium — `CloseLots` requires `tx.Quantity` to be strictly positive, but callers using `ActionSell` (not `ActionSTC`) could pass a negative quantity from broker data

File: `internal/service/position_service.go`, lines 69–72

```go
toClose := tx.Quantity
if toClose.IsZero() || toClose.IsNegative() {
    return nil, fmt.Errorf("close lots: closing quantity must be positive, got %s", toClose)
}
```

There is no upstream normalization shown in this diff that guarantees `tx.Quantity` is always positive for closing transactions. If a broker CSV parser passes in a signed (negative) sell quantity without stripping the sign, `CloseLots` will return a confusing "closing quantity must be positive" error rather than a clear validation message. This is a latent bug when the import layer is added.

---

### 🟡 Medium — `strangle` rule does not check whether put strike is below call strike

File: `internal/strategy/rules.go`, lines 388–409

`ruleStrangle` accepts any two-leg same-direction put+call with different strikes and the same expiry. A "guts" strangle (put strike > call strike) is included by design per the test comment on line 381. This is fine as a product decision, but the rule name `StrategyStrangle` covers both conventional and inverted (guts) strangles without distinction. The test at line 381 documents this. No bug — but the type system does not distinguish them.

---

### 🟡 Medium — `ListOpenLotsByInstrument` has no secondary sort key — FIFO tie-breaking is non-deterministic for same-timestamp lots

File: `internal/repository/sqlite/position_repo.go`, lines 156–159

```sql
ORDER BY pl.opened_at ASC
```

The header comment in `position_service.go` line 52 says "id ASC breaks ties", but the actual SQL has no secondary sort. If two lots share the same `opened_at` (which is plausible for multi-leg orders executed simultaneously), the ordering is non-deterministic. Add `ORDER BY pl.opened_at ASC, pl.id ASC` to match the documented contract.

---

### nit — `RefreshPosition` sets `ClosedAt` based on `totalQty.IsZero()` but does not consider mixed-sign lots

File: `internal/service/position_service.go`, lines 186–190

If somehow an account accumulated both long and short lots for the same instrument (e.g. due to the acknowledged non-atomicity), `totalQty` could be zero while individual lots still have non-zero `RemainingQuantity`. `ClosedAt` would be set incorrectly. This is an edge case but worth a defensive check: `ClosedAt` should only be set when `len(lots) == 0` (no open lots), not just when the sum is zero.

---

## 3. Security

### nit — SQLite `TEXT` primary keys and foreign keys are compared case-sensitively, but UUIDs are mixed-case-safe

No SQL injection risk: all queries use `?` placeholders throughout. No raw string interpolation was observed in any query.

### nit — SHA-256 instrument IDs (`InstrumentID()`) are hex-encoded but not length-validated on read

File: `internal/domain/instrument.go`, lines 57–75

The ID is always 64 hex chars when generated by this code, but `GetByID` accepts any caller-supplied string. If a caller constructs an invalid ID, the DB simply returns `ErrNotFound` — that is safe. No issue.

---

## 4. Best Practices

### 🟡 Medium — `uuid.New()` is used for lot/closing IDs rather than UUIDv7

File: `internal/service/position_service.go`, lines 33, 103

The project uses UUIDv7 for all primary keys per the project description ("All primary keys are UUIDv7"). `uuid.New()` generates UUIDv4. This creates inconsistency: lots and lot closings will have non-time-sortable IDs while trades, accounts, etc. use v7. If `github.com/google/uuid` supports v7 (`uuid.NewV7()`), use it here. If not, add the `google/uuid` v1.6+ import that exposes `uuid.New7()` or switch to a library that does.

---

### 🟡 Medium — `CloseLots` does not verify that FIFO lot signs are consistent with the closing action

File: `internal/service/position_service.go`, lines 75–118

If an account has only short lots (negative `RemainingQuantity`) and a closing transaction arrives with `ActionBTC` (which is the correct close for a short), `CloseLots` will correctly consume short lots because `absRemaining = lot.RemainingQuantity.Abs()` strips the sign. However, if `ActionSell` is passed as the closing action for an existing long lot (which would be correct — STC), `CloseLots` will also consume it without checking whether the lot's direction (long/short) matches the closing action's direction. There is no guard preventing closing a long lot with a "buy-to-close" action, which would represent an incorrect trade record. Consider asserting that the lot sign is consistent with the closing direction.

---

### 🟡 Medium — `ruleRatio` / `ruleBackRatio` require `allSameExpiry` but real ratio spreads can span expirations (diagonal ratio)

File: `internal/strategy/rules.go`, lines 326–363

This is a deliberate scope limitation, but it is not commented. A 1x2 ratio spread where the two short legs are at a different expiry from the long leg (a valid real-world trade) will fall through to `StrategyUnknown`. Add a comment explaining this is by design and that cross-expiry ratios are deferred.

---

### nit — `TestCloseLots_SpansTwoLots` does not verify the position's `RealizedPnL` after spanning

File: `internal/service/position_service_test.go`, lines 318–363

The test verifies lot A is closed, lot B is partially closed, and lot quantities are correct, but does not assert the final position `RealizedPnL`. Since `CloseLots` updates `RealizedPnL` incrementally, this leaves the most important output of the multi-lot path untested.

---

### nit — `TestCloseLots_InsufficientOpenQuantity` does not verify that no DB state changed

File: `internal/service/position_service_test.go`, lines 365–383

The test checks that an error is returned, but does not assert the lot's `RemainingQuantity` is unchanged. Because the failure occurs before any `CloseLot` call (the loop never executes), this is fine in practice — but the test should verify the lot is still fully open to make the safety guarantee explicit and guard against future regressions.

---

### nit — `makeTx` helper uses `decimal.NewFromFloat` for price/fees

File: `internal/service/position_service_test.go`, line 108–111

Test helpers use `float64` → `decimal.NewFromFloat`, which has the usual float representation issues for values like `1.50`. For a financial test helper, use `decimal.RequireFromString("1.50")` or `mustDecimal("1.50")` (already defined in the test file) to avoid any float precision surprises in edge-case amounts.

---

### nit — `FuturesExpiryMonth` stored as RFC3339 but parsed with RFC3339 in `model/instrument.go`

File: `internal/repository/sqlite/model/instrument.go`, line 76

`FuturesExpiryMonth` is stored as a full RFC3339 timestamp (`2006-01-...T00:00:00Z`) even though conceptually it is just a month. The `InstrumentID()` hash in `domain/instrument.go` formats it as `"2006-01"`. If the expiry month field is ever set from a source that provides day or time precision, the storage format and the hash format will diverge, causing a hash collision or ID mismatch. Standardize to a single format (either store as `"2006-01"` or hash as `time.RFC3339`).

---

*End of review.*
