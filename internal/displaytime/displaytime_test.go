package displaytime

import (
	"testing"
	"time"
	_ "time/tzdata"
)

func TestFormatKeepsInstantAndDisambiguatesLocalTransitions(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ recorded, want string }{
		{"2026-01-01T01:30:00Z", "2025-12-31T20:30:00-05:00"},
		{"2026-03-08T06:59:59Z", "2026-03-08T01:59:59-05:00"},
		{"2026-03-08T07:00:00Z", "2026-03-08T03:00:00-04:00"},
		{"2026-11-01T05:30:00Z", "2026-11-01T01:30:00-04:00"},
		{"2026-11-01T06:30:00Z", "2026-11-01T01:30:00-05:00"},
		{"2026-11-01T06:30:00.123456789Z", "2026-11-01T01:30:00.123456789-05:00"},
	} {
		got, err := FromRFC3339(tc.recorded, location)
		if err != nil || got != tc.want {
			t.Fatalf("FromRFC3339(%q) = %q, %v; want %q", tc.recorded, got, err, tc.want)
		}
		// Formatting may change the view, but parsing the view must keep the instant.
		original, _ := time.Parse(time.RFC3339Nano, tc.recorded)
		shown, err := time.Parse(time.RFC3339Nano, got)
		if err != nil || !shown.Equal(original) {
			t.Fatalf("display changed instant %q -> %q: %v", tc.recorded, got, err)
		}
	}
	if got, err := FromRFC3339("2026-01-01T00:00:00Z", time.UTC); err != nil || got != "2026-01-01T00:00:00+00:00" {
		t.Fatalf("UTC display = %q, %v", got, err)
	}
	if got, err := FromRFC3339("not-a-time", location); err == nil || got != "" {
		t.Fatalf("malformed stored time = %q, %v", got, err)
	}
}
