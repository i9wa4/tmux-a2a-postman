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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fswatcher/fswatcher"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/controlplane"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/msgtrace"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

type recordingFilesystemWatcher struct {
	removed []string
}

func (w *recordingFilesystemWatcher) Add(string, fswatcher.Op) error {
	return nil
}

func (w *recordingFilesystemWatcher) Remove(path string) error {
	w.removed = append(w.removed, path)
	return nil
}

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

func TestPostDeliveryTraceFieldsPreservesEnvelopeCorrelation(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "source-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	filename := "20260626-010000-from-orchestrator-to-worker.md"
	eventPath := filepath.Join(sessionDir, "post", filename)
	content := "---\nparams:\n  contextId: envelope-ctx\n  from: orchestrator\n  to: worker\n  messageId: " + filename + "\n  replyTo: 20260626-005900-from-worker-to-orchestrator.md\n  input_request_id: ireq_attempt_123\n  timestamp: 2026-06-26T01:00:00+09:00\n---\n\nbody\n"
	if err := os.WriteFile(eventPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rt := &daemonRuntime{contextID: "runtime-ctx"}
	fields := rt.postDeliveryTraceFields(eventPath, filename)

	if fields.MessageID != filename {
		t.Fatalf("MessageID = %q, want %q", fields.MessageID, filename)
	}
	if fields.MessagePath != filepath.Join("post", filename) {
		t.Fatalf("MessagePath = %q, want post-relative filename", fields.MessagePath)
	}
	if fields.ContextID != "envelope-ctx" {
		t.Fatalf("ContextID = %q, want envelope-ctx", fields.ContextID)
	}
	if fields.InputRequestID != "ireq_attempt_123" {
		t.Fatalf("InputRequestID = %q, want ireq_attempt_123", fields.InputRequestID)
	}
	if fields.ReplyTo != "20260626-005900-from-worker-to-orchestrator.md" {
		t.Fatalf("ReplyTo = %q", fields.ReplyTo)
	}
}

func TestDispatchPostDeliveryPreservesProjectionSyncCorrelationAfterMove(t *testing.T) {
	installRuntimeTestTmux(t, t.TempDir())

	sourceSessionDir := filepath.Join(t.TempDir(), "sender-session")
	if err := config.CreateSessionDirs(sourceSessionDir); err != nil {
		t.Fatalf("CreateSessionDirs source: %v", err)
	}
	recipientSessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(recipientSessionDir); err != nil {
		t.Fatalf("CreateSessionDirs recipient: %v", err)
	}

	filename := "20260626-011500-from-orchestrator-to-review-session:worker.md"
	postPath := filepath.Join(sourceSessionDir, "post", filename)
	content := "---\nparams:\n  contextId: envelope-ctx\n  from: orchestrator\n  to: review-session:worker\n  messageId: " + filename + "\n  replyTo: 20260626-011400-from-review-session:worker-to-orchestrator.md\n  input_request_id: ireq_runtime_123\n  timestamp: 2026-06-26T01:15:00+09:00\n---\n\nbody\n"
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	nodes := map[string]discovery.NodeInfo{
		"sender-session:orchestrator": {SessionName: "sender-session", SessionDir: sourceSessionDir, PaneID: "%1"},
		"review-session:worker":       {SessionName: "review-session", SessionDir: recipientSessionDir, PaneID: "%2"},
	}
	adjacency := map[string][]string{
		"orchestrator": {"review-session:worker"},
	}
	cfg := &config.Config{
		EnterDelay:  0.1,
		TmuxTimeout: 1.0,
	}
	rt := &daemonRuntime{
		contextID:   "runtime-ctx",
		selfSession: "sender-session",
		nodes:       nodes,
		adjacency:   adjacency,
		cfg:         cfg,
		events:      make(chan tui.DaemonEvent, 8),
		daemonState: NewDaemonState(0, "runtime-ctx"),
		idleTracker: idle.NewIdleTracker(),
	}
	rt.daemonState.SetSessionEnabled("sender-session", true)
	rt.daemonState.SetSessionEnabled("review-session", true)

	originalSync := syncMailboxProjectionWithTraceFn
	captured := make(chan msgtrace.Fields, 2)
	syncMailboxProjectionWithTraceFn = func(sessionDir string, fields msgtrace.Fields) {
		captured <- fields
		originalSync(sessionDir, fields)
	}
	t.Cleanup(func() { syncMailboxProjectionWithTraceFn = originalSync })

	rt.dispatchPostDelivery(postPath, filename, nodes, adjacency, cfg, postDeliveryReservation{})
	waitForInboxEntries(t, recipientSessionDir, "worker", 1)

	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Fatalf("post file still present after delivery: %v", err)
	}

	byPath := map[string]msgtrace.Fields{}
	for i := 0; i < 2; i++ {
		select {
		case fields := <-captured:
			byPath[fields.MessagePath] = fields
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for runtime projection_sync call %d", i+1)
		}
	}

	if len(byPath) != 2 {
		t.Fatalf("captured runtime sync calls = %d, want 2", len(byPath))
	}
	sourceFields, ok := byPath[filepath.Join("post", filename)]
	if !ok {
		t.Fatalf("missing source projection_sync fields: %#v", captured)
	}
	recipientFields, ok := byPath[filepath.Join("inbox", "worker", filename)]
	if !ok {
		t.Fatalf("missing recipient projection_sync fields: %#v", captured)
	}
	for name, fields := range map[string]msgtrace.Fields{"source": sourceFields, "recipient": recipientFields} {
		if fields.InputRequestID != "ireq_runtime_123" {
			t.Fatalf("%s InputRequestID = %q, want ireq_runtime_123", name, fields.InputRequestID)
		}
		if fields.ReplyTo != "20260626-011400-from-review-session:worker-to-orchestrator.md" {
			t.Fatalf("%s ReplyTo = %q", name, fields.ReplyTo)
		}
		if fields.ContextID != "envelope-ctx" {
			t.Fatalf("%s ContextID = %q, want envelope-ctx", name, fields.ContextID)
		}
	}
}

type daemonSubmitWorkerHarness struct {
	workers []func()
}

func (h *daemonSubmitWorkerHarness) launch(worker func()) {
	h.workers = append(h.workers, worker)
}

