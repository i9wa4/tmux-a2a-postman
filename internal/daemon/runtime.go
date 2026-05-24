package daemon

import (
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

	"github.com/gofsnotify/fsnotify"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/controlplane"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/ping"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/reconciler"
	"github.com/i9wa4/tmux-a2a-postman/internal/session"
	"github.com/i9wa4/tmux-a2a-postman/internal/store"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
	"github.com/i9wa4/tmux-a2a-postman/internal/uinode"
)

type daemonRuntime struct {
	baseDir     string
	sessionDir  string
	contextID   string
	configPath  string
	selfSession string
	cfg         *config.Config
	watcher     *fsnotify.Watcher
	adjacency   map[string][]string
	nodes       map[string]discovery.NodeInfo
	knownNodes  map[string]bool
	events      chan<- tui.DaemonEvent
	configPaths []string
	nodesDirs   []string
	daemonState *DaemonState
	idleTracker *idle.IdleTracker
	clock       func() time.Time

	sharedNodes *atomic.Pointer[map[string]discovery.NodeInfo]

	watchedDirs        map[string]bool
	claimedPanes       map[string]bool
	prevPaneStatesJSON string
	prevNodeCount      int
	prevSessionNames   []string
	prevSessionNodes   map[string][]string

	postEventsMu     sync.Mutex
	activePostEvents map[string]bool

	sendAutoPing     autoPingSender
	autoPingEventsMu sync.Mutex
	activeAutoPings  map[string]bool

	processDaemonSubmit        daemonSubmitProcessor
	daemonSubmitSem            chan struct{}
	daemonSubmitResults        chan daemonSubmitRuntimeResult
	activeDaemonSubmitSessions map[string]bool
}

const daemonSubmitWorkerLimit = 4

type daemonSubmitProcessor func(requestPath string) (daemonSubmitProcessResult, error)

type daemonSubmitRuntimeResult struct {
	requestPath string
	sessionKey  string
	result      daemonSubmitProcessResult
	err         error
}

type runtimeStatusSnapshot struct {
	NodeCount              int
	Sessions               []tui.SessionInfo
	SessionNodes           map[string][]string
	NormalizedSessionNames []string
	NormalizedSessionNodes map[string][]string
}

type postDeliveryReservation struct {
	route          string
	reservedAt     time.Time
	hasReservation bool
}

type autoPingSender func(nodeInfo discovery.NodeInfo, contextID, nodeName, tmpl string, cfg *config.Config, activeNodes []string, livenessMap map[string]bool, adjacency map[string][]string, nodes map[string]discovery.NodeInfo) (controlplane.SystemMessageResult, error)

func newDaemonRuntime(
	baseDir string,
	sessionDir string,
	contextID string,
	cfg *config.Config,
	watcher *fsnotify.Watcher,
	adjacency map[string][]string,
	nodes map[string]discovery.NodeInfo,
	knownNodes map[string]bool,
	events chan<- tui.DaemonEvent,
	configPath string,
	configPaths []string,
	nodesDirs []string,
	daemonState *DaemonState,
	idleTracker *idle.IdleTracker,
	sharedNodes *atomic.Pointer[map[string]discovery.NodeInfo],
	selfSession string,
) *daemonRuntime {
	return &daemonRuntime{
		baseDir:                    baseDir,
		sessionDir:                 sessionDir,
		contextID:                  contextID,
		configPath:                 configPath,
		selfSession:                selfSession,
		cfg:                        cfg,
		watcher:                    watcher,
		adjacency:                  adjacency,
		nodes:                      nodes,
		knownNodes:                 knownNodes,
		events:                     events,
		configPaths:                configPaths,
		nodesDirs:                  nodesDirs,
		daemonState:                daemonState,
		idleTracker:                idleTracker,
		sharedNodes:                sharedNodes,
		watchedDirs:                make(map[string]bool),
		claimedPanes:               make(map[string]bool),
		prevSessionNodes:           make(map[string][]string),
		activePostEvents:           make(map[string]bool),
		activeAutoPings:            make(map[string]bool),
		processDaemonSubmit:        processDaemonSubmitRequest,
		daemonSubmitSem:            make(chan struct{}, daemonSubmitWorkerLimit),
		daemonSubmitResults:        make(chan daemonSubmitRuntimeResult, daemonSubmitWorkerLimit),
		activeDaemonSubmitSessions: make(map[string]bool),
	}
}

