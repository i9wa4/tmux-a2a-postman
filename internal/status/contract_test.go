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

func TestSessionVisibleState(t *testing.T) {
	nodes := []NodeHealth{
		{Name: "worker", VisibleState: "pending"},
		{Name: "critic", VisibleState: "stale"},
	}

	if got := SessionVisibleState(nodes); got != "stale" {
		t.Fatalf("SessionVisibleState(...) = %q, want %q", got, "stale")
	}
}
