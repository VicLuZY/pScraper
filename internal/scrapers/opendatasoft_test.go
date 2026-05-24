package scrapers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/example/bc-permit-scraper/internal/fetcher"
	"github.com/example/bc-permit-scraper/internal/model"
)

func TestOpenDataSoftScraperParsesRecords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total_count":1,"results":[{"permit_number":"BP-1","permit_type":"Building","status":"Issued","address":"1 Main St"}]}`))
	}))
	defer srv.Close()
	s := model.Source{ID: "test", Name: "Test", Jurisdiction: "City of Test", Kind: "opendatasoft_v2", URL: srv.URL, DatasetID: "dummy", FieldMap: map[string]string{"permit_number": "permit_number", "permit_type": "permit_type", "status": "status", "address": "address"}}
	scraper := OpenDataSoft{}
	recs, err := scraper.Scrape(context.Background(), fetcher.New("test", time.Second, time.Millisecond), s, Options{Limit: 10, MaxPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1, got %d", len(recs))
	}
	if recs[0].PermitNumber != "BP-1" || recs[0].Status != "Issued" {
		t.Fatalf("bad record: %+v", recs[0])
	}
}

func TestOpenDataSoftScraperPaginates(t *testing.T) {
	offsets := []int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
		if err != nil {
			t.Fatalf("bad offset: %v", err)
		}
		offsets = append(offsets, offset)
		count := 0
		switch offset {
		case 0:
			count = 100
		case 100:
			count = 2
		default:
			count = 0
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"total_count":102,"results":[`)
		for i := 0; i < count; i++ {
			if i > 0 {
				_, _ = fmt.Fprint(w, ",")
			}
			_, _ = fmt.Fprintf(w, `{"permit_number":"BP-%03d","permit_type":"Building","status":"Issued","address":"%d Main St"}`, offset+i, offset+i)
		}
		_, _ = fmt.Fprint(w, `]}`)
	}))
	defer srv.Close()

	s := model.Source{ID: "test", Name: "Test", Jurisdiction: "City of Test", Kind: "opendatasoft_v2", URL: srv.URL, DatasetID: "dummy", FieldMap: map[string]string{"permit_number": "permit_number", "permit_type": "permit_type", "status": "status", "address": "address"}}
	recs, err := (OpenDataSoft{}).Scrape(context.Background(), fetcher.New("test", time.Second, time.Millisecond), s, Options{MaxPages: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 102 {
		t.Fatalf("expected 102 records, got %d", len(recs))
	}
	if len(offsets) != 2 || offsets[0] != 0 || offsets[1] != 100 {
		t.Fatalf("expected offsets [0 100], got %v", offsets)
	}
}
