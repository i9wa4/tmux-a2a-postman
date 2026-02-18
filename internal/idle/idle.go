package idle

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
)

// NodeActivity holds activity tracking state for a node (Issue #55).
type NodeActivity struct {
	LastReceived        time.Time
	LastSent            time.Time
	PongReceived        bool
	LastNotifiedDropped time.Time // Issue #56: cooldown tracking for dropped-ball alerts
	LastScreenChange    time.Time // Last screen content change (for debug/display only, not used for idle detection)
}

// PaneCaptureState holds pane capture state for hybrid idle detection.
type PaneCaptureState struct {
	LastHash      uint32    // CRC32 hash of pane content
	LastChangeAt  time.Time // Last time content change was detected
	ChangeCount   int       // Consecutive change count (2 = active)
	LastCaptureAt time.Time // Last capture time
}

// IdleTracker manages idle detection state (Issue #71).
type IdleTracker struct {
	nodeActivity     map[string]NodeActivity
	lastReminderSent map[string]time.Time
	paneCaptureState map[string]PaneCaptureState // paneKey -> PaneCaptureState
	mu               sync.Mutex
}

// NewIdleTracker creates a new IdleTracker instance (Issue #71).
func NewIdleTracker() *IdleTracker {
	return &IdleTracker{
		nodeActivity:     make(map[string]NodeActivity),
		lastReminderSent: make(map[string]time.Time),
		paneCaptureState: make(map[string]PaneCaptureState),
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

// GetPongActiveNodes returns a set of node keys that have received PONG (Issue #84).
// Returns non-nil map (empty if no PONG received).
// NOTE: PONG-active status is informational (UX), not an access control mechanism.
func (t *IdleTracker) GetPongActiveNodes() map[string]bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make(map[string]bool)
	for key, activity := range t.nodeActivity {
		if activity.PongReceived {
			result[key] = true
		}
	}
	return result
}

// GetPaneActivityStatus returns pane activity status based on idle.go logic.
// Returns map of paneID -> isActive (true if 2+ content changes within activity window).
// Issue #120: Expose paneCaptureState for get-session-status-oneline command.
func (t *IdleTracker) GetPaneActivityStatus(cfg *config.Config) map[string]bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	result := make(map[string]bool)
	now := time.Now()
	activityWindow := time.Duration(cfg.ActivityWindowSeconds) * time.Second

	for paneID, state := range t.paneCaptureState {
		// A pane is considered active if:
		// 1. It has had content changes recently (within activity window)
		// 2. The change count reached 2+ (indicating consecutive changes)
		// We check if the last change was within the activity window and change count is recent
		timeSinceLastChange := now.Sub(state.LastChangeAt)
		isActive := timeSinceLastChange <= activityWindow && state.ChangeCount > 0
		result[paneID] = isActive
	}

	return result
}

// ExportPaneActivityToFile writes pane activity status to a JSON file.
// Issue #120: Export state for get-session-status-oneline command.
func (t *IdleTracker) ExportPaneActivityToFile(cfg *config.Config, filepath string) error {
	status := t.GetPaneActivityStatus(cfg)
	data, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("marshaling pane activity: %w", err)
	}
	return os.WriteFile(filepath, data, 0o644)
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

// capturePaneContent captures the visible content of a tmux pane.
// Returns the content as a string, or empty string on error.
func capturePaneContent(paneID string) (string, error) {
	cmd := exec.Command("tmux", "capture-pane", "-p", "-t", paneID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("capturing pane %s: %w", paneID, err)
	}
	return string(output), nil
}

// hashContentCRC32 computes CRC32 hash of the content.
func hashContentCRC32(content string) uint32 {
	return crc32.ChecksumIEEE([]byte(content))
}

