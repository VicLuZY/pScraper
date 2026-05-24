package scrapers

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/example/bc-permit-scraper/internal/fetcher"
	"github.com/example/bc-permit-scraper/internal/model"
)

type ArcGISFeatureService struct{}

type arcResp struct {
	Features []struct {
		Attributes map[string]any `json:"attributes"`
		Geometry   map[string]any `json:"geometry"`
	} `json:"features"`
	ExceededTransferLimit bool `json:"exceededTransferLimit"`
	Error                 *struct {
		Message string   `json:"message"`
		Details []string `json:"details"`
	} `json:"error"`
}

func (ArcGISFeatureService) Scrape(ctx context.Context, client *fetcher.Client, source model.Source, opts Options) ([]model.PermitRecord, error) {
	endpoint := strings.TrimSpace(source.Endpoint)
	if endpoint == "" {
		reason := source.SkipReason
		if reason == "" {
			reason = "ArcGIS FeatureServer layer query endpoint is not configured"
		}
		return nil, NewSkipError(StatusEndpointNeeded, source.ID, reason)
	}
	pageSize := opts.Limit
	if pageSize <= 0 || pageSize > 2000 {
		pageSize = 1000
	}
	maxPages := opts.MaxPages
	if maxPages <= 0 {
		maxPages = 100000
	}
	out := []model.PermitRecord{}
	for page := 0; page < maxPages; page++ {
		u, err := withQuery(endpoint, map[string]string{
			"f":                 "json",
			"where":             "1=1",
			"outFields":         "*",
			"outSR":             "4326",
			"resultOffset":      strconv.Itoa(page * pageSize),
			"resultRecordCount": strconv.Itoa(pageSize),
			"returnGeometry":    "true",
		})
		if err != nil {
			return nil, err
		}
		b, _, err := client.Get(ctx, u, source.Headers)
		if err != nil {
			return nil, err
		}
		var resp arcResp
		if err := json.Unmarshal(b, &resp); err != nil {
			return nil, fmt.Errorf("parse arcgis response: %w", err)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("arcgis error: %s %v", resp.Error.Message, resp.Error.Details)
		}
		if len(resp.Features) == 0 {
			break
		}
		for _, f := range resp.Features {
			raw := flatten(f.Attributes, "")
			if f.Geometry != nil {
				geo := flatten(f.Geometry, "geometry")
				for k, v := range geo {
					raw[k] = v
				}
				if raw["latitude"] == "" && raw["geometry.y"] != "" {
					raw["latitude"] = raw["geometry.y"]
				}
				if raw["longitude"] == "" && raw["geometry.x"] != "" {
					raw["longitude"] = raw["geometry.x"]
				}
			}
			out = append(out, applyFieldMap(source, raw))
		}
		if opts.Limit > 0 && len(out) >= opts.Limit {
			return out[:opts.Limit], nil
		}
		if len(resp.Features) < pageSize && !resp.ExceededTransferLimit {
			break
		}
	}
	return out, nil
}
