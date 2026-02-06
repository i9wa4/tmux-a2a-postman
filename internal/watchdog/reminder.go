package watchdog

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ReminderState tracks the last reminder time for each pane to implement cooldown.
type ReminderState struct {
	mu              sync.Mutex
	lastReminderMap map[string]time.Time
}

// NewReminderState creates a new ReminderState.
func NewReminderState() *ReminderState {
	return &ReminderState{
		lastReminderMap: make(map[string]time.Time),
	}
}

// ShouldSendReminder checks if a reminder should be sent based on cooldown.
// Returns true if enough time has passed since the last reminder.
func (r *ReminderState) ShouldSendReminder(paneID string, cooldownSeconds float64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if cooldownSeconds <= 0 {
		return true // No cooldown, always send
	}

	lastReminder, exists := r.lastReminderMap[paneID]
	if !exists {
		return true // First reminder
	}

	cooldown := time.Duration(cooldownSeconds * float64(time.Second))
	return time.Since(lastReminder) > cooldown
}

// MarkReminderSent records that a reminder was sent for the given pane.
func (r *ReminderState) MarkReminderSent(paneID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastReminderMap[paneID] = time.Now()
}

// SendIdleReminder sends an idle reminder message via postman messaging.
// Creates a message file in the post/ directory for delivery.
// Issue #46: Added uiNode parameter to generalize target node.
func SendIdleReminder(paneID, sessionDir, contextID, uiNode string, activity PaneActivity) error {
	now := time.Now()
	// Use UnixNano for uniqueness to prevent filename collisions
	ts := fmt.Sprintf("%s-%d", now.Format("20060102-150405"), now.UnixNano()%1000000)
	// Issue #46: Use uiNode parameter instead of hardcoded "orchestrator"
	filename := fmt.Sprintf("%s-from-watchdog-to-%s.md", ts, uiNode)
	postPath := filepath.Join(sessionDir, "post", filename)

	// Calculate idle duration
	idleDuration := time.Since(activity.LastActivityTime)

	// Build message content
	// Issue #46: Use uiNode parameter instead of hardcoded "orchestrator"
	content := fmt.Sprintf(`---
method: message/send
params:
  contextId: %s
  from: watchdog
  to: %s
  timestamp: %s
---

## Idle Alert

Pane %s has been idle for %s.

Last activity: %s
`, contextID, uiNode, now.Format(time.RFC3339), paneID, idleDuration.Round(time.Second), activity.LastActivityTime.Format(time.RFC3339))

	// Write message to post/ directory
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing reminder message: %w", err)
	}

	return nil
}
