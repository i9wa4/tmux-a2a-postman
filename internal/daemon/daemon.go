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
	edgeHistory              map[string]EdgeActivity
	edgeHistoryMu            sync.RWMutex
	enabledSessions          map[string]bool
	enabledSessionsMu        sync.RWMutex
	notifiedInboxFiles       map[string]time.Time       // Issue #96: Track notified inbox files (filename -> notification time)
	notifiedInboxFilesMu     sync.RWMutex               // Issue #96: Mutex for notifiedInboxFiles
	notifiedNodeInactivity   map[string]time.Time       // Issue #99: Track notified node inactivity (nodeKey:severity -> notification time)
	notifiedNodeInactivityMu sync.RWMutex               // Issue #99: Mutex for notifiedNodeInactivity
	prevPaneStates           map[string]uinode.PaneInfo // Issue #98: Track previous pane states for restart detection
	prevPaneStatesMu         sync.RWMutex               // Issue #98: Mutex for prevPaneStates
	prevPaneToNode           map[string]string          // Track previous pane ID -> node key mapping for restart detection
	lastAlertTimestamp       map[string]time.Time       // Issue #118: Track last alert timestamps (alertKey -> time)
	lastAlertTimestampMu     sync.RWMutex               // Issue #118: Mutex for lastAlertTimestamp
}

