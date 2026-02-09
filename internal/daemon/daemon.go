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
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/observer"
	"github.com/i9wa4/tmux-a2a-postman/internal/ping"
	"github.com/i9wa4/tmux-a2a-postman/internal/reminder"
	"github.com/i9wa4/tmux-a2a-postman/internal/session"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
	"github.com/i9wa4/tmux-a2a-postman/internal/ui_node"
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
	notifiedInboxFiles       map[string]time.Time                // Issue #96: Track notified inbox files (filename -> notification time)
	notifiedInboxFilesMu     sync.RWMutex                        // Issue #96: Mutex for notifiedInboxFiles
	notifiedNodeInactivity   map[string]time.Time                // Issue #99: Track notified node inactivity (nodeKey:severity -> notification time)
	notifiedNodeInactivityMu sync.RWMutex                        // Issue #99: Mutex for notifiedNodeInactivity
	prevPaneStates           map[string]ui_node.PaneInfo          // Issue #98: Track previous pane states for restart detection
	prevPaneStatesMu         sync.RWMutex                        // Issue #98: Mutex for prevPaneStates
}

// NewDaemonState creates a new DaemonState instance (Issue #71).
func NewDaemonState() *DaemonState {
	return &DaemonState{
		edgeHistory:            make(map[string]EdgeActivity),
		enabledSessions:        make(map[string]bool),
		notifiedInboxFiles:     make(map[string]time.Time),     // Issue #96
		notifiedNodeInactivity: make(map[string]time.Time),     // Issue #99
		prevPaneStates:         make(map[string]ui_node.PaneInfo), // Issue #98
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
				switch {
				case forwardActive && backwardActive:
					segmentDir = "bidirectional"
					isActive = true
				case forwardActive:
					segmentDir = "forward"
					isActive = true
				case backwardActive:
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
	digestedFiles map[string]bool,
	reminderState *reminder.ReminderState,
	events chan<- tui.DaemonEvent,
	configPath string,
	nodesDir string,
	daemonState *DaemonState,
	idleTracker *idle.IdleTracker,
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

	// Issue #41: Periodic node discovery
	scanInterval := time.Duration(cfg.ScanInterval * float64(time.Second))
	scanTicker := time.NewTicker(scanInterval)
	defer scanTicker.Stop()

	// Issue #96: Periodic inbox stagnation check (30 seconds)
	inboxCheckTicker := time.NewTicker(30 * time.Second)
	defer inboxCheckTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("postman: daemon loop received shutdown signal")
			// Issue #57: Send channel_closed to trigger TUI exit
			events <- tui.DaemonEvent{
				Type:    "channel_closed",
				Message: "Shutting down",
			}
			log.Println("postman: daemon loop stopped")
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
						// Re-discover nodes before each delivery
						if freshNodes, err := discovery.DiscoverNodes(baseDir, contextID); err == nil {
							// Build active nodes list
							activeNodes := make([]string, 0, len(freshNodes))
							for nodeName := range freshNodes {
								activeNodes = append(activeNodes, nodeName)
							}

							// Detect new nodes and send PING
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

									// Send PING after delay (Issue #98: Apply PingMode filter)
									shouldPing := false
									switch cfg.PingMode {
									case "all":
										shouldPing = true
									case "ui_node_only":
										// Extract simple name for UI node check
										simpleName := nodeName
										if parts := strings.SplitN(nodeName, ":", 2); len(parts) == 2 {
											simpleName = parts[1]
										}
										shouldPing = (simpleName == cfg.UINode)
									case "disabled":
										shouldPing = false
									default:
										shouldPing = true // Default to "all" behavior
									}

									if shouldPing && cfg.NewNodePingDelay > 0 {
										newNodeDelay := time.Duration(cfg.NewNodePingDelay * float64(time.Second))
										capturedNode := nodeName
										capturedNodeInfo := nodeInfo
										capturedActiveNodes := activeNodes
										capturedPongActiveNodes := idleTracker.GetPongActiveNodes()
										safeAfterFunc(newNodeDelay, "new-node-ping", events, func() {
											if err := ping.SendPingToNode(capturedNodeInfo, contextID, capturedNode, cfg.PingTemplate, cfg, capturedActiveNodes, capturedPongActiveNodes); err != nil {
												events <- tui.DaemonEvent{
													Type:    "error",
													Message: fmt.Sprintf("PING to new node %s failed: %v", capturedNode, err),
												}
											}
										})
									}
								}
							}
							nodes = freshNodes

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
							sessionList := session.BuildSessionList(nodes, daemonState.IsSessionEnabled)

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
									// Issue #55: Handle PONG separately from normal messages
									if info.To == "postman" {
										// PONG received - track state, skip edge/observer/reminder
										// Issue #79: Use session-prefixed key for tracking
										prefixedKey := sourceSessionName + ":" + info.From
										idleTracker.MarkPongReceived(prefixedKey)
										idleTracker.UpdateSendActivity(prefixedKey) // Track PONG as send activity
										events <- tui.DaemonEvent{
											Type: "pong_received",
											Details: map[string]interface{}{
												"node": prefixedKey,
											},
										}
									} else {
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

										observer.SendObserverDigest(filename, info.From, info.To, nodes, cfg, digestedFiles)
										// Increment reminder counter for recipient
										reminderState.Increment(info.To, nodes, cfg)

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

						// Send config update event
						// Issue #37: Build edge list with activity data
						edgeList := daemonState.BuildEdgeList(newCfg.Edges, newCfg)

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
						sessionList := session.BuildSessionList(nodes, daemonState.IsSessionEnabled)

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
			// Issue #41: Periodic node discovery
			freshNodes, err := discovery.DiscoverNodes(baseDir, contextID)
			if err != nil {
				continue
			}

			// Build active nodes list
			activeNodes := make([]string, 0, len(freshNodes))
			for nodeName := range freshNodes {
				activeNodes = append(activeNodes, nodeName)
			}

			// Detect new nodes and send PING
			newNodesDetected := false
			for nodeName, nodeInfo := range freshNodes {
				if !knownNodes[nodeName] {
					knownNodes[nodeName] = true
					newNodesDetected = true

					// Ensure session directories exist for new node
					if err := config.CreateSessionDirs(nodeInfo.SessionDir); err != nil {
						events <- tui.DaemonEvent{
							Type:    "error",
							Message: fmt.Sprintf("failed to create session dirs for %s: %v", nodeName, err),
						}
						continue
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

					// Send PING after delay (Issue #98: Apply PingMode filter)
					shouldPing := false
					switch cfg.PingMode {
					case "all":
						shouldPing = true
					case "ui_node_only":
						// Extract simple name for UI node check
						simpleName := nodeName
						if parts := strings.SplitN(nodeName, ":", 2); len(parts) == 2 {
							simpleName = parts[1]
						}
						shouldPing = (simpleName == cfg.UINode)
					case "disabled":
						shouldPing = false
					default:
						shouldPing = true // Default to "all" behavior
					}

					if shouldPing && cfg.NewNodePingDelay > 0 {
						newNodeDelay := time.Duration(cfg.NewNodePingDelay * float64(time.Second))
						capturedNode := nodeName
						capturedNodeInfo := nodeInfo
						capturedActiveNodes := activeNodes
						capturedPongActiveNodes := idleTracker.GetPongActiveNodes()
						safeAfterFunc(newNodeDelay, "scan-discovered-ping", events, func() {
							if err := ping.SendPingToNode(capturedNodeInfo, contextID, capturedNode, cfg.PingTemplate, cfg, capturedActiveNodes, capturedPongActiveNodes); err != nil {
								events <- tui.DaemonEvent{
									Type:    "error",
									Message: fmt.Sprintf("PING to new node %s failed: %v", capturedNode, err),
								}
							}
						})
					}
				}
			}

			// Update nodes map and send status update if new nodes detected
			if newNodesDetected {
				nodes = freshNodes

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
				sessionList := session.BuildSessionList(nodes, daemonState.IsSessionEnabled)

				// Update node count and session info
				events <- tui.DaemonEvent{
					Type:    "status_update",
					Message: "Running",
					Details: map[string]interface{}{
						"node_count":    len(nodes),
						"sessions":      sessionList,
						"session_nodes": sessionNodes, // Issue #59: Session-node mapping
					},
				}
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
			paneStates, err := ui_node.GetAllPanesInfo()
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

					// Issue #98: Check for pane restarts
					daemonState.checkPaneRestarts(paneStates, paneToNode, nodes, cfg, events, contextID, adjacency, idleTracker)

					// Check for pane disappearance (killed panes)
					daemonState.checkPaneDisappearance(paneStates, paneToNode, events)

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
			currentNode := os.Getenv("A2A_NODE")
			daemonState.checkInboxStagnation(nodes, cfg, events, currentNode)

			// Issue #99: Check node inactivity
			daemonState.checkNodeInactivity(nodes, idleTracker, cfg, events)

			// Issue #100: Check unreplied messages
			daemonState.checkUnrepliedMessages(nodes, cfg, events)
		}
	}
}

// SetSessionEnabled sets the enabled/disabled state for a session (Issue #71).
func (ds *DaemonState) SetSessionEnabled(sessionName string, enabled bool) {
	ds.enabledSessionsMu.Lock()
	defer ds.enabledSessionsMu.Unlock()
	ds.enabledSessions[sessionName] = enabled
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

// checkInboxStagnation checks for stagnant inbox messages and sends notifications (Issue #96).
// Warning: 10+ minutes, Critical: 30+ minutes.
func (ds *DaemonState) checkInboxStagnation(nodes map[string]discovery.NodeInfo, cfg *config.Config, events chan<- tui.DaemonEvent, currentNodeName string) {
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
			message := fmt.Sprintf("Inbox stagnation [%s]: %s has unread message in inbox for %s (threshold: %s)",
				strings.ToUpper(severity),
				simpleName,
				age.Round(time.Second).String(),
				threshold.String(),
			)
			
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
			
			// Send tmux notification to the pane
			tmuxMsg := fmt.Sprintf("⚠️  %s", message)
			_ = exec.Command("tmux", "display-message", "-t", nodeInfo.PaneID, tmuxMsg).Run()
			// Ignore tmux command error (pane might not exist)
		}
	}
}

// checkNodeInactivity checks for inactive nodes and sends notifications (Issue #99).
// Warning: 5-10 minutes, Critical: 15-20 minutes, Dropped: 30-60 minutes.
func (ds *DaemonState) checkNodeInactivity(nodes map[string]discovery.NodeInfo, idleTracker *idle.IdleTracker, cfg *config.Config, events chan<- tui.DaemonEvent) {
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

		// Build notification message
		eventType := "node_inactivity_" + severity
		message := fmt.Sprintf("Node inactivity [%s]: %s inactive for %s (threshold: %s). Last sent: %s, Last received: %s, PONG received: %s",
			strings.ToUpper(severity),
			nodeKey,
			inactiveDuration.Round(time.Second).String(),
			threshold.String(),
			lastSentStr,
			lastReceivedStr,
			pongStatus,
		)

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

		// Send tmux notification to the pane
		nodeInfo := nodes[nodeKey]
		tmuxMsg := fmt.Sprintf("⚠️  %s", message)
		_ = exec.Command("tmux", "display-message", "-t", nodeInfo.PaneID, tmuxMsg).Run()
		// Ignore tmux command error (pane might not exist)
	}
}

