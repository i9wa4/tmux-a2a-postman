package sessionidle

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

// SessionIdleState tracks the last check time for each session to implement cooldown.
type SessionIdleState struct {
	mu              sync.Mutex
	lastAlertMap    map[string]time.Time
	lastActivityMap map[string]map[string]time.Time // sessionName -> nodeName -> lastActivityTime
}

// NewSessionIdleState creates a new SessionIdleState.
func NewSessionIdleState() *SessionIdleState {
	return &SessionIdleState{
		lastAlertMap:    make(map[string]time.Time),
		lastActivityMap: make(map[string]map[string]time.Time),
	}
}

// PaneActivity holds pane activity information.
type PaneActivity struct {
	PaneID           string
	LastActivityTime time.Time
}

// GetPaneActivities retrieves activity information for all tmux panes.
// Parses output of: tmux list-panes -a -F "#{pane_activity} #{pane_id}"
func GetPaneActivities() ([]PaneActivity, error) {
	cmd := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_activity} #{pane_id}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("listing panes: %w: %s", err, output)
	}

	var activities []PaneActivity
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}

		// Parse "#{pane_activity} #{pane_id}"
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}

		// Parse Unix timestamp (seconds since epoch)
		timestamp, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}

		// Skip panes with activity timestamp of 0 (never active or just created)
		if timestamp == 0 {
			continue
		}

		activities = append(activities, PaneActivity{
			PaneID:           parts[1],
			LastActivityTime: time.Unix(timestamp, 0),
		})
	}

	return activities, nil
}

// CheckSessionIdle checks if all nodes in each session are idle.
// Returns a list of session names that are fully idle.
// Only monitors nodes that are connected via edges (adjacency map).
func (s *SessionIdleState) CheckSessionIdle(
	nodes map[string]discovery.NodeInfo,
	adjacency map[string][]string,
	idleThresholdSeconds float64,
	cooldownSeconds float64,
) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if idleThresholdSeconds <= 0 {
		return nil, nil // Idle detection disabled
	}

	// Get pane activities
	activities, err := GetPaneActivities()
	if err != nil {
		return nil, fmt.Errorf("getting pane activities: %w", err)
	}

	// Build paneID -> activity map
	activityMap := make(map[string]PaneActivity)
	for _, activity := range activities {
		activityMap[activity.PaneID] = activity
	}

	// Group nodes by session
	sessionNodes := make(map[string][]string)
	nodePaneMap := make(map[string]string) // nodeName -> paneID
	for nodeName, nodeInfo := range nodes {
		// Only include nodes connected via edges
		if _, connected := adjacency[nodeName]; !connected {
			continue // Skip nodes not in edges
		}
		sessionNodes[nodeInfo.SessionName] = append(sessionNodes[nodeInfo.SessionName], nodeName)
		nodePaneMap[nodeName] = nodeInfo.PaneID
	}

	// Initialize lastActivityMap for new sessions
	for sessionName := range sessionNodes {
		if _, exists := s.lastActivityMap[sessionName]; !exists {
			s.lastActivityMap[sessionName] = make(map[string]time.Time)
		}
	}

	// Check each session
	threshold := time.Duration(idleThresholdSeconds * float64(time.Second))
	cooldown := time.Duration(cooldownSeconds * float64(time.Second))
	now := time.Now()
	var idleSessions []string

	for sessionName, nodeNames := range sessionNodes {
		// Skip if recently alerted
		if lastAlert, exists := s.lastAlertMap[sessionName]; exists {
			if now.Sub(lastAlert) < cooldown {
				continue
			}
		}

		// Check if ALL nodes in session are idle
		allIdle := true
		for _, nodeName := range nodeNames {
			paneID := nodePaneMap[nodeName]
			activity, found := activityMap[paneID]

			if !found {
				// Pane not found in activity list, consider as not idle
				allIdle = false
				break
			}

			// Update last activity map
			s.lastActivityMap[sessionName][nodeName] = activity.LastActivityTime

			// Check if idle
			timeSinceActivity := now.Sub(activity.LastActivityTime)
			if timeSinceActivity < threshold {
				allIdle = false
				break
			}
		}

		if allIdle && len(nodeNames) > 0 {
			idleSessions = append(idleSessions, sessionName)
			s.lastAlertMap[sessionName] = now
		}
	}

	return idleSessions, nil
}

