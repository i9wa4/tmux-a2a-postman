package status

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSchemaVersionIsV4StatusContract(t *testing.T) {
	if SchemaVersion != 4 {
		t.Fatalf("SchemaVersion = %d, want 4", SchemaVersion)
	}

	payload := SessionStatus{
		SchemaVersion: SchemaVersion,
		SessionName:   "review",
		Nodes:         []NodeStatus{{Name: "worker", VisibleState: "ready"}},
	}
	if payload.SchemaVersion != 4 || payload.Nodes[0].Name != "worker" {
		t.Fatalf("unexpected status payload: %#v", payload)
	}
}

func TestRequestSatisfactionSummarySerializesZeroTimingMetrics(t *testing.T) {
	payload, err := json.Marshal(RequestSatisfactionSummary{
		OpenedCount:              1,
		FilledCount:              1,
		StaleAfterSeconds:        3600,
		AverageTimeToFillSeconds: 0,
		LongestOpenAgeSeconds:    0,
		Signal:                   "responsiveness",
		Interpretation:           "same-second fill",
	})
	if err != nil {
		t.Fatalf("Marshal RequestSatisfactionSummary: %v", err)
	}

	got := string(payload)
	for _, want := range []string{
		`"average_time_to_fill_seconds":0`,
		`"longest_open_age_seconds":0`,
		`"dead_lettered_count":0`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("RequestSatisfactionSummary JSON = %s, want %s", got, want)
		}
	}
}

func TestNewRuntimeDiagnosticsIsScalarAndPointInTime(t *testing.T) {
	observedAt := time.Date(2026, 5, 24, 12, 30, 0, 123, time.UTC)
	diagnostics := NewRuntimeDiagnostics("daemon_runtime", DaemonRuntimeCardinality{
		SessionCount:            3,
		NodeCount:               5,
		WatchedDirCount:         7,
		ClaimedPaneCount:        2,
		ActivePostEventCount:    1,
		ActiveAutoPingCount:     4,
		ActiveDaemonSubmitCount: 6,
	}, DaemonSubmitRuntimeDiagnostics{
		WorkerLimit:                  4,
		ActiveWorkerCount:            2,
		ActiveRequestCount:           2,
		PendingRequestCount:          3,
		OldestPendingAgeSeconds:      90,
		ClaimedRequestCount:          1,
		LateResponseCount:            2,
		OldestLateResponseAgeSeconds: 120,
		SaturationCount:              1,
		LastSaturatedAt:              "2026-05-24T12:29:00Z",
	}, NonDaemonDeliveryRuntimeDiagnostics{
		WorkerLimit:            8,
		ActivePostCount:        1,
		PendingPostCount:       2,
		ActiveAutoPingCount:    3,
		PendingAutoPingCount:   4,
		ActiveManualPingCount:  5,
		PendingManualPingCount: 6,
		SaturationCount:        7,
		LastSaturatedAt:        "2026-05-24T12:28:00Z",
	}, observedAt)

	if diagnostics.Source != "daemon_runtime" {
		t.Fatalf("Source = %q, want daemon_runtime", diagnostics.Source)
	}
	if !diagnostics.PointInTime {
		t.Fatal("PointInTime = false, want true")
	}
	if diagnostics.ObservedAt != "2026-05-24T12:30:00.000000123Z" {
		t.Fatalf("ObservedAt = %q", diagnostics.ObservedAt)
	}
	if diagnostics.GoRuntime.GoroutineCount <= 0 {
		t.Fatalf("GoroutineCount = %d, want positive", diagnostics.GoRuntime.GoroutineCount)
	}
	if diagnostics.Daemon.SessionCount != 3 || diagnostics.Daemon.ActiveDaemonSubmitCount != 6 {
		t.Fatalf("Daemon cardinality = %#v", diagnostics.Daemon)
	}
	if diagnostics.DaemonSubmit.PendingRequestCount != 3 || diagnostics.DaemonSubmit.LateResponseCount != 2 {
		t.Fatalf("DaemonSubmit diagnostics = %#v", diagnostics.DaemonSubmit)
	}
	if diagnostics.NonDaemonDelivery.ActivePostCount != 1 || diagnostics.NonDaemonDelivery.PendingManualPingCount != 6 {
		t.Fatalf("NonDaemonDelivery diagnostics = %#v", diagnostics.NonDaemonDelivery)
	}

	payload, err := json.Marshal(diagnostics)
	if err != nil {
		t.Fatalf("Marshal diagnostics: %v", err)
	}
	for _, forbidden := range []string{"message_id", "pane_content", "body", "/home/", "/tmp/"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("diagnostics payload contains forbidden content marker %q: %s", forbidden, payload)
		}
	}
}

