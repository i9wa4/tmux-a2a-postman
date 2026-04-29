package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/i9wa4/tmux-a2a-postman/internal/alert"
	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/ping"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/reminder"
	"github.com/i9wa4/tmux-a2a-postman/internal/session"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
	"github.com/i9wa4/tmux-a2a-postman/internal/uinode"
)

type daemonRuntime struct {
	baseDir       string
	sessionDir    string
	contextID     string
	configPath    string
	selfSession   string
	cfg           *config.Config
	watcher       *fsnotify.Watcher
	adjacency     map[string][]string
	nodes         map[string]discovery.NodeInfo
	knownNodes    map[string]bool
	reminderState *reminder.ReminderState
	events        chan<- tui.DaemonEvent
	configPaths   []string
	nodesDirs     []string
	daemonState   *DaemonState
	idleTracker   *idle.IdleTracker

	alertRateLimiter *alert.AlertRateLimiter
	sharedNodes      *atomic.Pointer[map[string]discovery.NodeInfo]

	configTimer        *time.Timer
	watchedDirs        map[string]bool
	claimedPanes       map[string]bool
	prevPaneStatesJSON string
	prevNodeCount      int
	prevSessionNames   []string
	prevSessionNodes   map[string][]string

	alertDeliverySignalState string
	registry                 *binding.BindingRegistry
	bindingWatchDirs         []string
	postEventsMu             sync.Mutex
	activePostEvents         map[string]bool
}

type runtimeStatusSnapshot struct {
	NodeCount              int
	Sessions               []tui.SessionInfo
	SessionNodes           map[string][]string
	NormalizedSessionNames []string
	NormalizedSessionNodes map[string][]string
}

func newDaemonRuntime(
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
) *daemonRuntime {
	return &daemonRuntime{
		baseDir:          baseDir,
		sessionDir:       sessionDir,
		contextID:        contextID,
		configPath:       configPath,
		selfSession:      selfSession,
		cfg:              cfg,
		watcher:          watcher,
		adjacency:        adjacency,
		nodes:            nodes,
		knownNodes:       knownNodes,
		reminderState:    reminderState,
		events:           events,
		configPaths:      configPaths,
		nodesDirs:        nodesDirs,
		daemonState:      daemonState,
		idleTracker:      idleTracker,
		alertRateLimiter: alertRateLimiter,
		sharedNodes:      sharedNodes,
		watchedDirs:      make(map[string]bool),
		claimedPanes:     make(map[string]bool),
		prevSessionNodes: make(map[string][]string),
		activePostEvents: make(map[string]bool),
	}
}

func buildRuntimeStatusSnapshot(nodes map[string]discovery.NodeInfo, allSessions []string, isSessionEnabled func(string) bool) runtimeStatusSnapshot {
	sessionNodes := make(map[string][]string)
	for nodeName := range nodes {
		parts := strings.SplitN(nodeName, ":", 2)
		if len(parts) != 2 {
			continue
		}
		sessionNodes[parts[0]] = append(sessionNodes[parts[0]], parts[1])
	}

	normalizedSessionNames := make([]string, 0, len(allSessions))
	sessionNameSet := make(map[string]bool)
	for _, sessionName := range allSessions {
		if sessionNameSet[sessionName] {
			continue
		}
		sessionNameSet[sessionName] = true
		normalizedSessionNames = append(normalizedSessionNames, sessionName)
	}
	for nodeName := range nodes {
		parts := strings.SplitN(nodeName, ":", 2)
		if len(parts) != 2 || sessionNameSet[parts[0]] {
			continue
		}
		sessionNameSet[parts[0]] = true
		normalizedSessionNames = append(normalizedSessionNames, parts[0])
	}
	sort.Strings(normalizedSessionNames)

	normalizedSessionNodes := make(map[string][]string, len(sessionNodes))
	for sessionName, nodeNames := range sessionNodes {
		sortedNodeNames := make([]string, len(nodeNames))
		copy(sortedNodeNames, nodeNames)
		sort.Strings(sortedNodeNames)
		normalizedSessionNodes[sessionName] = sortedNodeNames
	}

	return runtimeStatusSnapshot{
		NodeCount:              len(nodes),
		Sessions:               session.BuildSessionList(nodes, allSessions, isSessionEnabled),
		SessionNodes:           sessionNodes,
		NormalizedSessionNames: normalizedSessionNames,
		NormalizedSessionNodes: normalizedSessionNodes,
	}
}

func (snapshot runtimeStatusSnapshot) changed(prevNodeCount int, prevSessionNames []string, prevSessionNodes map[string][]string) bool {
	if snapshot.NodeCount != prevNodeCount {
		return true
	}
	if len(snapshot.NormalizedSessionNames) != len(prevSessionNames) {
		return true
	}
	for i := range snapshot.NormalizedSessionNames {
		if snapshot.NormalizedSessionNames[i] != prevSessionNames[i] {
			return true
		}
	}
	if len(snapshot.NormalizedSessionNodes) != len(prevSessionNodes) {
		return true
	}
	for sessionName, nodeNames := range snapshot.NormalizedSessionNodes {
		prevNodeNames, ok := prevSessionNodes[sessionName]
		if !ok || len(nodeNames) != len(prevNodeNames) {
			return true
		}
		for i := range nodeNames {
			if nodeNames[i] != prevNodeNames[i] {
				return true
			}
		}
	}
	return false
}

func runtimeSessionDirs(primarySessionDir string, nodes map[string]discovery.NodeInfo) []string {
	seen := make(map[string]bool)
	sessionDirs := make([]string, 0, len(nodes)+1)
	appendDir := func(sessionDir string) {
		if sessionDir == "" || seen[sessionDir] {
			return
		}
		seen[sessionDir] = true
		sessionDirs = append(sessionDirs, sessionDir)
	}

	appendDir(primarySessionDir)
	nodeNames := make([]string, 0, len(nodes))
	for nodeName := range nodes {
		nodeNames = append(nodeNames, nodeName)
	}
	sort.Strings(nodeNames)
	for _, nodeName := range nodeNames {
		appendDir(nodes[nodeName].SessionDir)
	}
	sort.Strings(sessionDirs)
	return sessionDirs
}

