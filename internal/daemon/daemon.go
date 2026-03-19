package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/i9wa4/tmux-a2a-postman/internal/alert"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/heartbeat"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/reminder"
	"github.com/i9wa4/tmux-a2a-postman/internal/session"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
	"github.com/i9wa4/tmux-a2a-postman/internal/uinode"
)

const inboxCheckInterval = 30 * time.Second // Issue #239: ticker interval for inbox stagnation checks

// safeAfterFunc wraps time.AfterFunc with panic recovery (Issue #57).
func safeAfterFunc(d time.Duration, name string, events chan<- tui.DaemonEvent, fn func()) *time.Timer {
	return time.AfterFunc(d, func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				log.Printf("🚨 PANIC in timer callback %q: %v\n%s\n", name, r, string(stack))
				if events != nil {
					events <- tui.DaemonEvent{
						Type:    "error",
						Message: fmt.Sprintf("Internal error in %s (recovered)", name),
					}
				}
			}
		}()
		fn()
	})
}

// replaceWaitingState replaces the state field value within YAML frontmatter only,
// and updates state_updated_at to the current time (#175).
// Scoped to frontmatter to prevent accidental replacement of state mentions in message body.
func replaceWaitingState(content, oldState, newState string) string {
	// Find frontmatter boundaries
	first := strings.Index(content, "---\n")
	if first < 0 {
		return content
	}
	rest := content[first+4:]
	second := strings.Index(rest, "\n---")
	if second < 0 {
		return content
	}
	fm := rest[:second]
	after := rest[second:]

	// Replace state in frontmatter only
	fm = strings.Replace(fm, "state: "+oldState, "state: "+newState, 1)

	// Update state_updated_at
	now := time.Now().UTC().Format(time.RFC3339)
	if strings.Contains(fm, "state_updated_at: ") {
		lines := strings.Split(fm, "\n")
		for i, line := range lines {
			if strings.HasPrefix(line, "state_updated_at: ") {
				lines[i] = "state_updated_at: " + now
				break
			}
		}
		fm = strings.Join(lines, "\n")
	} else {
		fm += "\nstate_updated_at: " + now
	}

	return content[:first+4] + fm + after
}

// EdgeActivity tracks communication timestamps for an edge (Issue #37).
type EdgeActivity struct {
	LastForwardAt  time.Time // A -> B last communication time
	LastBackwardAt time.Time // B -> A last communication time
}

// DaemonState manages daemon state (Issue #71).
type DaemonState struct {
	startedAt                     time.Time     // Daemon start timestamp (#217)
	drainWindow                   time.Duration // Startup drain window duration (#217)
	edgeHistory                   map[string]EdgeActivity
	edgeHistoryMu                 sync.RWMutex
	enabledSessions               map[string]bool
	enabledSessionsMu             sync.RWMutex
	prevPaneStates                map[string]uinode.PaneInfo // Issue #98: Track previous pane states for restart detection
	prevPaneStatesMu              sync.RWMutex               // Issue #98: Mutex for prevPaneStates
	prevPaneToNode                map[string]string          // Track previous pane ID -> node key mapping for restart detection
	lastAlertTimestamp            map[string]time.Time       // Issue #118: Track last alert timestamps (alertKey -> time)
	lastAlertTimestampMu          sync.RWMutex               // Issue #118: Mutex for lastAlertTimestamp
	lastInboxUnreadCount          map[string]int             // Issue #264: per-node last alerted inbox count
	lastInboxUnreadCountMu        sync.RWMutex               // Issue #264
	lastDeliveryBySenderRecipient map[string]time.Time       // Issue #211: Rate limit duplicate deliveries (sender:recipient -> time)
	lastDeliveryMu                sync.RWMutex               // Issue #211: Mutex for lastDeliveryBySenderRecipient
	alertedReadFiles              map[string]struct{}        // Paths of read/ files already alerted (suppress repeats)
	alertedReadFilesMu            sync.Mutex                 // Mutex for alertedReadFiles
}

// NewDaemonState creates a new DaemonState instance (Issue #71).
// drainWindowSeconds configures the startup drain window during which
// IsSessionEnabled returns true for all sessions (#217).
func NewDaemonState(drainWindowSeconds float64) *DaemonState {
	return &DaemonState{
		startedAt:                     time.Now(),
		drainWindow:                   time.Duration(drainWindowSeconds * float64(time.Second)),
		edgeHistory:                   make(map[string]EdgeActivity),
		enabledSessions:               make(map[string]bool),
		prevPaneStates:                make(map[string]uinode.PaneInfo), // Issue #98
		prevPaneToNode:                make(map[string]string),          // paneID -> nodeKey mapping
		lastAlertTimestamp:            make(map[string]time.Time),       // Issue #118
		lastDeliveryBySenderRecipient: make(map[string]time.Time),       // Issue #211
		lastInboxUnreadCount:          make(map[string]int),             // Issue #264
		alertedReadFiles:              make(map[string]struct{}),
	}
}

// makeEdgeKey generates a sorted edge key for consistent lookups (Issue #37).
func makeEdgeKey(nodeA, nodeB string) string {
	nodes := []string{nodeA, nodeB}
	sort.Strings(nodes)
	return nodes[0] + ":" + nodes[1]
}

// RecordEdgeActivity records edge communication activity (Issue #37, #71).
func (ds *DaemonState) RecordEdgeActivity(from, to string, timestamp time.Time) {
	ds.edgeHistoryMu.Lock()
	defer ds.edgeHistoryMu.Unlock()

	key := makeEdgeKey(from, to)
	activity := ds.edgeHistory[key]

	// Determine direction: sort nodes and check if from is first
	nodes := []string{from, to}
	sort.Strings(nodes)
	if from == nodes[0] {
		activity.LastForwardAt = timestamp
	} else {
		activity.LastBackwardAt = timestamp
	}

	ds.edgeHistory[key] = activity
}

// ClearEdgeHistory clears all edge activity history (called on session switch).
func (ds *DaemonState) ClearEdgeHistory() {
	ds.edgeHistoryMu.Lock()
	defer ds.edgeHistoryMu.Unlock()
	ds.edgeHistory = make(map[string]EdgeActivity)
}

// BuildEdgeList builds edge list with activity data (Issue #37, #42, #71).
func (ds *DaemonState) BuildEdgeList(edges []string, cfg *config.Config) []tui.Edge {
	ds.edgeHistoryMu.RLock()
	defer ds.edgeHistoryMu.RUnlock()

	now := time.Now()
	activityWindow := time.Duration(cfg.EdgeActivitySeconds * float64(time.Second))

	edgeList := make([]tui.Edge, len(edges))
	for i, e := range edges {
		// Issue #42: Parse chain edge into node segments
		nodes := tui.ParseEdgeNodes(e)

		// Calculate direction for each segment
		var segmentDirections []string
		var lastActivityAt time.Time
		isActive := false

		// Process each adjacent pair
		for j := 0; j < len(nodes)-1; j++ {
			nodeA := nodes[j]
			nodeB := nodes[j+1]

			key := makeEdgeKey(nodeA, nodeB)
			activity, exists := ds.edgeHistory[key]

			segmentDir := "none"
			if exists && nodeA != "" && nodeB != "" {
				// Check if each direction is active (within activity window)
				forwardActive := !activity.LastForwardAt.IsZero() && now.Sub(activity.LastForwardAt) <= activityWindow
				backwardActive := !activity.LastBackwardAt.IsZero() && now.Sub(activity.LastBackwardAt) <= activityWindow

				// Update global last activity time
				if activity.LastForwardAt.After(lastActivityAt) {
					lastActivityAt = activity.LastForwardAt
				}
				if activity.LastBackwardAt.After(lastActivityAt) {
					lastActivityAt = activity.LastBackwardAt
				}

				// Determine segment direction
				// NOTE: forward/backward in edgeHistory are based on sorted node order:
				//   forward = sorted[0] -> sorted[1]
				//   backward = sorted[1] -> sorted[0]
				// We need to map this to nodeA->nodeB direction based on edge definition order.
				sortedNodes := []string{nodeA, nodeB}
				sort.Strings(sortedNodes)

				var nodeAtoB, nodeBtoA bool
				if nodeA == sortedNodes[0] {
					// nodeA is sorted[0], nodeB is sorted[1]
					nodeAtoB = forwardActive  // sorted[0]->sorted[1] = nodeA->nodeB
					nodeBtoA = backwardActive // sorted[1]->sorted[0] = nodeB->nodeA
				} else {
					// nodeA is sorted[1], nodeB is sorted[0]
					nodeAtoB = backwardActive // sorted[1]->sorted[0] = nodeA->nodeB
					nodeBtoA = forwardActive  // sorted[0]->sorted[1] = nodeB->nodeA
				}

				switch {
				case nodeAtoB && nodeBtoA:
					segmentDir = "bidirectional"
					isActive = true
				case nodeAtoB:
					segmentDir = "forward"
					isActive = true
				case nodeBtoA:
					segmentDir = "backward"
					isActive = true
				}
			}

			segmentDirections = append(segmentDirections, segmentDir)
		}

		// For backward compatibility, set Direction to first segment direction
		direction := "none"
		if len(segmentDirections) > 0 {
			direction = segmentDirections[0]
		}

		edgeList[i] = tui.Edge{
			Raw:               e,
			LastActivityAt:    lastActivityAt,
			IsActive:          isActive,
			Direction:         direction,
			SegmentDirections: segmentDirections,
		}
	}

	return edgeList
}

