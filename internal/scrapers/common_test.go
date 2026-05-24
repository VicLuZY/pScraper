package scrapers

import "testing"

func TestNormalizeDateUnixMilliseconds(t *testing.T) {
	tests := map[string]string{
		"1481241600000": "2016-12-09",
		"-680400000000": "1948-06-10",
		"20260520":      "2026-05-20",
		"15/10/2025":    "2025-10-15",
		"May-21-2026":   "2026-05-21",
	}
	for input, want := range tests {
		if got := normalizeDate(input); got != want {
			t.Fatalf("normalizeDate(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestComposeAddressUsesStreetNumberAndName(t *testing.T) {
	raw := map[string]string{
		"Street Number": "11045",
		"Street Name":   "Kiitiwake",
	}
	if got := composeAddress(raw); got != "11045 Kiitiwake" {
		t.Fatalf("composeAddress() = %q", got)
	}
}
