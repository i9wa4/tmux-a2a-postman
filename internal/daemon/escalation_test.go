package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

func TestEvaluateEscalationTripsThresholds(t *testing.T) {
	now := time.Date(2026, time.July, 13, 9, 0, 0, 0, time.UTC)
	cfg := &config.Config{
		EscalationOldestOpenSeconds:  60,
		EscalationDeadLetterCount:    1,
		EscalationUnreadBacklogCount: 3,
		EscalationStaleNodeSeconds:   30,
	}
	snapshot := status.SessionStatus{
		Queues: status.SessionQueues{DeadLetterCount: 1},
		Nodes: []status.NodeStatus{
			{
				Name:       "worker",
				InboxCount: 3,
				InputRequired: []status.InputRequestDetail{{
					MessageID: "request.md",
					OpenedAt:  now.Add(-90 * time.Second).Format(time.RFC3339Nano),
				}},
			},
			{
				Name:      "critic",
				PaneState: "stale",
				ScreenProgress: &status.ScreenProgressEvidence{
					EvidenceState: "stale",
					LastCaptureAt: now.Add(-45 * time.Second).Format(time.RFC3339Nano),
				},
			},
		},
	}

	trips := evaluateEscalationTrips(snapshot, cfg, now)
	seen := map[string]bool{}
	for _, trip := range trips {
		seen[trip.Kind] = true
	}
	for _, want := range []string{"dead_letter", "oldest_open_request", "stale_node", "unread_backlog"} {
		if !seen[want] {
			t.Fatalf("missing escalation trip %q in %#v", want, trips)
		}
	}
}

func TestMaybePushEscalationSuppressesSameLogicalOpenTripAcrossAdvancingAge(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "ctx", "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	now := time.Date(2026, time.July, 13, 10, 0, 0, 0, time.UTC)
	appendOpenInputRequest(t, sessionDir, "review", "request.md", "ireq_same", now.Add(-2*time.Minute))

	var sent []string
	rt := escalationTestRuntime(sessionDir, "review", "%1", &sent)
	rt.cfg.EscalationOldestOpenSeconds = 60
	rt.maybePushEscalation(now)
	rt.maybePushEscalation(now.Add(2 * time.Minute))

	if len(sent) != 1 {
		t.Fatalf("sent notifications = %d, want one for unchanged logical open trip", len(sent))
	}
}

func TestMaybePushEscalationRetriesAfterTransientSendFailure(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "ctx", "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	if err := writeRuntimeMarkdown(filepath.Join(sessionDir, "dead-letter", "bad.md")); err != nil {
		t.Fatalf("write dead-letter: %v", err)
	}

	now := time.Date(2026, time.July, 13, 10, 5, 0, 0, time.UTC)
	attempts := 0
	var sent []string
	events := make(chan tui.DaemonEvent, 4)
	rt := escalationTestRuntime(sessionDir, "review", "%1", &sent)
	rt.events = events
	rt.sendPaneNotification = func(_ string, message string, _ time.Duration, _ time.Duration, _ int, _ bool, _ time.Duration, _ int) error {
		attempts++
		if attempts == 1 {
			return errors.New("temporary tmux failure")
		}
		sent = append(sent, message)
		return nil
	}
	rt.cfg.EscalationDeadLetterCount = 1

	rt.maybePushEscalation(now)
	rt.maybePushEscalation(now.Add(2 * time.Second))

	if attempts != 2 {
		t.Fatalf("send attempts = %d, want retry after first failure", attempts)
	}
	if len(sent) != 1 {
		t.Fatalf("successful notifications = %d, want one after retry", len(sent))
	}
	if rt.lastEscalationPushKey == "" {
		t.Fatal("lastEscalationPushKey empty after successful retry")
	}
}

