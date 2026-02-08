package idle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
)

// NodeActivity holds activity tracking state for a node (Issue #55).
type NodeActivity struct {
	LastReceived        time.Time
	LastSent            time.Time
	PongReceived        bool
	LastNotifiedDropped time.Time // Issue #56: cooldown tracking for dropped-ball alerts
}

// IdleTracker manages idle detection state (Issue #71).
type IdleTracker struct {
	nodeActivity     map[string]NodeActivity
	lastReminderSent map[string]time.Time
	mu               sync.Mutex
}

// NewIdleTracker creates a new IdleTracker instance (Issue #71).
func NewIdleTracker() *IdleTracker {
	return &IdleTracker{
		nodeActivity:     make(map[string]NodeActivity),
		lastReminderSent: make(map[string]time.Time),
	}
}

// UpdateSendActivity updates the last sent timestamp for a node (Issue #55).
// Issue #79: Use session-prefixed key (sessionName:nodeName) for tracking.
func (t *IdleTracker) UpdateSendActivity(nodeKey string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	activity := t.nodeActivity[nodeKey]
	activity.LastSent = time.Now()
	t.nodeActivity[nodeKey] = activity
}

// UpdateReceiveActivity updates the last received timestamp for a node (Issue #55).
// Issue #79: Use session-prefixed key (sessionName:nodeName) for tracking.
func (t *IdleTracker) UpdateReceiveActivity(nodeKey string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	activity := t.nodeActivity[nodeKey]
	activity.LastReceived = time.Now()
	t.nodeActivity[nodeKey] = activity
}

// MarkPongReceived marks that a node has received PONG confirmation (Issue #55).
// Issue #79: Use session-prefixed key (sessionName:nodeName) for tracking.
func (t *IdleTracker) MarkPongReceived(nodeKey string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	activity := t.nodeActivity[nodeKey]
	activity.PongReceived = true
	t.nodeActivity[nodeKey] = activity
}

// GetNodeStates returns a copy of all node activity states (Issue #55).
func (t *IdleTracker) GetNodeStates() map[string]NodeActivity {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make(map[string]NodeActivity)
	for k, v := range t.nodeActivity {
		result[k] = v
	}
	return result
}

// IsHoldingBall returns true if the node received a message but hasn't sent a reply yet (Issue #55).
// NOTE: IsHoldingBall uses simple timestamp comparison (LastReceived > LastSent).
// This is a heuristic - it may misjudge in multi-sender scenarios.
// For precise tracking, consider per-sender counters (future #56).
// Issue #79: Use session-prefixed key (sessionName:nodeName) for tracking.
func (t *IdleTracker) IsHoldingBall(nodeKey string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	activity, exists := t.nodeActivity[nodeKey]
	if !exists {
		return false
	}
	return !activity.LastReceived.IsZero() && activity.LastReceived.After(activity.LastSent)
}

// CheckDroppedBalls detects nodes holding the ball for too long (Issue #56).
// Returns map of nodeKey -> holding duration for nodes exceeding threshold.
// NOTE: Uses simple timestamp comparison (same limitation as IsHoldingBall).
// Multi-sender scenarios may trigger false positives.
// Issue #79: Use session-prefixed keys for tracking, extract simple name for config lookup.
func (t *IdleTracker) CheckDroppedBalls(nodeConfigs map[string]config.NodeConfig) map[string]time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()

	dropped := make(map[string]time.Duration)
	now := time.Now()

	for nodeKey, activity := range t.nodeActivity {
		// Extract simple name for nodeConfigs lookup
		simpleName := nodeKey
		if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
			simpleName = parts[1]
		}
		cfg, exists := nodeConfigs[simpleName]
		if !exists || cfg.DroppedBallTimeoutSeconds <= 0 {
			continue
		}
		// Skip if PONG not received (handshake incomplete)
		if !activity.PongReceived {
			continue
		}
		// Check holding: LastReceived > LastSent
		if activity.LastReceived.IsZero() || !activity.LastReceived.After(activity.LastSent) {
			continue
		}
		// Check duration
		holdingDuration := now.Sub(activity.LastReceived)
		threshold := time.Duration(cfg.DroppedBallTimeoutSeconds) * time.Second
		if holdingDuration <= threshold {
			continue
		}
		// Check cooldown
		cooldown := time.Duration(cfg.DroppedBallCooldownSeconds) * time.Second
		if cooldown <= 0 {
			cooldown = threshold // default: same as timeout
		}
		if !activity.LastNotifiedDropped.IsZero() && now.Sub(activity.LastNotifiedDropped) < cooldown {
			continue
		}
		dropped[nodeKey] = holdingDuration
	}
	return dropped
}