// filterNodesByEdges removes nodes from the map whose raw name (after session prefix)
// is not listed in the configured edges. Modifies the map in place.
func filterNodesByEdges(nodes map[string]discovery.NodeInfo, edges []string) {
	allowed := config.GetEdgeNodeNames(edges)
	for nodeName := range nodes {
		parts := strings.SplitN(nodeName, ":", 2)
		rawName := parts[len(parts)-1]
		if !allowed[rawName] {
			delete(nodes, nodeName)
		}
	}
}

// RunDaemonLoop runs the daemon event loop in a goroutine (Issue #71).
func RunDaemonLoop(
	ctx context.Context,
	baseDir string,
	sessionDir string,
	contextID string,
	cfg *config.Config,
	watcher *fsnotify.Watcher,
	adjacency map[string][]string,
	nodes map[string]discovery.NodeInfo,
	knownNodes map[string]bool,
	reminderState *reminder.ReminderState,
	events chan<- tui.DaemonEvent,
	configPath string,
	nodesDir string,
	daemonState *DaemonState,
	idleTracker *idle.IdleTracker,
	alertRateLimiter *alert.AlertRateLimiter,
	sharedNodes *atomic.Pointer[map[string]discovery.NodeInfo],
) {
	// NOTE: Do not close(events) here. The channel is shared by multiple goroutines
	// (UI pane monitoring, TUI commands handler, daemon loop). Closing it would cause
	// "send on closed channel" panics. Let the channel be garbage collected when all
	// goroutines exit.

	// Debounce timers for different paths
	// Issue #45: Removed inboxTimer (inbox_update event removed)
	var configTimer *time.Timer

	// Track watched directories to avoid duplicates
	watchedDirs := make(map[string]bool)

	// Issue #94: Track previous pane states to avoid spam
	var prevPaneStatesJSON string

	// Issue #117: Track previous node and session counts to avoid spam
	prevNodeCount := 0
	prevSessionCount := 0

	// Issue #41: Periodic node discovery
	scanInterval := time.Duration(cfg.ScanInterval * float64(time.Second))
	scanTicker := time.NewTicker(scanInterval)
	defer scanTicker.Stop()

	// Issue #96: Periodic inbox stagnation check
	inboxCheckTicker := time.NewTicker(inboxCheckInterval)
	defer inboxCheckTicker.Stop()

	// Issue #136: Start heartbeat-LLM trigger goroutine if configured
	if config.BoolVal(cfg.Heartbeat.Enabled, false) && cfg.Heartbeat.LLMNode != "" && cfg.Heartbeat.IntervalSeconds > 0 {
		go startHeartbeatTrigger(ctx, sharedNodes, contextID, cfg, adjacency)
	}

	for {
		select {
		case <-ctx.Done():
			// Issue #57: Send channel_closed to trigger TUI exit
			events <- tui.DaemonEvent{
				Type:    "channel_closed",
				Message: "Shutting down",
			}
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			// Path-based routing
			eventPath := event.Name

			// Handle post/ directory events (any session)
			if strings.HasSuffix(filepath.Dir(eventPath), "post") {
				if event.Op&(fsnotify.Create|fsnotify.Rename) != 0 {
					filename := filepath.Base(eventPath)
					if strings.HasSuffix(filename, ".md") {
						// Re-discover nodes before each delivery (edge-filtered)
						if freshNodes, _, err := discovery.DiscoverNodesWithCollisions(baseDir, contextID); err == nil {
							filterNodesByEdges(freshNodes, cfg.Edges)
							// Claim discovered panes with this daemon's context ID.
							for _, nodeInfo := range freshNodes {
								claimCmd := exec.Command(
									"tmux", "set-option", "-p", "-t", nodeInfo.PaneID,
									"@a2a_context_id", contextID,
								)
								if err := claimCmd.Run(); err != nil {
									log.Printf(
										"postman: WARNING: failed to claim pane %s: %v\n",
										nodeInfo.PaneID, err,
									)
								}
							}
							// Detect new nodes
							for nodeName, nodeInfo := range freshNodes {
								if !knownNodes[nodeName] {
									knownNodes[nodeName] = true

									// Ensure session directories exist for new node
									if err := config.CreateSessionDirs(nodeInfo.SessionDir); err != nil {
										events <- tui.DaemonEvent{
											Type:    "error",
											Message: fmt.Sprintf("failed to create session dirs for %s: %v", nodeName, err),
										}
										continue
									}

									if err := config.GenerateBoilerplateFiles(nodeInfo.SessionDir, contextID, cfg); err != nil {
										log.Printf("postman: WARNING: failed to generate boilerplate files for %s: %v\n", nodeName, err)
									}

									// Add new node's directories to watch
									nodePostDir := filepath.Join(nodeInfo.SessionDir, "post")
									nodeInboxDir := filepath.Join(nodeInfo.SessionDir, "inbox")
									if !watchedDirs[nodePostDir] {
										if err := watcher.Add(nodePostDir); err == nil {
											watchedDirs[nodePostDir] = true
										}
									}
									if !watchedDirs[nodeInboxDir] {
										if err := watcher.Add(nodeInboxDir); err == nil {
											watchedDirs[nodeInboxDir] = true
										}
									}
									nodeReadDir := filepath.Join(nodeInfo.SessionDir, "read")
									if !watchedDirs[nodeReadDir] {
										if err := watcher.Add(nodeReadDir); err == nil {
											watchedDirs[nodeReadDir] = true
										}
									}
								}
							}
							nodes = freshNodes
							if sharedNodes != nil {
								nodesSnapshot := freshNodes
								sharedNodes.Store(&nodesSnapshot)
							}

							// Issue #117: Discover all tmux sessions
							allSessions, _ := discovery.DiscoverAllSessions()
							if allSessions == nil {
								allSessions = []string{}
							}

							// Issue #36: Bug 2 - Build session info from nodes
							sessionNodes := make(map[string][]string) // Issue #59: session -> simple node names
							for nodeName := range nodes {
								// Extract session name from "session:node" format
								parts := strings.SplitN(nodeName, ":", 2)
								if len(parts) == 2 {
									sessionName := parts[0]
									simpleNodeName := parts[1]
									sessionNodes[sessionName] = append(sessionNodes[sessionName], simpleNodeName)
								}
							}
							sessionList := session.BuildSessionList(nodes, allSessions, daemonState.GetConfiguredSessionEnabled)

							// Update node count and session info
							events <- tui.DaemonEvent{
								Type:    "status_update",
								Message: "Running",
								Details: map[string]interface{}{
									"node_count":    len(nodes),
									"sessions":      sessionList,  // Issue #36: Bug 2 - Send session info
									"session_nodes": sessionNodes, // Issue #59: Session-node mapping
								},
							}
						}
						// Issue #211: Rate limit duplicate deliveries
						if cfg.MinDeliveryGapSeconds > 0 {
							if msgInfo, parseErr := message.ParseMessageFilename(filename); parseErr == nil {
								deliveryKey := msgInfo.From + ":" + msgInfo.To
								gap := time.Duration(cfg.MinDeliveryGapSeconds * float64(time.Second))
								daemonState.lastDeliveryMu.RLock()
								lastTime, exists := daemonState.lastDeliveryBySenderRecipient[deliveryKey]
								daemonState.lastDeliveryMu.RUnlock()
								if exists && time.Since(lastTime) < gap {
									log.Printf("postman: rate-limited delivery %s -> %s (gap: %.1fs)\n", msgInfo.From, msgInfo.To, cfg.MinDeliveryGapSeconds)
									continue
								}
							}
						}

						// Use eventPath directly for multi-session support
						// Issue #53: Create wrapper channel for dead-letter notifications
						messageEvents := make(chan message.DaemonEvent, 1)
						if err := message.DeliverMessage(eventPath, contextID, nodes, adjacency, cfg, daemonState.IsSessionEnabled, messageEvents, idleTracker); err != nil {
							events <- tui.DaemonEvent{
								Type:    "error",
								Message: fmt.Sprintf("deliver %s: %v", filename, err),
							}
						} else {
							// Issue #53: Check if dead-letter event was sent
							deadLetterEventSent := false
							select {
							case msgEvent := <-messageEvents:
								events <- tui.DaemonEvent{
									Type:    msgEvent.Type,
									Message: msgEvent.Message,
									Details: msgEvent.Details,
								}
								deadLetterEventSent = true
							default:
								// No dead-letter event, normal delivery
							}

							// Issue #211: Record delivery timestamp for rate limiting
							if !deadLetterEventSent {
								if msgInfo, parseErr := message.ParseMessageFilename(filename); parseErr == nil {
									deliveryKey := msgInfo.From + ":" + msgInfo.To
									daemonState.lastDeliveryMu.Lock()
									daemonState.lastDeliveryBySenderRecipient[deliveryKey] = time.Now()
									daemonState.lastDeliveryMu.Unlock()

								}
							}

							// Send normal delivery event only if not dead-lettered
							if !deadLetterEventSent {
								// Remove waiting files for sender: successfully sent, no longer composing reply
								{
									senderSessionDir := filepath.Dir(filepath.Dir(eventPath))
									if senderInfo, parseErr := message.ParseMessageFilename(filename); parseErr == nil {
										waitingDir := filepath.Join(senderSessionDir, "waiting")
										pattern := filepath.Join(waitingDir, "*-to-"+senderInfo.From+".md")
										if matches, globErr := filepath.Glob(pattern); globErr == nil {
											for _, match := range matches {
												if removeErr := os.Remove(match); removeErr != nil {
													log.Printf("postman: WARNING: failed to remove waiting file %s: %v\n", match, removeErr)
												}
											}
										}
									}
								}
								// Issue #59: Extract session name from eventPath
								// eventPath format: /path/to/context-id/session-name/post/message.md
								sourceSessionDir := filepath.Dir(filepath.Dir(eventPath))
								sourceSessionName := filepath.Base(sourceSessionDir)
								events <- tui.DaemonEvent{
									Type:    "message_received",
									Message: fmt.Sprintf("Delivered: %s", filename),
									Details: map[string]interface{}{
										"session": sourceSessionName,
									},
								}
							}
							// Send observer digest on successful delivery (only for normal delivery)
							if !deadLetterEventSent {
								if info, err := message.ParseMessageFilename(filename); err == nil {
									// Normal message delivery - record edge activity, send digest, etc.
									// Issue #37: Record edge activity
									daemonState.RecordEdgeActivity(info.From, info.To, time.Now())

									// Issue #40: Send edge_update event to TUI
									edgeList := daemonState.BuildEdgeList(cfg.Edges, cfg)
									events <- tui.DaemonEvent{
										Type: "edge_update",
										Details: map[string]interface{}{
											"edges": edgeList,
										},
									}

									// Issue #55: Emit ball state update after message delivery
									nodeStates := idleTracker.GetNodeStates()
									events <- tui.DaemonEvent{
										Type: "ball_state_update",
										Details: map[string]interface{}{
											"node_states": nodeStates,
										},
									}
								}
							}
						}
					}
				}
			} else if strings.HasSuffix(filepath.Dir(eventPath), "read") {
				// Handle read/ directory events — confirm liveness from inbox->read/ move (Issue #150).
				if event.Op&(fsnotify.Create|fsnotify.Rename) != 0 {
					filename := filepath.Base(eventPath)
					if strings.HasSuffix(filename, ".md") {
						if info, err := message.ParseMessageFilename(filename); err == nil {
							// Skip files moved to read/ by daemon (To == "postman").
							// Skip daemon-originated files.
							if info.To != "postman" && info.To != "daemon" {
								// Liveness confirmed: node archived a message, proving it is alive.
								sourceSessionDir := filepath.Dir(filepath.Dir(eventPath))
								sourceSessionName := filepath.Base(sourceSessionDir)
								prefixedKey := sourceSessionName + ":" + info.To
								idleTracker.MarkNodeAlive(prefixedKey)
								events <- tui.DaemonEvent{
									Type: "node_alive",
									Details: map[string]interface{}{
										"node":   prefixedKey,
										"source": "read_move",
									},
								}
								// Count actual reads toward reminder threshold (#244).
								// Only count human-authored messages (not daemon/postman alerts).
								if reminderShouldIncrement(info.From) {
									reminderState.Increment(info.To, sourceSessionName, nodes, cfg)
								}
								// Create waiting file: only for agent-to-agent messages (not daemon alerts)
								if info.From != "postman" && info.From != "daemon" {
									waitingDir := filepath.Join(sourceSessionDir, "waiting")
									waitingFile := filepath.Join(waitingDir, filename)
									waitingSince := time.Now().UTC().Format(time.RFC3339)
									// Determine state: user_input for ui_node, composing for all others
									state := "composing"
									if cfg.UINode != "" && info.To == cfg.UINode {
										state = "user_input"
									}
									waitingContent := fmt.Sprintf(
										"---\nfrom: %s\nto: %s\nwaiting_since: %s\nstate: %s\nstate_updated_at: %s\n---\n",
										info.From, info.To, waitingSince, state, waitingSince)
									if writeErr := os.WriteFile(waitingFile, []byte(waitingContent), 0o600); writeErr != nil {
										log.Printf("postman: WARNING: failed to create waiting file %s: %v\n", waitingFile, writeErr)
									}
								}
							}
						}
					}
				}
			}

			// Issue #45: Removed inbox_update event (Messages view removed from TUI)

			// Handle config file events (with debounce)
			// Issue #50: Also handle events from nodes/ directory
			isConfigEvent := configPath != "" && eventPath == configPath
			isNodesDirEvent := nodesDir != "" && strings.HasPrefix(eventPath, nodesDir+string(filepath.Separator))
			if isConfigEvent || isNodesDirEvent {
				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
					// Debounce config updates (200ms)
					if configTimer != nil {
						configTimer.Stop()
					}
					configTimer = safeAfterFunc(200*time.Millisecond, "config-reload", events, func() {
						// Reload config
						newCfg, err := config.LoadConfig(configPath)
						if err != nil {
							events <- tui.DaemonEvent{
								Type:    "error",
								Message: fmt.Sprintf("config reload failed: %v", err),
							}
							return
						}
						// Update adjacency
						newAdjacency, err := config.ParseEdges(newCfg.Edges)
						if err != nil {
							events <- tui.DaemonEvent{
								Type:    "error",
								Message: fmt.Sprintf("edge parsing failed: %v", err),
							}
							return
						}
						// Update shared state
						cfg = newCfg
						adjacency = newAdjacency

						if err := config.GenerateBoilerplateFiles(sessionDir, contextID, newCfg); err != nil {
							log.Printf("postman: WARNING: failed to generate boilerplate files on reload: %v\n", err)
						}

						// Send config update event
						// Issue #37: Build edge list with activity data
						edgeList := daemonState.BuildEdgeList(newCfg.Edges, newCfg)

						// Issue #117: Discover all tmux sessions
						allSessions, _ := discovery.DiscoverAllSessions()
						if allSessions == nil {
							allSessions = []string{}
						}

						// Issue #35: Requirement 3 - build session info from nodes
						sessionNodes := make(map[string][]string) // Issue #59: session -> simple node names
						for nodeName := range nodes {
							// Extract session name from "session:node" format
							parts := strings.SplitN(nodeName, ":", 2)
							if len(parts) == 2 {
								sessionName := parts[0]
								simpleNodeName := parts[1]
								sessionNodes[sessionName] = append(sessionNodes[sessionName], simpleNodeName)
							}
						}
						sessionList := session.BuildSessionList(nodes, allSessions, daemonState.GetConfiguredSessionEnabled)

						events <- tui.DaemonEvent{
							Type: "config_update",
							Details: map[string]interface{}{
								"edges":         edgeList,
								"sessions":      sessionList,  // Issue #35: Requirement 3
								"session_nodes": sessionNodes, // Issue #59: Session-node mapping
							},
						}
						events <- tui.DaemonEvent{
							Type:    "message_received",
							Message: "Config reloaded",
						}
					})
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			events <- tui.DaemonEvent{
				Type:    "error",
				Message: fmt.Sprintf("watcher error: %v", err),
			}
		case <-scanTicker.C:
			// Issue #41: Periodic node discovery (edge-filtered)
			freshNodes, scanCollisions, err := discovery.DiscoverNodesWithCollisions(baseDir, contextID)
			if err != nil {
				continue
			}
			filterNodesByEdges(freshNodes, cfg.Edges)
			// Claim discovered panes with this daemon's context ID.
			for _, nodeInfo := range freshNodes {
				claimCmd := exec.Command(
					"tmux", "set-option", "-p", "-t", nodeInfo.PaneID,
					"@a2a_context_id", contextID,
				)
				if err := claimCmd.Run(); err != nil {
					log.Printf(
						"postman: WARNING: failed to claim pane %s: %v\n",
						nodeInfo.PaneID, err,
					)
				}
			}
			for _, collision := range scanCollisions {
				alertKey := "pane_collision:" + collision.WinnerPaneID + ":" + collision.LoserPaneID
				if daemonState.ShouldSendAlert(alertKey, 300) {
					events <- tui.DaemonEvent{Type: "pane_collision", Message: fmt.Sprintf(
						"[COLLISION] %s: %s displaced by %s", collision.NodeKey, collision.LoserPaneID, collision.WinnerPaneID,
					)}
					daemonState.MarkAlertSent(alertKey)
				}
			}

			// Detect new nodes
			for nodeName, nodeInfo := range freshNodes {
				if !knownNodes[nodeName] {
					knownNodes[nodeName] = true

					// Ensure session directories exist for new node
					if err := config.CreateSessionDirs(nodeInfo.SessionDir); err != nil {
						events <- tui.DaemonEvent{
							Type:    "error",
							Message: fmt.Sprintf("failed to create session dirs for %s: %v", nodeName, err),
						}
						continue
					}

					if err := config.GenerateBoilerplateFiles(nodeInfo.SessionDir, contextID, cfg); err != nil {
						log.Printf("postman: WARNING: failed to generate boilerplate files for %s: %v\n", nodeName, err)
					}

					// Add new node's directories to watch
					nodePostDir := filepath.Join(nodeInfo.SessionDir, "post")
					nodeInboxDir := filepath.Join(nodeInfo.SessionDir, "inbox")
					if !watchedDirs[nodePostDir] {
						if err := watcher.Add(nodePostDir); err == nil {
							watchedDirs[nodePostDir] = true
						}
					}
					if !watchedDirs[nodeInboxDir] {
						if err := watcher.Add(nodeInboxDir); err == nil {
							watchedDirs[nodeInboxDir] = true
						}
					}
					nodeReadDir := filepath.Join(nodeInfo.SessionDir, "read")
					if !watchedDirs[nodeReadDir] {
						if err := watcher.Add(nodeReadDir); err == nil {
							watchedDirs[nodeReadDir] = true
						}
					}
				}
			}

			// Issue #117: Always update nodes map (removes dead nodes)
			nodes = freshNodes
			if sharedNodes != nil {
				nodesSnapshot := freshNodes
				sharedNodes.Store(&nodesSnapshot)
			}

			// Issue #117: Discover all tmux sessions (not just A2A sessions)
			allSessions, err := discovery.DiscoverAllSessions()
			if err != nil {
				// Log error but continue with A2A sessions only
				events <- tui.DaemonEvent{
					Type:    "error",
					Message: fmt.Sprintf("failed to discover all sessions: %v", err),
				}
				allSessions = []string{}
			}

			// Build session info from nodes
			sessionNodes := make(map[string][]string) // Issue #59: session -> simple node names
			for nodeName := range nodes {
				parts := strings.SplitN(nodeName, ":", 2)
				if len(parts) == 2 {
					sessionName := parts[0]
					simpleNodeName := parts[1]
					sessionNodes[sessionName] = append(sessionNodes[sessionName], simpleNodeName)
				}
			}
			sessionList := session.BuildSessionList(nodes, allSessions, daemonState.GetConfiguredSessionEnabled)

			// Issue #117: Send event only on state change (avoid spam)
			if len(nodes) != prevNodeCount || len(sessionList) != prevSessionCount {
				events <- tui.DaemonEvent{
					Type:    "status_update",
					Message: "Running",
					Details: map[string]interface{}{
						"node_count":    len(nodes),
						"sessions":      sessionList,
						"session_nodes": sessionNodes, // Issue #59: Session-node mapping
					},
				}
				prevNodeCount = len(nodes)
				prevSessionCount = len(sessionList)
			}

			// Issue #56: Check dropped balls
			// Build simple-name -> NodeConfig mapping
			nodeConfigs := make(map[string]config.NodeConfig)
			for fullName := range nodes {
				parts := strings.SplitN(fullName, ":", 2)
				if len(parts) == 2 {
					simpleName := parts[1]
					if nc, ok := cfg.Nodes[simpleName]; ok {
						nodeConfigs[simpleName] = nc
					}
				}
			}

			droppedNodes := idleTracker.CheckDroppedBalls(nodeConfigs)
			for nodeKey, duration := range droppedNodes {
				idleTracker.MarkDroppedBallNotified(nodeKey)

				// Extract simple name for nodeConfigs lookup
				simpleName := nodeKey
				if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
					simpleName = parts[1]
				}

				// Issue #82: Use configurable template for dropped ball events
				eventTemplate := cfg.DroppedBallEventTemplate
				if eventTemplate == "" {
					eventTemplate = "Dropped ball: {node} (holding for {duration})"
				}
				timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
				vars := map[string]string{
					"node":     nodeKey,
					"duration": duration.Round(time.Second).String(),
				}
				eventMessage := template.ExpandTemplate(eventTemplate, vars, timeout)

				// Emit dropped_ball event for Events pane
				events <- tui.DaemonEvent{
					Type:    "dropped_ball",
					Message: eventMessage,
					Details: map[string]interface{}{
						"node":     nodeKey,
						"duration": duration.Seconds(),
					},
				}

				// Optional: tmux display-message
				nc := nodeConfigs[simpleName]
				notification := nc.DroppedBallNotification
				if notification == "" {
					notification = "tui"
				}
				if notification == "display" || notification == "all" {
					_ = exec.Command("tmux", "display-message", eventMessage).Run()
				}
			}

			// Issue #94: Monitor all panes with high frequency
			paneStates, err := uinode.GetAllPanesInfo()
			if err == nil {
				// IMPORTANT FIX: Only send event if pane states changed
				currentJSON, _ := json.Marshal(paneStates)
				currentJSONStr := string(currentJSON)

				if currentJSONStr != prevPaneStatesJSON {
					// Build paneID -> nodeKey mapping for TUI
					paneToNode := make(map[string]string)
					for nodeKey, nodeInfo := range nodes {
						paneToNode[nodeInfo.PaneID] = nodeKey
					}

					// Send pane_state_update event to TUI
					events <- tui.DaemonEvent{
						Type:    "pane_state_update",
						Message: "Pane states updated",
						Details: map[string]interface{}{
							"pane_states":  paneStates,
							"pane_to_node": paneToNode,
						},
					}

					// Check for pane disappearance (killed panes) - MUST run before checkPaneRestarts
					// because checkPaneRestarts updates prevPaneStates
					daemonState.checkPaneDisappearance(paneStates, daemonState.prevPaneToNode, nodes, events)

					// Issue #98: Check for pane restarts (updates prevPaneStates at end)
					daemonState.checkPaneRestarts(paneStates, paneToNode, nodes, cfg, events, contextID, sessionDir, adjacency, idleTracker)

					// Update previous state
					prevPaneStatesJSON = currentJSONStr
				}
			}

			// Get currently dropped nodes for TUI display (no cooldown check)
			droppedNodeMap := idleTracker.GetCurrentlyDroppedNodes(nodeConfigs)

			// Send ball_state_update event with dropped_nodes
			nodeStates := idleTracker.GetNodeStates()
			events <- tui.DaemonEvent{
				Type:    "ball_state_update",
				Message: "Ball states updated",
				Details: map[string]interface{}{
					"node_states":   nodeStates,
					"dropped_nodes": droppedNodeMap,
				},
			}
		case <-inboxCheckTicker.C:
			// Issue #245: Check inbox unread count and alert UINode
			checkInboxStagnation(nodes, cfg, events, sessionDir, contextID, adjacency, idleTracker, alertRateLimiter, daemonState)
			checkNodeInactivity(nodes, cfg, events, sessionDir, contextID, adjacency, idleTracker, alertRateLimiter)
			checkUnrepliedMessages(nodes, cfg, events, sessionDir, contextID, adjacency, idleTracker, alertRateLimiter, daemonState)
			// Issue #283: Emit live inbox counts for TUI display (replaces cumulative reminder counts).
			events <- tui.DaemonEvent{
				Type: "read_count_update",
				Details: map[string]interface{}{
					"counts": scanLiveInboxCounts(nodes),
				},
			}

			// Update waiting file states based on current pane activity
			paneStatus := idleTracker.GetPaneActivityStatus(cfg)
			idleThreshold := time.Duration(cfg.NodeIdleSeconds * float64(time.Second))
			spinningEnabled := cfg.NodeSpinningSeconds > 0
			spinningThreshold := time.Duration(cfg.NodeSpinningSeconds * float64(time.Second))
			for _, nodeInfo := range nodes {
				waitingDir := filepath.Join(nodeInfo.SessionDir, "waiting")
				entries, err := os.ReadDir(waitingDir)
				if err != nil {
					continue
				}
				for _, entry := range entries {
					if !strings.HasSuffix(entry.Name(), ".md") {
						continue
					}
					filePath := filepath.Join(waitingDir, entry.Name())
					fileContent, readErr := os.ReadFile(filePath)
					if readErr != nil {
						continue
					}
					contentStr := string(fileContent)
					isComposing := strings.Contains(contentStr, "state: composing")
					isSpinning := strings.Contains(contentStr, "state: spinning")
					if !isComposing && !isSpinning {
						continue // skip user_input, stuck, or unreadable
					}
					// Parse waiting_since from file content (anchor for composing window)
					var waitingSince time.Time
					for _, line := range strings.Split(contentStr, "\n") {
						if strings.HasPrefix(line, "waiting_since: ") {
							ts := strings.TrimPrefix(line, "waiting_since: ")
							if t, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(ts)); parseErr == nil {
								waitingSince = t
							}
							break
						}
					}
					if waitingSince.IsZero() {
						continue // malformed file; skip
					}
					// Parse recipient from filename
					fileInfo, parseErr := message.ParseMessageFilename(entry.Name())
					if parseErr != nil {
						continue
					}
					// Look up recipient's pane ID via sessionName:nodeName key
					recipientKey := nodeInfo.SessionName + ":" + fileInfo.To
					recipientInfo, ok := nodes[recipientKey]
					if !ok {
						continue // recipient not in current node snapshot
					}
					paneState := paneStatus[recipientInfo.PaneID]

					if isSpinning {
						// spinning → stuck: pane went stale after spinning was detected
						if paneState == "stale" {
							updated := replaceWaitingState(contentStr, "spinning", "stuck")
							_ = os.WriteFile(filePath, []byte(updated), 0o600)
						}
						continue
					}

					// isComposing: composing window guard (same as before)
					if time.Since(waitingSince) <= idleThreshold {
						continue
					}
					// composing → stuck: pane stale after composing window
					if paneState == "stale" {
						updated := replaceWaitingState(contentStr, "composing", "stuck")
						_ = os.WriteFile(filePath, []byte(updated), 0o600)
						continue
					}
					// composing → spinning: pane active after spinning threshold
					if spinningEnabled && time.Since(waitingSince) > spinningThreshold && paneState == "active" {
						updated := replaceWaitingState(contentStr, "composing", "spinning")
						_ = os.WriteFile(filePath, []byte(updated), 0o600)
					}
				}
			}
			// Collect waiting states for TUI color display (second pass, post-transition)
			waitingStates := make(map[string]string)
			worstStatePriority := map[string]int{
				"user_input": 0,
				"composing":  1,
				"spinning":   3,
				"stuck":      4,
			}
			for _, nodeInfo := range nodes {
				wDir := filepath.Join(nodeInfo.SessionDir, "waiting")
				wEntries, wErr := os.ReadDir(wDir)
				if wErr != nil {
					continue
				}
				for _, wEntry := range wEntries {
					if !strings.HasSuffix(wEntry.Name(), ".md") {
						continue
					}
					wContent, wReadErr := os.ReadFile(filepath.Join(wDir, wEntry.Name()))
					if wReadErr != nil {
						continue
					}
					cs := string(wContent)
					var fileState string
					switch {
					case strings.Contains(cs, "state: stuck"):
						fileState = "stuck"
					case strings.Contains(cs, "state: spinning"):
						fileState = "spinning"
					case strings.Contains(cs, "state: composing"):
						fileState = "composing"
					case strings.Contains(cs, "state: user_input"):
						fileState = "user_input"
					default:
						continue
					}
					wFileInfo, wParseErr := message.ParseMessageFilename(wEntry.Name())
					if wParseErr != nil {
						continue
					}
					recipientKey := nodeInfo.SessionName + ":" + wFileInfo.To
					if worstStatePriority[fileState] >= worstStatePriority[waitingStates[recipientKey]] {
						waitingStates[recipientKey] = fileState
					}
				}
			}
			events <- tui.DaemonEvent{
				Type:    "waiting_state_update",
				Message: "Waiting states updated",
				Details: map[string]interface{}{
					"waiting_states": waitingStates,
				},
			}
		}
	}
}

