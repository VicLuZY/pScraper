package scrapers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/bc-permit-scraper/internal/fetcher"
	"github.com/example/bc-permit-scraper/internal/model"
)

func TestHTMLTableSkipsRowsWithoutPermitSignal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<table>
			<tr><th>Bulletins</th><th>Date</th></tr>
			<tr><td>Housing Target Progress Report</td><td>2026-01-01</td></tr>
		</table>`))
	}))
	defer srv.Close()

	source := model.Source{ID: "html", Name: "HTML", Jurisdiction: "City of Test", Kind: "html_table", URL: srv.URL, PermitTypes: []string{"Building permit"}}
	recs, err := (HTMLTable{}).Scrape(context.Background(), fetcher.New("test", time.Second, time.Millisecond), source, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("expected no records, got %+v", recs)
	}
}

func TestHTMLTableKeepsRowsWithMappedPermitSignal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<table>
			<tr><th>File</th><th>Address</th><th>Description</th></tr>
			<tr><td>DP2604D</td><td>1339 Hamill Lane</td><td>Watercourse Development Permit</td></tr>
		</table>`))
	}))
	defer srv.Close()

	source := model.Source{
		ID:           "html",
		Name:         "HTML",
		Jurisdiction: "City of Test",
		Kind:         "html_table",
		URL:          srv.URL,
		FieldMap:     map[string]string{"application_id": "File", "address": "Address", "description": "Description"},
	}
	recs, err := (HTMLTable{}).Scrape(context.Background(), fetcher.New("test", time.Second, time.Millisecond), source, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].ApplicationID != "DP2604D" || recs[0].Address != "1339 Hamill Lane" {
		t.Fatalf("bad records: %+v", recs)
	}
}
