package scrapers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/bc-permit-scraper/internal/fetcher"
	"github.com/example/bc-permit-scraper/internal/model"
)

func TestArcGISScraperParsesFeatures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"features":[{"attributes":{"PermitNumber":"BP-9","PermitType":"Building","Status":"Issued","Address":"9 Main St"},"geometry":{"x":-123.1,"y":49.2}}]}`))
	}))
	defer srv.Close()
	s := model.Source{ID: "arc", Name: "Arc", Jurisdiction: "City of Test", Kind: "arcgis_feature_service", Endpoint: srv.URL, FieldMap: map[string]string{"permit_number": "PermitNumber", "permit_type": "PermitType", "status": "Status", "address": "Address", "latitude": "latitude", "longitude": "longitude"}}
	recs, err := (ArcGISFeatureService{}).Scrape(context.Background(), fetcher.New("test", time.Second, time.Millisecond), s, Options{Limit: 10, MaxPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1, got %d", len(recs))
	}
	if recs[0].PermitNumber != "BP-9" || recs[0].Latitude == "" || recs[0].Longitude == "" {
		t.Fatalf("bad record: %+v", recs[0])
	}
}

func TestArcGISScraperUsesGeometryWithoutExplicitFieldMap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"features":[{"attributes":{"PermitNumber":"BP-10"},"geometry":{"x":-119.37,"y":49.89}}]}`))
	}))
	defer srv.Close()
	s := model.Source{ID: "arc", Name: "Arc", Jurisdiction: "City of Test", Kind: "arcgis_feature_service", Endpoint: srv.URL, FieldMap: map[string]string{"permit_number": "PermitNumber"}}
	recs, err := (ArcGISFeatureService{}).Scrape(context.Background(), fetcher.New("test", time.Second, time.Millisecond), s, Options{Limit: 10, MaxPages: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Latitude != "49.89" || recs[0].Longitude != "-119.37" {
		t.Fatalf("expected canonical geometry fields, got %+v", recs)
	}
}

func TestArcGISScraperPaginates(t *testing.T) {
	offsets := []string{}
	outSRs := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset := r.URL.Query().Get("resultOffset")
		offsets = append(offsets, offset)
		outSRs = append(outSRs, r.URL.Query().Get("outSR"))
		w.Header().Set("Content-Type", "application/json")
		switch offset {
		case "0":
			_, _ = fmt.Fprint(w, `{"exceededTransferLimit":true,"features":[{"attributes":{"PermitNumber":"BP-1","PermitType":"Building","Status":"Issued","Address":"1 Main St"}}]}`)
		case "1000":
			_, _ = fmt.Fprint(w, `{"features":[{"attributes":{"PermitNumber":"BP-2","PermitType":"Building","Status":"Issued","Address":"2 Main St"}}]}`)
		default:
			_, _ = fmt.Fprint(w, `{"features":[]}`)
		}
	}))
	defer srv.Close()

	s := model.Source{ID: "arc", Name: "Arc", Jurisdiction: "City of Test", Kind: "arcgis_feature_service", Endpoint: srv.URL, FieldMap: map[string]string{"permit_number": "PermitNumber", "permit_type": "PermitType", "status": "Status", "address": "Address"}}
	recs, err := (ArcGISFeatureService{}).Scrape(context.Background(), fetcher.New("test", time.Second, time.Millisecond), s, Options{MaxPages: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}
	if len(offsets) != 2 || offsets[0] != "0" || offsets[1] != "1000" {
		t.Fatalf("expected offsets [0 1000], got %v", offsets)
	}
	if len(outSRs) != 2 || outSRs[0] != "4326" || outSRs[1] != "4326" {
		t.Fatalf("expected outSR=4326 on every request, got %v", outSRs)
	}
}