func (h *daemonSubmitWorkerHarness) runNext(t *testing.T) {
	t.Helper()
	if len(h.workers) == 0 {
		t.Fatal("no daemon-submit worker queued")
	}
	worker := h.workers[0]
	h.workers = h.workers[1:]
	worker()
}

type runtimeTimerCall struct {
	delay    time.Duration
	name     string
	callback func()
}

type runtimeTimerHarness struct {
	calls []runtimeTimerCall
}

func (h *runtimeTimerHarness) schedule(delay time.Duration, name string, _ chan<- tui.DaemonEvent, callback func()) {
	h.calls = append(h.calls, runtimeTimerCall{
		delay:    delay,
		name:     name,
		callback: callback,
	})
}

func (h *runtimeTimerHarness) runNext(t *testing.T) {
	t.Helper()
	if len(h.calls) == 0 {
		t.Fatal("no runtime timer queued")
	}
	call := h.calls[0]
	h.calls = h.calls[1:]
	call.callback()
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
	now := time.Now().UTC()
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-diagnostics",
		Command:   projection.DaemonSubmitRuntimeDiagnostics,
		CreatedAt: now.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}
	if _, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pending",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: now.Add(-2 * time.Minute).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("WriteDaemonSubmitRequest(pending): %v", err)
	}
	claimedPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-claimed",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: now.Add(-3 * time.Minute).Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest(claimed): %v", err)
	}
	if err := os.Rename(claimedPath, claimedPath+".processing"); err != nil {
		t.Fatalf("Rename claimed request: %v", err)
	}
	if _, err := projection.WriteDaemonSubmitResponse(sessionDir, projection.DaemonSubmitResponse{
		RequestID: "req-late",
		Command:   projection.DaemonSubmitSend,
		HandledAt: now.Add(-4 * time.Minute).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("WriteDaemonSubmitResponse(late): %v", err)
	}

	daemonState := NewDaemonState(0, "ctx-runtime")
	daemonState.AutoEnableSessionIfNew("review")
	daemonState.AutoEnableSessionIfNew("qa")
	rt := &daemonRuntime{
		sessionDir:  sessionDir,
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
		daemonSubmitSaturationCount: 2,
		daemonSubmitLastSaturatedAt: now.Add(-time.Minute),
		daemonState:                 daemonState,
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
	if diagnostics.DaemonSubmit.WorkerLimit != config.DefaultDaemonSubmitWorkerLimit ||
		diagnostics.DaemonSubmit.ActiveWorkerCount != 0 ||
		diagnostics.DaemonSubmit.ActiveRequestCount != 1 ||
		diagnostics.DaemonSubmit.PendingRequestCount != 1 ||
		diagnostics.DaemonSubmit.ClaimedRequestCount != 1 ||
		diagnostics.DaemonSubmit.LateResponseCount != 1 ||
		diagnostics.DaemonSubmit.SaturationCount != 2 {
		t.Fatalf("DaemonSubmit diagnostics = %#v", diagnostics.DaemonSubmit)
	}
	if diagnostics.DaemonSubmit.OldestPendingAgeSeconds <= 0 ||
		diagnostics.DaemonSubmit.OldestClaimedAgeSeconds <= 0 ||
		diagnostics.DaemonSubmit.OldestLateResponseAgeSeconds <= 0 ||
		diagnostics.DaemonSubmit.LastSaturatedAt == "" {
		t.Fatalf("DaemonSubmit age diagnostics = %#v", diagnostics.DaemonSubmit)
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
		"req-pending",
		"req-claimed",
		"req-late",
	} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("diagnostics leaked %q in %s", forbidden, payload)
		}
	}
	assertRuntimeDiagnosticsHasNoArrays(t, diagnostics)
}

func TestNewDaemonRuntimeConfiguresDaemonSubmitWorkerLimit(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
		want int
	}{
		{name: "nil config uses default", cfg: nil, want: config.DefaultDaemonSubmitWorkerLimit},
		{name: "missing config uses default", cfg: &config.Config{}, want: config.DefaultDaemonSubmitWorkerLimit},
		{name: "configured", cfg: &config.Config{DaemonSubmitWorkerLimit: 12}, want: 12},
		{name: "above max clamps", cfg: &config.Config{DaemonSubmitWorkerLimit: 99}, want: config.MaxDaemonSubmitWorkerLimit},
		{name: "negative uses default", cfg: &config.Config{DaemonSubmitWorkerLimit: -1}, want: config.DefaultDaemonSubmitWorkerLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := newDaemonRuntime(
				"",
				"",
				"",
				tt.cfg,
				nil,
				nil,
				nil,
				nil,
				nil,
				"",
				nil,
				nil,
				nil,
				nil,
				nil,
				"",
			)
			if got := cap(rt.daemonSubmitSem); got != tt.want {
				t.Fatalf("daemonSubmitSem cap = %d, want %d", got, tt.want)
			}
			if got := cap(rt.daemonSubmitResults); got != tt.want {
				t.Fatalf("daemonSubmitResults cap = %d, want %d", got, tt.want)
			}
			if got := rt.daemonSubmitRuntimeDiagnostics(time.Now()).WorkerLimit; got != tt.want {
				t.Fatalf("diagnostic worker limit = %d, want %d", got, tt.want)
			}
		})
	}
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

