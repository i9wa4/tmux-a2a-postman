package status

import "testing"

func TestVisibleState(t *testing.T) {
	tests := []struct {
		name         string
		paneState    string
		waitingState string
		unreadCount  int
		want         string
	}{
		{
			name:         "ready_from_active",
			paneState:    "active",
			waitingState: "",
			unreadCount:  0,
			want:         "ready",
		},
		{
			name:         "pending_from_unread_inbox",
			paneState:    "ready",
			waitingState: "",
			unreadCount:  2,
			want:         "pending",
		},
		{
			name:         "waiting_state_beats_pending",
			paneState:    "ready",
			waitingState: "composing",
			unreadCount:  2,
			want:         "composing",
		},
		{
			name:         "stuck_aliases_to_stalled",
			paneState:    "stale",
			waitingState: "stuck",
			unreadCount:  0,
			want:         "stalled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VisibleState(tt.paneState, tt.waitingState, tt.unreadCount); got != tt.want {
				t.Fatalf("VisibleState(%q, %q, %d) = %q, want %q", tt.paneState, tt.waitingState, tt.unreadCount, got, tt.want)
			}
		})
	}
}

func TestSessionVisibleState(t *testing.T) {
	nodes := []NodeHealth{
		{Name: "worker", VisibleState: "pending"},
		{Name: "critic", VisibleState: "composing"},
	}

	if got := SessionVisibleState(nodes); got != "composing" {
		t.Fatalf("SessionVisibleState(...) = %q, want %q", got, "composing")
	}
}