// sendAlertToUINode sends an alert message to the ui_node inbox.
// Writes directly to post/ so the daemon delivery loop routes and notifies normally.
// When AlertMessageTemplate is configured, uses two-pass expansion (BuildEnvelope + Pass 2).
func sendAlertToUINode(sessionDir, contextID, uiNode, body, alertType string, cfg *config.Config, adjacency map[string][]string, nodes map[string]discovery.NodeInfo) error {
	tmpl := cfg.AlertMessageTemplate
	if tmpl == "" {
		return nil // no template configured; silent no-op
	}
	sourceSessionName := filepath.Base(filepath.Dir(sessionDir))
	now := time.Now()
	ts := fmt.Sprintf("%s-%d", now.Format("20060102-150405"), now.UnixNano()%1000000)
	filename := fmt.Sprintf("%s-from-daemon-to-%s.md", ts, uiNode)
	postPath := filepath.Join(sessionDir, "post", filename)
	taskID := ts + "-alert"

	scaffolded := envelope.BuildEnvelope(
		cfg, tmpl, uiNode, "daemon",
		contextID, taskID, postPath,
		nil, adjacency, nodes, sourceSessionName,
		nil,
	)
	content := template.ExpandVariables(scaffolded, map[string]string{
		"alert_type":   alertType,
		"message":      body,
		"role_content": envelope.BuildRoleContent(cfg, uiNode),
	})
	return os.WriteFile(postPath, []byte(content), 0o600)
}

