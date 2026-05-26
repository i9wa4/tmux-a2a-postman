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

func TestDaemonStateDrainWindowUsesInjectedClock(t *testing.T) {
	now := time.Date(2026, time.May, 21, 5, 0, 0, 0, time.UTC)
	ds := newDaemonStateWithClock(10, "ctx-main", func() time.Time { return now })

	if !ds.IsSessionEnabled("review") {
		t.Fatal("session should be enabled during startup drain window")
	}

	now = now.Add(11 * time.Second)
	if ds.IsSessionEnabled("review") {
		t.Fatal("unconfigured session stayed enabled after startup drain window")
	}

	ds.SetSessionEnabled("review", true)
	if !ds.IsSessionEnabled("review") {
		t.Fatal("configured session should be enabled after startup drain window")
	}
}

func TestReserveDeliveryRouteUsesExplicitClock(t *testing.T) {
	ds := NewDaemonState(0, "ctx-main")
	route := "orchestrator:messenger"
	gap := 10 * time.Second
	start := time.Date(2026, time.May, 21, 6, 0, 0, 0, time.UTC)

	remaining, reservedAt, ok := ds.reserveDeliveryRoute(route, gap, start)
	if !ok {
		t.Fatal("first reservation was rejected")
	}
	if remaining != 0 {
		t.Fatalf("first reservation remaining = %s, want 0", remaining)
	}
	if !reservedAt.Equal(start) {
		t.Fatalf("reservedAt = %v, want %v", reservedAt, start)
	}

	remaining, _, ok = ds.reserveDeliveryRoute(route, gap, start.Add(3*time.Second))
	if ok {
		t.Fatal("second reservation was allowed while the first was in flight")
	}
	if remaining != 7*time.Second {
		t.Fatalf("in-flight remaining = %s, want 7s", remaining)
	}

	finishedAt := start.Add(4 * time.Second)
	ds.finishDeliveryRoute(route, reservedAt, true, true, finishedAt)

	remaining, _, ok = ds.reserveDeliveryRoute(route, gap, start.Add(9*time.Second))
	if ok {
		t.Fatal("reservation was allowed before delivery gap elapsed")
	}
	if remaining != 5*time.Second {
		t.Fatalf("post-delivery remaining = %s, want 5s", remaining)
	}

	remaining, reservedAt, ok = ds.reserveDeliveryRoute(route, gap, start.Add(15*time.Second))
	if !ok {
		t.Fatal("reservation was rejected after delivery gap elapsed")
	}
	if remaining != 0 {
		t.Fatalf("post-gap remaining = %s, want 0", remaining)
	}
	if !reservedAt.Equal(start.Add(15 * time.Second)) {
		t.Fatalf("post-gap reservedAt = %v, want %v", reservedAt, start.Add(15*time.Second))
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