func (rt *daemonRuntime) now() time.Time {
	if rt.clock == nil {
		return time.Now()
	}
	return rt.clock()
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

func resumeMailboxProjections(primarySessionDir string, nodes map[string]discovery.NodeInfo) error {
	for _, sessionDir := range runtimeSessionDirs(primarySessionDir, nodes) {
		if err := projection.SyncMailboxProjection(sessionDir); err != nil {
			return fmt.Errorf("sync mailbox projection %s: %w", sessionDir, err)
		}
	}
	return nil
}

func (rt *daemonRuntime) bootstrap() {
	rt.storeSharedNodes()

	now := rt.now()
	installShadowJournalManager(rt.sessionDir, rt.contextID, rt.selfSession, now)
	if err := resumeMailboxProjections(rt.sessionDir, rt.nodes); err != nil {
		log.Printf("postman: WARNING: %v\n", err)
	}
	rt.dispatchPendingDaemonSubmitRequests()
	rt.recordPendingAutoPings(startupAutoPingNodeKeys(rt.nodes, rt.cfg), rt.nodes, "startup", now)
	autoEnableSessions := config.BoolVal(rt.cfg.AutoEnableNewSessions, true)
	rt.dispatchPendingAutoPings(rt.nodes, autoEnableSessions, now)
	rt.dispatchPendingPostMessages()
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

	switch {
	case filepath.Base(filepath.Dir(eventPath)) == "requests" && filepath.Base(filepath.Dir(filepath.Dir(eventPath))) == string(projection.SubmitPathDaemon):
		if event.Op&(fsnotify.Create|fsnotify.Rename) != 0 && strings.HasSuffix(filepath.Base(eventPath), ".json") {
			rt.handleDaemonSubmitRequest(eventPath)
		}
	case strings.HasSuffix(filepath.Dir(eventPath), "post"):
		if event.Op&(fsnotify.Create|fsnotify.Rename) != 0 {
			rt.wakePostReconciler(eventPath)
		}
	case strings.HasSuffix(filepath.Dir(eventPath), "read"):
		rt.handleReadWatcherEvent(eventPath, event.Op)
	}
}

func (rt *daemonRuntime) handleDaemonSubmitRequest(requestPath string) {
	status := rt.dispatchDaemonSubmitRequest(requestPath)
	if status == daemonSubmitDispatchSaturated {
		log.Printf("postman: WARNING: component=%s event=request_workers_saturated submit_path=%s request=%s\n",
			projection.SubmitPathDaemon, projection.SubmitPathDaemon, filepath.Base(requestPath))
	}
}

type daemonSubmitDispatchStatus int

const (
	daemonSubmitDispatched daemonSubmitDispatchStatus = iota
	daemonSubmitDispatchDeferred
	daemonSubmitDispatchSaturated
)

func (rt *daemonRuntime) dispatchDaemonSubmitRequest(requestPath string) daemonSubmitDispatchStatus {
	rt.ensureDaemonSubmitRuntime()
	sessionKey := daemonSubmitSessionKey(requestPath)
	if rt.activeDaemonSubmitSessions[sessionKey] {
		return daemonSubmitDispatchDeferred
	}
	select {
	case rt.daemonSubmitSem <- struct{}{}:
	default:
		return daemonSubmitDispatchSaturated
	}
	rt.activeDaemonSubmitSessions[sessionKey] = true
	processor := rt.processDaemonSubmit

	go func() {
		workerResult := daemonSubmitRuntimeResult{
			requestPath: requestPath,
			sessionKey:  sessionKey,
		}
		defer func() {
			if r := recover(); r != nil {
				workerResult.err = fmt.Errorf("panic processing %s: %v", filepath.Base(requestPath), r)
			}
			rt.daemonSubmitResults <- workerResult
			<-rt.daemonSubmitSem
		}()
		workerResult.result, workerResult.err = processor(requestPath)
	}()

	return daemonSubmitDispatched
}

func (rt *daemonRuntime) ensureDaemonSubmitRuntime() {
	if rt.processDaemonSubmit == nil {
		rt.processDaemonSubmit = processDaemonSubmitRequest
	}
	if rt.daemonSubmitSem == nil {
		rt.daemonSubmitSem = make(chan struct{}, daemonSubmitWorkerLimit)
	}
	if rt.daemonSubmitResults == nil {
		rt.daemonSubmitResults = make(chan daemonSubmitRuntimeResult, daemonSubmitWorkerLimit)
	}
	if rt.activeDaemonSubmitSessions == nil {
		rt.activeDaemonSubmitSessions = make(map[string]bool)
	}
}

func daemonSubmitSessionKey(requestPath string) string {
	if sessionDir, ok := daemonSubmitSessionDir(requestPath); ok {
		return sessionDir
	}
	return filepath.Dir(requestPath)
}

func (rt *daemonRuntime) handleDaemonSubmitResult(workerResult daemonSubmitRuntimeResult) {
	rt.ensureDaemonSubmitRuntime()
	delete(rt.activeDaemonSubmitSessions, workerResult.sessionKey)
	if workerResult.err != nil {
		rt.events <- tui.DaemonEvent{
			Type:    "error",
			Message: fmt.Sprintf("%s %s: %v", projection.SubmitPathDaemon, filepath.Base(workerResult.requestPath), workerResult.err),
		}
		return
	}
	if workerResult.result.hasPostDispatch() {
		log.Printf("postman: component=%s event=send_reconcile submit_path=%s session=%s file=%s\n",
			projection.SubmitPathDaemon, projection.SubmitPathDaemon, filepath.Base(workerResult.result.SessionDir), workerResult.result.Filename)
		rt.wakePostReconciler(workerResult.result.PostPath)
	}
	rt.dispatchPendingDaemonSubmitRequests()
}

func (rt *daemonRuntime) dispatchPendingDaemonSubmitRequests() {
	for _, sessionDir := range runtimeSessionDirs(rt.sessionDir, rt.nodes) {
		requestsDir := projection.DaemonSubmitRequestsDir(sessionDir)
		entries, err := os.ReadDir(requestsDir)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Printf("postman: WARNING: component=%s event=request_scan_failed submit_path=%s session=%s err=%v\n",
					projection.SubmitPathDaemon, projection.SubmitPathDaemon, filepath.Base(sessionDir), err)
			}
			continue
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			names = append(names, entry.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			status := rt.dispatchDaemonSubmitRequest(filepath.Join(requestsDir, name))
			if status == daemonSubmitDispatchSaturated {
				return
			}
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

	rt.processActivePostEvent(eventPath, filename)
}

func (rt *daemonRuntime) wakePostReconciler(eventPath string) {
	if _, err := rt.postReconciler().ReconcilePath(eventPath, rt.handlePendingPost); err != nil {
		log.Printf("postman: WARNING: failed to reconcile post wake-up %s: %v\n", eventPath, err)
	}
}

func (rt *daemonRuntime) postReconciler() reconciler.PostReconciler {
	return reconciler.PostReconciler{
		CoalesceRateLimitedRoutes: rt.cfg.MinDeliveryGapSeconds > 0,
		RouteKey: func(post store.PendingPost) (string, bool) {
			info, err := message.ParseMessageFilename(post.Filename)
			if err != nil {
				return "", false
			}
			return info.From + ":" + info.To, true
		},
	}
}

func (rt *daemonRuntime) handlePendingPost(post store.PendingPost) {
	rt.handlePostWatcherEvent(post.Path, fsnotify.Create)
}

func (rt *daemonRuntime) processActivePostEvent(eventPath, filename string) {
	reservation, ok := rt.reservePostDeliveryOrScheduleRetry(eventPath, filename)
	if !ok {
		return
	}

	now := rt.now()
	recordShadowMailboxPathEvent(eventPath, projection.MailboxProjectionPostedEventType, journal.VisibilityMailboxProjection, now)
	sourceSessionDir := filepath.Dir(filepath.Dir(eventPath))
	syncMailboxProjection(sourceSessionDir)

	freshNodes, _, err := rt.discoverNodes()
	if err == nil {
		rt.claimNewPanes(freshNodes)
		rt.pruneKnownNodes(freshNodes)
		newNodes := rt.detectNewNodes(freshNodes)
		rt.recordPendingAutoPings(newNodes, freshNodes, "discovered", now)
		rt.logPaneIDChanges(freshNodes)
		rt.nodes = freshNodes
		rt.storeSharedNodes()
		rt.dispatchPendingAutoPings(freshNodes, config.BoolVal(rt.cfg.AutoEnableNewSessions, true), now)

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

	rt.dispatchPostDelivery(eventPath, filename, rt.nodes, rt.adjacency, rt.cfg, reservation)
}

func (rt *daemonRuntime) reservePostDeliveryOrScheduleRetry(eventPath, filename string) (postDeliveryReservation, bool) {
	msgInfo, parseErr := message.ParseMessageFilename(filename)
	if parseErr != nil {
		return postDeliveryReservation{}, true
	}
	deliveryKey := msgInfo.From + ":" + msgInfo.To
	if rt.cfg.MinDeliveryGapSeconds <= 0 {
		return postDeliveryReservation{route: deliveryKey}, true
	}

	gap := time.Duration(rt.cfg.MinDeliveryGapSeconds * float64(time.Second))
	remaining, reservedAt, ok := rt.daemonState.reserveDeliveryRoute(deliveryKey, gap, rt.now())
	if !ok {
		rt.scheduleRateLimitedPostRetry(eventPath, filename, msgInfo.From, msgInfo.To, remaining, rt.cfg.MinDeliveryGapSeconds)
		return postDeliveryReservation{}, false
	}
	return postDeliveryReservation{
		route:          deliveryKey,
		reservedAt:     reservedAt,
		hasReservation: true,
	}, true
}

func (rt *daemonRuntime) scheduleRateLimitedPostRetry(eventPath, filename, from, to string, remaining time.Duration, gapSeconds float64) {
	if remaining < time.Millisecond {
		remaining = time.Millisecond
	}
	log.Printf("postman: rate-limited delivery %s -> %s (gap: %.1fs, retry_in=%s)\n", from, to, gapSeconds, remaining.Round(time.Millisecond))
	safeAfterFunc(remaining, "post-rate-limit-retry", rt.events, func() {
		rt.retryActivePostDelivery(eventPath, filename)
	})
}

func (rt *daemonRuntime) retryActivePostDelivery(eventPath, filename string) {
	if _, err := os.Stat(eventPath); os.IsNotExist(err) {
		rt.finishPostEvent(eventPath)
		return
	}

	rt.processActivePostEvent(eventPath, filename)
}

func (rt *daemonRuntime) dispatchPostDelivery(eventPath, filename string, nodes map[string]discovery.NodeInfo, adjacency map[string][]string, cfg *config.Config, reservation postDeliveryReservation) {
	go func(eventPath, filename string, nodes map[string]discovery.NodeInfo, adjacency map[string][]string, cfg *config.Config) {
		deliveredNormally := false
		defer func() {
			if reservation.route != "" {
				rt.daemonState.finishDeliveryRoute(reservation.route, reservation.reservedAt, reservation.hasReservation, deliveredNormally, rt.now())
			}
			rt.finishPostEvent(eventPath)
		}()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("🚨 PANIC in delivery goroutine for %s: %v\n", filename, r)
			}
		}()

		messageEvents := make(chan message.DaemonEvent, 1)
		if msgInfo, parseErr := message.ParseMessageFilename(filename); parseErr == nil {
			log.Printf("postman: deliver: picked up %s -> %s (file=%s)\n", msgInfo.From, msgInfo.To, filename)
		}
		if err := message.DeliverMessage(eventPath, rt.contextID, nodes, adjacency, cfg, rt.daemonState.IsSessionEnabled, messageEvents, rt.idleTracker, rt.selfSession); err != nil {
			rt.events <- tui.DaemonEvent{
				Type:    "error",
				Message: fmt.Sprintf("deliver %s: %v", filename, err),
			}
			return
		}

		sourceSessionDir := filepath.Dir(filepath.Dir(eventPath))
		sourceSessionName := filepath.Base(sourceSessionDir)
		syncMailboxProjection(sourceSessionDir)
		if info, parseErr := message.ParseMessageFilename(filename); parseErr == nil {
			recipientFullName := discovery.ResolveNodeName(info.To, sourceSessionName, nodes)
			if nodeInfo, ok := nodes[recipientFullName]; ok {
				syncMailboxProjection(nodeInfo.SessionDir)
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
			deliveredNormally = true
		}

		if !suppressNormalDelivery {
			rt.events <- tui.DaemonEvent{
				Type:    "message_received",
				Message: fmt.Sprintf("Delivered: %s", filename),
				Details: map[string]interface{}{
					"session": sourceSessionName,
				},
			}
		}

		if !suppressNormalDelivery {
			if _, err := message.ParseMessageFilename(filename); err == nil {
				nodeStates := rt.idleTracker.GetNodeStates()
				rt.events <- tui.DaemonEvent{
					Type: "node_activity_update",
					Details: map[string]interface{}{
						"node_states": nodeStates,
					},
				}
			}
		}
	}(eventPath, filename, nodes, adjacency, cfg)
}

func (rt *daemonRuntime) dispatchPendingPostMessages() {
	if _, err := rt.postReconciler().ReconcileSessionDirs(runtimeSessionDirs(rt.sessionDir, rt.nodes), rt.handlePendingPost); err != nil {
		log.Printf("postman: WARNING: failed to reconcile pending post messages: %v\n", err)
	}
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

	recordShadowMailboxPathEvent(eventPath, projection.MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, rt.now())
	sourceSessionDir := filepath.Dir(filepath.Dir(eventPath))
	sourceSessionName := filepath.Base(sourceSessionDir)
	syncMailboxProjection(sourceSessionDir)

	if info.To == "postman" || info.To == "daemon" {
		return
	}

	prefixedKey := sourceSessionName + ":" + info.To
	rt.idleTracker.MarkNodeAlive(prefixedKey)
	rt.events <- tui.DaemonEvent{
		Type: "node_alive",
		Details: map[string]interface{}{
			"node":   prefixedKey,
			"source": "read_move",
		},
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
		rt.events <- tui.DaemonEvent{
			Type:    "pane_collision",
			Message: fmt.Sprintf("[COLLISION] %s: %s displaced by %s", collision.NodeKey, collision.LoserPaneID, collision.WinnerPaneID),
			Details: map[string]interface{}{
				"node":           collision.NodeKey,
				"winner_pane_id": collision.WinnerPaneID,
				"loser_pane_id":  collision.LoserPaneID,
			},
		}
	}

	autoEnableSessions := config.BoolVal(rt.cfg.AutoEnableNewSessions, true)
	rt.pruneKnownNodes(freshNodes)
	newNodes := rt.detectNewNodes(freshNodes)
	now := rt.now()
	rt.recordPendingAutoPings(newNodes, freshNodes, "discovered", now)
	rt.nodes = freshNodes
	rt.storeSharedNodes()

	allSessions, err := discovery.DiscoverAllSessions()
	if err != nil {
		rt.events <- tui.DaemonEvent{
			Type:    "error",
			Message: fmt.Sprintf("failed to discover all sessions: %v", err),
		}
		allSessions = []string{}
	}

	rt.emitStatusUpdateIfChanged(allSessions)

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
			restartedNodes := rt.daemonState.checkPaneRestarts(paneStates, paneToNode, rt.nodes, rt.events)
			rt.recordPendingAutoPings(restartedNodes, rt.nodes, "pane_restart", now)
			rt.prevPaneStatesJSON = currentJSONStr
		}
	}

	rt.dispatchPendingAutoPings(freshNodes, autoEnableSessions, now)
	rt.dispatchPendingDaemonSubmitRequests()
	rt.dispatchPendingPostMessages()

	nodeStates := rt.idleTracker.GetNodeStates()
	rt.events <- tui.DaemonEvent{
		Type:    "node_activity_update",
		Message: "Node activity updated",
		Details: map[string]interface{}{
			"node_states": nodeStates,
		},
	}
}