// checkInboxStagnation checks inbox unread count for all nodes and sends an alert to
// cfg.UINode when the count reaches cfg.InboxUnreadThreshold.
// Only the inbox_unread_summary count-based path is restored here (design doc #245).
// Three guards are enforced:
//   - Guard 1: alertRateLimiter.Allow — per-recipient cooldown
//   - Guard 2: idleTracker.GetLastReceived — suppress if UINode received recently
//   - Guard 3: count-based signal (distinct from stagnation / node_inactivity)
func checkInboxStagnation(nodes map[string]discovery.NodeInfo, cfg *config.Config, events chan<- tui.DaemonEvent, sessionDir, contextID string, adjacency map[string][]string, idleTracker *idle.IdleTracker, alertRateLimiter *alert.AlertRateLimiter, ds *DaemonState) {
	if cfg.UINode == "" || cfg.InboxUnreadThreshold <= 0 {
		return
	}

	now := time.Now()

	for nodeKey, nodeInfo := range nodes {
		simpleName := nodeKey
		if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
			simpleName = parts[1]
		}

		// Do not alert about UINode's own inbox here
		if simpleName == cfg.UINode {
			continue
		}

		inboxPath := filepath.Join(nodeInfo.SessionDir, "inbox", simpleName)
		entries, err := os.ReadDir(inboxPath)
		if err != nil {
			continue
		}

		inboxCount := 0
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
				inboxCount++
			}
		}
		if inboxCount < cfg.InboxUnreadThreshold {
			ds.lastInboxUnreadCountMu.Lock()
			delete(ds.lastInboxUnreadCount, simpleName)
			ds.lastInboxUnreadCountMu.Unlock()
			continue
		}
		ds.lastInboxUnreadCountMu.RLock()
		lastCount := ds.lastInboxUnreadCount[simpleName]
		ds.lastInboxUnreadCountMu.RUnlock()
		if inboxCount <= lastCount {
			continue
		}
		ds.lastInboxUnreadCountMu.Lock()
		ds.lastInboxUnreadCount[simpleName] = inboxCount
		ds.lastInboxUnreadCountMu.Unlock()

		// Send TUI event unconditionally (no rate limit for TUI display)
		alertVars := map[string]string{
			"node":      simpleName,
			"count":     fmt.Sprintf("%d", inboxCount),
			"threshold": fmt.Sprintf("%d", cfg.InboxUnreadThreshold),
		}
		msg := template.ExpandVariables(cfg.InboxUnreadSummaryAlertTemplate, alertVars)
		events <- tui.DaemonEvent{
			Type:    "inbox_unread_summary",
			Message: msg,
			Details: map[string]interface{}{
				"node":      simpleName,
				"count":     inboxCount,
				"threshold": cfg.InboxUnreadThreshold,
			},
		}

		// Guard 1: per-recipient cooldown
		if !alertRateLimiter.Allow(cfg.UINode, now) {
			continue
		}

		// Guard 2: suppress if UINode received a message recently
		// Use session-prefixed key matching UpdateReceiveActivity convention
		uiNodeFullKey := nodeInfo.SessionName + ":" + cfg.UINode
		deliveryWindow := time.Duration(cfg.AlertDeliveryWindowSeconds) * time.Second
		if deliveryWindow > 0 && time.Since(idleTracker.GetLastReceived(uiNodeFullKey)) < deliveryWindow {
			continue
		}

		// Build action text
		var replyCmd string
		if cfg.ReplyCommand != "" {
			replyCmd = strings.ReplaceAll(cfg.ReplyCommand, "{context_id}", contextID)
			replyCmd = strings.ReplaceAll(replyCmd, "<recipient>", simpleName)
		} else {
			replyCmd = fmt.Sprintf(
				"nix run github:i9wa4/tmux-a2a-postman -- create-draft --context-id %s --to %s",
				contextID, simpleName,
			)
		}
		canReach := false
		for _, neighbor := range adjacency[cfg.UINode] {
			if neighbor == simpleName {
				canReach = true
				break
			}
		}
		actionVars := map[string]string{
			"node":          simpleName,
			"reply_command": replyCmd,
		}
		var actionText string
		if canReach && cfg.AlertActionReachableTemplate != "" {
			actionText = template.ExpandVariables(cfg.AlertActionReachableTemplate, actionVars)
		} else if !canReach && cfg.AlertActionUnreachableTemplate != "" {
			actionText = template.ExpandVariables(cfg.AlertActionUnreachableTemplate, actionVars)
		}

		if err := sendAlertToUINode(sessionDir, contextID, cfg.UINode, msg+actionText, "inbox_unread_summary", cfg, adjacency, nodes); err == nil {
			alertRateLimiter.Record(cfg.UINode, now)
		}
	}
}

