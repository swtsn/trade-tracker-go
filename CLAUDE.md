# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Common Commands

**Build the application:**
```bash
go build -o bin/trade-tracker ./cmd/trade-tracker
```

**Run the application:**
```bash
./bin/trade-tracker
```

**Run tests:**
```bash
go test ./...
```

**Run a single test:**
```bash
go test -run TestName ./path/to/package
```

**Format code:**
```bash
go fmt ./...
```

**Lint code (requires golangci-lint):**
```bash
golangci-lint run ./...
```

## Project Architecture

This is a standard Go project structured for scalability:

- **`cmd/trade-tracker/`** — Application binary entrypoint. Each binary gets its own directory under `cmd/`.
- **`internal/`** — Private packages (not importable by external projects). Organize business logic, domain models, and services here as the project grows.
- **`go.mod`** — Module definition; update version requirements here.
- **`docs/`** - Architecture and phase documentation. This is where work is designed, planned, and tracked.

## Development Notes

- Keep `cmd/` minimal—it should mostly handle CLI bootstrapping and delegate to internal packages.
- Use `internal/` packages to encapsulate business logic and prevent external dependencies on internal APIs.
- Tests should live alongside the code they test (e.g., `internal/core/trade_test.go` for `internal/core/trade.go`).
- Use Go 1.23+ features as appropriate; avoid unnecessary compatibility constraints.
