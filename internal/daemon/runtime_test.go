package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

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

func TestResumeCompatibilityMailboxProjections_RestoresKnownSessionTrees(t *testing.T) {
	baseDir := t.TempDir()
	primarySessionDir := filepath.Join(baseDir, "ctx-main", "review")
	secondarySessionDir := filepath.Join(baseDir, "ctx-main", "critic")
	now := time.Date(2026, time.April, 14, 4, 30, 0, 0, time.UTC)

	primaryWriter, err := journal.OpenShadowWriter(primarySessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter(primary) error = %v", err)
	}
	secondaryWriter, err := journal.OpenShadowWriter(secondarySessionDir, "ctx-main", "critic", 102, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter(secondary) error = %v", err)
	}

	primaryFilename := "20260414-043001-r1111-from-orchestrator-to-worker.md"
	primaryContent := "---\nparams:\n  from: orchestrator\n  to: worker\n---\n\nPrimary inbox payload\n"
	appendRuntimeMailboxEventForTest(t, primaryWriter, "compatibility_mailbox_delivered", journal.VisibilityCompatibilityMailbox, journal.MailboxEventPayload{
		MessageID: primaryFilename,
		From:      "orchestrator",
		To:        "worker",
		Path:      filepath.Join("inbox", "worker", primaryFilename),
		Content:   primaryContent,
	}, now.Add(time.Second))

	secondaryFilename := "20260414-043002-r2222-from-review-to-critic.md"
	secondaryContent := "---\nfrom: review\nto: critic\nstate: stalled\nexpects_reply: true\n---\n"
	appendRuntimeMailboxEventForTest(t, secondaryWriter, "compatibility_mailbox_waiting_created", journal.VisibilityOperatorVisible, journal.MailboxEventPayload{
		MessageID: secondaryFilename,
		From:      "review",
		To:        "critic",
		Path:      filepath.Join("waiting", secondaryFilename),
		Content:   secondaryContent,
	}, now.Add(2*time.Second))

	primaryProjectedPath := filepath.Join(primarySessionDir, "inbox", "worker", primaryFilename)
	if err := os.MkdirAll(filepath.Dir(primaryProjectedPath), 0o700); err != nil {
		t.Fatalf("MkdirAll(primary projected): %v", err)
	}
	if err := os.WriteFile(primaryProjectedPath, []byte("stale primary"), 0o600); err != nil {
		t.Fatalf("WriteFile(primary stale): %v", err)
	}

	secondaryProjectedPath := filepath.Join(secondarySessionDir, "waiting", secondaryFilename)
	if err := os.MkdirAll(filepath.Dir(secondaryProjectedPath), 0o700); err != nil {
		t.Fatalf("MkdirAll(secondary projected): %v", err)
	}
	if err := os.WriteFile(secondaryProjectedPath, []byte("stale secondary"), 0o600); err != nil {
		t.Fatalf("WriteFile(secondary stale): %v", err)
	}

	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			SessionName: "review",
			SessionDir:  primarySessionDir,
		},
		"critic:critic": {
			SessionName: "critic",
			SessionDir:  secondarySessionDir,
		},
	}

	if err := resumeCompatibilityMailboxProjections(primarySessionDir, nodes); err != nil {
		t.Fatalf("resumeCompatibilityMailboxProjections() error = %v", err)
	}

	gotPrimary, err := os.ReadFile(primaryProjectedPath)
	if err != nil {
		t.Fatalf("ReadFile(primary projected): %v", err)
	}
	if string(gotPrimary) != primaryContent {
		t.Fatalf("primary projection content = %q, want %q", string(gotPrimary), primaryContent)
	}

	gotSecondary, err := os.ReadFile(secondaryProjectedPath)
	if err != nil {
		t.Fatalf("ReadFile(secondary projected): %v", err)
	}
	if string(gotSecondary) != secondaryContent {
		t.Fatalf("secondary projection content = %q, want %q", string(gotSecondary), secondaryContent)
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

func TestDetectNewNodes_ReturnsOnlyNewRealNodesWithoutAutoEnable(t *testing.T) {
	freshNodes := map[string]discovery.NodeInfo{
		"self:known": {
			SessionName: "self",
			SessionDir:  t.TempDir(),
		},
		"foreign:worker": {
			SessionName: "foreign",
			SessionDir:  t.TempDir(),
		},
		"phony:helper": {
			SessionName: "phony",
			SessionDir:  t.TempDir(),
			IsPhony:     true,
		},
	}
	watcher, err := fsnotify.NewWatcher()
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

func TestHandleScanTick_SourceContractUsesAutoEnableNewSessionsConfig(t *testing.T) {
	sourceBytes, err := os.ReadFile("runtime.go")
	if err != nil {
		t.Fatalf("ReadFile(runtime.go): %v", err)
	}
	source := string(sourceBytes)

	if !strings.Contains(source, "autoEnableSessions := config.BoolVal(rt.cfg.AutoEnableNewSessions, false)") {
		t.Fatal("runtime.handleScanTick no longer derives session auto-enable from cfg.AutoEnableNewSessions")
	}
	if !strings.Contains(source, "newNodes := rt.detectNewNodes(freshNodes)") {
		t.Fatal("runtime.handleScanTick no longer collects newly discovered node keys from detectNewNodes")
	}
	if !strings.Contains(source, "rt.dispatchPendingAutoPings(freshNodes, autoEnableSessions") {
		t.Fatal("runtime.handleScanTick no longer passes the config-backed auto-enable decision into dispatchPendingAutoPings")
	}
	if strings.Contains(source, "rt.detectNewNodes(freshNodes, autoEnableSessions)") {
		t.Fatal("runtime.handleScanTick still pushes auto-enable side effects into detectNewNodes")
	}
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

	entries, err := os.ReadDir(filepath.Join(sessionDir, "inbox", "worker"))
	if err != nil {
		t.Fatalf("ReadDir inbox: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("inbox entries = %d, want 1", len(entries))
	}

	state, ok, err := projection.ProjectAutoPingState(sessionDir)
	if err != nil {
		t.Fatalf("ProjectAutoPingState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectAutoPingState() ok = false, want true")
	}
	if state.Nodes["review:worker"].Pending {
		t.Fatal("pending auto-PING debt was not cleared after confirmed delivery")
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

	rt.bootstrap(context.Background())

	entries, err := os.ReadDir(filepath.Join(sessionDir, "inbox", "worker"))
	if err != nil {
		t.Fatalf("ReadDir inbox: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("inbox entries after bootstrap = %d, want 1", len(entries))
	}

	state, ok, err := projection.ProjectAutoPingState(sessionDir)
	if err != nil {
		t.Fatalf("ProjectAutoPingState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectAutoPingState() ok = false, want true")
	}
	if state.Nodes["review:worker"].Pending {
		t.Fatal("pending auto-PING debt was not cleared during bootstrap reconciliation")
	}
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
	if err := os.WriteFile(filepath.Join(dir, "postman.pid"), []byte("1"), 0o600); err != nil {
		t.Fatalf("WriteFile(postman.pid): %v", err)
	}
}

func appendRuntimeMailboxEventForTest(t *testing.T, writer *journal.Writer, eventType string, visibility journal.Visibility, payload journal.MailboxEventPayload, now time.Time) {
	t.Helper()
	if _, err := writer.AppendEvent(eventType, visibility, payload, now); err != nil {
		t.Fatalf("AppendEvent(%s): %v", eventType, err)
	}
}