// scanLiveInboxCounts returns the current .md file count per node from the
// inbox filesystem, keyed by session-prefixed node key (e.g. "session:worker").
// Used to update the TUI readCounts display with live data (Issue #283).
func scanLiveInboxCounts(nodes map[string]discovery.NodeInfo) map[string]int {
	counts := make(map[string]int, len(nodes))
	for nodeKey, nodeInfo := range nodes {
		simpleName := nodeKey
		if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
			simpleName = parts[1]
		}
		inboxPath := filepath.Join(nodeInfo.SessionDir, "inbox", simpleName)
		entries, err := os.ReadDir(inboxPath)
		if err != nil {
			counts[nodeKey] = 0
			continue
		}
		n := 0
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
				n++
			}
		}
		counts[nodeKey] = n
	}
	return counts
}

// checkNodeInactivity alerts UINode when a monitored node has been inactive
// (no send + no receive) for longer than its configured IdleTimeoutSeconds.
// Three guards: TUI event (unconditional), Guard 1 (rate limiter), Guard 2 (delivery window).
// Guard 3 (signal): excludes nodes with state:user_input waiting files.
func checkNodeInactivity(nodes map[string]discovery.NodeInfo, cfg *config.Config, events chan<- tui.DaemonEvent, sessionDir, contextID string, adjacency map[string][]string, idleTracker *idle.IdleTracker, alertRateLimiter *alert.AlertRateLimiter) {
	if cfg.UINode == "" || cfg.NodeInactivityAlertTemplate == "" {
		return
	}

	now := time.Now()

	for nodeKey, nodeInfo := range nodes {
		simpleName := nodeKey
		if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
			simpleName = parts[1]
		}

		if simpleName == cfg.UINode {
			continue
		}

		nodeConfig, ok := cfg.Nodes[simpleName]
		if !ok || nodeConfig.IdleTimeoutSeconds <= 0 {
			continue
		}

		// Exclude nodes with state:user_input waiting files
		waitingDir := filepath.Join(nodeInfo.SessionDir, "waiting")
		waitingEntries, err := os.ReadDir(waitingDir)
		if err == nil {
			userInputFound := false
			for _, entry := range waitingEntries {
				if !strings.HasSuffix(entry.Name(), ".md") {
					continue
				}
				filePath := filepath.Join(waitingDir, entry.Name())
				fileContent, readErr := os.ReadFile(filePath)
				if readErr != nil {
					continue
				}
				contentStr := string(fileContent)
				if strings.Contains(contentStr, "state: user_input") {
					if fi, fiErr := message.ParseMessageFilename(entry.Name()); fiErr == nil && fi.From == simpleName {
						userInputFound = true
						break
					}
				}
			}
			if userInputFound {
				continue
			}
		}

		nodeStates := idleTracker.GetNodeStates()
		activity, actOk := nodeStates[nodeKey]
		if !actOk {
			continue
		}
		lastAct := activity.LastSent
		if activity.LastReceived.After(lastAct) {
			lastAct = activity.LastReceived
		}
		if lastAct.IsZero() {
			continue
		}
		idleDuration := time.Since(lastAct)
		threshold := time.Duration(nodeConfig.IdleTimeoutSeconds * float64(time.Second))
		if idleDuration < threshold {
			continue
		}

		alertVars := map[string]string{
			"node":     simpleName,
			"duration": idleDuration.Round(time.Second).String(),
		}
		msg := template.ExpandVariables(cfg.NodeInactivityAlertTemplate, alertVars)
		events <- tui.DaemonEvent{
			Type:    "node_inactivity",
			Message: msg,
			Details: map[string]interface{}{
				"node":     simpleName,
				"duration": idleDuration.String(),
			},
		}

		// Guard 1: per-recipient cooldown
		if !alertRateLimiter.Allow(cfg.UINode, now) {
			continue
		}

		// Guard 2: suppress if UINode received a message recently
		uiNodeFullKey := nodeInfo.SessionName + ":" + cfg.UINode
		deliveryWindow := time.Duration(cfg.AlertDeliveryWindowSeconds) * time.Second
		if deliveryWindow > 0 && time.Since(idleTracker.GetLastReceived(uiNodeFullKey)) < deliveryWindow {
			continue
		}

		var replyCmd string
		if cfg.ReplyCommand != "" {
			replyCmd = strings.ReplaceAll(cfg.ReplyCommand, "{context_id}", contextID)
			replyCmd = strings.ReplaceAll(replyCmd, "<recipient>", simpleName)
		} else {
			replyCmd = fmt.Sprintf(
				"nix run github:i9wa4/tmux-a2a-postman -- create-draft --context-id %s --to %s",
				contextID, simpleName,
			)
		}
		canReach := false
		for _, neighbor := range adjacency[cfg.UINode] {
			if neighbor == simpleName {
				canReach = true
				break
			}
		}
		actionVars := map[string]string{
			"node":          simpleName,
			"reply_command": replyCmd,
		}
		var actionText string
		if canReach && cfg.AlertActionReachableTemplate != "" {
			actionText = template.ExpandVariables(cfg.AlertActionReachableTemplate, actionVars)
		} else if !canReach && cfg.AlertActionUnreachableTemplate != "" {
			actionText = template.ExpandVariables(cfg.AlertActionUnreachableTemplate, actionVars)
		}

		if err := sendAlertToUINode(sessionDir, contextID, cfg.UINode, msg+actionText, "node_inactivity", cfg, adjacency, nodes); err == nil {
			alertRateLimiter.Record(cfg.UINode, now)
		}
	}
}