// SendWatchdogAlert sends an idle alert message to the watchdog node.
func SendWatchdogAlert(
	sessionName string,
	nodes map[string]discovery.NodeInfo,
	adjacency map[string][]string,
	sessionDir string,
	contextID string,
	cfg *config.Config,
) error {
	// Find watchdog node
	watchdogInfo, watchdogExists := nodes["watchdog"]
	if !watchdogExists {
		return fmt.Errorf("watchdog node not found")
	}

	// Build list of idle nodes in this session
	var idleNodes []string
	for nodeName, nodeInfo := range nodes {
		if nodeInfo.SessionName == sessionName {
			idleNodes = append(idleNodes, nodeName)
		}
	}

	// Build talks_to_line for watchdog using adjacency map
	talksToLine := "Can talk to: (none)"
	if watchdogTalksTo, exists := adjacency["watchdog"]; exists && len(watchdogTalksTo) > 0 {
		talksToLine = fmt.Sprintf("Can talk to: %s", strings.Join(watchdogTalksTo, ", "))
	}

	// Build message content
	now := time.Now()
	// Use UnixNano for uniqueness to prevent filename collisions
	ts := fmt.Sprintf("%s-%d", now.Format("20060102-150405"), now.UnixNano()%1000000)
	filename := fmt.Sprintf("%s-from-postman-to-watchdog.md", ts)

	content := fmt.Sprintf("---\nmethod: message/send\nparams:\n  contextId: %s\n  from: postman\n  to: watchdog\n  timestamp: %s\n---\n\n## Idle Alert\n\ntmux session `%s` の全ノードが停止しています。\n\nIdle nodes: %s\n\n%s\n\nReply: `tmux-a2a-postman create-draft --to <node>`\n",
		contextID, now.Format(time.RFC3339), sessionName, strings.Join(idleNodes, ", "), talksToLine)

	// Write to watchdog inbox
	watchdogInbox := filepath.Join(watchdogInfo.SessionDir, "inbox", "watchdog")
	if err := os.MkdirAll(watchdogInbox, 0o755); err != nil {
		return fmt.Errorf("creating watchdog inbox: %w", err)
	}

	alertPath := filepath.Join(watchdogInbox, filename)
	if err := os.WriteFile(alertPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing alert message: %w", err)
	}

	return nil
}

// StartSessionIdleCheck starts a goroutine that periodically checks for idle sessions.
// Returns a channel that can be closed to stop the check.
func StartSessionIdleCheck(
	baseDir string,
	contextID string,
	sessionDir string,
	cfg *config.Config,
	adjacency map[string][]string,
	checkIntervalSeconds float64,
) chan<- struct{} {
	stopChan := make(chan struct{})
	state := NewSessionIdleState()

	go func() {
		if checkIntervalSeconds <= 0 || cfg.Watchdog.IdleThresholdSeconds <= 0 {
			return // Session idle detection disabled
		}

		interval := time.Duration(checkIntervalSeconds * float64(time.Second))
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				// Discover nodes
				nodes, err := discovery.DiscoverNodes(baseDir, contextID)
				if err != nil {
					continue
				}

				// Check for idle sessions
				idleSessions, err := state.CheckSessionIdle(
					nodes,
					adjacency,
					cfg.Watchdog.IdleThresholdSeconds,
					cfg.Watchdog.CooldownSeconds,
				)
				if err != nil {
					continue
				}

				// Send alerts for idle sessions
				for _, sessionName := range idleSessions {
					if err := SendWatchdogAlert(sessionName, nodes, adjacency, sessionDir, contextID, cfg); err != nil {
						// Log error but continue
						fmt.Fprintf(os.Stderr, "⚠️  postman: session idle alert failed for %s: %v\n", sessionName, err)
					}
				}
			}
		}
	}()

	return stopChan
}
