package journal

// Compaction observation events describe evidence, not delivery. Writers and
// delivery integration are intentionally separate from this replay schema.
const (
	CompactionObservationPresentEventType = "compaction_observation_present"
	CompactionObservationAbsentEventType  = "compaction_observation_absent"
	CompactionLifecycleAliasedEventType   = "compaction_lifecycle_aliased"
	CompactionObservationVersion          = 2
)

// CompactionObservationPayload is scoped by the enclosing journal event's
// SessionKey and Generation. Version 2 requires ObservationOrdinal on each
// authoritative presence or absence. The producer must assign a strictly
// increasing ordinal per node/pane and session generation at capture time,
// preserve that ordinal on retries, and resume above the replayed high-water
// mark after restart. A presence requires a successful capture of a present
// pane; FullHistory is required for authoritative absence. An absence names
// the active predecessor it may close. An alias maps a concrete lifecycle
// token to the canonical token used for deduplication.
type CompactionObservationPayload struct {
	Version            int    `json:"version"`
	NodeKey            string `json:"node_key"`
	PaneID             string `json:"pane_id"`
	LifecycleToken     string `json:"lifecycle_token,omitempty"`
	CanonicalToken     string `json:"canonical_token,omitempty"`
	ObservedPID        string `json:"observed_pid,omitempty"`
	MarkerFingerprint  string `json:"marker_fingerprint,omitempty"`
	MarkerCount        int    `json:"marker_count,omitempty"`
	PredecessorKey     string `json:"predecessor_key,omitempty"`
	ExpectedEpoch      uint64 `json:"expected_epoch,omitempty"`
	ObservationOrdinal uint64 `json:"observation_ordinal,omitempty"`
	CaptureSucceeded   bool   `json:"capture_succeeded,omitempty"`
	FullHistory        bool   `json:"full_history,omitempty"`
	PanePresent        bool   `json:"pane_present,omitempty"`
}