func (rt *daemonRuntime) handleSessionScanTick() {
	allSessions, err := discovery.DiscoverAllSessions()
	if err != nil {
		rt.events <- tui.DaemonEvent{
			Type:    "error",
			Message: fmt.Sprintf("failed to discover all sessions: %v", err),
		}
		return
	}
	if rt.activateNewSessionsFromScan(allSessions) {
		rt.refreshNodesAfterSessionActivation(allSessions)
	}
	rt.emitStatusUpdateIfChanged(allSessions)
}

func (rt *daemonRuntime) activateNewSessionsFromScan(allSessions []string) bool {
	if rt == nil || rt.cfg == nil || rt.daemonState == nil {
		return false
	}
	if !config.BoolVal(rt.cfg.AutoEnableNewSessions, true) {
		return false
	}

	contextDir := filepath.Dir(rt.sessionDir)
	candidateNodes := runtimeActivationNodeNames(rt.cfg)
	activated := false
	for _, targetSession := range allSessions {
		if targetSession == "" || targetSession == rt.selfSession {
			continue
		}
		if rt.daemonState.hasConfiguredSession(targetSession) {
			continue
		}
		if owner := config.FindSessionOwner(rt.baseDir, targetSession, rt.contextID); owner != "" {
			continue
		}

		preClaimed := preclaimRuntimeSessionCandidatePanes(targetSession, rt.contextID, candidateNodes)
		if preClaimed == 0 {
			continue
		}
		if err := config.CreateMultiSessionDirs(contextDir, targetSession); err != nil {
			log.Printf("postman: WARNING: failed to create auto-enabled session dirs for %s: %v\n", targetSession, err)
			continue
		}
		rt.daemonState.AutoEnableSessionIfNew(targetSession)
		log.Printf("postman: session-scan activated session %s (%d panes)\n", targetSession, preClaimed)
		activated = true
	}
	return activated
}

