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

func TestVisibleStateWithObligations(t *testing.T) {
	tests := []struct {
		name                string
		paneState           string
		unreadCount         int
		actionRequiredCount int
		waitingOnReplyCount int
		want                string
	}{
		{
			name:      "ready_without_obligations",
			paneState: "active",
			want:      "ready",
		},
		{
			name:                "pending_from_action_required",
			paneState:           "ready",
			unreadCount:         0,
			actionRequiredCount: 1,
			want:                "pending",
		},
		{
			name:                "waiting_from_outbound_required",
			paneState:           "ready",
			waitingOnReplyCount: 1,
			want:                "waiting",
		},
		{
			name:                "pending_beats_waiting",
			paneState:           "ready",
			actionRequiredCount: 1,
			waitingOnReplyCount: 1,
			want:                "pending",
		},
		{
			name:                "stale_beats_obligations",
			paneState:           "stale",
			actionRequiredCount: 1,
			waitingOnReplyCount: 1,
			want:                "stale",
		},
		{
			name:                "informational_unread_does_not_make_pending",
			paneState:           "ready",
			unreadCount:         1,
			actionRequiredCount: 0,
			want:                "ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VisibleStateWithObligations(tt.paneState, tt.unreadCount, tt.actionRequiredCount, tt.waitingOnReplyCount)
			if got != tt.want {
				t.Fatalf("VisibleStateWithObligations(...) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSessionVisibleState(t *testing.T) {
	nodes := []NodeHealth{
		{Name: "worker", VisibleState: "pending"},
		{Name: "critic", VisibleState: "stale"},
	}

	if got := SessionVisibleState(nodes); got != "stale" {
		t.Fatalf("SessionVisibleState(...) = %q, want %q", got, "stale")
	}
}
