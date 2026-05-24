package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example/bc-permit-scraper/internal/model"
)

func TestRunExportsStaticMap(t *testing.T) {
	dbDir := t.TempDir()
	webDir := t.TempDir()
	outDir := filepath.Join(t.TempDir(), "permit-map")

	writeFile(t, filepath.Join(webDir, "index.html"), `<!doctype html>
<html><head><link rel="stylesheet" href="/styles.css"></head><body><script src="/app.js" defer></script></body></html>`)
	writeFile(t, filepath.Join(webDir, "app.js"), `console.log(window.PERMIT_STATIC_DATA.records.length);`)
	writeFile(t, filepath.Join(webDir, "styles.css"), `body { margin: 0; }`)
	writeJSONL(t, filepath.Join(dbDir, "current.jsonl"), []model.PermitRecord{{
		SourceID:     "test",
		SourceName:   "Test Source",
		Jurisdiction: "City of Test",
		PermitNumber: "BP-1",
		Status:       "Issued",
		Latitude:     "49.25",
		Longitude:    "-123.1",
		ScrapedAt:    "2026-05-24T20:00:00Z",
	}})
	writeJSONL(t, filepath.Join(dbDir, "scrape_audit.jsonl"), []model.ScrapeAudit{{
		RunID:    "run-1",
		SourceID: "test",
		Status:   "ok",
	}})

	var out bytes.Buffer
	if err := run([]string{"--db", dbDir, "--web", webDir, "--out", outDir}, &out); err != nil {
		t.Fatal(err)
	}
	var sum exportSummary
	if err := json.Unmarshal(out.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Records != 1 || sum.AuditRows != 1 {
		t.Fatalf("bad export summary: %+v", sum)
	}

	index := readFile(t, filepath.Join(outDir, "index.html"))
	if !strings.Contains(index, `href="styles.css"`) || !strings.Contains(index, `src="data.js"`) || !strings.Contains(index, `src="app.js"`) {
		t.Fatalf("static index was not rewritten correctly: %s", index)
	}
	data := readFile(t, filepath.Join(outDir, "data.js"))
	if !strings.Contains(data, "window.PERMIT_STATIC_DATA") || !strings.Contains(data, `"total":1`) {
		t.Fatalf("data.js missing embedded payload: %s", data)
	}
}

func TestRunExportsStaticMapWithEmbeddedWeb(t *testing.T) {
	dbDir := t.TempDir()
	outDir := filepath.Join(t.TempDir(), "permit-map")
	writeJSONL(t, filepath.Join(dbDir, "current.jsonl"), []model.PermitRecord{{
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
	if err := run([]string{"--db", dbDir, "--web", filepath.Join(t.TempDir(), "missing-web"), "--out", outDir}, &out); err != nil {
		t.Fatal(err)
	}
	index := readFile(t, filepath.Join(outDir, "index.html"))
	if !strings.Contains(index, `src="data.js"`) || !strings.Contains(index, `src="app.js"`) {
		t.Fatalf("embedded static index was not generated correctly: %s", index)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
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