func runtimeActivationNodeNames(cfg *config.Config) map[string]bool {
	if cfg == nil {
		return map[string]bool{}
	}
	candidateNodes := config.GetEdgeNodeNames(cfg.Edges)
	if candidateNodes == nil {
		candidateNodes = make(map[string]bool)
	}
	for _, nodeName := range cfg.OrderedNodeNames() {
		if nodeName == "" {
			continue
		}
		candidateNodes[nodeName] = true
	}
	return candidateNodes
}

func filterNodesByRuntimeConfig(nodes map[string]discovery.NodeInfo, cfg *config.Config) {
	filterNodesByRuntimeCandidates(nodes, runtimeActivationNodeNames(cfg))
}

func filterNodesByRuntimeCandidates(nodes map[string]discovery.NodeInfo, candidateNodes map[string]bool) {
	for nodeName := range nodes {
		if !config.EdgeNodeAllowed(candidateNodes, nodeName) {
			delete(nodes, nodeName)
		}
	}
}

func preclaimRuntimeSessionCandidatePanes(sessionName, contextID string, candidateNodes map[string]bool) int {
	out, err := exec.Command("tmux", "list-panes", "-s", "-t", sessionName, "-F", "#{pane_id} #{pane_title}").Output()
	if err != nil {
		log.Printf("postman: WARNING: failed to list panes for session %s: %v\n", sessionName, err)
		return 0
	}

	preClaimed := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		nodeName := parts[1]
		nodeKey := sessionName + ":" + nodeName
		if !config.EdgeNodeAllowed(candidateNodes, nodeKey) {
			continue
		}
		if err := exec.Command("tmux", "set-option", "-p", "-t", parts[0], "@a2a_context_id", contextID).Run(); err != nil {
			log.Printf("postman: WARNING: failed to pre-claim pane %s (%s): %v\n", parts[0], parts[1], err)
			continue
		}
		preClaimed++
	}
	return preClaimed
}

