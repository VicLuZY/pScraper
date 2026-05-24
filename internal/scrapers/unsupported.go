package scrapers

import (
	"context"

	"github.com/example/bc-permit-scraper/internal/fetcher"
	"github.com/example/bc-permit-scraper/internal/model"
)

type Unsupported struct{}

func (Unsupported) Scrape(ctx context.Context, client *fetcher.Client, source model.Source, opts Options) ([]model.PermitRecord, error) {
	reason := source.SkipReason
	if reason == "" {
		reason = "source is not safely downloadable without search input, login, or manual endpoint discovery"
	}
	return nil, NewSkipError(classifyUnsupported(source.Kind), source.ID, reason)
}

func classifyUnsupported(kind string) string {
	switch kind {
	case "public_search_needs_input":
		return StatusRequiresSearchInput
	case "applicant_login":
		return StatusLoginOrAuthorizedOnly
	case "report_download", "report_download_needed":
		return StatusEndpointNeeded
	case "application_hub", "authority_reference", "unsupported":
		return StatusNotPublicBulk
	default:
		return StatusNotPublicBulk
	}
}