func resumeCompatibilityMailboxProjections(primarySessionDir string, nodes map[string]discovery.NodeInfo) error {
	for _, sessionDir := range runtimeSessionDirs(primarySessionDir, nodes) {
		if err := projection.SyncCompatibilityMailbox(sessionDir); err != nil {
			return fmt.Errorf("sync compatibility mailbox %s: %w", sessionDir, err)
		}
	}
	return nil
}

func (rt *daemonRuntime) bootstrap(ctx context.Context) {
	if config.BoolVal(rt.cfg.Heartbeat.Enabled, false) && rt.cfg.Heartbeat.LLMNode != "" && rt.cfg.Heartbeat.IntervalSeconds > 0 {
		go startHeartbeatTrigger(ctx, rt.sharedNodes, rt.contextID, rt.cfg, rt.adjacency)
	}

	rt.alertDeliverySignalState = warnAlertConfig(rt.cfg, rt.nodes, rt.events)

	if watchDir := bindingsWatchDir(rt.cfg.BindingsPath); watchDir != "" {
		if updatedWatchDirs, watchErr := ensureWatchedPath(rt.bindingWatchDirs, watchDir, rt.watcher.Add); watchErr != nil {
			log.Printf("postman: WARNING: failed to watch bindings registry dir %s: %v\n", watchDir, watchErr)
		} else {
			rt.bindingWatchDirs = updatedWatchDirs
		}
	}
	if rt.cfg.BindingsPath != "" {
		if reg, loadErr := binding.Load(rt.cfg.BindingsPath, binding.AllowEmptySenders()); loadErr != nil {
			log.Printf("postman: WARNING: failed to load bindings registry %s: %v\n", rt.cfg.BindingsPath, loadErr)
		} else {
			rt.registry = reg
		}
	}
	mergePhonyNodes(rt.nodes, rt.registry)
	rt.storeSharedNodes()

	installShadowJournalManager(rt.sessionDir, rt.contextID, rt.selfSession, time.Now())
	if err := resumeCompatibilityMailboxProjections(rt.sessionDir, rt.nodes); err != nil {
		log.Printf("postman: WARNING: %v\n", err)
	}
	autoEnableSessions := config.BoolVal(rt.cfg.AutoEnableNewSessions, false)
	rt.dispatchPendingAutoPings(rt.nodes, autoEnableSessions, time.Now())
}

func (rt *daemonRuntime) handleContextDone() {
	rt.daemonState.enabledSessionsMu.RLock()
	for sessionName, enabled := range rt.daemonState.enabledSessions {
		if enabled {
			_ = exec.Command("tmux", "set-option", "-gu", "@a2a_session_on_"+sessionName).Run()
		}
	}
	rt.daemonState.enabledSessionsMu.RUnlock()

	rt.events <- tui.DaemonEvent{
		Type:    "channel_closed",
		Message: "Shutting down",
	}
}

func (rt *daemonRuntime) handleWatcherEvent(event fsnotify.Event) {
	eventPath := event.Name

	if filepath.Base(filepath.Dir(eventPath)) == "requests" && filepath.Base(filepath.Dir(filepath.Dir(eventPath))) == "compatibility-submit" {
		if event.Op&(fsnotify.Create|fsnotify.Rename) != 0 && strings.HasSuffix(filepath.Base(eventPath), ".json") {
			if err := processCompatibilitySubmitRequest(eventPath); err != nil {
				rt.events <- tui.DaemonEvent{
					Type:    "error",
					Message: fmt.Sprintf("compatibility submit %s: %v", filepath.Base(eventPath), err),
				}
			}
		}
	} else if strings.HasSuffix(filepath.Dir(eventPath), "post") {
		rt.handlePostWatcherEvent(eventPath, event.Op)
	} else if strings.HasSuffix(filepath.Dir(eventPath), "read") {
		rt.handleReadWatcherEvent(eventPath, event.Op)
	}

	isConfigEvent := false
	for _, watchedConfigPath := range rt.configPaths {
		if eventPath == watchedConfigPath {
			isConfigEvent = true
			break
		}
	}
	isNodesDirEvent := false
	for _, nodesDir := range rt.nodesDirs {
		if strings.HasPrefix(eventPath, nodesDir+string(filepath.Separator)) {
			isNodesDirEvent = true
			break
		}
	}
	isBindingsEvent := matchesBindingsEvent(eventPath, rt.cfg.BindingsPath)
	if isConfigEvent || isNodesDirEvent || isBindingsEvent {
		if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
			if rt.configTimer != nil {
				rt.configTimer.Stop()
			}
			rt.configTimer = safeAfterFunc(200*time.Millisecond, "config-reload", rt.events, func() {
				rt.handleConfigReload()
			})
		}
	}
}

