package daemon

import (
	"bytes"
	"context"
	"fmt"
	"log"
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
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
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

func TestHandleWatcherEvent_CompatibilitySubmitSendDispatchesPostWithoutPostWatcherEvent(t *testing.T) {
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
	cfg.Edges = []string{"orchestrator -- messenger"}
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
	requestPath, err := projection.WriteCompatibilitySubmitRequest(sessionDir, projection.CompatibilitySubmitRequest{
		RequestID: "req-send",
		Command:   projection.CompatibilitySubmitSend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  filename,
		Content:   content,
	})
	if err != nil {
		t.Fatalf("WriteCompatibilitySubmitRequest: %v", err)
	}

	rt.handleWatcherEvent(fsnotify.Event{Name: requestPath, Op: fsnotify.Create})

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
	cfg.Edges = []string{"orchestrator -- messenger"}
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
	rt.handlePostWatcherEvent(postPath, fsnotify.Create)

	if _, err := os.Stat(postPath); err != nil {
		t.Fatalf("rate-limited post file should remain until retry: %v", err)
	}

	inboxPath := filepath.Join(sessionDir, "inbox", "messenger", filename)
	if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
		t.Fatalf("inbox file should not be delivered before retry gap, got err=%v", err)
	}
	deliveredAt := waitForFileContent(t, inboxPath, content, time.Second)
	if elapsed := deliveredAt.Sub(start); elapsed < 150*time.Millisecond {
		t.Fatalf("message delivered before rate-limit gap elapsed: %s", elapsed)
	}
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Fatalf("post file still present after retry or wrong error: %v", err)
	}
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
	cfg.Edges = []string{"orchestrator -- messenger"}
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

	rt.handlePostWatcherEvent(firstPostPath, fsnotify.Create)
	rt.handlePostWatcherEvent(secondPostPath, fsnotify.Create)

	firstInboxPath := filepath.Join(sessionDir, "inbox", "messenger", firstFilename)
	firstDeliveredAt := waitForFileContent(t, firstInboxPath, firstContent, time.Second)
	secondInboxPath := filepath.Join(sessionDir, "inbox", "messenger", secondFilename)
	if _, err := os.Stat(secondInboxPath); !os.IsNotExist(err) {
		t.Fatalf("second same-route message should wait behind in-flight delivery, got err=%v", err)
	}

	secondDeliveredAt := waitForFileContent(t, secondInboxPath, secondContent, 2*time.Second)
	if elapsed := secondDeliveredAt.Sub(firstDeliveredAt); elapsed < 150*time.Millisecond {
		t.Fatalf("second same-route message delivered too soon after first: %s", elapsed)
	}
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
	cfg.Edges = []string{"orchestrator -- messenger"}
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
	recordCompatibilityMailboxPayload(sessionDir, sessionName, "compatibility_mailbox_posted", journal.VisibilityCompatibilityMailbox, journal.MailboxEventPayload{
		MessageID: filename,
		From:      "orchestrator",
		To:        "messenger",
		Path:      shadowRelativePath(sessionDir, postPath),
		Content:   content,
	})

	rt.bootstrap(context.Background())

	inboxPath := filepath.Join(sessionDir, "inbox", "messenger", filename)
	waitForFileContent(t, inboxPath, content, time.Second)
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Fatalf("post file still present after bootstrap backlog reconciliation or wrong error: %v", err)
	}
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
