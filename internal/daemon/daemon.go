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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/i9wa4/tmux-a2a-postman/internal/alert"
	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/heartbeat"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
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

func frontmatterBool(content, key string) bool {
	first := strings.Index(content, "---\n")
	if first < 0 {
		return false
	}
	rest := content[first+4:]
	second := strings.Index(rest, "\n---")
	if second < 0 {
		return false
	}
	for _, line := range strings.Split(rest[:second], "\n") {
		if strings.TrimSpace(line) == key+": true" {
			return true
		}
	}
	return false
}

func waitingFileContentForRead(info *message.MessageInfo, messageContent []byte, cfg *config.Config, now time.Time) (string, bool) {
	waitingSince := now.UTC().Format(time.RFC3339)
	if cfg != nil && cfg.UINode != "" && info.To == cfg.UINode {
		return fmt.Sprintf(
			"---\nfrom: %s\nto: %s\nwaiting_since: %s\nstate: user_input\nstate_updated_at: %s\nexpects_reply: false\n---\n",
			info.From, info.To, waitingSince, waitingSince,
		), true
	}
	if !frontmatterBool(string(messageContent), "expects_reply") {
		return "", false
	}
	return fmt.Sprintf(
		"---\nfrom: %s\nto: %s\nwaiting_since: %s\nstate: composing\nstate_updated_at: %s\nexpects_reply: true\n---\n",
		info.From, info.To, waitingSince, waitingSince,
	), true
}

func waitingSinceFromContent(content string) time.Time {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "waiting_since: ") {
			ts := strings.TrimPrefix(line, "waiting_since: ")
			if t, err := time.Parse(time.RFC3339, strings.TrimSpace(ts)); err == nil {
				return t
			}
			return time.Time{}
		}
	}
	return time.Time{}
}

func advanceWaitingState(content, paneState string, now time.Time, idleThreshold, spinningThreshold time.Duration, spinningEnabled bool) (string, bool) {
	if !frontmatterBool(content, "expects_reply") {
		return content, false
	}

	isComposing := strings.Contains(content, "state: composing")
	isSpinning := strings.Contains(content, "state: spinning")
	if !isComposing && !isSpinning {
		return content, false
	}

	if isSpinning {
		if paneState == "stale" {
			return replaceWaitingState(content, "spinning", "stalled"), true
		}
		return content, false
	}

	waitingSince := waitingSinceFromContent(content)
	if waitingSince.IsZero() {
		return content, false
	}
	if now.Sub(waitingSince) <= idleThreshold {
		return content, false
	}
	if paneState == "stale" {
		return replaceWaitingState(content, "composing", "stalled"), true
	}
	if spinningEnabled && now.Sub(waitingSince) > spinningThreshold && paneState == "active" {
		return replaceWaitingState(content, "composing", "spinning"), true
	}
	return content, false
}

func visibleWaitingState(content string) string {
	if strings.Contains(content, "state: user_input") {
		return "user_input"
	}
	if !frontmatterBool(content, "expects_reply") {
		return ""
	}
	switch {
	case strings.Contains(content, "state: stalled"), strings.Contains(content, "state: stuck"):
		return "stalled"
	case strings.Contains(content, "state: spinning"):
		return "spinning"
	case strings.Contains(content, "state: composing"):
		return "composing"
	default:
		return ""
	}
}

// EdgeActivity tracks communication timestamps for an edge (Issue #37).
type EdgeActivity struct {
	LastForwardAt  time.Time // A -> B last communication time
	LastBackwardAt time.Time // B -> A last communication time
}

// DaemonState manages daemon state (Issue #71).
type DaemonState struct {
	contextID                     string        // This daemon's contextID (for tmux option writes)
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
	swallowedRetryCount           map[string]int             // Issue #282: inbox file path -> re-delivery attempt count
	swallowedRetryCountMu         sync.Mutex                 // Issue #282
}