func (rt *daemonRuntime) handlePostWatcherEvent(eventPath string, op fsnotify.Op) {
	if op&(fsnotify.Create|fsnotify.Rename) == 0 {
		return
	}
	filename := filepath.Base(eventPath)
	if !strings.HasSuffix(filename, ".md") {
		return
	}
	if !rt.beginPostEvent(eventPath) {
		return
	}
	if _, err := os.Stat(eventPath); os.IsNotExist(err) {
		rt.finishPostEvent(eventPath)
		return
	}

	recordShadowMailboxPathEvent(eventPath, "compatibility_mailbox_posted", journal.VisibilityCompatibilityMailbox, time.Now())
	sourceSessionDir := filepath.Dir(filepath.Dir(eventPath))
	syncCompatibilityMailboxProjection(sourceSessionDir)

	freshNodes, _, err := rt.discoverNodes()
	if err == nil {
		rt.claimNewPanes(freshNodes)
		newNodes := rt.detectNewNodes(freshNodes)
		rt.recordPendingAutoPings(newNodes, freshNodes, "discovered", time.Now())
		rt.logPaneIDChanges(freshNodes)
		rt.nodes = freshNodes
		rt.storeSharedNodes()
		rt.dispatchPendingAutoPings(freshNodes, config.BoolVal(rt.cfg.AutoEnableNewSessions, false), time.Now())

		allSessions, _ := discovery.DiscoverAllSessions()
		if allSessions == nil {
			allSessions = []string{}
		}
		snapshot := buildRuntimeStatusSnapshot(rt.nodes, allSessions, rt.daemonState.GetConfiguredSessionEnabled)
		rt.events <- tui.DaemonEvent{
			Type:    "status_update",
			Message: "Running",
			Details: map[string]interface{}{
				"node_count":    snapshot.NodeCount,
				"sessions":      snapshot.Sessions,
				"session_nodes": snapshot.SessionNodes,
			},
		}
	}

	if rt.cfg.MinDeliveryGapSeconds > 0 {
		if msgInfo, parseErr := message.ParseMessageFilename(filename); parseErr == nil {
			deliveryKey := msgInfo.From + ":" + msgInfo.To
			gap := time.Duration(rt.cfg.MinDeliveryGapSeconds * float64(time.Second))
			rt.daemonState.lastDeliveryMu.RLock()
			lastTime, exists := rt.daemonState.lastDeliveryBySenderRecipient[deliveryKey]
			rt.daemonState.lastDeliveryMu.RUnlock()
			if exists && time.Since(lastTime) < gap {
				log.Printf("postman: rate-limited delivery %s -> %s (gap: %.1fs)\n", msgInfo.From, msgInfo.To, rt.cfg.MinDeliveryGapSeconds)
				rt.finishPostEvent(eventPath)
				return
			}
		}
	}

	go func(eventPath, filename string, nodes map[string]discovery.NodeInfo, registry *binding.BindingRegistry, adjacency map[string][]string, cfg *config.Config) {
		defer rt.finishPostEvent(eventPath)
		defer func() {
			if r := recover(); r != nil {
				log.Printf("🚨 PANIC in delivery goroutine for %s: %v\n", filename, r)
			}
		}()

		messageEvents := make(chan message.DaemonEvent, 1)
		if msgInfo, parseErr := message.ParseMessageFilename(filename); parseErr == nil {
			log.Printf("postman: deliver: picked up %s -> %s (file=%s)\n", msgInfo.From, msgInfo.To, filename)
		}
		if err := message.DeliverMessage(eventPath, rt.contextID, nodes, registry, adjacency, cfg, rt.daemonState.IsSessionEnabled, messageEvents, rt.idleTracker, rt.selfSession); err != nil {
			rt.events <- tui.DaemonEvent{
				Type:    "error",
				Message: fmt.Sprintf("deliver %s: %v", filename, err),
			}
			return
		}

		sourceSessionDir := filepath.Dir(filepath.Dir(eventPath))
		sourceSessionName := filepath.Base(sourceSessionDir)
		syncCompatibilityMailboxProjection(sourceSessionDir)
		if info, parseErr := message.ParseMessageFilename(filename); parseErr == nil {
			recipientFullName := discovery.ResolveNodeName(info.To, sourceSessionName, nodes)
			if nodeInfo, ok := nodes[recipientFullName]; ok {
				syncCompatibilityMailboxProjection(nodeInfo.SessionDir)
			}
		}

		suppressNormalDelivery := false
		select {
		case msgEvent := <-messageEvents:
			rt.events <- tui.DaemonEvent{
				Type:    msgEvent.Type,
				Message: msgEvent.Message,
				Details: msgEvent.Details,
			}
			suppressNormalDelivery = messageEventSuppressesNormalDelivery(msgEvent)
		default:
		}

		if !suppressNormalDelivery {
			if msgInfo, parseErr := message.ParseMessageFilename(filename); parseErr == nil {
				deliveryKey := msgInfo.From + ":" + msgInfo.To
				rt.daemonState.lastDeliveryMu.Lock()
				rt.daemonState.lastDeliveryBySenderRecipient[deliveryKey] = time.Now()
				rt.daemonState.lastDeliveryMu.Unlock()
			}
		}

		if !suppressNormalDelivery {
			senderSessionDir := filepath.Dir(filepath.Dir(eventPath))
			clearedWaiting := false
			if senderInfo, parseErr := message.ParseMessageFilename(filename); parseErr == nil {
				waitingDir := filepath.Join(senderSessionDir, "waiting")
				pattern := filepath.Join(waitingDir, "*-to-"+senderInfo.From+".md")
				if matches, globErr := filepath.Glob(pattern); globErr == nil {
					for _, match := range matches {
						waitingInfo, waitingParseErr := message.ParseMessageFilename(filepath.Base(match))
						waitingContent, readErr := os.ReadFile(match)
						if readErr != nil && !os.IsNotExist(readErr) {
							log.Printf("postman: WARNING: failed to read waiting file %s: %v\n", match, readErr)
						}
						if removeErr := os.Remove(match); removeErr != nil {
							log.Printf("postman: WARNING: failed to remove waiting file %s: %v\n", match, removeErr)
							continue
						}
						from := ""
						to := senderInfo.From
						if waitingParseErr == nil {
							from = waitingInfo.From
							to = waitingInfo.To
						}
						recordCompatibilityMailboxPayload(senderSessionDir, filepath.Base(senderSessionDir), "compatibility_mailbox_waiting_cleared", journal.VisibilityOperatorVisible, journal.MailboxEventPayload{
							MessageID: filepath.Base(match),
							From:      from,
							To:        to,
							Path:      shadowRelativePath(senderSessionDir, match),
							Content:   string(waitingContent),
						})
						clearedWaiting = true
					}
				}
			}
			if clearedWaiting {
				syncCompatibilityMailboxProjection(senderSessionDir)
			}

			rt.events <- tui.DaemonEvent{
				Type:    "message_received",
				Message: fmt.Sprintf("Delivered: %s", filename),
				Details: map[string]interface{}{
					"session": sourceSessionName,
				},
			}
		}

		if !suppressNormalDelivery {
			if info, err := message.ParseMessageFilename(filename); err == nil {
				rt.daemonState.RecordEdgeActivity(info.From, info.To, time.Now())

				edgeList := rt.daemonState.BuildEdgeList(cfg.Edges, cfg)
				rt.events <- tui.DaemonEvent{
					Type: "edge_update",
					Details: map[string]interface{}{
						"edges": edgeList,
					},
				}

				nodeStates := rt.idleTracker.GetNodeStates()
				rt.events <- tui.DaemonEvent{
					Type: "ball_state_update",
					Details: map[string]interface{}{
						"node_states": nodeStates,
					},
				}
			}
		}
	}(eventPath, filename, rt.nodes, rt.registry, rt.adjacency, rt.cfg)
}

