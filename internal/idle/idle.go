package idle

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/paneutil"
)

const compactionPingCooldown = 30 * time.Second

// NodeActivity holds activity tracking state for a node (Issue #55).
type NodeActivity struct {
	LastReceived      time.Time
	LastSent          time.Time
	LivenessConfirmed bool
	LastScreenChange  time.Time // Last screen content change (for debug/display only, not used for idle detection)
}

// PaneActivityExport holds pane activity data for JSON export.
// Issue #123: Enriched format with lastChangeAt for external consumers.
type PaneActivityExport struct {
	Status       string    `json:"status"`
	LastChangeAt time.Time `json:"lastChangeAt"`
}

// PaneCaptureState holds pane capture state for hybrid idle detection.
type PaneCaptureState struct {
	LastHash              uint32    // CRC32 hash of pane content
	LastChangeAt          time.Time // Last time content change was detected
	ChangeCount           int       // Consecutive change count (2 = active)
	LastCaptureAt         time.Time // Last capture time
	LastCompactionPingAt  time.Time // Last compaction-triggered PING for this pane
	LastCompactionTrigger string    // Non-empty while a compaction marker remains visible
	LastCompactionHash    uint32    // Full pane hash for the most recent compaction ping
}

// IdleTracker manages idle detection state (Issue #71).
type IdleTracker struct {
	nodeActivity     map[string]NodeActivity
	paneCaptureState map[string]PaneCaptureState // paneKey -> PaneCaptureState
	mu               sync.Mutex
}

