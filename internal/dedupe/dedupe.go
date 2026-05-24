package dedupe

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/example/bc-permit-scraper/internal/model"
	"github.com/example/bc-permit-scraper/internal/normalize"
)

func BuildKey(r model.PermitRecord) string {
	jurisdiction := normalize.Text(r.Jurisdiction)
	permitType := normalize.Text(r.PermitType)
	if permitType == "" {
		permitType = normalize.Text(r.PermitFamily)
	}
	permitNo := normalize.Compact(r.PermitNumber)
	appID := normalize.Compact(r.ApplicationID)
	pid := normalize.Compact(r.PID)
	roll := normalize.Compact(r.RollNumber)
	address := normalize.Address(r.Address)
	date := firstNonEmpty(r.IssuedDate, r.AppliedDate, r.CompletedDate, r.FinalDate)

	parts := []string{jurisdiction}
	switch {
	case permitNo != "":
		parts = append(parts, "permit", permitNo)
	case appID != "":
		parts = append(parts, "application", appID)
	case pid != "":
		parts = append(parts, "pid", pid, permitType, date)
	case roll != "":
		parts = append(parts, "roll", roll, permitType, date)
	case address != "":
		parts = append(parts, "address", address, permitType, date, short(normalize.Text(r.Description), 80))
	default:
		parts = append(parts, "hash", normalize.Text(r.SourceID), permitType, date, short(normalize.Text(r.Description), 120))
	}
	raw := strings.Join(parts, "|")
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func Enrich(r model.PermitRecord) model.PermitRecord {
	r.DedupeKey = BuildKey(r)
	r.ContentHash = model.HashRecord(r)
	return r
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v != "" {
			return normalize.Text(v)
		}
	}
	return ""
}

func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
