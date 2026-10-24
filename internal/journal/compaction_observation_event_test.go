package journal

import (
	"encoding/json"
	"testing"
)

func TestCompactionObservationPayloadRoundTrip(t *testing.T) {
	payload := CompactionObservationPayload{
		Version:            CompactionObservationVersion,
		NodeKey:            "review:worker",
		PaneID:             "%17",
		LifecycleToken:     "pid:100",
		CanonicalToken:     "endpoint:%17",
		ObservedPID:        "100",
		PredecessorKey:     "compaction:v1:abc",
		ExpectedEpoch:      2,
		ObservationOrdinal: 17,
		CaptureSucceeded:   true,
		FullHistory:        true,
		PanePresent:        true,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded CompactionObservationPayload
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != payload {
		t.Fatalf("round trip = %+v, want %+v", decoded, payload)
	}
}
