package mapdata

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/example/bc-permit-scraper/internal/model"
)

type Summary struct {
	DBPath     string         `json:"db_path"`
	Total      int            `json:"total"`
	Mapped     int            `json:"mapped"`
	Unmapped   int            `json:"unmapped"`
	Sources    map[string]int `json:"sources"`
	Statuses   map[string]int `json:"statuses"`
	LastSeenAt string         `json:"last_seen_at,omitempty"`
}

func LoadRecords(path string) ([]model.PermitRecord, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s does not exist; run permit-scraper first", path)
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 32*1024*1024)
	records := []model.PermitRecord{}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec model.PermitRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		records = append(records, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func LoadAudit(path string, limit int) ([]model.ScrapeAudit, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return []model.ScrapeAudit{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	rows := []model.ScrapeAudit{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256*1024), 8*1024*1024)
	for sc.Scan() {
		var audit model.ScrapeAudit
		if err := json.Unmarshal(sc.Bytes(), &audit); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		rows = append(rows, audit)
		if limit > 0 && len(rows) > limit {
			rows = rows[len(rows)-limit:]
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func Summarize(dbDir string, records []model.PermitRecord) Summary {
	sum := Summary{
		DBPath:   dbDir,
		Total:    len(records),
		Sources:  map[string]int{},
		Statuses: map[string]int{},
	}
	for _, rec := range records {
		source := firstNonEmpty(rec.SourceName, rec.SourceID, "Unknown source")
		status := summaryStatus(rec.Status)
		sum.Sources[source]++
		sum.Statuses[status]++
		if _, _, ok := recordLatLon(rec); ok {
			sum.Mapped++
		}
		sum.LastSeenAt = laterTime(sum.LastSeenAt, firstNonEmpty(rec.LastSeenAt, rec.ScrapedAt))
	}
	sum.Unmapped = sum.Total - sum.Mapped
	return sum
}

func summaryStatus(status string) string {
	status = firstNonEmpty(status, "No status")
	return strings.ToUpper(status)
}

func recordLatLon(rec model.PermitRecord) (float64, float64, bool) {
	candidates := [][2]string{
		{rec.Latitude, rec.Longitude},
		{rawValue(rec.Raw, "geo_point_2d.lat"), rawValue(rec.Raw, "geo_point_2d.lon")},
		{rawValue(rec.Raw, "Y_LAT"), rawValue(rec.Raw, "X_LONG")},
		{rawValue(rec.Raw, "latitude"), rawValue(rec.Raw, "longitude")},
		{rawValue(rec.Raw, "geometry.y"), rawValue(rec.Raw, "geometry.x")},
	}
	for _, pair := range candidates {
		lat, latErr := strconv.ParseFloat(strings.TrimSpace(pair[0]), 64)
		lon, lonErr := strconv.ParseFloat(strings.TrimSpace(pair[1]), 64)
		if latErr == nil && lonErr == nil && validLatLon(lat, lon) {
			return lat, lon, true
		}
	}
	return 0, 0, false
}

func rawValue(raw map[string]string, key string) string {
	for k, v := range raw {
		if strings.EqualFold(strings.TrimSpace(k), key) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func validLatLon(lat, lon float64) bool {
	return lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
}

func firstNonEmpty(vals ...string) string {
	for _, val := range vals {
		if strings.TrimSpace(val) != "" {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

func laterTime(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	at, aErr := time.Parse(time.RFC3339, a)
	bt, bErr := time.Parse(time.RFC3339, b)
	if aErr != nil || bErr != nil {
		if b > a {
			return b
		}
		return a
	}
	if bt.After(at) {
		return b
	}
	return a
}