func (rt *daemonRuntime) beginPostEvent(eventPath string) bool {
	rt.postEventsMu.Lock()
	defer rt.postEventsMu.Unlock()
	if rt.activePostEvents[eventPath] {
		return false
	}
	rt.activePostEvents[eventPath] = true
	return true
}

func (rt *daemonRuntime) finishPostEvent(eventPath string) {
	rt.postEventsMu.Lock()
	delete(rt.activePostEvents, eventPath)
	rt.postEventsMu.Unlock()
}

func (rt *daemonRuntime) handleReadWatcherEvent(eventPath string, op fsnotify.Op) {
	if op&(fsnotify.Create|fsnotify.Rename) == 0 {
		return
	}
	filename := filepath.Base(eventPath)
	if !strings.HasSuffix(filename, ".md") {
		return
	}

	info, err := message.ParseMessageFilename(filename)
	if err != nil {
		return
	}

	recordShadowMailboxPathEvent(eventPath, "compatibility_mailbox_read", journal.VisibilityOperatorVisible, time.Now())
	sourceSessionDir := filepath.Dir(filepath.Dir(eventPath))
	sourceSessionName := filepath.Base(sourceSessionDir)
	syncCompatibilityMailboxProjection(sourceSessionDir)

	if info.To == "postman" || info.To == "daemon" {
		return
	}

	prefixedKey := sourceSessionName + ":" + info.To
	if info.From != "postman" && info.From != "daemon" {
		waitingDir := filepath.Join(sourceSessionDir, "waiting")
		waitingFile := filepath.Join(waitingDir, filename)
		readContent, readErr := os.ReadFile(eventPath)
		if readErr != nil {
			log.Printf("postman: WARNING: failed to inspect read file %s for waiting semantics: %v\n", eventPath, readErr)
		} else if waitingContent, ok := waitingFileContentForRead(info, readContent, rt.cfg, time.Now()); ok {
			if writeErr := os.WriteFile(waitingFile, []byte(waitingContent), 0o600); writeErr != nil {
				log.Printf("postman: WARNING: failed to create waiting file %s: %v\n", waitingFile, writeErr)
			} else {
				recordCompatibilityMailboxPayload(sourceSessionDir, sourceSessionName, "compatibility_mailbox_waiting_created", journal.VisibilityOperatorVisible, journal.MailboxEventPayload{
					MessageID: filename,
					From:      info.From,
					To:        info.To,
					Path:      shadowRelativePath(sourceSessionDir, waitingFile),
					Content:   waitingContent,
				})
				syncCompatibilityMailboxProjection(sourceSessionDir)
			}
		}
	}

	rt.idleTracker.MarkNodeAlive(prefixedKey)
	rt.events <- tui.DaemonEvent{
		Type: "node_alive",
		Details: map[string]interface{}{
			"node":   prefixedKey,
			"source": "read_move",
		},
	}
	if reminderShouldIncrement(info.From) {
		rt.reminderState.Increment(info.To, sourceSessionName, rt.nodes, rt.cfg)
	}
}

func (rt *daemonRuntime) handleConfigReload() {
	newCfg, err := config.LoadConfig(rt.configPath)
	if err != nil {
		rt.events <- tui.DaemonEvent{
			Type:    "error",
			Message: fmt.Sprintf("config reload failed: %v", err),
		}
		return
	}

	newAdjacency, err := config.ParseEdges(newCfg.Edges)
	if err != nil {
		rt.events <- tui.DaemonEvent{
			Type:    "error",
			Message: fmt.Sprintf("edge parsing failed: %v", err),
		}
		return
	}

	rt.cfg = newCfg
	rt.adjacency = newAdjacency
	if updatedBindingWatchDirs, watchErr := ensureWatchedPath(rt.bindingWatchDirs, bindingsWatchDir(newCfg.BindingsPath), rt.watcher.Add); watchErr != nil {
		log.Printf("postman: WARNING: failed to watch bindings registry dir %s: %v\n", bindingsWatchDir(newCfg.BindingsPath), watchErr)
	} else {
		rt.bindingWatchDirs = updatedBindingWatchDirs
	}

	if newCfg.BindingsPath != "" {
		if reg, loadErr := binding.Load(newCfg.BindingsPath, binding.AllowEmptySenders()); loadErr != nil {
			log.Printf("postman: WARNING: failed to reload bindings registry %s: %v\n", newCfg.BindingsPath, loadErr)
			rt.registry = nil
		} else {
			rt.registry = reg
		}
	} else {
		rt.registry = nil
	}

	if freshNodes, _, discErr := discovery.DiscoverNodesWithCollisions(rt.baseDir, rt.contextID, rt.selfSession); discErr != nil {
		log.Printf("postman: WARNING: failed to refresh nodes after config reload: %v\n", discErr)
		rt.nodes = refreshNodesWithRegistry(rt.nodes, rt.registry)
	} else {
		filterNodesByEdges(freshNodes, rt.cfg.Edges)
		mergePhonyNodes(freshNodes, rt.registry)
		rt.nodes = freshNodes
	}
	rt.storeSharedNodes()
	rt.alertDeliverySignalState = syncAlertDeliveryStatus(rt.alertDeliverySignalState, rt.cfg, rt.nodes, rt.events)

	allSessions, _ := discovery.DiscoverAllSessions()
	if allSessions == nil {
		allSessions = []string{}
	}
	snapshot := buildRuntimeStatusSnapshot(rt.nodes, allSessions, rt.daemonState.GetConfiguredSessionEnabled)

	edgeList := rt.daemonState.BuildEdgeList(newCfg.Edges, newCfg)
	rt.events <- tui.DaemonEvent{
		Type: "config_update",
		Details: map[string]interface{}{
			"edges":         edgeList,
			"sessions":      snapshot.Sessions,
			"session_nodes": snapshot.SessionNodes,
		},
	}
	rt.events <- tui.DaemonEvent{
		Type:    "message_received",
		Message: "Config reloaded",
	}
}

