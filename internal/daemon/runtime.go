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

	"github.com/fswatcher/fswatcher"
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
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
	"github.com/i9wa4/tmux-a2a-postman/internal/store"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
	"github.com/i9wa4/tmux-a2a-postman/internal/uinode"
)

type daemonRuntime struct {
	baseDir                         string
	sessionDir                      string
	contextID                       string
	configPath                      string
	selfSession                     string
	cfg                             *config.Config
	watcher                         filesystemWatcher
	adjacency                       map[string][]string
	nodes                           map[string]discovery.NodeInfo
	knownNodes                      map[string]bool
	events                          chan<- tui.DaemonEvent
	configPaths                     []string
	nodesDirs                       []string
	daemonState                     *DaemonState
	nonDaemonDeliveryBudgetFallback *nonDaemonDeliveryBudget
	idleTracker                     *idle.IdleTracker
	clock                           func() time.Time

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

	processDaemonSubmit           daemonSubmitProcessor
	launchDaemonSubmitWorker      daemonSubmitWorkerLauncher
	daemonSubmitSem               chan struct{}
	daemonSubmitResults           chan daemonSubmitRuntimeResult
	activeDaemonSubmitKeys        map[string]bool
	daemonSubmitSaturationCount   int
	daemonSubmitLastSaturatedAt   time.Time
	scheduleRuntimeTimer          runtimeTimerScheduler
	mailboxProjectionSyncMu       sync.Mutex
	activeMailboxProjectionSyncs  map[string]bool
	pendingMailboxProjectionSyncs map[string]bool
	mailboxProjectionSyncWG       sync.WaitGroup
}

type daemonSubmitProcessor func(requestPath string) (daemonSubmitProcessResult, error)

// daemonSubmitWorkerLauncher owns worker scheduling. The worker owns exactly one
// daemon-submit result send and semaphore release.
type daemonSubmitWorkerLauncher func(worker func())

// runtimeTimerScheduler owns callback scheduling while callbacks keep runtime
// ownership of their state transitions.
type runtimeTimerScheduler func(delay time.Duration, name string, events chan<- tui.DaemonEvent, callback func())

type daemonSubmitRuntimeResult struct {
	requestPath string
	dispatchKey string
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
	watcher filesystemWatcher,
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
	daemonSubmitWorkerLimit := daemonSubmitWorkerLimitFromConfig(cfg)
	return &daemonRuntime{
		baseDir:                         baseDir,
		sessionDir:                      sessionDir,
		contextID:                       contextID,
		configPath:                      configPath,
		selfSession:                     selfSession,
		cfg:                             cfg,
		watcher:                         watcher,
		adjacency:                       adjacency,
		nodes:                           nodes,
		knownNodes:                      knownNodes,
		events:                          events,
		configPaths:                     configPaths,
		nodesDirs:                       nodesDirs,
		daemonState:                     daemonState,
		nonDaemonDeliveryBudgetFallback: newNonDaemonDeliveryBudget(nil),
		idleTracker:                     idleTracker,
		sharedNodes:                     sharedNodes,
		watchedDirs:                     make(map[string]bool),
		claimedPanes:                    make(map[string]bool),
		prevSessionNodes:                make(map[string][]string),
		activePostEvents:                make(map[string]bool),
		activeAutoPings:                 make(map[string]bool),
		processDaemonSubmit:             processDaemonSubmitRequest,
		launchDaemonSubmitWorker:        defaultDaemonSubmitWorkerLauncher,
		daemonSubmitSem:                 make(chan struct{}, daemonSubmitWorkerLimit),
		daemonSubmitResults:             make(chan daemonSubmitRuntimeResult, daemonSubmitWorkerLimit),
		activeDaemonSubmitKeys:          make(map[string]bool),
		scheduleRuntimeTimer:            defaultRuntimeTimerScheduler,
		activeMailboxProjectionSyncs:    make(map[string]bool),
		pendingMailboxProjectionSyncs:   make(map[string]bool),
	}
}

func daemonSubmitWorkerLimitFromConfig(cfg *config.Config) int {
	if cfg == nil || cfg.DaemonSubmitWorkerLimit == 0 {
		return config.DefaultDaemonSubmitWorkerLimit
	}
	limit, warning := config.EffectiveDaemonSubmitWorkerLimit(cfg.DaemonSubmitWorkerLimit)
	if warning != "" {
		log.Printf("postman: WARNING: %s\n", warning)
	}
	return limit
}

