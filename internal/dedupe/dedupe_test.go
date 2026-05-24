package dedupe

import (
	"testing"

	"github.com/example/bc-permit-scraper/internal/model"
)

func TestBuildKeyUsesPermitNumberAcrossSourceFormatting(t *testing.T) {
	a := model.PermitRecord{Jurisdiction: "City of Vancouver", PermitNumber: "BP-2024-001", PermitType: "Building Permit", Address: "123 Main Street"}
	b := model.PermitRecord{Jurisdiction: "city of vancouver", PermitNumber: "bp 2024 001", PermitType: "building permit", Address: "123 Main St"}
	if BuildKey(a) != BuildKey(b) {
		t.Fatalf("expected same key for same permit number")
	}
}

func TestBuildKeyAddressFallbackNormalizesStreet(t *testing.T) {
	a := model.PermitRecord{Jurisdiction: "District of North Vancouver", PermitType: "Plumbing Permit", Address: "4000 Mountain Highway", IssuedDate: "2025-01-01"}
	b := model.PermitRecord{Jurisdiction: "District of North Vancouver", PermitType: "plumbing permit", Address: "4000 Mountain Hwy", IssuedDate: "2025-01-01"}
	if BuildKey(a) != BuildKey(b) {
		t.Fatalf("expected same key for normalized address fallback")
	}
}
