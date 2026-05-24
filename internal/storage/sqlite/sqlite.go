package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/example/bc-permit-scraper/internal/model"
	"github.com/example/bc-permit-scraper/internal/storage"
	_ "modernc.org/sqlite"
)

type DB struct {
	db *sql.DB
}

func Open(path string) (*DB, error) {
	if path == "" {
		path = "data/permits.sqlite"
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	sqldb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db := &DB{db: sqldb}
	if err := db.applySchema(); err != nil {
		_ = sqldb.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error {
	if db == nil || db.db == nil {
		return nil
	}
	return db.db.Close()
}

func (db *DB) Upsert(r model.PermitRecord) (storage.UpsertResult, error) {
	if r.DedupeKey == "" || r.ContentHash == "" {
		return "", fmt.Errorf("record missing dedupe_key or content_hash")
	}
	tx, err := db.db.BeginTx(context.Background(), nil)
	if err != nil {
		return "", err
	}
	defer rollbackUnlessDone(tx)

	old, exists, err := getCurrentTx(tx, r.DedupeKey)
	if err != nil {
		return "", err
	}
	now := model.NowUTC()
	result := storage.Inserted
	if !exists {
		r.FirstSeenAt = now
		r.LastChangedAt = now
	} else if old.ContentHash == r.ContentHash {
		r.FirstSeenAt = old.FirstSeenAt
		r.LastChangedAt = old.LastChangedAt
		result = storage.Unchanged
	} else {
		r.FirstSeenAt = old.FirstSeenAt
		r.LastChangedAt = now
		result = storage.Updated
	}
	r.LastSeenAt = now

	if err := putCurrentTx(tx, r); err != nil {
		return "", err
	}
	if result == storage.Inserted || result == storage.Updated {
		evt := model.StatusEvent{
			DedupeKey:      r.DedupeKey,
			SourceID:       r.SourceID,
			Jurisdiction:   r.Jurisdiction,
			PermitNumber:   r.PermitNumber,
			ApplicationID:  r.ApplicationID,
			NewStatus:      r.Status,
			NewContentHash: r.ContentHash,
			ChangedAt:      now,
			Snapshot:       r,
		}
		if exists {
			evt.OldStatus = old.Status
			evt.OldContentHash = old.ContentHash
		}
		if err := addHistoryTx(tx, evt); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return result, nil
}

func (db *DB) AddAudit(a model.ScrapeAudit) error {
	_, err := db.db.Exec(auditInsertSQL,
		a.RunID,
		a.SourceID,
		a.SourceName,
		a.Jurisdiction,
		a.Kind,
		a.StartedAt,
		a.FinishedAt,
		a.Status,
		a.Message,
		a.RecordsSeen,
		a.Inserted,
		a.Updated,
		a.Unchanged,
		a.Skipped,
	)
	return err
}

func (db *DB) AllCurrent() []model.PermitRecord {
	rows, err := db.db.Query("SELECT " + currentColumns + " FROM permit_current ORDER BY dedupe_key")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []model.PermitRecord
	for rows.Next() {
		r, err := scanPermit(rows)
		if err != nil {
			return nil
		}
		out = append(out, r)
	}
	return out
}

func (db *DB) PutCurrent(r model.PermitRecord) error {
	if r.DedupeKey == "" || r.ContentHash == "" {
		return fmt.Errorf("record missing dedupe_key or content_hash")
	}
	_, err := db.db.Exec(currentUpsertSQL, recordArgs(r)...)
	return err
}

func (db *DB) AddHistory(evt model.StatusEvent) error {
	_, err := db.db.Exec(historyInsertSQL, historyArgs(evt)...)
	return err
}

func (db *DB) Reset() error {
	tx, err := db.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessDone(tx)
	for _, table := range []string{"scrape_audit", "permit_status_history", "permit_current"} {
		if _, err := tx.Exec("DELETE FROM " + table); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) TableCounts() (map[string]int, error) {
	counts := map[string]int{}
	for _, table := range []string{"permit_current", "permit_status_history", "scrape_audit"} {
		var count int
		if err := db.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			return nil, err
		}
		counts[table] = count
	}
	return counts, nil
}

func (db *DB) applySchema() error {
	if _, err := db.db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return err
	}
	_, err := db.db.Exec(schemaSQL)
	return err
}

func getCurrentTx(tx *sql.Tx, key string) (model.PermitRecord, bool, error) {
	row := tx.QueryRow("SELECT "+currentColumns+" FROM permit_current WHERE dedupe_key = ?", key)
	r, err := scanPermit(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.PermitRecord{}, false, nil
	}
	if err != nil {
		return model.PermitRecord{}, false, err
	}
	return r, true, nil
}

func putCurrentTx(tx *sql.Tx, r model.PermitRecord) error {
	_, err := tx.Exec(currentUpsertSQL, recordArgs(r)...)
	return err
}

func addHistoryTx(tx *sql.Tx, evt model.StatusEvent) error {
	_, err := tx.Exec(historyInsertSQL, historyArgs(evt)...)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanPermit(sc scanner) (model.PermitRecord, error) {
	var r model.PermitRecord
	var rawJSON string
	err := sc.Scan(
		&r.DedupeKey,
		&r.SourceID,
		&r.SourceName,
		&r.Jurisdiction,
		&r.JurisdictionType,
		&r.Region,
		&r.PermitNumber,
		&r.ApplicationID,
		&r.PermitType,
		&r.PermitFamily,
		&r.Status,
		&r.Address,
		&r.PID,
		&r.RollNumber,
		&r.Applicant,
		&r.Contractor,
		&r.Description,
		&r.AppliedDate,
		&r.IssuedDate,
		&r.FinalDate,
		&r.CompletedDate,
		&r.Value,
		&r.Latitude,
		&r.Longitude,
		&r.URL,
		&r.ContentHash,
		&rawJSON,
		&r.FirstSeenAt,
		&r.LastSeenAt,
		&r.LastChangedAt,
		&r.ScrapedAt,
	)
	if err != nil {
		return model.PermitRecord{}, err
	}
	if strings.TrimSpace(rawJSON) != "" && rawJSON != "null" {
		if err := json.Unmarshal([]byte(rawJSON), &r.Raw); err != nil {
			return model.PermitRecord{}, err
		}
	}
	return r, nil
}

func recordArgs(r model.PermitRecord) []any {
	return []any{
		r.DedupeKey,
		r.SourceID,
		r.SourceName,
		r.Jurisdiction,
		r.JurisdictionType,
		r.Region,
		r.PermitNumber,
		r.ApplicationID,
		r.PermitType,
		r.PermitFamily,
		r.Status,
		r.Address,
		r.PID,
		r.RollNumber,
		r.Applicant,
		r.Contractor,
		r.Description,
		r.AppliedDate,
		r.IssuedDate,
		r.FinalDate,
		r.CompletedDate,
		r.Value,
		r.Latitude,
		r.Longitude,
		r.URL,
		r.ContentHash,
		jsonString(r.Raw),
		r.FirstSeenAt,
		r.LastSeenAt,
		r.LastChangedAt,
		r.ScrapedAt,
	}
}

func historyArgs(evt model.StatusEvent) []any {
	return []any{
		evt.DedupeKey,
		evt.SourceID,
		evt.Jurisdiction,
		evt.PermitNumber,
		evt.ApplicationID,
		evt.OldStatus,
		evt.NewStatus,
		evt.OldContentHash,
		evt.NewContentHash,
		evt.ChangedAt,
		jsonString(evt.Snapshot),
	}
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func rollbackUnlessDone(tx *sql.Tx) {
	_ = tx.Rollback()
}

const currentColumns = `dedupe_key, source_id, source_name, jurisdiction, jurisdiction_type, region, permit_number, application_id, permit_type, permit_family, status, address, pid, roll_number, applicant, contractor, description, applied_date, issued_date, final_date, completed_date, value, latitude, longitude, url, content_hash, raw_json, first_seen_at, last_seen_at, last_changed_at, scraped_at`

var currentPlaceholders = strings.TrimRight(strings.Repeat("?,", 31), ",")

var currentUpsertSQL = fmt.Sprintf("INSERT OR REPLACE INTO permit_current (%s) VALUES (%s)", currentColumns, currentPlaceholders)

const historyInsertSQL = `INSERT INTO permit_status_history (
	dedupe_key, source_id, jurisdiction, permit_number, application_id, old_status, new_status,
	old_content_hash, new_content_hash, changed_at, snapshot_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const auditInsertSQL = `INSERT INTO scrape_audit (
	run_id, source_id, source_name, jurisdiction, kind, started_at, finished_at, status, message,
	records_seen, inserted, updated, unchanged, skipped
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const schemaSQL = `
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
`