func (rt *daemonRuntime) handleWatcherError(err error) {
	rt.events <- tui.DaemonEvent{
		Type:    "error",
		Message: fmt.Sprintf("watcher error: %v", err),
	}
}

func (rt *daemonRuntime) handleScanTick() {
	freshNodes, scanCollisions, err := rt.discoverNodes()
	if err != nil {
		return
	}

	rt.pruneClaimedPanes(freshNodes)
	rt.claimNewPanes(freshNodes)
	for _, collision := range scanCollisions {
		alertKey := "pane_collision:" + collision.WinnerPaneID + ":" + collision.LoserPaneID
		if rt.daemonState.ShouldSendAlert(alertKey, 300) {
			rt.events <- tui.DaemonEvent{
				Type:    "pane_collision",
				Message: fmt.Sprintf("[COLLISION] %s: %s displaced by %s", collision.NodeKey, collision.LoserPaneID, collision.WinnerPaneID),
				Details: map[string]interface{}{
					"node":           collision.NodeKey,
					"winner_pane_id": collision.WinnerPaneID,
					"loser_pane_id":  collision.LoserPaneID,
				},
			}
			rt.daemonState.MarkAlertSent(alertKey)
		}
	}

	autoEnableSessions := config.BoolVal(rt.cfg.AutoEnableNewSessions, false)
	newNodes := rt.detectNewNodes(freshNodes)
	rt.recordPendingAutoPings(newNodes, freshNodes, "discovered", time.Now())
	rt.nodes = freshNodes
	rt.storeSharedNodes()
	rt.alertDeliverySignalState = syncAlertDeliveryStatus(rt.alertDeliverySignalState, rt.cfg, rt.nodes, rt.events)

	allSessions, err := discovery.DiscoverAllSessions()
	if err != nil {
		rt.events <- tui.DaemonEvent{
			Type:    "error",
			Message: fmt.Sprintf("failed to discover all sessions: %v", err),
		}
		allSessions = []string{}
	}

	snapshot := buildRuntimeStatusSnapshot(rt.nodes, allSessions, rt.daemonState.GetConfiguredSessionEnabled)
	if snapshot.changed(rt.prevNodeCount, rt.prevSessionNames, rt.prevSessionNodes) {
		rt.events <- tui.DaemonEvent{
			Type:    "status_update",
			Message: "Running",
			Details: map[string]interface{}{
				"node_count":    snapshot.NodeCount,
				"sessions":      snapshot.Sessions,
				"session_nodes": snapshot.SessionNodes,
			},
		}
		rt.prevNodeCount = snapshot.NodeCount
		rt.prevSessionNames = snapshot.NormalizedSessionNames
		rt.prevSessionNodes = snapshot.NormalizedSessionNodes
	}

	nodeConfigs := make(map[string]config.NodeConfig)
	for fullName := range rt.nodes {
		parts := strings.SplitN(fullName, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if nodeConfig, ok := rt.cfg.Nodes[parts[1]]; ok {
			nodeConfigs[parts[1]] = nodeConfig
		}
	}

	droppedNodes := rt.idleTracker.CheckDroppedBalls(nodeConfigs)
	for nodeKey, duration := range droppedNodes {
		rt.idleTracker.MarkDroppedBallNotified(nodeKey)

		simpleName := nodeKey
		if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
			simpleName = parts[1]
		}

		eventTemplate := rt.cfg.DroppedBallEventTemplate
		if eventTemplate == "" {
			eventTemplate = "Dropped ball: {node} (holding for {duration})"
		}
		timeout := time.Duration(rt.cfg.TmuxTimeout * float64(time.Second))
		eventMessage := template.ExpandTemplate(eventTemplate, map[string]string{
			"node":     nodeKey,
			"duration": duration.Round(time.Second).String(),
		}, timeout, rt.cfg.AllowShellForDroppedBallEventTemplate())

		rt.events <- tui.DaemonEvent{
			Type:    "dropped_ball",
			Message: eventMessage,
			Details: map[string]interface{}{
				"node":     nodeKey,
				"duration": duration.Seconds(),
			},
		}

		nodeConfig := nodeConfigs[simpleName]
		notification := nodeConfig.DroppedBallNotification
		if notification == "" {
			notification = "tui"
		}
		if notification == "display" || notification == "all" {
			_ = exec.Command("tmux", "display-message", eventMessage).Run()
		}
	}

	paneStates, err := uinode.GetAllPanesInfo()
	if err == nil {
		currentJSON, _ := json.Marshal(paneStates)
		currentJSONStr := string(currentJSON)
		if currentJSONStr != rt.prevPaneStatesJSON {
			paneToNode := make(map[string]string)
			for nodeKey, nodeInfo := range rt.nodes {
				paneToNode[nodeInfo.PaneID] = nodeKey
			}

			rt.events <- tui.DaemonEvent{
				Type:    "pane_state_update",
				Message: "Pane states updated",
				Details: map[string]interface{}{
					"pane_states":  paneStates,
					"pane_to_node": paneToNode,
				},
			}

			rt.daemonState.checkPaneDisappearance(paneStates, rt.daemonState.prevPaneToNode, rt.nodes, rt.events)
			restartedNodes := rt.daemonState.checkPaneRestarts(paneStates, paneToNode, rt.nodes, rt.cfg, rt.events, rt.contextID, rt.sessionDir, rt.adjacency, rt.idleTracker)
			rt.recordPendingAutoPings(restartedNodes, rt.nodes, "pane_restart", time.Now())
			rt.prevPaneStatesJSON = currentJSONStr
		}
	}

	rt.dispatchPendingAutoPings(freshNodes, autoEnableSessions, time.Now())

	droppedNodeMap := rt.idleTracker.GetCurrentlyDroppedNodes(nodeConfigs)
	nodeStates := rt.idleTracker.GetNodeStates()
	rt.events <- tui.DaemonEvent{
		Type:    "ball_state_update",
		Message: "Ball states updated",
		Details: map[string]interface{}{
			"node_states":   nodeStates,
			"dropped_nodes": droppedNodeMap,
		},
	}
}

