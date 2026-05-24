package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/example/bc-permit-scraper/internal/dedupe"
	"github.com/example/bc-permit-scraper/internal/model"
	sqlitestore "github.com/example/bc-permit-scraper/internal/storage/sqlite"
)

func TestImportJSONLToSQLite(t *testing.T) {
	jsonlDir := t.TempDir()
	r := dedupe.Enrich(model.PermitRecord{
		SourceID:     "test",
		SourceName:   "Test Source",
		Jurisdiction: "City of Test",
		PermitNumber: "BP-1",
		Status:       "Issued",
		ScrapedAt:    "2026-05-24T20:00:00Z",
		FirstSeenAt:  "2026-05-24T20:00:00Z",
		LastSeenAt:   "2026-05-24T20:00:00Z",
		Raw:          map[string]string{"PermitNo": "BP-1"},
	})
	writeJSONL(t, filepath.Join(jsonlDir, "current.jsonl"), []model.PermitRecord{r})
	writeJSONL(t, filepath.Join(jsonlDir, "history.jsonl"), []model.StatusEvent{{
		DedupeKey:      r.DedupeKey,
		SourceID:       r.SourceID,
		Jurisdiction:   r.Jurisdiction,
		PermitNumber:   r.PermitNumber,
		NewStatus:      r.Status,
		NewContentHash: r.ContentHash,
		ChangedAt:      r.LastSeenAt,
		Snapshot:       r,
	}})
	writeJSONL(t, filepath.Join(jsonlDir, "scrape_audit.jsonl"), []model.ScrapeAudit{{
		RunID:       "run-1",
		SourceID:    "test",
		SourceName:  "Test Source",
		Status:      "ok",
		RecordsSeen: 1,
	}})

	sqlitePath := filepath.Join(t.TempDir(), "permits.sqlite")
	var out bytes.Buffer
	if err := run([]string{"import-jsonl", "--jsonl", jsonlDir, "--sqlite", sqlitePath}, &out); err != nil {
		t.Fatal(err)
	}
	var sum importSummary
	if err := json.Unmarshal(out.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Current != 1 || sum.History != 1 || sum.Audit != 1 {
		t.Fatalf("bad import summary: %+v", sum)
	}

	db, err := sqlitestore.Open(sqlitePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	current := db.AllCurrent()
	if len(current) != 1 || current[0].Raw["PermitNo"] != "BP-1" {
		t.Fatalf("bad imported current rows: %+v", current)
	}
	counts, err := db.TableCounts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["permit_status_history"] != 1 || counts["scrape_audit"] != 1 {
		t.Fatalf("bad imported table counts: %+v", counts)
	}
}

func writeJSONL[T any](t *testing.T, path string, rows []T) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			f.Close()
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
