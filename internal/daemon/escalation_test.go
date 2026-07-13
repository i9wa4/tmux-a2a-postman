package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
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

func writeRuntimeMarkdown(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("body"), 0o644)
}
