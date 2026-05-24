package scrapers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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
				if lat, lon, ok := arcGeometryCenter(f.Geometry); ok {
					raw["latitude"] = lat
					raw["longitude"] = lon
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

func arcGeometryCenter(geo map[string]any) (string, string, bool) {
	x, xOK := numberValue(geo["x"])
	y, yOK := numberValue(geo["y"])
	if xOK && yOK {
		return formatFloat(y), formatFloat(x), true
	}

	b := geometryBounds{minX: math.Inf(1), minY: math.Inf(1), maxX: math.Inf(-1), maxY: math.Inf(-1)}
	for _, key := range []string{"rings", "paths", "points"} {
		collectGeometryBounds(geo[key], &b)
	}
	if !b.ok {
		return "", "", false
	}
	return formatFloat((b.minY + b.maxY) / 2), formatFloat((b.minX + b.maxX) / 2), true
}

type geometryBounds struct {
	minX float64
	minY float64
	maxX float64
	maxY float64
	ok   bool
}

func collectGeometryBounds(v any, b *geometryBounds) {
	switch x := v.(type) {
	case []any:
		if len(x) >= 2 {
			lon, lonOK := numberValue(x[0])
			lat, latOK := numberValue(x[1])
			if lonOK && latOK {
				b.add(lon, lat)
				return
			}
		}
		for _, item := range x {
			collectGeometryBounds(item, b)
		}
	}
}

func (b *geometryBounds) add(x, y float64) {
	if x < b.minX {
		b.minX = x
	}
	if x > b.maxX {
		b.maxX = x
	}
	if y < b.minY {
		b.minY = y
	}
	if y > b.maxY {
		b.maxY = y
	}
	b.ok = true
}

func numberValue(v any) (float64, bool) {
	n, ok := v.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, false
	}
	return n, true
}
