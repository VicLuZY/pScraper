package sqlite

import (
	"path/filepath"
	"testing"

	"github.com/example/bc-permit-scraper/internal/dedupe"
	"github.com/example/bc-permit-scraper/internal/model"
	"github.com/example/bc-permit-scraper/internal/storage"
)

func TestSQLiteUpsertTracksInsertUnchangedUpdate(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "permits.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	r := dedupe.Enrich(model.PermitRecord{
		SourceID:     "test",
		Jurisdiction: "City of Test",
		PermitNumber: "BP-1",
		PermitType:   "Building",
		Status:       "Submitted",
		ScrapedAt:    model.NowUTC(),
		Raw:          map[string]string{"Status": "Submitted"},
	})
	got, err := db.Upsert(r)
	if err != nil || got != storage.Inserted {
		t.Fatalf("insert got %s err %v", got, err)
	}
	got, err = db.Upsert(r)
	if err != nil || got != storage.Unchanged {
		t.Fatalf("unchanged got %s err %v", got, err)
	}
	r.Status = "Issued"
	r.Raw["Status"] = "Issued"
	r = dedupe.Enrich(r)
	got, err = db.Upsert(r)
	if err != nil || got != storage.Updated {
		t.Fatalf("update got %s err %v", got, err)
	}

	current := db.AllCurrent()
	if len(current) != 1 {
		t.Fatalf("expected 1 current record, got %d", len(current))
	}
	if current[0].Status != "Issued" || current[0].Raw["Status"] != "Issued" {
		t.Fatalf("expected issued current record with raw fields, got %+v", current[0])
	}
	counts, err := db.TableCounts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["permit_status_history"] != 2 {
		t.Fatalf("expected insert and update history events, got counts %+v", counts)
	}
}

func TestSQLiteAuditAndDirectImportMethods(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "permits.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	r := dedupe.Enrich(model.PermitRecord{
		SourceID:     "test",
		Jurisdiction: "City of Test",
		PermitNumber: "BP-2",
		Status:       "Issued",
		ScrapedAt:    "2026-05-24T20:00:00Z",
		FirstSeenAt:  "2026-05-24T20:00:00Z",
		LastSeenAt:   "2026-05-24T20:00:00Z",
	})
	if err := db.PutCurrent(r); err != nil {
		t.Fatal(err)
	}
	if err := db.AddHistory(model.StatusEvent{
		DedupeKey:      r.DedupeKey,
		SourceID:       r.SourceID,
		Jurisdiction:   r.Jurisdiction,
		PermitNumber:   r.PermitNumber,
		NewStatus:      r.Status,
		NewContentHash: r.ContentHash,
		ChangedAt:      r.LastSeenAt,
		Snapshot:       r,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.AddAudit(model.ScrapeAudit{RunID: "run-1", SourceID: "test", Status: "ok", RecordsSeen: 1}); err != nil {
		t.Fatal(err)
	}

	counts, err := db.TableCounts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["permit_current"] != 1 || counts["permit_status_history"] != 1 || counts["scrape_audit"] != 1 {
		t.Fatalf("bad counts after direct inserts: %+v", counts)
	}
	if err := db.Reset(); err != nil {
		t.Fatal(err)
	}
	counts, err = db.TableCounts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["permit_current"] != 0 || counts["permit_status_history"] != 0 || counts["scrape_audit"] != 0 {
		t.Fatalf("reset did not clear tables: %+v", counts)
	}
}
