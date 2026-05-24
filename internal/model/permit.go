package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

type Source struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Jurisdiction     string            `json:"jurisdiction"`
	JurisdictionType string            `json:"jurisdiction_type"`
	Region           string            `json:"region"`
	Kind             string            `json:"kind"`
	URL              string            `json:"url"`
	Endpoint         string            `json:"endpoint,omitempty"`
	DatasetID        string            `json:"dataset_id,omitempty"`
	Enabled          bool              `json:"enabled"`
	DownloadAll      bool              `json:"download_all"`
	OpenlySearchable bool              `json:"openly_searchable"`
	NeedsInput       bool              `json:"needs_input"`
	PermitFamilies   []string          `json:"permit_families"`
	PermitTypes      []string          `json:"permit_types"`
	SearchKeys       []string          `json:"search_keys"`
	FieldMap         map[string]string `json:"field_map,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	SkipReason       string            `json:"skip_reason,omitempty"`
	Notes            string            `json:"notes,omitempty"`
}

type PermitRecord struct {
	SourceID         string            `json:"source_id"`
	SourceName       string            `json:"source_name"`
	Jurisdiction     string            `json:"jurisdiction"`
	JurisdictionType string            `json:"jurisdiction_type,omitempty"`
	Region           string            `json:"region,omitempty"`
	PermitNumber     string            `json:"permit_number,omitempty"`
	ApplicationID    string            `json:"application_id,omitempty"`
	PermitType       string            `json:"permit_type,omitempty"`
	PermitFamily     string            `json:"permit_family,omitempty"`
	Status           string            `json:"status,omitempty"`
	Address          string            `json:"address,omitempty"`
	PID              string            `json:"pid,omitempty"`
	RollNumber       string            `json:"roll_number,omitempty"`
	Applicant        string            `json:"applicant,omitempty"`
	Contractor       string            `json:"contractor,omitempty"`
	Description      string            `json:"description,omitempty"`
	AppliedDate      string            `json:"applied_date,omitempty"`
	IssuedDate       string            `json:"issued_date,omitempty"`
	FinalDate        string            `json:"final_date,omitempty"`
	CompletedDate    string            `json:"completed_date,omitempty"`
	Value            string            `json:"value,omitempty"`
	Latitude         string            `json:"latitude,omitempty"`
	Longitude        string            `json:"longitude,omitempty"`
	URL              string            `json:"url,omitempty"`
	Raw              map[string]string `json:"raw,omitempty"`
	DedupeKey        string            `json:"dedupe_key"`
	ContentHash      string            `json:"content_hash"`
	FirstSeenAt      string            `json:"first_seen_at,omitempty"`
	LastSeenAt       string            `json:"last_seen_at,omitempty"`
	LastChangedAt    string            `json:"last_changed_at,omitempty"`
	ScrapedAt        string            `json:"scraped_at"`
}

type StatusEvent struct {
	DedupeKey      string       `json:"dedupe_key"`
	SourceID       string       `json:"source_id"`
	Jurisdiction   string       `json:"jurisdiction"`
	PermitNumber   string       `json:"permit_number,omitempty"`
	ApplicationID  string       `json:"application_id,omitempty"`
	OldStatus      string       `json:"old_status,omitempty"`
	NewStatus      string       `json:"new_status,omitempty"`
	OldContentHash string       `json:"old_content_hash,omitempty"`
	NewContentHash string       `json:"new_content_hash"`
	ChangedAt      string       `json:"changed_at"`
	Snapshot       PermitRecord `json:"snapshot"`
}

type ScrapeAudit struct {
	RunID        string `json:"run_id"`
	SourceID     string `json:"source_id"`
	SourceName   string `json:"source_name"`
	Jurisdiction string `json:"jurisdiction"`
	Kind         string `json:"kind"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at"`
	Status       string `json:"status"`
	Message      string `json:"message,omitempty"`
	RecordsSeen  int    `json:"records_seen"`
	Inserted     int    `json:"inserted"`
	Updated      int    `json:"updated"`
	Unchanged    int    `json:"unchanged"`
	Skipped      bool   `json:"skipped"`
}

func NowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func HashStableMap(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(strings.ToLower(strings.TrimSpace(k))))
		h.Write([]byte{0})
		h.Write([]byte(strings.TrimSpace(m[k])))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func HashRecord(r PermitRecord) string {
	canonical := map[string]string{
		"source_id":      r.SourceID,
		"jurisdiction":   r.Jurisdiction,
		"permit_number":  r.PermitNumber,
		"application_id": r.ApplicationID,
		"permit_type":    r.PermitType,
		"permit_family":  r.PermitFamily,
		"status":         r.Status,
		"address":        r.Address,
		"pid":            r.PID,
		"roll_number":    r.RollNumber,
		"applicant":      r.Applicant,
		"contractor":     r.Contractor,
		"description":    r.Description,
		"applied_date":   r.AppliedDate,
		"issued_date":    r.IssuedDate,
		"final_date":     r.FinalDate,
		"completed_date": r.CompletedDate,
		"value":          r.Value,
		"url":            r.URL,
	}
	for k, v := range r.Raw {
		canonical["raw."+k] = v
	}
	return HashStableMap(canonical)
}

func CloneRecord(r PermitRecord) PermitRecord {
	if r.Raw != nil {
		raw := make(map[string]string, len(r.Raw))
		for k, v := range r.Raw {
			raw[k] = v
		}
		r.Raw = raw
	}
	return r
}

func ToJSONLine(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