func (rt *daemonRuntime) handleInboxCheckTick() {
	checkInboxStagnation(rt.nodes, rt.cfg, rt.events, rt.sessionDir, rt.contextID, rt.adjacency, rt.idleTracker, rt.alertRateLimiter, rt.daemonState)
	checkNodeInactivity(rt.nodes, rt.cfg, rt.events, rt.sessionDir, rt.contextID, rt.adjacency, rt.idleTracker, rt.alertRateLimiter)
	checkUnrepliedMessages(rt.nodes, rt.cfg, rt.events, rt.sessionDir, rt.contextID, rt.adjacency, rt.idleTracker, rt.alertRateLimiter, rt.daemonState)
	checkSwallowedMessages(rt.nodes, rt.cfg, rt.events, rt.contextID, rt.adjacency, rt.idleTracker, rt.daemonState)

	rt.events <- tui.DaemonEvent{
		Type: "inbox_unread_count_update",
		Details: map[string]interface{}{
			"unread_counts": scanLiveInboxCounts(rt.nodes),
		},
	}

	paneStatus := rt.idleTracker.GetPaneActivityStatus(rt.cfg)
	now := time.Now()
	idleThreshold := time.Duration(rt.cfg.NodeIdleSeconds * float64(time.Second))
	spinningEnabled := rt.cfg.NodeSpinningSeconds > 0
	spinningThreshold := time.Duration(rt.cfg.NodeSpinningSeconds * float64(time.Second))

	for _, nodeInfo := range rt.nodes {
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
			fileInfo, parseErr := message.ParseMessageFilename(entry.Name())
			if parseErr != nil {
				continue
			}
			recipientKey := nodeInfo.SessionName + ":" + fileInfo.To
			recipientInfo, ok := rt.nodes[recipientKey]
			if !ok {
				continue
			}
			paneState := paneStatus[recipientInfo.PaneID]
			contentStr := string(fileContent)
			if updated, changed := advanceWaitingState(contentStr, paneState, now, idleThreshold, spinningThreshold, spinningEnabled); changed {
				_ = os.WriteFile(filePath, []byte(updated), 0o600)
				recordCompatibilityMailboxPayload(nodeInfo.SessionDir, nodeInfo.SessionName, "compatibility_mailbox_waiting_updated", journal.VisibilityOperatorVisible, compatibilityMailboxPayloadForFile(entry.Name(), filepath.Join("waiting", entry.Name()), updated))
				previousState := visibleWaitingState(contentStr)
				updatedState := visibleWaitingState(updated)
				if previousState == "composing" && updatedState == "spinning" {
					_ = sendSpinningAlertForWaitingFile(rt.sessionDir, rt.contextID, entry.Name(), updated, now, spinningThreshold, rt.cfg, rt.adjacency, rt.nodes)
				}
				if (previousState == "composing" || previousState == "spinning") && updatedState == "stalled" {
					_ = sendStalledAlertForWaitingFile(rt.sessionDir, rt.contextID, entry.Name(), previousState, updated, now, idleThreshold, rt.cfg, rt.adjacency, rt.nodes)
				}
			}
		}
	}

	waitingStates := make(map[string]string)
	worstStatePriority := map[string]int{
		"user_input": 0,
		"pending":    1,
		"composing":  2,
		"spinning":   3,
		"stalled":    4,
	}
	for _, nodeInfo := range rt.nodes {
		waitingDir := filepath.Join(nodeInfo.SessionDir, "waiting")
		entries, err := os.ReadDir(waitingDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			content, readErr := os.ReadFile(filepath.Join(waitingDir, entry.Name()))
			if readErr != nil {
				continue
			}
			fileState := visibleWaitingState(string(content))
			if fileState == "" {
				continue
			}
			fileInfo, parseErr := message.ParseMessageFilename(entry.Name())
			if parseErr != nil {
				continue
			}
			recipientKey := nodeInfo.SessionName + ":" + fileInfo.To
			if worstStatePriority[fileState] >= worstStatePriority[waitingStates[recipientKey]] {
				waitingStates[recipientKey] = fileState
			}
		}
	}
	for key, state := range collectPendingStates(rt.nodes, worstStatePriority) {
		if worstStatePriority[state] >= worstStatePriority[waitingStates[key]] {
			waitingStates[key] = state
		}
	}
	rt.events <- tui.DaemonEvent{
		Type:    "waiting_state_update",
		Message: "Waiting states updated",
		Details: map[string]interface{}{
			"waiting_states": waitingStates,
		},
	}
}

func (rt *daemonRuntime) discoverNodes() (map[string]discovery.NodeInfo, []discovery.CollisionReport, error) {
	freshNodes, collisions, err := discovery.DiscoverNodesWithCollisions(rt.baseDir, rt.contextID, rt.selfSession)
	if err != nil {
		return nil, nil, err
	}
	filterNodesByEdges(freshNodes, rt.cfg.Edges)
	mergePhonyNodes(freshNodes, rt.registry)
	return freshNodes, collisions, nil
}

func (rt *daemonRuntime) storeSharedNodes() {
	if rt.sharedNodes == nil {
		return
	}
	nodesSnapshot := rt.nodes
	rt.sharedNodes.Store(&nodesSnapshot)
}

