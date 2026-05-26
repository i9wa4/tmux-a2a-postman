package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fswatcher/fswatcher"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/controlplane"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

func waitForInboxEntries(t *testing.T, sessionDir, nodeName string, want int) {
	t.Helper()
	inboxDir := filepath.Join(sessionDir, "inbox", nodeName)
	deadline := time.Now().Add(2 * time.Second)
	for {
		entries, err := os.ReadDir(inboxDir)
		if err == nil && len(entries) == want {
			return
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("ReadDir inbox: %v", err)
			}
			t.Fatalf("inbox entries = %d, want %d", len(entries), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForDaemonSubmitResult(t *testing.T, rt *daemonRuntime) daemonSubmitRuntimeResult {
	t.Helper()
	rt.ensureDaemonSubmitRuntime()
	select {
	case result := <-rt.daemonSubmitResults:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for daemon-submit worker result")
		return daemonSubmitRuntimeResult{}
	}
}

func waitForAutoPingPending(t *testing.T, sessionDir, nodeKey string, want bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		state, ok, err := projection.ProjectAutoPingState(sessionDir)
		if err != nil {
			t.Fatalf("ProjectAutoPingState() error = %v", err)
		}
		if ok {
			if node, exists := state.Nodes[nodeKey]; exists && node.Pending == want {
				return
			}
		}
		if time.Now().After(deadline) {
			state, _, _ := projection.ProjectAutoPingState(sessionDir)
			t.Fatalf("auto-PING pending[%s] did not become %v; state=%#v", nodeKey, want, state.Nodes[nodeKey])
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDispatchRuntimeDiagnosticsSubmitRequestWritesBoundedCounts(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-diagnostics",
		Command:   projection.DaemonSubmitRuntimeDiagnostics,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	daemonState := NewDaemonState(0, "ctx-runtime")
	daemonState.AutoEnableSessionIfNew("review")
	daemonState.AutoEnableSessionIfNew("qa")
	rt := &daemonRuntime{
		selfSession: "review",
		nodes: map[string]discovery.NodeInfo{
			"review:worker": {SessionName: "review"},
			"qa:critic":     {SessionName: "qa"},
		},
		watchedDirs: map[string]bool{
			"/tmp/private/review/post":  true,
			"/tmp/private/review/inbox": true,
		},
		claimedPanes: map[string]bool{
			"%42": true,
		},
		activePostEvents: map[string]bool{
			"/tmp/private/review/post/20260524-120000-r1111-from-a-to-b.md": true,
		},
		activeAutoPings: map[string]bool{
			"review:worker": true,
		},
		activeDaemonSubmitKeys: map[string]bool{
			"send:/tmp/private/request.json": true,
		},
		daemonState: daemonState,
	}

	if got := rt.dispatchDaemonSubmitRequest(requestPath); got != daemonSubmitDispatched {
		t.Fatalf("dispatchDaemonSubmitRequest() = %v, want daemonSubmitDispatched", got)
	}

	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-diagnostics"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.RuntimeDiagnostics == nil {
		t.Fatal("RuntimeDiagnostics = nil")
	}
	diagnostics := response.RuntimeDiagnostics
	if diagnostics.Source != "daemon_runtime" {
		t.Fatalf("Source = %q, want daemon_runtime", diagnostics.Source)
	}
	if !diagnostics.PointInTime {
		t.Fatal("PointInTime = false, want true")
	}
	if diagnostics.GoRuntime.GoroutineCount <= 0 {
		t.Fatalf("GoroutineCount = %d, want positive", diagnostics.GoRuntime.GoroutineCount)
	}
	if diagnostics.Daemon != (status.DaemonRuntimeCardinality{
		SessionCount:            2,
		NodeCount:               2,
		WatchedDirCount:         2,
		ClaimedPaneCount:        1,
		ActivePostEventCount:    1,
		ActiveAutoPingCount:     1,
		ActiveDaemonSubmitCount: 1,
	}) {
		t.Fatalf("Daemon cardinality = %#v", diagnostics.Daemon)
	}

	payload, err := json.Marshal(diagnostics)
	if err != nil {
		t.Fatalf("Marshal diagnostics: %v", err)
	}
	for _, forbidden := range []string{
		"/tmp/private",
		"20260524-120000-r1111-from-a-to-b.md",
		"review:worker",
		"%42",
		"request.json",
	} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("diagnostics leaked %q in %s", forbidden, payload)
		}
	}
	assertRuntimeDiagnosticsHasNoArrays(t, diagnostics)
}

func assertRuntimeDiagnosticsHasNoArrays(t *testing.T, diagnostics *status.RuntimeDiagnostics) {
	t.Helper()
	payload, err := json.Marshal(diagnostics)
	if err != nil {
		t.Fatalf("Marshal diagnostics: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("Unmarshal diagnostics: %v", err)
	}
	var walk func(any)
	walk = func(value any) {
		t.Helper()
		switch typed := value.(type) {
		case []any:
			t.Fatalf("diagnostics contained an array: %s", payload)
		case map[string]any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(decoded)
}

func TestBuildRuntimeStatusSnapshot_SortsSessionNamesAndNormalizesSessionNodes(t *testing.T) {
	nodes := map[string]discovery.NodeInfo{
		"bravo:worker":   {SessionName: "bravo"},
		"alpha:worker":   {SessionName: "alpha"},
		"alpha:critic":   {SessionName: "alpha"},
		"delta:observer": {SessionName: "delta"},
	}

	snapshot := buildRuntimeStatusSnapshot(nodes, []string{"bravo", "alpha", "charlie"}, func(sessionName string) bool {
		return sessionName != "charlie"
	})

	wantNames := []string{"alpha", "bravo", "charlie", "delta"}
	if !reflect.DeepEqual(snapshot.NormalizedSessionNames, wantNames) {
		t.Fatalf("NormalizedSessionNames = %#v, want %#v", snapshot.NormalizedSessionNames, wantNames)
	}
	if got := snapshot.NormalizedSessionNodes["alpha"]; !reflect.DeepEqual(got, []string{"critic", "worker"}) {
		t.Fatalf("NormalizedSessionNodes[alpha] = %#v, want %#v", got, []string{"critic", "worker"})
	}
	if got := snapshot.NormalizedSessionNodes["bravo"]; !reflect.DeepEqual(got, []string{"worker"}) {
		t.Fatalf("NormalizedSessionNodes[bravo] = %#v, want %#v", got, []string{"worker"})
	}
	if !snapshot.changed(3, wantNames, map[string][]string{
		"alpha": {"critic", "worker"},
		"bravo": {"worker"},
		"delta": {"observer"},
	}) {
		t.Fatal("snapshot.changed() = false, want true when node count changed")
	}
	if snapshot.changed(4, wantNames, map[string][]string{
		"alpha": {"critic", "worker"},
		"bravo": {"worker"},
		"delta": {"observer"},
	}) {
		t.Fatal("snapshot.changed() = true, want false for identical normalized state")
	}
}

func TestSessionScanIntervalUsesPublicConfigAndFallback(t *testing.T) {
	cfg := &config.Config{
		ScanInterval:        1.0,
		SessionScanInterval: 0.1,
	}
	if got, want := sessionScanInterval(cfg), 100*time.Millisecond; got != want {
		t.Fatalf("sessionScanInterval() = %s, want %s", got, want)
	}

	cfg.SessionScanInterval = 0
	if got, want := sessionScanInterval(cfg), time.Second; got != want {
		t.Fatalf("sessionScanInterval() fallback = %s, want %s", got, want)
	}

	cfg.ScanInterval = 0
	if got, want := sessionScanInterval(cfg), time.Second; got != want {
		t.Fatalf("sessionScanInterval() zero fallback = %s, want %s", got, want)
	}
}

func TestHandleSessionScanTick_EmitsNewSessionWithoutPaneScan(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := installRuntimeSessionScanTmux(t, tmpDir, []string{"main", "review"})
	events := make(chan tui.DaemonEvent, 1)

	rt := &daemonRuntime{
		nodes: map[string]discovery.NodeInfo{
			"main:worker": {SessionName: "main"},
		},
		events:           events,
		daemonState:      NewDaemonState(0, "ctx-fast-session"),
		prevNodeCount:    1,
		prevSessionNames: []string{"main"},
		prevSessionNodes: map[string][]string{
			"main": {"worker"},
		},
	}

	rt.handleSessionScanTick()

	select {
	case event := <-events:
		if event.Type != "status_update" {
			t.Fatalf("event.Type = %q, want status_update", event.Type)
		}
		sessions, ok := event.Details["sessions"].([]tui.SessionInfo)
		if !ok {
			t.Fatalf("sessions detail type = %T, want []tui.SessionInfo", event.Details["sessions"])
		}
		if !sessionInfoExists(sessions, "review", 0) {
			t.Fatalf("sessions = %#v, want review session with zero nodes", sessions)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for status_update")
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(tmux log): %v", err)
	}
	logText := string(logBytes)
	if !strings.Contains(logText, "list-sessions -F") {
		t.Fatalf("tmux log missing list-sessions call: %q", logText)
	}
	if strings.Contains(logText, "list-panes") {
		t.Fatalf("session scan should not run pane discovery; tmux log: %q", logText)
	}
}

func TestHandleSessionScanTick_AutoActivatesNewSessionWithConfiguredPanes(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "state")
	contextID := "ctx-fast-session"
	selfSession := "main"
	targetSession := "review"
	contextDir := filepath.Join(baseDir, contextID)
	if err := config.CreateMultiSessionDirs(contextDir, selfSession); err != nil {
		t.Fatalf("CreateMultiSessionDirs(self): %v", err)
	}
	if err := os.WriteFile(filepath.Join(contextDir, selfSession, "postman.pid"), []byte(fmt.Sprint(os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile(postman.pid): %v", err)
	}

	logPath := installRuntimeSessionScanActivationTmux(t, tmpDir, []string{selfSession, targetSession})
	watcher, err := fswatcher.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher(): %v", err)
	}
	defer func() { _ = watcher.Close() }()
	events := make(chan tui.DaemonEvent, 4)

	rt := &daemonRuntime{
		baseDir:      baseDir,
		sessionDir:   filepath.Join(contextDir, selfSession),
		contextID:    contextID,
		selfSession:  selfSession,
		cfg:          config.DefaultConfig(),
		watcher:      watcher,
		knownNodes:   map[string]bool{},
		watchedDirs:  map[string]bool{},
		claimedPanes: map[string]bool{},
		daemonState:  NewDaemonState(0, contextID),
		events:       events,
	}
	rt.cfg.Edges = []string{"messenger --- orchestrator"}

	rt.handleSessionScanTick()

	if !rt.daemonState.GetConfiguredSessionEnabled(targetSession) {
		t.Fatalf("target session was not auto-enabled")
	}
	for _, subdir := range []string{"post", "inbox", "read"} {
		if _, err := os.Stat(filepath.Join(contextDir, targetSession, subdir)); err != nil {
			t.Fatalf("target session %s dir missing: %v", subdir, err)
		}
	}
	if _, ok := rt.nodes[targetSession+":messenger"]; !ok {
		t.Fatalf("rt.nodes missing activated messenger: %#v", rt.nodes)
	}
	if _, ok := rt.nodes[targetSession+":orchestrator"]; !ok {
		t.Fatalf("rt.nodes missing activated orchestrator: %#v", rt.nodes)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(tmux log): %v", err)
	}
	logText := string(logBytes)
	for _, want := range []string{
		"set-option -p -t %201 @a2a_context_id ctx-fast-session",
		"set-option -p -t %202 @a2a_context_id ctx-fast-session",
		"set-option -g @a2a_session_on_review ctx-fast-session:",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("tmux log missing %q: %q", want, logText)
		}
	}
}

func TestHandleSessionScanTick_AutoActivatesNewSessionWithNodesOnlyConfiguredPanes(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "state")
	contextID := "ctx-fast-session"
	selfSession := "main"
	targetSession := "review"
	contextDir := filepath.Join(baseDir, contextID)
	if err := config.CreateMultiSessionDirs(contextDir, selfSession); err != nil {
		t.Fatalf("CreateMultiSessionDirs(self): %v", err)
	}
	if err := os.WriteFile(filepath.Join(contextDir, selfSession, "postman.pid"), []byte(fmt.Sprint(os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile(postman.pid): %v", err)
	}

	logPath := installRuntimeSessionScanActivationTmuxWithPanes(t, tmpDir, []string{selfSession, targetSession}, []string{"worker", "critic", "unrelated"})
	watcher, err := fswatcher.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher(): %v", err)
	}
	defer func() { _ = watcher.Close() }()
	events := make(chan tui.DaemonEvent, 4)

	rt := &daemonRuntime{
		baseDir:      baseDir,
		sessionDir:   filepath.Join(contextDir, selfSession),
		contextID:    contextID,
		selfSession:  selfSession,
		cfg:          config.DefaultConfig(),
		watcher:      watcher,
		knownNodes:   map[string]bool{},
		watchedDirs:  map[string]bool{},
		claimedPanes: map[string]bool{},
		daemonState:  NewDaemonState(0, contextID),
		events:       events,
	}
	rt.cfg.Edges = nil
	rt.cfg.NodeOrder = []string{"worker", "critic"}
	rt.cfg.Nodes = map[string]config.NodeConfig{
		"worker": {},
		"critic": {},
	}

	rt.handleSessionScanTick()

	if !rt.daemonState.GetConfiguredSessionEnabled(targetSession) {
		t.Fatalf("target session was not auto-enabled")
	}
	if _, ok := rt.nodes[targetSession+":worker"]; !ok {
		t.Fatalf("rt.nodes missing activated worker: %#v", rt.nodes)
	}
	if _, ok := rt.nodes[targetSession+":critic"]; !ok {
		t.Fatalf("rt.nodes missing activated critic: %#v", rt.nodes)
	}
	if _, ok := rt.nodes[targetSession+":unrelated"]; ok {
		t.Fatalf("rt.nodes contains unrelated pane: %#v", rt.nodes)
	}
	for _, subdir := range []string{"post", "inbox", "read"} {
		watchDir := filepath.Join(contextDir, targetSession, subdir)
		if !rt.watchedDirs[watchDir] {
			t.Fatalf("target session %s watch missing", subdir)
		}
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(tmux log): %v", err)
	}
	logText := string(logBytes)
	for _, want := range []string{
		"set-option -p -t %201 @a2a_context_id ctx-fast-session",
		"set-option -p -t %202 @a2a_context_id ctx-fast-session",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("tmux log missing %q: %q", want, logText)
		}
	}
	if strings.Contains(logText, "set-option -p -t %203 @a2a_context_id ctx-fast-session") {
		t.Fatalf("tmux log claimed unrelated pane: %q", logText)
	}
}

func TestHandleWatcherEvent_DaemonSubmitWorkerDoesNotBlockSessionStatusTick(t *testing.T) {
	tmpDir := t.TempDir()
	installRuntimeSessionScanTmux(t, tmpDir, []string{"main", "review"})

	baseDir := filepath.Join(tmpDir, "state")
	contextID := "ctx-submit-worker"
	sessionDir := filepath.Join(baseDir, contextID, "main")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-blocking",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	workerStarted := make(chan struct{})
	releaseWorker := make(chan struct{})
	events := make(chan tui.DaemonEvent, 2)
	rt := &daemonRuntime{
		nodes: map[string]discovery.NodeInfo{
			"main:worker": {SessionName: "main"},
		},
		events:           events,
		daemonState:      NewDaemonState(0, contextID),
		prevNodeCount:    1,
		prevSessionNames: []string{"main"},
		prevSessionNodes: map[string][]string{
			"main": {"worker"},
		},
		processDaemonSubmit: func(requestPath string) (daemonSubmitProcessResult, error) {
			close(workerStarted)
			<-releaseWorker
			return daemonSubmitProcessResult{}, nil
		},
	}
	defer close(releaseWorker)

	done := make(chan struct{})
	go func() {
		rt.handleWatcherEvent(fswatcher.Event{Name: requestPath, Op: fswatcher.Create})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("daemon-submit watcher event blocked on request processing")
	}
	select {
	case <-workerStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("daemon-submit worker did not start")
	}

	rt.handleSessionScanTick()
	select {
	case event := <-events:
		if event.Type != "status_update" {
			t.Fatalf("event.Type = %q, want status_update", event.Type)
		}
		sessions, ok := event.Details["sessions"].([]tui.SessionInfo)
		if !ok {
			t.Fatalf("sessions detail type = %T, want []tui.SessionInfo", event.Details["sessions"])
		}
		if !sessionInfoExists(sessions, "review", 0) {
			t.Fatalf("sessions = %#v, want review session with zero nodes", sessions)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for status_update while daemon-submit worker was blocked")
	}
}

func TestDispatchDaemonSubmitRequest_AllowsSendWhilePopActive(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	popPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest(pop): %v", err)
	}
	sendPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-send",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  "20260521-093000-r1111-from-orchestrator-to-worker.md",
		Content:   "hello",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest(send): %v", err)
	}

	started := make(chan projection.DaemonSubmitCommand, 2)
	releasePop := make(chan struct{})
	rt := &daemonRuntime{
		processDaemonSubmit: func(requestPath string) (daemonSubmitProcessResult, error) {
			request, err := projection.ReadDaemonSubmitRequest(requestPath)
			if err != nil {
				return daemonSubmitProcessResult{}, err
			}
			started <- request.Command
			if request.Command == projection.DaemonSubmitPop {
				<-releasePop
			}
			return daemonSubmitProcessResult{}, nil
		},
	}
	defer close(releasePop)

	if status := rt.dispatchDaemonSubmitRequest(popPath); status != daemonSubmitDispatched {
		t.Fatalf("dispatch pop status = %v, want dispatched", status)
	}
	if got := <-started; got != projection.DaemonSubmitPop {
		t.Fatalf("first started command = %q, want pop", got)
	}

	if status := rt.dispatchDaemonSubmitRequest(sendPath); status != daemonSubmitDispatched {
		t.Fatalf("dispatch send status = %v, want dispatched while pop is active", status)
	}
	select {
	case got := <-started:
		if got != projection.DaemonSubmitSend {
			t.Fatalf("second started command = %q, want send", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("send did not start while unrelated pop was active")
	}
}

func TestDispatchDaemonSubmitRequest_SerializesSameNodePopOnly(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	popWorker1, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-worker-1",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest(worker 1): %v", err)
	}
	popWorker2, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-worker-2",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest(worker 2): %v", err)
	}
	popReviewer, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-reviewer",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Node:      "reviewer",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest(reviewer): %v", err)
	}

	started := make(chan string, 2)
	release := make(chan struct{})
	rt := &daemonRuntime{
		processDaemonSubmit: func(requestPath string) (daemonSubmitProcessResult, error) {
			request, err := projection.ReadDaemonSubmitRequest(requestPath)
			if err != nil {
				return daemonSubmitProcessResult{}, err
			}
			started <- request.RequestID
			<-release
			return daemonSubmitProcessResult{}, nil
		},
	}
	defer close(release)

	if status := rt.dispatchDaemonSubmitRequest(popWorker1); status != daemonSubmitDispatched {
		t.Fatalf("dispatch first worker pop status = %v, want dispatched", status)
	}
	if got := <-started; got != "req-pop-worker-1" {
		t.Fatalf("first started request = %q, want req-pop-worker-1", got)
	}
	if status := rt.dispatchDaemonSubmitRequest(popWorker2); status != daemonSubmitDispatchDeferred {
		t.Fatalf("dispatch second worker pop status = %v, want deferred", status)
	}
	if status := rt.dispatchDaemonSubmitRequest(popReviewer); status != daemonSubmitDispatched {
		t.Fatalf("dispatch reviewer pop status = %v, want dispatched", status)
	}
	select {
	case got := <-started:
		if got != "req-pop-reviewer" {
			t.Fatalf("second started request = %q, want req-pop-reviewer", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("different-node pop did not start while worker pop was active")
	}
}

func TestHandleSessionScanTick_EmitsRenameAndDeleteSnapshots(t *testing.T) {
	tmpDir := t.TempDir()
	installRuntimeSessionScanTmux(t, tmpDir, []string{"review"})
	events := make(chan tui.DaemonEvent, 2)

	rt := &daemonRuntime{
		events:           events,
		daemonState:      NewDaemonState(0, "ctx-session-lifecycle"),
		prevNodeCount:    0,
		prevSessionNames: []string{"main"},
		prevSessionNodes: map[string][]string{},
	}

	rt.handleSessionScanTick()

	renameEvent := <-events
	sessions, ok := renameEvent.Details["sessions"].([]tui.SessionInfo)
	if !ok {
		t.Fatalf("rename sessions detail type = %T, want []tui.SessionInfo", renameEvent.Details["sessions"])
	}
	if sessionInfoExists(sessions, "main", 0) {
		t.Fatalf("rename sessions = %#v, did not expect old session name", sessions)
	}
	if !sessionInfoExists(sessions, "review", 0) {
		t.Fatalf("rename sessions = %#v, want review session", sessions)
	}

	installRuntimeSessionScanTmux(t, tmpDir, nil)
	rt.handleSessionScanTick()

	deleteEvent := <-events
	sessions, ok = deleteEvent.Details["sessions"].([]tui.SessionInfo)
	if !ok {
		t.Fatalf("delete sessions detail type = %T, want []tui.SessionInfo", deleteEvent.Details["sessions"])
	}
	if len(sessions) != 0 {
		t.Fatalf("delete sessions = %#v, want empty session list", sessions)
	}
}

func sessionInfoExists(sessions []tui.SessionInfo, name string, nodeCount int) bool {
	for _, session := range sessions {
		if session.Name == name && session.NodeCount == nodeCount {
			return true
		}
	}
	return false
}

func TestResumeMailboxProjections_RestoresKnownSessionTrees(t *testing.T) {
	baseDir := t.TempDir()
	primarySessionDir := filepath.Join(baseDir, "ctx-main", "review")
	now := time.Date(2026, time.April, 14, 4, 30, 0, 0, time.UTC)

	primaryWriter, err := journal.OpenShadowWriter(primarySessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter(primary) error = %v", err)
	}

	primaryFilename := "20260414-043001-r1111-from-orchestrator-to-worker.md"
	primaryContent := "---\nparams:\n  from: orchestrator\n  to: worker\n---\n\nPrimary inbox payload\n"
	appendRuntimeMailboxEventForTest(t, primaryWriter, projection.MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: primaryFilename,
		From:      "orchestrator",
		To:        "worker",
		Path:      filepath.Join("inbox", "worker", primaryFilename),
		Content:   primaryContent,
	}, now.Add(time.Second))

	primaryProjectedPath := filepath.Join(primarySessionDir, "inbox", "worker", primaryFilename)
	if err := os.MkdirAll(filepath.Dir(primaryProjectedPath), 0o700); err != nil {
		t.Fatalf("MkdirAll(primary projected): %v", err)
	}
	if err := os.WriteFile(primaryProjectedPath, []byte("stale primary"), 0o600); err != nil {
		t.Fatalf("WriteFile(primary stale): %v", err)
	}

	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			SessionName: "review",
			SessionDir:  primarySessionDir,
		},
	}

	if err := resumeMailboxProjections(primarySessionDir, nodes); err != nil {
		t.Fatalf("resumeMailboxProjections() error = %v", err)
	}

	gotPrimary, err := os.ReadFile(primaryProjectedPath)
	if err != nil {
		t.Fatalf("ReadFile(primary projected): %v", err)
	}
	if string(gotPrimary) != primaryContent {
		t.Fatalf("primary projection content = %q, want %q", string(gotPrimary), primaryContent)
	}
}