func TestVisibleState(t *testing.T) {
	tests := []struct {
		name        string
		paneState   string
		unreadCount int
		want        string
	}{
		{
			name:        "initial_from_missing_pane_state",
			paneState:   "",
			unreadCount: 0,
			want:        "initial",
		},
		{
			name:        "ready_from_active",
			paneState:   "active",
			unreadCount: 0,
			want:        "ready",
		},
		{
			name:        "pending_from_unread_inbox",
			paneState:   "ready",
			unreadCount: 2,
			want:        "pending",
		},
		{
			name:        "stale_from_stale",
			paneState:   "stale",
			unreadCount: 0,
			want:        "stale",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VisibleState(tt.paneState, tt.unreadCount); got != tt.want {
				t.Fatalf("VisibleState(%q, %d) = %q, want %q", tt.paneState, tt.unreadCount, got, tt.want)
			}
		})
	}
}

func TestVisibleStateWithInputRequests(t *testing.T) {
	tests := []struct {
		name                string
		paneState           string
		unreadCount         int
		inputRequiredCount  int
		waitingOnInputCount int
		want                string
	}{
		{
			name:      "ready_without_reply_slots",
			paneState: "active",
			want:      "ready",
		},
		{
			name:               "pending_from_input_required",
			paneState:          "ready",
			unreadCount:        0,
			inputRequiredCount: 1,
			want:               "pending",
		},
		{
			name:                "waiting_from_outbound_required",
			paneState:           "ready",
			waitingOnInputCount: 1,
			want:                "waiting",
		},
		{
			name:                "pending_beats_waiting",
			paneState:           "ready",
			inputRequiredCount:  1,
			waitingOnInputCount: 1,
			want:                "pending",
		},
		{
			name:                "stale_beats_reply_slots",
			paneState:           "stale",
			inputRequiredCount:  1,
			waitingOnInputCount: 1,
			want:                "stale",
		},
		{
			name:               "informational_unread_does_not_make_pending",
			paneState:          "ready",
			unreadCount:        1,
			inputRequiredCount: 0,
			want:               "ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VisibleStateWithInputRequests(tt.paneState, tt.unreadCount, tt.inputRequiredCount, tt.waitingOnInputCount)
			if got != tt.want {
				t.Fatalf("VisibleStateWithInputRequests(...) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSessionVisibleState(t *testing.T) {
	tests := []struct {
		name  string
		nodes []NodeStatus
		want  string
	}{
		{
			name:  "empty_node_set_stays_initial",
			nodes: nil,
			want:  "initial",
		},
		{
			name: "only_initial_nodes_stay_initial",
			nodes: []NodeStatus{
				{Name: "worker", VisibleState: "initial"},
				{Name: "critic"},
			},
			want: "initial",
		},
		{
			name: "expected_ai_without_positive_evidence_stays_initial",
			nodes: []NodeStatus{
				{Name: "worker", CurrentCommand: "claude"},
			},
			want: "initial",
		},
		{
			name: "ready_from_positive_pane_evidence",
			nodes: []NodeStatus{
				{Name: "worker", PaneState: "active"},
			},
			want: "ready",
		},
		{
			name: "worst_specific_state_wins",
			nodes: []NodeStatus{
				{Name: "worker", VisibleState: "pending"},
				{Name: "critic", VisibleState: "stale"},
			},
			want: "stale",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SessionVisibleState(tt.nodes); got != tt.want {
				t.Fatalf("SessionVisibleState(...) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSeverityRank(t *testing.T) {
	ordered := []string{
		"ok",
		"working",
		"expected_wait",
		"needs_action",
		"blocked",
		"attention_stale",
		"delivery_stuck",
		"delivery_failure",
	}

	for i := 1; i < len(ordered); i++ {
		if SeverityRank(ordered[i]) <= SeverityRank(ordered[i-1]) {
			t.Fatalf("SeverityRank(%q) = %d, want greater than %q rank %d", ordered[i], SeverityRank(ordered[i]), ordered[i-1], SeverityRank(ordered[i-1]))
		}
	}
	if got := WorseSeverity("needs_action", "delivery_stuck"); got != "delivery_stuck" {
		t.Fatalf("WorseSeverity(...) = %q, want delivery_stuck", got)
	}
	if got := WorseSeverity("", "working"); got != "working" {
		t.Fatalf("WorseSeverity(empty, working) = %q, want working", got)
	}
}