// checkPaneRestarts detects pane restarts and sends PING (Issue #98).
// Detects restart by comparing current paneStates with previous paneStates.
func (ds *DaemonState) checkPaneRestarts(paneStates map[string]ui_node.PaneInfo, paneToNode map[string]string, nodes map[string]discovery.NodeInfo, cfg *config.Config, events chan<- tui.DaemonEvent, contextID string, adjacency map[string][]string, idleTracker *idle.IdleTracker) {
	ds.prevPaneStatesMu.Lock()
	defer ds.prevPaneStatesMu.Unlock()

	// Check PingMode
	if cfg.PingMode == "disabled" {
		return
	}

	// Build active nodes list for PING
	activeNodes := make([]string, 0, len(nodes))
	for nodeName := range nodes {
		activeNodes = append(activeNodes, nodeName)
	}

	// Get PONG-active nodes for PING
	pongActiveNodes := idleTracker.GetPongActiveNodes()

	for currentPaneID, currentInfo := range paneStates {
		nodeKey, exists := paneToNode[currentPaneID]
		if !exists {
			continue // No node mapped to this pane
		}

		nodeInfo, nodeExists := nodes[nodeKey]
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
		for oldID, oldInfo := range ds.prevPaneStates {
			if oldNodeKey, found := paneToNode[oldID]; found && oldNodeKey == nodeKey {
				// Found old pane for the same node
				oldPaneID = oldID
				_ = oldInfo // Use oldInfo to avoid unused variable warning
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
					"node":         nodeKey,
					"old_pane_id":  oldPaneID,
					"new_pane_id":  currentPaneID,
					"pane_info":    currentInfo,
				},
			}

			// Send PING after NewNodePingDelay
			// Check PingMode filter
			shouldPing := false
			switch cfg.PingMode {
			case "all":
				shouldPing = true
			case "ui_node_only":
				// Extract simple name for UI node check
				simpleName := nodeKey
				if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
					simpleName = parts[1]
				}
				shouldPing = (simpleName == cfg.UINode)
			}

			if shouldPing && cfg.NewNodePingDelay > 0 {
				delay := time.Duration(cfg.NewNodePingDelay * float64(time.Second))
				safeAfterFunc(delay, "pane-restart-ping", events, func() {
					if err := ping.SendPingToNode(nodeInfo, contextID, nodeKey, cfg.PingTemplate, cfg, activeNodes, pongActiveNodes); err != nil {
						events <- tui.DaemonEvent{
							Type:    "error",
							Message: fmt.Sprintf("PING to restarted node %s failed: %v", nodeKey, err),
						}
					}
				})
			}
		}
	}

	// Update prevPaneStates
	ds.prevPaneStates = make(map[string]ui_node.PaneInfo)
	for paneID, info := range paneStates {
		ds.prevPaneStates[paneID] = info
	}
}

