package scrapers

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/example/bc-permit-scraper/internal/model"
)

func applyFieldMap(source model.Source, raw map[string]string) model.PermitRecord {
	get := func(canonical string) string {
		field := source.FieldMap[canonical]
		if field == "" {
			return ""
		}
		for _, candidate := range strings.Split(field, "|") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				continue
			}
			if v, ok := raw[candidate]; ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
			// Try case-insensitive field match because civic datasets often change casing.
			for rk, rv := range raw {
				if strings.EqualFold(strings.TrimSpace(rk), candidate) && strings.TrimSpace(rv) != "" {
					return strings.TrimSpace(rv)
				}
			}
		}
		return ""
	}
	family := strings.Join(source.PermitFamilies, "; ")
	ptype := get("permit_type")
	if ptype == "" && len(source.PermitTypes) > 0 {
		ptype = strings.Join(source.PermitTypes, "; ")
	}
	address := first(get("address"), get("site_address"), get("civic_address"), composeAddress(raw))
	return model.PermitRecord{
		SourceID:         source.ID,
		SourceName:       source.Name,
		Jurisdiction:     source.Jurisdiction,
		JurisdictionType: source.JurisdictionType,
		Region:           source.Region,
		PermitNumber:     first(get("permit_number"), get("permit_no"), get("folder_number")),
		ApplicationID:    first(get("application_id"), get("application_number"), get("file_number")),
		PermitType:       ptype,
		PermitFamily:     family,
		Status:           first(get("status"), get("permit_status"), get("application_status")),
		Address:          address,
		PID:              get("pid"),
		RollNumber:       get("roll_number"),
		Applicant:        get("applicant"),
		Contractor:       get("contractor"),
		Description:      first(get("description"), get("work_description"), get("project_description")),
		AppliedDate:      normalizeDate(first(get("applied_date"), get("application_date"), get("date_applied"))),
		IssuedDate:       normalizeDate(first(get("issued_date"), get("issue_date"), get("date_issued"))),
		FinalDate:        normalizeDate(first(get("final_date"), get("date_final"))),
		CompletedDate:    normalizeDate(first(get("completed_date"), get("completion_date"))),
		Value:            first(get("value"), get("construction_value"), get("permit_value")),
		Latitude:         first(get("latitude"), get("lat"), rawFirst(raw, "latitude", "lat", "geometry.y", "Y_LAT", "geo_point_2d.lat")),
		Longitude:        first(get("longitude"), get("lon"), get("lng"), rawFirst(raw, "longitude", "lon", "lng", "geometry.x", "X_LONG", "geo_point_2d.lon")),
		URL:              first(get("url"), source.URL),
		Raw:              raw,
		ScrapedAt:        model.NowUTC(),
	}
}

func first(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func composeAddress(raw map[string]string) string {
	unit := rawFirst(raw, "Unit", "UNIT", "unit")
	house := rawFirst(raw, "House", "HOUSE", "house", "Street Number", "street_number")
	street := rawFirst(raw, "Street", "STREET", "street", "Street Name", "street_name")
	base := strings.TrimSpace(strings.Join(nonEmpty(house, street), " "))
	if base == "" {
		return ""
	}
	if unit != "" {
		return unit + "-" + base
	}
	return base
}

func rawFirst(raw map[string]string, names ...string) string {
	for _, name := range names {
		for k, v := range raw {
			if strings.EqualFold(strings.TrimSpace(k), name) && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

func nonEmpty(vals ...string) []string {
	out := []string{}
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}

func normalizeDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) == 8 && allDigits(s) {
		if t, err := time.Parse("20060102", s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	if len(s) >= 12 && allDigits(strings.TrimPrefix(s, "-")) {
		ms, err := strconv.ParseInt(s, 10, 64)
		if err == nil {
			return time.UnixMilli(ms).UTC().Format("2006-01-02")
		}
	}
	for _, layout := range []string{"02/01/2006", "02/01/06", "Jan-02-2006", "Jan 2, 2006", "Jan 02, 2006", "Jan, 2, 2006", "Jan, 02, 2006", "January 2, 2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return s
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func withQuery(base string, params map[string]string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func requireEndpoint(source model.Source) (string, error) {
	if source.Endpoint != "" {
		return source.Endpoint, nil
	}
	if source.URL != "" {
		return source.URL, nil
	}
	return "", fmt.Errorf("source %s has no url or endpoint", source.ID)
}
