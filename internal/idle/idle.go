package idle

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

// NodeActivity holds activity tracking state for a node (Issue #55).
type NodeActivity struct {
	LastReceived time.Time
	LastSent     time.Time
	PongReceived bool
}

// Idle detection state
var (
	nodeActivity     = make(map[string]NodeActivity)
	lastReminderSent = make(map[string]time.Time)
	idleMutex        sync.Mutex
)

// UpdateSendActivity updates the last sent timestamp for a node (Issue #55).
func UpdateSendActivity(nodeName string) {
	idleMutex.Lock()
	defer idleMutex.Unlock()
	activity := nodeActivity[nodeName]
	activity.LastSent = time.Now()
	nodeActivity[nodeName] = activity
}

// UpdateReceiveActivity updates the last received timestamp for a node (Issue #55).
func UpdateReceiveActivity(nodeName string) {
	idleMutex.Lock()
	defer idleMutex.Unlock()
	activity := nodeActivity[nodeName]
	activity.LastReceived = time.Now()
	nodeActivity[nodeName] = activity
}

// MarkPongReceived marks that a node has received PONG confirmation (Issue #55).
func MarkPongReceived(nodeName string) {
	idleMutex.Lock()
	defer idleMutex.Unlock()
	activity := nodeActivity[nodeName]
	activity.PongReceived = true
	nodeActivity[nodeName] = activity
}

// GetNodeStates returns a copy of all node activity states (Issue #55).
func GetNodeStates() map[string]NodeActivity {
	idleMutex.Lock()
	defer idleMutex.Unlock()
	result := make(map[string]NodeActivity)
	for k, v := range nodeActivity {
		result[k] = v
	}
	return result
}

// IsHoldingBall returns true if the node received a message but hasn't sent a reply yet (Issue #55).
// NOTE: IsHoldingBall uses simple timestamp comparison (LastReceived > LastSent).
// This is a heuristic - it may misjudge in multi-sender scenarios.
// For precise tracking, consider per-sender counters (future #56).
func IsHoldingBall(nodeName string) bool {
	idleMutex.Lock()
	defer idleMutex.Unlock()
	activity, exists := nodeActivity[nodeName]
	if !exists {
		return false
	}
	return !activity.LastReceived.IsZero() && activity.LastReceived.After(activity.LastSent)
}

// StartIdleCheck starts a goroutine that periodically checks for idle nodes.
func StartIdleCheck(cfg *config.Config, adjacency map[string][]string, sessionDir string) {
	ticker := time.NewTicker(10 * time.Second) // Check every 10 seconds
	go func() {
		for range ticker.C {
			checkIdleNodes(cfg, adjacency, sessionDir)
		}
	}()
}

// checkIdleNodes checks all nodes for idle timeout and sends reminders.
func checkIdleNodes(cfg *config.Config, adjacency map[string][]string, sessionDir string) {
	idleMutex.Lock()
	defer idleMutex.Unlock()

	now := time.Now()

	for nodeName, nodeConfig := range cfg.Nodes {
		// Skip if idle timeout not configured
		if nodeConfig.IdleTimeoutSeconds <= 0 {
			continue
		}

		// Skip if no last activity recorded yet (Issue #55: use max of LastSent/LastReceived)
		activity, exists := nodeActivity[nodeName]
		if !exists {
			continue
		}

		// Use max of LastSent and LastReceived for idle detection
		lastAct := activity.LastSent
		if activity.LastReceived.After(lastAct) {
			lastAct = activity.LastReceived
		}
		if lastAct.IsZero() {
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
			_ = err // Suppress unused variable warning
			continue
		}

		// Update last reminder sent timestamp
		lastReminderSent[nodeName] = now
	}
}

// sendIdleReminder sends an idle reminder message to the specified node.
func sendIdleReminder(nodeName, message, sessionDir string) error {
	inboxDir := filepath.Join(sessionDir, "inbox", nodeName)
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		return fmt.Errorf("creating inbox directory: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("%s-from-postman-to-%s-idle-reminder.md", timestamp, nodeName)
	filePath := filepath.Join(inboxDir, filename)

	content := fmt.Sprintf("---\nmethod: message/send\nparams:\n  from: postman\n  to: %s\n  timestamp: %s\n---\n\n## Idle Reminder\n\n%s\n",
		nodeName, time.Now().Format(time.RFC3339), message)

	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing reminder file: %w", err)
	}

	return nil
}