// MarkDroppedBallNotified marks that a dropped-ball alert was sent for the node (Issue #56).
// Issue #79: Use session-prefixed key (sessionName:nodeName) for tracking.
func (t *IdleTracker) MarkDroppedBallNotified(nodeKey string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if activity, exists := t.nodeActivity[nodeKey]; exists {
		activity.LastNotifiedDropped = time.Now()
		t.nodeActivity[nodeKey] = activity
	}
}

// GetCurrentlyDroppedNodes returns nodes currently in dropped-ball state (Issue #56).
// Unlike CheckDroppedBalls, this does NOT check cooldown - used for TUI display only.
// Issue #79: Use session-prefixed keys for tracking, extract simple name for config lookup.
func (t *IdleTracker) GetCurrentlyDroppedNodes(nodeConfigs map[string]config.NodeConfig) map[string]bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	dropped := make(map[string]bool)
	now := time.Now()

	for nodeKey, activity := range t.nodeActivity {
		// Extract simple name for nodeConfigs lookup
		simpleName := nodeKey
		if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
			simpleName = parts[1]
		}
		cfg, exists := nodeConfigs[simpleName]
		if !exists || cfg.DroppedBallTimeoutSeconds <= 0 {
			continue
		}
		// Skip if PONG not received (handshake incomplete)
		if !activity.PongReceived {
			continue
		}
		// Check holding: LastReceived > LastSent
		if activity.LastReceived.IsZero() || !activity.LastReceived.After(activity.LastSent) {
			continue
		}
		// Check duration (NO cooldown check for TUI display)
		holdingDuration := now.Sub(activity.LastReceived)
		threshold := time.Duration(cfg.DroppedBallTimeoutSeconds) * time.Second
		if holdingDuration > threshold {
			dropped[nodeKey] = true
		}
	}
	return dropped
}

// StartIdleCheck starts a goroutine that periodically checks for idle nodes (Issue #71).
func (t *IdleTracker) StartIdleCheck(ctx context.Context, cfg *config.Config, adjacency map[string][]string, sessionDir string) {
	ticker := time.NewTicker(10 * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				t.checkIdleNodes(cfg, adjacency, sessionDir)
			}
		}
	}()
}

// checkIdleNodes checks all nodes for idle timeout and sends reminders.
// Issue #79: Iterate nodeActivity (session-prefixed keys), extract simple name for config lookup.
func (t *IdleTracker) checkIdleNodes(cfg *config.Config, adjacency map[string][]string, sessionDir string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()

	for nodeKey, activity := range t.nodeActivity {
		// Extract simple name for nodeConfigs lookup
		simpleName := nodeKey
		if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
			simpleName = parts[1]
		}
		nodeConfig, exists := cfg.Nodes[simpleName]
		if !exists || nodeConfig.IdleTimeoutSeconds <= 0 {
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

		// Check cooldown period (use session-prefixed key)
		if lastSent, ok := t.lastReminderSent[nodeKey]; ok {
			cooldown := time.Duration(nodeConfig.IdleReminderCooldownSeconds) * time.Second
			if now.Sub(lastSent) < cooldown {
				continue
			}
		}

		// Send reminder (use simple name for inbox path)
		message := nodeConfig.IdleReminderMessage
		if message == "" {
			message = "Idle reminder: Are you still working?"
		}

		if err := t.sendIdleReminder(cfg, simpleName, message, sessionDir); err != nil {
			_ = err // Suppress unused variable warning
			continue
		}

		// Update last reminder sent timestamp (use session-prefixed key)
		t.lastReminderSent[nodeKey] = now
	}
}

// sendIdleReminder sends an idle reminder message to the specified node.
// Issue #82: Use configurable template for header.
func (t *IdleTracker) sendIdleReminder(cfg *config.Config, nodeName, message, sessionDir string) error {
	inboxDir := filepath.Join(sessionDir, "inbox", nodeName)
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		return fmt.Errorf("creating inbox directory: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("%s-from-postman-to-%s-idle-reminder.md", timestamp, nodeName)
	filePath := filepath.Join(inboxDir, filename)

	// Issue #82: Expand header template
	headerTemplate := cfg.IdleReminderHeaderTemplate
	if headerTemplate == "" {
		headerTemplate = "## Idle Reminder"
	}
	timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
	vars := map[string]string{
		"node": nodeName,
	}
	header := template.ExpandTemplate(headerTemplate, vars, timeout)

	content := fmt.Sprintf("---\nmethod: message/send\nparams:\n  from: postman\n  to: %s\n  timestamp: %s\n---\n\n%s\n\n%s\n",
		nodeName, time.Now().Format(time.RFC3339), header, message)

	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing reminder file: %w", err)
	}

	return nil
}