func TestLogRuntimeDiagnosticsSnapshotWritesPassiveScalarLine(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	if _, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-secret-pending",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: now.Add(-2 * time.Minute).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("WriteDaemonSubmitRequest(pending): %v", err)
	}
	if _, err := projection.WriteDaemonSubmitResponse(sessionDir, projection.DaemonSubmitResponse{
		RequestID: "req-secret-late",
		Command:   projection.DaemonSubmitSend,
		HandledAt: now.Add(-4 * time.Minute).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("WriteDaemonSubmitResponse(late): %v", err)
	}

	rt := &daemonRuntime{
		sessionDir:  sessionDir,
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
		daemonSubmitSaturationCount: 2,
		daemonSubmitLastSaturatedAt: now.Add(-time.Minute),
	}

	var buf bytes.Buffer
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
	})
	originalRSS := currentProcessRSSSnapshot
	currentProcessRSSSnapshot = func() processRSSSnapshot {
		return processRSSSnapshot{Supported: true, Available: true, Bytes: 123456}
	}
	t.Cleanup(func() {
		currentProcessRSSSnapshot = originalRSS
	})

	rt.logRuntimeDiagnosticsSnapshot("startup", now)
	line := buf.String()
	for _, want := range []string{
		"postman: component=daemon_runtime event=memory_snapshot source=passive_log reason=startup",
		"observed_at=2026-06-02T00:00:00Z",
		"rss_supported=true",
		"rss_available=true",
		"rss_bytes=123456",
		"heap_alloc_bytes=",
		"heap_sys_bytes=",
		"heap_objects=",
		"gc_count=",
		"goroutine_count=",
		"daemon_session_count=2",
		"daemon_node_count=2",
		"daemon_watched_dir_count=2",
		"daemon_active_post_event_count=1",
		"daemon_active_auto_ping_count=1",
		"daemon_active_daemon_submit_count=1",
		"daemon_submit_pending_request_count=1",
		"daemon_submit_late_response_count=1",
		"daemon_submit_saturation_count=2",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("runtime diagnostics log missing %q in %q", want, line)
		}
	}
	for _, forbidden := range []string{
		"/tmp/private",
		"20260524-120000-r1111-from-a-to-b.md",
		"review:worker",
		"%42",
		"request.json",
		"req-secret-pending",
		"req-secret-late",
	} {
		if strings.Contains(line, forbidden) {
			t.Fatalf("runtime diagnostics log leaked %q in %q", forbidden, line)
		}
	}
}

func TestRuntimeDiagnosticsLogLineMarksUnsupportedRSSExplicitly(t *testing.T) {
	originalRSS := currentProcessRSSSnapshot
	currentProcessRSSSnapshot = func() processRSSSnapshot {
		return processRSSSnapshot{}
	}
	t.Cleanup(func() {
		currentProcessRSSSnapshot = originalRSS
	})

	diagnostics := status.NewRuntimeDiagnostics(
		"daemon_runtime",
		status.DaemonRuntimeCardinality{},
		status.DaemonSubmitRuntimeDiagnostics{},
		status.NonDaemonDeliveryRuntimeDiagnostics{},
		time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
	)

	line := runtimeDiagnosticsLogLine("interval", &diagnostics)
	for _, want := range []string{
		"rss_supported=false",
		"rss_available=false",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("runtime diagnostics log missing %q in %q", want, line)
		}
	}
	if strings.Contains(line, "rss_bytes=") {
		t.Fatalf("unsupported RSS log included rss_bytes in %q", line)
	}
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
	if err := config.WriteSessionPIDFile(filepath.Join(contextDir, selfSession, "postman.pid"), os.Getpid()); err != nil {
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
	autoEnable := true
	rt.cfg.AutoEnableNewSessions = &autoEnable
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
	if err := config.WriteSessionPIDFile(filepath.Join(contextDir, selfSession, "postman.pid"), os.Getpid()); err != nil {
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
	autoEnable := true
	rt.cfg.AutoEnableNewSessions = &autoEnable
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

	workerHarness := &daemonSubmitWorkerHarness{}
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
			return daemonSubmitProcessResult{}, nil
		},
		launchDaemonSubmitWorker: workerHarness.launch,
	}

	rt.handleWatcherEvent(fswatcher.Event{Name: requestPath, Op: fswatcher.Create})
	if got := len(workerHarness.workers); got != 1 {
		t.Fatalf("queued daemon-submit workers = %d, want 1", got)
	}
	select {
	case result := <-rt.daemonSubmitResults:
		t.Fatalf("daemon-submit worker ran synchronously: %#v", result)
	default:
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
	default:
		t.Fatal("missing status_update while daemon-submit worker was queued")
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

	workerHarness := &daemonSubmitWorkerHarness{}
	var started []projection.DaemonSubmitCommand
	rt := &daemonRuntime{
		processDaemonSubmit: func(requestPath string) (daemonSubmitProcessResult, error) {
			request, err := projection.ReadDaemonSubmitRequest(requestPath)
			if err != nil {
				return daemonSubmitProcessResult{}, err
			}
			started = append(started, request.Command)
			return daemonSubmitProcessResult{}, nil
		},
		launchDaemonSubmitWorker: workerHarness.launch,
	}

	if status := rt.dispatchDaemonSubmitRequest(popPath); status != daemonSubmitDispatched {
		t.Fatalf("dispatch pop status = %v, want dispatched", status)
	}
	if got := len(workerHarness.workers); got != 1 {
		t.Fatalf("queued workers after pop = %d, want 1", got)
	}

	if status := rt.dispatchDaemonSubmitRequest(sendPath); status != daemonSubmitDispatched {
		t.Fatalf("dispatch send status = %v, want dispatched while pop is active", status)
	}
	if got := len(workerHarness.workers); got != 2 {
		t.Fatalf("queued workers after send = %d, want 2", got)
	}

	workerHarness.runNext(t)
	workerHarness.runNext(t)
	if !reflect.DeepEqual(started, []projection.DaemonSubmitCommand{projection.DaemonSubmitPop, projection.DaemonSubmitSend}) {
		t.Fatalf("started commands = %#v, want pop then send", started)
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

	workerHarness := &daemonSubmitWorkerHarness{}
	var started []string
	rt := &daemonRuntime{
		processDaemonSubmit: func(requestPath string) (daemonSubmitProcessResult, error) {
			request, err := projection.ReadDaemonSubmitRequest(requestPath)
			if err != nil {
				return daemonSubmitProcessResult{}, err
			}
			started = append(started, request.RequestID)
			return daemonSubmitProcessResult{}, nil
		},
		launchDaemonSubmitWorker: workerHarness.launch,
	}

	if status := rt.dispatchDaemonSubmitRequest(popWorker1); status != daemonSubmitDispatched {
		t.Fatalf("dispatch first worker pop status = %v, want dispatched", status)
	}
	if got := len(workerHarness.workers); got != 1 {
		t.Fatalf("queued workers after first worker pop = %d, want 1", got)
	}
	if status := rt.dispatchDaemonSubmitRequest(popWorker2); status != daemonSubmitDispatchDeferred {
		t.Fatalf("dispatch second worker pop status = %v, want deferred", status)
	}
	if got := len(workerHarness.workers); got != 1 {
		t.Fatalf("queued workers after deferred worker pop = %d, want 1", got)
	}
	if status := rt.dispatchDaemonSubmitRequest(popReviewer); status != daemonSubmitDispatched {
		t.Fatalf("dispatch reviewer pop status = %v, want dispatched", status)
	}
	if got := len(workerHarness.workers); got != 2 {
		t.Fatalf("queued workers after reviewer pop = %d, want 2", got)
	}

	workerHarness.runNext(t)
	workerHarness.runNext(t)
	if !reflect.DeepEqual(started, []string{"req-pop-worker-1", "req-pop-reviewer"}) {
		t.Fatalf("started requests = %#v, want first worker then reviewer", started)
	}
}

func TestDispatchDaemonSubmitRequest_ReportsSaturationWhenWorkerLimitFull(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	workerHarness := &daemonSubmitWorkerHarness{}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	rt := &daemonRuntime{
		clock: func() time.Time {
			return now
		},
		processDaemonSubmit: func(requestPath string) (daemonSubmitProcessResult, error) {
			return daemonSubmitProcessResult{}, nil
		},
		launchDaemonSubmitWorker: workerHarness.launch,
	}

	workerLimit := config.DefaultDaemonSubmitWorkerLimit
	for i := 0; i < workerLimit; i++ {
		requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
			RequestID: fmt.Sprintf("req-send-%d", i),
			Command:   projection.DaemonSubmitSend,
			CreatedAt: now.Format(time.RFC3339),
			Filename:  fmt.Sprintf("20260601-12000%d-r1111-from-orchestrator-to-worker.md", i),
			Content:   "queued",
		})
		if err != nil {
			t.Fatalf("WriteDaemonSubmitRequest(%d): %v", i, err)
		}
		if status := rt.dispatchDaemonSubmitRequest(requestPath); status != daemonSubmitDispatched {
			t.Fatalf("dispatch request %d status = %v, want dispatched", i, status)
		}
	}
	if got := len(workerHarness.workers); got != workerLimit {
		t.Fatalf("queued workers = %d, want %d", got, workerLimit)
	}
	if got := len(rt.daemonSubmitSem); got != workerLimit {
		t.Fatalf("active worker slots = %d, want %d", got, workerLimit)
	}

	extraPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-send-extra",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: now.Format(time.RFC3339),
		Filename:  "20260601-120010-r1111-from-orchestrator-to-worker.md",
		Content:   "saturated",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest(extra): %v", err)
	}
	if status := rt.dispatchDaemonSubmitRequest(extraPath); status != daemonSubmitDispatchSaturated {
		t.Fatalf("dispatch saturated request status = %v, want saturated", status)
	}
	if got := len(workerHarness.workers); got != workerLimit {
		t.Fatalf("queued workers after saturation = %d, want %d", got, workerLimit)
	}
	if rt.daemonSubmitSaturationCount != 1 {
		t.Fatalf("daemonSubmitSaturationCount = %d, want 1", rt.daemonSubmitSaturationCount)
	}
	if !rt.daemonSubmitLastSaturatedAt.Equal(now) {
		t.Fatalf("daemonSubmitLastSaturatedAt = %s, want %s", rt.daemonSubmitLastSaturatedAt, now)
	}
}

