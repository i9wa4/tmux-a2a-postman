package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofsnotify/fsnotify"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

func TestHandleWatcherEvent_ConfigAndNodesEventsDoNotMutateStartupSnapshot(t *testing.T) {
	tmpDir := t.TempDir()

	baseDir := filepath.Join(tmpDir, "state")
	contextID := "ctx-startup-snapshot"
	sessionName := "review-session"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	configPath := filepath.Join(tmpDir, "postman.toml")
	nodesDir := filepath.Join(tmpDir, "nodes")
	if err := os.MkdirAll(nodesDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(nodes): %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.ScanInterval = 1
	cfg.SessionScanInterval = 1
	events := make(chan tui.DaemonEvent, 8)
	rt := &daemonRuntime{
		baseDir:          baseDir,
		sessionDir:       sessionDir,
		contextID:        contextID,
		selfSession:      sessionName,
		cfg:              cfg,
		adjacency:        map[string][]string{},
		nodes:            map[string]discovery.NodeInfo{},
		knownNodes:       map[string]bool{},
		events:           events,
		configPath:       configPath,
		configPaths:      []string{configPath},
		nodesDirs:        []string{nodesDir},
		daemonState:      NewDaemonState(0, contextID),
		idleTracker:      idle.NewIdleTracker(),
		watchedDirs:      map[string]bool{},
		claimedPanes:     map[string]bool{},
		prevSessionNodes: map[string][]string{},
		activePostEvents: map[string]bool{},
	}

	writeWatcherReloadConfig(t, configPath, 7)
	rt.handleWatcherEvent(fsnotify.Event{Name: configPath, Op: fsnotify.Write})
	assertNoConfigReloadEvent(t, events)
	if got := rt.cfg.ScanInterval; got != 1 {
		t.Fatalf("config path event mutated ScanInterval = %v, want startup snapshot 1", got)
	}

	writeWatcherReloadConfig(t, configPath, 11)
	rt.handleWatcherEvent(fsnotify.Event{Name: filepath.Join(nodesDir, "worker.toml"), Op: fsnotify.Create})
	assertNoConfigReloadEvent(t, events)
	if got := rt.cfg.ScanInterval; got != 1 {
		t.Fatalf("nodes dir event mutated ScanInterval = %v, want startup snapshot 1", got)
	}
}

func TestHandleWatcherEvent_PostCreateAndAtomicRenameDeliverMessages(t *testing.T) {
	rt, sessionDir := newWatcherPostRuntime(t, "ctx-post-watch", "review-session")

	cases := []struct {
		name string
		op   fsnotify.Op
		put  func(path, content string)
	}{
		{
			name: "create",
			op:   fsnotify.Create,
			put: func(path, content string) {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatalf("WriteFile(post): %v", err)
				}
			},
		},
		{
			name: "atomic rename",
			op:   fsnotify.Rename,
			put: func(path, content string) {
				tmpPath := filepath.Join(t.TempDir(), filepath.Base(path)+".tmp")
				if err := os.WriteFile(tmpPath, []byte(content), 0o600); err != nil {
					t.Fatalf("WriteFile(tmp post): %v", err)
				}
				if err := os.Rename(tmpPath, path); err != nil {
					t.Fatalf("Rename(tmp post): %v", err)
				}
			},
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			filename := fmt.Sprintf("20260508-10110%d-r1111-from-orchestrator-to-messenger.md", i)
			content := fmt.Sprintf("---\nparams:\n  contextId: ctx-post-watch\n  from: orchestrator\n  to: messenger\n  timestamp: 2026-05-08T10:11:0%d+09:00\n---\n\n%s delivery\n", i, tc.name)
			postPath := filepath.Join(sessionDir, "post", filename)
			tc.put(postPath, content)

			rt.handleWatcherEvent(fsnotify.Event{Name: postPath, Op: tc.op})

			inboxPath := filepath.Join(sessionDir, "inbox", "messenger", filename)
			waitForFileContent(t, inboxPath, content, 10*time.Second)
			waitForFileGone(t, postPath, 10*time.Second)
			waitForPostEventIdle(t, rt, postPath, 10*time.Second)
		})
	}
}

