# Phase 3 — gRPC Server

Build the gRPC API layer. No TUI yet. At the end of this phase: a running server binary
that accepts connections, exposes all service-layer capabilities over ConnectRPC, and can
be exercised end-to-end with `buf curl` or a generated client.

---

## Scope

- Proto definitions: `proto/tradetracker/v1/`
- Generated code: `gen/tradetracker/v1/` (committed; regenerated via `make proto`)
- gRPC handlers: `internal/grpc/`
- Server binary: `cmd/trade-tracker-server/`
- buf toolchain: `buf.yaml`, `buf.gen.yaml`

### Status

| Component | Status |
|---|---|
| `underlying_symbol` migration + denormalization | ✅ |
| Service interface extraction | 🔲 |
| buf toolchain setup | 🔲 |
| `import.proto` + handler | 🔲 |
| `trade.proto` + handler | 🔲 |
| `position.proto` + handler | 🔲 |
| `analytics.proto` + handler | 🔲 |
| `chain.proto` + handler | 🔲 |
| Server binary wiring | 🔲 |
| End-to-end smoke test | 🔲 |

---

## Transport

**Raw gRPC** (`google.golang.org/grpc`) over plain TCP. The server registers handlers
directly on a `grpc.Server`; no HTTP mux layer.

Server listens on a configurable address (default `localhost:50051`). No TLS in Phase 3 —
plain TCP for local network use only (tracked in `future.md`).

### CSV import transport

CSV files are sent as raw `bytes` in the request message. Files are ≤ 1 MB in practice,
well under gRPC's default 4 MB message limit, and the TUI runs on a different machine so a
shared filesystem path is not an option. Streaming upload would add protocol complexity
with no benefit at this size.

---

## Handler Layer (`internal/grpc/`)

Each handler file wraps one service. Handlers are thin: validate inputs, call the service,
map domain types → proto response types. No business logic lives here.

```
internal/grpc/
├── server.go            # grpc.Server setup, service registration, Serve()
├── import_handler.go    # calls broker parser + ImportService.Import()
├── trade_handler.go     # calls TradeService (read-only queries)
├── position_handler.go  # calls PositionService (read-only queries)
├── analytics_handler.go # calls AnalyticsService
└── chain_handler.go     # calls ChainService
```

### Import handler note

The import handler is the most complex: it receives raw CSV bytes, dispatches to the
correct broker parser based on `broker` field, then calls `ImportService.Import()`. The
parser selection is a simple switch; no registry needed at this scale.

```go
func (h *ImportHandler) ImportTransactions(ctx context.Context,
    req *connect.Request[v1.ImportTransactionsRequest]) (*connect.Response[v1.ImportTransactionsResponse], error) {

    txns, err := h.parse(req.Msg.Broker, req.Msg.CsvData)
    // ...
    result, err := h.importSvc.Import(ctx, txns)
    // ...map result → proto response
}
```

---

## Server Binary (`cmd/trade-tracker-server/`)

```go
func main() {
    db, _ := sqlite.Open(flagDBPath)
    repos := sqlite.NewRepos(db)

    chainSvc     := service.NewChainService(repos.Chains, repos.Trades, repos.Transactions)
    positionSvc  := service.NewPositionService(repos.Positions)
    importSvc    := service.NewImportService(..., chainSvc,
        service.PostImportHook{Name: "position", Run: positionSvc.ProcessTrade})
    analyticsSvc := service.NewAnalyticsService(db)

    srv := grpc.NewServer()
    // register handlers
    srv.Serve(lis)
}
```

---

## buf Toolchain

```yaml
# buf.yaml
version: v2
modules:
  - path: proto

# buf.gen.yaml
version: v2
plugins:
  - remote: buf.build/protocolbuffers/go
    out: gen
    opt: paths=source_relative
  - remote: buf.build/grpc/go
    out: gen
    opt: paths=source_relative
```

`make proto` runs `buf generate`. Generated files are committed so the build does not
require buf to be installed in CI.

---

## Pagination

List RPCs use cursor-based pagination (opaque `page_token` string, `page_size` int).
The token encodes the last-seen row's sort key (typically `(opened_at, id)` as a
base64-encoded JSON pair). Server default page size: 50. Max: 200.

This is consistent with AIP-158 and avoids the offset-pagination anomalies that affect
`LIMIT/OFFSET` queries on live data.

---

## Error Mapping

Domain errors map to Connect status codes in a central helper:

| Domain error | Connect code |
|---|---|
| `domain.ErrNotFound` | `connect.CodeNotFound` |
| `domain.ErrDuplicate` | `connect.CodeAlreadyExists` |
| validation errors | `connect.CodeInvalidArgument` |
| everything else | `connect.CodeInternal` |

---

## Service Interfaces

The service layer currently exposes concrete structs (`*ImportService`, `*PositionService`,
etc.). Handlers must depend on interfaces so they can be tested with fakes. Each service
gets a matching interface in `internal/service/interfaces.go`:

```go
type Importer interface {
    Import(ctx context.Context, txns []domain.Transaction) (*ImportResult, error)
}

type PositionProcessor interface {
    ProcessTrade(ctx context.Context, tradeID string, txns []domain.Transaction, chainID string) error
    // read-only query methods used by the position handler
}

type Analytics interface {
    GetSymbolPnL(...)      (decimal.Decimal, error)
    GetPnLSummary(...)     (*PnLSummary, error)
    GetStrategyPerformance(...) ([]StrategyStats, error)
    GetWinRate(...)        (decimal.Decimal, error)
}
```

The concrete service structs satisfy these interfaces implicitly; no changes to the service
implementations are needed beyond the interface declarations.

---

## Testing

- Handler unit tests: inject fake implementations of the service interfaces, assert proto
  response shape and error mapping.
- Integration tests: spin up a real `grpc.Server` with `:memory:` SQLite, dial via a
  generated client. One integration test per RPC covers the full stack.
- No test hits the network; no mocking of the DB layer.

---

## Done When

- `make build` produces `bin/trade-tracker-server`
- All five services are reachable via `buf curl` against a running server
- `go test ./...` passes including handler tests
- Server wires up all Phase 2 services correctly at startup
