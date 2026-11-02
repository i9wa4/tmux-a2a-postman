package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

func configuredStatusFixture(t *testing.T, f tmuxFixture, m Mail) Tmux {
	t.Helper()
	rt := f.adapter()
	path := filepath.Join(f.home, "postman.md")
	if err := os.WriteFile(path, []byte("## `edges`\n```mermaid\ngraph LR\nworker --- approver\n```\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	topology, err := config.LoadWorkspaceTopology(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	rt.ConfigPath = path
	rt.ConfigFingerprint = StatusConfigFingerprint(topology)
	if _, err := m.Claim(context.Background(), rt, ""); err != nil {
		t.Fatal(err)
	}
	return rt
}

func refreshFixture(t *testing.T, m Mail, f tmuxFixture, at time.Time) ServerStatus {
	t.Helper()
	result, err := m.RefreshServerStatus(context.Background(), ServerContext{Socket: f.socket}, f.home, RefreshOptions{now: func() time.Time { return at }})
	if err != nil && result.Status != "partial" {
		t.Fatal(result, err)
	}
	_, snapshot, err := m.Store.ReadServerStatus(ServerContext{Socket: f.socket})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func firstStatusPane(t *testing.T, s ServerStatus) StatusPane {
	t.Helper()
	return s.Sessions[0].Windows[0].Panes[0]
}

func TestServerRefreshCadenceHistoryCountsAndReplacement(t *testing.T) {
	f := newTmuxFixture(t)
	m := mailFixture(t)
	rt := configuredStatusFixture(t, f, m)
	ordinary := f.run(t, "new-window", "-d", "-t", "one", "-P", "-F", "#{pane_id}", "printf NATIVE_UNCONFIGURED; exec sleep 120")
	f.run(t, "select-pane", "-t", ordinary, "-T", "ordinary-terminal")
	f.run(t, "new-session", "-d", "-s", "other-session", "exec sleep 120")
	at := time.Now().UTC()
	a := refreshFixture(t, m, f, at)
	if a.Coverage.Captured != 3 || len(a.Sessions) != 2 || !a.Coverage.Complete {
		t.Fatal(a)
	}
	p := firstStatusPane(t, a)
	if p.Status == nil || p.Status.UnreadCount == nil || p.Capture == nil || p.Capture.ChangedAt != nil || p.Scope == nil {
		t.Fatal(p)
	}
	b := refreshFixture(t, m, f, at.Add(time.Second))
	if b.Coverage.Captured != 0 || b.Coverage.Reused != 3 || firstStatusPane(t, b).Capture.At != p.Capture.At {
		t.Fatal(b)
	}
	f.run(t, "send-keys", "-t", f.pane, "-l", "screen changed")
	c := refreshFixture(t, m, f, at.Add(StatusCaptureInterval))
	if firstStatusPane(t, c).Capture.ChangedAt == nil {
		t.Fatal(c)
	}
	dir, err := m.Store.statusDir(ServerContext{Socket: f.socket}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dir.Close() }()
	history, err := privateDir(dir, "history", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = history.Close() }()
	entries, err := boundedEntries(history)
	if err != nil || len(entries) != 6 {
		t.Fatal(len(entries), err)
	}
	found := false
	for _, e := range entries {
		raw, err := readStatusFile(history, e.Name())
		if err != nil {
			t.Fatal(err)
		}
		found = found || strings.Contains(string(raw), "NATIVE_UNCONFIGURED")
	}
	if !found {
		t.Fatal("unconfigured pane text not retained")
	}
	if _, err := os.Stat(filepath.Join(m.Store.StateRoot, "repo", "agents", "ordinary-terminal")); !os.IsNotExist(err) {
		t.Fatal("manufactured mailbox", err)
	}
	f.run(t, "respawn-pane", "-k", "-t", f.pane, "exec sleep 120")
	d := refreshFixture(t, m, f, at.Add(StatusCaptureInterval+time.Second))
	replacement := firstStatusPane(t, d)
	if replacement.PID == p.PID || replacement.Capture.ChangedAt != nil || (replacement.Status != nil && replacement.Status.UnreadCount != nil) {
		t.Fatal("replacement reused binding/change baseline", replacement)
	}
	// New normal registration may bind the replacement, never the collector.
	if _, err := m.Claim(context.Background(), rt, ""); err != nil {
		t.Fatal(err)
	}
	e := refreshFixture(t, m, f, at.Add(32*time.Second))
	if firstStatusPane(t, e).Status == nil {
		t.Fatal(e)
	}
	// A backwards wall clock rebaselines instead of reusing future captures.
	rollback := refreshFixture(t, m, f, at.Add(-time.Minute))
	if rollback.Coverage.Captured != 3 || firstStatusPane(t, rollback).Capture.ChangedAt != nil {
		t.Fatal(rollback)
	}
}

func TestServerRefreshBusyIsImmediateAndOwnerMetadataIsOnlyDiagnostic(t *testing.T) {
	f := newTmuxFixture(t)
	m := mailFixture(t)
	server := ServerContext{Socket: f.socket}
	dir, err := m.Store.statusDir(server, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dir.Close() }()
	lock, err := acquireRefresh(dir)
	if err != nil {
		t.Fatal(err)
	}
	owner := RefreshOwner{PID: os.Getpid(), Invocation: "owned-test", AcquiredAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), Target: f.socket, Phase: "capture"}
	if err := replaceJSON(dir, "owner.json", owner); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for range 20 {
		r, err := m.RefreshServerStatus(context.Background(), server, f.home, RefreshOptions{})
		if err != nil || r.Status != "skipped_busy" || !strings.Contains(r.Reason, "long-held") || r.Owner == nil {
			t.Fatal(r, err)
		}
	}
	if time.Since(start) > time.Second {
		t.Fatal("busy refresh queued")
	}
	if err := replaceJSON(dir, "owner.json", map[string]any{"broken": true}); err != nil {
		t.Fatal(err)
	}
	r, err := m.RefreshServerStatus(context.Background(), server, f.home, RefreshOptions{})
	if err != nil || !strings.Contains(r.Reason, "unavailable or invalid") {
		t.Fatal(r, err)
	}
	// A child exec cannot inherit the lock. Parent close releases it even while
	// that child is still alive. This exercises FD_CLOEXEC, not just goroutines.
	child := exec.Command("sleep", "3")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	_ = lock.Close()
	next, err := acquireRefresh(dir)
	if err != nil {
		t.Fatal("child inherited refresh lock", err)
	}
	_ = next.Close()
}

func TestServerRefreshPublicationFailuresPreserveOrDescribeCommittedSnapshot(t *testing.T) {
	f := newTmuxFixture(t)
	m := mailFixture(t)
	at := time.Now().UTC()
	a := refreshFixture(t, m, f, at)
	server := ServerContext{Socket: f.socket}
	before, _, _ := m.Store.ReadServerStatus(server)
	failure := errors.New("injected disk failure")
	r, err := m.RefreshServerStatus(context.Background(), server, f.home, RefreshOptions{now: func() time.Time { return at.Add(time.Second) }, beforePublish: func() error { return failure }})
	if !errors.Is(err, failure) || r.Status != "error" {
		t.Fatal(r, err)
	}
	after, _, _ := m.Store.ReadServerStatus(server)
	if string(before) != string(after) {
		t.Fatal("pre-rename failure replaced snapshot")
	}
	_, err = m.RefreshServerStatus(context.Background(), server, f.home, RefreshOptions{now: func() time.Time { return at.Add(2 * time.Second) }, afterRename: func() error { return failure }})
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "durability uncertain") {
		t.Fatal(err)
	}
	_, published, err := m.Store.ReadServerStatus(server)
	if err != nil || published.AsOf == a.AsOf {
		t.Fatal(published, err)
	}
	// A fatal native error likewise leaves the last complete document unchanged.
	raw, _, _ := m.Store.ReadServerStatus(server)
	bad := ServerContext{Socket: f.socket, PID: 99999999}
	_, err = m.RefreshServerStatus(context.Background(), bad, f.home, RefreshOptions{})
	if err == nil {
		t.Fatal("known PID mismatch accepted")
	}
	got, _, _ := m.Store.ReadServerStatus(server)
	if string(got) != string(raw) {
		t.Fatal("PID mismatch published")
	}
}

func TestServerRefreshSlowCapturesRemainDueAndDoNotAdvanceWholeSweep(t *testing.T) {
	f := newTmuxFixture(t)
	m := mailFixture(t)
	at := time.Now().UTC()
	a := refreshFixture(t, m, f, at)
	real, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	proxy := "#!/bin/sh\ncase \"$*\" in *capture-pane*) sleep 1;; esac\nexec " + shellQuote(real) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(proxy), 0o700); err != nil {
		t.Fatal(err)
	}
	original := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+original)
	r, err := m.RefreshServerStatus(context.Background(), ServerContext{Socket: f.socket}, f.home, RefreshOptions{now: func() time.Time { return at.Add(time.Minute) }})
	if err == nil || r.Status != "partial" || r.Coverage.Unavailable != 1 {
		t.Fatal(r, err)
	}
	_, b, err := m.Store.ReadServerStatus(ServerContext{Socket: f.socket})
	if err != nil || firstStatusPane(t, b).Capture.At != firstStatusPane(t, a).Capture.At || b.Coverage.Complete {
		t.Fatal(b, err)
	}
	t.Setenv("PATH", original)
	c := refreshFixture(t, m, f, at.Add(time.Minute+time.Second))
	if c.Coverage.Captured != 1 {
		t.Fatal("uncaptured pane postponed", c)
	}
}

