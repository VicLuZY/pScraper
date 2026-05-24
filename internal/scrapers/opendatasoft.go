package scrapers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/example/bc-permit-scraper/internal/fetcher"
	"github.com/example/bc-permit-scraper/internal/model"
)

type OpenDataSoft struct{}

type odsResp struct {
	Results    []map[string]any `json:"results"`
	TotalCount int              `json:"total_count"`
}

func (OpenDataSoft) Scrape(ctx context.Context, client *fetcher.Client, source model.Source, opts Options) ([]model.PermitRecord, error) {
	if source.DatasetID == "" {
		return nil, NewSkipError(StatusEndpointNeeded, source.ID, "OpenDataSoft dataset_id is not configured")
	}
	base, err := requireEndpoint(source)
	if err != nil {
		return nil, NewSkipError(StatusEndpointNeeded, source.ID, err.Error())
	}
	limit := opts.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	maxPages := opts.MaxPages
	if maxPages <= 0 {
		maxPages = 100000
	}
	out := []model.PermitRecord{}
	for page := 0; page < maxPages; page++ {
		offset := page * limit
		u, err := withQuery(base, map[string]string{
			"limit":  strconv.Itoa(limit),
			"offset": strconv.Itoa(offset),
		})
		if err != nil {
			return nil, err
		}
		b, _, err := client.Get(ctx, u, source.Headers)
		if err != nil {
			return nil, err
		}
		var resp odsResp
		if err := json.Unmarshal(b, &resp); err != nil {
			return nil, fmt.Errorf("parse ods response: %w", err)
		}
		if len(resp.Results) == 0 {
			break
		}
		for _, item := range resp.Results {
			raw := flatten(item, "")
			out = append(out, applyFieldMap(source, raw))
		}
		if opts.Limit > 0 && len(out) >= opts.Limit {
			return out[:opts.Limit], nil
		}
		if len(resp.Results) < limit {
			break
		}
	}
	return out, nil
}

func flatten(m map[string]any, prefix string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch x := v.(type) {
		case map[string]any:
			nested := flatten(x, key)
			for nk, nv := range nested {
				out[nk] = nv
			}
		case []any:
			out[key] = fmt.Sprint(x)
		case nil:
			out[key] = ""
		case float64:
			out[key] = formatFloat(x)
		default:
			out[key] = fmt.Sprint(x)
		}
	}
	return out
}

func formatFloat(x float64) string {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return ""
	}
	if math.Trunc(x) == x {
		return strconv.FormatInt(int64(x), 10)
	}
	return strconv.FormatFloat(x, 'f', -1, 64)
}
