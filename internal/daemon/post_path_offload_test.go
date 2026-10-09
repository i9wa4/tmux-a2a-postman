package daemon

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/msgtrace"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

// installPostPaneIdentityStub replaces the live tmux identity lookup with a
// map of paneID -> {session, title}; unknown panes return an error (pane gone).
// The map is read on every lookup, so tests may mutate it between posts.
func installPostPaneIdentityStub(t *testing.T, live map[string][2]string) {
	t.Helper()
	original := postPaneIdentityLookup
	postPaneIdentityLookup = func(paneID string) (string, string, error) {
		identity, ok := live[paneID]
		if !ok {
			return "", "", fmt.Errorf("can't find pane %s", paneID)
		}
		return identity[0], identity[1], nil
	}
	t.Cleanup(func() { postPaneIdentityLookup = original })
}

func TestPostEndpointsIdentityValid(t *testing.T) {
	nodes := map[string]discovery.NodeInfo{
		"sess:orchestrator": {SessionName: "sess", PaneID: "%1"},
		"sess:worker":       {SessionName: "sess", PaneID: "%2"},
	}
	sessionDir := filepath.Join("base", "ctx", "sess")
	filename := "20261010-000000-r0001-from-orchestrator-to-worker.md"

	cases := []struct {
		name  string
		live  map[string][2]string
		nodes map[string]discovery.NodeInfo
		want  bool
	}{
		{"unchanged", map[string][2]string{"%1": {"sess", "orchestrator"}, "%2": {"sess", "worker"}}, nodes, true},
		{"recipient pane gone", map[string][2]string{"%1": {"sess", "orchestrator"}}, nodes, false},
		{"recipient renamed", map[string][2]string{"%1": {"sess", "orchestrator"}, "%2": {"sess", "critic"}}, nodes, false},
		{"recipient moved session", map[string][2]string{"%1": {"sess", "orchestrator"}, "%2": {"other", "worker"}}, nodes, false},
		{"non-tmux backend never reused", map[string][2]string{"%1": {"sess", "orchestrator"}, "%2": {"sess", "worker"}}, map[string]discovery.NodeInfo{
			"sess:orchestrator": {SessionName: "sess", PaneID: "%1"},
			"sess:worker":       {SessionName: "sess", PaneID: "%2", Backend: "herdr"},
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			installPostPaneIdentityStub(t, tc.live)
			rt := &daemonRuntime{nodes: tc.nodes}
			if got := rt.postEndpointsIdentityValid(sessionDir, filename); got != tc.want {
				t.Fatalf("postEndpointsIdentityValid = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestProcessActivePostEventRediscoversWhenCachedPaneReplaced covers G871-2: a
// same-key node whose pane was replaced inside the reuse window must not be
// served from the stale cache.
func TestProcessActivePostEventRediscoversWhenCachedPaneReplaced(t *testing.T) {
	previousLog := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(previousLog) })
	installRuntimeTestTmux(t, t.TempDir())

	live := map[string][2]string{"%1": {"sess", "orchestrator"}, "%2": {"sess", "worker"}}
	installPostPaneIdentityStub(t, live)

	baseDir := t.TempDir()
	sessionDir := filepath.Join(baseDir, "ctx", "sess")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	var discoveries atomic.Int32
	var workerPane atomic.Value
	workerPane.Store("%2")
	originalDiscover := discoverNodesWithCollisionsForRuntime
	discoverNodesWithCollisionsForRuntime = func(string, string, string) (map[string]discovery.NodeInfo, []discovery.CollisionReport, error) {
		discoveries.Add(1)
		return map[string]discovery.NodeInfo{
			"sess:orchestrator": {SessionName: "sess", SessionDir: sessionDir, PaneID: "%1"},
			"sess:worker":       {SessionName: "sess", SessionDir: sessionDir, PaneID: workerPane.Load().(string)},
		}, nil, nil
	}
	t.Cleanup(func() { discoverNodesWithCollisionsForRuntime = originalDiscover })

	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	rt := &daemonRuntime{
		baseDir:     baseDir,
		sessionDir:  sessionDir,
		contextID:   "ctx",
		selfSession: "sess",
		adjacency:   map[string][]string{},

		activePostEvents: map[string]bool{},
		watcher:          &recordingFilesystemWatcher{},
		knownNodes:       map[string]bool{},
		claimedPanes:     map[string]bool{},
		watchedDirs:      map[string]bool{},

		cfg:         &config.Config{TmuxTimeout: 1.0, NodeOrder: []string{"orchestrator", "worker"}},
		events:      make(chan tui.DaemonEvent, 256),
		daemonState: NewDaemonState(0, "ctx"),
		idleTracker: idle.NewIdleTracker(),
		clock:       func() time.Time { return now },
	}
	rt.daemonState.SetSessionEnabled("sess", true)

	post := func(name string) {
		t.Helper()
		postPath := filepath.Join(sessionDir, "post", name)
		if err := os.WriteFile(postPath, []byte("body\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if !rt.beginPostEvent(postPath) {
			t.Fatalf("beginPostEvent(%s) = false", postPath)
		}
		rt.processActivePostEvent(postPath, name)
	}

	post("20261010-000001-r0001-from-orchestrator-to-worker.md")
	post("20261010-000002-r0002-from-orchestrator-to-worker.md")
	if got := discoveries.Load(); got != 1 {
		t.Fatalf("discovery ran %d times with an unchanged topology, want 1", got)
	}

	// The worker pane is replaced: same node key, new pane id, old pane gone.
	delete(live, "%2")
	live["%3"] = [2]string{"sess", "worker"}
	workerPane.Store("%3")
	post("20261010-000003-r0003-from-orchestrator-to-worker.md")
	if got := discoveries.Load(); got != 2 {
		t.Fatalf("discovery ran %d times after the pane was replaced, want 2 (stale NodeInfo must not be reused)", got)
	}
	if got := rt.nodes["sess:worker"].PaneID; got != "%3" {
		t.Fatalf("cached worker pane = %q, want %%3", got)
	}

	post("20261010-000004-r0004-from-orchestrator-to-worker.md")
	if got := discoveries.Load(); got != 2 {
		t.Fatalf("discovery ran %d times after refresh, want reuse again (2)", got)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rt.postEventsMu.Lock()
		active := len(rt.activePostEvents)
		rt.postEventsMu.Unlock()
		if active == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPreDeliverySyncIsSerializedAndPrecedesDelivery covers G871-1: several
// delivery goroutines must hand their pre-delivery sync to ONE worker (max
// concurrency 1, so #871 adds no sync-vs-sync overlap), and each sync must run
// before DeliverMessage for its message (no inbox file yet).
func TestPreDeliverySyncIsSerializedAndPrecedesDelivery(t *testing.T) {
	previousLog := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(previousLog) })
	installRuntimeTestTmux(t, t.TempDir())

	baseDir := t.TempDir()
	sessionDir := filepath.Join(baseDir, "ctx", "sess")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"sess:orchestrator": {SessionName: "sess", SessionDir: sessionDir, PaneID: "%1"},
		"sess:worker":       {SessionName: "sess", SessionDir: sessionDir, PaneID: "%2"},
	}

	var (
		inFlight, maxInFlight atomic.Int32
		mu                    sync.Mutex
		syncedBeforeDelivery  = map[string]bool{}
		deliveredEarly        []string
	)
	original := preDeliverySyncFn
	preDeliverySyncFn = func(dir string, fields msgtrace.Fields) {
		cur := inFlight.Add(1)
		for {
			prev := maxInFlight.Load()
			if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
		if _, err := os.Stat(filepath.Join(dir, "inbox", "worker", fields.MessageID)); err == nil {
			mu.Lock()
			deliveredEarly = append(deliveredEarly, fields.MessageID)
			mu.Unlock()
		}
		mu.Lock()
		syncedBeforeDelivery[fields.MessageID] = true
		mu.Unlock()
		inFlight.Add(-1)
	}
	t.Cleanup(func() { preDeliverySyncFn = original })

	rt := &daemonRuntime{
		contextID:        "ctx",
		selfSession:      "sess",
		nodes:            nodes,
		adjacency:        map[string][]string{"orchestrator": {"worker"}},
		activePostEvents: map[string]bool{},
		cfg:              &config.Config{EnterDelay: 0.01, TmuxTimeout: 1.0},
		events:           make(chan tui.DaemonEvent, 256),
		daemonState:      NewDaemonState(0, "ctx"),
		idleTracker:      idle.NewIdleTracker(),
	}
	rt.daemonState.SetSessionEnabled("sess", true)

	const posts = 6
	for i := 0; i < posts; i++ {
		name := fmt.Sprintf("20261010-0200%02d-from-orchestrator-to-worker.md", i)
		content := "---\nparams:\n  contextId: ctx\n  from: orchestrator\n  to: worker\n  messageId: " + name + "\n  timestamp: 2026-10-10T02:00:00+09:00\n---\n\nbody\n"
		postPath := filepath.Join(sessionDir, "post", name)
		if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if !rt.beginPostEvent(postPath) {
			t.Fatalf("beginPostEvent(%s) = false", postPath)
		}
		rt.dispatchPostDelivery(postPath, name, nodes, rt.adjacency, rt.cfg, postDeliveryReservation{})
	}

	waitForInboxEntries(t, sessionDir, "worker", posts)

	if got := maxInFlight.Load(); got != 1 {
		t.Fatalf("max concurrent pre-delivery syncs = %d, want 1 (single serialized worker)", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(syncedBeforeDelivery) != posts {
		t.Fatalf("pre-delivery sync ran for %d messages, want %d", len(syncedBeforeDelivery), posts)
	}
	if len(deliveredEarly) != 0 {
		t.Fatalf("messages already in the inbox when their pre-delivery sync ran: %v", deliveredEarly)
	}
}

// TestPreDeliverySyncTimeoutFailsClosed covers the G871-1 timeout edge: when the
// pre-delivery sync outlasts the wait, DeliverMessage must NOT run (it would
// overlap the still-running sync and widen the #802 sync-vs-pop window). The
// post stays in post/ for the pending-post reconciler, and once the sync can
// complete a retry delivers it exactly once.
func TestPreDeliverySyncTimeoutFailsClosed(t *testing.T) {
	previousLog := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(previousLog) })
	installRuntimeTestTmux(t, t.TempDir())

	baseDir := t.TempDir()
	sessionDir := filepath.Join(baseDir, "ctx", "sess")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"sess:orchestrator": {SessionName: "sess", SessionDir: sessionDir, PaneID: "%1"},
		"sess:worker":       {SessionName: "sess", SessionDir: sessionDir, PaneID: "%2"},
	}

	release := make(chan struct{})
	var syncStarted, syncFinished atomic.Int32
	originalSync := preDeliverySyncFn
	preDeliverySyncFn = func(string, msgtrace.Fields) {
		syncStarted.Add(1)
		<-release
		syncFinished.Add(1)
	}
	originalTimeout := preDeliverySyncWaitTimeout
	preDeliverySyncWaitTimeout = 150 * time.Millisecond
	t.Cleanup(func() {
		preDeliverySyncFn = originalSync
		preDeliverySyncWaitTimeout = originalTimeout
	})

	installPostPaneIdentityStub(t, map[string][2]string{
		"%1": {"sess", "orchestrator"},
		"%2": {"sess", "worker"},
	})
	originalDiscover := discoverNodesWithCollisionsForRuntime
	discoverNodesWithCollisionsForRuntime = func(string, string, string) (map[string]discovery.NodeInfo, []discovery.CollisionReport, error) {
		return nodes, nil, nil
	}
	t.Cleanup(func() { discoverNodesWithCollisionsForRuntime = originalDiscover })

	events := make(chan tui.DaemonEvent, 256)
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

		cfg:         &config.Config{EnterDelay: 0.01, TmuxTimeout: 1.0, NodeOrder: []string{"orchestrator", "worker"}},
		events:      events,
		daemonState: NewDaemonState(0, "ctx"),
		idleTracker: idle.NewIdleTracker(),
	}
	rt.daemonState.SetSessionEnabled("sess", true)

	name := "20261010-030000-from-orchestrator-to-worker.md"
	content := "---\nparams:\n  contextId: ctx\n  from: orchestrator\n  to: worker\n  messageId: " + name + "\n  timestamp: 2026-10-10T03:00:00+09:00\n---\n\nbody\n"
	postPath := filepath.Join(sessionDir, "post", name)
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	waitIdle := func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			rt.postEventsMu.Lock()
			active := len(rt.activePostEvents)
			rt.postEventsMu.Unlock()
			if active == 0 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("delivery goroutine did not finish")
	}

	// First attempt through the real pending-post reconciler.
	rt.dispatchPendingPostMessages()
	waitIdle() // returns only after the wait timeout fired

	sawStallEvent := false
	for drained := false; !drained; {
		select {
		case ev := <-events:
			if ev.Type == "error" && strings.Contains(ev.Message, "pre-delivery sync wait timeout") {
				sawStallEvent = true
			}
		default:
			drained = true
		}
	}
	if !sawStallEvent {
		t.Fatal("a wedged pre-delivery sync must surface a visible error event")
	}
	if syncStarted.Load() != 1 || syncFinished.Load() != 0 {
		t.Fatalf("sync started=%d finished=%d, want the sync still running", syncStarted.Load(), syncFinished.Load())
	}
	if _, err := os.Stat(postPath); err != nil {
		t.Fatalf("post file must stay in post/ after a sync timeout: %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(sessionDir, "inbox", "worker")); err == nil && len(entries) != 0 {
		t.Fatalf("message delivered while its pre-delivery sync was still running (%d inbox entries)", len(entries))
	}

	// The sync can finish now; the reconciler's retry must deliver exactly
	// once, and further reconcile passes must not deliver it again.
	close(release)
	rt.dispatchPendingPostMessages()
	waitForInboxEntries(t, sessionDir, "worker", 1)
	waitIdle()
	rt.dispatchPendingPostMessages()
	waitIdle()
	rt.waitForMailboxProjectionSyncs()
	waitForInboxEntries(t, sessionDir, "worker", 1)
	if syncFinished.Load() < 1 {
		t.Fatal("the original sync never finished")
	}
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Fatalf("post file still present after the successful retry: %v", err)
	}
}

func TestPostDiscoveryFreshWindow(t *testing.T) {
	base := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	nodes := map[string]discovery.NodeInfo{"s:worker": {SessionName: "s"}}

	cases := []struct {
		name  string
		rt    daemonRuntime
		now   time.Time
		fresh bool
	}{
		{"never discovered", daemonRuntime{nodes: nodes}, base, false},
		{"no nodes yet", daemonRuntime{lastPostDiscoveryAt: base}, base, false},
		{"inside window", daemonRuntime{nodes: nodes, lastPostDiscoveryAt: base}, base.Add(postDiscoveryReuseWindow - time.Millisecond), true},
		{"at window edge", daemonRuntime{nodes: nodes, lastPostDiscoveryAt: base}, base.Add(postDiscoveryReuseWindow), false},
		{"clock went backwards", daemonRuntime{nodes: nodes, lastPostDiscoveryAt: base}, base.Add(-time.Second), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rt.postDiscoveryFresh(tc.now); got != tc.fresh {
				t.Fatalf("postDiscoveryFresh = %v, want %v", got, tc.fresh)
			}
		})
	}
}

func TestPostEndpointsKnown(t *testing.T) {
	rt := &daemonRuntime{nodes: map[string]discovery.NodeInfo{
		"sess:orchestrator": {SessionName: "sess"},
		"sess:worker":       {SessionName: "sess"},
	}}
	sessionDir := filepath.Join("base", "ctx", "sess")

	cases := []struct {
		filename string
		want     bool
	}{
		{"20261010-000000-r0001-from-orchestrator-to-worker.md", true},
		{"20261010-000000-r0001-from-ghost-to-worker.md", false},
		{"20261010-000000-r0001-from-orchestrator-to-ghost.md", false},
		{"20261010-000000-r0001-from-postman-to-worker.md", true},
		{"not-a-message.md", false},
	}
	for _, tc := range cases {
		if got := rt.postEndpointsKnown(sessionDir, tc.filename); got != tc.want {
			t.Fatalf("postEndpointsKnown(%q) = %v, want %v", tc.filename, got, tc.want)
		}
	}
}

// TestProcessActivePostEventReusesDiscoveryUnderPostBurst is the #871
// regression for the dispatcher-loop stall: every post used to fork tmux
// discovery inline on the single select loop that also dispatches
// daemon-submit pop requests. With a slow injected discovery step, a burst of
// posts (including cap-stuck retries of the same file) must pay that cost
// once per postDiscoveryReuseWindow, not once per post, while an unknown
// endpoint must still force a fresh discovery.
func TestProcessActivePostEventReusesDiscoveryUnderPostBurst(t *testing.T) {
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

	const slowStep = 150 * time.Millisecond
	// A slow pre-delivery sync must not be paid on the dispatcher loop: before
	// the fix each post ran it inline (here 5 x 200 ms on top of discovery).
	const slowSync = 200 * time.Millisecond
	originalSync := preDeliverySyncFn
	preDeliverySyncFn = func(dir string, fields msgtrace.Fields) {
		time.Sleep(slowSync)
		originalSync(dir, fields)
	}
	t.Cleanup(func() { preDeliverySyncFn = originalSync })
	var discoveries atomic.Int32
	originalDiscover := discoverNodesWithCollisionsForRuntime
	discoverNodesWithCollisionsForRuntime = func(string, string, string) (map[string]discovery.NodeInfo, []discovery.CollisionReport, error) {
		discoveries.Add(1)
		time.Sleep(slowStep)
		return map[string]discovery.NodeInfo{
			"sess:orchestrator": {SessionName: "sess", SessionDir: sessionDir, PaneID: "%1"},
			"sess:worker":       {SessionName: "sess", SessionDir: sessionDir, PaneID: "%2"},
		}, nil, nil
	}
	t.Cleanup(func() { discoverNodesWithCollisionsForRuntime = originalDiscover })

	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	rt := &daemonRuntime{
		baseDir:     baseDir,
		sessionDir:  sessionDir,
		contextID:   "ctx",
		selfSession: "sess",
		adjacency:   map[string][]string{},

		activePostEvents: map[string]bool{},
		watcher:          &recordingFilesystemWatcher{},
		knownNodes:       map[string]bool{},
		claimedPanes:     map[string]bool{},
		watchedDirs:      map[string]bool{},

		// NodeOrder makes discoverNodes' runtime-config filter keep the nodes.
		cfg:         &config.Config{TmuxTimeout: 1.0, NodeOrder: []string{"orchestrator", "worker"}},
		events:      make(chan tui.DaemonEvent, 256),
		daemonState: NewDaemonState(0, "ctx"),
		idleTracker: idle.NewIdleTracker(),
		clock:       func() time.Time { return now },
	}
	rt.daemonState.SetSessionEnabled("sess", true)

	post := func(name string) {
		t.Helper()
		postPath := filepath.Join(sessionDir, "post", name)
		if err := os.WriteFile(postPath, []byte("body\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if !rt.beginPostEvent(postPath) {
			t.Fatalf("beginPostEvent(%s) = false", postPath)
		}
		rt.processActivePostEvent(postPath, name)
	}

	const posts = 5
	start := time.Now()
	for i := 0; i < posts; i++ {
		post("20261010-00000" + string(rune('0'+i)) + "-r0001-from-orchestrator-to-worker.md")
	}
	elapsed := time.Since(start)

	if got := discoveries.Load(); got != 1 {
		t.Fatalf("discovery ran %d times for %d back-to-back posts, want 1", got, posts)
	}
	// Before the fix the loop paid (slowStep + slowSync) per post:
	// posts*(150+200) ms = 1750 ms. After: one discovery (150 ms) plus the
	// bounded identity checks (instant in this stub).
	t.Logf("loop held %s for %d posts (before the fix: >= %s)", elapsed, posts, time.Duration(posts)*(slowStep+slowSync))
	if limit := slowStep + 500*time.Millisecond; elapsed > limit {
		t.Fatalf("post burst held the dispatcher for %s, want <= %s", elapsed, limit)
	}

	// A post from an endpoint missing in the cached topology must force a
	// fresh discovery even inside the reuse window.
	post("20261010-000008-r0003-from-ghost-to-worker.md")
	if got := discoveries.Load(); got != 2 {
		t.Fatalf("discovery ran %d times after an unknown-sender post, want 2", got)
	}

	// Past the window the next post must rediscover (topology stays fresh).
	now = now.Add(postDiscoveryReuseWindow + time.Millisecond)
	post("20261010-000009-r0002-from-orchestrator-to-worker.md")
	if got := discoveries.Load(); got != 3 {
		t.Fatalf("discovery ran %d times after the reuse window, want 3", got)
	}

	// Let delivery goroutines drain before the test tears down shared seams.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rt.postEventsMu.Lock()
		active := len(rt.activePostEvents)
		rt.postEventsMu.Unlock()
		if active == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}
