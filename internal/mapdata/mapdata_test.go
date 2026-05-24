package mapdata

import (
	"path/filepath"
	"testing"
)

func TestEmbeddedDefaultPermitSnapshot(t *testing.T) {
	t.Chdir(t.TempDir())
	records, err := LoadRecords(filepath.Join("data", "permits-db", "current.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("expected embedded current records")
	}
	audit, err := LoadAudit(filepath.Join("data", "permits-db", "scrape_audit.jsonl"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) == 0 {
		t.Fatal("expected embedded audit rows")
	}
	progress, err := LoadProgress(filepath.Join("data", "permits-db", "scrape_progress.json"))
	if err != nil {
		t.Fatal(err)
	}
	if progress.Total == 0 || len(progress.Sources) == 0 {
		t.Fatalf("expected embedded progress, got %+v", progress)
	}
}
