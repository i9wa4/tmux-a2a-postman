package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fswatcher/fswatcher"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

// TestPostBurstWithConcurrentPopsLosesNothing covers R4 of the #871 review: the
// pre-delivery mailbox projection sync now runs on one dedicated worker,
// concurrently with the pop worker, the post-delivery syncs and the projection
// syncs. A burst of posts to one recipient with pops running at the same time
// must lose nothing: once the syncs have drained, every message has exactly one
// read/ file and no copy left in inbox/, post/ or dead-letter/. Run under -race
// in CI.
//
// Exactly-once POP is deliberately not asserted here: with a single pop worker
// per node (the production dispatchKey invariant) and the daemon's post-pop
// steps replicated, this same burst already pops messages more than once on
// UNMODIFIED origin/main (2e3429c), because a stale projection sync recreates
// inbox files after a pop (#802, fixed and strictly asserted on the #802
// branch). Duplicate counts are only reported through BURST_STATS.
func TestPostBurstWithConcurrentPopsLosesNothing(t *testing.T) {
	previousLog := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(previousLog) })
	installRuntimeTestTmux(t, t.TempDir())
	installPostPaneIdentityStub(t, map[string][2]string{
		"%1": {"sess", "orchestrator"},
		"%2": {"sess", "worker"},
	})

	baseDir := t.TempDir()
	sessionDir := filepath.Join(baseDir, "ctx", "sess")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	manager := journal.NewManager("ctx", os.Getpid())
	journal.InstallProcessManager(manager)
	t.Cleanup(journal.ClearProcessManager)
	if err := manager.Bootstrap(sessionDir, "sess", time.Now()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	nodes := map[string]discovery.NodeInfo{
		"sess:orchestrator": {SessionName: "sess", SessionDir: sessionDir, PaneID: "%1"},
		"sess:worker":       {SessionName: "sess", SessionDir: sessionDir, PaneID: "%2"},
	}
	originalDiscover := discoverNodesWithCollisionsForRuntime
	discoverNodesWithCollisionsForRuntime = func(string, string, string) (map[string]discovery.NodeInfo, []discovery.CollisionReport, error) {
		return nodes, nil, nil
	}
	t.Cleanup(func() { discoverNodesWithCollisionsForRuntime = originalDiscover })

	rt := &daemonRuntime{
		baseDir:     baseDir,
		sessionDir:  sessionDir,
		contextID:   "ctx",
		selfSession: "sess",
		nodes:       nodes,
		adjacency:   map[string][]string{"orchestrator": {"worker"}},

		activePostEvents: map[string]bool{},
		watcher:          &recordingFilesystemWatcher{},
		knownNodes:       map[string]bool{},
		claimedPanes:     map[string]bool{},
		watchedDirs:      map[string]bool{},

		// NodeOrder makes discoverNodes' runtime-config filter keep the nodes.
		cfg:         &config.Config{EnterDelay: 0.01, TmuxTimeout: 1.0, NodeOrder: []string{"orchestrator", "worker"}},
		events:      make(chan tui.DaemonEvent, 1024),
		daemonState: NewDaemonState(0, "ctx"),
		idleTracker: idle.NewIdleTracker(),
	}
	rt.daemonState.SetSessionEnabled("sess", true)

	const posts = 12
	want := make([]string, 0, posts)
	for i := 0; i < posts; i++ {
		want = append(want, fmt.Sprintf("20261010-0100%02d-from-orchestrator-to-worker.md", i))
	}

	var (
		mu     sync.Mutex
		popped = map[string]int{}
		done   = make(chan struct{})
	)
	// One pop worker per node: the production dispatcher dedupes pops per
	// session+node via daemonSubmitDispatchKey ("pop:<session>:<node>"), so at
	// most one pop for a node is in flight. The posts, the pre-delivery sync
	// worker, the post-delivery syncs and the mailbox projection syncs still
	// run concurrently with it, which is the interleaving under test.
	var wg sync.WaitGroup
	for w := 0; w < 1; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for seq := 0; ; seq++ {
				select {
				case <-done:
					return
				default:
				}
				requestID := fmt.Sprintf("race-pop-%d-%d", worker, seq)
				requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
					RequestID: requestID,
					Command:   projection.DaemonSubmitPop,
					CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
					Node:      "worker",
				})
				if err != nil {
					t.Errorf("WriteDaemonSubmitRequest: %v", err)
					return
				}
				popResult, err := processDaemonSubmitRequest(requestPath)
				if err != nil {
					t.Errorf("processDaemonSubmitRequest: %v", err)
					return
				}
				responsePath := projection.DaemonSubmitResponsePath(sessionDir, requestID)
				raw, err := os.ReadFile(responsePath)
				if err != nil {
					t.Errorf("ReadFile response: %v", err)
					return
				}
				_ = os.Remove(responsePath)
				var response projection.DaemonSubmitResponse
				if err := json.Unmarshal(raw, &response); err != nil {
					t.Errorf("Unmarshal response: %v", err)
					return
				}
				if response.Empty || response.Filename == "" {
					time.Sleep(2 * time.Millisecond)
					continue
				}
				mu.Lock()
				popped[response.Filename]++
				// Do what the running daemon does after a pop: the read/
				// archive appearing fires the read watcher, which journals the
				// read event BEFORE any later projection sync (the
				// "read-event-before-sync" ordering), and the daemon-submit
				// result schedules a coalesced projection sync. Without the
				// read event a later sync would rebuild the inbox file from
				// stale projection state, a harness artifact and not daemon
				// behaviour.
				rt.handleReadWatcherEvent(filepath.Join(sessionDir, "read", response.Filename), fswatcher.Create)
				if popResult.ProjectionSyncSessionDir != "" {
					rt.scheduleMailboxProjectionSync(popResult.ProjectionSyncSessionDir)
				}
				mu.Unlock()
			}
		}(w)
	}

	for _, name := range want {
		content := "---\nparams:\n  contextId: ctx\n  from: orchestrator\n  to: worker\n  messageId: " + name + "\n  timestamp: 2026-10-10T01:00:00+09:00\n---\n\nbody " + name + "\n"
		postPath := filepath.Join(sessionDir, "post", name)
		if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if !rt.beginPostEvent(postPath) {
			t.Fatalf("beginPostEvent(%s) = false", postPath)
		}
		rt.processActivePostEvent(postPath, name)
	}

	wantSet := make(map[string]bool, posts)
	for _, name := range want {
		wantSet[name] = true
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		seen := 0
		for name := range popped {
			if wantSet[name] {
				seen++
			}
		}
		mu.Unlock()
		if seen >= posts {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Let the delivery goroutines finish their post-delivery syncs and the pop
	// worker drain the inbox (a stale sync may resurrect a message, which is
	// then popped again), then let the post-pop syncs settle before looking at
	// the final state.
	settled := func() bool {
		rt.postEventsMu.Lock()
		active := len(rt.activePostEvents)
		rt.postEventsMu.Unlock()
		if active != 0 {
			return false
		}
		entries, _ := os.ReadDir(filepath.Join(sessionDir, "inbox", "worker"))
		for _, e := range entries {
			if wantSet[e.Name()] {
				return false
			}
		}
		return true
	}
	for time.Now().Before(deadline) {
		if settled() {
			rt.waitForMailboxProjectionSyncs()
			if settled() {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(done)
	wg.Wait()
	rt.waitForMailboxProjectionSyncs()

	// System messages the daemon enqueues on its own (for example the
	// auto-PING to a newly discovered node) are not part of this burst.
	// Machine-readable per-run statistics for paired main-vs-branch runs.
	dupMessages, extraPops := 0, 0
	for name, count := range popped {
		if wantSet[name] && count > 1 {
			dupMessages++
			extraPops += count - 1
		}
	}
	var missed []string
	for _, name := range want {
		if popped[name] > 0 {
			continue
		}
		state := "not-delivered-to-inbox(latency)"
		if _, err := os.Stat(filepath.Join(sessionDir, "inbox", "worker", name)); err == nil {
			state = "delivered-but-not-popped(#802-class)"
		} else if _, err := os.Stat(filepath.Join(sessionDir, "read", name)); err == nil {
			state = "archived-in-read-but-not-observed-by-pop(#802-class)"
		}
		missed = append(missed, name+"="+state)
	}
	t.Logf("BURST_STATS posts=%d dup_messages=%d extra_pops=%d missed=%d missed_detail=%v", posts, dupMessages, extraPops, len(missed), missed)

	got := make([]string, 0, len(popped))
	for name := range popped {
		if wantSet[name] {
			got = append(got, name)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("popped messages = %v, want %v", got, want)
	}

	// Final state after the syncs drained: exactly one read/ file per message,
	// and no copy left in inbox/, post/ or dead-letter/.
	for _, name := range want {
		if _, err := os.Stat(filepath.Join(sessionDir, "read", name)); err != nil {
			t.Errorf("read/%s missing: %v", name, err)
		}
		for _, dir := range []string{filepath.Join("inbox", "worker"), "post", "dead-letter"} {
			entries, _ := os.ReadDir(filepath.Join(sessionDir, dir))
			for _, e := range entries {
				if strings.Contains(e.Name(), name) {
					t.Errorf("%s still holds a copy of %s (%s)", dir, name, e.Name())
				}
			}
		}
	}
}
