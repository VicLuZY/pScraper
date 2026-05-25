package scrapers

import (
	"context"
	"fmt"

	"github.com/example/bc-permit-scraper/internal/fetcher"
	"github.com/example/bc-permit-scraper/internal/model"
)

type Options struct {
	MaxPages      int
	Limit         int
	DryRun        bool
	FromDate      string
	ToDate        string
	DataDir       string
	IndexWorkers  int
	DetailWorkers int
	IndexOnly     bool
	DetailOnly    bool
	DetailStatus  string
}

type Scraper interface {
	Scrape(ctx context.Context, client *fetcher.Client, source model.Source, opts Options) ([]model.PermitRecord, error)
}

type BatchCounts struct {
	RecordsSeen int
	Inserted    int
	Updated     int
	Unchanged   int
}

type RecordSink interface {
	PutRecords([]model.PermitRecord) (BatchCounts, error)
}

type StreamingScraper interface {
	ScrapeToSink(ctx context.Context, client *fetcher.Client, source model.Source, opts Options, sink RecordSink) error
}

func ForKind(kind string) (Scraper, error) {
	switch kind {
	case "opendatasoft_v2":
		return OpenDataSoft{}, nil
	case "arcgis_feature_service":
		return ArcGISFeatureService{}, nil
	case "html_table":
		return HTMLTable{}, nil
	case "nanaimo_whatsbuilding":
		return NanaimoWhatsBuilding{}, nil
	case "vancouver_posse_date_search":
		return VancouverPOSSEDateSearch{}, nil
	case "report_download", "report_download_needed":
		return ReportDownload{}, nil
	case "unsupported", "applicant_login", "public_search_needs_input", "application_hub", "authority_reference":
		return Unsupported{}, nil
	default:
		return nil, fmt.Errorf("unsupported source kind %q", kind)
	}
}