func TestPostEventGuard_DedupesByPathUntilFinished(t *testing.T) {
	rt := &daemonRuntime{
		activePostEvents: make(map[string]bool),
	}

	path := "/tmp/post/message.md"
	if !rt.beginPostEvent(path) {
		t.Fatal("beginPostEvent(first) = false, want true")
	}
	if rt.beginPostEvent(path) {
		t.Fatal("beginPostEvent(duplicate) = true, want false")
	}

	rt.finishPostEvent(path)

	if !rt.beginPostEvent(path) {
		t.Fatal("beginPostEvent(after finish) = false, want true")
	}
}

func TestHandleWatcherEvent_DaemonSubmitSendDispatchesPostWithoutPostWatcherEvent(t *testing.T) {
	tmpDir := t.TempDir()
	installRuntimeTestTmux(t, tmpDir)

	baseDir := filepath.Join(tmpDir, "state")
	contextID := "ctx-submit"
	sessionName := "review-session"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	manager := journal.NewManager(contextID, os.Getpid())
	journal.InstallProcessManager(manager)
	t.Cleanup(journal.ClearProcessManager)
	if err := manager.Bootstrap(sessionDir, sessionName, time.Now()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Edges = []string{"orchestrator --- messenger"}
	cfg.NotificationTemplate = "new message from {from_node}"
	cfg.TmuxTimeout = 0.01
	adjacency, err := config.ParseEdges(cfg.Edges)
	if err != nil {
		t.Fatalf("ParseEdges: %v", err)
	}

	daemonState := NewDaemonState(0, contextID)
	daemonState.enabledSessionsMu.Lock()
	daemonState.enabledSessions[sessionName] = true
	daemonState.enabledSessionsMu.Unlock()

	rt := &daemonRuntime{
		baseDir:          baseDir,
		sessionDir:       sessionDir,
		contextID:        contextID,
		selfSession:      sessionName,
		cfg:              cfg,
		adjacency:        adjacency,
		nodes:            map[string]discovery.NodeInfo{},
		knownNodes:       make(map[string]bool),
		events:           make(chan tui.DaemonEvent, 8),
		daemonState:      daemonState,
		idleTracker:      idle.NewIdleTracker(),
		watchedDirs:      make(map[string]bool),
		claimedPanes:     make(map[string]bool),
		prevSessionNodes: make(map[string][]string),
		activePostEvents: make(map[string]bool),
	}
	rt.nodes[sessionName+":orchestrator"] = discovery.NodeInfo{SessionName: sessionName, SessionDir: sessionDir, PaneID: "%1"}
	rt.nodes[sessionName+":messenger"] = discovery.NodeInfo{SessionName: sessionName, SessionDir: sessionDir, PaneID: "%2"}

	filename := "20260502-004600-r1111-from-orchestrator-to-messenger.md"
	content := "---\nparams:\n  contextId: ctx-submit\n  from: orchestrator\n  to: messenger\n  timestamp: 2026-05-02T00:46:00+09:00\n---\n\nhello\n"
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-send",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  filename,
		Content:   content,
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	rt.handleWatcherEvent(fswatcher.Event{Name: requestPath, Op: fswatcher.Create})
	rt.handleDaemonSubmitResult(waitForDaemonSubmitResult(t, rt))

	inboxPath := filepath.Join(sessionDir, "inbox", "messenger", filename)
	deadline := time.Now().Add(time.Second)
	for {
		got, err := os.ReadFile(inboxPath)
		if err == nil {
			if string(got) != content {
				t.Fatalf("inbox content = %q, want %q", string(got), content)
			}
			break
		}
		if !os.IsNotExist(err) {
			t.Fatalf("ReadFile(inbox): %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for delivered inbox file %s", inboxPath)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "post", filename)); !os.IsNotExist(err) {
		t.Fatalf("post file still present or wrong error: %v", err)
	}
	waitForPostEventIdle(t, rt, filepath.Join(sessionDir, "post", filename), 10*time.Second)
}

func TestDispatchPendingDaemonSubmitRequestsProcessesMissedPopRequest(t *testing.T) {
	tmpDir := t.TempDir()

	baseDir := filepath.Join(tmpDir, "state")
	contextID := "ctx-submit-scan"
	sessionName := "review-session"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260502-004700-r1111-from-orchestrator-to-worker.md"
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-05-02T00:47:00+09:00\n---\n\nhello\n"
	if err := os.WriteFile(filepath.Join(inboxDir, filename), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-scan",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	rt := &daemonRuntime{
		baseDir:    baseDir,
		sessionDir: sessionDir,
		contextID:  contextID,
		nodes:      map[string]discovery.NodeInfo{},
		events:     make(chan tui.DaemonEvent, 8),
	}

	rt.dispatchPendingDaemonSubmitRequests()
	rt.handleDaemonSubmitResult(waitForDaemonSubmitResult(t, rt))

	if _, err := os.Stat(requestPath); !os.IsNotExist(err) {
		t.Fatalf("request file still present or wrong error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "read", filename)); err != nil {
		t.Fatalf("archived read file missing: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-pop-scan"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Filename != filename {
		t.Fatalf("response.Filename = %q, want %q", response.Filename, filename)
	}
}

func TestDispatchPendingDaemonSubmitRequestsProcessesNodeSessionRequests(t *testing.T) {
	tmpDir := t.TempDir()

	baseDir := filepath.Join(tmpDir, "state")
	contextID := "ctx-submit-cross-session"
	primarySessionDir := filepath.Join(baseDir, contextID, "primary-session")
	nodeSessionDir := filepath.Join(baseDir, contextID, "node-session")
	for _, sessionDir := range []string{primarySessionDir, nodeSessionDir} {
		if err := config.CreateSessionDirs(sessionDir); err != nil {
			t.Fatalf("CreateSessionDirs(%q): %v", sessionDir, err)
		}
	}

	inboxDir := filepath.Join(nodeSessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260502-004800-r1111-from-orchestrator-to-worker.md"
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-05-02T00:48:00+09:00\n---\n\nhello from another session\n"
	if err := os.WriteFile(filepath.Join(inboxDir, filename), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(nodeSessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-cross-session",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	rt := &daemonRuntime{
		baseDir:    baseDir,
		sessionDir: primarySessionDir,
		contextID:  contextID,
		nodes: map[string]discovery.NodeInfo{
			"node-session:worker": {
				SessionName: "node-session",
				SessionDir:  nodeSessionDir,
			},
		},
		events: make(chan tui.DaemonEvent, 8),
	}

	rt.dispatchPendingDaemonSubmitRequests()
	rt.handleDaemonSubmitResult(waitForDaemonSubmitResult(t, rt))

	if _, err := os.Stat(requestPath); !os.IsNotExist(err) {
		t.Fatalf("cross-session request file still present or wrong error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(nodeSessionDir, "read", filename)); err != nil {
		t.Fatalf("cross-session read file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inboxDir, filename)); !os.IsNotExist(err) {
		t.Fatalf("cross-session inbox file still present or wrong error: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(nodeSessionDir, "req-pop-cross-session"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Filename != filename {
		t.Fatalf("response.Filename = %q, want %q", response.Filename, filename)
	}
}

func TestHandlePostWatcherEvent_RateLimitedMessageRetriesAfterGap(t *testing.T) {
	tmpDir := t.TempDir()
	installRuntimeTestTmux(t, tmpDir)

	baseDir := filepath.Join(tmpDir, "state")
	contextID := "ctx-rate-limit"
	sessionName := "review-session"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	installShadowJournalManager(sessionDir, contextID, sessionName, time.Now())
	t.Cleanup(journal.ClearProcessManager)

	cfg := config.DefaultConfig()
	cfg.Edges = []string{"orchestrator --- messenger"}
	cfg.NotificationTemplate = "new message from {from_node}"
	cfg.TmuxTimeout = 0.01
	cfg.MinDeliveryGapSeconds = 0.2
	adjacency, err := config.ParseEdges(cfg.Edges)
	if err != nil {
		t.Fatalf("ParseEdges: %v", err)
	}

	daemonState := NewDaemonState(0, contextID)
	daemonState.SetSessionEnabled(sessionName, true)
	daemonState.lastDeliveryMu.Lock()
	daemonState.lastDeliveryBySenderRecipient["orchestrator:messenger"] = time.Now()
	daemonState.lastDeliveryMu.Unlock()

	rt := &daemonRuntime{
		baseDir:          baseDir,
		sessionDir:       sessionDir,
		contextID:        contextID,
		selfSession:      sessionName,
		cfg:              cfg,
		adjacency:        adjacency,
		nodes:            map[string]discovery.NodeInfo{},
		knownNodes:       make(map[string]bool),
		events:           make(chan tui.DaemonEvent, 8),
		daemonState:      daemonState,
		idleTracker:      idle.NewIdleTracker(),
		watchedDirs:      make(map[string]bool),
		claimedPanes:     make(map[string]bool),
		prevSessionNodes: make(map[string][]string),
		activePostEvents: make(map[string]bool),
	}
	rt.nodes[sessionName+":orchestrator"] = discovery.NodeInfo{SessionName: sessionName, SessionDir: sessionDir, PaneID: "%1"}
	rt.nodes[sessionName+":messenger"] = discovery.NodeInfo{SessionName: sessionName, SessionDir: sessionDir, PaneID: "%2"}

	filename := "20260502-010000-r1111-from-orchestrator-to-messenger.md"
	content := "---\nparams:\n  contextId: ctx-rate-limit\n  from: orchestrator\n  to: messenger\n  timestamp: 2026-05-02T01:00:00+09:00\n---\n\nhello after gap\n"
	postPath := filepath.Join(sessionDir, "post", filename)
	if err := os.WriteFile(postPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(post): %v", err)
	}

	start := time.Now()
	rt.handlePostWatcherEvent(postPath, fswatcher.Create)

	if _, err := os.Stat(postPath); err != nil {
		t.Fatalf("rate-limited post file should remain until retry: %v", err)
	}

	inboxPath := filepath.Join(sessionDir, "inbox", "messenger", filename)
	if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
		t.Fatalf("inbox file should not be delivered before retry gap, got err=%v", err)
	}
	deliveredAt := waitForFileContent(t, inboxPath, content, 10*time.Second)
	if elapsed := deliveredAt.Sub(start); elapsed < 150*time.Millisecond {
		t.Fatalf("message delivered before rate-limit gap elapsed: %s", elapsed)
	}
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Fatalf("post file still present after retry or wrong error: %v", err)
	}
	waitForPostEventIdle(t, rt, postPath, 10*time.Second)
}

func TestHandlePostWatcherEvent_SameRouteInFlightDeliveryIsSerialized(t *testing.T) {
	tmpDir := t.TempDir()
	installRuntimeTestTmux(t, tmpDir)

	baseDir := filepath.Join(tmpDir, "state")
	contextID := "ctx-inflight"
	sessionName := "review-session"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	installShadowJournalManager(sessionDir, contextID, sessionName, time.Now())
	t.Cleanup(journal.ClearProcessManager)

	cfg := config.DefaultConfig()
	cfg.Edges = []string{"orchestrator --- messenger"}
	cfg.NotificationTemplate = "new message from {from_node}"
	cfg.TmuxTimeout = 0.01
	cfg.MinDeliveryGapSeconds = 0.2
	adjacency, err := config.ParseEdges(cfg.Edges)
	if err != nil {
		t.Fatalf("ParseEdges: %v", err)
	}

	rt := &daemonRuntime{
		baseDir:          baseDir,
		sessionDir:       sessionDir,
		contextID:        contextID,
		selfSession:      sessionName,
		cfg:              cfg,
		adjacency:        adjacency,
		nodes:            map[string]discovery.NodeInfo{},
		knownNodes:       make(map[string]bool),
		events:           make(chan tui.DaemonEvent, 8),
		daemonState:      NewDaemonState(0, contextID),
		idleTracker:      idle.NewIdleTracker(),
		watchedDirs:      make(map[string]bool),
		claimedPanes:     make(map[string]bool),
		prevSessionNodes: make(map[string][]string),
		activePostEvents: make(map[string]bool),
	}
	rt.daemonState.SetSessionEnabled(sessionName, true)
	rt.nodes[sessionName+":orchestrator"] = discovery.NodeInfo{SessionName: sessionName, SessionDir: sessionDir, PaneID: "%1"}
	rt.nodes[sessionName+":messenger"] = discovery.NodeInfo{SessionName: sessionName, SessionDir: sessionDir, PaneID: "%2"}

	firstFilename := "20260502-010100-r1111-from-orchestrator-to-messenger.md"
	firstContent := "---\nparams:\n  contextId: ctx-inflight\n  from: orchestrator\n  to: messenger\n  timestamp: 2026-05-02T01:01:00+09:00\n---\n\nfirst\n"
	firstPostPath := filepath.Join(sessionDir, "post", firstFilename)
	if err := os.WriteFile(firstPostPath, []byte(firstContent), 0o600); err != nil {
		t.Fatalf("WriteFile(first post): %v", err)
	}
	secondFilename := "20260502-010101-r2222-from-orchestrator-to-messenger.md"
	secondContent := "---\nparams:\n  contextId: ctx-inflight\n  from: orchestrator\n  to: messenger\n  timestamp: 2026-05-02T01:01:01+09:00\n---\n\nsecond\n"
	secondPostPath := filepath.Join(sessionDir, "post", secondFilename)
	if err := os.WriteFile(secondPostPath, []byte(secondContent), 0o600); err != nil {
		t.Fatalf("WriteFile(second post): %v", err)
	}

	rt.handlePostWatcherEvent(firstPostPath, fswatcher.Create)
	rt.handlePostWatcherEvent(secondPostPath, fswatcher.Create)

	firstInboxPath := filepath.Join(sessionDir, "inbox", "messenger", firstFilename)
	firstDeliveredAt := waitForFileContent(t, firstInboxPath, firstContent, 10*time.Second)
	secondInboxPath := filepath.Join(sessionDir, "inbox", "messenger", secondFilename)
	if _, err := os.Stat(secondInboxPath); !os.IsNotExist(err) {
		t.Fatalf("second same-route message should wait behind in-flight delivery, got err=%v", err)
	}

	secondDeliveredAt := waitForFileContent(t, secondInboxPath, secondContent, 10*time.Second)
	if elapsed := secondDeliveredAt.Sub(firstDeliveredAt); elapsed < 150*time.Millisecond {
		t.Fatalf("second same-route message delivered too soon after first: %s", elapsed)
	}
	waitForPostEventIdle(t, rt, firstPostPath, 10*time.Second)
	waitForPostEventIdle(t, rt, secondPostPath, 10*time.Second)
}

func TestBootstrap_ReconcilesExistingPostBacklog(t *testing.T) {
	tmpDir := t.TempDir()
	installRuntimeTestTmux(t, tmpDir)

	baseDir := filepath.Join(tmpDir, "state")
	contextID := "ctx-backlog"
	sessionName := "review-session"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	installShadowJournalManager(sessionDir, contextID, sessionName, time.Now())
	t.Cleanup(journal.ClearProcessManager)

	cfg := config.DefaultConfig()
	cfg.Edges = []string{"orchestrator --- messenger"}
	cfg.NotificationTemplate = "new message from {from_node}"
	cfg.TmuxTimeout = 0.01
	adjacency, err := config.ParseEdges(cfg.Edges)
	if err != nil {
		t.Fatalf("ParseEdges: %v", err)
	}

	rt := &daemonRuntime{
		baseDir:          baseDir,
		sessionDir:       sessionDir,
		contextID:        contextID,
		selfSession:      sessionName,
		cfg:              cfg,
		adjacency:        adjacency,
		nodes:            map[string]discovery.NodeInfo{},
		knownNodes:       make(map[string]bool),
		events:           make(chan tui.DaemonEvent, 8),
		daemonState:      NewDaemonState(0, contextID),
		idleTracker:      idle.NewIdleTracker(),
		watchedDirs:      make(map[string]bool),
		claimedPanes:     make(map[string]bool),
		prevSessionNodes: make(map[string][]string),
		activePostEvents: make(map[string]bool),
	}
	rt.daemonState.SetSessionEnabled(sessionName, true)
	rt.nodes[sessionName+":orchestrator"] = discovery.NodeInfo{SessionName: sessionName, SessionDir: sessionDir, PaneID: "%1"}
	rt.nodes[sessionName+":messenger"] = discovery.NodeInfo{SessionName: sessionName, SessionDir: sessionDir, PaneID: "%2"}

	filename := "20260502-011000-r1111-from-orchestrator-to-messenger.md"
	content := "---\nparams:\n  contextId: ctx-backlog\n  from: orchestrator\n  to: messenger\n  timestamp: 2026-05-02T01:10:00+09:00\n---\n\nhello backlog\n"
	postPath := filepath.Join(sessionDir, "post", filename)
	if err := os.WriteFile(postPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(post): %v", err)
	}
	recordMailboxProjectionPayload(sessionDir, sessionName, projection.MailboxProjectionPostedEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: filename,
		From:      "orchestrator",
		To:        "messenger",
		Path:      shadowRelativePath(sessionDir, postPath),
		Content:   content,
	})

	rt.bootstrap()

	inboxPath := filepath.Join(sessionDir, "inbox", "messenger", filename)
	waitForFileContent(t, inboxPath, content, 10*time.Second)
	waitForFileGone(t, postPath, 10*time.Second)
	waitForPostEventIdle(t, rt, postPath, 10*time.Second)
	waitForAutoPingEventIdle(t, rt, sessionName+":orchestrator", 10*time.Second)
	waitForAutoPingEventIdle(t, rt, sessionName+":messenger", 10*time.Second)
}

func installRuntimeTestTmux(t *testing.T, tmpDir string) {
	t.Helper()
	fakeBin := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(fakeBin, 0o700); err != nil {
		t.Fatalf("MkdirAll(fakeBin): %v", err)
	}
	script := "#!/bin/sh\ncase \"$1\" in\n  set-buffer|paste-buffer|send-keys) exit 0 ;;\n  *) exit 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installRuntimeSessionScanTmux(t *testing.T, tmpDir string, sessions []string) string {
	t.Helper()
	fakeBin := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(fakeBin, 0o700); err != nil {
		t.Fatalf("MkdirAll(fakeBin): %v", err)
	}
	logPath := filepath.Join(tmpDir, "tmux.log")
	var sessionOutput strings.Builder
	for i, sessionName := range sessions {
		fmt.Fprintf(&sessionOutput, "  printf '%%s\\t$%d\\n' '%s'\n", i+1, sessionName)
	}
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\nif [ \"$1\" = 'list-sessions' ]; then\n%s  exit 0\nfi\nif [ \"$1\" = 'list-panes' ]; then\n  echo 'unexpected list-panes' >&2\n  exit 42\nfi\nexit 0\n", logPath, sessionOutput.String())
	if err := os.WriteFile(filepath.Join(fakeBin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func installRuntimeSessionScanActivationTmux(t *testing.T, tmpDir string, sessions []string) string {
	return installRuntimeSessionScanActivationTmuxWithPanes(t, tmpDir, sessions, []string{"messenger", "orchestrator", "unrelated"})
}

func installRuntimeSessionScanActivationTmuxWithPanes(t *testing.T, tmpDir string, sessions, paneTitles []string) string {
	t.Helper()
	fakeBin := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(fakeBin, 0o700); err != nil {
		t.Fatalf("MkdirAll(fakeBin): %v", err)
	}
	logPath := filepath.Join(tmpDir, "tmux.log")
	var sessionOutput strings.Builder
	for i, sessionName := range sessions {
		fmt.Fprintf(&sessionOutput, "  printf '%%s\\t$%d\\n' '%s'\n", i+1, sessionName)
	}
	var sessionPaneOutput strings.Builder
	var allPaneOutput strings.Builder
	for i, paneTitle := range paneTitles {
		paneID := fmt.Sprintf("%%%d", 201+i)
		fmt.Fprintf(&sessionPaneOutput, "  printf '%%s\\n' '%s %s'\n", paneID, paneTitle)
		contextForPane := "ctx-fast-session"
		if paneTitle == "unrelated" {
			contextForPane = ""
		}
		fmt.Fprintf(&allPaneOutput, "  printf '%%s\\n' '%s\t%s\treview\t%s'\n", paneID, contextForPane, paneTitle)
	}
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
if [ "$1" = 'list-sessions' ]; then
%s  exit 0
fi
if [ "$1" = 'list-panes' ] && [ "$2" = '-s' ] && [ "$3" = '-t' ] && [ "$4" = 'review' ]; then
%s
  exit 0
fi
if [ "$1" = 'list-panes' ] && [ "$2" = '-a' ]; then
%s
  exit 0
fi
if [ "$1" = 'show-options' ] && [ "$2" = '-gqv' ]; then
  exit 0
fi
if [ "$1" = 'set-option' ]; then
  exit 0
fi
exit 0
`, logPath, sessionOutput.String(), sessionPaneOutput.String(), allPaneOutput.String())
	if err := os.WriteFile(filepath.Join(fakeBin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func waitForFileContent(t *testing.T, path, want string, timeout time.Duration) time.Time {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		got, err := os.ReadFile(path)
		if err == nil {
			if string(got) != want {
				t.Fatalf("file content = %q, want %q", string(got), want)
			}
			return time.Now()
		}
		if !os.IsNotExist(err) {
			t.Fatalf("ReadFile(%s): %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for file %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForFileGone(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return
		} else if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("file still present after %s: %s", timeout, path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForPostEventIdle(t *testing.T, rt *daemonRuntime, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		rt.postEventsMu.Lock()
		active := rt.activePostEvents[path]
		rt.postEventsMu.Unlock()
		if !active {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for post event to finish %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForAutoPingEventIdle(t *testing.T, rt *daemonRuntime, nodeKey string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		rt.autoPingEventsMu.Lock()
		active := rt.activeAutoPings[nodeKey]
		rt.autoPingEventsMu.Unlock()
		if !active {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for auto-PING event to finish %s", nodeKey)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDetectNewNodes_ReturnsOnlyNewNodesWithoutAutoEnable(t *testing.T) {
	freshNodes := map[string]discovery.NodeInfo{
		"self:known": {
			SessionName: "self",
			SessionDir:  t.TempDir(),
		},
		"foreign:worker": {
			SessionName: "foreign",
			SessionDir:  t.TempDir(),
		},
	}
	watcher, err := fswatcher.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher(): %v", err)
	}
	defer func() { _ = watcher.Close() }()

	rt := &daemonRuntime{
		cfg:         config.DefaultConfig(),
		watcher:     watcher,
		knownNodes:  map[string]bool{"self:known": true},
		watchedDirs: make(map[string]bool),
		daemonState: NewDaemonState(0, "ctx-disabled"),
		events:      make(chan tui.DaemonEvent, 1),
	}
	newNodes := rt.detectNewNodes(freshNodes)
	sort.Strings(newNodes)

	if got, want := newNodes, []string{"foreign:worker"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("detectNewNodes() = %#v, want %#v", got, want)
	}
	if rt.daemonState.GetConfiguredSessionEnabled("foreign") {
		t.Fatal("detectNewNodes() auto-enabled a foreign session even though dispatch should own that decision")
	}
}

func TestPruneKnownNodes_AllowsReturnedNodeToReceiveAutoPingAgain(t *testing.T) {
	watcher, err := fswatcher.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher(): %v", err)
	}
	defer func() { _ = watcher.Close() }()

	rt := &daemonRuntime{
		knownNodes: map[string]bool{
			"review:worker": true,
			"review:critic": true,
		},
		watcher:     watcher,
		watchedDirs: make(map[string]bool),
		daemonState: NewDaemonState(0, "ctx-returned"),
		events:      make(chan tui.DaemonEvent, 1),
	}

	rt.pruneKnownNodes(map[string]discovery.NodeInfo{
		"review:critic": {SessionName: "review", SessionDir: t.TempDir()},
	})

	if rt.knownNodes["review:worker"] {
		t.Fatal("pruneKnownNodes() kept disappeared review:worker")
	}
	if !rt.knownNodes["review:critic"] {
		t.Fatal("pruneKnownNodes() removed still-live review:critic")
	}

	freshNodes := map[string]discovery.NodeInfo{
		"review:worker": {SessionName: "review", SessionDir: t.TempDir()},
		"review:critic": {SessionName: "review", SessionDir: t.TempDir()},
	}
	newNodes := rt.detectNewNodes(freshNodes)
	sort.Strings(newNodes)

	if got, want := newNodes, []string{"review:worker"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("detectNewNodes() after pruneKnownNodes() = %#v, want %#v", got, want)
	}
}

func TestHandleScanTick_DisabledAutoEnableLeavesNewSessionPendingAndDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "state")
	contextID := "ctx-fast-session"
	selfSession := "main"
	targetSession := "review"
	contextDir := filepath.Join(baseDir, contextID)
	selfSessionDir := filepath.Join(contextDir, selfSession)
	targetSessionDir := filepath.Join(contextDir, targetSession)
	if err := config.CreateMultiSessionDirs(contextDir, selfSession); err != nil {
		t.Fatalf("CreateMultiSessionDirs(self): %v", err)
	}
	if err := config.CreateSessionDirs(targetSessionDir); err != nil {
		t.Fatalf("CreateSessionDirs(target): %v", err)
	}
	installRuntimeSessionScanActivationTmuxWithPanes(t, tmpDir, []string{selfSession, targetSession}, []string{"worker"})
	installShadowJournalManager(targetSessionDir, contextID, targetSession, time.Now())
	t.Cleanup(journal.ClearProcessManager)

	watcher, err := fswatcher.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher(): %v", err)
	}
	defer func() { _ = watcher.Close() }()

	autoEnableNewSessions := false
	cfg := config.DefaultConfig()
	cfg.AutoEnableNewSessions = &autoEnableNewSessions
	cfg.NodeOrder = []string{"worker"}
	cfg.Nodes = map[string]config.NodeConfig{"worker": {}}
	events := make(chan tui.DaemonEvent, 8)
	sendAutoPingCalled := make(chan struct{}, 1)
	rt := &daemonRuntime{
		baseDir:          baseDir,
		sessionDir:       selfSessionDir,
		contextID:        contextID,
		selfSession:      selfSession,
		cfg:              cfg,
		watcher:          watcher,
		adjacency:        map[string][]string{},
		nodes:            map[string]discovery.NodeInfo{},
		knownNodes:       map[string]bool{},
		events:           events,
		daemonState:      NewDaemonState(0, contextID),
		idleTracker:      idle.NewIdleTracker(),
		watchedDirs:      map[string]bool{},
		claimedPanes:     map[string]bool{},
		prevSessionNodes: map[string][]string{},
		sendAutoPing: func(discovery.NodeInfo, string, string, string, *config.Config, []string, map[string]bool, map[string][]string, map[string]discovery.NodeInfo) (controlplane.SystemMessageResult, error) {
			sendAutoPingCalled <- struct{}{}
			return controlplane.SystemMessageResult{Delivered: true}, nil
		},
	}

	rt.handleScanTick()

	nodeKey := targetSession + ":worker"
	if !rt.knownNodes[nodeKey] {
		t.Fatalf("knownNodes[%q] = false, want true", nodeKey)
	}
	if _, ok := rt.nodes[nodeKey]; !ok {
		t.Fatalf("rt.nodes missing discovered node %q: %#v", nodeKey, rt.nodes)
	}
	if rt.daemonState.GetConfiguredSessionEnabled(targetSession) {
		t.Fatalf("target session %q was auto-enabled despite AutoEnableNewSessions=false", targetSession)
	}
	select {
	case <-sendAutoPingCalled:
		t.Fatal("auto-PING sender was called for a disabled newly discovered session")
	default:
	}

	state, ok, err := projection.ProjectAutoPingState(targetSessionDir)
	if err != nil {
		t.Fatalf("ProjectAutoPingState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectAutoPingState() ok = false, want true")
	}
	nodeState, ok := state.Nodes[nodeKey]
	if !ok {
		t.Fatalf("auto-PING state missing %q: %#v", nodeKey, state.Nodes)
	}
	if !nodeState.Pending {
		t.Fatalf("auto-PING pending[%s] = false, want true", nodeKey)
	}

	statusEvent := <-events
	if statusEvent.Type != "status_update" {
		t.Fatalf("first event Type = %q, want status_update", statusEvent.Type)
	}
	sessions, ok := statusEvent.Details["sessions"].([]tui.SessionInfo)
	if !ok {
		t.Fatalf("sessions detail type = %T, want []tui.SessionInfo", statusEvent.Details["sessions"])
	}
	for _, session := range sessions {
		if session.Name == targetSession {
			if session.NodeCount != 1 {
				t.Fatalf("target session NodeCount = %d, want 1", session.NodeCount)
			}
			if session.Enabled {
				t.Fatalf("target session Enabled = true, want false")
			}
			return
		}
	}
	t.Fatalf("status sessions missing target session %q: %#v", targetSession, sessions)
}

func TestDispatchPendingAutoPings_ForeignOwnedSessionStaysPendingAndDisabled(t *testing.T) {
	baseDir := t.TempDir()
	sessionDir := filepath.Join(baseDir, "ctx-self", "foreign")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs(): %v", err)
	}
	installShadowJournalManager(sessionDir, "ctx-self", "foreign", time.Now())
	t.Cleanup(journal.ClearProcessManager)

	writeRuntimeLivePID(t, baseDir, "ctx-owner", "daemon")
	if err := os.MkdirAll(filepath.Join(baseDir, "ctx-owner", "foreign"), 0o755); err != nil {
		t.Fatalf("MkdirAll(ctx-owner/foreign): %v", err)
	}
	installRuntimeSessionOwnerTmux(t, map[string]string{
		"foreign": "ctx-owner:43210",
	})

	now := time.Date(2026, time.April, 26, 21, 55, 0, 0, time.UTC)
	if err := journal.RecordProcessEvent(sessionDir, "foreign", projection.AutoPingPendingEventType, journal.VisibilityOperatorVisible, projection.AutoPingEventPayload{
		NodeKey:      "foreign:worker",
		SessionName:  "foreign",
		NodeName:     "worker",
		PaneID:       "%51",
		Reason:       "discovered",
		TriggeredAt:  now.Add(-2 * time.Second).Format(time.RFC3339Nano),
		DelaySeconds: 0,
		NotBeforeAt:  now.Add(-2 * time.Second).Format(time.RFC3339Nano),
	}, now.Add(-time.Second)); err != nil {
		t.Fatalf("RecordProcessEvent(pending): %v", err)
	}

	rt := &daemonRuntime{
		baseDir:     baseDir,
		contextID:   "ctx-self",
		cfg:         &config.Config{DaemonMessageTemplate: "PING {node} in {context_id}"},
		adjacency:   map[string][]string{},
		daemonState: NewDaemonState(0, "ctx-self"),
		nodes: map[string]discovery.NodeInfo{
			"foreign:worker": {
				PaneID:      "%51",
				SessionName: "foreign",
				SessionDir:  sessionDir,
			},
		},
	}

	rt.dispatchPendingAutoPings(rt.nodes, true, now)

	if rt.daemonState.GetConfiguredSessionEnabled("foreign") {
		t.Fatal("dispatchPendingAutoPings() auto-enabled a foreign-owned session")
	}

	state, ok, err := projection.ProjectAutoPingState(sessionDir)
	if err != nil {
		t.Fatalf("ProjectAutoPingState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectAutoPingState() ok = false, want true")
	}
	if !state.Nodes["foreign:worker"].Pending {
		t.Fatal("pending auto-PING was cleared even though the session is foreign-owned")
	}
}

func TestDispatchPendingAutoPings_DeliversDuePendingPingAndClearsDebt(t *testing.T) {
	baseDir := t.TempDir()
	sessionDir := filepath.Join(baseDir, "ctx-self", "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs(): %v", err)
	}

	now := time.Date(2026, time.April, 26, 22, 0, 0, 0, time.UTC)
	installShadowJournalManager(sessionDir, "ctx-self", "review", now)
	t.Cleanup(journal.ClearProcessManager)
	if err := journal.RecordProcessEvent(sessionDir, "review", projection.AutoPingPendingEventType, journal.VisibilityOperatorVisible, projection.AutoPingEventPayload{
		NodeKey:      "review:worker",
		SessionName:  "review",
		NodeName:     "worker",
		PaneID:       "%61",
		Reason:       "discovered",
		TriggeredAt:  now.Add(-2 * time.Second).Format(time.RFC3339Nano),
		DelaySeconds: 0,
		NotBeforeAt:  now.Add(-2 * time.Second).Format(time.RFC3339Nano),
	}, now.Add(-time.Second)); err != nil {
		t.Fatalf("RecordProcessEvent(pending): %v", err)
	}

	rt := &daemonRuntime{
		baseDir:   baseDir,
		contextID: "ctx-self",
		cfg: &config.Config{
			DaemonMessageTemplate: "PING {node} in {context_id}",
			TmuxTimeout:           1.0,
		},
		adjacency:   map[string][]string{},
		daemonState: NewDaemonState(0, "ctx-self"),
		nodes: map[string]discovery.NodeInfo{
			"review:worker": {
				PaneID:      "%61",
				SessionName: "review",
				SessionDir:  sessionDir,
			},
		},
	}
	rt.daemonState.SetSessionEnabled("review", true)

	rt.dispatchPendingAutoPings(rt.nodes, false, now)

	waitForInboxEntries(t, sessionDir, "worker", 1)
	waitForAutoPingPending(t, sessionDir, "review:worker", false)
}

func TestDispatchPendingAutoPingsRecordsDeliveredAtWithRuntimeClock(t *testing.T) {
	baseDir := t.TempDir()
	sessionDir := filepath.Join(baseDir, "ctx-self", "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs(): %v", err)
	}

	now := time.Date(2026, time.May, 21, 7, 0, 0, 0, time.UTC)
	deliveredAt := now.Add(9 * time.Second)
	installShadowJournalManager(sessionDir, "ctx-self", "review", now)
	t.Cleanup(journal.ClearProcessManager)
	if err := journal.RecordProcessEvent(sessionDir, "review", projection.AutoPingPendingEventType, journal.VisibilityOperatorVisible, projection.AutoPingEventPayload{
		NodeKey:      "review:worker",
		SessionName:  "review",
		NodeName:     "worker",
		PaneID:       "%61",
		Reason:       "discovered",
		TriggeredAt:  now.Add(-2 * time.Second).Format(time.RFC3339Nano),
		DelaySeconds: 0,
		NotBeforeAt:  now.Add(-2 * time.Second).Format(time.RFC3339Nano),
	}, now.Add(-time.Second)); err != nil {
		t.Fatalf("RecordProcessEvent(pending): %v", err)
	}

	rt := &daemonRuntime{
		baseDir:   baseDir,
		contextID: "ctx-self",
		cfg: &config.Config{
			DaemonMessageTemplate: "PING {node} in {context_id}",
		},
		adjacency:   map[string][]string{},
		daemonState: NewDaemonState(0, "ctx-self"),
		nodes: map[string]discovery.NodeInfo{
			"review:worker": {
				PaneID:      "%61",
				SessionName: "review",
				SessionDir:  sessionDir,
			},
		},
		sendAutoPing: func(discovery.NodeInfo, string, string, string, *config.Config, []string, map[string]bool, map[string][]string, map[string]discovery.NodeInfo) (controlplane.SystemMessageResult, error) {
			return controlplane.SystemMessageResult{Delivered: true}, nil
		},
		clock: func() time.Time { return deliveredAt },
	}
	rt.daemonState.SetSessionEnabled("review", true)

	rt.dispatchPendingAutoPings(rt.nodes, false, now)
	waitForAutoPingPending(t, sessionDir, "review:worker", false)

	state, ok, err := projection.ProjectAutoPingState(sessionDir)
	if err != nil {
		t.Fatalf("ProjectAutoPingState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectAutoPingState() ok = false, want true")
	}
	if got, want := state.Nodes["review:worker"].DeliveredAt, deliveredAt.Format(time.RFC3339Nano); got != want {
		t.Fatalf("DeliveredAt = %q, want %q", got, want)
	}
}

func TestDispatchPendingAutoPings_DoesNotBlockDaemonLoopWhilePaneDeliveryRuns(t *testing.T) {
	baseDir := t.TempDir()
	sessionDir := filepath.Join(baseDir, "ctx-self", "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs(): %v", err)
	}

	now := time.Date(2026, time.April, 26, 22, 1, 0, 0, time.UTC)
	installShadowJournalManager(sessionDir, "ctx-self", "review", now)
	t.Cleanup(journal.ClearProcessManager)
	if err := journal.RecordProcessEvent(sessionDir, "review", projection.AutoPingPendingEventType, journal.VisibilityOperatorVisible, projection.AutoPingEventPayload{
		NodeKey:      "review:worker",
		SessionName:  "review",
		NodeName:     "worker",
		PaneID:       "%61",
		Reason:       "discovered",
		TriggeredAt:  now.Add(-2 * time.Second).Format(time.RFC3339Nano),
		DelaySeconds: 0,
		NotBeforeAt:  now.Add(-2 * time.Second).Format(time.RFC3339Nano),
	}, now.Add(-time.Second)); err != nil {
		t.Fatalf("RecordProcessEvent(pending): %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	rt := &daemonRuntime{
		baseDir:   baseDir,
		contextID: "ctx-self",
		cfg: &config.Config{
			DaemonMessageTemplate: "PING {node} in {context_id}",
			TmuxTimeout:           1.0,
		},
		adjacency:   map[string][]string{},
		daemonState: NewDaemonState(0, "ctx-self"),
		nodes: map[string]discovery.NodeInfo{
			"review:worker": {
				PaneID:      "%61",
				SessionName: "review",
				SessionDir:  sessionDir,
			},
		},
		sendAutoPing: func(discovery.NodeInfo, string, string, string, *config.Config, []string, map[string]bool, map[string][]string, map[string]discovery.NodeInfo) (controlplane.SystemMessageResult, error) {
			if calls.Add(1) == 1 {
				close(started)
			}
			<-release
			return controlplane.SystemMessageResult{Delivered: true}, nil
		},
	}
	rt.daemonState.SetSessionEnabled("review", true)

	start := time.Now()
	rt.dispatchPendingAutoPings(rt.nodes, false, now)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("dispatchPendingAutoPings blocked for %s", elapsed)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("auto-PING sender did not start")
	}

	rt.dispatchPendingAutoPings(rt.nodes, false, now)
	if got := calls.Load(); got != 1 {
		t.Fatalf("auto-PING sender calls while first delivery active = %d, want 1", got)
	}

	close(release)
	waitForAutoPingPending(t, sessionDir, "review:worker", false)
}

func TestBootstrap_QueuesAndDeliversStartupAutoPingForDiscoveredNode(t *testing.T) {
	baseDir := t.TempDir()
	sessionDir := filepath.Join(baseDir, "ctx-self", "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs(): %v", err)
	}

	now := time.Date(2026, time.April, 27, 2, 40, 0, 0, time.UTC)
	installShadowJournalManager(sessionDir, "ctx-self", "review", now)
	t.Cleanup(journal.ClearProcessManager)

	rt := &daemonRuntime{
		baseDir:     baseDir,
		sessionDir:  sessionDir,
		contextID:   "ctx-self",
		selfSession: "review",
		cfg: &config.Config{
			DaemonMessageTemplate: "PING {node} in {context_id}",
			TmuxTimeout:           1.0,
		},
		adjacency:   map[string][]string{},
		daemonState: NewDaemonState(0, "ctx-self"),
		events:      make(chan tui.DaemonEvent, 8),
		nodes: map[string]discovery.NodeInfo{
			"review:worker": {
				PaneID:      "%63",
				SessionName: "review",
				SessionDir:  sessionDir,
			},
		},
	}
	rt.daemonState.SetSessionEnabled("review", true)

	rt.bootstrap()

	waitForInboxEntries(t, sessionDir, "worker", 1)
	waitForAutoPingPending(t, sessionDir, "review:worker", false)
}

func TestBootstrap_QueuesStartupAutoPingOnlyForExplicitUINode(t *testing.T) {
	baseDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(baseDir, "xdg-config"))
	t.Setenv("HOME", filepath.Join(baseDir, "home"))
	t.Chdir(baseDir)

	configPath := filepath.Join(baseDir, "postman.toml")
	content := "[postman]\nui_node = \"messenger\"\nedges = [\"messenger --- worker\"]\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig(): %v", err)
	}

	sessionDir := filepath.Join(baseDir, "ctx-self", "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs(): %v", err)
	}

	now := time.Date(2026, time.May, 17, 0, 35, 0, 0, time.UTC)
	installShadowJournalManager(sessionDir, "ctx-self", "review", now)
	t.Cleanup(journal.ClearProcessManager)

	rt := &daemonRuntime{
		baseDir:     baseDir,
		sessionDir:  sessionDir,
		contextID:   "ctx-self",
		selfSession: "review",
		cfg:         cfg,
		adjacency:   map[string][]string{},
		daemonState: NewDaemonState(0, "ctx-self"),
		events:      make(chan tui.DaemonEvent, 8),
		nodes: map[string]discovery.NodeInfo{
			"review:messenger": {
				PaneID:      "%63",
				SessionName: "review",
				SessionDir:  sessionDir,
			},
			"review:worker": {
				PaneID:      "%64",
				SessionName: "review",
				SessionDir:  sessionDir,
			},
		},
	}
	rt.daemonState.SetSessionEnabled("review", true)

	rt.bootstrap()

	state, ok, err := projection.ProjectAutoPingState(sessionDir)
	if err != nil {
		t.Fatalf("ProjectAutoPingState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectAutoPingState() ok = false, want true")
	}
	if !state.Nodes["review:messenger"].Pending {
		t.Fatal("startup auto-PING was not queued for explicit ui_node")
	}
	if state.Nodes["review:worker"].Pending {
		t.Fatal("startup auto-PING was queued for non-ui_node worker")
	}
}

func TestBootstrap_ReconcilesDuePendingAutoPingDebtAfterHydration(t *testing.T) {
	baseDir := t.TempDir()
	sessionDir := filepath.Join(baseDir, "ctx-self", "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs(): %v", err)
	}

	now := time.Date(2026, time.April, 27, 2, 50, 0, 0, time.UTC)
	installShadowJournalManager(sessionDir, "ctx-self", "review", now)
	t.Cleanup(journal.ClearProcessManager)
	if err := journal.RecordProcessEvent(sessionDir, "review", projection.AutoPingPendingEventType, journal.VisibilityOperatorVisible, projection.AutoPingEventPayload{
		NodeKey:      "review:worker",
		SessionName:  "review",
		NodeName:     "worker",
		PaneID:       "%63",
		Reason:       "discovered",
		TriggeredAt:  now.Add(-2 * time.Second).Format(time.RFC3339Nano),
		DelaySeconds: 0,
		NotBeforeAt:  now.Add(-2 * time.Second).Format(time.RFC3339Nano),
	}, now.Add(-time.Second)); err != nil {
		t.Fatalf("RecordProcessEvent(pending): %v", err)
	}

	rt := &daemonRuntime{
		baseDir:     baseDir,
		sessionDir:  sessionDir,
		contextID:   "ctx-self",
		selfSession: "review",
		cfg: &config.Config{
			DaemonMessageTemplate: "PING {node} in {context_id}",
			TmuxTimeout:           1.0,
		},
		adjacency:   map[string][]string{},
		daemonState: NewDaemonState(0, "ctx-self"),
		events:      make(chan tui.DaemonEvent, 8),
		nodes: map[string]discovery.NodeInfo{
			"review:worker": {
				PaneID:      "%63",
				SessionName: "review",
				SessionDir:  sessionDir,
			},
		},
	}
	rt.daemonState.SetSessionEnabled("review", true)

	rt.bootstrap()

	waitForInboxEntries(t, sessionDir, "worker", 1)
	waitForAutoPingPending(t, sessionDir, "review:worker", false)
}

func TestDispatchPendingAutoPings_QueueFullLeavesPendingWithoutDeadLetter(t *testing.T) {
	baseDir := t.TempDir()
	sessionDir := filepath.Join(baseDir, "ctx-self", "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs(): %v", err)
	}

	now := time.Date(2026, time.April, 26, 22, 2, 0, 0, time.UTC)
	installShadowJournalManager(sessionDir, "ctx-self", "review", now)
	t.Cleanup(journal.ClearProcessManager)
	if err := journal.RecordProcessEvent(sessionDir, "review", projection.AutoPingPendingEventType, journal.VisibilityOperatorVisible, projection.AutoPingEventPayload{
		NodeKey:      "review:worker",
		SessionName:  "review",
		NodeName:     "worker",
		PaneID:       "%61",
		Reason:       "discovered",
		TriggeredAt:  now.Add(-2 * time.Second).Format(time.RFC3339Nano),
		DelaySeconds: 0,
		NotBeforeAt:  now.Add(-2 * time.Second).Format(time.RFC3339Nano),
	}, now.Add(-time.Second)); err != nil {
		t.Fatalf("RecordProcessEvent(pending): %v", err)
	}

	recipientInbox := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(recipientInbox, 0o700); err != nil {
		t.Fatalf("MkdirAll recipient inbox: %v", err)
	}
	for i := range 20 {
		name := filepath.Join(recipientInbox, fmt.Sprintf("20260426-2202%02d-rabcd-from-postman-to-worker.md", i))
		if err := os.WriteFile(name, []byte("queued"), 0o600); err != nil {
			t.Fatalf("WriteFile queued fixture %d: %v", i, err)
		}
	}

	rt := &daemonRuntime{
		baseDir:   baseDir,
		contextID: "ctx-self",
		cfg: &config.Config{
			DaemonMessageTemplate: "PING {node} in {context_id}",
			TmuxTimeout:           1.0,
		},
		adjacency:   map[string][]string{},
		daemonState: NewDaemonState(0, "ctx-self"),
		nodes: map[string]discovery.NodeInfo{
			"review:worker": {
				PaneID:      "%61",
				SessionName: "review",
				SessionDir:  sessionDir,
			},
		},
	}
	rt.daemonState.SetSessionEnabled("review", true)

	rt.dispatchPendingAutoPings(rt.nodes, false, now)

	deadEntries, err := os.ReadDir(filepath.Join(sessionDir, "dead-letter"))
	if err != nil {
		t.Fatalf("ReadDir dead-letter: %v", err)
	}
	if len(deadEntries) != 0 {
		t.Fatalf("dead-letter entries = %d, want 0 for retryable queue-full auto-PING", len(deadEntries))
	}

	state, ok, err := projection.ProjectAutoPingState(sessionDir)
	if err != nil {
		t.Fatalf("ProjectAutoPingState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectAutoPingState() ok = false, want true")
	}
	if !state.Nodes["review:worker"].Pending {
		t.Fatal("pending auto-PING debt was cleared even though delivery was blocked by a full inbox")
	}
}

func TestDispatchPendingAutoPings_RespectsNotBeforeAt(t *testing.T) {
	baseDir := t.TempDir()
	sessionDir := filepath.Join(baseDir, "ctx-self", "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs(): %v", err)
	}

	now := time.Date(2026, time.April, 26, 22, 5, 0, 0, time.UTC)
	installShadowJournalManager(sessionDir, "ctx-self", "review", now)
	t.Cleanup(journal.ClearProcessManager)
	if err := journal.RecordProcessEvent(sessionDir, "review", projection.AutoPingPendingEventType, journal.VisibilityOperatorVisible, projection.AutoPingEventPayload{
		NodeKey:      "review:worker",
		SessionName:  "review",
		NodeName:     "worker",
		PaneID:       "%62",
		Reason:       "discovered",
		TriggeredAt:  now.Format(time.RFC3339Nano),
		DelaySeconds: 30,
		NotBeforeAt:  now.Add(30 * time.Second).Format(time.RFC3339Nano),
	}, now); err != nil {
		t.Fatalf("RecordProcessEvent(pending): %v", err)
	}

	rt := &daemonRuntime{
		baseDir:   baseDir,
		contextID: "ctx-self",
		cfg: &config.Config{
			DaemonMessageTemplate: "PING {node} in {context_id}",
			TmuxTimeout:           1.0,
		},
		adjacency:   map[string][]string{},
		daemonState: NewDaemonState(0, "ctx-self"),
		nodes: map[string]discovery.NodeInfo{
			"review:worker": {
				PaneID:      "%62",
				SessionName: "review",
				SessionDir:  sessionDir,
			},
		},
	}
	rt.daemonState.SetSessionEnabled("review", true)

	rt.dispatchPendingAutoPings(rt.nodes, false, now)

	inboxEntries, err := os.ReadDir(filepath.Join(sessionDir, "inbox"))
	if err != nil {
		t.Fatalf("ReadDir inbox root: %v", err)
	}
	if len(inboxEntries) != 0 {
		t.Fatalf("inbox root entries = %d, want 0 before not_before_at", len(inboxEntries))
	}

	state, ok, err := projection.ProjectAutoPingState(sessionDir)
	if err != nil {
		t.Fatalf("ProjectAutoPingState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectAutoPingState() ok = false, want true")
	}
	if !state.Nodes["review:worker"].Pending {
		t.Fatal("future pending auto-PING was cleared before not_before_at")
	}
}

func TestRecordPendingAutoPing_UsesConfiguredAutoPingDelay(t *testing.T) {
	baseDir := t.TempDir()
	sessionDir := filepath.Join(baseDir, "ctx-self", "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs(): %v", err)
	}

	now := time.Date(2026, time.May, 4, 14, 20, 0, 0, time.UTC)
	installShadowJournalManager(sessionDir, "ctx-self", "review", now)
	t.Cleanup(journal.ClearProcessManager)

	rt := &daemonRuntime{
		baseDir:   baseDir,
		contextID: "ctx-self",
		cfg: &config.Config{
			AutoPingDelaySeconds: 20,
		},
	}
	rt.recordPendingAutoPing("review:worker", discovery.NodeInfo{
		PaneID:      "%77",
		SessionName: "review",
		SessionDir:  sessionDir,
	}, "discovered", now)

	state, ok, err := projection.ProjectAutoPingState(sessionDir)
	if err != nil {
		t.Fatalf("ProjectAutoPingState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectAutoPingState() ok = false, want true")
	}
	pending := state.Nodes["review:worker"]
	if !pending.Pending {
		t.Fatal("pending auto-PING was not recorded")
	}
	if pending.DelaySeconds != 20 {
		t.Fatalf("DelaySeconds: got %v, want 20", pending.DelaySeconds)
	}
	if got, want := pending.NotBeforeAt, now.Add(20*time.Second).Format(time.RFC3339Nano); got != want {
		t.Fatalf("NotBeforeAt: got %q, want %q", got, want)
	}
}

func TestRecordPendingAutoPing_UsesDefaultAutoPingDelay(t *testing.T) {
	baseDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(baseDir, "xdg-config"))
	t.Setenv("HOME", filepath.Join(baseDir, "home"))
	cfg, err := config.LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig(): %v", err)
	}

	sessionDir := filepath.Join(baseDir, "ctx-self", "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs(): %v", err)
	}

	now := time.Date(2026, time.May, 4, 14, 20, 0, 0, time.UTC)
	installShadowJournalManager(sessionDir, "ctx-self", "review", now)
	t.Cleanup(journal.ClearProcessManager)

	rt := &daemonRuntime{
		baseDir:   baseDir,
		contextID: "ctx-self",
		cfg:       cfg,
	}
	rt.recordPendingAutoPing("review:worker", discovery.NodeInfo{
		PaneID:      "%77",
		SessionName: "review",
		SessionDir:  sessionDir,
	}, "startup", now)

	state, ok, err := projection.ProjectAutoPingState(sessionDir)
	if err != nil {
		t.Fatalf("ProjectAutoPingState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectAutoPingState() ok = false, want true")
	}
	pending := state.Nodes["review:worker"]
	if !pending.Pending {
		t.Fatal("pending auto-PING was not recorded")
	}
	if pending.DelaySeconds != 20 {
		t.Fatalf("DelaySeconds: got %v, want 20", pending.DelaySeconds)
	}
	if got, want := pending.NotBeforeAt, now.Add(20*time.Second).Format(time.RFC3339Nano); got != want {
		t.Fatalf("NotBeforeAt: got %q, want %q", got, want)
	}
}

func installRuntimeSessionOwnerTmux(t *testing.T, owners map[string]string) {
	t.Helper()

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	var builder strings.Builder
	builder.WriteString("#!/bin/sh\n")
	builder.WriteString("if [ \"$1 $2\" = \"show-options -gqv\" ]; then\n")
	builder.WriteString("  case \"$3\" in\n")
	keys := make([]string, 0, len(owners))
	for key := range owners {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		builder.WriteString("    @a2a_session_on_" + key + ")\n")
		builder.WriteString("      printf '%s\\n' '" + owners[key] + "'\n")
		builder.WriteString("      exit 0\n")
		builder.WriteString("      ;;\n")
	}
	builder.WriteString("    *)\n")
	builder.WriteString("      exit 0\n")
	builder.WriteString("      ;;\n")
	builder.WriteString("  esac\n")
	builder.WriteString("fi\n")
	builder.WriteString("exit 1\n")

	if err := os.WriteFile(scriptPath, []byte(builder.String()), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}

	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func writeRuntimeLivePID(t *testing.T, baseDir, contextName, sessionName string) {
	t.Helper()

	dir := filepath.Join(baseDir, contextName, sessionName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(pid dir): %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "postman.pid"), []byte(fmt.Sprint(os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile(postman.pid): %v", err)
	}
}

func appendRuntimeMailboxEventForTest(t *testing.T, writer *journal.Writer, eventType string, visibility journal.Visibility, payload journal.MailboxEventPayload, now time.Time) {
	t.Helper()
	if _, err := writer.AppendEvent(eventType, visibility, payload, now); err != nil {
		t.Fatalf("AppendEvent(%s): %v", eventType, err)
	}
}

func TestLogPaneIDChanges_LogsRediscoveryAndCollapse(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	rt := &daemonRuntime{
		nodes: map[string]discovery.NodeInfo{
			"alpha:worker": {SessionName: "alpha", PaneID: "%10"},
			"beta:worker":  {SessionName: "beta", PaneID: "%20"},
			"gamma:worker": {SessionName: "gamma", PaneID: "%30"},
		},
	}

	freshNodes := map[string]discovery.NodeInfo{
		"alpha:worker": {SessionName: "alpha", PaneID: "%11"}, // pane changed → re-discovery
		"beta:worker":  {SessionName: "beta", PaneID: "%20"},  // unchanged → no log
		"delta:worker": {SessionName: "delta", PaneID: "%40"}, // new → no log
		// gamma:worker absent → collapse
	}

	rt.logPaneIDChanges(freshNodes)
	logOut := buf.String()

	wantRediscovery := "postman: discovery: session alpha re-discovered node alpha:worker (pane=%10 -> %11)"
	if !strings.Contains(logOut, wantRediscovery) {
		t.Errorf("expected re-discovery log %q, got: %s", wantRediscovery, logOut)
	}

	wantCollapse := "postman: discovery: session gamma collapsed (pane=%30 node=gamma:worker)"
	if !strings.Contains(logOut, wantCollapse) {
		t.Errorf("expected collapse log %q, got: %s", wantCollapse, logOut)
	}

	if strings.Contains(logOut, "beta") {
		t.Errorf("unexpected log for unchanged node beta: %s", logOut)
	}
	if strings.Contains(logOut, "delta") {
		t.Errorf("unexpected log for new node delta: %s", logOut)
	}
}