func TestServerRefreshLockInodeNeverReplaced(t *testing.T) {
	f := newTmuxFixture(t)
	m := mailFixture(t)
	server := ServerContext{Socket: f.socket}
	refreshFixture(t, m, f, time.Now().UTC())
	dir, err := m.Store.statusDir(server, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dir.Close() }()
	before, _ := dir.Stat("refresh.lock")
	refreshFixture(t, m, f, time.Now().UTC())
	after, _ := dir.Stat("refresh.lock")
	if !os.SameFile(before, after) {
		t.Fatal("refresh lock inode replaced")
	}
	lock, err := acquireRefresh(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
}

func TestServerRefreshNativeNumberGapsNamesAndTwoSockets(t *testing.T) {
	f := newTmuxFixture(t)
	m := mailFixture(t)
	for i := 1; i <= 12; i++ {
		name := fmt.Sprintf("display-name-%d", 20-i)
		id := f.run(t, "new-session", "-d", "-s", name, "-c", f.repo, "-P", "-F", "#{session_id}", "exec sleep 120")
		if i != 3 && i != 9 && i != 12 {
			f.run(t, "kill-session", "-t", id)
		}
	}
	f.run(t, "rename-session", "-t", "$9", "renamed-999")
	at := time.Now().UTC()
	a := refreshFixture(t, m, f, at)
	ids := []string{}
	for _, s := range a.Sessions {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != "$0,$3,$9,$12" || a.Sessions[2].Name != "renamed-999" {
		t.Fatal(ids, a.Sessions)
	}
	second := newTmuxFixture(t)
	b := refreshFixture(t, m, second, at)
	if b.Server.Socket == a.Server.Socket || len(b.Sessions) != 1 {
		t.Fatal(b)
	}
	_, unchanged, err := m.Store.ReadServerStatus(ServerContext{Socket: f.socket})
	if err != nil || unchanged.AsOf != a.AsOf {
		t.Fatal(unchanged, err)
	}
	// Reusing a socket after process exit cannot reuse its old capture baseline.
	f.run(t, "kill-server")
	// kill-server acknowledges the command before the daemon necessarily exits.
	// Await the owned process boundary; tmux may retain its stale socket path.
	// Do not delete the socket or retry a runtime operation.
	deadline := time.Now().Add(2 * time.Second)
	for processLiveness(a.Server.PID) != Dead {
		if time.Now().After(deadline) {
			t.Fatal("owned server did not exit before restart")
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.run(t, "-f", "/dev/null", "new-session", "-d", "-s", "replacement", "-c", f.repo, "exec sleep 120")
	c := refreshFixture(t, m, f, at.Add(time.Second))
	if c.Server.Incarnation == a.Server.Incarnation || c.Coverage.Captured != 1 || firstStatusPane(t, c).Capture.ChangedAt != nil {
		t.Fatal(c)
	}
}

func TestServerRefreshConfigurationConflictsCannotDependOnPublisher(t *testing.T) {
	f := newTmuxFixture(t)
	m := mailFixture(t)
	rt := configuredStatusFixture(t, f, m)
	peer := f.run(t, "split-window", "-d", "-t", f.pane, "-c", f.repo, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.run(t, "select-pane", "-t", peer, "-T", "approver")
	other := filepath.Join(f.home, "other.md")
	if err := os.WriteFile(other, []byte("## `edges`\n```mermaid\ngraph LR\napprover --- observer\n```\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	topology, err := config.LoadWorkspaceTopology(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	conflicting := Tmux{Socket: f.socket, Pane: peer, Home: f.home, Roles: topology.Nodes, ConfigPath: other, ConfigFingerprint: StatusConfigFingerprint(topology)}
	if _, err := m.Claim(context.Background(), conflicting, ""); err != nil {
		t.Fatal(err)
	}
	a := refreshFixture(t, m, f, time.Now().UTC())
	p := a.Sessions[0].Windows[0].Panes[1]
	if p.Status == nil || p.Status.UnreadCount != nil || !strings.Contains(p.Status.Reason, "conflicting") {
		t.Fatal(p)
	}
	// Admission by the other role cannot make its config the global winner.
	if _, err := m.Claim(context.Background(), rt, ""); err != nil {
		t.Fatal(err)
	}
	b := refreshFixture(t, m, f, time.Now().UTC())
	p = firstStatusPane(t, b)
	if p.Status == nil || p.Status.UnreadCount != nil || !strings.Contains(p.Status.Reason, "conflicting") {
		t.Fatal(p)
	}
}

func TestRefreshLockChildProcessHelper(t *testing.T) {
	if os.Getenv("POSTMAN_REFRESH_LOCK_HELPER") != "1" {
		return
	}
	m := Mail{Store: Store{StateRoot: os.Getenv("POSTMAN_REFRESH_TEST_ROOT")}}
	phase := os.Getenv("POSTMAN_REFRESH_TEST_PHASE")
	_, err := m.RefreshServerStatus(context.Background(), ServerContext{Socket: os.Getenv("POSTMAN_REFRESH_TEST_SOCKET")}, os.Getenv("POSTMAN_REFRESH_TEST_HOME"), RefreshOptions{phaseStarted: func(current string) {
		if current != phase {
			return
		}
		if err := os.WriteFile(os.Getenv("POSTMAN_REFRESH_TEST_READY"), []byte("ready"), 0o600); err != nil {
			t.Fatal(err)
		}
		for {
			time.Sleep(time.Minute)
		}
	}})
	t.Fatal("child refresh unexpectedly finished", err)
}

func TestServerRefreshProcessDeathReleasesEveryPhaseWithoutDeletingLock(t *testing.T) {
	f := newTmuxFixture(t)
	m := mailFixture(t)
	server := ServerContext{Socket: f.socket}
	refreshFixture(t, m, f, time.Now().UTC())
	dir, err := m.Store.statusDir(server, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dir.Close() }()
	inode, _ := dir.Stat("refresh.lock")
	for _, phase := range []string{"enumerate", "capture", "aggregate", "publish"} {
		t.Run(phase, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "ready")
			child := exec.Command(os.Args[0], "-test.run=^TestRefreshLockChildProcessHelper$")
			child.Env = append(os.Environ(), "POSTMAN_REFRESH_LOCK_HELPER=1", "POSTMAN_REFRESH_TEST_ROOT="+m.Store.StateRoot, "POSTMAN_REFRESH_TEST_SOCKET="+f.socket, "POSTMAN_REFRESH_TEST_PHASE="+phase, "POSTMAN_REFRESH_TEST_HOME="+f.home, "POSTMAN_REFRESH_TEST_READY="+marker)
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
			deadline := time.Now().Add(3 * time.Second)
			for {
				if _, err := os.Stat(marker); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("child never acquired test lock")
				}
				time.Sleep(5 * time.Millisecond)
			}
			result, err := m.RefreshServerStatus(context.Background(), server, f.home, RefreshOptions{})
			if err != nil || result.Status != "skipped_busy" || result.Owner == nil || result.Owner.Phase != phase {
				t.Fatal(result, err)
			}
			if err := child.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = child.Wait()
			refreshFixture(t, m, f, time.Now().UTC())
			after, _ := dir.Stat("refresh.lock")
			if !os.SameFile(inode, after) {
				t.Fatal("process-death recovery replaced lock")
			}
		})
	}
}

func TestServerRefreshPartialCapacityIsNotGlobalFreshness(t *testing.T) {
	f := newTmuxFixture(t)
	m := mailFixture(t)
	for range SnapshotPaneLimit + 1 {
		f.run(t, "new-window", "-d", "-t", "one", "-c", f.repo, "exec sleep 120")
	}
	at := time.Now().UTC()
	r, err := m.RefreshServerStatus(context.Background(), ServerContext{Socket: f.socket}, f.home, RefreshOptions{now: func() time.Time { return at }})
	if err == nil || r.Status != "partial" || r.Coverage.Enumerated != SnapshotPaneLimit+2 || r.Coverage.Captured > SnapshotPaneLimit || r.Coverage.Complete || r.Coverage.Unavailable == 0 {
		t.Fatal(r, err)
	}
	// Immediately following sweep covers previously missing observations even
	// though earlier successful pane captures are not due again.
	r2, _ := m.RefreshServerStatus(context.Background(), ServerContext{Socket: f.socket}, f.home, RefreshOptions{now: func() time.Time { return at.Add(time.Second) }})
	if r2.Coverage == nil || r2.Coverage.Captured == 0 || r2.Coverage.Reused == 0 {
		t.Fatal("partial sweep postponed missing panes", r2)
	}
}

func TestServerRefreshAutomaticWindowRenameDoesNotInvalidateNativeIdentity(t *testing.T) {
	f := newTmuxFixture(t)
	m := mailFixture(t)
	server := ServerContext{Socket: f.socket}
	f.run(t, "rename-window", "-t", f.pane, "initial-display-name")
	result, err := m.RefreshServerStatus(context.Background(), server, f.home, RefreshOptions{phaseStarted: func(phase string) {
		if phase == "capture" {
			f.run(t, "rename-window", "-t", f.pane, "updated-display-name")
		}
	}})
	if err != nil || result.Status != "updated" {
		t.Fatal(result, err)
	}
	before, snapshot, err := m.Store.ReadServerStatus(server)
	if err != nil || snapshot.Sessions[0].Windows[0].Name != "updated-display-name" {
		t.Fatal(snapshot, err)
	}
	// Permitting display renames must not permit process or routing replacement.
	result, err = m.RefreshServerStatus(context.Background(), server, f.home, RefreshOptions{phaseStarted: func(phase string) {
		if phase == "capture" {
			f.run(t, "respawn-pane", "-k", "-t", f.pane, "exec sleep 120")
		}
	}})
	if err == nil || result.Status != "error" {
		t.Fatal("process replacement accepted", result, err)
	}
	after, _, err := m.Store.ReadServerStatus(server)
	if err != nil || string(before) != string(after) {
		t.Fatal("replacement overwrote prior snapshot", err)
	}
}

func TestServerRefreshBusyMailboxKeepsVerifiedScopeWithUnknownCounts(t *testing.T) {
	f := newTmuxFixture(t)
	m := mailFixture(t)
	rt := configuredStatusFixture(t, f, m)
	held, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		finished <- m.Store.WithVerified(context.Background(), rt, func(*os.Root, Snapshot) error { close(held); <-release; return nil })
	}()
	defer func() {
		close(release)
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-held:
	case <-time.After(3 * time.Second):
		t.Fatal("fixture never acquired workspace mutation lock")
	}
	result, err := m.RefreshServerStatus(context.Background(), ServerContext{Socket: f.socket}, f.home, RefreshOptions{})
	if err == nil || result.Status != "partial" || result.Coverage.UnknownNodes != 1 {
		t.Fatal(result, err)
	}
	_, snapshot, err := m.Store.ReadServerStatus(ServerContext{Socket: f.socket})
	if err != nil {
		t.Fatal(err)
	}
	pane := firstStatusPane(t, snapshot)
	if pane.Scope == nil || pane.Status == nil || pane.Status.Node != "worker" || pane.Status.UnreadCount != nil || !strings.Contains(pane.Status.Reason, "busy") {
		t.Fatal("busy mail erased verified scope or invented counts", pane)
	}
}
