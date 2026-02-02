package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Idle detection state
var (
	lastActivity     = make(map[string]time.Time)
	lastReminderSent = make(map[string]time.Time)
	idleMutex        sync.Mutex
)

// UpdateActivity updates the last activity timestamp for a node.
func UpdateActivity(nodeName string) {
	idleMutex.Lock()
	defer idleMutex.Unlock()
	lastActivity[nodeName] = time.Now()
}

// startIdleCheck starts a goroutine that periodically checks for idle nodes.
func startIdleCheck(cfg *Config, adjacency map[string][]string, sessionDir string) {
	ticker := time.NewTicker(10 * time.Second) // Check every 10 seconds
	go func() {
		for range ticker.C {
			checkIdleNodes(cfg, adjacency, sessionDir)
		}
	}()
}

// checkIdleNodes checks all nodes for idle timeout and sends reminders.
func checkIdleNodes(cfg *Config, adjacency map[string][]string, sessionDir string) {
	idleMutex.Lock()
	defer idleMutex.Unlock()

	now := time.Now()

	for nodeName, nodeConfig := range cfg.Nodes {
		// Skip if idle timeout not configured
		if nodeConfig.IdleTimeoutSeconds <= 0 {
			continue
		}

		// Skip if no last activity recorded yet
		lastAct, exists := lastActivity[nodeName]
		if !exists {
			continue
		}

		// Check if idle threshold exceeded
		idleDuration := now.Sub(lastAct)
		if idleDuration.Seconds() < nodeConfig.IdleTimeoutSeconds {
			continue
		}

		// Check cooldown period
		if lastSent, ok := lastReminderSent[nodeName]; ok {
			cooldown := time.Duration(nodeConfig.IdleReminderCooldownSeconds) * time.Second
			if now.Sub(lastSent) < cooldown {
				continue
			}
		}

		// Send reminder
		message := nodeConfig.IdleReminderMessage
		if message == "" {
			message = "Idle reminder: Are you still working?"
		}

		if err := sendIdleReminder(nodeName, message, sessionDir); err != nil {
			fmt.Fprintf(os.Stderr, "postman: idle reminder to %s failed: %v\n", nodeName, err)
			continue
		}

		// Update last reminder sent timestamp
		lastReminderSent[nodeName] = now
		fmt.Fprintf(os.Stderr, "postman: idle reminder sent to %s\n", nodeName)
	}
}

// sendIdleReminder sends an idle reminder message to the specified node.
func sendIdleReminder(nodeName, message, sessionDir string) error {
	inboxDir := filepath.Join(sessionDir, "inbox", nodeName)
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		return fmt.Errorf("creating inbox directory: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("%s-from-postman-idle-reminder.md", timestamp)
	filePath := filepath.Join(inboxDir, filename)

	content := fmt.Sprintf("---\nmethod: message/send\nparams:\n  from: postman\n  to: %s\n  timestamp: %s\n---\n\n## Idle Reminder\n\n%s\n",
		nodeName, time.Now().Format(time.RFC3339), message)

	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing reminder file: %w", err)
	}

	return nil
}
