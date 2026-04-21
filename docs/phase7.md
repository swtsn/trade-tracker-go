# Phase 7 — Post-MVP Analytics ✅

Per-symbol and per-strategy P&L analytics, deferred from Phase 3 (MVP) to keep the initial
server surface minimal.

---

## Scope

| RPC | Service method | Status |
|---|---|---|
| `GetSymbolPerformance` | `AnalyticsService.GetSymbolPnL` | ✅ |
| `GetStrategyPerformance` | `AnalyticsService.GetStrategyPerformance` | ✅ |

---

## Done

- `analytics.proto` extended with both RPCs and message types
- `AnalyticsHandler` accepts `Analytics` interface (superset of the former `AccountSummaryReader`);
  implements `GetSymbolPerformance` and `GetStrategyPerformance` with full input validation
  including inverted-range checks
- Handler tests cover response mapping, missing field validation, inverted range, and empty results
- `AccountSummaryReader` interface removed (dead code after handler interface change)
- `make test` passes
