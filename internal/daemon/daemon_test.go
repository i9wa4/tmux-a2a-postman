package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
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

// TestHasNodeSentSince verifies the send-history guard used by swallowed-message
// detection (Issue #282).
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

func TestSwallowedDetectorSelectsPositiveCandidate(t *testing.T) {
	now := time.Date(2026, time.June, 1, 5, 0, 0, 0, time.UTC)
	filename := "20260601-120000-from-orchestrator-to-worker.md"
	sessionDir := "session-dir"
	cfg := config.DefaultConfig()
	cfg.Nodes["worker"] = config.NodeConfig{
		DeliveryIdleTimeoutSeconds: 60,
		DeliveryIdleRetryMax:       2,
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%1",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}
	detector := swallowedDetector{
		now: func() time.Time { return now },
		listInbox: func(inboxDir string) ([]swallowedInboxEntry, error) {
			want := filepath.Join(sessionDir, "inbox", "worker")
			if inboxDir != want {
				t.Fatalf("listInbox dir = %q, want %q", inboxDir, want)
			}
			return []swallowedInboxEntry{{
				name:    filename,
				modTime: now.Add(-2 * time.Minute),
			}}, nil
		},
		hasNodeSentSince: func(nodeName string, since time.Time) bool {
			if nodeName != "worker" {
				t.Fatalf("hasNodeSentSince node = %q, want worker", nodeName)
			}
			return false
		},
		retryCount: func(inboxPath string) int {
			want := filepath.Join(sessionDir, "inbox", "worker", filename)
			if inboxPath != want {
				t.Fatalf("retryCount path = %q, want %q", inboxPath, want)
			}
			return 1
		},
	}

	got := detector.detect(nodes, cfg, map[string]string{"%1": "idle"})
	if len(got) != 1 {
		t.Fatalf("detect candidates = %d, want 1", len(got))
	}
	candidate := got[0]
	if candidate.nodeKey != "review:worker" || candidate.nodeName != "worker" {
		t.Fatalf("candidate node = %q/%q, want review:worker/worker", candidate.nodeKey, candidate.nodeName)
	}
	if candidate.fileName != filename || candidate.from != "orchestrator" {
		t.Fatalf("candidate file/from = %q/%q, want %q/orchestrator", candidate.fileName, candidate.from, filename)
	}
	if candidate.retryCount != 1 || candidate.retryMax != 2 {
		t.Fatalf("candidate retry = %d/%d, want 1/2", candidate.retryCount, candidate.retryMax)
	}
	if !candidate.detectedAt.Equal(now) {
		t.Fatalf("candidate detectedAt = %s, want %s", candidate.detectedAt, now)
	}
}

func TestSwallowedDetectorSkipsNonCandidates(t *testing.T) {
	now := time.Date(2026, time.June, 1, 5, 0, 0, 0, time.UTC)
	old := now.Add(-2 * time.Minute)
	cfg := config.DefaultConfig()
	cfg.Nodes["worker"] = config.NodeConfig{
		DeliveryIdleTimeoutSeconds: 60,
		DeliveryIdleRetryMax:       2,
	}
	nodes := map[string]discovery.NodeInfo{
		"worker": {
			PaneID:     "%1",
			SessionDir: "session-dir",
		},
	}

	tests := []struct {
		name     string
		pane     string
		entries  []swallowedInboxEntry
		sent     bool
		retries  int
		wantList bool
	}{
		{
			name: "active pane",
			pane: "active",
		},
		{
			name: "postman sender",
			pane: "idle",
			entries: []swallowedInboxEntry{{
				name:    "20260601-120000-from-postman-to-worker.md",
				modTime: old,
			}},
			wantList: true,
		},
		{
			name: "daemon sender",
			pane: "idle",
			entries: []swallowedInboxEntry{{
				name:    "20260601-120000-from-daemon-to-worker.md",
				modTime: old,
			}},
			wantList: true,
		},
		{
			name: "recent file",
			pane: "idle",
			entries: []swallowedInboxEntry{{
				name:    "20260601-120000-from-orchestrator-to-worker.md",
				modTime: now.Add(-30 * time.Second),
			}},
			wantList: true,
		},
		{
			name: "node sent since delivery",
			pane: "stale",
			entries: []swallowedInboxEntry{{
				name:    "20260601-120000-from-orchestrator-to-worker.md",
				modTime: old,
			}},
			sent:     true,
			wantList: true,
		},
		{
			name: "max retry",
			pane: "idle",
			entries: []swallowedInboxEntry{{
				name:    "20260601-120000-from-orchestrator-to-worker.md",
				modTime: old,
			}},
			retries:  2,
			wantList: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			listed := false
			detector := swallowedDetector{
				now: func() time.Time { return now },
				listInbox: func(string) ([]swallowedInboxEntry, error) {
					listed = true
					return tc.entries, nil
				},
				hasNodeSentSince: func(string, time.Time) bool { return tc.sent },
				retryCount:       func(string) int { return tc.retries },
			}
			got := detector.detect(nodes, cfg, map[string]string{"%1": tc.pane})
			if len(got) != 0 {
				t.Fatalf("detect candidates = %#v, want none", got)
			}
			if listed != tc.wantList {
				t.Fatalf("listInbox called = %v, want %v", listed, tc.wantList)
			}
		})
	}
}

