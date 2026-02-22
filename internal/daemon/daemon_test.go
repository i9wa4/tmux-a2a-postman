package daemon

import (
	"testing"
	"time"
)

// TestShouldSendAlert_CooldownBoundary verifies that ShouldSendAlert returns true
// on first call, false immediately after MarkAlertSent, and true again after the
// cooldown window has elapsed.
func TestShouldSendAlert_CooldownBoundary(t *testing.T) {
	ds := NewDaemonState()
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

// TestShouldSendAlert_ZeroCooldown verifies that zero cooldown always returns true.
func TestShouldSendAlert_ZeroCooldown(t *testing.T) {
	ds := NewDaemonState()
	alertKey := "zero_cooldown"

	ds.MarkAlertSent(alertKey)

	// Zero cooldown: always return true regardless of timestamp
	if !ds.ShouldSendAlert(alertKey, 0) {
		t.Error("expected true with zero cooldown")
	}
}
