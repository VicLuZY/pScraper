package storage

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/example/bc-permit-scraper/internal/model"
)

type UpsertResult string

const (
	Inserted  UpsertResult = "inserted"
	Updated   UpsertResult = "updated"
	Unchanged UpsertResult = "unchanged"
)

type JSONDB struct {
	dir     string
	mu      sync.Mutex
	current map[string]model.PermitRecord
	loaded  bool
}

func OpenJSONDB(dir string) (*JSONDB, error) {
	if dir == "" {
		dir = "data/permits-db"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db := &JSONDB{dir: dir, current: map[string]model.PermitRecord{}}
	if err := db.load(); err != nil {
		return nil, err
	}
	return db, nil
}

func (db *JSONDB) CurrentPath() string { return filepath.Join(db.dir, "current.jsonl") }
func (db *JSONDB) HistoryPath() string { return filepath.Join(db.dir, "history.jsonl") }
func (db *JSONDB) AuditPath() string   { return filepath.Join(db.dir, "scrape_audit.jsonl") }

func (db *JSONDB) load() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.loaded {
		return nil
	}
	path := db.CurrentPath()
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		db.loaded = true
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*1024)
	sc.Buffer(buf, 16*1024*1024)
	for sc.Scan() {
		var r model.PermitRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return fmt.Errorf("parse current record: %w", err)
		}
		db.current[r.DedupeKey] = r
	}
	if err := sc.Err(); err != nil {
		return err
	}
	db.loaded = true
	return nil
}

func (db *JSONDB) Upsert(r model.PermitRecord) (UpsertResult, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if r.DedupeKey == "" || r.ContentHash == "" {
		return "", fmt.Errorf("record missing dedupe_key or content_hash")
	}
	now := model.NowUTC()
	old, exists := db.current[r.DedupeKey]
	result := Inserted
	if !exists {
		r.FirstSeenAt = now
		r.LastChangedAt = now
	} else if old.ContentHash == r.ContentHash {
		r.FirstSeenAt = old.FirstSeenAt
		r.LastChangedAt = old.LastChangedAt
		result = Unchanged
	} else {
		r.FirstSeenAt = old.FirstSeenAt
		r.LastChangedAt = now
		result = Updated
	}
	r.LastSeenAt = now
	db.current[r.DedupeKey] = r
	if result == Inserted || result == Updated {
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
		if err := appendJSON(db.HistoryPath(), evt); err != nil {
			return "", err
		}
	}
	if err := db.flushCurrentLocked(); err != nil {
		return "", err
	}
	return result, nil
}

func (db *JSONDB) AddAudit(a model.ScrapeAudit) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	return appendJSON(db.AuditPath(), a)
}

func (db *JSONDB) AllCurrent() []model.PermitRecord {
	db.mu.Lock()
	defer db.mu.Unlock()
	out := make([]model.PermitRecord, 0, len(db.current))
	for _, r := range db.current {
		out = append(out, model.CloneRecord(r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DedupeKey < out[j].DedupeKey })
	return out
}

func (db *JSONDB) flushCurrentLocked() error {
	tmp := db.CurrentPath() + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(db.current))
	for k := range db.current {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	enc := json.NewEncoder(f)
	for _, k := range keys {
		if err := enc.Encode(db.current[k]); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, db.CurrentPath())
}

func appendJSON(path string, v any) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(v)
}
