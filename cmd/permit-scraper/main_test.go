package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example/bc-permit-scraper/internal/model"
	"github.com/example/bc-permit-scraper/internal/scrapers"
	sqlitestore "github.com/example/bc-permit-scraper/internal/storage/sqlite"
)

func TestRunTryAllClassifiesUnsupportedSkip(t *testing.T) {
	cfgPath := writeTestConfig(t, `{
		"version":"test",
		"sources":[{
			"id":"login_source",
			"name":"Login Source",
			"jurisdiction":"City of Test",
			"kind":"applicant_login",
			"skip_reason":"requires an authorised applicant account"
		}]
	}`)
	dbDir := filepath.Join(t.TempDir(), "db")
	var out bytes.Buffer
	if err := run([]string{"--sources", cfgPath, "--db", dbDir, "--try-all"}, &out); err != nil {
		t.Fatal(err)
	}

	var sum summary
	if err := json.Unmarshal(out.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Skipped != 1 || len(sum.Errors) != 0 {
		t.Fatalf("expected one skip and no errors, got skipped=%d errors=%v", sum.Skipped, sum.Errors)
	}
	audit := readOnlyAudit(t, dbDir)
	if audit.Status != scrapers.StatusLoginOrAuthorizedOnly || !audit.Skipped {
		t.Fatalf("expected login skip audit, got %+v", audit)
	}
}

func TestRunFailFastReturnsOnSkip(t *testing.T) {
	cfgPath := writeTestConfig(t, `{
		"version":"test",
		"sources":[{
			"id":"search_source",
			"name":"Search Source",
			"jurisdiction":"City of Test",
			"kind":"public_search_needs_input",
			"skip_reason":"requires a user supplied permit number"
		}]
	}`)
	dbDir := filepath.Join(t.TempDir(), "db")
	var out bytes.Buffer
	err := run([]string{"--sources", cfgPath, "--db", dbDir, "--try-all", "--fail-fast"}, &out)
	if err == nil {
		t.Fatal("expected fail-fast to return skip error")
	}
	if out.Len() != 0 {
		t.Fatalf("expected no summary on fail-fast error, got %s", out.String())
	}
	audit := readOnlyAudit(t, dbDir)
	if audit.Status != scrapers.StatusRequiresSearchInput || !audit.Skipped {
		t.Fatalf("expected search-input skip audit, got %+v", audit)
	}
}

func TestRunClassifiesReportDownloadNeededAsEndpointNeeded(t *testing.T) {
	cfgPath := writeTestConfig(t, `{
		"version":"test",
		"sources":[{
			"id":"pdf_reports",
			"name":"PDF Reports",
			"jurisdiction":"City of Test",
			"kind":"report_download_needed",
			"skip_reason":"public PDF report parser is not configured"
		}]
	}`)
	dbDir := filepath.Join(t.TempDir(), "db")
	var out bytes.Buffer
	if err := run([]string{"--sources", cfgPath, "--db", dbDir, "--try-all"}, &out); err != nil {
		t.Fatal(err)
	}
	audit := readOnlyAudit(t, dbDir)
	if audit.Status != scrapers.StatusEndpointNeeded || !audit.Skipped {
		t.Fatalf("expected endpoint-needed report skip, got %+v", audit)
	}
}

func TestRunIngestsReportDownloadCSV(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte("Permit Number,Address\nBP-1,123 Main St\n"))
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, `{
		"version":"test",
		"sources":[{
			"id":"csv_reports",
			"name":"CSV Reports",
			"jurisdiction":"City of Test",
			"kind":"report_download",
			"url":"`+srv.URL+`",
			"enabled":true,
			"field_map":{"permit_number":"Permit Number","address":"Address"}
		}]
	}`)
	dbDir := filepath.Join(t.TempDir(), "db")
	var out bytes.Buffer
	if err := run([]string{"--sources", cfgPath, "--db", dbDir, "--all"}, &out); err != nil {
		t.Fatal(err)
	}
	var sum summary
	if err := json.Unmarshal(out.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.RecordsSeen != 1 || sum.Inserted != 1 {
		t.Fatalf("expected one inserted report row, got %+v", sum)
	}
	audit := readOnlyAudit(t, dbDir)
	if audit.Status != "ok" || audit.RecordsSeen != 1 {
		t.Fatalf("expected ok report audit, got %+v", audit)
	}
}