func defaultDaemonSubmitWorkerLauncher(worker func()) {
	go worker()
}

func defaultRuntimeTimerScheduler(delay time.Duration, name string, events chan<- tui.DaemonEvent, callback func()) {
	safeAfterFunc(delay, name, events, callback)
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

type runtimeWatcherEventKind int

const (
	runtimeWatcherEventIgnored runtimeWatcherEventKind = iota
	runtimeWatcherEventDaemonSubmitRequest
	runtimeWatcherEventPost
	runtimeWatcherEventRead
)

func classifyRuntimeWatcherEvent(event fswatcher.Event) runtimeWatcherEventKind {
	eventPath := event.Name

	switch {
	case filepath.Base(filepath.Dir(eventPath)) == "requests" && filepath.Base(filepath.Dir(filepath.Dir(eventPath))) == string(projection.SubmitPathDaemon):
		if event.Op&(fswatcher.Create|fswatcher.Rename) != 0 && strings.HasSuffix(filepath.Base(eventPath), ".json") {
			return runtimeWatcherEventDaemonSubmitRequest
		}
	case strings.HasSuffix(filepath.Dir(eventPath), "post"):
		if event.Op&(fswatcher.Create|fswatcher.Rename) != 0 {
			return runtimeWatcherEventPost
		}
	case strings.HasSuffix(filepath.Dir(eventPath), "read"):
		return runtimeWatcherEventRead
	}
	return runtimeWatcherEventIgnored
}

func (rt *daemonRuntime) handleWatcherEvent(event fswatcher.Event) {
	eventPath := event.Name

	switch classifyRuntimeWatcherEvent(event) {
	case runtimeWatcherEventDaemonSubmitRequest:
		rt.handleDaemonSubmitRequest(eventPath)
	case runtimeWatcherEventPost:
		rt.wakePostReconciler(eventPath)
	case runtimeWatcherEventRead:
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

func (rt *daemonRuntime) recordDaemonSubmitSaturation() {
	rt.daemonSubmitSaturationCount++
	rt.daemonSubmitLastSaturatedAt = rt.now()
}

type daemonSubmitDispatchStatus int

const (
	daemonSubmitDispatched daemonSubmitDispatchStatus = iota
	daemonSubmitDispatchDeferred
	daemonSubmitDispatchSaturated
)

func (rt *daemonRuntime) dispatchDaemonSubmitRequest(requestPath string) daemonSubmitDispatchStatus {
	rt.ensureDaemonSubmitRuntime()
	if rt.isRuntimeDiagnosticsSubmitRequest(requestPath) {
		if err := rt.processRuntimeDiagnosticsSubmitRequest(requestPath); err != nil {
			if rt.events != nil {
				rt.events <- tui.DaemonEvent{
					Type:    "error",
					Message: fmt.Sprintf("%s %s: %v", projection.SubmitPathDaemon, filepath.Base(requestPath), err),
				}
			}
		}
		return daemonSubmitDispatched
	}
	dispatchKey := daemonSubmitDispatchKey(requestPath)
	if rt.activeDaemonSubmitKeys[dispatchKey] {
		return daemonSubmitDispatchDeferred
	}
	select {
	case rt.daemonSubmitSem <- struct{}{}:
	default:
		rt.recordDaemonSubmitSaturation()
		return daemonSubmitDispatchSaturated
	}
	rt.activeDaemonSubmitKeys[dispatchKey] = true
	processor := rt.processDaemonSubmit

	worker := func() {
		workerResult := daemonSubmitRuntimeResult{
			requestPath: requestPath,
			dispatchKey: dispatchKey,
		}
		defer func() {
			if r := recover(); r != nil {
				workerResult.err = fmt.Errorf("panic processing %s: %v", filepath.Base(requestPath), r)
			}
			rt.daemonSubmitResults <- workerResult
			<-rt.daemonSubmitSem
		}()
		workerResult.result, workerResult.err = processor(requestPath)
	}
	rt.launchDaemonSubmitWorker(worker)

	return daemonSubmitDispatched
}

func (rt *daemonRuntime) isRuntimeDiagnosticsSubmitRequest(requestPath string) bool {
	request, err := projection.ReadDaemonSubmitRequest(requestPath)
	return err == nil && request.Command == projection.DaemonSubmitRuntimeDiagnostics
}

func (rt *daemonRuntime) processRuntimeDiagnosticsSubmitRequest(requestPath string) error {
	claimedPath, claimed, err := claimDaemonSubmitRequest(requestPath)
	if err != nil || !claimed {
		return err
	}
	defer func() {
		if removeErr := os.Remove(claimedPath); removeErr != nil && !os.IsNotExist(removeErr) {
			log.Printf("postman: WARNING: component=%s event=request_remove_failed submit_path=%s path=%s err=%v\n", projection.SubmitPathDaemon, projection.SubmitPathDaemon, claimedPath, removeErr)
		}
	}()

	sessionDir, ok := daemonSubmitSessionDir(claimedPath)
	if !ok {
		return nil
	}
	request, err := projection.ReadDaemonSubmitRequest(claimedPath)
	if err != nil {
		return err
	}
	response := projection.DaemonSubmitResponse{
		RequestID:          request.RequestID,
		Command:            request.Command,
		HandledAt:          time.Now().UTC().Format(time.RFC3339Nano),
		RuntimeDiagnostics: rt.runtimeDiagnostics(time.Now()),
	}
	if request.RequestID == "" {
		response.Error = "daemon submit runtime-diagnostics missing request_id"
	}
	_, err = projection.WriteDaemonSubmitResponse(sessionDir, response)
	return err
}

func (rt *daemonRuntime) runtimeDiagnostics(now time.Time) *status.RuntimeDiagnostics {
	diag := status.NewRuntimeDiagnostics("daemon_runtime", rt.runtimeCardinality(), rt.daemonSubmitRuntimeDiagnostics(now), rt.nonDaemonDeliveryRuntimeDiagnostics(), now)
	return &diag
}

func (rt *daemonRuntime) logRuntimeDiagnosticsSnapshot(reason string, now time.Time) {
	if reason == "" {
		reason = "interval"
	}
	log.Print(runtimeDiagnosticsLogLine(reason, rt.runtimeDiagnostics(now)))
}

func runtimeDiagnosticsLogLine(reason string, diagnostics *status.RuntimeDiagnostics) string {
	mem := diagnostics.GoRuntime.Memory
	gc := diagnostics.GoRuntime.GC
	daemon := diagnostics.Daemon
	submit := diagnostics.DaemonSubmit
	nonDaemon := diagnostics.NonDaemonDelivery
	rss := currentProcessRSSSnapshot()

	return fmt.Sprintf(
		"postman: component=daemon_runtime event=memory_snapshot source=passive_log reason=%s observed_at=%s %s heap_alloc_bytes=%d heap_sys_bytes=%d heap_objects=%d stack_inuse_bytes=%d total_alloc_bytes=%d memory_sys_bytes=%d memory_frees_count=%d gc_count=%d gc_next_bytes=%d gc_pause_total_ns=%d gc_last_pause_ns=%d goroutine_count=%d daemon_session_count=%d daemon_node_count=%d daemon_watched_dir_count=%d daemon_claimed_pane_count=%d daemon_active_post_event_count=%d daemon_active_auto_ping_count=%d daemon_active_daemon_submit_count=%d daemon_submit_worker_limit=%d daemon_submit_active_worker_count=%d daemon_submit_active_request_count=%d daemon_submit_pending_request_count=%d daemon_submit_oldest_pending_age_seconds=%d daemon_submit_claimed_request_count=%d daemon_submit_oldest_claimed_age_seconds=%d daemon_submit_late_response_count=%d daemon_submit_oldest_late_response_age_seconds=%d daemon_submit_saturation_count=%d daemon_submit_last_saturated_at=%s non_daemon_delivery_worker_limit=%d non_daemon_delivery_active_post_count=%d non_daemon_delivery_pending_post_count=%d non_daemon_delivery_active_auto_ping_count=%d non_daemon_delivery_pending_auto_ping_count=%d non_daemon_delivery_active_manual_ping_count=%d non_daemon_delivery_pending_manual_ping_count=%d non_daemon_delivery_saturation_count=%d non_daemon_delivery_last_saturated_at=%s",
		reason,
		diagnostics.ObservedAt,
		processRSSLogFields(rss),
		mem.HeapAllocBytes,
		mem.HeapSysBytes,
		mem.HeapObjects,
		mem.StackInuseBytes,
		mem.TotalAllocBytes,
		mem.MemorySysBytes,
		mem.MemoryFreesCount,
		gc.Count,
		gc.NextGCBytes,
		gc.PauseTotalNS,
		gc.LastPauseNS,
		diagnostics.GoRuntime.GoroutineCount,
		daemon.SessionCount,
		daemon.NodeCount,
		daemon.WatchedDirCount,
		daemon.ClaimedPaneCount,
		daemon.ActivePostEventCount,
		daemon.ActiveAutoPingCount,
		daemon.ActiveDaemonSubmitCount,
		submit.WorkerLimit,
		submit.ActiveWorkerCount,
		submit.ActiveRequestCount,
		submit.PendingRequestCount,
		submit.OldestPendingAgeSeconds,
		submit.ClaimedRequestCount,
		submit.OldestClaimedAgeSeconds,
		submit.LateResponseCount,
		submit.OldestLateResponseAgeSeconds,
		submit.SaturationCount,
		submit.LastSaturatedAt,
		nonDaemon.WorkerLimit,
		nonDaemon.ActivePostCount,
		nonDaemon.PendingPostCount,
		nonDaemon.ActiveAutoPingCount,
		nonDaemon.PendingAutoPingCount,
		nonDaemon.ActiveManualPingCount,
		nonDaemon.PendingManualPingCount,
		nonDaemon.SaturationCount,
		nonDaemon.LastSaturatedAt,
	)
}

func (rt *daemonRuntime) runtimeCardinality() status.DaemonRuntimeCardinality {
	activePostEventCount := 0
	rt.postEventsMu.Lock()
	activePostEventCount = len(rt.activePostEvents)
	rt.postEventsMu.Unlock()

	activeAutoPingCount := 0
	rt.autoPingEventsMu.Lock()
	activeAutoPingCount = len(rt.activeAutoPings)
	rt.autoPingEventsMu.Unlock()

	sessionNames := map[string]bool{}
	if rt.selfSession != "" {
		sessionNames[rt.selfSession] = true
	}
	if rt.daemonState != nil {
		rt.daemonState.enabledSessionsMu.RLock()
		for sessionName := range rt.daemonState.enabledSessions {
			if sessionName != "" {
				sessionNames[sessionName] = true
			}
		}
		rt.daemonState.enabledSessionsMu.RUnlock()
	}
	for nodeName, nodeInfo := range rt.nodes {
		sessionName := nodeInfo.SessionName
		if sessionName == "" {
			parts := strings.SplitN(nodeName, ":", 2)
			if len(parts) == 2 {
				sessionName = parts[0]
			}
		}
		if sessionName != "" {
			sessionNames[sessionName] = true
		}
	}

	return status.DaemonRuntimeCardinality{
		SessionCount:            len(sessionNames),
		NodeCount:               len(rt.nodes),
		WatchedDirCount:         len(rt.watchedDirs),
		ClaimedPaneCount:        len(rt.claimedPanes),
		ActivePostEventCount:    activePostEventCount,
		ActiveAutoPingCount:     activeAutoPingCount,
		ActiveDaemonSubmitCount: len(rt.activeDaemonSubmitKeys),
	}
}

func (rt *daemonRuntime) daemonSubmitRuntimeDiagnostics(now time.Time) status.DaemonSubmitRuntimeDiagnostics {
	diagnostics := status.DaemonSubmitRuntimeDiagnostics{
		WorkerLimit:        rt.daemonSubmitWorkerLimit(),
		ActiveWorkerCount:  len(rt.daemonSubmitSem),
		ActiveRequestCount: len(rt.activeDaemonSubmitKeys),
		SaturationCount:    rt.daemonSubmitSaturationCount,
	}
	if !rt.daemonSubmitLastSaturatedAt.IsZero() {
		diagnostics.LastSaturatedAt = rt.daemonSubmitLastSaturatedAt.UTC().Format(time.RFC3339Nano)
	}

	for _, sessionDir := range runtimeSessionDirs(rt.sessionDir, rt.nodes) {
		scanDaemonSubmitRequests(sessionDir, now, &diagnostics)
		scanDaemonSubmitResponses(sessionDir, now, &diagnostics)
	}
	return diagnostics
}

func (rt *daemonRuntime) daemonSubmitWorkerLimit() int {
	if rt.daemonSubmitSem != nil {
		return cap(rt.daemonSubmitSem)
	}
	return daemonSubmitWorkerLimitFromConfig(rt.cfg)
}

func (rt *daemonRuntime) nonDaemonDeliveryBudget() *nonDaemonDeliveryBudget {
	if rt.daemonState != nil {
		return rt.daemonState.nonDaemonDeliveryBudgetForUse()
	}
	if rt.nonDaemonDeliveryBudgetFallback == nil {
		rt.nonDaemonDeliveryBudgetFallback = newNonDaemonDeliveryBudget(rt.now)
	}
	return rt.nonDaemonDeliveryBudgetFallback
}

func (rt *daemonRuntime) nonDaemonDeliveryRuntimeDiagnostics() status.NonDaemonDeliveryRuntimeDiagnostics {
	return rt.nonDaemonDeliveryBudget().snapshot()
}

func scanDaemonSubmitRequests(sessionDir string, now time.Time, diagnostics *status.DaemonSubmitRuntimeDiagnostics) {
	entries, err := os.ReadDir(projection.DaemonSubmitRequestsDir(sessionDir))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		requestPath := filepath.Join(projection.DaemonSubmitRequestsDir(sessionDir), entry.Name())
		state := ""
		switch {
		case strings.HasSuffix(entry.Name(), ".json"):
			state = "pending"
		case strings.HasSuffix(entry.Name(), ".processing"):
			state = "claimed"
		default:
			continue
		}

		request, err := projection.ReadDaemonSubmitRequest(requestPath)
		if err == nil && request.Command == projection.DaemonSubmitRuntimeDiagnostics {
			continue
		}
		switch state {
		case "pending":
			diagnostics.PendingRequestCount++
			if err == nil {
				diagnostics.OldestPendingAgeSeconds = oldestDaemonSubmitAgeSeconds(diagnostics.OldestPendingAgeSeconds, request.CreatedAt, now)
			}
		case "claimed":
			diagnostics.ClaimedRequestCount++
			if err == nil {
				diagnostics.OldestClaimedAgeSeconds = oldestDaemonSubmitAgeSeconds(diagnostics.OldestClaimedAgeSeconds, request.CreatedAt, now)
			}
		}
	}
}

func scanDaemonSubmitResponses(sessionDir string, now time.Time, diagnostics *status.DaemonSubmitRuntimeDiagnostics) {
	entries, err := os.ReadDir(projection.DaemonSubmitResponsesDir(sessionDir))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		responsePath := filepath.Join(projection.DaemonSubmitResponsesDir(sessionDir), entry.Name())
		response, err := projection.ReadDaemonSubmitResponse(responsePath)
		if err == nil && response.Command == projection.DaemonSubmitRuntimeDiagnostics {
			continue
		}

		diagnostics.LateResponseCount++
		if err == nil {
			diagnostics.OldestLateResponseAgeSeconds = oldestDaemonSubmitAgeSeconds(diagnostics.OldestLateResponseAgeSeconds, response.HandledAt, now)
			continue
		}
		info, infoErr := entry.Info()
		if infoErr == nil {
			diagnostics.OldestLateResponseAgeSeconds = oldestDaemonSubmitAgeSecondsFromTime(diagnostics.OldestLateResponseAgeSeconds, info.ModTime(), now)
		}
	}
}

func oldestDaemonSubmitAgeSeconds(current int, timestamp string, now time.Time) int {
	if timestamp == "" {
		return current
	}
	parsed, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return current
	}
	return oldestDaemonSubmitAgeSecondsFromTime(current, parsed, now)
}

