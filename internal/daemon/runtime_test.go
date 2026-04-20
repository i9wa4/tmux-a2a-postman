package daemon

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
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

func TestDetectNewNodes_HonorsAutoEnableFlag(t *testing.T) {
	freshNodes := map[string]discovery.NodeInfo{
		"foreign:worker": {
			SessionName: "foreign",
			SessionDir:  t.TempDir(),
		},
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher(): %v", err)
	}
	defer func() { _ = watcher.Close() }()

	rtDisabled := &daemonRuntime{
		cfg:         config.DefaultConfig(),
		watcher:     watcher,
		knownNodes:  make(map[string]bool),
		watchedDirs: make(map[string]bool),
		daemonState: NewDaemonState(0, "ctx-disabled"),
		events:      make(chan tui.DaemonEvent, 1),
	}
	rtDisabled.detectNewNodes(freshNodes, false)
	if rtDisabled.daemonState.GetConfiguredSessionEnabled("foreign") {
		t.Fatal("detectNewNodes() auto-enabled a foreign session even though auto-enable was disabled")
	}

	watcherEnabled, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("NewWatcher(enabled): %v", err)
	}
	defer func() { _ = watcherEnabled.Close() }()

	rtEnabled := &daemonRuntime{
		cfg:         config.DefaultConfig(),
		watcher:     watcherEnabled,
		knownNodes:  make(map[string]bool),
		watchedDirs: make(map[string]bool),
		daemonState: NewDaemonState(0, "ctx-enabled"),
		events:      make(chan tui.DaemonEvent, 1),
	}
	rtEnabled.detectNewNodes(freshNodes, true)
	if !rtEnabled.daemonState.GetConfiguredSessionEnabled("foreign") {
		t.Fatal("detectNewNodes() did not auto-enable a foreign session when the auto-enable flag was true")
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
	if !strings.Contains(source, "rt.detectNewNodes(freshNodes, autoEnableSessions)") {
		t.Fatal("runtime.handleScanTick no longer passes the config-backed auto-enable decision into detectNewNodes")
	}
	if strings.Contains(source, "rt.detectNewNodes(freshNodes, true)") {
		t.Fatal("runtime.handleScanTick still hardcodes foreign-session auto-enable")
	}
}

func appendRuntimeMailboxEventForTest(t *testing.T, writer *journal.Writer, eventType string, visibility journal.Visibility, payload journal.MailboxEventPayload, now time.Time) {
	t.Helper()
	if _, err := writer.AppendEvent(eventType, visibility, payload, now); err != nil {
		t.Fatalf("AppendEvent(%s): %v", eventType, err)
	}
}