// checkPaneCapture performs pane content capture and updates NodeActivity on consecutive changes.
// Issue #xxx: Hybrid idle detection with screen capture.
func (t *IdleTracker) checkPaneCapture(cfg *config.Config, nodes map[string]discovery.NodeInfo) {
	if !cfg.PaneCaptureEnabled {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Get all pane IDs
	cmd := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Failed to list panes - skip this check
		return
	}

	// Build paneID -> nodeKey mapping from nodes
	paneToNode := make(map[string]string) // paneID -> nodeKey (session:node format)
	for nodeName, nodeInfo := range nodes {
		sessionKey := nodeInfo.SessionName + ":" + nodeName
		paneToNode[nodeInfo.PaneID] = sessionKey
	}

	// Parse pane IDs and filter to node panes only (MUST 3: node panes first)
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var nodePaneIDs []string
	for _, line := range lines {
		paneID := strings.TrimSpace(line)
		if paneID == "" {
			continue
		}
		// Only include panes that belong to nodes
		if _, isNode := paneToNode[paneID]; isNode {
			nodePaneIDs = append(nodePaneIDs, paneID)
		}
	}

	// Apply max_panes limit after filtering to node panes (0 = unlimited)
	maxPanes := cfg.PaneCaptureMaxPanes
	if maxPanes > 0 && len(nodePaneIDs) > maxPanes {
		nodePaneIDs = nodePaneIDs[:maxPanes]
	}

	now := time.Now()

	for _, paneID := range nodePaneIDs {
		// Capture pane content
		content, err := capturePaneContent(paneID)
		if err != nil {
			// MUST 2: Capture failed - treat as "unmeasurable", skip but keep state
			// Do NOT delete state - carry forward to next poll
			continue
		}

		// Compute CRC32 hash
		currentHash := hashContentCRC32(content)

		// Get previous state
		state, exists := t.paneCaptureState[paneID]
		if !exists {
			// First time seeing this pane - initialize state
			t.paneCaptureState[paneID] = PaneCaptureState{
				LastHash:      currentHash,
				LastChangeAt:  now,
				ChangeCount:   0,
				LastCaptureAt: now,
			}
			continue
		}

		// Update last capture time
		state.LastCaptureAt = now

		// Check if content changed
		if currentHash != state.LastHash {
			// MUST 1: Check if within activity window from last change
			activityWindow := time.Duration(cfg.ActivityWindowSeconds) * time.Second
			timeSinceLastChange := now.Sub(state.LastChangeAt)
			if timeSinceLastChange > activityWindow {
				// Too much time elapsed - reset change count
				state.ChangeCount = 1
			} else {
				// Within time window - increment change count
				state.ChangeCount++
			}

			// Update hash and timestamp
			state.LastHash = currentHash
			state.LastChangeAt = now

			// Check if this is the 2nd consecutive change (within 120 seconds)
			if state.ChangeCount >= 2 {
				// Mark as active - update NodeActivity
				nodeKey, hasNode := paneToNode[paneID]
				if hasNode {
					activity := t.nodeActivity[nodeKey]
					// Update screen change timestamp (for debug/display only)
					activity.LastScreenChange = now
					t.nodeActivity[nodeKey] = activity
				}
				// Reset change count after marking active
				state.ChangeCount = 0
			}
		} else {
			// Content unchanged - reset change count
			state.ChangeCount = 0
		}

		// Save updated state
		t.paneCaptureState[paneID] = state
	}

	// MUST 5: Clean up stale entries (memory leak prevention)
	// Remove entries where LastCaptureAt is older than stale threshold
	staleThreshold := time.Duration(cfg.NodeStaleSeconds * float64(time.Second))
	for paneID, state := range t.paneCaptureState {
		if !state.LastCaptureAt.IsZero() && now.Sub(state.LastCaptureAt) > staleThreshold {
			delete(t.paneCaptureState, paneID)
		}
	}
}

// StartPaneCaptureCheck starts a goroutine that periodically captures pane content.
// Issue #xxx: Hybrid idle detection with screen capture.
func (t *IdleTracker) StartPaneCaptureCheck(ctx context.Context, cfg *config.Config, baseDir string, contextID string) {
	if !cfg.PaneCaptureEnabled || cfg.PaneCaptureIntervalSeconds <= 0 {
		return // Pane capture disabled
	}

	interval := time.Duration(cfg.PaneCaptureIntervalSeconds * float64(time.Second))
	ticker := time.NewTicker(interval)

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Discover nodes (edge-filtered)
				nodes, err := discovery.DiscoverNodes(baseDir, contextID)
				if err != nil {
					continue
				}
				edgeNodes := config.GetEdgeNodeNames(cfg.Edges)
				for nodeName := range nodes {
					parts := strings.SplitN(nodeName, ":", 2)
					rawName := parts[len(parts)-1]
					if !edgeNodes[rawName] {
						delete(nodes, nodeName)
					}
				}

				// Perform pane capture check
				t.checkPaneCapture(cfg, nodes)

				// Issue #120: Export pane activity status to file for CLI access
				stateFile := filepath.Join(baseDir, contextID, "pane-activity.json")
				_ = t.ExportPaneActivityToFile(cfg, stateFile)
			}
		}
	}()
}
