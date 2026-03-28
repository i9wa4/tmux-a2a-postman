package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

// TestWarnAlertConfig verifies the three warning branches of warnAlertConfig.
// Issue #352: daemon must emit a visible warning when the alert system is
// effectively disabled due to missing ui_node or all-zero timeout values.
func TestWarnAlertConfig(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *config.Config
		wantWarning bool
		wantContain string
	}{
		{
			name:        "UINodeUnset",
			cfg:         &config.Config{UINode: ""},
			wantWarning: true,
			wantContain: "ui_node is not set",
		},
		{
			name: "UINodeSetAllZeroTimeouts",
			cfg: &config.Config{
				UINode: "messenger",
				Nodes:  map[string]config.NodeConfig{"worker": {}},
			},
			wantWarning: true,
			wantContain: "partially disabled",
		},
		{
			name: "UINodeSetWithActivePerNodeTimeout",
			cfg: &config.Config{
				UINode: "messenger",
				Nodes:  map[string]config.NodeConfig{"worker": {IdleTimeoutSeconds: 900}},
			},
			wantWarning: false,
		},
		{
			name: "UINodeSetWithActiveNodeDefaults",
			cfg: &config.Config{
				UINode:       "messenger",
				NodeDefaults: config.NodeConfig{DroppedBallTimeoutSeconds: 900},
				Nodes:        map[string]config.NodeConfig{"worker": {}},
			},
			wantWarning: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			events := make(chan tui.DaemonEvent, 5)
			warnAlertConfig(tc.cfg, events)
			if tc.wantWarning {
				if len(events) == 0 {
					t.Fatal("expected a warning event but channel is empty")
				}
				ev := <-events
				if ev.Type != "alert_config_warning" {
					t.Errorf("event type: got %q, want %q", ev.Type, "alert_config_warning")
				}
				if tc.wantContain != "" && !strings.Contains(ev.Message, tc.wantContain) {
					t.Errorf("event message %q does not contain %q", ev.Message, tc.wantContain)
				}
			} else {
				if len(events) != 0 {
					ev := <-events
					t.Errorf("expected no warning but got event: %q", ev.Message)
				}
			}
		})
	}
}

// TestShouldSendAlert_CooldownBoundary verifies that ShouldSendAlert returns true
// on first call, false immediately after MarkAlertSent, and true again after the
// cooldown window has elapsed.
func TestShouldSendAlert_CooldownBoundary(t *testing.T) {
	ds := NewDaemonState(0, "test-context")
	alertKey := "test_alert"
	cooldown := 300.0 // 5 minutes in seconds

	// First call: no previous alert — must return true
	if !ds.ShouldSendAlert(alertKey, cooldown) {
		t.Error("expected true for first alert (no prior timestamp)")
	}

	// Mark alert as sent
	ds.MarkAlertSent(alertKey)

	// Immediately after: cooldown not expired — must return false
	if ds.ShouldSendAlert(alertKey, cooldown) {
		t.Error("expected false immediately after MarkAlertSent (cooldown active)")
	}

	// Sub-case 2: 299s elapsed — cooldown still active (not > 300s)
	ds.lastAlertTimestampMu.Lock()
	ds.lastAlertTimestamp[alertKey] = time.Now().Add(-5*time.Minute + 1*time.Second)
	ds.lastAlertTimestampMu.Unlock()

	if ds.ShouldSendAlert(alertKey, cooldown) {
		t.Error("expected ShouldSendAlert=false at 299s elapsed (cooldown still active)")
	}

	// Sub-case 3: 301s elapsed — cooldown expired (> 300s)
	ds.lastAlertTimestampMu.Lock()
	ds.lastAlertTimestamp[alertKey] = time.Now().Add(-301 * time.Second)
	ds.lastAlertTimestampMu.Unlock()

	if !ds.ShouldSendAlert(alertKey, cooldown) {
		t.Error("expected true after cooldown expired (301s > 300s)")
	}
}