func (rt *daemonRuntime) ensureNodeWatchDirs(nodeName string, nodeInfo discovery.NodeInfo) {
	if err := config.CreateSessionDirs(nodeInfo.SessionDir); err != nil {
		rt.events <- tui.DaemonEvent{
			Type:    "error",
			Message: fmt.Sprintf("failed to create session dirs for %s: %v", nodeName, err),
		}
		return
	}

	nodePostDir := filepath.Join(nodeInfo.SessionDir, "post")
	nodeInboxDir := filepath.Join(nodeInfo.SessionDir, "inbox")
	nodeReadDir := filepath.Join(nodeInfo.SessionDir, "read")

	if !rt.watchedDirs[nodePostDir] {
		if err := rt.watcher.Add(nodePostDir); err == nil {
			rt.watchedDirs[nodePostDir] = true
		}
	}
	if !rt.watchedDirs[nodeInboxDir] {
		if err := rt.watcher.Add(nodeInboxDir); err == nil {
			rt.watchedDirs[nodeInboxDir] = true
		}
	}
	if !rt.watchedDirs[nodeReadDir] {
		if err := rt.watcher.Add(nodeReadDir); err == nil {
			rt.watchedDirs[nodeReadDir] = true
		}
	}

	submitRequestsDir := projection.CompatibilitySubmitRequestsDir(nodeInfo.SessionDir)
	if err := projection.EnsureCompatibilitySubmitDirs(nodeInfo.SessionDir); err != nil {
		log.Printf("postman: WARNING: failed to create compatibility submit dirs for %s: %v\n", nodeName, err)
		return
	}
	if !rt.watchedDirs[submitRequestsDir] {
		if err := rt.watcher.Add(submitRequestsDir); err == nil {
			rt.watchedDirs[submitRequestsDir] = true
		}
	}
}

func (rt *daemonRuntime) detectNewNodes(freshNodes map[string]discovery.NodeInfo) []string {
	nodeNames := make([]string, 0, len(freshNodes))
	for nodeName := range freshNodes {
		nodeNames = append(nodeNames, nodeName)
	}
	sort.Strings(nodeNames)

	newNodes := make([]string, 0, len(nodeNames))
	for _, nodeName := range nodeNames {
		nodeInfo := freshNodes[nodeName]
		if rt.knownNodes[nodeName] {
			continue
		}
		rt.knownNodes[nodeName] = true
		if nodeInfo.IsPhony {
			continue
		}
		rt.ensureNodeWatchDirs(nodeName, nodeInfo)
		newNodes = append(newNodes, nodeName)
	}
	return newNodes
}

func (rt *daemonRuntime) pruneClaimedPanes(freshNodes map[string]discovery.NodeInfo) {
	livePaneIDs := make(map[string]bool, len(freshNodes))
	for _, nodeInfo := range freshNodes {
		if !nodeInfo.IsPhony && nodeInfo.PaneID != "" {
			livePaneIDs[nodeInfo.PaneID] = true
		}
	}
	for paneID := range rt.claimedPanes {
		if !livePaneIDs[paneID] {
			delete(rt.claimedPanes, paneID)
		}
	}
}

func (rt *daemonRuntime) claimNewPanes(freshNodes map[string]discovery.NodeInfo) {
	for _, nodeInfo := range freshNodes {
		if nodeInfo.IsPhony || nodeInfo.PaneID == "" || rt.claimedPanes[nodeInfo.PaneID] {
			continue
		}
		claimCmd := exec.Command(
			"tmux", "set-option", "-p", "-t", nodeInfo.PaneID,
			"@a2a_context_id", rt.contextID,
		)
		if err := claimCmd.Run(); err != nil {
			log.Printf("postman: WARNING: failed to claim pane %s: %v\n", nodeInfo.PaneID, err)
			continue
		}
		rt.claimedPanes[nodeInfo.PaneID] = true
	}
}

// logPaneIDChanges logs collapse and re-discovery events for nodes whose PaneID
// changed since the last cycle. Called before rt.nodes is updated to freshNodes.
func (rt *daemonRuntime) logPaneIDChanges(freshNodes map[string]discovery.NodeInfo) {
	for nodeKey, oldInfo := range rt.nodes {
		if oldInfo.IsPhony || oldInfo.PaneID == "" {
			continue
		}
		freshInfo, found := freshNodes[nodeKey]
		if !found || freshInfo.PaneID == "" {
			log.Printf("postman: discovery: session %s collapsed (pane=%s node=%s)\n",
				oldInfo.SessionName, oldInfo.PaneID, nodeKey)
		} else if freshInfo.PaneID != oldInfo.PaneID {
			log.Printf("postman: discovery: session %s re-discovered node %s (pane=%s -> %s)\n",
				freshInfo.SessionName, nodeKey, oldInfo.PaneID, freshInfo.PaneID)
		}
	}
}

func (rt *daemonRuntime) recordPendingAutoPings(nodeKeys []string, freshNodes map[string]discovery.NodeInfo, reason string, now time.Time) {
	if len(nodeKeys) == 0 {
		return
	}

	sortedNodeKeys := append([]string{}, nodeKeys...)
	sort.Strings(sortedNodeKeys)
	for _, nodeKey := range sortedNodeKeys {
		nodeInfo, ok := freshNodes[nodeKey]
		if !ok {
			continue
		}
		rt.recordPendingAutoPing(nodeKey, nodeInfo, reason, now)
	}
}

