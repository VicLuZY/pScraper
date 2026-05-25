package scrapers

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

type vancouverIndexStore struct {
	db       *sql.DB
	dbPath   string
	jsonPath string
}

func openVancouverIndexStore(dataDir string) (*vancouverIndexStore, error) {
	dbPath := vancouverIndexDBPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	store := &vancouverIndexStore{db: db, dbPath: dbPath, jsonPath: vancouverIndexJSONLPath(dataDir)}
	if err := store.applySchema(); err != nil {
		_ = store.Close()
		return nil, err
	}
	if err := store.migrateLegacyJSONLIfEmpty(); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

func vancouverIndexDBPath(dataDir string) string {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "data/permits-db"
	}
	return filepath.Join(dataDir, vancouverIndexDBFileName)
}

func vancouverIndexJSONLPath(dataDir string) string {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "data/permits-db"
	}
	return filepath.Join(dataDir, vancouverIndexFileName)
}

func (s *vancouverIndexStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *vancouverIndexStore) applySchema() error {
	for _, stmt := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		vancouverIndexSchemaSQL,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	for _, stmt := range vancouverIndexAlterSQL {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return err
		}
	}
	return nil
}

func (s *vancouverIndexStore) Count() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM vancouver_posse_index").Scan(&count)
	return count, err
}