func TestRuntimeEscalationSnapshotIsolatesSameRoleAcrossSessions(t *testing.T) {
	tmpDir := t.TempDir()
	now := time.Date(2026, time.July, 13, 10, 10, 0, 0, time.UTC)
	reviewDir := filepath.Join(tmpDir, "ctx", "review")
	otherDir := filepath.Join(tmpDir, "ctx", "other")
	for _, dir := range []string{reviewDir, otherDir} {
		if err := config.CreateSessionDirs(dir); err != nil {
			t.Fatalf("CreateSessionDirs(%s): %v", dir, err)
		}
	}
	appendOpenInputRequest(t, reviewDir, "review", "review-request.md", "ireq_review", now.Add(-2*time.Minute))
	appendOpenInputRequest(t, otherDir, "other", "other-request.md", "ireq_other", now.Add(-3*time.Minute))

	var sent []string
	rt := escalationTestRuntime(reviewDir, "review", "%10", &sent)
	rt.nodes = map[string]discovery.NodeInfo{
		"other:messenger": {PaneID: "%99", SessionName: "other", SessionDir: otherDir},
		"other:worker":    {PaneID: "%20", SessionName: "other", SessionDir: otherDir},
		"review:messenger": {
			PaneID:      "%10",
			SessionName: "review",
			SessionDir:  reviewDir,
		},
		"review:worker": {PaneID: "%11", SessionName: "review", SessionDir: reviewDir},
	}

	snapshot := rt.runtimeEscalationSnapshot(now)
	reviewWorker := findEscalationNode(t, snapshot, "review:worker")
	otherWorker := findEscalationNode(t, snapshot, "other:worker")
	if len(reviewWorker.InputRequired) != 1 || reviewWorker.InputRequired[0].InputRequestID != "ireq_review" {
		t.Fatalf("review worker requests = %#v, want only ireq_review", reviewWorker.InputRequired)
	}
	if len(otherWorker.InputRequired) != 1 || otherWorker.InputRequired[0].InputRequestID != "ireq_other" {
		t.Fatalf("other worker requests = %#v, want only ireq_other", otherWorker.InputRequired)
	}

	uiNode, ok := runtimeUINode(rt.cfg, rt.nodes, "review")
	if !ok {
		t.Fatal("runtimeUINode() ok = false, want review messenger")
	}
	if uiNode.NodeKey != "review:messenger" || uiNode.PaneID != "%10" {
		t.Fatalf("runtimeUINode() = %s/%s, want review:messenger/%%10", uiNode.NodeKey, uiNode.PaneID)
	}
}

func TestRuntimeEscalationSnapshotStaleDurationThresholdBoundaries(t *testing.T) {
	now := time.Date(2026, time.July, 13, 10, 15, 0, 0, time.UTC)
	cfg := &config.Config{EscalationStaleNodeSeconds: 30}
	for _, tc := range []struct {
		name    string
		age     time.Duration
		wantHit bool
	}{
		{name: "below", age: 29 * time.Second},
		{name: "exact", age: 30 * time.Second, wantHit: true},
		{name: "above", age: 31 * time.Second, wantHit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := status.SessionStatus{
				Nodes: []status.NodeStatus{{
					Name:   "review:worker",
					PaneID: "%11",
				}},
			}
			attachRuntimePaneActivity(&snapshot, map[string]idle.PaneActivityExport{
				"%11": {
					Status:        "stale",
					LastCaptureAt: now.Add(-tc.age),
				},
			}, now)
			trips := evaluateEscalationTrips(snapshot, cfg, now)
			if gotHit := len(trips) == 1; gotHit != tc.wantHit {
				t.Fatalf("stale trip hit = %v, want %v; trips=%#v", gotHit, tc.wantHit, trips)
			}
			if tc.wantHit && trips[0].Observed != int(tc.age.Seconds()) {
				t.Fatalf("trip observed = %d, want %d", trips[0].Observed, int(tc.age.Seconds()))
			}
		})
	}
}

