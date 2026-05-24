package scrapers

import (
	"context"
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/example/bc-permit-scraper/internal/fetcher"
	"github.com/example/bc-permit-scraper/internal/model"
)

type NanaimoWhatsBuilding struct{}

var nanaimoApplicationRE = regexp.MustCompile(`(?is)<li>\s*([A-Z]{1,8}\d{3,})\s*<a\s+href=["']([^"']+)["'][^>]*>(.*?)</a>\s*([^<]*)</li>`)

func (NanaimoWhatsBuilding) Scrape(ctx context.Context, client *fetcher.Client, source model.Source, opts Options) ([]model.PermitRecord, error) {
	target, err := requireEndpoint(source)
	if err != nil {
		return nil, NewSkipError(StatusEndpointNeeded, source.ID, err.Error())
	}
	body, _, err := client.Get(ctx, target, source.Headers)
	if err != nil {
		return nil, err
	}
	base, _ := url.Parse(target)
	matches := nanaimoApplicationRE.FindAllStringSubmatch(string(body), -1)
	out := make([]model.PermitRecord, 0, len(matches))
	for _, m := range matches {
		id := strings.TrimSpace(html.UnescapeString(m[1]))
		href := strings.TrimSpace(html.UnescapeString(m[2]))
		title := cleanCell(m[3])
		category := strings.TrimSpace(html.UnescapeString(m[4]))
		resolved := href
		if u, err := url.Parse(href); err == nil && base != nil {
			resolved = base.ResolveReference(u).String()
		}
		address, description := splitNanaimoTitle(title)
		raw := map[string]string{
			"application_id": id,
			"href":           href,
			"title":          title,
			"category":       category,
			"status":         "Active",
			"url":            resolved,
		}
		rec := model.PermitRecord{
			SourceID:         source.ID,
			SourceName:       source.Name,
			Jurisdiction:     source.Jurisdiction,
			JurisdictionType: source.JurisdictionType,
			Region:           source.Region,
			ApplicationID:    id,
			PermitType:       first(description, category, strings.Join(source.PermitTypes, "; ")),
			PermitFamily:     strings.Join(source.PermitFamilies, "; "),
			Status:           "Active",
			Address:          address,
			Description:      description,
			URL:              resolved,
			Raw:              raw,
			ScrapedAt:        model.NowUTC(),
		}
		if strings.HasPrefix(strings.ToUpper(id), "BP") {
			rec.PermitNumber = id
		}
		out = append(out, rec)
		if opts.Limit > 0 && len(out) >= opts.Limit {
			break
		}
	}
	return out, nil
}

func splitNanaimoTitle(title string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(title), " - ", 2)
	if len(parts) == 1 {
		return strings.TrimSpace(title), ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}
