package scrapers

import (
	"context"
	"html"
	"regexp"
	"strings"

	"github.com/example/bc-permit-scraper/internal/fetcher"
	"github.com/example/bc-permit-scraper/internal/model"
)

type HTMLTable struct{}

var rowRE = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
var cellRE = regexp.MustCompile(`(?is)<t[dh][^>]*>(.*?)</t[dh]>`)
var scriptBlockRE = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>|<style\b[^>]*>.*?</style>`)
var tagRE = regexp.MustCompile(`(?is)<[^>]+>`)

func (HTMLTable) Scrape(ctx context.Context, client *fetcher.Client, source model.Source, opts Options) ([]model.PermitRecord, error) {
	b, _, err := client.Get(ctx, source.URL, source.Headers)
	if err != nil {
		return nil, err
	}
	htmlText := string(b)
	rows := rowRE.FindAllStringSubmatch(htmlText, -1)
	if len(rows) == 0 {
		return []model.PermitRecord{}, nil
	}
	headers := []string{}
	out := []model.PermitRecord{}
	for i, row := range rows {
		cells := cellRE.FindAllStringSubmatch(row[1], -1)
		if len(cells) == 0 {
			continue
		}
		vals := make([]string, 0, len(cells))
		for _, c := range cells {
			vals = append(vals, cleanCell(c[1]))
		}
		if i == 0 || len(headers) == 0 {
			headers = vals
			continue
		}
		raw := map[string]string{}
		for c, v := range vals {
			key := "col_" + strings.TrimSpace(string(rune('A'+c)))
			if c < len(headers) && strings.TrimSpace(headers[c]) != "" {
				key = headers[c]
			}
			raw[key] = v
		}
		rec := applyFieldMap(source, raw)
		if !hasPermitSignal(rec) {
			continue
		}
		out = append(out, rec)
		if opts.Limit > 0 && len(out) >= opts.Limit {
			break
		}
	}
	return out, nil
}

func cleanCell(s string) string {
	s = scriptBlockRE.ReplaceAllString(s, " ")
	s = tagRE.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

func hasPermitSignal(r model.PermitRecord) bool {
	return first(r.PermitNumber, r.ApplicationID, r.Address, r.PID, r.RollNumber, r.Description) != ""
}
