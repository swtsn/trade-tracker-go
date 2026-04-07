-- import_jobs tracks CSV import runs per account.
-- Domain type and repository implementation deferred to Phase 2.
CREATE TABLE import_jobs (
    id             TEXT PRIMARY KEY,
    account_id     TEXT NOT NULL REFERENCES accounts(id),
    broker         TEXT NOT NULL,
    filename       TEXT NOT NULL,
    row_count      INTEGER NOT NULL DEFAULT 0,
    imported_count INTEGER NOT NULL DEFAULT 0,
    skipped_count  INTEGER NOT NULL DEFAULT 0,
    error_count    INTEGER NOT NULL DEFAULT 0,
    status         TEXT NOT NULL,
    error_detail   TEXT,
    started_at     TEXT NOT NULL,
    completed_at   TEXT
);
