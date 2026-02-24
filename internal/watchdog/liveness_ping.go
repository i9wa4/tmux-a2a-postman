package watchdog

// Package-level note: this file implements the watchdog liveness ping —
// a periodic "I am alive" signal from the watchdog process to the UI node.
// This is NOT the OpenClaw HEARTBEAT checklist protocol (which is an
// LLM-driven round-trip acknowledgment system). The two are conceptually
// distinct: the watchdog liveness ping is one-way and carries no semantic
// payload; openclaw HEARTBEAT is a round-trip agent-to-human checklist.

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SendLivenessPing sends a liveness ping message to target node via postman messaging.
// Creates a PING message file in the post/ directory for delivery.
// Issue #46: Added uiNode parameter to generalize target node.
func SendLivenessPing(sessionDir, contextID, uiNode string) error {
	now := time.Now()
	// Use UnixNano for uniqueness to prevent filename collisions
	ts := fmt.Sprintf("%s-%d", now.Format("20060102-150405"), now.UnixNano()%1000000)
	// Issue #46: Use uiNode parameter instead of hardcoded "orchestrator"
	filename := fmt.Sprintf("%s-from-watchdog-to-%s.md", ts, uiNode)
	postPath := filepath.Join(sessionDir, "post", filename)

	// Build PING message content
	// Issue #46: Use uiNode parameter instead of hardcoded "orchestrator"
	content := fmt.Sprintf(`---
method: message/send
params:
  contextId: %s
  from: watchdog
  to: %s
  timestamp: %s
---

## Heartbeat

Watchdog is alive and monitoring.

Timestamp: %s
`, contextID, uiNode, now.Format(time.RFC3339), now.Format(time.RFC3339))

	// Write message to post/ directory
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing liveness ping message: %w", err)
	}

	return nil
}

// StartLivenessPing starts a goroutine that sends liveness ping messages at regular intervals.
// Returns a channel that can be closed to stop the liveness ping.
// Issue #46: Added uiNode parameter to generalize target node.
func StartLivenessPing(sessionDir, contextID, uiNode string, intervalSeconds float64) chan<- struct{} {
	stopChan := make(chan struct{})

	go func() {
		if intervalSeconds <= 0 {
			return // Liveness ping disabled
		}

		interval := time.Duration(intervalSeconds * float64(time.Second))
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				// Issue #46: Pass uiNode parameter to SendLivenessPing
				if err := SendLivenessPing(sessionDir, contextID, uiNode); err != nil {
					// Log error but continue
					fmt.Fprintf(os.Stderr, "⚠️  watchdog: liveness ping failed: %v\n", err)
				}
			}
		}
	}()

	return stopChan
}