func oldestDaemonSubmitAgeSecondsFromTime(current int, timestamp time.Time, now time.Time) int {
	if timestamp.IsZero() {
		return current
	}
	age := 0
	if timestamp.Before(now) {
		age = int(now.Sub(timestamp).Seconds())
	}
	if age > current {
		return age
	}
	return current
}

func (rt *daemonRuntime) ensureDaemonSubmitRuntime() {
	if rt.processDaemonSubmit == nil {
		rt.processDaemonSubmit = processDaemonSubmitRequest
	}
	if rt.launchDaemonSubmitWorker == nil {
		rt.launchDaemonSubmitWorker = defaultDaemonSubmitWorkerLauncher
	}
	if rt.daemonSubmitSem == nil {
		rt.daemonSubmitSem = make(chan struct{}, daemonSubmitWorkerLimitFromConfig(rt.cfg))
	}
	if rt.daemonSubmitResults == nil {
		rt.daemonSubmitResults = make(chan daemonSubmitRuntimeResult, daemonSubmitWorkerLimitFromConfig(rt.cfg))
	}
	if rt.activeDaemonSubmitKeys == nil {
		rt.activeDaemonSubmitKeys = make(map[string]bool)
	}
}

func daemonSubmitDispatchKey(requestPath string) string {
	if sessionDir, ok := daemonSubmitSessionDir(requestPath); ok {
		request, err := projection.ReadDaemonSubmitRequest(requestPath)
		if err != nil {
			return "session:" + sessionDir
		}
		switch request.Command {
		case projection.DaemonSubmitPop:
			if request.Node == "" {
				return "pop:" + sessionDir
			}
			return "pop:" + sessionDir + ":" + request.Node
		case projection.DaemonSubmitSend:
			return "send:" + requestPath
		default:
			return "request:" + requestPath
		}
	}
	return "path:" + requestPath
}

