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

## Proto Layout (`proto/tradetracker/v1/`)

One file per domain area. All messages use `string` for decimal values (matches the
`shopspring/decimal` TEXT storage convention) and `google.protobuf.Timestamp` for times.

### `import.proto`

```protobuf
service ImportService {
  rpc ImportTransactions(ImportTransactionsRequest) returns (ImportTransactionsResponse);
}

message ImportTransactionsRequest {
  string account_id = 1;
  string broker     = 2;       // "tastytrade" | "schwab"
  bytes  csv_data   = 3;       // raw CSV bytes
}

message ImportTransactionsResponse {
  int32 imported = 1;
  int32 skipped  = 2;
  int32 failed   = 3;
  repeated ImportError errors = 4;
}

message ImportError {
  string trade_id  = 1;
  string hook_name = 2;  // empty for trade processing errors
  string message   = 3;
}
```

### `trade.proto`

```protobuf
service TradeService {
  rpc GetTrade(GetTradeRequest)     returns (GetTradeResponse);
  rpc ListTrades(ListTradesRequest) returns (ListTradesResponse);
}

message Trade {
  string trade_id                    = 1;
  string account_id                  = 2;
  string broker                      = 3;
  string strategy_type               = 4;
  string underlying_symbol           = 5;
  google.protobuf.Timestamp opened_at = 6;
  google.protobuf.Timestamp closed_at = 7;  // unset if open
}

message ListTradesRequest {
  string account_id          = 1;
  int32  page_size           = 2;   // 0 = server default (50)
  string page_token          = 3;
  // Filters — all optional; multiple filters are ANDed.
  google.protobuf.Timestamp from          = 4;  // opened_at >=
  google.protobuf.Timestamp to            = 5;  // opened_at <=
  string                    strategy_type = 6;  // exact match; empty = all
  string                    symbol        = 7;  // underlying symbol; empty = all
}

message ListTradesResponse {
  repeated Trade trades         = 1;
  string         next_page_token = 2;  // empty when no further pages
}
```

`underlying_symbol` is stored as a denormalized column on the `trades` table (added in a
new migration). It is populated by `ImportService` from the first transaction's instrument
symbol at import time, eliminating any join at query time.

### `position.proto`

```protobuf
service PositionService {
  rpc GetPosition(GetPositionRequest)   returns (GetPositionResponse);
  rpc ListPositions(ListPositionsRequest) returns (ListPositionsResponse);
  rpc ListLots(ListLotsRequest)         returns (ListLotsResponse);
}

message Position {
  string position_id          = 1;
  string account_id           = 2;
  string chain_id             = 3;
  string originating_trade_id = 4;
  string underlying_symbol    = 5;
  string strategy_type        = 6;
  string cost_basis           = 7;   // decimal string
  string realized_pnl         = 8;   // decimal string
  google.protobuf.Timestamp opened_at = 9;
  google.protobuf.Timestamp closed_at = 10;  // unset if open
}

message ListPositionsRequest {
  string account_id  = 1;
  int32  page_size   = 2;
  string page_token  = 3;
  bool   open_only   = 4;   // false = return all positions
}

message PositionLot {
  string lot_id            = 1;
  string trade_id          = 2;
  string instrument_id     = 3;
  string underlying_symbol = 4;
  string open_quantity     = 5;   // decimal string; negative = short
  string remaining_quantity = 6;
  string open_price        = 7;
  string open_fees         = 8;
  google.protobuf.Timestamp opened_at = 9;
  google.protobuf.Timestamp closed_at = 10;
}

message ListLotsRequest {
  string position_id = 1;   // lots are returned for the position's originating_trade_id
  // open_only is implicitly true for open positions; for closed positions all lots
  // are returned (remaining_quantity = 0 for all of them by definition).
}
```

### `analytics.proto`

```protobuf
service AnalyticsService {
  rpc GetSymbolPnL(GetSymbolPnLRequest)                      returns (GetSymbolPnLResponse);
  rpc GetPnLSummary(GetPnLSummaryRequest)                    returns (GetPnLSummaryResponse);
  rpc GetStrategyPerformance(GetStrategyPerformanceRequest)  returns (GetStrategyPerformanceResponse);
  rpc GetWinRate(GetWinRateRequest)                          returns (GetWinRateResponse);
}

message GetSymbolPnLRequest {
  string account_id                = 1;
  string symbol                    = 2;
  google.protobuf.Timestamp from   = 3;
  google.protobuf.Timestamp to     = 4;
}

message GetSymbolPnLResponse {
  string realized_pnl = 1;  // decimal string
}

message GetPnLSummaryRequest {
  string account_id                = 1;
  google.protobuf.Timestamp from   = 2;
  google.protobuf.Timestamp to     = 3;
}

message GetPnLSummaryResponse {
  string realized_pnl      = 1;
  string close_fees        = 2;
  string win_rate          = 3;
  int32  positions_closed  = 4;
}

message GetStrategyPerformanceRequest {
  string account_id                = 1;
  google.protobuf.Timestamp from   = 2;
  google.protobuf.Timestamp to     = 3;
}

message GetStrategyPerformanceResponse {
  repeated StrategyStats stats = 1;
}

message StrategyStats {
  string strategy_type = 1;
  int32  count         = 2;
  string win_rate      = 3;
  string average_pnl   = 4;
  string total_pnl     = 5;
}

message GetWinRateRequest {
  string account_id                = 1;
  google.protobuf.Timestamp from   = 2;
  google.protobuf.Timestamp to     = 3;
}

message GetWinRateResponse {
  string win_rate = 1;  // decimal string; 0–1
}
```

### `chain.proto`

```protobuf
service ChainService {
  rpc GetChain(GetChainRequest)       returns (GetChainResponse);
  rpc ListChains(ListChainsRequest)   returns (ListChainsResponse);
}

message Chain {
  string chain_id           = 1;
  string account_id         = 2;
  string underlying_symbol  = 3;
  string original_trade_id  = 4;
  google.protobuf.Timestamp created_at = 5;
  google.protobuf.Timestamp closed_at  = 6;  // unset if open
}

message ChainLink {
  string link_id           = 1;
  string chain_id          = 2;
  int32  sequence          = 3;
  string link_type         = 4;  // "roll" | "assignment" | "exercise" | "close"
  string closing_trade_id  = 5;
  string opening_trade_id  = 6;
  string credit_debit      = 7;  // decimal string; positive = credit
  string strike_change     = 8;  // decimal string; zero for non-rolls
  int32  expiration_change_days = 9;
}

message GetChainResponse {
  Chain            chain = 1;
  repeated ChainLink links = 2;
}

message ListChainsRequest {
  string account_id        = 1;
  string underlying_symbol = 2;  // empty = all underlyings
  bool   open_only         = 3;
  int32  page_size         = 4;
  string page_token        = 5;
}
```

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