func (rt *daemonRuntime) refreshNodesAfterSessionActivation(allSessions []string) {
	freshNodes, scanCollisions, err := rt.discoverNodes()
	if err != nil {
		log.Printf("postman: WARNING: session-scan node discovery failed after activation: %v\n", err)
		return
	}

	rt.pruneClaimedPanes(freshNodes)
	rt.claimNewPanes(freshNodes)
	for _, collision := range scanCollisions {
		rt.events <- tui.DaemonEvent{
			Type:    "pane_collision",
			Message: fmt.Sprintf("[COLLISION] %s: %s displaced by %s", collision.NodeKey, collision.LoserPaneID, collision.WinnerPaneID),
			Details: map[string]interface{}{
				"node":           collision.NodeKey,
				"winner_pane_id": collision.WinnerPaneID,
				"loser_pane_id":  collision.LoserPaneID,
			},
		}
	}
	rt.pruneKnownNodes(freshNodes)
	newNodes := rt.detectNewNodes(freshNodes)
	now := rt.now()
	rt.recordPendingAutoPings(newNodes, freshNodes, "discovered", now)
	rt.nodes = freshNodes
	rt.storeSharedNodes()
	rt.dispatchPendingAutoPings(freshNodes, config.BoolVal(rt.cfg.AutoEnableNewSessions, true), now)
	rt.emitStatusUpdateIfChanged(allSessions)
}