func (rt *daemonRuntime) handleDaemonSubmitResult(workerResult daemonSubmitRuntimeResult) {
	rt.ensureDaemonSubmitRuntime()
	delete(rt.activeDaemonSubmitKeys, workerResult.dispatchKey)
	if workerResult.err != nil {
		rt.events <- tui.DaemonEvent{
			Type:    "error",
			Message: fmt.Sprintf("%s %s: %v", projection.SubmitPathDaemon, filepath.Base(workerResult.requestPath), workerResult.err),
		}
		return
	}
	if workerResult.result.ProjectionSyncSessionDir != "" {
		rt.scheduleMailboxProjectionSync(workerResult.result.ProjectionSyncSessionDir)
	}
	if workerResult.result.hasPostDispatch() {
		log.Printf("postman: component=%s event=send_reconcile submit_path=%s session=%s file=%s\n",
			projection.SubmitPathDaemon, projection.SubmitPathDaemon, filepath.Base(workerResult.result.SessionDir), workerResult.result.Filename)
		rt.wakePostReconciler(workerResult.result.PostPath)
	}
	rt.dispatchPendingDaemonSubmitRequests()
}

func (rt *daemonRuntime) dispatchPendingDaemonSubmitRequests() {
	pendingBySession := rt.pendingDaemonSubmitRequestsBySession()
	for {
		dispatchedInRound := false
		for i := range pendingBySession {
			pending := &pendingBySession[i]
			for pending.next < len(pending.names) {
				name := pending.names[pending.next]
				pending.next++
				status := rt.dispatchDaemonSubmitRequest(filepath.Join(pending.requestsDir, name))
				if status == daemonSubmitDispatchSaturated {
					return
				}
				if status == daemonSubmitDispatched {
					dispatchedInRound = true
					break
				}
			}
		}
		if !dispatchedInRound {
			return
		}
	}
}

