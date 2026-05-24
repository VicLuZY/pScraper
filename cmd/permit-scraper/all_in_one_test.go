package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example/bc-permit-scraper/internal/dedupe"
	"github.com/example/bc-permit-scraper/internal/model"
)

func TestRunDispatchesPortableExportMap(t *testing.T) {
	dbDir := t.TempDir()
	outDir := filepath.Join(t.TempDir(), "map-export")
	writePortableTestJSONL(t, filepath.Join(dbDir, "current.jsonl"), []model.PermitRecord{{
		SourceID:     "test",
		SourceName:   "Test Source",
		Jurisdiction: "City of Test",
		PermitNumber: "BP-1",
		Status:       "Issued",
		Latitude:     "49.25",
		Longitude:    "-123.1",
		ScrapedAt:    "2026-05-24T20:00:00Z",
	}})

	var out bytes.Buffer
	if err := run([]string{"export-map", "--db", dbDir, "--web", filepath.Join(t.TempDir(), "missing-web"), "--out", outDir}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "index.html")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "data.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "window.PERMIT_STATIC_DATA") {
		t.Fatalf("data.js missing embedded payload: %s", string(data))
	}
}

func TestRunDispatchesPortableDBImport(t *testing.T) {
	jsonlDir := t.TempDir()
	rec := dedupe.Enrich(model.PermitRecord{
		SourceID:     "test",
		SourceName:   "Test Source",
		Jurisdiction: "City of Test",
		PermitNumber: "BP-1",
		Status:       "Issued",
		ScrapedAt:    "2026-05-24T20:00:00Z",
	})
	writePortableTestJSONL(t, filepath.Join(jsonlDir, "current.jsonl"), []model.PermitRecord{rec})
	sqlitePath := filepath.Join(t.TempDir(), "permits.sqlite")

	var out bytes.Buffer
	if err := run([]string{"db", "import-jsonl", "--jsonl", jsonlDir, "--sqlite", sqlitePath}, &out); err != nil {
		t.Fatal(err)
	}
	var sum portableImportSummary
	if err := json.Unmarshal(out.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Current != 1 || sum.SQLite != sqlitePath {
		t.Fatalf("bad import summary: %+v", sum)
	}
	if _, err := os.Stat(sqlitePath); err != nil {
		t.Fatal(err)
	}
}

func writePortableTestJSONL[T any](t *testing.T, path string, rows []T) {
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