func TestHandleWatcherEvent_PostWriteOnlyWaitsForRescan(t *testing.T) {
	rt, sessionDir := newWatcherPostRuntime(t, "ctx-post-rescan", "review-session")

	filename := "20260508-101200-r1111-from-orchestrator-to-messenger.md"
	content := "---\nparams:\n  contextId: ctx-post-rescan\n  from: orchestrator\n  to: messenger\n  timestamp: 2026-05-08T10:12:00+09:00\n---\n\nrescan delivery\n"
	postPath := filepath.Join(sessionDir, "post", filename)
	if err := os.WriteFile(postPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(post): %v", err)
	}

	rt.handleWatcherEvent(fsnotify.Event{Name: postPath, Op: fsnotify.Write})

	inboxPath := filepath.Join(sessionDir, "inbox", "messenger", filename)
	if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
		t.Fatalf("write-only watcher event delivered inbox unexpectedly: %v", err)
	}
	rt.postEventsMu.Lock()
	active := rt.activePostEvents[postPath]
	rt.postEventsMu.Unlock()
	if active {
		t.Fatal("write-only watcher event started post delivery; want rescan to own recovery")
	}

	rt.dispatchPendingPostMessages()

	waitForFileContent(t, inboxPath, content, 10*time.Second)
	waitForFileGone(t, postPath, 10*time.Second)
	waitForPostEventIdle(t, rt, postPath, 10*time.Second)
}

func TestHandleWatcherEvent_ReadRenameMarksRecipientAliveAndSyncsProjection(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "state")
	contextID := "ctx-read-watch"
	sessionName := "review-session"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	installShadowJournalManager(sessionDir, contextID, sessionName, time.Now())
	t.Cleanup(journal.ClearProcessManager)

	events := make(chan tui.DaemonEvent, 4)
	tracker := idle.NewIdleTracker()
	rt := &daemonRuntime{
		events:      events,
		idleTracker: tracker,
	}

	filename := "20260508-101300-r1111-from-orchestrator-to-worker.md"
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-05-08T10:13:00+09:00\n---\n\nread archive\n"
	readPath := filepath.Join(sessionDir, "read", filename)
	if err := os.WriteFile(readPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(read): %v", err)
	}

	rt.handleWatcherEvent(fsnotify.Event{Name: readPath, Op: fsnotify.Rename})

	event := waitForDaemonEvent(t, events, "read node_alive", func(event tui.DaemonEvent) bool {
		return event.Type == "node_alive"
	})
	if got := event.Details["node"]; got != sessionName+":worker" {
		t.Fatalf("node_alive node = %#v, want %q", got, sessionName+":worker")
	}
	if got := event.Details["source"]; got != "read_move" {
		t.Fatalf("node_alive source = %#v, want read_move", got)
	}
	if !tracker.GetLivenessMap()[sessionName+":worker"] {
		t.Fatalf("read event did not mark %s:worker alive", sessionName)
	}

	projected, ok, err := projection.ProjectMailboxProjection(sessionDir)
	if err != nil {
		t.Fatalf("ProjectMailboxProjection: %v", err)
	}
	if !ok {
		t.Fatal("ProjectMailboxProjection ok = false, want true")
	}
	readKey := filepath.Join("read", filename)
	if got := projected.Read[readKey].Content; got != content {
		t.Fatalf("projected read content = %q, want %q", got, content)
	}
}

