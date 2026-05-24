package scrapers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/bc-permit-scraper/internal/fetcher"
	"github.com/example/bc-permit-scraper/internal/model"
)

func TestReportDownloadParsesDirectCSV(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte("Permit Number,Address,Issued,Description\nBP-1,123 Main St,24/05/2026,New deck\n"))
	}))
	defer srv.Close()

	source := reportSource(srv.URL)
	recs, err := (ReportDownload{}).Scrape(context.Background(), fetcher.New("test", time.Second, time.Millisecond), source, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].PermitNumber != "BP-1" || recs[0].IssuedDate != "2026-05-24" {
		t.Fatalf("bad records: %+v", recs)
	}
	if recs[0].Raw["report_url"] != srv.URL {
		t.Fatalf("expected report_url raw field, got %+v", recs[0].Raw)
	}
}

func TestReportDownloadDiscoversCSVFromIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/reports":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<a href="/permits.csv">May building permit report</a>`))
		case "/permits.csv":
			w.Header().Set("Content-Type", "text/csv")
			_, _ = w.Write([]byte("Permit Number,Address,Issued\nBP-2,456 Oak Ave,May-24-2026\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	recs, err := (ReportDownload{}).Scrape(context.Background(), fetcher.New("test", time.Second, time.Millisecond), reportSource(srv.URL+"/reports"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].PermitNumber != "BP-2" || recs[0].Address != "456 Oak Ave" {
		t.Fatalf("bad records: %+v", recs)
	}
}

func TestReportDownloadAuditsPDFWithoutParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.7"))
	}))
	defer srv.Close()

	_, err := (ReportDownload{}).Scrape(context.Background(), fetcher.New("test", time.Second, time.Millisecond), reportSource(srv.URL+"/monthly-report-pdf"), Options{})
	skip, ok := AsSkipError(err)
	if !ok {
		t.Fatalf("expected skip error, got %v", err)
	}
	if skip.Status != StatusEndpointNeeded || !strings.Contains(skip.Reason, "PDF parser is not configured") {
		t.Fatalf("bad skip: %+v", skip)
	}
}

func TestReportDownloadDiscoversPDFLinksFromIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/reports":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`
				<a href="/business-development/permits-licensing">Permits navigation</a>
				<a href="/media/file/april-building-permits">April building permit PDF</a>
			`))
		case "/media/file/april-building-permits":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-1.7"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	_, err := (ReportDownload{}).Scrape(context.Background(), fetcher.New("test", time.Second, time.Millisecond), reportSource(srv.URL+"/reports"), Options{MaxPages: 1})
	skip, ok := AsSkipError(err)
	if !ok {
		t.Fatalf("expected skip error, got %v", err)
	}
	if skip.Status != StatusEndpointNeeded || !strings.Contains(skip.Reason, "found 1 public PDF report link") {
		t.Fatalf("bad skip: %+v", skip)
	}
}

func reportSource(rawURL string) model.Source {
	return model.Source{
		ID:           "reports",
		Name:         "Reports",
		Jurisdiction: "City of Test",
		Kind:         "report_download",
		URL:          rawURL,
		FieldMap: map[string]string{
			"permit_number": "Permit Number",
			"address":       "Address",
			"issued_date":   "Issued",
			"description":   "Description",
		},
	}
}