// checkUnrepliedMessages alerts UINode when a monitored node has messages in
// read/ that are older than DroppedBallTimeoutSeconds without a reply.
// Excludes daemon-generated messages (From == "postman" in filename).
// Three guards: TUI event (unconditional), Guard 1 (rate limiter), Guard 2 (delivery window).
func checkUnrepliedMessages(nodes map[string]discovery.NodeInfo, cfg *config.Config, events chan<- tui.DaemonEvent, sessionDir, contextID string, adjacency map[string][]string, idleTracker *idle.IdleTracker, alertRateLimiter *alert.AlertRateLimiter, daemonState *DaemonState) {
	if cfg.UINode == "" || cfg.UnrepliedMessageAlertTemplate == "" {
		return
	}

	now := time.Now()

	for nodeKey, nodeInfo := range nodes {
		simpleName := nodeKey
		if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
			simpleName = parts[1]
		}

		if simpleName == cfg.UINode {
			continue
		}

		nodeConfig, ok := cfg.Nodes[simpleName]
		if !ok || nodeConfig.DroppedBallTimeoutSeconds <= 0 {
			continue
		}

		readDir := filepath.Join(nodeInfo.SessionDir, "read")
		entries, err := os.ReadDir(readDir)
		if err != nil {
			continue
		}

		unrepliedCount := 0
		var (
			oldestFrom          string
			oldestTimeSinceRead time.Duration
			newAlertPaths       []string
		)
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			fileInfo, parseErr := message.ParseMessageFilename(entry.Name())
			if parseErr != nil {
				continue
			}
			if fileInfo.From == "postman" {
				continue
			}
			if fileInfo.To != simpleName {
				continue
			}
			entryInfo, infoErr := entry.Info()
			if infoErr != nil {
				continue
			}
			absPath := filepath.Join(readDir, entry.Name())
			daemonState.alertedReadFilesMu.Lock()
			_, alreadyAlerted := daemonState.alertedReadFiles[absPath]
			daemonState.alertedReadFilesMu.Unlock()
			if alreadyAlerted {
				continue
			}
			if time.Since(entryInfo.ModTime()) >= time.Duration(nodeConfig.DroppedBallTimeoutSeconds)*time.Second {
				unrepliedCount++
				age := time.Since(entryInfo.ModTime())
				if unrepliedCount == 1 || age > oldestTimeSinceRead {
					oldestTimeSinceRead = age
					oldestFrom = fileInfo.From
				}
				newAlertPaths = append(newAlertPaths, absPath)
			}
		}
		if unrepliedCount == 0 {
			continue
		}

		alertVars := map[string]string{
			"node":            simpleName,
			"count":           fmt.Sprintf("%d", unrepliedCount),
			"time_since_read": oldestTimeSinceRead.Round(time.Second).String(),
			"from":            oldestFrom,
			"threshold":       fmt.Sprintf("%d", nodeConfig.DroppedBallTimeoutSeconds),
		}
		msg := template.ExpandVariables(cfg.UnrepliedMessageAlertTemplate, alertVars)
		events <- tui.DaemonEvent{
			Type:    "unreplied_message",
			Message: msg,
			Details: map[string]interface{}{
				"node":  simpleName,
				"count": unrepliedCount,
			},
		}
		// Record newly alerted files to suppress future repeats.
		daemonState.alertedReadFilesMu.Lock()
		for _, p := range newAlertPaths {
			daemonState.alertedReadFiles[p] = struct{}{}
		}
		daemonState.alertedReadFilesMu.Unlock()

		// Guard 1: per-recipient cooldown
		if !alertRateLimiter.Allow(cfg.UINode, now) {
			continue
		}

		// Guard 2: suppress if UINode received a message recently
		uiNodeFullKey := nodeInfo.SessionName + ":" + cfg.UINode
		deliveryWindow := time.Duration(cfg.AlertDeliveryWindowSeconds) * time.Second
		if deliveryWindow > 0 && time.Since(idleTracker.GetLastReceived(uiNodeFullKey)) < deliveryWindow {
			continue
		}

		var replyCmd string
		if cfg.ReplyCommand != "" {
			replyCmd = strings.ReplaceAll(cfg.ReplyCommand, "{context_id}", contextID)
			replyCmd = strings.ReplaceAll(replyCmd, "<recipient>", simpleName)
		} else {
			replyCmd = fmt.Sprintf(
				"nix run github:i9wa4/tmux-a2a-postman -- create-draft --context-id %s --to %s",
				contextID, simpleName,
			)
		}
		canReach := false
		for _, neighbor := range adjacency[cfg.UINode] {
			if neighbor == simpleName {
				canReach = true
				break
			}
		}
		actionVars := map[string]string{
			"node":          simpleName,
			"reply_command": replyCmd,
		}
		var actionText string
		if canReach && cfg.AlertActionReachableTemplate != "" {
			actionText = template.ExpandVariables(cfg.AlertActionReachableTemplate, actionVars)
		} else if !canReach && cfg.AlertActionUnreachableTemplate != "" {
			actionText = template.ExpandVariables(cfg.AlertActionUnreachableTemplate, actionVars)
		}

		if err := sendAlertToUINode(sessionDir, contextID, cfg.UINode, msg+actionText, "unreplied_message", cfg, adjacency, nodes); err == nil {
			alertRateLimiter.Record(cfg.UINode, now)
		}
	}
}

