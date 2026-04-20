# Phase 6 — Verification & Tweaks

Connect the TUI to a live server with real data, exercise every view, and fix whatever
is missing or broken. Items are added here as gaps are discovered.

Prerequisite: Phase 5 (TUI implemented).

---

## Scope

| Item | Status |
|---|---|
| Account management (create, rename, delete) | 🔲 |

---

## Account Management

### Current state

Accounts are created **implicitly** during CSV import — no explicit creation RPC exists.
The `AccountRepository` already has a `Create` method, but it is not wired to any service
or proto RPC. There is no rename or delete at any layer.

The proto comment reads: *"Accounts are created implicitly during CSV import; no mutation
RPCs are exposed."* This needs to change.

### What's missing

**Proto / gRPC (`account.proto` + `AccountHandler`)**

| RPC | Notes |
|---|---|
| `CreateAccount` | Explicit account creation; broker + account_number + optional name |
| `UpdateAccount` | Rename only (name field); broker and account_number are immutable |
| `DeleteAccount` | Delete account and all associated data, or reject if data exists |

**Service layer**

`AccountReader` in `internal/service/interfaces.go` is read-only. Either extend it to
`AccountService` or introduce a separate `AccountWriter` interface. The repo already
satisfies `Create`; `Update` (rename) and `Delete` need to be added to
`repository.AccountRepository` and `sqlite.accountRepo`.

**Delete semantics**: decide before implementing —
- *Reject if data exists*: simpler, safer. Return `FailedPrecondition` if any trades,
  positions, or transactions reference the account.
- *Cascade delete*: removes all associated data. Destructive; needs explicit confirmation
  in the TUI.

**TUI**

The accounts view (`internal/tui/views/accounts.go`) currently only lists accounts for
selection. It needs:
- `n` keybinding — create new account (inline form: broker, account number, name)
- `r` keybinding — rename selected account
- `d` / `D` keybinding — delete selected account (confirm prompt before calling RPC)

---

## Future / Deferred

- **`UNIQUE(broker, account_number)` constraint** — the `accounts` table currently has only a PK on `id`. Two creates with different UUIDs but the same broker+account_number both succeed. Add a migration with `UNIQUE(broker, account_number)` and map that SQLite constraint to `domain.ErrDuplicate` in `isUniqueConstraint`.

---

## Done When

- `make test` passes
- All views exercise real data without crashes or layout issues
- Each item in the scope table is checked off
