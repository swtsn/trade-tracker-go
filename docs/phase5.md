# Phase 5 — TUI Client

Build the terminal UI client. At the end of this phase: a `trade-tracker-tui` binary
that runs on macOS, connects to the gRPC server over the local network, and provides
the full set of views described below.

Prerequisite: Phase 4 (server is deployed and manually verified on real data).

---

## Scope

- TUI binary: `cmd/trade-tracker-tui/`
- TUI package: `internal/tui/`
- Bubbletea app with view routing, table/list components, and input forms
- All views listed below

| Component | Status |
|---|---|
| TUI binary + server connection config | 🔲 |
| Account selector (startup screen) | 🔲 |
| Open positions view | 🔲 |
| Historic positions view | 🔲 |
| Position detail / chain drill-down | 🔲 |
| Trades audit view | 🔲 |
| Analytics dashboard | 🔲 |
| CSV import flow | 🔲 |

---

## Views

### Account selector

Shown at startup if no `--account` flag is passed. Calls `AccountService.ListAccounts`,
displays broker + account number, user selects one. Selected account is used for all
subsequent queries.

### Open positions view

Table of open positions for the selected account. Columns:
`Symbol | Strategy | Quantity | Cost Basis | Unrealized P&L | Opened`

Calls `PositionService.ListPositions(open_only=true)`. Sorted by `opened_at` descending.
Enter on a row navigates to position detail.

### Historic positions view

Same as open positions but `open_only=false`, filtered to closed positions. Shows
`Realized P&L` instead of unrealized.

### Position detail / chain drill-down

Shows position metadata at top, then chain timeline below (if the position is linked to
a chain). Chain timeline is sourced from `ChainService.GetChain`: one row per `ChainLink`
showing link type (ROLL / ASSIGNMENT / EXERCISE), date, strike change, and credit/debit.
Net chain P&L at the bottom.

Keybindings: `l` to link chain, `u` to unlink chain (calls `PositionService.LinkChain` /
`UnlinkChain`).

### Trades audit view

Paginated table of all trades. Calls `TradeService.ListTrades` with optional filters:
`account_id`, `from`, `to`, `symbol`, `strategy_type`. Filter bar toggled with `/`.
Sorted by `opened_at` descending.

### Analytics dashboard

Three panels, tab-navigable:
1. **Account summary** — total realized P&L, unrealized P&L, win rate, total trades
2. **Per-symbol** — table of symbol + P&L + trade count, sourced from `GetSymbolPerformance`
3. **Per-strategy** — table of strategy type + P&L + win rate, sourced from `GetStrategyPerformance`

### CSV import flow

File picker (or typed path), broker selector (Tastytrade / Schwab), account selector.
On confirm: reads file, base64-encodes, calls `ImportService.ImportTransactions`.
Progress shown via streaming response. Summary (imported / skipped / errors) on completion.

---

## Architecture

```
internal/tui/
├── app.go           # root Bubbletea model, view routing, key dispatch
├── model.go         # shared state (selected account, active view, pagination cursors)
├── views/
│   ├── accounts.go
│   ├── positions.go
│   ├── position_detail.go
│   ├── trades.go
│   ├── analytics.go
│   └── import.go
└── client/
    └── client.go    # thin wrapper around generated gRPC client stubs
```

The `client/` wrapper owns the `grpc.ClientConn` and exposes typed methods so views
don't import generated proto packages directly. This keeps the view layer testable with
a fake client.

---

## Client CLI

```
trade-tracker-tui [--addr localhost:50051] [--account ACCOUNT_ID]
```

`--addr` defaults to `localhost:50051`. `--account` skips the account selector screen.
Both accept environment variables: `TRADE_TRACKER_ADDR`, `TRADE_TRACKER_ACCOUNT`.

---

## Build

```makefile
# Build TUI for host platform
build-tui:
	go build -o bin/trade-tracker-tui ./cmd/trade-tracker-tui

# Cross-compile TUI for macOS (darwin/arm64)
release-client:
	GOOS=darwin GOARCH=arm64 go build -o bin/trade-tracker-tui-darwin-arm64 ./cmd/trade-tracker-tui
```

---

## Done When

- `make release-client` produces a darwin/arm64 binary
- All views listed in the scope table are implemented
- TUI connects to the Phase 4 server and all views display real data
- CSV import flow works end-to-end from the TUI
- `go test ./...` passes