func TestDispatchPostDelivery_QueuesRetryWhenNonDaemonBudgetFull(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC)
	timerHarness := &runtimeTimerHarness{}
	rt := &daemonRuntime{
		daemonState:          NewDaemonState(0, "ctx-budget"),
		events:               make(chan tui.DaemonEvent, 8),
		scheduleRuntimeTimer: timerHarness.schedule,
		clock: func() time.Time {
			return now
		},
	}
	budget := rt.nonDaemonDeliveryBudget()
	for i := 0; i < budget.workerLimit(); i++ {
		if !budget.tryStart(nonDaemonDeliveryPathPost) {
			t.Fatalf("pre-fill post slot %d failed", i)
		}
	}

	rt.dispatchPostDelivery(
		filepath.Join(t.TempDir(), "post", "20260603-090000-r1111-from-a-to-b.md"),
		"20260603-090000-r1111-from-a-to-b.md",
		nil,
		nil,
		config.DefaultConfig(),
		postDeliveryReservation{},
	)

	if len(timerHarness.calls) != 1 {
		t.Fatalf("scheduled retries = %d, want 1", len(timerHarness.calls))
	}
	if got := timerHarness.calls[0].name; got != "post-delivery-budget-retry" {
		t.Fatalf("retry timer name = %q, want post-delivery-budget-retry", got)
	}
	diag := rt.nonDaemonDeliveryRuntimeDiagnostics()
	if diag.ActivePostCount != budget.workerLimit() || diag.PendingPostCount != 1 || diag.SaturationCount != 1 {
		t.Fatalf("non-daemon diagnostics after post saturation = %#v", diag)
	}
}

func TestDispatchAutoPing_QueuesRetryWhenNonDaemonBudgetFull(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 1, 0, 0, time.UTC)
	timerHarness := &runtimeTimerHarness{}
	var calls atomic.Int32
	rt := &daemonRuntime{
		daemonState:          NewDaemonState(0, "ctx-budget"),
		events:               make(chan tui.DaemonEvent, 8),
		scheduleRuntimeTimer: timerHarness.schedule,
		sendAutoPing: func(discovery.NodeInfo, string, string, string, *config.Config, []string, map[string]bool, map[string][]string, map[string]discovery.NodeInfo) (controlplane.SystemMessageResult, error) {
			calls.Add(1)
			return controlplane.SystemMessageResult{Delivered: true}, nil
		},
		clock: func() time.Time {
			return now
		},
	}
	budget := rt.nonDaemonDeliveryBudget()
	for i := 0; i < budget.workerLimit(); i++ {
		if !budget.tryStart(nonDaemonDeliveryPathAutoPing) {
			t.Fatalf("pre-fill auto-PING slot %d failed", i)
		}
	}

	rt.dispatchAutoPingDelivery(
		"review:worker",
		discovery.NodeInfo{SessionName: "review", PaneID: "%1"},
		projection.AutoPingNodeState{},
		"PING {node}",
		nil,
		nil,
		nil,
		nil,
		0,
	)

	if calls.Load() != 0 {
		t.Fatalf("auto-PING sender calls = %d, want 0 while budget saturated", calls.Load())
	}
	if len(timerHarness.calls) != 1 {
		t.Fatalf("scheduled retries = %d, want 1", len(timerHarness.calls))
	}
	if got := timerHarness.calls[0].name; got != "auto-ping-budget-retry" {
		t.Fatalf("retry timer name = %q, want auto-ping-budget-retry", got)
	}
	diag := rt.nonDaemonDeliveryRuntimeDiagnostics()
	if diag.ActiveAutoPingCount != budget.workerLimit() || diag.PendingAutoPingCount != 1 || diag.SaturationCount != 1 {
		t.Fatalf("non-daemon diagnostics after auto-PING saturation = %#v", diag)
	}
}