// SetSessionEnabled sets the enabled/disabled state for a session (Issue #71).
func (ds *DaemonState) SetSessionEnabled(sessionName string, enabled bool) {
	ds.enabledSessionsMu.Lock()
	defer ds.enabledSessionsMu.Unlock()
	ds.enabledSessions[sessionName] = enabled
}

// AutoEnableSessionIfNew enables a session if it has never been configured (Issue #91).
// Called on first discovery of a new pane to allow auto-PING without TUI intervention.
// Does nothing if the session is already tracked (operator's explicit state is preserved).
func (ds *DaemonState) AutoEnableSessionIfNew(sessionName string) {
	ds.enabledSessionsMu.Lock()
	defer ds.enabledSessionsMu.Unlock()
	if _, exists := ds.enabledSessions[sessionName]; !exists {
		ds.enabledSessions[sessionName] = true
	}
}

// IsSessionEnabled checks if a session is enabled (Issue #71).
// During the startup drain window, returns true for all sessions to prevent
// the race where messages are rejected before sessions are registered (#217).
func (ds *DaemonState) IsSessionEnabled(sessionName string) bool {
	if ds.drainWindow > 0 && time.Since(ds.startedAt) < ds.drainWindow {
		return true
	}
	ds.enabledSessionsMu.RLock()
	defer ds.enabledSessionsMu.RUnlock()
	enabled, exists := ds.enabledSessions[sessionName]
	if !exists {
		return false // Default: disabled
	}
	return enabled
}

// GetConfiguredSessionEnabled returns the explicitly configured session state,
// ignoring the startup drain window. Use for TUI display only.
func (ds *DaemonState) GetConfiguredSessionEnabled(sessionName string) bool {
	ds.enabledSessionsMu.RLock()
	defer ds.enabledSessionsMu.RUnlock()
	enabled, exists := ds.enabledSessions[sessionName]
	if !exists {
		return false // Default: disabled
	}
	return enabled
}

// ShouldSendAlert checks if enough time has passed since the last alert (Issue #118).
// Returns true if the alert should be sent (cooldown expired or first time).
func (ds *DaemonState) ShouldSendAlert(alertKey string, cooldownSeconds float64) bool {
	ds.lastAlertTimestampMu.Lock()
	defer ds.lastAlertTimestampMu.Unlock()

	if cooldownSeconds <= 0 {
		return true
	}

	lastSent, exists := ds.lastAlertTimestamp[alertKey]
	if !exists {
		return true
	}

	return time.Since(lastSent) > time.Duration(cooldownSeconds*float64(time.Second))
}

// MarkAlertSent records the current time as the last alert sent time (Issue #118).
func (ds *DaemonState) MarkAlertSent(alertKey string) {
	ds.lastAlertTimestampMu.Lock()
	defer ds.lastAlertTimestampMu.Unlock()
	ds.lastAlertTimestamp[alertKey] = time.Now()
}