func TestMaybePushEscalationSendsPaneNotificationOncePerTripSet(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "ctx", "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	if err := writeRuntimeMarkdown(filepath.Join(sessionDir, "dead-letter", "bad.md")); err != nil {
		t.Fatalf("write dead-letter: %v", err)
	}

	now := time.Date(2026, time.July, 13, 9, 5, 0, 0, time.UTC)
	var sent []string
	events := make(chan tui.DaemonEvent, 4)
	rt := &daemonRuntime{
		sessionDir:          sessionDir,
		contextID:           "ctx",
		selfSession:         "review",
		cfg:                 config.DefaultConfig(),
		nodes:               map[string]discovery.NodeInfo{"review:messenger": {PaneID: "%1", SessionDir: sessionDir}},
		events:              events,
		idleTracker:         idle.NewIdleTracker(),
		clock:               func() time.Time { return now },
		lastEscalationCheck: now.Add(-time.Minute),
		sendPaneNotification: func(_ string, message string, _ time.Duration, _ time.Duration, _ int, _ bool, _ time.Duration, _ int) error {
			sent = append(sent, message)
			return nil
		},
	}
	rt.cfg.UINode = "messenger"
	rt.cfg.Nodes = map[string]config.NodeConfig{"messenger": {}}
	rt.cfg.EscalationCheckIntervalSeconds = 1
	rt.cfg.EscalationDeadLetterCount = 1

	rt.maybePushEscalation(now)
	rt.maybePushEscalation(now.Add(2 * time.Second))

	if len(sent) != 1 {
		t.Fatalf("sent notifications = %d, want one duplicate-suppressed notification", len(sent))
	}
	if !strings.Contains(sent[0], "dead_letter") || !strings.Contains(sent[0], "threshold-push on runtime facts") {
		t.Fatalf("notification message = %q, want dead_letter threshold-push wording", sent[0])
	}
	select {
	case event := <-events:
		if event.Type != "escalation_push" {
			t.Fatalf("event.Type = %q, want escalation_push", event.Type)
		}
	default:
		t.Fatal("missing escalation_push event")
	}
}

func escalationTestRuntime(sessionDir, sessionName, uiPaneID string, sent *[]string) *daemonRuntime {
	now := time.Date(2026, time.July, 13, 9, 5, 0, 0, time.UTC)
	rt := &daemonRuntime{
		sessionDir:  sessionDir,
		contextID:   "ctx",
		selfSession: sessionName,
		cfg:         config.DefaultConfig(),
		nodes: map[string]discovery.NodeInfo{
			sessionName + ":messenger": {PaneID: uiPaneID, SessionName: sessionName, SessionDir: sessionDir},
			sessionName + ":worker":    {PaneID: "%2", SessionName: sessionName, SessionDir: sessionDir},
		},
		idleTracker:         idle.NewIdleTracker(),
		clock:               func() time.Time { return now },
		lastEscalationCheck: now.Add(-time.Minute),
		sendPaneNotification: func(_ string, message string, _ time.Duration, _ time.Duration, _ int, _ bool, _ time.Duration, _ int) error {
			*sent = append(*sent, message)
			return nil
		},
	}
	rt.cfg.UINode = "messenger"
	rt.cfg.Nodes = map[string]config.NodeConfig{"messenger": {}}
	rt.cfg.EscalationCheckIntervalSeconds = 1
	return rt
}

func appendOpenInputRequest(t *testing.T, sessionDir, sessionName, messageID, inputRequestID string, deliveredAt time.Time) {
	t.Helper()
	writer, err := journal.OpenShadowWriter(sessionDir, "ctx", sessionName, 101, deliveredAt)
	if err != nil {
		t.Fatalf("OpenShadowWriter: %v", err)
	}
	content := verdictGateSendContent("orchestrator", "worker", messageID, "required", inputRequestID)
	appendRuntimeMailboxEventForTest(t, writer, projection.MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID:      messageID,
		From:           "orchestrator",
		To:             "worker",
		InputRequestID: inputRequestID,
		Content:        content,
	}, deliveredAt)
}

func findEscalationNode(t *testing.T, snapshot status.SessionStatus, name string) status.NodeStatus {
	t.Helper()
	for _, node := range snapshot.Nodes {
		if node.Name == name {
			return node
		}
	}
	t.Fatalf("missing node %q in %#v", name, snapshot.Nodes)
	return status.NodeStatus{}
}

func writeRuntimeMarkdown(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("body"), 0o644)
}