// TestDispatchAutoPing_DropsAfterMaxStaleRetries guards Issue #572 M1: once
// a saturated auto-PING has exhausted maxAutoPingStaleRetries, it must be
// dropped (not rescheduled against ever-staler topology) and must release
// its beginAutoPing in-flight marker so the next scan can re-attempt the
// node with fresh data.
func TestDispatchAutoPing_DropsAfterMaxStaleRetries(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 3, 0, 0, time.UTC)
	timerHarness := &runtimeTimerHarness{}
	var calls atomic.Int32
	rt := &daemonRuntime{
		daemonState:          NewDaemonState(0, "ctx-budget"),
		events:               make(chan tui.DaemonEvent, 8),
		scheduleRuntimeTimer: timerHarness.schedule,
		sendAutoPing: func(discovery.NodeInfo, string, string, string, *config.Config, []string, map[string]bool, map[string][]string, map[string]discovery.NodeInfo) (controlplane.SystemMessageResult, error) {
			calls.Add(1)
			return controlplane.SystemMessageResult{Delivered: true}, nil
		},
		clock: func() time.Time {
			return now
		},
	}
	if !rt.beginAutoPing("review:worker") {
		t.Fatal("beginAutoPing: want true for first call")
	}
	budget := rt.nonDaemonDeliveryBudget()
	for i := 0; i < budget.workerLimit(); i++ {
		if !budget.tryStart(nonDaemonDeliveryPathAutoPing) {
			t.Fatalf("pre-fill auto-PING slot %d failed", i)
		}
	}

	rt.dispatchAutoPingDelivery(
		"review:worker",
		discovery.NodeInfo{SessionName: "review", PaneID: "%1"},
		projection.AutoPingNodeState{},
		"PING {node}",
		nil, nil, nil, nil,
		maxAutoPingStaleRetries,
	)

	if calls.Load() != 0 {
		t.Fatalf("auto-PING sender calls = %d, want 0 for a dropped stale retry", calls.Load())
	}
	if len(timerHarness.calls) != 0 {
		t.Fatalf("scheduled retries = %d, want 0 (dropped, not rescheduled)", len(timerHarness.calls))
	}
	diag := rt.nonDaemonDeliveryRuntimeDiagnostics()
	if diag.PendingAutoPingCount != 0 {
		t.Fatalf("PendingAutoPingCount after drop = %d, want 0 (unqueue called)", diag.PendingAutoPingCount)
	}
	if rt.beginAutoPing("review:worker") != true {
		t.Fatal("beginAutoPing after drop: want true — finishAutoPing should have released the in-flight marker")
	}
}

// TestNonDaemonBudget_BoundsConcurrentInFlightUnderBurst directly tests
// Issue #572 AC#4's literal claim — goroutine counts bounded under bursts —
// at the semaphore primitive dispatchPostDelivery/dispatchAutoPingDelivery/
// manual-PING all wrap: a burst well beyond workerLimit() must never let
// more than workerLimit() holders be active at once.
func TestNonDaemonBudget_BoundsConcurrentInFlightUnderBurst(t *testing.T) {
	budget := newNonDaemonDeliveryBudget(time.Now)
	limit := budget.workerLimit()
	burst := limit * 4

	var wg sync.WaitGroup
	var active int32
	var maxActive int32
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !budget.tryStart(nonDaemonDeliveryPathPost) {
				time.Sleep(time.Millisecond)
			}
			cur := atomic.AddInt32(&active, 1)
			for {
				prevMax := atomic.LoadInt32(&maxActive)
				if cur <= prevMax || atomic.CompareAndSwapInt32(&maxActive, prevMax, cur) {
					break
				}
			}
			time.Sleep(3 * time.Millisecond)
			atomic.AddInt32(&active, -1)
			budget.finish(nonDaemonDeliveryPathPost)
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&maxActive); got > int32(limit) {
		t.Fatalf("max concurrent in-flight post deliveries under burst = %d, want <= %d", got, limit)
	}
	if diag := budget.snapshot(); diag.ActivePostCount != 0 || diag.PendingPostCount != 0 {
		t.Fatalf("diagnostics after burst drains = %#v, want zeroed", diag)
	}
}

func TestNonDaemonBudget_BoundsManualPingFanout(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 2, 0, 0, time.UTC)
	budget := newNonDaemonDeliveryBudget(func() time.Time { return now })

	workerLimit := budget.beginManualFanout(config.DefaultDaemonSubmitWorkerLimit + 3)
	if workerLimit != config.DefaultDaemonSubmitWorkerLimit {
		t.Fatalf("manual worker limit = %d, want %d", workerLimit, config.DefaultDaemonSubmitWorkerLimit)
	}
	diag := budget.snapshot()
	if diag.PendingManualPingCount != config.DefaultDaemonSubmitWorkerLimit+3 || diag.SaturationCount != 1 || diag.LastSaturatedAt == "" {
		t.Fatalf("manual fanout diagnostics after begin = %#v", diag)
	}

	for i := 0; i < workerLimit; i++ {
		budget.unqueue(nonDaemonDeliveryPathManualPing)
		if !budget.tryStart(nonDaemonDeliveryPathManualPing) {
			t.Fatalf("manual worker %d could not start", i)
		}
	}
	if budget.tryStart(nonDaemonDeliveryPathManualPing) {
		t.Fatal("manual worker started beyond budget limit")
	}
	diag = budget.snapshot()
	if diag.ActiveManualPingCount != workerLimit || diag.PendingManualPingCount != 3 || diag.SaturationCount != 2 {
		t.Fatalf("manual fanout diagnostics while active = %#v", diag)
	}
}

