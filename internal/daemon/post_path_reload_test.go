package daemon

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/msgtrace"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

// installLoggingTestTmux installs a fake tmux that records every invocation's
// arguments (one line each) and accepts the paste/send-keys family.
func installLoggingTestTmux(t *testing.T, tmpDir string) string {
	t.Helper()
	fakeBin := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(fakeBin, 0o700); err != nil {
		t.Fatalf("MkdirAll(fakeBin): %v", err)
	}
	logPath := filepath.Join(tmpDir, "tmux.log")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\ncase \"$1\" in\n  set-buffer|paste-buffer|send-keys) exit 0 ;;\n  *) exit 1 ;;\nesac\n", logPath)
	if err := os.WriteFile(filepath.Join(fakeBin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// TestPostDeliveryReloadsSharedTopologyAfterPreDeliverySyncWait covers
// F-872-1: the topology captured on the select loop can go stale while the
// delivery goroutine waits behind the serialized pre-delivery sync. After the
// wait, DeliverMessage (and the recipient post-delivery sync) must use the
// topology currently published through rt.sharedNodes, as the #572 M1 budget
// retry does, so the notification reaches the replacement pane and not the
// pane that was replaced during the wait.
func TestPostDeliveryReloadsSharedTopologyAfterPreDeliverySyncWait(t *testing.T) {
	previousLog := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(previousLog) })
	tmuxLog := installLoggingTestTmux(t, t.TempDir())

	baseDir := t.TempDir()
	sessionDir := filepath.Join(baseDir, "ctx", "sess")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	// The select loop captured staleNodes; the worker pane %2 is replaced by %3
	// (same node key) while the delivery goroutine waits for the sync.
	staleNodes := map[string]discovery.NodeInfo{
		"sess:orchestrator": {SessionName: "sess", SessionDir: sessionDir, PaneID: "%1"},
		"sess:worker":       {SessionName: "sess", SessionDir: sessionDir, PaneID: "%2"},
	}
	freshNodes := map[string]discovery.NodeInfo{
		"sess:orchestrator": {SessionName: "sess", SessionDir: sessionDir, PaneID: "%1"},
		"sess:worker":       {SessionName: "sess", SessionDir: sessionDir, PaneID: "%3"},
	}

	syncEntered := make(chan struct{})
	release := make(chan struct{})
	var syncCalls atomic.Int32
	originalSync := preDeliverySyncFn
	preDeliverySyncFn = func(dir string, fields msgtrace.Fields) {
		if syncCalls.Add(1) == 1 {
			close(syncEntered)
			<-release
		}
		originalSync(dir, fields)
	}
	t.Cleanup(func() { preDeliverySyncFn = originalSync })

	rt := &daemonRuntime{
		contextID:        "ctx",
		selfSession:      "sess",
		nodes:            staleNodes,
		adjacency:        map[string][]string{"orchestrator": {"worker"}},
		activePostEvents: map[string]bool{},
		cfg:              &config.Config{EnterDelay: 0.01, TmuxTimeout: 1.0, NotificationTemplate: "NOTIFY worker"},
		events:           make(chan tui.DaemonEvent, 256),
		daemonState:      NewDaemonState(0, "ctx"),
		idleTracker:      idle.NewIdleTracker(),
	}
	rt.daemonState.SetSessionEnabled("sess", true)
	// storeSharedNodes is a no-op without sharedNodes, which production always
	// sets before the runtime loop starts.
	rt.sharedNodes = &atomic.Pointer[map[string]discovery.NodeInfo]{}
	rt.storeSharedNodes()

	name := "20261010-050000-from-orchestrator-to-worker.md"
	content := "---\nparams:\n  contextId: ctx\n  from: orchestrator\n  to: worker\n  messageId: " + name + "\n  timestamp: 2026-10-10T05:00:00+09:00\n---\n\nbody\n"
	postPath := filepath.Join(sessionDir, "post", name)
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !rt.beginPostEvent(postPath) {
		t.Fatalf("beginPostEvent(%s) = false", postPath)
	}
	rt.dispatchPostDelivery(postPath, name, staleNodes, rt.adjacency, rt.cfg, postDeliveryReservation{})

	select {
	case <-syncEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("pre-delivery sync never started")
	}
	// The topology changes while the delivery goroutine waits: the scan
	// publishes the replacement pane through rt.sharedNodes.
	rt.nodes = freshNodes
	rt.storeSharedNodes()
	close(release)

	waitForInboxEntries(t, sessionDir, "worker", 1)
	deadline := time.Now().Add(5 * time.Second)
	var logged string
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(tmuxLog); err == nil && strings.Contains(string(data), "%3") {
			logged = string(data)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rt.waitForMailboxProjectionSyncs()
	if data, err := os.ReadFile(tmuxLog); err == nil {
		logged = string(data)
	}
	if !strings.Contains(logged, "%3") {
		t.Fatalf("notification never reached the replacement pane %%3; tmux calls:\n%s", logged)
	}
	if strings.Contains(logged, "%2") {
		t.Fatalf("notification went to the pane replaced during the sync wait (%%2); tmux calls:\n%s", logged)
	}
}
