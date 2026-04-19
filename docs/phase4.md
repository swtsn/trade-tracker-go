# Phase 4 — Verification & Deployment

Design and implement the build and deployment strategy, then manually verify the full
system end-to-end against a real SQLite database before the TUI is built.

At the end of this phase: a server binary that can be built for Ubuntu, started with a
single command locally for manual testing, deployed as a systemd service on a remote
Ubuntu host, and fully exercised via `buf curl` with real broker CSV data.

---

## Deployment Target

- **Server**: Ubuntu (linux/amd64) running as a systemd service. SQLite db on local disk.
- **Client**: macOS laptop (darwin/arm64). TUI binary (Phase 5) connects over local network.
- **Storage**: Single SQLite file. gRPC server is the sole accessor.
- **Scale**: Personal single-user tool; no multi-tenancy, no TLS required for local network.

---

## Scope

| Component | Status |
|---|---|
| Server CLI (`kong`: `serve`, `migrate`, `version`) | 🔲 |
| Graceful shutdown (SIGTERM drain) | 🔲 |
| Structured startup logging (`slog`) | 🔲 |
| `make run` local dev target | 🔲 |
| `make release-server` (linux/amd64 cross-compile) | 🔲 |
| `deploy/trade-tracker.service` systemd unit | 🔲 |
| `deploy/install.sh` server setup script | 🔲 |
| End-to-end manual verification (real CSV, real db) | 🔲 |

---

## Server CLI

The server binary uses `kong` for subcommand dispatch. Three subcommands:

```
trade-tracker-server serve   [--addr :50051] [--db PATH]
trade-tracker-server migrate [--db PATH]
trade-tracker-server version
```

`serve` auto-runs migrations at startup before opening the listener — no separate
migration step needed in normal operation. `migrate` is provided for manual use and
deployment scripts. Both default `--db` to `~/.trade-tracker/data.db`.

Config priority: flag > environment variable > default. Environment variable names:
`TRADE_TRACKER_ADDR`, `TRADE_TRACKER_DB`.

### Startup sequence (`serve`)

1. Parse flags / env
2. Ensure data directory exists (`os.MkdirAll`)
3. Open SQLite (`PRAGMA journal_mode=WAL; synchronous=NORMAL; foreign_keys=ON`)
4. Run migrations (`golang-migrate` embedded SQL)
5. Wire services and handlers
6. Open TCP listener
7. Log "listening on :50051" via `slog`
8. Block on `grpc.Server.Serve`; trap SIGTERM/SIGINT → `GracefulStop`

---

## Makefile Targets

```makefile
# Local dev — runs server with ./data/dev.db, creating the directory if needed
run:
	mkdir -p data
	go run ./cmd/trade-tracker-server serve --db ./data/dev.db

# Cross-compile server for Ubuntu deployment
release-server:
	GOOS=linux GOARCH=amd64 go build -o bin/trade-tracker-server-linux-amd64 ./cmd/trade-tracker-server

# Build for host platform (dev/CI)
build:
	go build -o bin/trade-tracker-server ./cmd/trade-tracker-server
```

`make run` is the single command needed for local manual verification — no environment
setup, no Docker, no migrations to run separately.

---

## Deployment Artifacts (`deploy/`)

### `deploy/trade-tracker.service`

```ini
[Unit]
Description=Trade Tracker gRPC server
After=network.target

[Service]
Type=simple
User=trade-tracker
ExecStart=/opt/trade-tracker/trade-tracker-server serve
Environment=TRADE_TRACKER_DB=/var/lib/trade-tracker/data.db
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

### `deploy/install.sh`

Idempotent setup script for a fresh Ubuntu host:

1. Create system user `trade-tracker` (no login shell)
2. Create `/opt/trade-tracker/` and `/var/lib/trade-tracker/` with correct ownership
3. Copy `bin/trade-tracker-server-linux-amd64` → `/opt/trade-tracker/trade-tracker-server`
4. Copy `deploy/trade-tracker.service` → `/etc/systemd/system/`
5. `systemctl daemon-reload && systemctl enable trade-tracker && systemctl start trade-tracker`

On subsequent deploys: copy new binary + `systemctl restart trade-tracker`.

---

## Local Verification Protocol

Run `make run` to start a server against `./data/dev.db`. Then verify each service:

### Import

```bash
# Import a Tastytrade CSV
buf curl --data '{"broker":"tastytrade","csv_data":"<base64>","account_id":"<id>"}' \
  http://localhost:50051/tradetracker.v1.ImportService/ImportTransactions

# Verify import job recorded
buf curl --data '{"account_id":"<id>"}' \
  http://localhost:50051/tradetracker.v1.ImportService/ListImportHistory
```

### Accounts

```bash
buf curl http://localhost:50051/tradetracker.v1.AccountService/ListAccounts
```

### Trades

```bash
buf curl --data '{"account_id":"<id>"}' \
  http://localhost:50051/tradetracker.v1.TradeService/ListTrades
```

### Positions

```bash
buf curl --data '{"account_id":"<id>","open_only":true}' \
  http://localhost:50051/tradetracker.v1.PositionService/ListPositions
```

### Analytics

```bash
buf curl --data '{"account_id":"<id>"}' \
  http://localhost:50051/tradetracker.v1.AnalyticsService/GetPnLSummary
```

### Chain linking

```bash
# Link two trades into a chain (manual)
buf curl --data '{"trade_id":"<id>","chain_id":"<id>"}' \
  http://localhost:50051/tradetracker.v1.PositionService/LinkChain

# Verify chain P&L
buf curl --data '{"chain_id":"<id>"}' \
  http://localhost:50051/tradetracker.v1.ChainService/GetChain
```

### Verification checklist

- [ ] Tastytrade CSV import: transaction count matches CSV row count minus header
- [ ] Schwab CSV import: strategy classification correct on known multi-leg trades
- [ ] Dedup: re-importing same CSV produces zero new records
- [ ] Open positions reflect net quantity across all lots
- [ ] Closed position P&L matches manual calculation from fills
- [ ] Chain P&L = sum of lot_closings.realized_pnl for that chain_id
- [ ] Analytics win rate matches manually counted wins/losses
- [ ] `buf curl` against all services returns valid proto responses

---

## Done When

- `make run` starts a server with no prerequisites beyond a Go toolchain
- `make release-server` produces a linux/amd64 binary
- `deploy/install.sh` sets up a fresh Ubuntu host end-to-end
- All items in the verification checklist pass against real broker CSV data
- `go test ./...` still passes