// TestNonDaemonBudget_SerializesConcurrentManualFanouts guards against
// Issue #572 B2: overlapping manual-PING fanout rounds (e.g. two quick "p"
// keypresses) must not run concurrently, or finishManualFanout's
// reset-to-zero corrupts a still-running round's pending/saturation
// counters and manualPingSem gets overcommitted across rounds.
func TestNonDaemonBudget_SerializesConcurrentManualFanouts(t *testing.T) {
	budget := newNonDaemonDeliveryBudget(time.Now)

	const rounds = 6
	var active int32
	var maxActive int32
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < rounds; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			budget.beginManualFanout(2)
			cur := atomic.AddInt32(&active, 1)
			for {
				prevMax := atomic.LoadInt32(&maxActive)
				if cur <= prevMax || atomic.CompareAndSwapInt32(&maxActive, prevMax, cur) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			atomic.AddInt32(&active, -1)
			budget.finishManualFanout()
		}()
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt32(&maxActive); got != 1 {
		t.Fatalf("max concurrent manual-PING fanout rounds = %d, want 1 (serialized)", got)
	}
	diag := budget.snapshot()
	if diag.PendingManualPingCount != 0 || diag.SaturationCount != 0 {
		t.Fatalf("diagnostics after all serialized rounds finished = %#v, want zeroed", diag)
	}
}

func TestHandleDaemonSubmitResult_ResumesDeferredSameNodePop(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	now := time.Date(2026, 6, 1, 12, 1, 0, 0, time.UTC)
	firstPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-worker-1",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: now.Format(time.RFC3339),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest(first): %v", err)
	}
	secondPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-worker-2",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: now.Format(time.RFC3339),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest(second): %v", err)
	}

	workerHarness := &daemonSubmitWorkerHarness{}
	var started []string
	rt := &daemonRuntime{
		sessionDir: sessionDir,
		nodes:      map[string]discovery.NodeInfo{},
		events:     make(chan tui.DaemonEvent, 4),
		processDaemonSubmit: func(requestPath string) (daemonSubmitProcessResult, error) {
			request, err := projection.ReadDaemonSubmitRequest(requestPath)
			if err != nil {
				return daemonSubmitProcessResult{}, err
			}
			started = append(started, request.RequestID)
			if err := os.Remove(requestPath); err != nil {
				return daemonSubmitProcessResult{}, err
			}
			return daemonSubmitProcessResult{}, nil
		},
		launchDaemonSubmitWorker: workerHarness.launch,
	}

	if status := rt.dispatchDaemonSubmitRequest(firstPath); status != daemonSubmitDispatched {
		t.Fatalf("dispatch first pop status = %v, want dispatched", status)
	}
	if status := rt.dispatchDaemonSubmitRequest(secondPath); status != daemonSubmitDispatchDeferred {
		t.Fatalf("dispatch second pop status = %v, want deferred", status)
	}
	if got := len(workerHarness.workers); got != 1 {
		t.Fatalf("queued workers before first result = %d, want 1", got)
	}

	workerHarness.runNext(t)
	rt.handleDaemonSubmitResult(waitForDaemonSubmitResult(t, rt))
	if got := len(workerHarness.workers); got != 1 {
		t.Fatalf("queued workers after first result = %d, want deferred request resumed", got)
	}

	workerHarness.runNext(t)
	rt.handleDaemonSubmitResult(waitForDaemonSubmitResult(t, rt))
	if !reflect.DeepEqual(started, []string{"req-pop-worker-1", "req-pop-worker-2"}) {
		t.Fatalf("started requests = %#v, want first then deferred second", started)
	}
	if len(rt.activeDaemonSubmitKeys) != 0 {
		t.Fatalf("active daemon-submit keys = %#v, want empty", rt.activeDaemonSubmitKeys)
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

func TestDispatchPendingDaemonSubmitRequestsRoundRobinsSessionsUnderSaturation(t *testing.T) {
	tmpDir := t.TempDir()

	baseDir := filepath.Join(tmpDir, "state")
	contextID := "ctx-submit-round-robin"
	highVolumeSessionDir := filepath.Join(baseDir, contextID, "a-high-volume")
	lowVolumeSessionDir := filepath.Join(baseDir, contextID, "b-low-volume")
	for _, sessionDir := range []string{highVolumeSessionDir, lowVolumeSessionDir} {
		if err := config.CreateSessionDirs(sessionDir); err != nil {
			t.Fatalf("CreateSessionDirs(%q): %v", sessionDir, err)
		}
	}

	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	workerLimit := 4
	for i := 1; i <= workerLimit; i++ {
		if _, err := projection.WriteDaemonSubmitRequest(highVolumeSessionDir, projection.DaemonSubmitRequest{
			RequestID: fmt.Sprintf("high-%02d", i),
			Command:   projection.DaemonSubmitSend,
			CreatedAt: now,
			Filename:  fmt.Sprintf("20260601-1200%02d-r1111-from-orchestrator-to-worker.md", i),
			Content:   "high-volume",
		}); err != nil {
			t.Fatalf("WriteDaemonSubmitRequest(high-%02d): %v", i, err)
		}
	}
	if _, err := projection.WriteDaemonSubmitRequest(lowVolumeSessionDir, projection.DaemonSubmitRequest{
		RequestID: "low-01",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: now,
		Filename:  "20260601-120001-r2222-from-orchestrator-to-worker.md",
		Content:   "low-volume",
	}); err != nil {
		t.Fatalf("WriteDaemonSubmitRequest(low-01): %v", err)
	}

	workerHarness := &daemonSubmitWorkerHarness{}
	var started []string
	rt := &daemonRuntime{
		sessionDir: highVolumeSessionDir,
		cfg:        &config.Config{DaemonSubmitWorkerLimit: workerLimit},
		nodes: map[string]discovery.NodeInfo{
			"b-low-volume:worker": {
				SessionName: "b-low-volume",
				SessionDir:  lowVolumeSessionDir,
			},
		},
		processDaemonSubmit: func(requestPath string) (daemonSubmitProcessResult, error) {
			request, err := projection.ReadDaemonSubmitRequest(requestPath)
			if err != nil {
				return daemonSubmitProcessResult{}, err
			}
			started = append(started, request.RequestID)
			return daemonSubmitProcessResult{}, nil
		},
		launchDaemonSubmitWorker: workerHarness.launch,
	}

	rt.dispatchPendingDaemonSubmitRequests()
	if got := len(workerHarness.workers); got != workerLimit {
		t.Fatalf("queued workers = %d, want %d", got, workerLimit)
	}
	for len(workerHarness.workers) > 0 {
		workerHarness.runNext(t)
	}

	wantStarted := []string{"high-01", "low-01", "high-02", "high-03"}
	if !reflect.DeepEqual(started, wantStarted) {
		t.Fatalf("started requests = %#v, want round-robin order %#v", started, wantStarted)
	}
}

func TestHandlePostWatcherEvent_RateLimitedMessageSchedulesSingleRetry(t *testing.T) {
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

	now := time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC)
	daemonState := NewDaemonState(0, contextID)
	daemonState.SetSessionEnabled(sessionName, true)
	daemonState.lastDeliveryMu.Lock()
	daemonState.lastDeliveryBySenderRecipient["orchestrator:messenger"] = now
	daemonState.lastDeliveryMu.Unlock()

	timerHarness := &runtimeTimerHarness{}
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
		clock: func() time.Time {
			return now
		},
		scheduleRuntimeTimer: timerHarness.schedule,
	}
	rt.nodes[sessionName+":orchestrator"] = discovery.NodeInfo{SessionName: sessionName, SessionDir: sessionDir, PaneID: "%1"}
	rt.nodes[sessionName+":messenger"] = discovery.NodeInfo{SessionName: sessionName, SessionDir: sessionDir, PaneID: "%2"}

	filename := "20260502-010000-r1111-from-orchestrator-to-messenger.md"
	content := "---\nparams:\n  contextId: ctx-rate-limit\n  from: orchestrator\n  to: messenger\n  timestamp: 2026-05-02T01:00:00+09:00\n---\n\nhello after gap\n"
	postPath := filepath.Join(sessionDir, "post", filename)
	if err := os.WriteFile(postPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(post): %v", err)
	}

	rt.handlePostWatcherEvent(postPath, fswatcher.Create)

	if _, err := os.Stat(postPath); err != nil {
		t.Fatalf("rate-limited post file should remain until retry: %v", err)
	}
	if len(timerHarness.calls) != 1 {
		t.Fatalf("scheduled retries = %d, want 1", len(timerHarness.calls))
	}
	if got := timerHarness.calls[0].name; got != "post-rate-limit-retry" {
		t.Fatalf("retry timer name = %q, want post-rate-limit-retry", got)
	}
	if got := timerHarness.calls[0].delay; got != 200*time.Millisecond {
		t.Fatalf("retry timer delay = %s, want 200ms", got)
	}

	rt.handlePostWatcherEvent(postPath, fswatcher.Rename)
	if len(timerHarness.calls) != 1 {
		t.Fatalf("scheduled retries after duplicate event = %d, want 1", len(timerHarness.calls))
	}

	inboxPath := filepath.Join(sessionDir, "inbox", "messenger", filename)
	if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
		t.Fatalf("inbox file should not be delivered before retry gap, got err=%v", err)
	}

	now = now.Add(200 * time.Millisecond)
	timerHarness.runNext(t)
	waitForFileContent(t, inboxPath, content, 10*time.Second)
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

	now := time.Date(2026, 6, 1, 13, 1, 0, 0, time.UTC)
	timerHarness := &runtimeTimerHarness{}
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
		clock: func() time.Time {
			return now
		},
		scheduleRuntimeTimer: timerHarness.schedule,
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
	waitForFileContent(t, firstInboxPath, firstContent, 10*time.Second)
	waitForPostEventIdle(t, rt, firstPostPath, 10*time.Second)
	secondInboxPath := filepath.Join(sessionDir, "inbox", "messenger", secondFilename)
	if _, err := os.Stat(secondInboxPath); !os.IsNotExist(err) {
		t.Fatalf("second same-route message should wait for retry timer, got err=%v", err)
	}
	if len(timerHarness.calls) != 1 {
		t.Fatalf("scheduled retries = %d, want 1", len(timerHarness.calls))
	}

	now = now.Add(200 * time.Millisecond)
	timerHarness.runNext(t)
	waitForFileContent(t, secondInboxPath, secondContent, 10*time.Second)
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