func TestRunDaemonLoop_WatcherErrorIsNonFatalAndLaterReadEventIsProcessed(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "state")
	contextID := "ctx-watch-error"
	sessionName := "review-session"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	installShadowJournalManager(sessionDir, contextID, sessionName, time.Now())
	t.Cleanup(journal.ClearProcessManager)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		if errors.Is(err, fsnotify.ErrUnsupported) {
			t.Skipf("fsnotify unsupported on this platform: %v", err)
		}
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = watcher.Close() }()

	cfg := config.DefaultConfig()
	cfg.ScanInterval = 3600
	cfg.SessionScanInterval = 3600
	events := make(chan tui.DaemonEvent, 16)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunDaemonLoop(
			ctx,
			baseDir,
			sessionDir,
			contextID,
			cfg,
			watcher,
			map[string][]string{},
			map[string]discovery.NodeInfo{},
			map[string]bool{},
			events,
			"",
			nil,
			nil,
			NewDaemonState(0, contextID),
			idle.NewIdleTracker(),
			nil,
			sessionName,
		)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("daemon loop did not exit after cancellation")
		}
	})

	firstFilename := "20260508-101400-r1111-from-orchestrator-to-worker.md"
	firstReadPath := filepath.Join(sessionDir, "read", firstFilename)
	if err := os.WriteFile(firstReadPath, []byte("---\nparams:\n  from: orchestrator\n  to: worker\n---\n\nread before error\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(read): %v", err)
	}
	watcher.Events <- fsnotify.Event{Name: firstReadPath, Op: fsnotify.Create}

	event := waitForDaemonEvent(t, events, "node_alive before watcher error", func(event tui.DaemonEvent) bool {
		return event.Type == "node_alive"
	})
	if got := event.Details["node"]; got != sessionName+":worker" {
		t.Fatalf("node_alive before watcher error node = %#v, want %q", got, sessionName+":worker")
	}

	watcher.Errors <- errors.New("transient backend error")
	waitForDaemonEvent(t, events, "watcher error", func(event tui.DaemonEvent) bool {
		return event.Type == "error" && strings.Contains(event.Message, "watcher error: transient backend error")
	})

	secondFilename := "20260508-101401-r1111-from-orchestrator-to-critic.md"
	secondReadPath := filepath.Join(sessionDir, "read", secondFilename)
	if err := os.WriteFile(secondReadPath, []byte("---\nparams:\n  from: orchestrator\n  to: critic\n---\n\nread after error\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(second read): %v", err)
	}
	watcher.Events <- fsnotify.Event{Name: secondReadPath, Op: fsnotify.Rename}

	event = waitForDaemonEvent(t, events, "node_alive after watcher error", func(event tui.DaemonEvent) bool {
		return event.Type == "node_alive"
	})
	if got := event.Details["node"]; got != sessionName+":critic" {
		t.Fatalf("node_alive after watcher error node = %#v, want %q", got, sessionName+":critic")
	}
}

func TestFsnotifyWatcherSeesAtomicRenameIntoWatchedPostDir(t *testing.T) {
	tmpDir := t.TempDir()
	postDir := filepath.Join(tmpDir, "post")
	if err := os.MkdirAll(postDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(post): %v", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		if errors.Is(err, fsnotify.ErrUnsupported) {
			t.Skipf("fsnotify unsupported on this platform: %v", err)
		}
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = watcher.Close() }()
	if err := watcher.Add(postDir, fsnotify.All); err != nil {
		if errors.Is(err, fsnotify.ErrUnsupported) {
			t.Skipf("fsnotify unsupported on this platform: %v", err)
		}
		t.Fatalf("watcher.Add(post): %v", err)
	}

	finalPath := filepath.Join(postDir, "20260508-101500-r1111-from-orchestrator-to-worker.md")
	tmpPath := filepath.Join(t.TempDir(), filepath.Base(finalPath)+".tmp")
	if err := os.WriteFile(tmpPath, []byte("atomic payload"), 0o600); err != nil {
		t.Fatalf("WriteFile(tmp): %v", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		t.Fatalf("Rename(tmp -> post): %v", err)
	}

	event := waitForFsnotifyEvent(t, watcher, finalPath, fsnotify.Create|fsnotify.Rename)
	if event.Name != finalPath {
		t.Fatalf("fsnotify event.Name = %q, want %q", event.Name, finalPath)
	}
}

