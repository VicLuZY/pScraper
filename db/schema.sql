-- Relational schema for migrating the file-backed DB into SQLite/Postgres.
-- JSONL remains the default storage backend; the optional SQLite adapter uses
-- the same current/history/audit tables for production-style runs.

CREATE TABLE IF NOT EXISTS permit_current (
    dedupe_key TEXT PRIMARY KEY,
    source_id TEXT NOT NULL,
    source_name TEXT,
    jurisdiction TEXT NOT NULL,
    jurisdiction_type TEXT,
    region TEXT,
    permit_number TEXT,
    application_id TEXT,
    permit_type TEXT,
    permit_family TEXT,
    status TEXT,
    address TEXT,
    pid TEXT,
    roll_number TEXT,
    applicant TEXT,
    contractor TEXT,
    description TEXT,
    applied_date TEXT,
    issued_date TEXT,
    final_date TEXT,
    completed_date TEXT,
    value TEXT,
    latitude TEXT,
    longitude TEXT,
    url TEXT,
    content_hash TEXT NOT NULL,
    raw_json TEXT,
    first_seen_at TIMESTAMP,
    last_seen_at TIMESTAMP,
    last_changed_at TIMESTAMP,
    scraped_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_permit_current_jurisdiction ON permit_current(jurisdiction);
CREATE INDEX IF NOT EXISTS idx_permit_current_permit_number ON permit_current(permit_number);
CREATE INDEX IF NOT EXISTS idx_permit_current_address ON permit_current(address);
CREATE INDEX IF NOT EXISTS idx_permit_current_status ON permit_current(status);
CREATE INDEX IF NOT EXISTS idx_permit_current_source ON permit_current(source_id);

CREATE TABLE IF NOT EXISTS permit_status_history (
    id INTEGER PRIMARY KEY,
    dedupe_key TEXT NOT NULL,
    source_id TEXT NOT NULL,
    jurisdiction TEXT NOT NULL,
    permit_number TEXT,
    application_id TEXT,
    old_status TEXT,
    new_status TEXT,
    old_content_hash TEXT,
    new_content_hash TEXT NOT NULL,
    changed_at TIMESTAMP NOT NULL,
    snapshot_json TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_permit_history_key ON permit_status_history(dedupe_key);
CREATE INDEX IF NOT EXISTS idx_permit_history_changed_at ON permit_status_history(changed_at);

CREATE TABLE IF NOT EXISTS scrape_audit (
    id INTEGER PRIMARY KEY,
    run_id TEXT NOT NULL,
    source_id TEXT NOT NULL,
    source_name TEXT,
    jurisdiction TEXT,
    kind TEXT,
    started_at TIMESTAMP,
    finished_at TIMESTAMP,
    status TEXT,
    message TEXT,
    records_seen INTEGER DEFAULT 0,
    inserted INTEGER DEFAULT 0,
    updated INTEGER DEFAULT 0,
    unchanged INTEGER DEFAULT 0,
    skipped BOOLEAN DEFAULT FALSE
);
