package status

import "testing"

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
		nodes []NodeHealth
		want  string
	}{
		{
			name:  "empty_node_set_stays_initial",
			nodes: nil,
			want:  "initial",
		},
		{
			name: "only_initial_nodes_stay_initial",
			nodes: []NodeHealth{
				{Name: "worker", VisibleState: "initial"},
				{Name: "critic"},
			},
			want: "initial",
		},
		{
			name: "expected_ai_without_positive_evidence_stays_initial",
			nodes: []NodeHealth{
				{Name: "worker", CurrentCommand: "claude"},
			},
			want: "initial",
		},
		{
			name: "ready_from_positive_pane_evidence",
			nodes: []NodeHealth{
				{Name: "worker", PaneState: "active"},
			},
			want: "ready",
		},
		{
			name: "worst_specific_state_wins",
			nodes: []NodeHealth{
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
