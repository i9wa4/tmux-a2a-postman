package status

import "time"

// ObserveScreenProgress adds bounded observation evidence without changing the
// existing screen-progress classification. now is supplied by the caller so
// status projection and daemon snapshots use the same time semantics. A
// non-positive stale horizon leaves freshness unknown unless existing evidence
// already classifies the capture as stale.
func ObserveScreenProgress(progress *ScreenProgressEvidence, now time.Time, staleAfter time.Duration) *ScreenProgressEvidence {
	observed := &ScreenProgressEvidence{
		EvidenceState: "missing",
		Source:        "unknown",
		Freshness:     "unknown",
	}
	if progress == nil {
		return observed
	}
	*observed = *progress
	observed.Source = "unknown"
	observed.ObservedAt = ""
	observed.Freshness = "unknown"
	observed.IdleAgeSeconds = nil
	observed.Conflict = false

	capturedAt, err := time.Parse(time.RFC3339Nano, progress.LastCaptureAt)
	if err != nil || capturedAt.IsZero() {
		return observed
	}
	observed.Source = "pane_capture"
	observed.ObservedAt = capturedAt.UTC().Format(time.RFC3339Nano)
	if now.IsZero() || capturedAt.After(now) {
		observed.Conflict = !now.IsZero()
		return observed
	}
	if progress.EvidenceState == "stale" {
		observed.Freshness = "stale"
	}
	if progress.ScreenFingerprint == "" {
		return observed
	}
	if observed.Freshness != "stale" && staleAfter > 0 && now.Sub(capturedAt) > staleAfter {
		observed.Freshness = "stale"
	} else if observed.Freshness != "stale" && staleAfter > 0 {
		observed.Freshness = "fresh"
	}

	changedAt, err := time.Parse(time.RFC3339Nano, progress.LastScreenChangeAt)
	if err != nil || changedAt.IsZero() {
		return observed
	}
	if changedAt.After(capturedAt) || changedAt.After(now) {
		observed.Conflict = true
		return observed
	}
	idleAge := int(now.Sub(changedAt).Seconds())
	observed.IdleAgeSeconds = &idleAge
	return observed
}
