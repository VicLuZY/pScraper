package storage

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/example/bc-permit-scraper/internal/dedupe"
	"github.com/example/bc-permit-scraper/internal/model"
)

func TestJSONDBUpsertTracksInsertUnchangedUpdate(t *testing.T) {
	db, err := OpenJSONDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := dedupe.Enrich(model.PermitRecord{Jurisdiction: "City of Test", PermitNumber: "BP-1", PermitType: "Building", Status: "Submitted", ScrapedAt: model.NowUTC()})
	got, err := db.Upsert(r)
	if err != nil || got != Inserted {
		t.Fatalf("insert got %s err %v", got, err)
	}
	got, err = db.Upsert(r)
	if err != nil || got != Unchanged {
		t.Fatalf("unchanged got %s err %v", got, err)
	}
	r.Status = "Issued"
	r = dedupe.Enrich(r)
	got, err = db.Upsert(r)
	if err != nil || got != Updated {
		t.Fatalf("update got %s err %v", got, err)
	}
	if len(db.AllCurrent()) != 1 {
		t.Fatalf("expected 1 current record")
	}
	b, err := os.ReadFile(db.HistoryPath())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected insert and update history events, got %d", len(lines))
	}
	var evt model.StatusEvent
	if err := json.Unmarshal([]byte(lines[1]), &evt); err != nil {
		t.Fatal(err)
	}
	if evt.OldStatus != "Submitted" || evt.NewStatus != "Issued" {
		t.Fatalf("expected status transition Submitted -> Issued, got %q -> %q", evt.OldStatus, evt.NewStatus)
	}
}

func TestJSONDBSeedsDefaultCurrentFromBundle(t *testing.T) {
	t.Chdir(t.TempDir())
	db, err := OpenJSONDB("data/permits-db")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(db.AllCurrent()); got == 0 {
		t.Fatal("expected bundled current records to seed the default JSONL store")
	}
}