// checkPaneDisappearance detects disappeared panes and marks corresponding nodes as inactive.
// When a pane is killed, it no longer appears in GetAllPanesInfo() output.
// This function compares previous pane states with current pane states to detect disappearances.
func (ds *DaemonState) checkPaneDisappearance(currentPaneStates map[string]ui_node.PaneInfo, paneToNode map[string]string, events chan<- tui.DaemonEvent) {
	ds.prevPaneStatesMu.RLock()
	defer ds.prevPaneStatesMu.RUnlock()

	// Find panes that existed before but don't exist now
	for prevPaneID := range ds.prevPaneStates {
		if _, stillExists := currentPaneStates[prevPaneID]; !stillExists {
			// Pane disappeared - find the node it belonged to
			if nodeKey, found := paneToNode[prevPaneID]; found {
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
func (ds *DaemonState) checkUnrepliedMessages(nodes map[string]discovery.NodeInfo, cfg *config.Config, events chan<- tui.DaemonEvent) {
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
			message := fmt.Sprintf("Unreplied message [WARNING]: %s has message in read/ for %s without reply (from: %s, threshold: %s)",
				simpleName,
				timeSinceRead.Round(time.Second).String(),
				msgInfo.From,
				threshold.String(),
			)

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

			// Send tmux notification to the pane
			tmuxMsg := fmt.Sprintf("⚠️  %s", message)
			_ = exec.Command("tmux", "display-message", "-t", nodeInfo.PaneID, tmuxMsg).Run()
			// Ignore tmux command error (pane might not exist)
		}
	}
}