type pendingDaemonSubmitSessionRequests struct {
	requestsDir string
	names       []string
	next        int
}

func (rt *daemonRuntime) pendingDaemonSubmitRequestsBySession() []pendingDaemonSubmitSessionRequests {
	pendingBySession := []pendingDaemonSubmitSessionRequests{}
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
		if len(names) > 0 {
			pendingBySession = append(pendingBySession, pendingDaemonSubmitSessionRequests{
				requestsDir: requestsDir,
				names:       names,
			})
		}
	}
	return pendingBySession
}

func (rt *daemonRuntime) handlePostWatcherEvent(eventPath string, op fswatcher.Op) {
	if op&(fswatcher.Create|fswatcher.Rename) == 0 {
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
	rt.handlePostWatcherEvent(post.Path, fswatcher.Create)
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
		rt.pruneWatchedDirs(freshNodes)
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
	scheduler := rt.scheduleRuntimeTimer
	if scheduler == nil {
		scheduler = defaultRuntimeTimerScheduler
	}
	scheduler(remaining, "post-rate-limit-retry", rt.events, func() {
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
	budget := rt.nonDaemonDeliveryBudget()
	if !budget.tryStart(nonDaemonDeliveryPathPost) {
		budget.queue(nonDaemonDeliveryPathPost)
		log.Printf("postman: WARNING: component=non_daemon_delivery event=workers_saturated path=post file=%s retry_in=%s\n", filename, nonDaemonDeliveryRetryDelay)
		scheduler := rt.scheduleRuntimeTimer
		if scheduler == nil {
			scheduler = defaultRuntimeTimerScheduler
		}
		scheduler(nonDaemonDeliveryRetryDelay, "post-delivery-budget-retry", rt.events, func() {
			budget.unqueue(nonDaemonDeliveryPathPost)
			rt.dispatchPostDelivery(eventPath, filename, nodes, adjacency, cfg, reservation)
		})
		return
	}
	go func(eventPath, filename string, nodes map[string]discovery.NodeInfo, adjacency map[string][]string, cfg *config.Config) {
		deliveredNormally := false
		defer func() {
			budget.finish(nonDaemonDeliveryPathPost)
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

func (rt *daemonRuntime) handleReadWatcherEvent(eventPath string, op fswatcher.Op) {
	if op&(fswatcher.Create|fswatcher.Rename) == 0 {
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
	rt.scheduleMailboxProjectionSync(sourceSessionDir)

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

func (rt *daemonRuntime) ensureMailboxProjectionSyncRuntime() {
	if rt.activeMailboxProjectionSyncs == nil {
		rt.activeMailboxProjectionSyncs = make(map[string]bool)
	}
	if rt.pendingMailboxProjectionSyncs == nil {
		rt.pendingMailboxProjectionSyncs = make(map[string]bool)
	}
}

func (rt *daemonRuntime) scheduleMailboxProjectionSync(sessionDir string) {
	if sessionDir == "" {
		return
	}
	rt.ensureMailboxProjectionSyncRuntime()
	rt.mailboxProjectionSyncMu.Lock()
	if rt.activeMailboxProjectionSyncs[sessionDir] {
		rt.pendingMailboxProjectionSyncs[sessionDir] = true
		rt.mailboxProjectionSyncMu.Unlock()
		return
	}
	rt.activeMailboxProjectionSyncs[sessionDir] = true
	rt.mailboxProjectionSyncMu.Unlock()

	rt.mailboxProjectionSyncWG.Add(1)
	go func() {
		defer rt.mailboxProjectionSyncWG.Done()
		rt.runMailboxProjectionSync(sessionDir)
	}()
}

func (rt *daemonRuntime) runMailboxProjectionSync(sessionDir string) {
	for {
		syncMailboxProjection(sessionDir)

		rt.mailboxProjectionSyncMu.Lock()
		if !rt.pendingMailboxProjectionSyncs[sessionDir] {
			delete(rt.activeMailboxProjectionSyncs, sessionDir)
			rt.mailboxProjectionSyncMu.Unlock()
			return
		}
		delete(rt.pendingMailboxProjectionSyncs, sessionDir)
		rt.mailboxProjectionSyncMu.Unlock()
	}
}

func (rt *daemonRuntime) waitForMailboxProjectionSyncs() {
	rt.mailboxProjectionSyncWG.Wait()
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
	rt.pruneWatchedDirs(freshNodes)
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
	rt.pruneWatchedDirs(freshNodes)
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
		if err := rt.watcher.Add(nodePostDir, fswatcher.All); err == nil {
			rt.watchedDirs[nodePostDir] = true
		}
	}
	if !rt.watchedDirs[nodeInboxDir] {
		if err := rt.watcher.Add(nodeInboxDir, fswatcher.All); err == nil {
			rt.watchedDirs[nodeInboxDir] = true
		}
	}
	if !rt.watchedDirs[nodeReadDir] {
		if err := rt.watcher.Add(nodeReadDir, fswatcher.All); err == nil {
			rt.watchedDirs[nodeReadDir] = true
		}
	}

	submitRequestsDir := projection.DaemonSubmitRequestsDir(nodeInfo.SessionDir)
	if err := projection.EnsureDaemonSubmitDirs(nodeInfo.SessionDir); err != nil {
		log.Printf("postman: WARNING: component=%s event=dirs_create_failed submit_path=%s node=%s err=%v\n", projection.SubmitPathDaemon, projection.SubmitPathDaemon, nodeName, err)
		return
	}
	if !rt.watchedDirs[submitRequestsDir] {
		if err := rt.watcher.Add(submitRequestsDir, fswatcher.All); err == nil {
			rt.watchedDirs[submitRequestsDir] = true
		}
	}
}

func nodeWatchDirs(nodeInfo discovery.NodeInfo) []string {
	if nodeInfo.SessionDir == "" {
		return nil
	}
	return []string{
		filepath.Join(nodeInfo.SessionDir, "post"),
		filepath.Join(nodeInfo.SessionDir, "inbox"),
		filepath.Join(nodeInfo.SessionDir, "read"),
		projection.DaemonSubmitRequestsDir(nodeInfo.SessionDir),
	}
}

func desiredWatchDirsForNodes(nodes map[string]discovery.NodeInfo) map[string]bool {
	desired := make(map[string]bool, len(nodes)*4)
	for _, nodeInfo := range nodes {
		for _, dir := range nodeWatchDirs(nodeInfo) {
			if dir != "" {
				desired[dir] = true
			}
		}
	}
	return desired
}

func (rt *daemonRuntime) pruneWatchedDirs(freshNodes map[string]discovery.NodeInfo) {
	desired := desiredWatchDirsForNodes(freshNodes)
	for dir := range rt.watchedDirs {
		if desired[dir] {
			continue
		}
		if rt.watcher != nil {
			if err := rt.watcher.Remove(dir); err != nil {
				log.Printf("postman: WARNING: failed to remove watcher dir %s: %v\n", dir, err)
				continue
			}
		}
		delete(rt.watchedDirs, dir)
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
		rt.dispatchAutoPingDelivery(dispatchNodeKey, dispatchNodeInfo, dispatchPending, dispatchTemplate, dispatchActiveNodes, dispatchLivenessMap, dispatchAdjacency, dispatchNodes)
	}
}

func (rt *daemonRuntime) dispatchAutoPingDelivery(nodeKey string, nodeInfo discovery.NodeInfo, pending projection.AutoPingNodeState, tmpl string, activeNodes []string, livenessMap map[string]bool, adjacency map[string][]string, nodes map[string]discovery.NodeInfo) {
	budget := rt.nonDaemonDeliveryBudget()
	if !budget.tryStart(nonDaemonDeliveryPathAutoPing) {
		budget.queue(nonDaemonDeliveryPathAutoPing)
		log.Printf("postman: WARNING: component=non_daemon_delivery event=workers_saturated path=auto_ping node=%s retry_in=%s\n", nodeKey, nonDaemonDeliveryRetryDelay)
		scheduler := rt.scheduleRuntimeTimer
		if scheduler == nil {
			scheduler = defaultRuntimeTimerScheduler
		}
		scheduler(nonDaemonDeliveryRetryDelay, "auto-ping-budget-retry", rt.events, func() {
			budget.unqueue(nonDaemonDeliveryPathAutoPing)
			rt.dispatchAutoPingDelivery(nodeKey, nodeInfo, pending, tmpl, activeNodes, livenessMap, adjacency, nodes)
		})
		return
	}

	sendAutoPing := rt.autoPingSender()
	go func() {
		defer budget.finish(nonDaemonDeliveryPathAutoPing)
		defer rt.finishAutoPing(nodeKey)

		result, err := sendAutoPing(nodeInfo, rt.contextID, nodeKey, tmpl, rt.cfg, activeNodes, livenessMap, adjacency, nodes)
		if err != nil {
			log.Printf("postman: WARNING: auto-PING send failed for %s: %v\n", nodeKey, err)
			return
		}
		if !result.Delivered {
			return
		}

		rt.recordDeliveredAutoPing(nodeKey, nodeInfo, pending, rt.now())
	}()
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
