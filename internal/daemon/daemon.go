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
	postDir := filepath.Join(sessionDir, "post")
	inboxDir := filepath.Join(sessionDir, "inbox")

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

			// Handle post/ directory events
			if strings.HasPrefix(eventPath, postDir) {
				if event.Op&(fsnotify.Create|fsnotify.Rename) != 0 {
					filename := filepath.Base(eventPath)
					if strings.HasSuffix(filename, ".md") {
						// Re-discover nodes before each delivery
						if freshNodes, err := discovery.DiscoverNodes(baseDir); err == nil {
							// Detect new nodes and send PING
							for nodeName, nodeInfo := range freshNodes {
								if !knownNodes[nodeName] {
									knownNodes[nodeName] = true
									// Send PING after delay
									if cfg.NewNodePingDelay > 0 {
										newNodeDelay := time.Duration(cfg.NewNodePingDelay * float64(time.Second))
										capturedNode := nodeName
										capturedNodeInfo := nodeInfo
										time.AfterFunc(newNodeDelay, func() {
											if err := ping.SendPingToNode(capturedNodeInfo, contextID, capturedNode, cfg.PingTemplate, cfg); err != nil {
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
							// Update node count
							events <- tui.DaemonEvent{
								Type:    "status_update",
								Message: "Running",
								Details: map[string]interface{}{
									"node_count": len(nodes),
								},
							}
						}
						if err := message.DeliverMessage(sessionDir, contextID, filename, nodes, adjacency, cfg); err != nil {
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

			// Handle inbox/ directory events (with debounce)
			if strings.HasPrefix(eventPath, inboxDir) {
				// Debounce inbox updates (200ms)
				if inboxTimer != nil {
					inboxTimer.Stop()
				}
				inboxTimer = time.AfterFunc(200*time.Millisecond, func() {
					// Scan inbox for current node
					if nodeName := os.Getenv("A2A_NODE"); nodeName != "" {
						msgList := message.ScanInboxMessages(filepath.Join(inboxDir, nodeName))
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
							edgeList[i] = tui.Edge{Raw: e}
						}
						events <- tui.DaemonEvent{
							Type: "config_update",
							Details: map[string]interface{}{
								"edges": edgeList,
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
