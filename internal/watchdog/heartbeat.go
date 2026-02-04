package watchdog

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SendHeartbeat sends a heartbeat message to orchestrator via postman messaging.
// Creates a PING message file in the post/ directory for delivery.
func SendHeartbeat(sessionDir, contextID string) error {
	now := time.Now()
	ts := now.Format("20060102-150405")
	filename := fmt.Sprintf("%s-from-watchdog-to-orchestrator.md", ts)
	postPath := filepath.Join(sessionDir, "post", filename)

	// Build PING message content
	content := fmt.Sprintf(`---
method: message/send
params:
  contextId: %s
  from: watchdog
  to: orchestrator
  timestamp: %s
---

## Heartbeat

Watchdog is alive and monitoring.

Timestamp: %s
`, contextID, now.Format(time.RFC3339), now.Format(time.RFC3339))

	// Write message to post/ directory
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing heartbeat message: %w", err)
	}

	return nil
}

// StartHeartbeat starts a goroutine that sends heartbeat messages at regular intervals.
// Returns a channel that can be closed to stop the heartbeat.
func StartHeartbeat(sessionDir, contextID string, intervalSeconds float64) chan<- struct{} {
	stopChan := make(chan struct{})

	go func() {
		if intervalSeconds <= 0 {
			return // Heartbeat disabled
		}

		interval := time.Duration(intervalSeconds * float64(time.Second))
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				if err := SendHeartbeat(sessionDir, contextID); err != nil {
					// Log error but continue
					fmt.Fprintf(os.Stderr, "⚠️  watchdog: heartbeat failed: %v\n", err)
				}
			}
		}
	}()

	return stopChan
}