// reminderShouldIncrement returns true if the message sender should trigger the reminder counter.
// Daemon-generated messages (from="postman" or from="daemon") are excluded.
func reminderShouldIncrement(from string) bool {
	return from != "postman" && from != "daemon"
}

// startHeartbeatTrigger periodically sends heartbeat triggers to the configured LLM node.
// Goroutine lifecycle: exits cleanly on ctx.Done() (consistent with daemon.go:275 pattern).
func startHeartbeatTrigger(ctx context.Context, sharedNodes *atomic.Pointer[map[string]discovery.NodeInfo], contextID string, cfg *config.Config, adjacency map[string][]string) {
	interval := time.Duration(cfg.Heartbeat.IntervalSeconds * float64(time.Second))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := heartbeat.SendHeartbeatTrigger(sharedNodes, contextID, cfg.Heartbeat.LLMNode, cfg.Heartbeat.Prompt, cfg.Heartbeat.IntervalSeconds, cfg, adjacency); err != nil {
				log.Printf("heartbeat: trigger error: %v", err)
			}
		}
	}
}

// checkPaneRestarts detects pane restarts and sends PING (Issue #98).
// Detects restart by comparing current paneStates with previous paneStates.
// Issue #118: Added sessionDir for alert messaging.
func (ds *DaemonState) checkPaneRestarts(paneStates map[string]uinode.PaneInfo, paneToNode map[string]string, nodes map[string]discovery.NodeInfo, cfg *config.Config, events chan<- tui.DaemonEvent, contextID, sessionDir string, adjacency map[string][]string, idleTracker *idle.IdleTracker) {
	ds.prevPaneStatesMu.Lock()
	defer ds.prevPaneStatesMu.Unlock()

	for currentPaneID, currentInfo := range paneStates {
		nodeKey, exists := paneToNode[currentPaneID]
		if !exists {
			continue // No node mapped to this pane
		}

		_, nodeExists := nodes[nodeKey]
		if !nodeExists {
			continue // Node not found
		}

		// Check if this pane existed before
		_, prevExists := ds.prevPaneStates[currentPaneID]

		if prevExists {
			// Pane existed before - no restart detected
			continue
		}

		// New pane detected - check if this is a restart
		// Restart criteria: A node that previously had a different paneID now has a new paneID
		// Search for previous pane with the same node
		var oldPaneID string
		for oldID := range ds.prevPaneStates {
			if oldNodeKey, found := ds.prevPaneToNode[oldID]; found && oldNodeKey == nodeKey {
				// Found old pane for the same node
				oldPaneID = oldID
				break
			}
		}

		if oldPaneID != "" {
			// Restart detected: node had oldPaneID, now has currentPaneID
			log.Printf("postman: pane restart detected for %s (old: %s, new: %s)\n", nodeKey, oldPaneID, currentPaneID)

			// Send TUI event
			events <- tui.DaemonEvent{
				Type:    "pane_restart",
				Message: fmt.Sprintf("Pane restart detected: %s (old: %s, new: %s)", nodeKey, oldPaneID, currentPaneID),
				Details: map[string]interface{}{
					"node":        nodeKey,
					"old_pane_id": oldPaneID,
					"new_pane_id": currentPaneID,
					"pane_info":   currentInfo,
				},
			}

			// Issue #213: Requeue waiting/ files for restarted node back to inbox/
			if nodeInfo, found := nodes[nodeKey]; found {
				simpleName := nodeKey
				if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
					simpleName = parts[1]
				}
				waitingDir := filepath.Join(nodeInfo.SessionDir, "waiting")
				nodeInbox := filepath.Join(nodeInfo.SessionDir, "inbox", simpleName)
				if entries, readErr := os.ReadDir(waitingDir); readErr == nil {
					requeueCount := 0
					for _, e := range entries {
						if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
							continue
						}
						if !strings.Contains(e.Name(), "-to-"+simpleName) {
							continue
						}
						src := filepath.Join(waitingDir, e.Name())
						dst := filepath.Join(nodeInbox, e.Name())
						if mkErr := os.MkdirAll(nodeInbox, 0o700); mkErr != nil {
							continue
						}
						if mvErr := os.Rename(src, dst); mvErr == nil {
							requeueCount++
						}
					}
					if requeueCount > 0 {
						log.Printf("postman: pane restart requeued %d waiting/ files for %s\n", requeueCount, nodeKey)
					}
				}
			}
		}
	}

	// Update prevPaneStates
	ds.prevPaneStates = make(map[string]uinode.PaneInfo)
	for paneID, info := range paneStates {
		ds.prevPaneStates[paneID] = info
	}

	// Update prevPaneToNode
	ds.prevPaneToNode = make(map[string]string)
	for paneID, nodeKey := range paneToNode {
		ds.prevPaneToNode[paneID] = nodeKey
	}
}

// checkPaneDisappearance detects disappeared panes and marks corresponding nodes as inactive.
// When a pane is killed, it no longer appears in GetAllPanesInfo() output.
// This function compares previous pane states with current pane states to detect disappearances.
func (ds *DaemonState) checkPaneDisappearance(currentPaneStates map[string]uinode.PaneInfo, prevPaneToNode map[string]string, knownNodes map[string]discovery.NodeInfo, events chan<- tui.DaemonEvent) {
	ds.prevPaneStatesMu.RLock()
	defer ds.prevPaneStatesMu.RUnlock()

	// Collect disappeared panes grouped by session (Issue #209)
	disappearedBySession := make(map[string][]string) // session -> []nodeKey

	// Find panes that existed before but don't exist now
	for prevPaneID := range ds.prevPaneStates {
		if _, stillExists := currentPaneStates[prevPaneID]; !stillExists {
			// Pane disappeared - find the node it belonged to
			if nodeKey, found := prevPaneToNode[prevPaneID]; found {
				// Issue #210: Count pending inbox/waiting files for recovery hint
				inboxCount, waitingCount := countPendingFiles(nodeKey, knownNodes)

				details := map[string]interface{}{
					"pane_id": prevPaneID,
					"node":    nodeKey,
				}
				if inboxCount > 0 {
					details["pending_inbox_count"] = inboxCount
				}
				if waitingCount > 0 {
					details["pending_waiting_count"] = waitingCount
				}

				// Send pane_disappeared event to TUI
				events <- tui.DaemonEvent{
					Type:    "pane_disappeared",
					Message: fmt.Sprintf("Pane disappeared: %s (node: %s)", prevPaneID, nodeKey),
					Details: details,
				}
				log.Printf("postman: pane disappeared for node %s (paneID: %s, inbox: %d, waiting: %d)\n", nodeKey, prevPaneID, inboxCount, waitingCount)

				// Group by session name
				sessionName := nodeKey
				if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
					sessionName = parts[0]
				}
				disappearedBySession[sessionName] = append(disappearedBySession[sessionName], nodeKey)
			}
		}
	}

	// Emit session_collapsed event when 2+ panes from same session disappeared (Issue #209)
	for sessionName, collapsedNodes := range disappearedBySession {
		if len(collapsedNodes) >= 2 {
			events <- tui.DaemonEvent{
				Type:    "session_collapsed",
				Message: fmt.Sprintf("Session collapsed: %s (%d panes disappeared)", sessionName, len(collapsedNodes)),
				Details: map[string]interface{}{
					"session": sessionName,
					"nodes":   collapsedNodes,
					"count":   len(collapsedNodes),
				},
			}
			log.Printf("postman: session collapsed: %s (%d panes disappeared: %v)\n", sessionName, len(collapsedNodes), collapsedNodes)
		}
	}
}

// countPendingFiles counts .md files in inbox/{node}/ and waiting/ for a given nodeKey.
// Used for post-collapse recovery hints (Issue #210).
func countPendingFiles(nodeKey string, knownNodes map[string]discovery.NodeInfo) (inboxCount, waitingCount int) {
	nodeInfo, ok := knownNodes[nodeKey]
	if !ok {
		return 0, 0
	}
	simpleName := nodeKey
	if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
		simpleName = parts[1]
	}

	// Count inbox files
	inboxDir := filepath.Join(nodeInfo.SessionDir, "inbox", simpleName)
	if entries, err := os.ReadDir(inboxDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				inboxCount++
			}
		}
	}

	// Count waiting files addressed to this node
	waitingDir := filepath.Join(nodeInfo.SessionDir, "waiting")
	if entries, err := os.ReadDir(waitingDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") && strings.Contains(e.Name(), "-to-"+simpleName) {
				waitingCount++
			}
		}
	}
	return inboxCount, waitingCount
}