func TestNewAutoPingDispatchSnapshot_ClonesDispatchInputs(t *testing.T) {
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%1",
			SessionName: "review",
			SessionDir:  "review-dir",
		},
	}
	activeNodes := []string{"worker"}
	livenessMap := map[string]bool{"review:worker": true}
	adjacency := map[string][]string{"review:worker": {"review:critic"}}

	snapshot := newAutoPingDispatchSnapshot(nodes, activeNodes, livenessMap, adjacency)

	nodes["review:worker"] = discovery.NodeInfo{PaneID: "%2", SessionName: "review", SessionDir: "mutated"}
	activeNodes[0] = "mutated"
	livenessMap["review:worker"] = false
	adjacency["review:worker"][0] = "review:mutated"

	if got := snapshot.nodes["review:worker"].PaneID; got != "%1" {
		t.Fatalf("snapshot node pane = %q, want original %%1", got)
	}
	if got := snapshot.activeNodes[0]; got != "worker" {
		t.Fatalf("snapshot active node = %q, want worker", got)
	}
	if got := snapshot.livenessMap["review:worker"]; !got {
		t.Fatal("snapshot liveness was mutated")
	}
	if got := snapshot.adjacency["review:worker"][0]; got != "review:critic" {
		t.Fatalf("snapshot adjacency = %q, want review:critic", got)
	}
}

var autoPingSnapshotSink *autoPingDispatchSnapshot