func TestExecuteSwallowedRedeliveryCallsPaneSenderAndEmitsEvent(t *testing.T) {
	now := time.Date(2026, time.June, 1, 5, 0, 0, 0, time.UTC)
	cfg := config.DefaultConfig()
	cfg.NotificationTemplate = "notice {node} from {from_node} file {filename}"
	cfg.EnterDelay = 1.5
	cfg.TmuxTimeout = 2
	cfg.EnterVerifyDelay = 0.25
	cfg.EnterRetryMax = 4
	cfg.Nodes["worker"] = config.NodeConfig{
		EnterDelay: 0.5,
		EnterCount: 2,
	}
	filename := "20260601-120000-from-orchestrator-to-worker.md"
	candidate := swallowedCandidate{
		nodeKey:      "review:worker",
		nodeName:     "worker",
		nodeInfo:     discovery.NodeInfo{PaneID: "%7", SessionName: "review", SessionDir: "session-dir"},
		fileName:     filename,
		inboxPath:    filepath.Join("session-dir", "inbox", "worker", filename),
		from:         "orchestrator",
		deliveryTime: now.Add(-5 * time.Minute),
		detectedAt:   now,
		retryCount:   1,
		retryMax:     3,
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": candidate.nodeInfo,
	}
	events := make(chan tui.DaemonEvent, 1)
	incrementedPath := ""

	var gotPaneID string
	var gotMessage string
	var gotEnterDelay, gotTmuxTimeout, gotVerifyDelay time.Duration
	var gotEnterCount, gotMaxRetries int
	var gotBypass bool
	executeSwallowedRedelivery(
		candidate, cfg, nil, nodes, "ctx-main", nil, events,
		func(paneID string, message string, enterDelay time.Duration, tmuxTimeout time.Duration, enterCount int, bypassCooldown bool, verifyDelay time.Duration, maxRetries int) error {
			gotPaneID = paneID
			gotMessage = message
			gotEnterDelay = enterDelay
			gotTmuxTimeout = tmuxTimeout
			gotEnterCount = enterCount
			gotBypass = bypassCooldown
			gotVerifyDelay = verifyDelay
			gotMaxRetries = maxRetries
			return nil
		},
		func(inboxPath string) {
			incrementedPath = inboxPath
		},
	)

	if gotPaneID != "%7" {
		t.Fatalf("paneID = %q, want %%7", gotPaneID)
	}
	if !strings.Contains(gotMessage, "notice worker from orchestrator file "+filename) {
		t.Fatalf("notification message = %q, want template data", gotMessage)
	}
	if gotEnterDelay != 500*time.Millisecond || gotTmuxTimeout != 2*time.Second || gotVerifyDelay != 250*time.Millisecond {
		t.Fatalf("durations = %s/%s/%s, want 500ms/2s/250ms", gotEnterDelay, gotTmuxTimeout, gotVerifyDelay)
	}
	if gotEnterCount != 2 || gotMaxRetries != 4 || !gotBypass {
		t.Fatalf("send settings count=%d retries=%d bypass=%v, want 2/4/true", gotEnterCount, gotMaxRetries, gotBypass)
	}
	if incrementedPath != candidate.inboxPath {
		t.Fatalf("incremented path = %q, want %q", incrementedPath, candidate.inboxPath)
	}

	event := <-events
	if event.Type != "swallowed_redelivery" {
		t.Fatalf("event type = %q, want swallowed_redelivery", event.Type)
	}
	if event.Message != "Re-delivered to worker: "+filename+" (attempt 2/3)" {
		t.Fatalf("event message = %q", event.Message)
	}
	if event.Details["node"] != "review:worker" || event.Details["file"] != filename || event.Details["attempt"] != 2 || event.Details["max"] != 3 {
		t.Fatalf("event details = %#v", event.Details)
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

func TestMessageEventFailureReason(t *testing.T) {
	event := message.DaemonEvent{
		Type:    "message_received",
		Message: "Dead-letter: orchestrator -> worker (routing denied)",
		Details: map[string]interface{}{
			"failure_reason": "routing-denied",
		},
	}
	if got := messageEventFailureReason(event); got != "routing-denied" {
		t.Fatalf("messageEventFailureReason() = %q, want routing-denied", got)
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