func TestRunSkipsDuplicateDedupeKeysWithinSourceBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"features":[
			{"attributes":{"OBJECTID":1,"FOLDER_NUMBER":"DP-1","STATUS":"ACTIVE","SUBJECT":"1 Main St"}},
			{"attributes":{"OBJECTID":2,"FOLDER_NUMBER":"DP-1","STATUS":"ACTIVE","SUBJECT":"1 Main St"}}
		]}`))
	}))
	defer srv.Close()

	cfgPath := writeTestConfig(t, `{
		"version":"test",
		"sources":[{
			"id":"arc_reports",
			"name":"Arc Reports",
			"jurisdiction":"City of Test",
			"kind":"arcgis_feature_service",
			"endpoint":"`+srv.URL+`",
			"enabled":true,
			"field_map":{"application_id":"FOLDER_NUMBER","status":"STATUS","address":"SUBJECT"}
		}]
	}`)
	dbDir := filepath.Join(t.TempDir(), "db")
	var out bytes.Buffer
	if err := run([]string{"--sources", cfgPath, "--db", dbDir, "--all"}, &out); err != nil {
		t.Fatal(err)
	}
	var first summary
	if err := json.Unmarshal(out.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.RecordsSeen != 2 || first.Inserted != 1 || first.Updated != 0 {
		t.Fatalf("expected two raw records but one insert, got %+v", first)
	}

	out.Reset()
	if err := run([]string{"--sources", cfgPath, "--db", dbDir, "--all"}, &out); err != nil {
		t.Fatal(err)
	}
	var second summary
	if err := json.Unmarshal(out.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.RecordsSeen != 2 || second.Inserted != 0 || second.Updated != 0 || second.Unchanged != 1 {
		t.Fatalf("expected duplicate row to be skipped without update churn, got %+v", second)
	}
	history := readJSONLLines(t, filepath.Join(dbDir, "history.jsonl"))
	if len(history) != 1 {
		t.Fatalf("expected one history event, got %d", len(history))
	}
}

func TestSelectSourcesAllKeepsDisabledRowsOut(t *testing.T) {
	srcs := []model.Source{
		{ID: "enabled", Enabled: true},
		{ID: "disabled", Enabled: false},
	}
	got := selectSources(srcs, "", true, false)
	if len(got) != 1 || got[0].ID != "enabled" {
		t.Fatalf("expected --all to select enabled rows only, got %+v", got)
	}
	got = selectSources(srcs, "", false, true)
	if len(got) != 2 {
		t.Fatalf("expected --try-all to select all rows, got %+v", got)
	}
}

func TestSQLiteDefaultDBPathUsesFile(t *testing.T) {
	if got := defaultDBPath("sqlite", "data/permits-db"); got != "data/permits.sqlite" {
		t.Fatalf("expected SQLite default file path, got %q", got)
	}
	if got := defaultDBPath("jsonl", "data/permits-db"); got != "data/permits-db" {
		t.Fatalf("expected JSONL default directory, got %q", got)
	}
}

func TestRunCanWriteSQLiteAudit(t *testing.T) {
	cfgPath := writeTestConfig(t, `{
		"version":"test",
		"sources":[{
			"id":"login_source",
			"name":"Login Source",
			"jurisdiction":"City of Test",
			"kind":"applicant_login",
			"skip_reason":"requires an authorised applicant account"
		}]
	}`)
	dbPath := filepath.Join(t.TempDir(), "permits.sqlite")
	var out bytes.Buffer
	if err := run([]string{"--sources", cfgPath, "--db", dbPath, "--store", "sqlite", "--try-all"}, &out); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	counts, err := db.TableCounts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["scrape_audit"] != 1 {
		t.Fatalf("expected one SQLite audit row, got %+v", counts)
	}
}

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sources.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readOnlyAudit(t *testing.T, dbDir string) model.ScrapeAudit {
	t.Helper()
	lines := readJSONLLines(t, filepath.Join(dbDir, "scrape_audit.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("expected one audit row, got %d", len(lines))
	}
	var audit model.ScrapeAudit
	if err := json.Unmarshal([]byte(lines[0]), &audit); err != nil {
		t.Fatal(err)
	}
	return audit
}

func readJSONLLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}