func (rt *daemonRuntime) emitStatusUpdateIfChanged(allSessions []string) {
	if allSessions == nil {
		allSessions = []string{}
	}
	snapshot := buildRuntimeStatusSnapshot(rt.nodes, allSessions, rt.daemonState.GetConfiguredSessionEnabled)
	if !snapshot.changed(rt.prevNodeCount, rt.prevSessionNames, rt.prevSessionNodes) {
		return
	}
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

func (rt *daemonRuntime) handleInboxCheckTick() {
	checkSwallowedMessages(rt.nodes, rt.cfg, rt.events, rt.contextID, rt.adjacency, rt.idleTracker, rt.daemonState)

	rt.events <- tui.DaemonEvent{
		Type: "inbox_unread_count_update",
		Details: map[string]interface{}{
			"unread_counts": scanLiveInboxCounts(rt.nodes),
		},
	}
}

func (rt *daemonRuntime) discoverNodes() (map[string]discovery.NodeInfo, []discovery.CollisionReport, error) {
	freshNodes, collisions, err := discovery.DiscoverNodesWithCollisions(rt.baseDir, rt.contextID, rt.selfSession)
	if err != nil {
		return nil, nil, err
	}
	filterNodesByRuntimeConfig(freshNodes, rt.cfg)
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
		if err := rt.watcher.Add(nodePostDir, fsnotify.All); err == nil {
			rt.watchedDirs[nodePostDir] = true
		}
	}
	if !rt.watchedDirs[nodeInboxDir] {
		if err := rt.watcher.Add(nodeInboxDir, fsnotify.All); err == nil {
			rt.watchedDirs[nodeInboxDir] = true
		}
	}
	if !rt.watchedDirs[nodeReadDir] {
		if err := rt.watcher.Add(nodeReadDir, fsnotify.All); err == nil {
			rt.watchedDirs[nodeReadDir] = true
		}
	}

	submitRequestsDir := projection.DaemonSubmitRequestsDir(nodeInfo.SessionDir)
	if err := projection.EnsureDaemonSubmitDirs(nodeInfo.SessionDir); err != nil {
		log.Printf("postman: WARNING: component=%s event=dirs_create_failed submit_path=%s node=%s err=%v\n", projection.SubmitPathDaemon, projection.SubmitPathDaemon, nodeName, err)
		return
	}
	if !rt.watchedDirs[submitRequestsDir] {
		if err := rt.watcher.Add(submitRequestsDir, fsnotify.All); err == nil {
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
		rt.ensureNodeWatchDirs(nodeName, nodeInfo)
		newNodes = append(newNodes, nodeName)
	}
	return newNodes
}

func (rt *daemonRuntime) pruneKnownNodes(freshNodes map[string]discovery.NodeInfo) {
	for nodeName := range rt.knownNodes {
		if _, live := freshNodes[nodeName]; !live {
			delete(rt.knownNodes, nodeName)
		}
	}
}

func (rt *daemonRuntime) pruneClaimedPanes(freshNodes map[string]discovery.NodeInfo) {
	livePaneIDs := make(map[string]bool, len(freshNodes))
	for _, nodeInfo := range freshNodes {
		if nodeInfo.PaneID != "" {
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
		if nodeInfo.PaneID == "" || rt.claimedPanes[nodeInfo.PaneID] {
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
		if oldInfo.PaneID == "" {
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
	if nodeInfo.SessionDir == "" || nodeInfo.SessionName == "" {
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
		if !rt.beginAutoPing(nodeKey) {
			continue
		}

		dispatchNodeKey := nodeKey
		dispatchNodeInfo := nodeInfo
		dispatchPending := pending
		dispatchTemplate := tmpl
		dispatchActiveNodes := append([]string(nil), activeNodes...)
		dispatchLivenessMap := cloneBoolMap(livenessMap)
		dispatchAdjacency := cloneStringSliceMap(rt.adjacency)
		dispatchNodes := cloneNodeInfoMap(freshNodes)
		sendAutoPing := rt.autoPingSender()
		go func() {
			defer rt.finishAutoPing(dispatchNodeKey)

			result, err := sendAutoPing(dispatchNodeInfo, rt.contextID, dispatchNodeKey, dispatchTemplate, rt.cfg, dispatchActiveNodes, dispatchLivenessMap, dispatchAdjacency, dispatchNodes)
			if err != nil {
				log.Printf("postman: WARNING: auto-PING send failed for %s: %v\n", dispatchNodeKey, err)
				return
			}
			if !result.Delivered {
				return
			}

			rt.recordDeliveredAutoPing(dispatchNodeKey, dispatchNodeInfo, dispatchPending, rt.now())
		}()
	}
}

func (rt *daemonRuntime) autoPingSender() autoPingSender {
	if rt.sendAutoPing != nil {
		return rt.sendAutoPing
	}
	return ping.SendPingToNodeWithResult
}

func (rt *daemonRuntime) beginAutoPing(nodeKey string) bool {
	rt.autoPingEventsMu.Lock()
	defer rt.autoPingEventsMu.Unlock()
	if rt.activeAutoPings == nil {
		rt.activeAutoPings = make(map[string]bool)
	}
	if rt.activeAutoPings[nodeKey] {
		return false
	}
	rt.activeAutoPings[nodeKey] = true
	return true
}

func (rt *daemonRuntime) finishAutoPing(nodeKey string) {
	rt.autoPingEventsMu.Lock()
	delete(rt.activeAutoPings, nodeKey)
	rt.autoPingEventsMu.Unlock()
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

func cloneBoolMap(in map[string]bool) map[string]bool {
	if in == nil {
		return nil
	}
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStringSliceMap(in map[string][]string) map[string][]string {
	if in == nil {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func cloneNodeInfoMap(in map[string]discovery.NodeInfo) map[string]discovery.NodeInfo {
	if in == nil {
		return nil
	}
	out := make(map[string]discovery.NodeInfo, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func runtimeNodeKeys(nodes map[string]discovery.NodeInfo) []string {
	nodeKeys := make([]string, 0, len(nodes))
	for nodeKey := range nodes {
		nodeKeys = append(nodeKeys, nodeKey)
	}
	sort.Strings(nodeKeys)
	return nodeKeys
}

func startupAutoPingNodeKeys(nodes map[string]discovery.NodeInfo, cfg *config.Config) []string {
	if cfg == nil || !cfg.HasExplicitUINodeSetting() || cfg.UINode == "" {
		return runtimeNodeKeys(nodes)
	}

	nodeKeys := runtimeNodeKeys(nodes)
	filtered := make([]string, 0, len(nodeKeys))
	for _, nodeKey := range nodeKeys {
		if ping.ExtractSimpleName(nodeKey) == cfg.UINode {
			filtered = append(filtered, nodeKey)
		}
	}
	return filtered
}