func newWatcherPostRuntime(t *testing.T, contextID, sessionName string) (*daemonRuntime, string) {
	t.Helper()
	tmpDir := t.TempDir()
	installRuntimeTestTmux(t, tmpDir)

	baseDir := filepath.Join(tmpDir, "state")
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

	daemonState := NewDaemonState(0, contextID)
	daemonState.SetSessionEnabled(sessionName, true)
	rt := &daemonRuntime{
		baseDir:          baseDir,
		sessionDir:       sessionDir,
		contextID:        contextID,
		selfSession:      sessionName,
		cfg:              cfg,
		adjacency:        adjacency,
		nodes:            map[string]discovery.NodeInfo{},
		knownNodes:       map[string]bool{},
		events:           make(chan tui.DaemonEvent, 16),
		daemonState:      daemonState,
		idleTracker:      idle.NewIdleTracker(),
		watchedDirs:      map[string]bool{},
		claimedPanes:     map[string]bool{},
		prevSessionNodes: map[string][]string{},
		activePostEvents: map[string]bool{},
	}
	rt.nodes[sessionName+":orchestrator"] = discovery.NodeInfo{SessionName: sessionName, SessionDir: sessionDir, PaneID: "%1"}
	rt.nodes[sessionName+":messenger"] = discovery.NodeInfo{SessionName: sessionName, SessionDir: sessionDir, PaneID: "%2"}
	return rt, sessionDir
}

func installWatcherReloadTmux(t *testing.T, tmpDir string) {
	t.Helper()
	fakeBin := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(fakeBin, 0o700); err != nil {
		t.Fatalf("MkdirAll(fakeBin): %v", err)
	}
	script := "#!/bin/sh\ncase \"$1\" in\n  list-panes) exit 0 ;;\n  list-sessions) printf '%s\\t$1\\n' 'review-session'; exit 0 ;;\n  set-option|show-options) exit 0 ;;\n  *) exit 0 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func writeWatcherReloadConfig(t *testing.T, path string, scanInterval float64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(config dir): %v", err)
	}
	content := fmt.Sprintf("[postman]\nscan_interval_seconds = %.1f\nsession_scan_interval_seconds = 3600.0\nedges = [\"orchestrator --- worker\"]\n", scanInterval)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}
}

func assertNoConfigReloadEvent(t *testing.T, events <-chan tui.DaemonEvent) {
	t.Helper()
	timer := time.NewTimer(25 * time.Millisecond)
	defer timer.Stop()

	for {
		select {
		case event := <-events:
			if event.Type == "message_received" && event.Message == "Config reloaded" {
				t.Fatalf("unexpected config reload event: %#v", event)
			}
			if event.Type == "config_update" {
				t.Fatalf("unexpected config update event: %#v", event)
			}
		case <-timer.C:
			return
		}
	}
}

func waitForDaemonEvent(t *testing.T, events <-chan tui.DaemonEvent, description string, accept func(tui.DaemonEvent) bool) tui.DaemonEvent {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()

	var seen []string
	for {
		select {
		case event := <-events:
			if accept(event) {
				return event
			}
			seen = append(seen, fmt.Sprintf("%s:%s", event.Type, event.Message))
		case <-timer.C:
			t.Fatalf("timed out waiting for %s; seen events=%v", description, seen)
		}
	}
}

func waitForFsnotifyEvent(t *testing.T, watcher *fsnotify.Watcher, path string, op fsnotify.Op) fsnotify.Event {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()

	var seen []string
	for {
		select {
		case event := <-watcher.Events:
			seen = append(seen, event.String())
			if event.Name == path && event.Op&op != 0 {
				return event
			}
		case err := <-watcher.Errors:
			t.Fatalf("unexpected watcher error while waiting for %s: %v", path, err)
		case <-timer.C:
			t.Fatalf("timed out waiting for watcher event path=%s op=%s; seen=%v", path, op, seen)
		}
	}
}
