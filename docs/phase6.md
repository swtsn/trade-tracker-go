# Phase 6 — Post-MVP Analytics

Per-symbol and per-strategy P&L analytics, deferred from Phase 3 (MVP) to keep the initial
server surface minimal.

---

## Scope

Two new RPCs on `AnalyticsService`, both already implemented in `internal/service/analytics_service.go`
and exposed via the `Analytics` interface. Phase 6 only adds the proto definitions and handlers;
no service or repository changes are expected.

| RPC | Service method |
|---|---|
| `GetSymbolPerformance` | `AnalyticsService.GetSymbolPnL` |
| `GetStrategyPerformance` | `AnalyticsService.GetStrategyPerformance` |

---

## RPCs

### GetSymbolPerformance

```
rpc GetSymbolPerformance(GetSymbolPerformanceRequest) returns (GetSymbolPerformanceResponse);
```

Request: `account_id`, `symbol`, `from`, `to`
Response: `realized_pnl` (decimal string)

Maps to `Analytics.GetSymbolPnL`. Returns net realized P&L for one underlying symbol
over a date range (e.g. all SPY trades closed between Jan and Apr).

### GetStrategyPerformance

```
rpc GetStrategyPerformance(GetStrategyPerformanceRequest) returns (GetStrategyPerformanceResponse);
```

Request: `account_id`, `from`, `to`
Response: repeated `StrategyStats` — one entry per strategy type

Maps to `Analytics.GetStrategyPerformance`. Each `StrategyStats` entry includes:
`strategy_type`, `count`, `win_rate`, `average_pnl`, `total_pnl` (all decimal strings).

---

## Done When

- `analytics.proto` extended with both RPCs
- `AnalyticsHandler` implements both new RPCs (injected interface already satisfies them)
- Handler tests cover response mapping and error paths
- `make test` passes
