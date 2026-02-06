package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/observer"
	"github.com/i9wa4/tmux-a2a-postman/internal/ping"
	"github.com/i9wa4/tmux-a2a-postman/internal/reminder"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

// RunDaemonLoop runs the daemon event loop in a goroutine.
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
) {
	defer close(events)

	// Debounce timers for different paths
	var inboxTimer *time.Timer
	var configTimer *time.Timer

	// Track watched directories to avoid duplicates
	watchedDirs := make(map[string]bool)

	for {
		select {
		case <-ctx.Done():
			events <- tui.DaemonEvent{
				Type:    "status_update",
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

									// Send PING after delay
									if cfg.NewNodePingDelay > 0 {
										newNodeDelay := time.Duration(cfg.NewNodePingDelay * float64(time.Second))
										capturedNode := nodeName
										capturedNodeInfo := nodeInfo
										capturedActiveNodes := activeNodes
										time.AfterFunc(newNodeDelay, func() {
											if err := ping.SendPingToNode(capturedNodeInfo, contextID, capturedNode, cfg.PingTemplate, cfg, capturedActiveNodes); err != nil {
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
							sessionNodeCount := make(map[string]int)
							for nodeName := range nodes {
								// Extract session name from "session:node" format
								parts := strings.SplitN(nodeName, ":", 2)
								if len(parts) == 2 {
									sessionName := parts[0]
									sessionNodeCount[sessionName]++
								}
							}
							sessionList := make([]tui.SessionInfo, 0, len(sessionNodeCount))
							for sessionName, nodeCount := range sessionNodeCount {
								sessionList = append(sessionList, tui.SessionInfo{
									Name:      sessionName,
									NodeCount: nodeCount,
									Enabled:   true, // Issue #35: All sessions enabled by default (memory only)
								})
							}

							// Update node count and session info
							events <- tui.DaemonEvent{
								Type:    "status_update",
								Message: "Running",
								Details: map[string]interface{}{
									"node_count": len(nodes),
									"sessions":   sessionList, // Issue #36: Bug 2 - Send session info
								},
							}
						}
						// Use eventPath directly for multi-session support
						if err := message.DeliverMessage(eventPath, contextID, nodes, adjacency, cfg); err != nil {
							events <- tui.DaemonEvent{
								Type:    "error",
								Message: fmt.Sprintf("deliver %s: %v", filename, err),
							}
						} else {
							// Send message received event
							events <- tui.DaemonEvent{
								Type:    "message_received",
								Message: fmt.Sprintf("Delivered: %s", filename),
							}
							// Send observer digest on successful delivery
							if info, err := message.ParseMessageFilename(filename); err == nil {
								observer.SendObserverDigest(filename, info.From, nodes, cfg, digestedFiles)
								// Increment reminder counter for recipient
								if info.To != "postman" {
									reminderState.Increment(info.To, nodes, cfg)
								}
							}
						}
					}
				}
			}

			// Handle inbox/ directory events (any session, with debounce)
			if strings.HasSuffix(filepath.Dir(filepath.Dir(eventPath)), "inbox") || strings.HasSuffix(filepath.Dir(eventPath), "inbox") {
				// Debounce inbox updates (200ms)
				if inboxTimer != nil {
					inboxTimer.Stop()
				}
				inboxTimer = time.AfterFunc(200*time.Millisecond, func() {
					// Scan inbox for current node
					if nodeName := os.Getenv("A2A_NODE"); nodeName != "" {
						// Get inbox directory from event path
						// eventPath could be: /path/to/session-xxx/inbox/nodename/message.md
						// or /path/to/session-xxx/inbox/nodename/
						var nodeInboxDir string
						if strings.HasSuffix(filepath.Dir(eventPath), nodeName) {
							nodeInboxDir = filepath.Dir(eventPath)
						} else {
							// Fallback: use default session
							nodeInboxDir = filepath.Join(sessionDir, "inbox", nodeName)
						}
						msgList := message.ScanInboxMessages(nodeInboxDir)
						events <- tui.DaemonEvent{
							Type: "inbox_update",
							Details: map[string]interface{}{
								"messages": msgList,
							},
						}
					}
				})
			}

			// Handle config file events (with debounce)
			if configPath != "" && eventPath == configPath {
				if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
					// Debounce config updates (200ms)
					if configTimer != nil {
						configTimer.Stop()
					}
					configTimer = time.AfterFunc(200*time.Millisecond, func() {
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
						// Send config update event
						edgeList := make([]tui.Edge, len(newCfg.Edges))
						for i, e := range newCfg.Edges {
							// Issue #35: Requirement 5 - edge activity tracking (placeholder)
							edgeList[i] = tui.Edge{
								Raw:            e,
								LastActivityAt: time.Time{}, // Zero time for now
								IsActive:       false,        // Not active initially
							}
						}

						// Issue #35: Requirement 3 - build session info from nodes
						sessionNodeCount := make(map[string]int)
						for nodeName := range nodes {
							// Extract session name from "session:node" format
							parts := strings.SplitN(nodeName, ":", 2)
							if len(parts) == 2 {
								sessionName := parts[0]
								sessionNodeCount[sessionName]++
							}
						}
						sessionList := make([]tui.SessionInfo, 0, len(sessionNodeCount))
						for sessionName, nodeCount := range sessionNodeCount {
							sessionList = append(sessionList, tui.SessionInfo{
								Name:      sessionName,
								NodeCount: nodeCount,
								Enabled:   true, // Issue #35: All sessions enabled by default (memory only)
							})
						}

						events <- tui.DaemonEvent{
							Type: "config_update",
							Details: map[string]interface{}{
								"edges":    edgeList,
								"sessions": sessionList, // Issue #35: Requirement 3
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
		}
	}
}
