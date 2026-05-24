package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/example/bc-permit-scraper/internal/mapdata"
	"github.com/example/bc-permit-scraper/internal/model"
)

func TestAPIRecordsAndSummary(t *testing.T) {
	dbDir := t.TempDir()
	records := []model.PermitRecord{
		{
			SourceID:     "vancouver",
			SourceName:   "Vancouver",
			Jurisdiction: "City of Vancouver",
			PermitNumber: "BP-1",
			Status:       "Issued",
			Raw:          map[string]string{"geo_point_2d.lat": "49.25", "geo_point_2d.lon": "-123.1"},
			LastSeenAt:   "2026-05-24T20:00:00Z",
		},
		{
			SourceID:     "csrd",
			SourceName:   "CSRD",
			Jurisdiction: "CSRD",
			PermitNumber: "3840 19 01",
			Status:       "Closed",
			Latitude:     "5645559.45",
			Longitude:    "417472.92",
			LastSeenAt:   "2026-05-24T20:01:00Z",
		},
	}
	writeJSONL(t, filepath.Join(dbDir, "current.jsonl"), records)

	app := appServer{dbDir: dbDir, webDir: t.TempDir()}
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/records")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("records status %d", resp.StatusCode)
	}
	var got []model.PermitRecord
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 records, got %d", len(got))
	}

	resp, err = http.Get(srv.URL + "/api/summary")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var summary mapdata.Summary
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	if summary.Total != 2 || summary.Mapped != 1 || summary.Unmapped != 1 {
		t.Fatalf("bad summary: %+v", summary)
	}
	if summary.LastSeenAt != "2026-05-24T20:01:00Z" {
		t.Fatalf("expected latest last_seen_at, got %q", summary.LastSeenAt)
	}
	if summary.Statuses["ISSUED"] != 1 || summary.Statuses["CLOSED"] != 1 {
		t.Fatalf("expected normalized status counts, got %+v", summary.Statuses)
	}
}

func TestAPIAuditLimit(t *testing.T) {
	dbDir := t.TempDir()
	rows := []model.ScrapeAudit{
		{SourceID: "a", Status: "ok"},
		{SourceID: "b", Status: "endpoint_needed"},
	}
	writeJSONL(t, filepath.Join(dbDir, "scrape_audit.jsonl"), rows)

	app := appServer{dbDir: dbDir, webDir: t.TempDir()}
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/audit?limit=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got []model.ScrapeAudit
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceID != "b" {
		t.Fatalf("bad audit rows: %+v", got)
	}
}

func TestAPIProgress(t *testing.T) {
	dbDir := t.TempDir()
	progress := model.ScrapeRunProgress{
		RunID:     "run-1",
		Total:     1,
		Completed: 1,
		Sources: []model.SourceProgress{{
			SourceID: "test",
			Status:   "ok",
			Progress: 100,
		}},
	}
	b, err := json.Marshal(progress)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dbDir, "scrape_progress.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	app := appServer{dbDir: dbDir, webDir: t.TempDir()}
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/progress")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got model.ScrapeRunProgress
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Completed != 1 || len(got.Sources) != 1 || got.Sources[0].Progress != 100 {
		t.Fatalf("bad progress response: %+v", got)
	}
}

func TestMapServesEmbeddedWebWhenWebDirMissing(t *testing.T) {
	app := appServer{dbDir: t.TempDir(), webDir: filepath.Join(t.TempDir(), "missing-web")}
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected embedded index status 200, got %d", resp.StatusCode)
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
