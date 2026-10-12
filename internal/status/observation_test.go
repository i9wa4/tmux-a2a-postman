package status

import (
	"testing"
	"time"
)

func TestObserveScreenProgress(t *testing.T) {
	now := time.Date(2026, time.October, 13, 0, 0, 30, 0, time.UTC)
	base := ScreenProgressEvidence{
		EvidenceState:      "unchanged",
		LastCaptureAt:      now.Add(-5 * time.Second).Format(time.RFC3339Nano),
		LastScreenChangeAt: now.Add(-20 * time.Second).Format(time.RFC3339Nano),
		ScreenFingerprint:  "00000011",
	}
	alreadyStale := base
	alreadyStale.EvidenceState = "stale"
	for _, tc := range []struct {
		name       string
		progress   *ScreenProgressEvidence
		staleAfter time.Duration
		freshness  string
		source     string
		idleAge    *int
		conflict   bool
	}{
		{name: "missing pane", freshness: "unknown", source: "unknown"},
		{name: "fresh capture", progress: &base, staleAfter: 10 * time.Second, freshness: "fresh", source: "pane_capture", idleAge: intPointer(20)},
		{name: "at stale boundary", progress: screenProgressWithCapture(base, now.Add(-10*time.Second)), staleAfter: 10 * time.Second, freshness: "fresh", source: "pane_capture", idleAge: intPointer(20)},
		{name: "observation gap", progress: screenProgressWithCapture(base, now.Add(-11*time.Second)), staleAfter: 10 * time.Second, freshness: "stale", source: "pane_capture", idleAge: intPointer(20)},
		{name: "zero horizon", progress: screenProgressWithCapture(base, now.Add(-11*time.Second)), freshness: "unknown", source: "pane_capture", idleAge: intPointer(20)},
		{name: "negative horizon", progress: screenProgressWithCapture(base, now.Add(-11*time.Second)), staleAfter: -time.Second, freshness: "unknown", source: "pane_capture", idleAge: intPointer(20)},
		{name: "already stale with fingerprint at zero horizon", progress: &alreadyStale, freshness: "stale", source: "pane_capture", idleAge: intPointer(20)},
		{name: "already stale without fingerprint at zero horizon", progress: screenProgressWithoutFingerprint(alreadyStale), freshness: "stale", source: "pane_capture"},
		{name: "already stale with fingerprint at negative horizon", progress: &alreadyStale, staleAfter: -time.Second, freshness: "stale", source: "pane_capture", idleAge: intPointer(20)},
		{name: "already stale without fingerprint at negative horizon", progress: screenProgressWithoutFingerprint(alreadyStale), staleAfter: -time.Second, freshness: "stale", source: "pane_capture"},
		{name: "future capture conflicts", progress: screenProgressWithCapture(base, now.Add(time.Second)), staleAfter: 10 * time.Second, freshness: "unknown", source: "pane_capture", conflict: true},
		{name: "capture failure without timestamp", progress: screenProgressWithCapture(base, time.Time{}), freshness: "unknown", source: "unknown"},
		{name: "invalid change timestamp", progress: screenProgressWithChangeString(base, "invalid"), staleAfter: 10 * time.Second, freshness: "fresh", source: "pane_capture"},
		{name: "past change after capture conflicts", progress: screenProgressWithChange(base, now.Add(-time.Second)), staleAfter: 10 * time.Second, freshness: "fresh", source: "pane_capture", conflict: true},
		{name: "future change conflicts", progress: screenProgressWithChange(base, now.Add(time.Second)), staleAfter: 10 * time.Second, freshness: "fresh", source: "pane_capture", conflict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ObserveScreenProgress(tc.progress, now, tc.staleAfter)
			if got.Freshness != tc.freshness || got.Source != tc.source || got.Conflict != tc.conflict {
				t.Fatalf("observation = %#v, want freshness=%q source=%q conflict=%v", got, tc.freshness, tc.source, tc.conflict)
			}
			if (got.IdleAgeSeconds == nil) != (tc.idleAge == nil) {
				t.Fatalf("idle age = %v, want %v", got.IdleAgeSeconds, tc.idleAge)
			}
			if got.IdleAgeSeconds != nil && *got.IdleAgeSeconds != *tc.idleAge {
				t.Fatalf("idle age = %d, want %d", *got.IdleAgeSeconds, *tc.idleAge)
			}
		})
	}
}

func intPointer(value int) *int { return &value }

func screenProgressWithCapture(base ScreenProgressEvidence, at time.Time) *ScreenProgressEvidence {
	base.LastCaptureAt = at.Format(time.RFC3339Nano)
	return &base
}

func screenProgressWithChange(base ScreenProgressEvidence, at time.Time) *ScreenProgressEvidence {
	base.LastScreenChangeAt = at.Format(time.RFC3339Nano)
	return &base
}

func screenProgressWithChangeString(base ScreenProgressEvidence, at string) *ScreenProgressEvidence {
	base.LastScreenChangeAt = at
	return &base
}

func screenProgressWithoutFingerprint(base ScreenProgressEvidence) *ScreenProgressEvidence {
	base.ScreenFingerprint = ""
	return &base
}