func BenchmarkAutoPingDispatchSnapshotAllocations(b *testing.B) {
	const nodeCount = 256
	nodes := make(map[string]discovery.NodeInfo, nodeCount)
	livenessMap := make(map[string]bool, nodeCount)
	adjacency := make(map[string][]string, nodeCount)
	activeNodes := make([]string, 0, nodeCount)
	for i := 0; i < nodeCount; i++ {
		nodeKey := fmt.Sprintf("review:worker-%03d", i)
		nodes[nodeKey] = discovery.NodeInfo{
			PaneID:      fmt.Sprintf("%%%d", i),
			SessionName: "review",
			SessionDir:  "review-dir",
		}
		livenessMap[nodeKey] = true
		adjacency[nodeKey] = []string{"review:orchestrator", "review:critic"}
		activeNodes = append(activeNodes, fmt.Sprintf("worker-%03d", i))
	}

	b.Run("legacy_per_target_clone", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for j := 0; j < nodeCount; j++ {
				autoPingSnapshotSink = &autoPingDispatchSnapshot{
					activeNodes: append([]string(nil), activeNodes...),
					livenessMap: cloneBoolMap(livenessMap),
					adjacency:   cloneStringSliceMap(adjacency),
					nodes:       cloneNodeInfoMap(nodes),
				}
			}
		}
	})

	b.Run("batched_snapshot", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			autoPingSnapshotSink = newAutoPingDispatchSnapshot(nodes, activeNodes, livenessMap, adjacency)
		}
	})
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

func TestPruneWatchedDirsRemovesDirsForDisappearedNodes(t *testing.T) {
	liveSessionDir := filepath.Join(t.TempDir(), "live")
	staleSessionDir := filepath.Join(t.TempDir(), "stale")
	livePostDir := filepath.Join(liveSessionDir, "post")
	stalePostDir := filepath.Join(staleSessionDir, "post")
	staleSubmitDir := projection.DaemonSubmitRequestsDir(staleSessionDir)
	watcher := &recordingFilesystemWatcher{}
	rt := &daemonRuntime{
		watcher: watcher,
		watchedDirs: map[string]bool{
			livePostDir:    true,
			stalePostDir:   true,
			staleSubmitDir: true,
		},
	}

	rt.pruneWatchedDirs(map[string]discovery.NodeInfo{
		"review:worker": {SessionDir: liveSessionDir},
	})

	if !rt.watchedDirs[livePostDir] {
		t.Fatalf("pruneWatchedDirs() removed live dir %q", livePostDir)
	}
	if rt.watchedDirs[stalePostDir] || rt.watchedDirs[staleSubmitDir] {
		t.Fatalf("pruneWatchedDirs() kept stale dirs: %#v", rt.watchedDirs)
	}
	sort.Strings(watcher.removed)
	wantRemoved := []string{stalePostDir, staleSubmitDir}
	sort.Strings(wantRemoved)
	if !reflect.DeepEqual(watcher.removed, wantRemoved) {
		t.Fatalf("removed dirs = %#v, want %#v", watcher.removed, wantRemoved)
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
	waitForAutoPingEventIdle(t, rt, "review:worker", 2*time.Second)

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

func TestRecordPendingAutoPing_SkipsSamePaneAfterDirectPingDelivery(t *testing.T) {
	baseDir := t.TempDir()
	sessionDir := filepath.Join(baseDir, "ctx-self", "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs(): %v", err)
	}

	now := time.Date(2026, time.May, 4, 14, 45, 0, 0, time.UTC)
	installShadowJournalManager(sessionDir, "ctx-self", "review", now)
	t.Cleanup(journal.ClearProcessManager)
	if err := journal.RecordProcessEvent(sessionDir, "review", projection.AutoPingDeliveredEventType, journal.VisibilityOperatorVisible, projection.AutoPingEventPayload{
		NodeKey:          "review:worker",
		SessionName:      "review",
		NodeName:         "worker",
		PaneID:           "%77",
		Reason:           "operator_tui",
		ResolutionReason: "operator_tui",
		TriggeredAt:      now.Format(time.RFC3339Nano),
		NotBeforeAt:      now.Format(time.RFC3339Nano),
		DeliveredAt:      now.Format(time.RFC3339Nano),
	}, now); err != nil {
		t.Fatalf("RecordProcessEvent(delivered): %v", err)
	}

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
	}, "discovered", now.Add(time.Second))

	state, ok, err := projection.ProjectAutoPingState(sessionDir)
	if err != nil {
		t.Fatalf("ProjectAutoPingState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectAutoPingState() ok = false, want true")
	}
	node := state.Nodes["review:worker"]
	if node.Pending {
		t.Fatalf("same-pane discovered auto-PING queued after direct delivery: %#v", node)
	}
	if node.DeliveredAt == "" || node.ResolutionReason != "operator_tui" {
		t.Fatalf("direct delivery state not preserved: %#v", node)
	}
}

func TestRecordPendingAutoPing_AllowsReplacementPaneAfterDirectPingDelivery(t *testing.T) {
	baseDir := t.TempDir()
	sessionDir := filepath.Join(baseDir, "ctx-self", "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs(): %v", err)
	}

	now := time.Date(2026, time.May, 4, 14, 46, 0, 0, time.UTC)
	installShadowJournalManager(sessionDir, "ctx-self", "review", now)
	t.Cleanup(journal.ClearProcessManager)
	if err := journal.RecordProcessEvent(sessionDir, "review", projection.AutoPingDeliveredEventType, journal.VisibilityOperatorVisible, projection.AutoPingEventPayload{
		NodeKey:          "review:worker",
		SessionName:      "review",
		NodeName:         "worker",
		PaneID:           "%77",
		Reason:           "operator_tui",
		ResolutionReason: "operator_tui",
		TriggeredAt:      now.Format(time.RFC3339Nano),
		NotBeforeAt:      now.Format(time.RFC3339Nano),
		DeliveredAt:      now.Format(time.RFC3339Nano),
	}, now); err != nil {
		t.Fatalf("RecordProcessEvent(delivered): %v", err)
	}

	rt := &daemonRuntime{
		baseDir:   baseDir,
		contextID: "ctx-self",
		cfg: &config.Config{
			AutoPingDelaySeconds: 20,
		},
	}
	rt.recordPendingAutoPing("review:worker", discovery.NodeInfo{
		PaneID:      "%78",
		SessionName: "review",
		SessionDir:  sessionDir,
	}, "pane_restart", now.Add(time.Second))

	state, ok, err := projection.ProjectAutoPingState(sessionDir)
	if err != nil {
		t.Fatalf("ProjectAutoPingState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectAutoPingState() ok = false, want true")
	}
	node := state.Nodes["review:worker"]
	if !node.Pending || node.PaneID != "%78" || node.Reason != "pane_restart" {
		t.Fatalf("replacement pane auto-PING was not queued: %#v", node)
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
	if err := config.WriteSessionPIDFile(filepath.Join(dir, "postman.pid"), os.Getpid()); err != nil {
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