// TestReminderIncrementSenderFilter verifies that reminderShouldIncrement excludes
// daemon-generated senders (postman, daemon) and includes all other senders.
func TestReminderIncrementSenderFilter(t *testing.T) {
	tests := []struct {
		from            string
		shouldIncrement bool
	}{
		{"postman", false},
		{"daemon", false},
		{"orchestrator", true},
		{"worker", true},
		{"messenger", true},
		{"", true}, // empty from is treated as human message (no special exclusion)
	}
	for _, tc := range tests {
		result := reminderShouldIncrement(tc.from)
		if result != tc.shouldIncrement {
			t.Errorf("reminderShouldIncrement(%q): expected %v, got %v", tc.from, tc.shouldIncrement, result)
		}
	}
}

// TestShouldSendAlert_ZeroCooldown verifies that zero cooldown always returns true.
func TestShouldSendAlert_ZeroCooldown(t *testing.T) {
	ds := NewDaemonState(0, "test-context")
	alertKey := "zero_cooldown"

	ds.MarkAlertSent(alertKey)

	// Zero cooldown: always return true regardless of timestamp
	if !ds.ShouldSendAlert(alertKey, 0) {
		t.Error("expected true with zero cooldown")
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
		{
			name: "latency warning",
			event: message.DaemonEvent{
				Type:    "latency_warning",
				Message: "Delivery latency alert: orchestrator -> worker (age: 31s, threshold: 30s)",
			},
			want: false,
		},
		{
			name: "phony delivery",
			event: message.DaemonEvent{
				Type:    "message_received",
				Message: "Phony delivery: orchestrator -> channel-a",
			},
			want: false,
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

func TestRequeueWaitingMessage_UnreadOriginalPreservesInboxPayload(t *testing.T) {
	sessionDir := t.TempDir()
	simpleName := "worker"
	filename := "20260328-101500-from-orchestrator-to-worker.md"
	waitingDir := filepath.Join(sessionDir, "waiting")
	inboxDir := filepath.Join(sessionDir, "inbox", simpleName)
	if err := os.MkdirAll(waitingDir, 0o700); err != nil {
		t.Fatalf("MkdirAll waiting: %v", err)
	}
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}

	waitingPath := filepath.Join(waitingDir, filename)
	inboxPath := filepath.Join(inboxDir, filename)
	waitingContent := []byte("---\nstate: composing\n---\n")
	originalContent := []byte("---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-03-28T10:15:00Z\n---\n\nOriginal payload\n")
	if err := os.WriteFile(waitingPath, waitingContent, 0o600); err != nil {
		t.Fatalf("WriteFile waiting: %v", err)
	}
	if err := os.WriteFile(inboxPath, originalContent, 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}

	if err := requeueWaitingMessage(sessionDir, simpleName, filename); err != nil {
		t.Fatalf("requeueWaitingMessage: %v", err)
	}

	if _, err := os.Stat(waitingPath); !os.IsNotExist(err) {
		t.Fatalf("waiting file still present or wrong error: %v", err)
	}
	gotInbox, err := os.ReadFile(inboxPath)
	if err != nil {
		t.Fatalf("ReadFile inbox: %v", err)
	}
	if string(gotInbox) != string(originalContent) {
		t.Fatalf("inbox content changed:\n got %q\nwant %q", gotInbox, originalContent)
	}
}

func TestRequeueWaitingMessage_ArchivedOriginalRestoresInboxCopy(t *testing.T) {
	sessionDir := t.TempDir()
	simpleName := "worker"
	filename := "20260328-101501-from-orchestrator-to-worker.md"
	waitingDir := filepath.Join(sessionDir, "waiting")
	readDir := filepath.Join(sessionDir, "read")
	inboxDir := filepath.Join(sessionDir, "inbox", simpleName)
	if err := os.MkdirAll(waitingDir, 0o700); err != nil {
		t.Fatalf("MkdirAll waiting: %v", err)
	}
	if err := os.MkdirAll(readDir, 0o700); err != nil {
		t.Fatalf("MkdirAll read: %v", err)
	}
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}

	waitingPath := filepath.Join(waitingDir, filename)
	readPath := filepath.Join(readDir, filename)
	inboxPath := filepath.Join(inboxDir, filename)
	waitingContent := []byte("---\nstate: composing\n---\n")
	originalContent := []byte("---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-03-28T10:15:01Z\n---\n\nArchived payload\n")
	if err := os.WriteFile(waitingPath, waitingContent, 0o600); err != nil {
		t.Fatalf("WriteFile waiting: %v", err)
	}
	if err := os.WriteFile(readPath, originalContent, 0o600); err != nil {
		t.Fatalf("WriteFile read: %v", err)
	}
	originalReadTime := time.Date(2026, time.March, 28, 10, 15, 1, 0, time.UTC)
	if err := os.Chtimes(readPath, originalReadTime, originalReadTime); err != nil {
		t.Fatalf("Chtimes read: %v", err)
	}

	if err := requeueWaitingMessage(sessionDir, simpleName, filename); err != nil {
		t.Fatalf("requeueWaitingMessage: %v", err)
	}

	if _, err := os.Stat(waitingPath); !os.IsNotExist(err) {
		t.Fatalf("waiting file still present or wrong error: %v", err)
	}
	gotInbox, err := os.ReadFile(inboxPath)
	if err != nil {
		t.Fatalf("ReadFile inbox: %v", err)
	}
	if string(gotInbox) != string(originalContent) {
		t.Fatalf("restored inbox content changed:\n got %q\nwant %q", gotInbox, originalContent)
	}
	gotRead, err := os.ReadFile(readPath)
	if err != nil {
		t.Fatalf("ReadFile read: %v", err)
	}
	if string(gotRead) != string(originalContent) {
		t.Fatalf("read content changed:\n got %q\nwant %q", gotRead, originalContent)
	}
	readInfo, err := os.Stat(readPath)
	if err != nil {
		t.Fatalf("Stat read: %v", err)
	}
	if !readInfo.ModTime().Equal(originalReadTime) {
		t.Fatalf("read modtime changed: got %s want %s", readInfo.ModTime().UTC().Format(time.RFC3339), originalReadTime.Format(time.RFC3339))
	}
}

func TestRequeueWaitingMessage_NoOriginalArtifactMovesMarkerToDeadLetter(t *testing.T) {
	sessionDir := t.TempDir()
	simpleName := "worker"
	filename := "20260328-101502-from-orchestrator-to-worker.md"
	waitingDir := filepath.Join(sessionDir, "waiting")
	deadLetterDir := filepath.Join(sessionDir, "dead-letter")
	inboxDir := filepath.Join(sessionDir, "inbox", simpleName)
	if err := os.MkdirAll(waitingDir, 0o700); err != nil {
		t.Fatalf("MkdirAll waiting: %v", err)
	}
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}

	waitingPath := filepath.Join(waitingDir, filename)
	waitingContent := []byte("---\nstate: composing\n---\n")
	if err := os.WriteFile(waitingPath, waitingContent, 0o600); err != nil {
		t.Fatalf("WriteFile waiting: %v", err)
	}

	if err := requeueWaitingMessage(sessionDir, simpleName, filename); err != nil {
		t.Fatalf("requeueWaitingMessage: %v", err)
	}

	if _, err := os.Stat(waitingPath); !os.IsNotExist(err) {
		t.Fatalf("waiting file still present or wrong error: %v", err)
	}
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		t.Fatalf("ReadDir inbox: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty inbox, found %d entries", len(entries))
	}
	deadLetterPath := filepath.Join(deadLetterDir, deadLetterMissingOriginalName(filename))
	gotDeadLetter, err := os.ReadFile(deadLetterPath)
	if err != nil {
		t.Fatalf("ReadFile dead-letter: %v", err)
	}
	if string(gotDeadLetter) != string(waitingContent) {
		t.Fatalf("dead-letter content changed:\n got %q\nwant %q", gotDeadLetter, waitingContent)
	}
}
