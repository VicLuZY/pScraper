package scrapers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/example/bc-permit-scraper/internal/fetcher"
	"github.com/example/bc-permit-scraper/internal/model"
)

func TestVancouverPOSSEDateSearchPostsDateWindow(t *testing.T) {
	var sawPost bool
	var sawDetail bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.URL.Path == "/detail" && r.URL.Query().Get("PosseObjectId") == "123" {
				sawDetail = true
				_, _ = w.Write([]byte(`<html><head><title>Building Permit BP-2026-01657</title></head><body>
					<div id="ctl00_cphTitleBand_pnlTitleBand">
						<span>Building Permit Application&nbsp;<span class="subprestitle">BP-2026-01657<div class="permitStatusDisplay">In Review</div></span></span>
					</div>
					<table class="possedetail">
						<tr><th><span class="posselabel">Application Date:</span></th><td></td><td><span id="ApplicationDate_1579987_297458092_sp">May, 19,  2026</span></td></tr>
						<tr><th><span class="posselabel">Primary Location:</span></th><td></td><td><span id="PermitLocation_1579826_297458092_sp">8188 MANITOBA STREET #424, Vancouver, BC</span></td></tr>
						<tr><th><span class="posselabel">Work Description:</span></th><td></td><td><span id="WorkDescription_1579826_297458092_sp">Interior alterations<br>Scope of work: Adding partitions.</span></td></tr>
					</table>
				</body></html>`))
				return
			}
			_, _ = w.Write([]byte(`<html><body>
				<script>PosseSubmitLink('http://example.test', 5, 987904);</script>
				<form id=possedocumentchangeform>
					<input id=currentpaneid name=currentpaneid value="1018439">
					<input id=sortcolumns name=sortcolumns value="{}">
					<input id=datachanges name=datachanges value="'seed-token'">
				</form>
			</body></html>`))
		case http.MethodPost:
			sawPost = true
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("currentpaneid") != "1018439" || r.Form.Get("paneid") != "987904" || r.Form.Get("functiondef") != "5" {
				t.Fatalf("bad POSSE form values: %v", r.Form)
			}
			changes := r.Form.Get("datachanges")
			if !strings.Contains(changes, "972146") || !strings.Contains(changes, "984849") {
				t.Fatalf("date columns missing from datachanges: %s", changes)
			}
			_, _ = w.Write([]byte(`<table class="possegrid">
				<tr><th></th><th>Type</th><th>Number</th><th>Location</th><th>Status</th><th>Created Date</th><th>Issue Date</th><th>Completed Date</th></tr>
				<tr>
					<td><a href="/detail?PosseObjectId=123">open</a></td>
					<td>Building Permit</td><td>BP-2026-01657</td><td>8188 MANITOBA STREET, Vancouver, BC</td><td>In Review</td><td>May 19, 2026</td><td></td><td></td>
				</tr>
			</table>`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer srv.Close()

	source := model.Source{
		ID:               "vancouver_public_permit_search",
		Name:             "City of Vancouver Application and Permit Search",
		Jurisdiction:     "City of Vancouver",
		Kind:             "vancouver_posse_date_search",
		URL:              srv.URL,
		PermitFamilies:   []string{"Building and construction"},
		OpenlySearchable: true,
		FieldMap: map[string]string{
			"permit_number":  "Number",
			"permit_type":    "Type",
			"status":         "Status",
			"address":        "Location",
			"applied_date":   "Created Date",
			"issued_date":    "Issue Date",
			"completed_date": "Completed Date",
			"url":            "Vancouver Object URL",
		},
	}
	dataDir := t.TempDir()
	recs, err := (VancouverPOSSEDateSearch{}).Scrape(context.Background(), fetcher.New("test", time.Second, time.Millisecond), source, Options{MaxPages: 1, DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	if !sawPost {
		t.Fatal("expected POSSE POST")
	}
	if !sawDetail {
		t.Fatal("expected detail GET")
	}
	if len(recs) != 1 {
		t.Fatalf("expected one record, got %+v", recs)
	}
	if recs[0].PermitNumber != "BP-2026-01657" || recs[0].AppliedDate != "2026-05-19" || !strings.Contains(recs[0].URL, "PosseObjectId=123") {
		t.Fatalf("bad record: %+v", recs[0])
	}
	if recs[0].Address != "8188 MANITOBA STREET #424, Vancouver, BC" || recs[0].Description != "Interior alterations Scope of work: Adding partitions." {
		t.Fatalf("bad record: %+v", recs[0])
	}
	if recs[0].Raw["Detail WorkDescription"] == "" || !strings.Contains(recs[0].Raw["Detail Page Text"], "Building Permit Application") {
		t.Fatalf("expected detail raw fields, got %+v", recs[0].Raw)
	}
	if _, err := os.Stat(filepath.Join(dataDir, vancouverIndexDBFileName)); err != nil {
		t.Fatalf("expected SQLite index file: %v", err)
	}
	store, err := openVancouverIndexStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	count, err := store.Count()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 indexed row, got %d", count)
	}
}

func TestVancouverPOSSEDateSearchSplitsCappedIndexWindows(t *testing.T) {
	var windows []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`<html><body>
				<script>PosseSubmitLink('http://example.test', 5, 987904);</script>
				<form id=possedocumentchangeform>
					<input id=currentpaneid name=currentpaneid value="1018439">
					<input id=sortcolumns name=sortcolumns value="{}">
					<input id=datachanges name=datachanges value="'seed-token'">
				</form>
			</body></html>`))
		case http.MethodPost:
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			changes := r.Form.Get("datachanges")
			switch {
			case strings.Contains(changes, "2026-01-01 00:00:00") && strings.Contains(changes, "2026-01-07 00:00:00"):
				windows = append(windows, "2026-01-01:2026-01-07")
				_, _ = w.Write([]byte(vancouverTestSearchRows("cap", 1000, "Jan 1, 2026")))
			case strings.Contains(changes, "2026-01-01 00:00:00") && strings.Contains(changes, "2026-01-03 00:00:00"):
				windows = append(windows, "2026-01-01:2026-01-03")
				_, _ = w.Write([]byte(vancouverTestSearchRows("left", 1, "Jan 1, 2026")))
			case strings.Contains(changes, "2026-01-04 00:00:00") && strings.Contains(changes, "2026-01-07 00:00:00"):
				windows = append(windows, "2026-01-04:2026-01-07")
				_, _ = w.Write([]byte(vancouverTestSearchRows("right", 1, "Jan 4, 2026")))
			default:
				t.Fatalf("unexpected datachanges: %s", changes)
			}
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer srv.Close()

	source := model.Source{
		ID:               "vancouver_public_permit_search",
		Name:             "City of Vancouver Application and Permit Search",
		Jurisdiction:     "City of Vancouver",
		Kind:             "vancouver_posse_date_search",
		URL:              srv.URL,
		PermitFamilies:   []string{"Building and construction"},
		OpenlySearchable: true,
		FieldMap: map[string]string{
			"permit_number":  "Number",
			"permit_type":    "Type",
			"status":         "Status",
			"address":        "Location",
			"applied_date":   "Created Date",
			"issued_date":    "Issue Date",
			"completed_date": "Completed Date",
			"url":            "Vancouver Object URL",
		},
	}
	dataDir := t.TempDir()
	recs, err := (VancouverPOSSEDateSearch{}).Scrape(context.Background(), fetcher.New("test", time.Second, 0), source, Options{
		FromDate:     "2026-01-01",
		ToDate:       "2026-01-07",
		DataDir:      dataDir,
		IndexWorkers: 1,
		IndexOnly:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("index-only should not return detail records: %+v", recs)
	}
	wantWindows := []string{"2026-01-01:2026-01-07", "2026-01-01:2026-01-03", "2026-01-04:2026-01-07"}
	if strings.Join(windows, ",") != strings.Join(wantWindows, ",") {
		t.Fatalf("unexpected windows: got %v want %v", windows, wantWindows)
	}
	store, err := openVancouverIndexStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	entries, err := store.SelectEntries("2026-01-01", "2026-01-07", nil, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected only split child entries in index, got %d", len(entries))
	}
	for _, entry := range entries {
		if entry.SearchWindowCount != 1 || entry.SearchWindowCapped || entry.SearchSplitDepth != 1 {
			t.Fatalf("bad split metadata: %+v", entry)
		}
		if entry.Raw["Search Window Count"] != "1" || entry.Raw["Search Window Capped"] != "false" || entry.Raw["Search Split Depth"] != "1" {
			t.Fatalf("bad raw split metadata: %+v", entry.Raw)
		}
	}
}

func TestVancouverDetailStatusFilter(t *testing.T) {
	cases := []struct {
		filter string
		status string
		want   bool
	}{
		{filter: "all", status: "", want: true},
		{filter: "", status: vancouverDetailStatusScraped, want: true},
		{filter: "pending,error", status: "", want: true},
		{filter: "pending,error", status: vancouverDetailStatusError, want: true},
		{filter: "pending,error", status: vancouverDetailStatusScraped, want: false},
		{filter: "errors", status: vancouverDetailStatusError, want: true},
		{filter: "not_processed", status: "", want: true},
		{filter: "scraped", status: "", want: false},
	}
	for _, tc := range cases {
		got := vancouverDetailStatusAllowed(tc.status, vancouverDetailStatusSet(tc.filter))
		if got != tc.want {
			t.Fatalf("filter %q status %q: got %v want %v", tc.filter, tc.status, got, tc.want)
		}
	}
}

func vancouverTestSearchRows(prefix string, count int, created string) string {
	var b strings.Builder
	b.WriteString(`<table class="possegrid">
		<tr><th></th><th>Type</th><th>Number</th><th>Location</th><th>Status</th><th>Created Date</th><th>Issue Date</th><th>Completed Date</th></tr>`)
	for i := 0; i < count; i++ {
		id := prefix + strconv.Itoa(i)
		b.WriteString(`<tr><td><a href="/detail?PosseObjectId=`)
		b.WriteString(id)
		b.WriteString(`">open</a></td><td>Building Permit</td><td>BP-2026-`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`</td><td>100 MAIN STREET, Vancouver, BC</td><td>Issued</td><td>`)
		b.WriteString(created)
		b.WriteString(`</td><td></td><td></td></tr>`)
	}
	b.WriteString(`</table>`)
	return b.String()
}