// NewDaemonState creates a new DaemonState instance (Issue #71).
func NewDaemonState() *DaemonState {
	return &DaemonState{
		edgeHistory:            make(map[string]EdgeActivity),
		enabledSessions:        make(map[string]bool),
		notifiedInboxFiles:     make(map[string]time.Time),       // Issue #96
		notifiedNodeInactivity: make(map[string]time.Time),       // Issue #99
		prevPaneStates:         make(map[string]uinode.PaneInfo), // Issue #98
		prevPaneToNode:         make(map[string]string),          // paneID -> nodeKey mapping
		lastAlertTimestamp:     make(map[string]time.Time),       // Issue #118
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

	// Issue #96: Periodic inbox stagnation check (30 seconds)
	inboxCheckTicker := time.NewTicker(30 * time.Second)
	defer inboxCheckTicker.Stop()

	// Issue #136: Start heartbeat-LLM trigger goroutine if configured
	if cfg.Heartbeat.Enabled && cfg.Heartbeat.LLMNode != "" && cfg.Heartbeat.IntervalSeconds > 0 {
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

									// Generate RULES.md for newly discovered node session
									if err := config.GenerateRulesFile(nodeInfo.SessionDir, contextID, cfg); err != nil {
										log.Printf("postman: WARNING: failed to generate RULES.md for %s: %v\n", nodeName, err)
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
							sessionList := session.BuildSessionList(nodes, allSessions, daemonState.IsSessionEnabled)

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
								// Issue #79: Extract session name for PONG tracking
								sourceSessionDir := filepath.Dir(filepath.Dir(eventPath))
								sourceSessionName := filepath.Base(sourceSessionDir)
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

									// Only count human-authored messages toward reminder threshold.
									// Daemon-generated alerts (from="postman" or from="daemon") must not
									// accelerate the reminder counter.
									if reminderShouldIncrement(info.From) {
										reminderState.Increment(info.To, sourceSessionName, nodes, cfg)
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
				// Handle read/ directory events — synthesize PONG from inbox->read/ move (Issue #150).
				if event.Op&(fsnotify.Create|fsnotify.Rename) != 0 {
					filename := filepath.Base(eventPath)
					if strings.HasSuffix(filename, ".md") {
						if info, err := message.ParseMessageFilename(filename); err == nil {
							// Skip PONG files moved to read/ by daemon (To == "postman").
							// Skip daemon-originated files.
							if info.To != "postman" && info.To != "daemon" {
								// Synthesize PONG: node archived a message, proving it is alive.
								sourceSessionDir := filepath.Dir(filepath.Dir(eventPath))
								sourceSessionName := filepath.Base(sourceSessionDir)
								prefixedKey := sourceSessionName + ":" + info.To
								idleTracker.MarkPongReceived(prefixedKey)
								events <- tui.DaemonEvent{
									Type: "pong_received",
									Details: map[string]interface{}{
										"node":   prefixedKey,
										"source": "read_move",
									},
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
									if writeErr := os.WriteFile(waitingFile, []byte(waitingContent), 0o644); writeErr != nil {
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

						// Issue #75: Regenerate RULES.md on config reload
						if err := config.GenerateRulesFile(sessionDir, contextID, newCfg); err != nil {
							log.Printf("⚠️  postman: failed to regenerate RULES.md: %v\n", err)
						}
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
						sessionList := session.BuildSessionList(nodes, allSessions, daemonState.IsSessionEnabled)

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

					// Generate RULES.md for newly discovered node session
					if err := config.GenerateRulesFile(nodeInfo.SessionDir, contextID, cfg); err != nil {
						log.Printf("postman: WARNING: failed to generate RULES.md for %s: %v\n", nodeName, err)
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
			sessionList := session.BuildSessionList(nodes, allSessions, daemonState.IsSessionEnabled)

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
					daemonState.checkPaneDisappearance(paneStates, daemonState.prevPaneToNode, events)

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
			// Issue #96: Check inbox stagnation
			currentNode := config.GetTmuxPaneName()
			daemonState.checkInboxStagnation(nodes, cfg, events, currentNode, sessionDir, contextID, adjacency)

			// Issue #99: Check node inactivity
			daemonState.checkNodeInactivity(nodes, idleTracker, cfg, events, sessionDir, contextID, adjacency)

			// Issue #100: Check unreplied messages
			daemonState.checkUnrepliedMessages(nodes, cfg, events, sessionDir, contextID, adjacency)

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
							_ = os.WriteFile(filePath, []byte(updated), 0o644)
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
						_ = os.WriteFile(filePath, []byte(updated), 0o644)
						continue
					}
					// composing → spinning: pane active after spinning threshold
					if spinningEnabled && time.Since(waitingSince) > spinningThreshold && paneState == "active" {
						updated := replaceWaitingState(contentStr, "composing", "spinning")
						_ = os.WriteFile(filePath, []byte(updated), 0o644)
						// Send spinning alert with rate-limiting
						alertKey := fmt.Sprintf("spinning:%s:%s", nodeInfo.SessionName, fileInfo.To)
						if daemonState.ShouldSendAlert(alertKey, 300.0) {
							spinningDuration := time.Since(waitingSince).Round(time.Second)
							alertVars := map[string]string{
								"node":              fileInfo.To,
								"spinning_duration": spinningDuration.String(),
								"threshold":         spinningThreshold.String(),
							}
							alertMsg := template.ExpandVariables(cfg.SpinningAlertTemplate, alertVars)
							// Always mark sent regardless of delivery result: once in spinning state,
							// subsequent ticks take the isSpinning branch (no alert path), so
							// not marking would permanently lose the cooldown and prevent retries
							// that can never happen anyway.
							_ = sendAlertToUINode(nodeInfo.SessionDir, contextID, cfg.UINode,
								alertMsg, "spinning", cfg, adjacency, nodes)
							daemonState.MarkAlertSent(alertKey)
							events <- tui.DaemonEvent{
								Type:    "spinning_detected",
								Message: alertMsg,
								Details: map[string]interface{}{
									"node":              fileInfo.To,
									"spinning_duration": time.Since(waitingSince).Seconds(),
									"threshold":         cfg.NodeSpinningSeconds,
								},
							}
						}
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
// Returns true if session is enabled, false otherwise.
func (ds *DaemonState) IsSessionEnabled(sessionName string) bool {
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

// sendAlertToUINodeLegacy is the legacy implementation of sendAlertToUINode,
// used as fallback when AlertMessageTemplate is empty.
func sendAlertToUINodeLegacy(sessionDir, contextID, uiNode, message, alertType string) error {
	now := time.Now()
	ts := fmt.Sprintf("%s-%d", now.Format("20060102-150405"), now.UnixNano()%1000000)
	filename := fmt.Sprintf("%s-from-daemon-to-%s.md", ts, uiNode)
	postPath := filepath.Join(sessionDir, "post", filename)

	content := fmt.Sprintf(`---
method: message/send
params:
  contextId: %s
  from: daemon
  to: %s
  timestamp: %s
  alertType: %s
---

## Alert: %s

%s
`, contextID, uiNode, now.Format(time.RFC3339), alertType, alertType, message)

	return os.WriteFile(postPath, []byte(content), 0o644)
}

// sendAlertToUINode sends an alert message to the ui_node inbox (Issue #118).
// Replaces tmux display-message calls with postman messaging.
// When AlertMessageTemplate is configured, uses two-pass expansion (BuildEnvelope + Pass 2).
// Falls back to sendAlertToUINodeLegacy when AlertMessageTemplate is empty.
func sendAlertToUINode(sessionDir, contextID, uiNode, message, alertType string, cfg *config.Config, adjacency map[string][]string, nodes map[string]discovery.NodeInfo) error {
	tmpl := cfg.AlertMessageTemplate
	if tmpl == "" {
		return sendAlertToUINodeLegacy(sessionDir, contextID, uiNode, message, alertType)
	}

	sourceSessionName := filepath.Base(filepath.Dir(sessionDir))
	now := time.Now()
	ts := fmt.Sprintf("%s-%d", now.Format("20060102-150405"), now.UnixNano()%1000000)
	filename := fmt.Sprintf("%s-from-daemon-to-%s.md", ts, uiNode)
	postPath := filepath.Join(sessionDir, "post", filename)
	taskID := ts + "-alert"

	// Pass 1: BuildEnvelope for standard vars (contacts_section, reply_command, etc.)
	scaffolded := envelope.BuildEnvelope(
		cfg, tmpl, uiNode, "daemon",
		contextID, taskID, postPath,
		nil, adjacency, nodes, sourceSessionName,
		nil, // pongActiveNodes = nil → static adjacency
	)

	// Pass 2: alert-specific + daemon-specific vars
	content := template.ExpandVariables(scaffolded, map[string]string{
		"alert_type":   alertType,
		"message":      message,
		"role_content": envelope.BuildRoleContent(cfg, uiNode),
	})

	return os.WriteFile(postPath, []byte(content), 0o644)
}

// parseMessageTimestamp extracts timestamp from message filename (Issue #96).
// Filename format: YYYYMMDD-HHMMSS-from-{sender}-to-{recipient}.md
// Example: 20260210-015513-from-orchestrator-to-worker.md
func parseMessageTimestamp(filename string) (time.Time, error) {
	// Extract first 15 characters: YYYYMMDD-HHMMSS
	if len(filename) < 15 {
		return time.Time{}, fmt.Errorf("invalid filename format: too short")
	}
	tsStr := filename[:15]

	// Parse as YYYYMMDD-HHMMSS
	t, err := time.Parse("20060102-150405", tsStr)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse timestamp: %w", err)
	}
	return t, nil
}

// reminderShouldIncrement returns true if the message sender should trigger the reminder counter.
// Daemon-generated messages (from="postman" or from="daemon") are excluded.
func reminderShouldIncrement(from string) bool {
	return from != "postman" && from != "daemon"
}

// checkInboxStagnation checks for stagnant inbox messages and sends notifications (Issue #96).
// Warning: 10+ minutes, Critical: 30+ minutes.
// Issue #118: Added sessionDir and contextID for alert messaging.
func (ds *DaemonState) checkInboxStagnation(nodes map[string]discovery.NodeInfo, cfg *config.Config, events chan<- tui.DaemonEvent, currentNodeName, sessionDir, contextID string, adjacency map[string][]string) {
	now := time.Now()

	for nodeKey, nodeInfo := range nodes {
		// Extract simple name from session-prefixed key
		simpleName := nodeKey
		if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
			simpleName = parts[1]
		}

		// Skip self (current node's inbox)
		if simpleName == currentNodeName {
			continue
		}

		// Build inbox path
		inboxPath := filepath.Join(nodeInfo.SessionDir, "inbox", simpleName)

		// Read inbox directory
		entries, err := os.ReadDir(inboxPath)
		if err != nil {
			continue // Skip if directory doesn't exist or can't be read
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}

			// Parse message timestamp
			msgTime, err := parseMessageTimestamp(entry.Name())
			if err != nil {
				continue // Skip files with invalid timestamp
			}

			// Calculate age
			age := now.Sub(msgTime)

			// Determine severity
			var severity string
			var threshold time.Duration
			switch {
			case age >= 30*time.Minute:
				severity = "critical"
				threshold = 30 * time.Minute
			case age >= 10*time.Minute:
				severity = "warning"
				threshold = 10 * time.Minute
			default:
				continue // No notification needed
			}

			// Check if already notified at this severity level
			fileKey := filepath.Join(inboxPath, entry.Name()) + ":" + severity
			ds.notifiedInboxFilesMu.RLock()
			lastNotified, notified := ds.notifiedInboxFiles[fileKey]
			ds.notifiedInboxFilesMu.RUnlock()

			// Skip if already notified within cooldown period (5 minutes)
			if notified && now.Sub(lastNotified) < 5*time.Minute {
				continue
			}

			// Record notification
			ds.notifiedInboxFilesMu.Lock()
			ds.notifiedInboxFiles[fileKey] = now
			ds.notifiedInboxFilesMu.Unlock()

			// Build notification message
			eventType := "inbox_stagnation_" + severity
			// NOTE: no empty-guard; postman.default.toml always provides non-empty default
			alertVars := map[string]string{
				"severity":  strings.ToUpper(severity),
				"node":      simpleName,
				"age":       age.Round(time.Second).String(),
				"threshold": threshold.String(),
			}
			message := template.ExpandVariables(cfg.InboxStagnationAlertTemplate, alertVars)

			// Send TUI event
			events <- tui.DaemonEvent{
				Type:    eventType,
				Message: message,
				Details: map[string]interface{}{
					"node":      simpleName,
					"filename":  entry.Name(),
					"age":       age.Seconds(),
					"threshold": threshold.Seconds(),
					"severity":  severity,
				},
			}

			// Issue #118: Send alert to ui_node with rate-limiting
			alertKey := fmt.Sprintf("inbox_stagnation:%s:%s", nodeKey, severity)
			cooldown := 300.0 // 5 minutes

			if ds.ShouldSendAlert(alertKey, cooldown) {
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
				err := sendAlertToUINode(sessionDir, contextID, cfg.UINode, message+actionText, "inbox_stagnation", cfg, adjacency, nodes)
				if err == nil {
					ds.MarkAlertSent(alertKey)
				}
			}
		}
	}

	// Node-level inbox unread summary (Issue #xxx)
	if cfg.InboxUnreadThreshold > 0 {
		for nodeKey, nodeInfo := range nodes {
			// Extract simple name from session-prefixed key
			simpleName := nodeKey
			if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
				simpleName = parts[1]
			}

			// Skip self (current node's inbox)
			if simpleName == currentNodeName {
				continue
			}

			// Build inbox path
			inboxPath := filepath.Join(nodeInfo.SessionDir, "inbox", simpleName)

			// Count inbox files
			entries, err := os.ReadDir(inboxPath)
			if err != nil {
				continue // Skip if directory doesn't exist or can't be read
			}

			inboxCount := 0
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
					inboxCount++
				}
			}

			// Check if count exceeds threshold
			if inboxCount < cfg.InboxUnreadThreshold {
				continue
			}

			// Check cooldown (use separate key for summary notifications)
			summaryKey := nodeKey + ":inbox_summary"
			ds.notifiedInboxFilesMu.RLock()
			lastNotified, notified := ds.notifiedInboxFiles[summaryKey]
			ds.notifiedInboxFilesMu.RUnlock()

			// Skip if already notified within cooldown period (5 minutes)
			if notified && now.Sub(lastNotified) < 5*time.Minute {
				continue
			}

			// Record notification
			ds.notifiedInboxFilesMu.Lock()
			ds.notifiedInboxFiles[summaryKey] = now
			ds.notifiedInboxFilesMu.Unlock()

			// Build notification message
			// NOTE: no empty-guard; postman.default.toml always provides non-empty default
			alertVars := map[string]string{
				"node":      simpleName,
				"count":     fmt.Sprintf("%d", inboxCount),
				"threshold": fmt.Sprintf("%d", cfg.InboxUnreadThreshold),
			}
			message := template.ExpandVariables(cfg.InboxUnreadSummaryAlertTemplate, alertVars)

			// Send TUI event
			events <- tui.DaemonEvent{
				Type:    "inbox_unread_summary",
				Message: message,
				Details: map[string]interface{}{
					"node":      simpleName,
					"count":     inboxCount,
					"threshold": cfg.InboxUnreadThreshold,
				},
			}

			// Issue #118: Send alert to ui_node with rate-limiting
			alertKey := fmt.Sprintf("unreplied_messages:%s", nodeKey)
			cooldown := 300.0 // 5 minutes

			if ds.ShouldSendAlert(alertKey, cooldown) {
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
				err := sendAlertToUINode(sessionDir, contextID, cfg.UINode, message+actionText, "inbox_unread_summary", cfg, adjacency, nodes)
				if err == nil {
					ds.MarkAlertSent(alertKey)
				}
			}
		}
	}
}

// checkNodeInactivity checks for inactive nodes and sends notifications (Issue #99).
// Warning: 5-10 minutes, Critical: 15-20 minutes, Dropped: 30-60 minutes.
// Issue #118: Added sessionDir and contextID for alert messaging.
func (ds *DaemonState) checkNodeInactivity(nodes map[string]discovery.NodeInfo, idleTracker *idle.IdleTracker, cfg *config.Config, events chan<- tui.DaemonEvent, sessionDir, contextID string, adjacency map[string][]string) {
	now := time.Now()
	nodeStates := idleTracker.GetNodeStates()

	for nodeKey := range nodes {
		activity, exists := nodeStates[nodeKey]
		if !exists {
			continue
		}

		// Calculate inactivity duration from max(LastSent, LastReceived)
		lastActivity := activity.LastSent
		if activity.LastReceived.After(lastActivity) {
			lastActivity = activity.LastReceived
		}

		if lastActivity.IsZero() {
			continue // No activity yet
		}

		inactiveDuration := now.Sub(lastActivity)

		// Determine severity based on inactivity duration
		var severity string
		var threshold time.Duration
		switch {
		case inactiveDuration >= 30*time.Minute: // Dropped: 30-60 minutes
			severity = "dropped"
			threshold = 30 * time.Minute
		case inactiveDuration >= 15*time.Minute: // Critical: 15-20 minutes
			severity = "critical"
			threshold = 15 * time.Minute
		case inactiveDuration >= 5*time.Minute: // Warning: 5-10 minutes
			severity = "warning"
			threshold = 5 * time.Minute
		default:
			continue // No notification needed
		}

		// Check if already notified at this severity level
		notifKey := nodeKey + ":" + severity
		ds.notifiedNodeInactivityMu.RLock()
		lastNotified, notified := ds.notifiedNodeInactivity[notifKey]
		ds.notifiedNodeInactivityMu.RUnlock()

		// Skip if already notified within cooldown period (5 minutes)
		if notified && now.Sub(lastNotified) < 5*time.Minute {
			continue
		}

		// Record notification
		ds.notifiedNodeInactivityMu.Lock()
		ds.notifiedNodeInactivity[notifKey] = now
		ds.notifiedNodeInactivityMu.Unlock()

		// Format timestamps for display
		lastSentStr := "N/A"
		if !activity.LastSent.IsZero() {
			lastSentStr = activity.LastSent.Format("15:04:05")
		}
		lastReceivedStr := "N/A"
		if !activity.LastReceived.IsZero() {
			lastReceivedStr = activity.LastReceived.Format("15:04:05")
		}
		pongStatus := "Yes"
		if !activity.PongReceived {
			pongStatus = "No"
		}

		// Normalize nodeKey to simple name (strip session prefix)
		parts := strings.SplitN(nodeKey, ":", 2)
		var simpleName string
		if len(parts) == 2 {
			simpleName = parts[1]
		} else {
			simpleName = nodeKey
		}

		// Build notification message
		eventType := "node_inactivity_" + severity
		// NOTE: no empty-guard; postman.default.toml always provides non-empty default
		alertVars := map[string]string{
			"severity":          strings.ToUpper(severity),
			"node":              simpleName,
			"inactive_duration": inactiveDuration.Round(time.Second).String(),
			"threshold":         threshold.String(),
			"last_sent":         lastSentStr,
			"last_received":     lastReceivedStr,
			"pong_received":     pongStatus,
		}
		message := template.ExpandVariables(cfg.NodeInactivityAlertTemplate, alertVars)

		// Send TUI event
		events <- tui.DaemonEvent{
			Type:    eventType,
			Message: message,
			Details: map[string]interface{}{
				"node":              nodeKey,
				"inactive_duration": inactiveDuration.Seconds(),
				"threshold":         threshold.Seconds(),
				"severity":          severity,
				"last_sent":         lastSentStr,
				"last_received":     lastReceivedStr,
				"pong_received":     activity.PongReceived,
			},
		}

		// Issue #118: Send alert to ui_node with rate-limiting
		alertKey := fmt.Sprintf("node_inactivity:%s:%s", nodeKey, severity)
		cooldown := 300.0 // 5 minutes

		if ds.ShouldSendAlert(alertKey, cooldown) {
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
			err := sendAlertToUINode(sessionDir, contextID, cfg.UINode, message+actionText, "node_inactivity", cfg, adjacency, nodes)
			if err == nil {
				ds.MarkAlertSent(alertKey)
			}
		}
	}
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
func (ds *DaemonState) checkPaneDisappearance(currentPaneStates map[string]uinode.PaneInfo, prevPaneToNode map[string]string, events chan<- tui.DaemonEvent) {
	ds.prevPaneStatesMu.RLock()
	defer ds.prevPaneStatesMu.RUnlock()

	// Find panes that existed before but don't exist now
	for prevPaneID := range ds.prevPaneStates {
		if _, stillExists := currentPaneStates[prevPaneID]; !stillExists {
			// Pane disappeared - find the node it belonged to
			if nodeKey, found := prevPaneToNode[prevPaneID]; found {
				// Send pane_disappeared event to TUI
				events <- tui.DaemonEvent{
					Type:    "pane_disappeared",
					Message: fmt.Sprintf("Pane disappeared: %s (node: %s)", prevPaneID, nodeKey),
					Details: map[string]interface{}{
						"pane_id": prevPaneID,
						"node":    nodeKey,
					},
				}
				log.Printf("postman: pane disappeared for node %s (paneID: %s)\n", nodeKey, prevPaneID)
			}
		}
	}
}

// checkUnrepliedMessages checks for messages in read/ without replies (Issue #100).
// Warning: 10+ minutes since moved to read/ without reply.
// Issue #118: Added sessionDir and contextID for alert messaging.
func (ds *DaemonState) checkUnrepliedMessages(nodes map[string]discovery.NodeInfo, cfg *config.Config, events chan<- tui.DaemonEvent, sessionDir, contextID string, adjacency map[string][]string) {
	now := time.Now()

	for nodeKey, nodeInfo := range nodes {
		// Extract simple name
		simpleName := nodeKey
		if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
			simpleName = parts[1]
		}

		// Build read/ path
		readPath := filepath.Join(nodeInfo.SessionDir, "read")

		// Read read/ directory
		entries, err := os.ReadDir(readPath)
		if err != nil {
			continue // Skip if directory doesn't exist or can't be read
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}

			// Parse message filename to extract recipient (to field)
			msgInfo, err := message.ParseMessageFilename(entry.Name())
			if err != nil {
				continue // Skip files with invalid format
			}

			// Only check messages TO this node (not FROM this node)
			if msgInfo.To != simpleName {
				continue
			}

			// Get file modification time (when moved to read/)
			fileInfo, err := entry.Info()
			if err != nil {
				continue
			}
			movedToReadAt := fileInfo.ModTime()

			// Calculate time since moved to read/
			timeSinceRead := now.Sub(movedToReadAt)

			// Check if threshold exceeded (10 minutes)
			threshold := 10 * time.Minute
			if timeSinceRead < threshold {
				continue // Not yet exceeded
			}

			// Check if already notified
			notifKey := filepath.Join(readPath, entry.Name()) + ":unreplied"
			ds.notifiedInboxFilesMu.RLock() // Reuse notifiedInboxFiles for simplicity
			lastNotified, notified := ds.notifiedInboxFiles[notifKey]
			ds.notifiedInboxFilesMu.RUnlock()

			// Skip if already notified within cooldown period (5 minutes)
			if notified && now.Sub(lastNotified) < 5*time.Minute {
				continue
			}

			// Record notification
			ds.notifiedInboxFilesMu.Lock()
			ds.notifiedInboxFiles[notifKey] = now
			ds.notifiedInboxFilesMu.Unlock()

			// Build notification message
			// NOTE: no empty-guard; postman.default.toml always provides non-empty default
			alertVars := map[string]string{
				"node":            simpleName,
				"time_since_read": timeSinceRead.Round(time.Second).String(),
				"from":            msgInfo.From,
				"threshold":       threshold.String(),
			}
			message := template.ExpandVariables(cfg.UnrepliedMessageAlertTemplate, alertVars)

			// Send TUI event
			events <- tui.DaemonEvent{
				Type:    "unreplied_message",
				Message: message,
				Details: map[string]interface{}{
					"node":            simpleName,
					"filename":        entry.Name(),
					"time_since_read": timeSinceRead.Seconds(),
					"threshold":       threshold.Seconds(),
					"from":            msgInfo.From,
					"to":              msgInfo.To,
				},
			}

			// Issue #118: Send alert to ui_node with rate-limiting
			alertKey := fmt.Sprintf("unreplied_message:%s:%s", nodeKey, entry.Name())
			cooldown := 300.0 // 5 minutes

			if ds.ShouldSendAlert(alertKey, cooldown) {
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
				err := sendAlertToUINode(sessionDir, contextID, cfg.UINode, message+actionText, "unreplied_message", cfg, adjacency, nodes)
				if err == nil {
					ds.MarkAlertSent(alertKey)
				}
			}
		}
	}
}