// NewIdleTracker creates a new IdleTracker instance (Issue #71).
func NewIdleTracker() *IdleTracker {
	return &IdleTracker{
		nodeActivity:     make(map[string]NodeActivity),
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

// MarkNodeAlive marks that a node has confirmed liveness (Issue #55).
// Issue #79: Use session-prefixed key (sessionName:nodeName) for tracking.
func (t *IdleTracker) MarkNodeAlive(nodeKey string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	activity := t.nodeActivity[nodeKey]
	activity.LivenessConfirmed = true
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

// GetLivenessMap returns a set of node keys that have confirmed liveness (Issue #84).
// Returns non-nil map (empty if no liveness confirmed).
// NOTE: Liveness status is informational (UX), not an access control mechanism.
func (t *IdleTracker) GetLivenessMap() map[string]bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make(map[string]bool)
	for key, activity := range t.nodeActivity {
		if activity.LivenessConfirmed {
			result[key] = true
		}
	}
	return result
}

// statusForState returns "active", "idle", or "stale" for a pane capture state.
// Lock-free — caller must hold t.mu.
func statusForState(state PaneCaptureState, now time.Time, cfg *config.Config) string {
	if state.LastChangeAt.IsZero() {
		return "stale"
	}
	switch elapsed := now.Sub(state.LastChangeAt); {
	case elapsed <= time.Duration(cfg.NodeActiveSeconds)*time.Second:
		return "active"
	case elapsed <= time.Duration(cfg.NodeIdleSeconds)*time.Second:
		return "idle"
	default:
		return "stale"
	}
}

// GetPaneActivityStatus returns pane activity status based on idle.go logic.
// Returns map of paneID -> status ("active"/"idle"/"stale").
// Issue #120: Expose paneCaptureState for get-health-oneline.
func (t *IdleTracker) GetPaneActivityStatus(cfg *config.Config) map[string]string {
	t.mu.Lock()
	defer t.mu.Unlock()

	result := make(map[string]string)
	now := time.Now()

	for paneID, state := range t.paneCaptureState {
		result[paneID] = statusForState(state, now, cfg)
	}

	return result
}

// ExportPaneActivityToFile writes pane activity status to a JSON file.
// Issue #120: Export state for get-health-oneline.
// Issue #123: Enriched format — writes map[string]PaneActivityExport instead of map[string]string.
func (t *IdleTracker) ExportPaneActivityToFile(cfg *config.Config, filePath string) error {
	t.mu.Lock()
	now := time.Now()
	export := make(map[string]PaneActivityExport, len(t.paneCaptureState))
	for paneID, state := range t.paneCaptureState {
		export[paneID] = PaneActivityExport{
			Status:       statusForState(state, now, cfg),
			LastChangeAt: state.LastChangeAt,
		}
	}
	t.mu.Unlock()

	data, err := json.Marshal(export)
	if err != nil {
		return fmt.Errorf("marshaling pane activity: %w", err)
	}
	return os.WriteFile(filePath, data, 0o600)
}

// hashContentCRC32 computes CRC32 hash of the content.
func hashContentCRC32(content string) uint32 {
	return crc32.ChecksumIEEE([]byte(content))
}

func containsCompactionTrigger(runtime, content string) bool {
	return compactionTrigger(runtime, content) != ""
}

func compactionTrigger(runtime, content string) string {
	switch strings.ToLower(strings.TrimSpace(runtime)) {
	case "claude":
		return claudeCompactionTrigger(content)
	case "codex":
		return codexCompactionTrigger(content)
	default:
		return ""
	}
}

func claudeCompactionTrigger(content string) string {
	for _, line := range strings.Split(content, "\n") {
		normalized := normalizeStatusLine(line)
		if strings.HasPrefix(normalized, "conversation compacted") {
			return "claude:conversation-compaction"
		}
		if strings.HasPrefix(normalized, "compacted (ctrl+o") {
			return "claude:conversation-compaction"
		}
	}
	return ""
}

func codexCompactionTrigger(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if isCodexCompactionLine(line) {
			return "codex:context-compaction"
		}
	}
	return ""
}

func isCodexCompactionLine(line string) bool {
	return normalizeStatusLine(line) == "context compacted"
}

func normalizeStatusLine(line string) string {
	normalized := strings.ToLower(strings.TrimSpace(line))
	normalized = strings.TrimLeftFunc(normalized, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	return strings.TrimSpace(normalized)
}

func filterPaneCaptureNodes(nodes map[string]discovery.NodeInfo, edgeNodes map[string]bool) map[string]discovery.NodeInfo {
	filtered := make(map[string]discovery.NodeInfo)
	for nodeName, nodeInfo := range nodes {
		if !config.EdgeNodeAllowed(edgeNodes, nodeName) {
			continue
		}
		filtered[nodeName] = nodeInfo
	}
	return filtered
}

// checkPaneCapture performs pane content capture and updates NodeActivity on consecutive changes.
// Issue #xxx: Hybrid idle detection with screen capture.
func (t *IdleTracker) checkPaneCapture(cfg *config.Config, nodes map[string]discovery.NodeInfo) []string {
	if !config.BoolVal(cfg.PaneCaptureEnabled, true) {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Get all pane IDs and runtimes.
	cmd := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id}\t#{pane_current_command}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Failed to list panes - skip this check
		return nil
	}

	// Build paneID -> nodeKey mapping from nodes
	paneToNode := make(map[string]string) // paneID -> nodeKey (session:node format)
	for nodeName, nodeInfo := range nodes {
		paneToNode[nodeInfo.PaneID] = nodeName
	}

	// Parse pane IDs and filter to node panes only (MUST 3: node panes first)
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var nodePaneIDs []string
	paneRuntimes := make(map[string]string)
	for _, line := range lines {
		parts := strings.SplitN(strings.TrimSpace(line), "\t", 2)
		paneID := strings.TrimSpace(parts[0])
		if paneID == "" {
			continue
		}
		if len(parts) == 2 {
			paneRuntimes[paneID] = strings.TrimSpace(parts[1])
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
	compactionTargets := make(map[string]struct{})

	for _, paneID := range nodePaneIDs {
		// Capture pane content
		content, err := paneutil.CaptureContent(paneID)
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
			state = PaneCaptureState{
				LastHash:      currentHash,
				LastChangeAt:  now,
				ChangeCount:   0,
				LastCaptureAt: now,
			}
			if nodeKey, hasNode := paneToNode[paneID]; hasNode {
				if trigger := compactionTrigger(paneRuntimes[paneID], content); trigger != "" {
					state.LastCompactionTrigger = trigger
					state.LastCompactionPingAt = now
					state.LastCompactionHash = currentHash
					compactionTargets[nodeKey] = struct{}{}
				}
			}
			t.paneCaptureState[paneID] = state
			continue
		}

		// Update last capture time
		state.LastCaptureAt = now
		if nodeKey, hasNode := paneToNode[paneID]; hasNode {
			if trigger := compactionTrigger(paneRuntimes[paneID], content); trigger != "" {
				if state.LastCompactionTrigger == "" && state.LastCompactionHash != currentHash && (state.LastCompactionPingAt.IsZero() || now.Sub(state.LastCompactionPingAt) >= compactionPingCooldown) {
					state.LastCompactionPingAt = now
					state.LastCompactionHash = currentHash
					compactionTargets[nodeKey] = struct{}{}
				}
				state.LastCompactionTrigger = trigger
			} else {
				state.LastCompactionTrigger = ""
			}
		}

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

	targetNodeKeys := make([]string, 0, len(compactionTargets))
	for nodeKey := range compactionTargets {
		targetNodeKeys = append(targetNodeKeys, nodeKey)
	}
	sort.Strings(targetNodeKeys)
	return targetNodeKeys
}

// StartPaneCaptureCheck starts a goroutine that periodically captures pane content.
// Issue #xxx: Hybrid idle detection with screen capture.
func (t *IdleTracker) StartPaneCaptureCheck(ctx context.Context, cfg *config.Config, baseDir string, contextID string, selfSession string, onCompactionPing func(map[string]discovery.NodeInfo, []string)) {
	if !config.BoolVal(cfg.PaneCaptureEnabled, true) || cfg.PaneCaptureIntervalSeconds <= 0 {
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
				nodes, _, err := discovery.DiscoverNodesWithCollisions(baseDir, contextID, selfSession)
				if err != nil {
					continue
				}
				nodes = filterPaneCaptureNodes(nodes, config.GetEdgeNodeNames(cfg.Edges))

				// Perform pane capture check
				compactionTargets := t.checkPaneCapture(cfg, nodes)
				if onCompactionPing != nil && len(compactionTargets) > 0 {
					onCompactionPing(nodes, compactionTargets)
				}

				// Issue #120: Export pane activity status to file for CLI access
				stateFile := filepath.Join(baseDir, contextID, "pane-activity.json")
				_ = t.ExportPaneActivityToFile(cfg, stateFile)
			}
		}
	}()
}