// NewDaemonState creates a new DaemonState instance (Issue #71).
// drainWindowSeconds configures the startup drain window during which
// IsSessionEnabled returns true for all sessions (#217).
func NewDaemonState(drainWindowSeconds float64, contextID string) *DaemonState {
	return &DaemonState{
		contextID:                     contextID,
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
		swallowedRetryCount:           make(map[string]int),
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

// mergePhonyNodes inserts phony NodeInfo entries from registry into nodes.
// Keys are bare node names (no session prefix) so dispatchPhonyNode can match
// the raw to: value before ResolveNodeName is called (#306).
func mergePhonyNodes(nodes map[string]discovery.NodeInfo, registry *binding.BindingRegistry) {
	if registry == nil {
		return
	}
	for _, b := range registry.Bindings {
		nodes[b.NodeName] = discovery.NodeInfo{IsPhony: true}
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
	configPaths []string,
	nodesDirs []string,
	daemonState *DaemonState,
	idleTracker *idle.IdleTracker,
	alertRateLimiter *alert.AlertRateLimiter,
	sharedNodes *atomic.Pointer[map[string]discovery.NodeInfo],
	selfSession string,
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

	// Issue #321: Track claimed panes to avoid redundant set-option execs.
	// Keyed on PaneID (e.g., "%31"). Phony nodes and empty PaneIDs are never
	// added. Pruned on every scan tick to remove stale entries.
	claimedPanes := make(map[string]bool)

	// Issue #94: Track previous pane states to avoid spam
	var prevPaneStatesJSON string

	// Issue #117: Track previous node and session state to avoid spam
	prevNodeCount := 0
	prevSessionNames := []string{}            // detects session renames (replaces prevSessionCount)
	prevSessionNodes := map[string][]string{} // detects node joins/leaves within sessions

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

	// Issue #352: Warn if alert system is effectively disabled at startup.
	warnAlertConfig(cfg, events)

	// #306: Load binding registry for phony node dispatch; nil = disabled.
	var registry *binding.BindingRegistry
	if cfg.BindingsPath != "" {
		if reg, loadErr := binding.Load(cfg.BindingsPath, binding.AllowEmptySenders()); loadErr != nil {
			log.Printf("postman: WARNING: failed to load bindings registry %s: %v\n", cfg.BindingsPath, loadErr)
		} else {
			registry = reg
		}
	}
	mergePhonyNodes(nodes, registry)

	for {
		select {
		case <-ctx.Done():
			// Clear all session-ON tmux options owned by this daemon.
			daemonState.enabledSessionsMu.RLock()
			for sessionName, on := range daemonState.enabledSessions {
				if on {
					_ = exec.Command("tmux", "set-option", "-gu", "@a2a_session_on_"+sessionName).Run()
				}
			}
			daemonState.enabledSessionsMu.RUnlock()
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
						if freshNodes, _, err := discovery.DiscoverNodesWithCollisions(baseDir, contextID, selfSession); err == nil {
							filterNodesByEdges(freshNodes, cfg.Edges)
							mergePhonyNodes(freshNodes, registry) // #306: inject phony nodes after edge filter
							// Issue #321: Claim only new, real panes (skip already-claimed).
							for _, nodeInfo := range freshNodes {
								if nodeInfo.IsPhony || nodeInfo.PaneID == "" {
									continue
								}
								if claimedPanes[nodeInfo.PaneID] {
									continue
								}
								claimCmd := exec.Command(
									"tmux", "set-option", "-p", "-t", nodeInfo.PaneID,
									"@a2a_context_id", contextID,
								)
								if err := claimCmd.Run(); err != nil {
									log.Printf(
										"postman: WARNING: failed to claim pane %s: %v\n",
										nodeInfo.PaneID, err,
									)
								} else {
									claimedPanes[nodeInfo.PaneID] = true
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

						// Deliver concurrently: SendToPane sleeps for enter_delay per pane;
						// parallel dispatch lets multiple panes receive simultaneously.
						// Mutable loop vars captured via function params to avoid races.
						go func(eventPath, filename string, nodes map[string]discovery.NodeInfo, registry *binding.BindingRegistry, adjacency map[string][]string, cfg *config.Config) {
							defer func() {
								if r := recover(); r != nil {
									log.Printf("🚨 PANIC in delivery goroutine for %s: %v\n", filename, r)
								}
							}()
							// Issue #53: Create wrapper channel for dead-letter notifications
							messageEvents := make(chan message.DaemonEvent, 1)
							if err := message.DeliverMessage(eventPath, contextID, nodes, registry, adjacency, cfg, daemonState.IsSessionEnabled, messageEvents, idleTracker, selfSession); err != nil {
								events <- tui.DaemonEvent{
									Type:    "error",
									Message: fmt.Sprintf("deliver %s: %v", filename, err),
								}
							} else {
								// Issue #53: Only terminal message-layer events suppress
								// the normal post-delivery bookkeeping and follow-up TUI
								// updates. Advisory success events should still pass
								// through while preserving the normal delivery path.
								suppressNormalDelivery := false
								select {
								case msgEvent := <-messageEvents:
									events <- tui.DaemonEvent{
										Type:    msgEvent.Type,
										Message: msgEvent.Message,
										Details: msgEvent.Details,
									}
									suppressNormalDelivery = messageEventSuppressesNormalDelivery(msgEvent)
								default:
									// No dead-letter event, normal delivery
								}

								// Issue #211: Record delivery timestamp for rate limiting
								if !suppressNormalDelivery {
									if msgInfo, parseErr := message.ParseMessageFilename(filename); parseErr == nil {
										deliveryKey := msgInfo.From + ":" + msgInfo.To
										daemonState.lastDeliveryMu.Lock()
										daemonState.lastDeliveryBySenderRecipient[deliveryKey] = time.Now()
										daemonState.lastDeliveryMu.Unlock()

									}
								}

								// Send normal delivery event only if not dead-lettered
								if !suppressNormalDelivery {
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
								if !suppressNormalDelivery {
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
						}(eventPath, filename, nodes, registry, adjacency, cfg)
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
								// Create waiting file only for explicit reply-tracked reads or ui_node prompts.
								if info.From != "postman" && info.From != "daemon" {
									waitingDir := filepath.Join(sourceSessionDir, "waiting")
									waitingFile := filepath.Join(waitingDir, filename)
									readContent, readErr := os.ReadFile(eventPath)
									if readErr != nil {
										log.Printf("postman: WARNING: failed to inspect read file %s for waiting semantics: %v\n", eventPath, readErr)
									} else if waitingContent, ok := waitingFileContentForRead(info, readContent, cfg, time.Now()); ok {
										if writeErr := os.WriteFile(waitingFile, []byte(waitingContent), 0o600); writeErr != nil {
											log.Printf("postman: WARNING: failed to create waiting file %s: %v\n", waitingFile, writeErr)
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
			isConfigEvent := false
			for _, watchedConfigPath := range configPaths {
				if eventPath == watchedConfigPath {
					isConfigEvent = true
					break
				}
			}
			isNodesDirEvent := false
			for _, nodesDir := range nodesDirs {
				if strings.HasPrefix(eventPath, nodesDir+string(filepath.Separator)) {
					isNodesDirEvent = true
					break
				}
			}
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

						// #306: Reload binding registry on config change.
						if newCfg.BindingsPath != "" {
							if reg, loadErr := binding.Load(newCfg.BindingsPath, binding.AllowEmptySenders()); loadErr != nil {
								log.Printf("postman: WARNING: failed to reload bindings registry %s: %v\n", newCfg.BindingsPath, loadErr)
							} else {
								registry = reg
							}
						} else {
							registry = nil
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
			freshNodes, scanCollisions, err := discovery.DiscoverNodesWithCollisions(baseDir, contextID, selfSession)
			if err != nil {
				continue
			}
			filterNodesByEdges(freshNodes, cfg.Edges)
			mergePhonyNodes(freshNodes, registry) // #306: inject phony nodes after edge filter
			// Issue #321: Prune stale claimedPanes entries (panes no longer in freshNodes).
			livePaneIDs := make(map[string]bool, len(freshNodes))
			for _, ni := range freshNodes {
				if !ni.IsPhony && ni.PaneID != "" {
					livePaneIDs[ni.PaneID] = true
				}
			}
			for pid := range claimedPanes {
				if !livePaneIDs[pid] {
					delete(claimedPanes, pid)
				}
			}
			// Claim only new, real panes with this daemon's context ID.
			for _, nodeInfo := range freshNodes {
				if nodeInfo.IsPhony || nodeInfo.PaneID == "" {
					continue
				}
				if claimedPanes[nodeInfo.PaneID] {
					continue
				}
				claimCmd := exec.Command(
					"tmux", "set-option", "-p", "-t", nodeInfo.PaneID,
					"@a2a_context_id", contextID,
				)
				if err := claimCmd.Run(); err != nil {
					log.Printf(
						"postman: WARNING: failed to claim pane %s: %v\n",
						nodeInfo.PaneID, err,
					)
				} else {
					claimedPanes[nodeInfo.PaneID] = true
				}
			}
			for _, collision := range scanCollisions {
				alertKey := "pane_collision:" + collision.WinnerPaneID + ":" + collision.LoserPaneID
				if daemonState.ShouldSendAlert(alertKey, 300) {
					events <- tui.DaemonEvent{
						Type:    "pane_collision",
						Message: fmt.Sprintf("[COLLISION] %s: %s displaced by %s", collision.NodeKey, collision.LoserPaneID, collision.WinnerPaneID),
						Details: map[string]interface{}{
							"node":           collision.NodeKey,
							"winner_pane_id": collision.WinnerPaneID,
							"loser_pane_id":  collision.LoserPaneID,
						},
					}
					daemonState.MarkAlertSent(alertKey)
				}
			}

			// Detect new nodes
			for nodeName, nodeInfo := range freshNodes {
				if !knownNodes[nodeName] {
					knownNodes[nodeName] = true

					// Issue #320: auto-enable session on first node discovery so the
					// session stays enabled after the startup drain window expires.
					daemonState.AutoEnableSessionIfNew(nodeInfo.SessionName)

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
			// Build sorted name slice for rename detection.
			currentNames := make([]string, len(sessionList))
			for i, s := range sessionList {
				currentNames[i] = s.Name
			}
			sort.Strings(currentNames)
			// Build normalized session_nodes snapshot (per-session sorted node lists).
			// Sorting neutralizes non-deterministic map/append iteration order.
			currentNodes := make(map[string][]string, len(sessionNodes))
			for sess, ns := range sessionNodes {
				sorted := make([]string, len(ns))
				copy(sorted, ns)
				sort.Strings(sorted)
				currentNodes[sess] = sorted
			}
			namesChanged := len(currentNames) != len(prevSessionNames)
			if !namesChanged {
				for i := range currentNames {
					if currentNames[i] != prevSessionNames[i] {
						namesChanged = true
						break
					}
				}
			}
			nodesChanged := len(currentNodes) != len(prevSessionNodes)
			if !nodesChanged {
				for sess, ns := range currentNodes {
					prev, ok := prevSessionNodes[sess]
					if !ok || len(ns) != len(prev) {
						nodesChanged = true
						break
					}
					for i := range ns {
						if ns[i] != prev[i] {
							nodesChanged = true
							break
						}
					}
					if nodesChanged {
						break
					}
				}
			}
			if len(nodes) != prevNodeCount || namesChanged || nodesChanged {
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
				prevSessionNames = currentNames
				prevSessionNodes = currentNodes
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
				eventMessage := template.ExpandTemplate(eventTemplate, vars, timeout, cfg.AllowShellForDroppedBallEventTemplate())

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
			checkSwallowedMessages(nodes, cfg, events, contextID, adjacency, idleTracker, daemonState)
			// Issue #283: Emit live unread inbox counts for routing-view display.
			events <- tui.DaemonEvent{
				Type: "inbox_unread_count_update",
				Details: map[string]interface{}{
					"unread_counts": scanLiveInboxCounts(nodes),
				},
			}

			// Update waiting file states based on current pane activity
			paneStatus := idleTracker.GetPaneActivityStatus(cfg)
			now := time.Now()
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
					if updated, changed := advanceWaitingState(contentStr, paneState, now, idleThreshold, spinningThreshold, spinningEnabled); changed {
						_ = os.WriteFile(filePath, []byte(updated), 0o600)
					}
				}
			}
			// Collect waiting states for TUI color display (second pass, post-transition)
			waitingStates := make(map[string]string)
			worstStatePriority := map[string]int{
				"user_input": 0,
				"pending":    1,
				"composing":  2,
				"spinning":   3,
				"stalled":    4,
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
					fileState := visibleWaitingState(string(wContent))
					if fileState == "" {
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
			// Collect pending states (inbox/ messages not yet archived)
			for k, v := range collectPendingStates(nodes, worstStatePriority) {
				if worstStatePriority[v] >= worstStatePriority[waitingStates[k]] {
					waitingStates[k] = v
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
// Uses DaemonMessageTemplate with two-pass expansion (BuildEnvelope + Pass 2).
func sendAlertToUINode(sessionDir, contextID, uiNode, body, alertType string, cfg *config.Config, adjacency map[string][]string, nodes map[string]discovery.NodeInfo) error {
	tmpl := cfg.DaemonMessageTemplate
	if tmpl == "" {
		return nil // no template configured; silent no-op
	}
	sourceSessionName := filepath.Base(filepath.Dir(sessionDir))
	now := time.Now()
	ts := fmt.Sprintf("%s-%d", now.Format("20060102-150405"), now.UnixNano()%1000000)
	filename := fmt.Sprintf("%s-from-daemon-to-%s.md", ts, uiNode)
	postPath := filepath.Join(sessionDir, "post", filename)
	scaffolded := envelope.BuildDaemonEnvelope(
		cfg, tmpl, uiNode, "daemon",
		contextID, postPath,
		nil, adjacency, nodes, sourceSessionName,
		nil,
	)
	content := template.ExpandVariables(scaffolded, map[string]string{
		"message_type": "alert",
		"heading":      "Alert: " + alertType,
		"alert_type":   alertType,
		"message":      body,
		"role_content": envelope.BuildRoleContent(cfg, uiNode),
	})
	return os.WriteFile(postPath, []byte(content), 0o600)
}

// collectPendingStates scans inbox/ directories for unarchived messages
// and returns a map of sessionName:nodeName -> "pending" for nodes with messages
// waiting in their inbox. Only applies when the node has no worse waiting-file state.
func collectPendingStates(nodes map[string]discovery.NodeInfo, priority map[string]int) map[string]string {
	result := make(map[string]string)
	for nodeKey, nodeInfo := range nodes {
		parts := strings.SplitN(nodeKey, ":", 2)
		if len(parts) != 2 {
			continue
		}
		nodeName := parts[1]
		inboxDir := filepath.Join(nodeInfo.SessionDir, "inbox", nodeName)
		entries, err := os.ReadDir(inboxDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".md") {
				if priority["pending"] >= priority[result[nodeKey]] {
					result[nodeKey] = "pending"
				}
				break
			}
		}
	}
	return result
}

// warnAlertConfig emits a log warning and TUI event when the alert system is
// effectively disabled due to missing ui_node or zero timeout values.
// This is observability-only: no behavior is changed. Issue #352.
func warnAlertConfig(cfg *config.Config, events chan<- tui.DaemonEvent) {
	if cfg.UINode == "" {
		msg := "postman: WARNING: alert system disabled: ui_node is not set. " +
			"Set ui_node in postman.toml or postman.md to enable inbox-stagnation, " +
			"node-inactivity, and unreplied-message alerts."
		log.Print(msg)
		events <- tui.DaemonEvent{Type: "alert_config_warning", Message: msg}
		return
	}
	// cfg.NodeDefaults applies to all nodes; treat non-zero defaults as active.
	hasActiveTimeout := cfg.NodeDefaults.IdleTimeoutSeconds > 0 ||
		cfg.NodeDefaults.DroppedBallTimeoutSeconds > 0
	if !hasActiveTimeout {
		for _, node := range cfg.Nodes {
			if node.IdleTimeoutSeconds > 0 || node.DroppedBallTimeoutSeconds > 0 {
				hasActiveTimeout = true
				break
			}
		}
	}
	if !hasActiveTimeout {
		msg := "postman: WARNING: alert system partially disabled: no nodes have " +
			"idle_timeout_seconds or dropped_ball_timeout_seconds set. " +
			"Node-inactivity and unreplied-message alerts will not fire."
		log.Print(msg)
		events <- tui.DaemonEvent{Type: "alert_config_warning", Message: msg}
	}
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
				"nix run github:i9wa4/tmux-a2a-postman -- send-message --context-id %s --to %s --body \"<your reply>\"",
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
// Used to update the TUI unread inbox depth display with live data (Issue #283).
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
				"nix run github:i9wa4/tmux-a2a-postman -- send-message --context-id %s --to %s --body \"<your reply>\"",
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
				"nix run github:i9wa4/tmux-a2a-postman -- send-message --context-id %s --to %s --body \"<your reply>\"",
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

// checkSwallowedMessages detects inbox messages likely swallowed by a busy agent pane
// and re-delivers the notification. Detection: inbox file older than delivery_idle_timeout_seconds
// AND pane idle AND node has not sent since file landed in inbox. Issue #282.
func checkSwallowedMessages(
	nodes map[string]discovery.NodeInfo,
	cfg *config.Config,
	events chan<- tui.DaemonEvent,
	contextID string,
	adjacency map[string][]string,
	idleTracker *idle.IdleTracker,
	daemonState *DaemonState,
) {
	paneStatus := idleTracker.GetPaneActivityStatus(cfg)
	livenessMap := idleTracker.GetLivenessMap()

	for nodeKey, nodeInfo := range nodes {
		simpleName := nodeKey
		if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
			simpleName = parts[1]
		}

		nodeCfg := cfg.GetNodeConfig(simpleName)
		if nodeCfg.DeliveryIdleTimeoutSeconds <= 0 {
			continue
		}

		retryMax := nodeCfg.DeliveryIdleRetryMax
		if retryMax <= 0 {
			retryMax = 3
		}

		paneState := paneStatus[nodeInfo.PaneID]
		if paneState != "idle" && paneState != "stale" {
			continue
		}

		timeout := time.Duration(nodeCfg.DeliveryIdleTimeoutSeconds * float64(time.Second))
		inboxDir := filepath.Join(nodeInfo.SessionDir, "inbox", simpleName)
		entries, err := os.ReadDir(inboxDir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			fileInfo, parseErr := message.ParseMessageFilename(entry.Name())
			if parseErr != nil {
				continue
			}
			if fileInfo.From == "postman" || fileInfo.From == "daemon" {
				continue
			}

			entryInfo, infoErr := entry.Info()
			if infoErr != nil {
				continue
			}
			deliveryTime := entryInfo.ModTime()

			if time.Since(deliveryTime) < timeout {
				continue
			}

			if daemonState.hasNodeSentSince(simpleName, deliveryTime) {
				continue
			}

			inboxPath := filepath.Join(inboxDir, entry.Name())
			daemonState.swallowedRetryCountMu.Lock()
			count := daemonState.swallowedRetryCount[inboxPath]
			daemonState.swallowedRetryCountMu.Unlock()
			if count >= retryMax {
				continue
			}

			notificationMsg := notification.BuildNotification(
				cfg, adjacency, nodes, contextID,
				simpleName, fileInfo.From,
				nodeInfo.SessionName, entry.Name(),
				livenessMap,
			)
			enterDelay := time.Duration(cfg.EnterDelay * float64(time.Second))
			if nodeCfg.EnterDelay != 0 {
				enterDelay = time.Duration(nodeCfg.EnterDelay * float64(time.Second))
			}
			tmuxTimeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
			enterCount := nodeCfg.EnterCount
			if enterCount == 0 {
				enterCount = 1
			}

			verifyDelay := time.Duration(cfg.EnterVerifyDelay * float64(time.Second))
			_ = notification.SendToPane(nodeInfo.PaneID, notificationMsg, enterDelay, tmuxTimeout, enterCount, true, verifyDelay, cfg.EnterRetryMax)

			daemonState.swallowedRetryCountMu.Lock()
			daemonState.swallowedRetryCount[inboxPath]++
			daemonState.swallowedRetryCountMu.Unlock()

			log.Printf("postman: swallowed message re-delivered to %s: %s (attempt %d/%d)\n",
				simpleName, entry.Name(), count+1, retryMax)
			events <- tui.DaemonEvent{
				Type:    "swallowed_redelivery",
				Message: fmt.Sprintf("Re-delivered to %s: %s (attempt %d/%d)", simpleName, entry.Name(), count+1, retryMax),
				Details: map[string]interface{}{
					"node":    nodeKey,
					"file":    entry.Name(),
					"attempt": count + 1,
					"max":     retryMax,
				},
			}
		}
	}
}

// SetSessionEnabled sets the enabled/disabled state for a session (Issue #71).
func (ds *DaemonState) SetSessionEnabled(sessionName string, enabled bool) {
	ds.enabledSessionsMu.Lock()
	ds.enabledSessions[sessionName] = enabled
	ds.enabledSessionsMu.Unlock()
	log.Printf("postman: session state change: session=%s enabled=%v source=toggle ts=%s\n",
		sessionName, enabled, time.Now().UTC().Format(time.RFC3339Nano))
	// Persist cross-daemon state in tmux server option (best-effort).
	key := "@a2a_session_on_" + sessionName
	if enabled {
		val := ds.contextID + ":" + strconv.Itoa(os.Getpid())
		_ = exec.Command("tmux", "set-option", "-g", key, val).Run()
	} else {
		_ = exec.Command("tmux", "set-option", "-gu", key).Run()
	}
}

// AutoEnableSessionIfNew enables a session if it has never been configured (Issue #91).
// Called on first discovery of a new pane to allow auto-PING without TUI intervention.
// Does nothing if the session is already tracked (operator's explicit state is preserved).
func (ds *DaemonState) AutoEnableSessionIfNew(sessionName string) {
	ds.enabledSessionsMu.Lock()
	defer ds.enabledSessionsMu.Unlock()
	if _, exists := ds.enabledSessions[sessionName]; !exists {
		ds.enabledSessions[sessionName] = true
		log.Printf("postman: session state change: session=%s enabled=true source=auto-enable ts=%s\n",
			sessionName, time.Now().UTC().Format(time.RFC3339Nano))
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

// hasNodeSentSince returns true if the node has sent a message after the given time.
// Issue #282: Used to detect swallowed deliveries.
func (ds *DaemonState) hasNodeSentSince(nodeName string, since time.Time) bool {
	ds.lastDeliveryMu.RLock()
	defer ds.lastDeliveryMu.RUnlock()
	prefix := nodeName + ":"
	for key, t := range ds.lastDeliveryBySenderRecipient {
		if strings.HasPrefix(key, prefix) && t.After(since) {
			return true
		}
	}
	return false
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

func messageEventSuppressesNormalDelivery(event message.DaemonEvent) bool {
	return event.Type == "message_received" && strings.HasPrefix(event.Message, "Dead-letter:")
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
				if entries, readErr := os.ReadDir(waitingDir); readErr == nil {
					requeueCount := 0
					deadLetterCount := 0
					for _, e := range entries {
						if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
							continue
						}
						if !strings.Contains(e.Name(), "-to-"+simpleName) {
							continue
						}
						if err := requeueWaitingMessage(nodeInfo.SessionDir, simpleName, e.Name()); err != nil {
							continue
						}
						deadLetterPath := filepath.Join(nodeInfo.SessionDir, "dead-letter", deadLetterMissingOriginalName(e.Name()))
						if _, err := os.Stat(deadLetterPath); err == nil {
							deadLetterCount++
						} else {
							requeueCount++
						}
					}
					if requeueCount > 0 {
						log.Printf("postman: pane restart requeued %d waiting/ files for %s\n", requeueCount, nodeKey)
					}
					if deadLetterCount > 0 {
						log.Printf("postman: pane restart dead-lettered %d waiting/ files for %s (missing original artifact)\n", deadLetterCount, nodeKey)
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

func requeueWaitingMessage(sessionDir, simpleName, filename string) error {
	waitingPath := filepath.Join(sessionDir, "waiting", filename)
	inboxDir := filepath.Join(sessionDir, "inbox", simpleName)
	inboxPath := filepath.Join(inboxDir, filename)
	readPath := filepath.Join(sessionDir, "read", filename)

	if _, err := os.Stat(inboxPath); err == nil {
		return os.Remove(waitingPath)
	} else if !os.IsNotExist(err) {
		return err
	}

	if data, err := os.ReadFile(readPath); err == nil {
		if err := os.MkdirAll(inboxDir, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(inboxPath, data, 0o600); err != nil {
			return err
		}
		return os.Remove(waitingPath)
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(filepath.Join(sessionDir, "dead-letter"), 0o700); err != nil {
		return err
	}
	return os.Rename(waitingPath, filepath.Join(sessionDir, "dead-letter", deadLetterMissingOriginalName(filename)))
}

func deadLetterMissingOriginalName(filename string) string {
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	return base + "-dl-missing-original" + ext
}