func (s *vancouverIndexStore) migrateLegacyJSONLIfEmpty() error {
	count, err := s.Count()
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	f, err := os.Open(s.jsonPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	stmt, err := tx.Prepare(vancouverIndexUpsertSQL)
	if err != nil {
		return err
	}
	defer stmt.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var entry vancouverIndexEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return fmt.Errorf("parse legacy Vancouver index %s line %d: %w", s.jsonPath, lineNo, err)
		}
		normalizeVancouverIndexEntry(&entry)
		if entry.ObjectID == "" {
			continue
		}
		if _, err := stmt.Exec(vancouverIndexArgs(entry)...); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *vancouverIndexStore) UpsertEntries(entries []vancouverIndexEntry, indexedAt string) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	stmt, err := tx.Prepare(vancouverIndexUpsertSQL)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, entry := range entries {
		normalizeVancouverIndexEntry(&entry)
		if entry.ObjectID == "" {
			continue
		}
		if entry.FirstIndexedAt == "" {
			entry.FirstIndexedAt = indexedAt
		}
		if entry.LastIndexedAt == "" {
			entry.LastIndexedAt = indexedAt
		}
		if _, err := stmt.Exec(vancouverIndexArgs(entry)...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *vancouverIndexStore) ResetDetailStatus(status string) error {
	if strings.TrimSpace(status) == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE vancouver_posse_index
		SET detail_status = '', detail_started_at = '', detail_error = ''
		WHERE detail_status = ?`, status)
	return err
}

func (s *vancouverIndexStore) MarkDetailStatus(objectID, status, message, at string) error {
	if strings.TrimSpace(objectID) == "" {
		return nil
	}
	_, err := s.db.Exec(vancouverDetailStatusUpdateSQL, vancouverDetailStatusArgs(objectID, status, message, at)...)
	return err
}

func (s *vancouverIndexStore) MarkDetailStatusBatch(objectIDs []string, status, message, at string) error {
	if len(objectIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	stmt, err := tx.Prepare(vancouverDetailStatusUpdateSQL)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, objectID := range objectIDs {
		if strings.TrimSpace(objectID) == "" {
			continue
		}
		if _, err := stmt.Exec(vancouverDetailStatusArgs(objectID, status, message, at)...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func vancouverDetailStatusArgs(objectID, status, message, at string) []any {
	startedAt := ""
	finishedAt := ""
	errorMessage := ""
	switch status {
	case vancouverDetailStatusScraping:
		startedAt = at
	case vancouverDetailStatusScraped:
		finishedAt = at
	case vancouverDetailStatusError:
		finishedAt = at
		errorMessage = message
	}
	return []any{status, startedAt, startedAt, finishedAt, finishedAt, errorMessage, objectID}
}

func (s *vancouverIndexStore) SelectEntries(fromDate, toDate string, discoveredIDs map[string]bool, detailOnly bool, limit int) ([]vancouverIndexEntry, error) {
	out := []vancouverIndexEntry{}
	err := s.ForEachEntry(context.Background(), fromDate, toDate, discoveredIDs, detailOnly, "", limit, func(entry vancouverIndexEntry) error {
		out = append(out, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedDate == out[j].CreatedDate {
			return out[i].ObjectID < out[j].ObjectID
		}
		return out[i].CreatedDate < out[j].CreatedDate
	})
	return out, nil
}

func (s *vancouverIndexStore) ForEachEntry(ctx context.Context, fromDate, toDate string, discoveredIDs map[string]bool, detailOnly bool, detailStatus string, limit int, fn func(vancouverIndexEntry) error) error {
	rows, err := s.db.QueryContext(ctx, vancouverIndexSelectSQL, fromDate, fromDate, toDate, toDate)
	if err != nil {
		return err
	}
	defer rows.Close()
	allowedStatuses := vancouverDetailStatusSet(detailStatus)
	seen := 0
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry, err := scanVancouverIndexEntry(rows)
		if err != nil {
			return err
		}
		if !detailOnly && len(discoveredIDs) > 0 && !discoveredIDs[entry.ObjectID] {
			continue
		}
		if !dateInRange(entry.CreatedDate, fromDate, toDate) {
			continue
		}
		if !vancouverDetailStatusAllowed(entry.DetailStatus, allowedStatuses) {
			continue
		}
		if err := fn(entry); err != nil {
			return err
		}
		seen++
		if limit > 0 && seen >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}

func vancouverDetailStatusSet(filter string) map[string]bool {
	filter = strings.TrimSpace(strings.ToLower(filter))
	if filter == "" || filter == "all" || filter == "*" {
		return nil
	}
	out := map[string]bool{}
	for _, part := range strings.Split(filter, ",") {
		switch strings.TrimSpace(strings.ToLower(part)) {
		case "", "all", "*":
			return nil
		case "pending", "unprocessed", "not_processed", "not-processed", "blank", "todo":
			out[""] = true
		case "error", "errors", "failed":
			out[vancouverDetailStatusError] = true
		case "scraping", "running", "active":
			out[vancouverDetailStatusScraping] = true
		case "scraped", "done", "complete", "completed":
			out[vancouverDetailStatusScraped] = true
		default:
			out[strings.TrimSpace(strings.ToLower(part))] = true
		}
	}
	return out
}

func vancouverDetailStatusAllowed(status string, allowed map[string]bool) bool {
	if allowed == nil {
		return true
	}
	return allowed[strings.TrimSpace(strings.ToLower(status))]
}

func scanVancouverIndexEntry(sc interface{ Scan(dest ...any) error }) (vancouverIndexEntry, error) {
	var entry vancouverIndexEntry
	var capped int
	err := sc.Scan(
		&entry.ObjectID,
		&entry.DetailURL,
		&entry.PermitNumber,
		&entry.PermitType,
		&entry.Status,
		&entry.Address,
		&entry.CreatedDate,
		&entry.IssueDate,
		&entry.CompletedDate,
		&entry.SearchWindowFrom,
		&entry.SearchWindowTo,
		&entry.SearchWindowCount,
		&capped,
		&entry.SearchSplitDepth,
		&entry.DetailStatus,
		&entry.DetailStartedAt,
		&entry.DetailFinishedAt,
		&entry.DetailError,
		&entry.FirstIndexedAt,
		&entry.LastIndexedAt,
	)
	if err != nil {
		return vancouverIndexEntry{}, err
	}
	entry.SearchWindowCapped = capped != 0
	entry.Raw = vancouverIndexRaw(entry)
	return entry, nil
}

func normalizeVancouverIndexEntry(entry *vancouverIndexEntry) {
	if entry == nil {
		return
	}
	if entry.Raw != nil {
		entry.PermitNumber = first(entry.PermitNumber, entry.Raw["Number"])
		entry.PermitType = first(entry.PermitType, entry.Raw["Type"])
		entry.Status = first(entry.Status, entry.Raw["Status"])
		entry.Address = first(entry.Address, entry.Raw["Location"])
		entry.CreatedDate = first(entry.CreatedDate, normalizeDate(entry.Raw["Created Date"]))
		entry.IssueDate = first(entry.IssueDate, normalizeDate(entry.Raw["Issue Date"]))
		entry.CompletedDate = first(entry.CompletedDate, normalizeDate(entry.Raw["Completed Date"]))
		entry.SearchWindowFrom = first(entry.SearchWindowFrom, entry.Raw["Search Window From"])
		entry.SearchWindowTo = first(entry.SearchWindowTo, entry.Raw["Search Window To"])
		entry.DetailURL = first(entry.DetailURL, entry.Raw["Vancouver Object URL"])
		if entry.SearchWindowCount == 0 {
			entry.SearchWindowCount = atoiDefault(entry.Raw["Search Window Count"], 0)
		}
		if !entry.SearchWindowCapped {
			entry.SearchWindowCapped = strings.EqualFold(entry.Raw["Search Window Capped"], "true")
		}
		if entry.SearchSplitDepth == 0 {
			entry.SearchSplitDepth = atoiDefault(entry.Raw["Search Split Depth"], 0)
		}
		entry.DetailStatus = first(entry.DetailStatus, entry.Raw["Detail Status"])
		entry.DetailStartedAt = first(entry.DetailStartedAt, entry.Raw["Detail Started At"])
		entry.DetailFinishedAt = first(entry.DetailFinishedAt, entry.Raw["Detail Finished At"])
		entry.DetailError = first(entry.DetailError, entry.Raw["Detail Error"])
	}
	if entry.Raw == nil {
		entry.Raw = vancouverIndexRaw(*entry)
	}
}

func vancouverIndexRaw(entry vancouverIndexEntry) map[string]string {
	raw := map[string]string{
		"Type":                 entry.PermitType,
		"Number":               entry.PermitNumber,
		"Location":             entry.Address,
		"Status":               entry.Status,
		"Created Date":         entry.CreatedDate,
		"Issue Date":           entry.IssueDate,
		"Completed Date":       entry.CompletedDate,
		"Search Window From":   entry.SearchWindowFrom,
		"Search Window To":     entry.SearchWindowTo,
		"Server Result Limit":  fmt.Sprint(vancouverServerResultLimit),
		"Vancouver Object URL": entry.DetailURL,
		"Search Window Count":  fmt.Sprint(entry.SearchWindowCount),
		"Search Window Capped": fmt.Sprint(entry.SearchWindowCapped),
		"Search Split Depth":   fmt.Sprint(entry.SearchSplitDepth),
		"Detail Status":        entry.DetailStatus,
		"Detail Started At":    entry.DetailStartedAt,
		"Detail Finished At":   entry.DetailFinishedAt,
		"Detail Error":         entry.DetailError,
	}
	for key, value := range raw {
		if strings.TrimSpace(value) == "" {
			delete(raw, key)
		}
	}
	return raw
}

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

func vancouverIndexArgs(entry vancouverIndexEntry) []any {
	return []any{
		entry.ObjectID,
		entry.DetailURL,
		entry.PermitNumber,
		entry.PermitType,
		entry.Status,
		entry.Address,
		entry.CreatedDate,
		entry.IssueDate,
		entry.CompletedDate,
		entry.SearchWindowFrom,
		entry.SearchWindowTo,
		entry.SearchWindowCount,
		boolInt(entry.SearchWindowCapped),
		entry.SearchSplitDepth,
		entry.DetailStatus,
		entry.DetailStartedAt,
		entry.DetailFinishedAt,
		entry.DetailError,
		entry.FirstIndexedAt,
		entry.LastIndexedAt,
	}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func rollbackUnlessCommitted(tx *sql.Tx) {
	_ = tx.Rollback()
}

const vancouverIndexColumns = `object_id, detail_url, permit_number, permit_type, status, address, created_date, issue_date, completed_date, search_window_from, search_window_to, search_window_count, search_window_capped, search_split_depth, detail_status, detail_started_at, detail_finished_at, detail_error, first_indexed_at, last_indexed_at`

const vancouverIndexSelectSQL = `SELECT ` + vancouverIndexColumns + ` FROM vancouver_posse_index
WHERE (? = '' OR created_date = '' OR created_date >= ?)
  AND (? = '' OR created_date = '' OR created_date <= ?)
ORDER BY created_date, object_id`

const vancouverIndexUpsertSQL = `INSERT INTO vancouver_posse_index (
	object_id, detail_url, permit_number, permit_type, status, address, created_date, issue_date,
	completed_date, search_window_from, search_window_to, search_window_count, search_window_capped,
	search_split_depth, detail_status, detail_started_at, detail_finished_at, detail_error, first_indexed_at, last_indexed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(object_id) DO UPDATE SET
	detail_url = excluded.detail_url,
	permit_number = excluded.permit_number,
	permit_type = excluded.permit_type,
	status = excluded.status,
	address = excluded.address,
	created_date = excluded.created_date,
	issue_date = excluded.issue_date,
	completed_date = excluded.completed_date,
	search_window_from = excluded.search_window_from,
	search_window_to = excluded.search_window_to,
	search_window_count = excluded.search_window_count,
	search_window_capped = excluded.search_window_capped,
	search_split_depth = excluded.search_split_depth,
	detail_status = CASE
		WHEN excluded.detail_status IS NULL OR excluded.detail_status = ''
		THEN vancouver_posse_index.detail_status
		ELSE excluded.detail_status
	END,
	detail_started_at = CASE
		WHEN excluded.detail_started_at IS NULL OR excluded.detail_started_at = ''
		THEN vancouver_posse_index.detail_started_at
		ELSE excluded.detail_started_at
	END,
	detail_finished_at = CASE
		WHEN excluded.detail_finished_at IS NULL OR excluded.detail_finished_at = ''
		THEN vancouver_posse_index.detail_finished_at
		ELSE excluded.detail_finished_at
	END,
	detail_error = CASE
		WHEN excluded.detail_error IS NULL OR excluded.detail_error = ''
		THEN vancouver_posse_index.detail_error
		ELSE excluded.detail_error
	END,
	first_indexed_at = CASE
		WHEN vancouver_posse_index.first_indexed_at IS NULL OR vancouver_posse_index.first_indexed_at = ''
		THEN excluded.first_indexed_at
		ELSE vancouver_posse_index.first_indexed_at
	END,
	last_indexed_at = excluded.last_indexed_at`

const vancouverIndexSchemaSQL = `
CREATE TABLE IF NOT EXISTS vancouver_posse_index (
	object_id TEXT PRIMARY KEY NOT NULL,
	detail_url TEXT NOT NULL,
	permit_number TEXT,
	permit_type TEXT,
	status TEXT,
	address TEXT,
	created_date TEXT,
	issue_date TEXT,
	completed_date TEXT,
	search_window_from TEXT,
	search_window_to TEXT,
	search_window_count INTEGER NOT NULL DEFAULT 0,
	search_window_capped INTEGER NOT NULL DEFAULT 0,
	search_split_depth INTEGER NOT NULL DEFAULT 0,
	detail_status TEXT NOT NULL DEFAULT '',
	detail_started_at TEXT NOT NULL DEFAULT '',
	detail_finished_at TEXT NOT NULL DEFAULT '',
	detail_error TEXT NOT NULL DEFAULT '',
	first_indexed_at TEXT,
	last_indexed_at TEXT
) WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS idx_vancouver_posse_created_date ON vancouver_posse_index(created_date);
CREATE INDEX IF NOT EXISTS idx_vancouver_posse_window ON vancouver_posse_index(search_window_from, search_window_to);
CREATE INDEX IF NOT EXISTS idx_vancouver_posse_permit_number ON vancouver_posse_index(permit_number);
CREATE INDEX IF NOT EXISTS idx_vancouver_posse_detail_status ON vancouver_posse_index(detail_status);
CREATE INDEX IF NOT EXISTS idx_vancouver_posse_detail_url ON vancouver_posse_index(detail_url);
CREATE INDEX IF NOT EXISTS idx_vancouver_posse_created_status ON vancouver_posse_index(created_date, detail_status);
`

var vancouverIndexAlterSQL = []string{
	"ALTER TABLE vancouver_posse_index ADD COLUMN detail_status TEXT NOT NULL DEFAULT ''",
	"ALTER TABLE vancouver_posse_index ADD COLUMN detail_started_at TEXT NOT NULL DEFAULT ''",
	"ALTER TABLE vancouver_posse_index ADD COLUMN detail_finished_at TEXT NOT NULL DEFAULT ''",
	"ALTER TABLE vancouver_posse_index ADD COLUMN detail_error TEXT NOT NULL DEFAULT ''",
	"CREATE INDEX IF NOT EXISTS idx_vancouver_posse_detail_status ON vancouver_posse_index(detail_status)",
	"CREATE INDEX IF NOT EXISTS idx_vancouver_posse_detail_url ON vancouver_posse_index(detail_url)",
	"CREATE INDEX IF NOT EXISTS idx_vancouver_posse_created_status ON vancouver_posse_index(created_date, detail_status)",
}

const vancouverDetailStatusUpdateSQL = `UPDATE vancouver_posse_index
SET detail_status = ?,
	detail_started_at = CASE WHEN ? != '' THEN ? ELSE detail_started_at END,
	detail_finished_at = CASE WHEN ? != '' THEN ? ELSE detail_finished_at END,
	detail_error = ?
WHERE object_id = ?`
