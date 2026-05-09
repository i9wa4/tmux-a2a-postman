package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
	"github.com/i9wa4/tmux-a2a-postman/internal/uinode"
)

func TestCheckPaneRestarts_IgnoresWinnerSwapWhileOldPaneStillLive(t *testing.T) {
	ds := NewDaemonState(0, "ctx-main")
	ds.prevPaneStates = map[string]uinode.PaneInfo{
		"%10": {},
	}
	ds.prevPaneToNode = map[string]string{
		"%10": "review:worker",
	}

	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  t.TempDir(),
		},
	}
	paneStates := map[string]uinode.PaneInfo{
		"%10": {},
		"%11": {},
	}
	paneToNode := map[string]string{
		"%11": "review:worker",
	}
	events := make(chan tui.DaemonEvent, 1)

	restarted := ds.checkPaneRestarts(paneStates, paneToNode, nodes, events)
	if len(restarted) != 0 {
		t.Fatalf("checkPaneRestarts() = %#v, want no restart when old pane is still live", restarted)
	}
	if len(events) != 0 {
		t.Fatalf("pane_restart event emitted for live winner swap: %#v", <-events)
	}
}

// TestHasNodeSentSince verifies swallowed-message detection logic (Issue #282).
// NOTE: checkSwallowedMessages itself is not unit-tested because it depends on
// filesystem state (real inbox/ directories), tmux (via notification.SendToPane),
// and idle.IdleTracker (which polls tmux pane activity). Mocking all three would
// require interface refactoring beyond the scope of this issue.
func TestHasNodeSentSince(t *testing.T) {
	now := time.Now()
	before := now.Add(-10 * time.Second)
	after := now.Add(10 * time.Second)

	tests := []struct {
		name     string
		entries  map[string]time.Time // lastDeliveryBySenderRecipient
		nodeName string
		since    time.Time
		want     bool
	}{
		{
			name:     "NoSend",
			entries:  map[string]time.Time{},
			nodeName: "worker",
			since:    now,
			want:     false,
		},
		{
			name:     "SendBefore",
			entries:  map[string]time.Time{"worker:orchestrator": before},
			nodeName: "worker",
			since:    now,
			want:     false,
		},
		{
			name:     "SendAfter",
			entries:  map[string]time.Time{"worker:orchestrator": after},
			nodeName: "worker",
			since:    now,
			want:     true,
		},
		{
			name:     "OtherNodeSent",
			entries:  map[string]time.Time{"orchestrator:worker": after},
			nodeName: "worker",
			since:    now,
			want:     false,
		},
		{
			name: "MultipleEntries",
			entries: map[string]time.Time{
				"worker:orchestrator": before,
				"worker:messenger":    after,
			},
			nodeName: "worker",
			since:    now,
			want:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ds := NewDaemonState(0, "test-context")
			ds.lastDeliveryMu.Lock()
			ds.lastDeliveryBySenderRecipient = tc.entries
			ds.lastDeliveryMu.Unlock()

			got := ds.hasNodeSentSince(tc.nodeName, tc.since)
			if got != tc.want {
				t.Errorf("hasNodeSentSince(%q, since): got %v, want %v", tc.nodeName, got, tc.want)
			}
		})
	}
}

func TestReserveDeliveryRoute_BackoffWhenInFlightReservationOutlivesGap(t *testing.T) {
	ds := NewDaemonState(0, "test-context")
	route := "orchestrator:critic"
	gap := time.Second
	now := time.Unix(1_800_000_000, 0)

	remaining, reservedAt, ok := ds.reserveDeliveryRoute(route, gap, now)
	if !ok {
		t.Fatalf("first reserveDeliveryRoute() ok = false, want true")
	}
	if remaining != 0 {
		t.Fatalf("first reserveDeliveryRoute() remaining = %s, want 0", remaining)
	}
	if !reservedAt.Equal(now) {
		t.Fatalf("first reserveDeliveryRoute() reservedAt = %s, want %s", reservedAt, now)
	}

	remaining, _, ok = ds.reserveDeliveryRoute(route, gap, now.Add(2*gap))
	if ok {
		t.Fatalf("second reserveDeliveryRoute() ok = true while route is still in flight")
	}
	if remaining != gap {
		t.Fatalf("second reserveDeliveryRoute() remaining = %s, want %s", remaining, gap)
	}
}

func TestReserveDeliveryRoute_UsesRemainingGapForFreshReservation(t *testing.T) {
	ds := NewDaemonState(0, "test-context")
	route := "orchestrator:critic"
	gap := time.Second
	now := time.Unix(1_800_000_000, 0)

	_, _, ok := ds.reserveDeliveryRoute(route, gap, now)
	if !ok {
		t.Fatalf("first reserveDeliveryRoute() ok = false, want true")
	}

	remaining, _, ok := ds.reserveDeliveryRoute(route, gap, now.Add(250*time.Millisecond))
	if ok {
		t.Fatalf("second reserveDeliveryRoute() ok = true while route is still in flight")
	}
	want := 750 * time.Millisecond
	if remaining != want {
		t.Fatalf("second reserveDeliveryRoute() remaining = %s, want %s", remaining, want)
	}
}

func TestMessageEventSuppressesNormalDelivery(t *testing.T) {
	tests := []struct {
		name  string
		event message.DaemonEvent
		want  bool
	}{
		{
			name: "dead letter",
			event: message.DaemonEvent{
				Type:    "message_received",
				Message: "Dead-letter: orchestrator -> worker (routing denied)",
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := messageEventSuppressesNormalDelivery(tc.event); got != tc.want {
				t.Fatalf("messageEventSuppressesNormalDelivery(%q) = %v, want %v", tc.event.Message, got, tc.want)
			}
		})
	}
}

func TestScanLiveInboxCounts_CountsUnreadInboxMarkdownFiles(t *testing.T) {
	sessionDir := t.TempDir()
	workerInbox := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(workerInbox, 0o700); err != nil {
		t.Fatalf("MkdirAll worker inbox: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workerInbox, "20260330-120000-from-orchestrator-to-worker.md"), []byte("first"), 0o600); err != nil {
		t.Fatalf("WriteFile first unread: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workerInbox, "20260330-120001-from-critic-to-worker.md"), []byte("second"), 0o600); err != nil {
		t.Fatalf("WriteFile second unread: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workerInbox, "notes.txt"), []byte("ignore"), 0o600); err != nil {
		t.Fatalf("WriteFile ignored note: %v", err)
	}

	counts := scanLiveInboxCounts(map[string]discovery.NodeInfo{
		"review:worker": {SessionDir: sessionDir},
	})

	if got := counts["review:worker"]; got != 2 {
		t.Fatalf("scanLiveInboxCounts review:worker = %d, want 2", got)
	}
}
