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

func TestNanaimoWhatsBuildingParsesAllActiveList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<ul>
			<li>BOV00796 <a href="/whatsbuilding/Folder/BOV00796">5686 Big Bear Ridge - Board of Variance</a> Development</li>
			<li>BP131536 <a href="/whatsbuilding/Folder/BP131536">6700 Island Highway N, (Costco Refrigeration Project) - Commercial / Multi-Res Alteration</a> Building Permits</li>
		</ul>`))
	}))
	defer srv.Close()

	source := model.Source{
		ID:               "nanaimo_whats_building",
		Name:             "Nanaimo What's Building",
		Jurisdiction:     "City of Nanaimo",
		JurisdictionType: "Municipality",
		Region:           "Central Vancouver Island",
		Kind:             "nanaimo_whatsbuilding",
		URL:              srv.URL + "/whatsbuilding/AllActiveApplications",
		PermitFamilies:   []string{"Building and construction", "Development and planning"},
	}
	recs, err := (NanaimoWhatsBuilding{}).Scrape(context.Background(), fetcher.New("test", time.Second, time.Millisecond), source, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %+v", recs)
	}
	if recs[0].ApplicationID != "BOV00796" || recs[0].PermitNumber != "" || recs[0].Address != "5686 Big Bear Ridge" || recs[0].Status != "Active" {
		t.Fatalf("bad development record: %+v", recs[0])
	}
	if recs[1].PermitNumber != "BP131536" || recs[1].Address != "6700 Island Highway N, (Costco Refrigeration Project)" {
		t.Fatalf("bad building permit record: %+v", recs[1])
	}
	if recs[1].URL != srv.URL+"/whatsbuilding/Folder/BP131536" {
		t.Fatalf("expected resolved URL, got %q", recs[1].URL)
	}
}

func TestNanaimoWhatsBuildingHonorsLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<li>BP001 <a href="/whatsbuilding/Folder/BP001">1 Main St - Building</a> Building Permits</li>
			<li>BP002 <a href="/whatsbuilding/Folder/BP002">2 Main St - Building</a> Building Permits</li>`))
	}))
	defer srv.Close()

	recs, err := (NanaimoWhatsBuilding{}).Scrape(context.Background(), fetcher.New("test", time.Second, time.Millisecond), model.Source{
		ID:           "nanaimo",
		Name:         "Nanaimo",
		Jurisdiction: "City of Nanaimo",
		URL:          srv.URL,
	}, Options{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 limited record, got %d", len(recs))
	}
}
