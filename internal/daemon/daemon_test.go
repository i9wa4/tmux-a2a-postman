package daemon

import (
	"testing"
	"time"
)

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