func (rt *daemonRuntime) recordPendingAutoPing(nodeKey string, nodeInfo discovery.NodeInfo, reason string, now time.Time) {
	if nodeInfo.IsPhony || nodeInfo.SessionDir == "" || nodeInfo.SessionName == "" {
		return
	}

	delaySeconds := 0.0
	if rt.cfg != nil {
		delaySeconds = rt.cfg.AutoPingDelaySeconds
	}

	triggeredAt := now
	notBeforeAt := now.Add(time.Duration(delaySeconds * float64(time.Second)))
	if state, ok, err := projection.ProjectAutoPingState(nodeInfo.SessionDir); err != nil {
		log.Printf("postman: WARNING: auto-PING replay failed for %s: %v\n", nodeKey, err)
	} else if ok {
		if existing, exists := state.Nodes[nodeKey]; exists && existing.Pending {
			if existing.DelaySeconds >= 0 {
				delaySeconds = existing.DelaySeconds
			}
			if parsed, err := time.Parse(time.RFC3339Nano, existing.TriggeredAt); err == nil {
				triggeredAt = parsed
			}
			if parsed, err := time.Parse(time.RFC3339Nano, existing.NotBeforeAt); err == nil {
				notBeforeAt = parsed
			}
			if reason == "" {
				reason = existing.Reason
			}
		}
	}
	if reason == "" {
		reason = "discovered"
	}

	payload := projection.AutoPingEventPayload{
		NodeKey:      nodeKey,
		SessionName:  nodeInfo.SessionName,
		NodeName:     ping.ExtractSimpleName(nodeKey),
		PaneID:       nodeInfo.PaneID,
		Reason:       reason,
		TriggeredAt:  triggeredAt.Format(time.RFC3339Nano),
		DelaySeconds: delaySeconds,
		NotBeforeAt:  notBeforeAt.Format(time.RFC3339Nano),
	}
	if err := journal.RecordProcessEvent(nodeInfo.SessionDir, nodeInfo.SessionName, projection.AutoPingPendingEventType, journal.VisibilityOperatorVisible, payload, now); err != nil {
		log.Printf("postman: WARNING: auto-PING pending append failed for %s: %v\n", nodeKey, err)
	}
}

func (rt *daemonRuntime) recordDeliveredAutoPing(nodeKey string, nodeInfo discovery.NodeInfo, pending projection.AutoPingNodeState, now time.Time) {
	payload := projection.AutoPingEventPayload{
		NodeKey:      nodeKey,
		SessionName:  nodeInfo.SessionName,
		NodeName:     ping.ExtractSimpleName(nodeKey),
		PaneID:       nodeInfo.PaneID,
		Reason:       pending.Reason,
		TriggeredAt:  pending.TriggeredAt,
		DelaySeconds: pending.DelaySeconds,
		NotBeforeAt:  pending.NotBeforeAt,
		DeliveredAt:  now.Format(time.RFC3339Nano),
	}
	if payload.Reason == "" {
		payload.Reason = "discovered"
	}
	if err := journal.RecordProcessEvent(nodeInfo.SessionDir, nodeInfo.SessionName, projection.AutoPingDeliveredEventType, journal.VisibilityOperatorVisible, payload, now); err != nil {
		log.Printf("postman: WARNING: auto-PING delivered append failed for %s: %v\n", nodeKey, err)
	}
}

func (rt *daemonRuntime) dispatchPendingAutoPings(freshNodes map[string]discovery.NodeInfo, autoEnableSessions bool, now time.Time) {
	if len(freshNodes) == 0 || rt.daemonState == nil {
		return
	}

	projectedBySession := make(map[string]projection.AutoPingState)
	for _, sessionDir := range runtimeSessionDirs(rt.sessionDir, freshNodes) {
		state, ok, err := projection.ProjectAutoPingState(sessionDir)
		if err != nil {
			log.Printf("postman: WARNING: auto-PING replay failed for %s: %v\n", sessionDir, err)
			continue
		}
		if ok {
			projectedBySession[sessionDir] = state
		}
	}

	nodeKeys := make([]string, 0, len(freshNodes))
	for nodeKey := range freshNodes {
		nodeKeys = append(nodeKeys, nodeKey)
	}
	sort.Strings(nodeKeys)

	activeNodes := activeRuntimePingNodeNames(freshNodes)
	livenessMap := map[string]bool{}
	if rt.idleTracker != nil {
		livenessMap = rt.idleTracker.GetLivenessMap()
	}

	for _, nodeKey := range nodeKeys {
		nodeInfo := freshNodes[nodeKey]
		if nodeInfo.IsPhony {
			continue
		}

		state, ok := projectedBySession[nodeInfo.SessionDir]
		if !ok {
			continue
		}
		pending, exists := state.Nodes[nodeKey]
		if !exists || !pending.Pending {
			continue
		}
		if pending.NotBeforeAt != "" {
			dueAt, err := time.Parse(time.RFC3339Nano, pending.NotBeforeAt)
			if err == nil && now.Before(dueAt) {
				continue
			}
		}
		if owner := config.FindSessionOwner(rt.baseDir, nodeInfo.SessionName, rt.contextID); owner != "" {
			continue
		}

		enabled := rt.daemonState.GetConfiguredSessionEnabled(nodeInfo.SessionName)
		if !enabled && autoEnableSessions {
			rt.daemonState.AutoEnableSessionIfNew(nodeInfo.SessionName)
			enabled = rt.daemonState.GetConfiguredSessionEnabled(nodeInfo.SessionName)
		}
		if !enabled {
			continue
		}

		tmpl := ""
		if rt.cfg != nil {
			tmpl = rt.cfg.DaemonMessageTemplate
		}
		result, err := ping.SendPingToNodeWithResult(nodeInfo, rt.contextID, nodeKey, tmpl, rt.cfg, activeNodes, livenessMap, rt.adjacency, freshNodes)
		if err != nil {
			log.Printf("postman: WARNING: auto-PING send failed for %s: %v\n", nodeKey, err)
			continue
		}
		if !result.Delivered {
			continue
		}

		rt.recordDeliveredAutoPing(nodeKey, nodeInfo, pending, now)
	}
}

func activeRuntimePingNodeNames(nodes map[string]discovery.NodeInfo) []string {
	activeNodes := make([]string, 0, len(nodes))
	seen := make(map[string]bool)
	for nodeName := range nodes {
		simpleName := ping.ExtractSimpleName(nodeName)
		if seen[simpleName] {
			continue
		}
		seen[simpleName] = true
		activeNodes = append(activeNodes, simpleName)
	}
	sort.Strings(activeNodes)
	return activeNodes
}
