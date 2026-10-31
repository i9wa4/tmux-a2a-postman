package workspace

import (
	"bufio"
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

	"github.com/i9wa4/tmux-a2a-postman/internal/lock"
)

type storedRuntime struct {
	snapshot Snapshot
	state    Liveness
	probeErr error
	second   *Snapshot
	calls    int
}

func (r *storedRuntime) Snapshot(context.Context) (Snapshot, error) {
	r.calls++
	if r.calls > 1 && r.second != nil {
		return *r.second, nil
	}
	return r.snapshot, nil
}

func (r *storedRuntime) Probe(context.Context, Owner) (Liveness, error) { return r.state, r.probeErr }

func storageRuntime() *storedRuntime {
	owner := Owner{Server: "/tmp/fixture-server", Incarnation: "100:1:2:3:4", Session: "$1", Pane: "%1"}
	binding := Binding{Role: "worker", Owner: owner}
	return &storedRuntime{snapshot: Snapshot{Root: "~/repo", Caller: binding, Bindings: []Binding{binding}}, state: Alive}
}

func noMutation(*os.Root, Snapshot) error { return nil }

func TestStorePersistsIdentityAndPreservesHistoryOnTakeover(t *testing.T) {
	store := Store{StateRoot: filepath.Join(t.TempDir(), "state")}
	runtime := storageRuntime()
	write := func(root *os.Root, _ Snapshot) error {
		return root.WriteFile("history.md", []byte("claimed mail survives"), 0o600)
	}
	if err := store.WithVerified(context.Background(), runtime, write); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.StateRoot, "repo")
	before, err := os.ReadFile(filepath.Join(path, "workspace.json"))
	if err != nil {
		t.Fatal(err)
	}
	runtime.snapshot.Caller.Owner.Session = "$2"
	runtime.snapshot.Caller.Owner.Pane = "%2"
	runtime.snapshot.Bindings = []Binding{runtime.snapshot.Caller}
	runtime.state = Dead
	if err := store.WithVerified(context.Background(), runtime, noMutation); err != nil {
		t.Fatal(err)
	}
	history, err := os.ReadFile(filepath.Join(path, "history.md"))
	if err != nil || string(history) != "claimed mail survives" {
		t.Fatalf("lost history: %q %v", history, err)
	}
	after, err := os.ReadFile(filepath.Join(path, "workspace.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("stable identity changed", err)
	}
	if err := (Store{StateRoot: store.StateRoot}).WithVerified(context.Background(), runtime, noMutation); err != nil {
		t.Fatal(err)
	}
}

func TestStoreRejectsLiveAndUnknownOwnerBeforeMutation(t *testing.T) {
	for _, state := range []Liveness{Alive, Unknown} {
		t.Run(map[Liveness]string{Alive: "live", Unknown: "unknown"}[state], func(t *testing.T) {
			store := Store{StateRoot: filepath.Join(t.TempDir(), "state")}
			old := storageRuntime()
			if err := store.WithVerified(context.Background(), old, noMutation); err != nil {
				t.Fatal(err)
			}
			bindingPath := filepath.Join(store.StateRoot, "repo", "runtime", "binding.json")
			before, err := os.ReadFile(bindingPath)
			if err != nil {
				t.Fatal(err)
			}
			next := storageRuntime()
			next.snapshot.Caller.Owner.Session = "$2"
			next.snapshot.Bindings = []Binding{next.snapshot.Caller}
			next.state = state
			called := false
			err = store.WithVerified(context.Background(), next, func(*os.Root, Snapshot) error { called = true; return nil })
			want := ErrUnknown
			if state == Alive {
				want = ErrOwned
			}
			if !errors.Is(err, want) || called {
				t.Fatalf("conflict mutated: %v %v", err, called)
			}
			after, err := os.ReadFile(bindingPath)
			if err != nil || string(before) != string(after) {
				t.Fatal("binding overwritten", err)
			}
		})
	}
}

func TestStoreRejectsMutationWhileLegacyDaemonLockHeld(t *testing.T) {
	store := Store{StateRoot: filepath.Join(t.TempDir(), "state")}
	legacyDir := filepath.Join(store.StateRoot, "lock")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy, err := lock.NewSessionLock(filepath.Join(legacyDir, fmt.Sprintf("user-%d.lock", os.Geteuid())))
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = store.WithVerified(context.Background(), storageRuntime(), func(*os.Root, Snapshot) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrBusy) || called {
		t.Fatalf("legacy daemon lock admission: err=%v called=%v", err, called)
	}
	if err := legacy.Release(); err != nil {
		t.Fatal(err)
	}
	if err := store.WithVerified(context.Background(), storageRuntime(), noMutation); err != nil {
		t.Fatal("legacy daemon lock was not released", err)
	}
	legacyPath := filepath.Join(legacyDir, fmt.Sprintf("user-%d.lock", os.Geteuid()))
	if err := store.WithVerified(context.Background(), storageRuntime(), func(*os.Root, Snapshot) error {
		if contender, err := lock.NewSessionLock(legacyPath); err == nil {
			_ = contender.Release()
			t.Fatal("legacy daemon acquired exclusive lock during file-first mutation")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	legacy, err = lock.NewSessionLock(legacyPath)
	if err != nil {
		t.Fatal("file-first mutation did not release legacy lock", err)
	}
	if err := legacy.Release(); err != nil {
		t.Fatal(err)
	}
}

// A separate process models the legacy daemon's lifetime-exclusive user lock.
func TestLegacyLockChild(t *testing.T) {
	mode := os.Getenv("POSTMAN_LEGACY_LOCK_CHILD")
	if mode == "" {
		return
	}
	path := os.Getenv("POSTMAN_LEGACY_LOCK_PATH")
	if mode == "hold-workspace" {
		store := Store{StateRoot: os.Getenv("POSTMAN_STATE_ROOT")}
		err := store.WithVerified(context.Background(), storageRuntime(), func(*os.Root, Snapshot) error {
			fmt.Println("READY")
			_, _ = os.Stdin.Read(make([]byte, 1))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	legacy, err := lock.NewSessionLock(path)
	if mode == "try" && errors.Is(err, syscall.EWOULDBLOCK) {
		fmt.Println("BLOCKED")
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := legacy.Release(); err != nil {
			t.Error(err)
		}
	}()
	if mode == "try" {
		fmt.Println("ACQUIRED")
		return
	}
	if mode != "hold" {
		t.Fatalf("invalid child mode %q", mode)
	}
	fmt.Println("READY")
	_, _ = os.Stdin.Read(make([]byte, 1))
}

func legacyLockPath(store Store) string {
	return filepath.Join(store.StateRoot, "lock", fmt.Sprintf("user-%d.lock", os.Geteuid()))
}

func legacyTryExclusive(t *testing.T, path string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLegacyLockChild$")
	cmd.Env = append(os.Environ(), "POSTMAN_LEGACY_LOCK_CHILD=try", "POSTMAN_LEGACY_LOCK_PATH="+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("legacy contender failed: %v: %s", err, out)
	}
	if strings.Contains(string(out), "BLOCKED") {
		return false
	}
	if strings.Contains(string(out), "ACQUIRED") {
		return true
	}
	t.Fatalf("legacy contender gave no result: %s", out)
	return false
}

func holdLegacyExclusive(t *testing.T, path string) func() {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLegacyLockChild$")
	cmd.Env = append(os.Environ(), "POSTMAN_LEGACY_LOCK_CHILD=hold", "POSTMAN_LEGACY_LOCK_PATH="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "READY" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("legacy child never locked: %q %v", scanner.Text(), scanner.Err())
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		_ = stdin.Close()
		if err := cmd.Wait(); err != nil {
			t.Errorf("legacy child exit: %v", err)
		}
	}
	t.Cleanup(release)
	return release
}

func TestNewWorkspaceLocksAcrossProcesses(t *testing.T) {
	store := Store{StateRoot: filepath.Join(t.TempDir(), "state")}
	cmd := exec.Command(os.Args[0], "-test.run=^TestLegacyLockChild$")
	cmd.Env = append(os.Environ(), "POSTMAN_LEGACY_LOCK_CHILD=hold-workspace", "POSTMAN_STATE_ROOT="+store.StateRoot)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		_ = stdin.Close()
		if err := cmd.Wait(); err != nil {
			t.Errorf("workspace child exit: %v", err)
		}
	}
	t.Cleanup(release)
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "READY" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		released = true
		t.Fatalf("workspace child never entered: %q %v", scanner.Text(), scanner.Err())
	}
	called := false
	err = store.WithVerified(context.Background(), storageRuntime(), func(*os.Root, Snapshot) error { called = true; return nil })
	if !errors.Is(err, ErrBusy) || called {
		t.Fatalf("same workspace was not serialized: %v called=%v", err, called)
	}
	other := storageRuntime()
	other.snapshot.Root = "~/other"
	if err := store.WithVerified(context.Background(), other, noMutation); err != nil {
		t.Fatalf("different workspace was blocked: %v", err)
	}
	if legacyTryExclusive(t, legacyLockPath(store)) {
		t.Fatal("legacy exclusive entered while new workspace mutation active")
	}
	release()
	if err := store.WithVerified(context.Background(), storageRuntime(), noMutation); err != nil {
		t.Fatalf("workspace lock not released: %v", err)
	}
}

func TestLegacyLeaseCrossProcessAndInodeReuse(t *testing.T) {
	store := Store{StateRoot: filepath.Join(t.TempDir(), "state")}
	path := legacyLockPath(store)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	release := holdLegacyExclusive(t, path)
	called := false
	err := store.WithVerified(context.Background(), storageRuntime(), func(*os.Root, Snapshot) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrBusy) || called {
		t.Fatalf("legacy exclusive admitted mutation: %v called=%v", err, called)
	}
	if lease, err := store.acquireLegacyLease(); !errors.Is(err, ErrBusy) {
		if lease != nil {
			_ = lease.Close()
		}
		t.Fatalf("legacy exclusive admitted whole-operation lease: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	release()
	lease, err := store.acquireLegacyLease()
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("legacy lock inode changed across restart: %v", err)
	}
	if legacyTryExclusive(t, path) {
		t.Fatal("legacy exclusive entered during new whole-operation lease")
	}
	if err := store.WithVerified(context.Background(), storageRuntime(), noMutation); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if !legacyTryExclusive(t, path) {
		t.Fatal("legacy exclusive did not enter after new lease release")
	}
}

func TestStoreNonblockingLockSerializesIndependentCallers(t *testing.T) {
	store := Store{StateRoot: filepath.Join(t.TempDir(), "state")}
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- store.WithVerified(context.Background(), storageRuntime(), func(*os.Root, Snapshot) error {
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case err := <-result:
		t.Fatal("first caller never entered mutation", err)
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("first caller did not enter within bound")
	}
	err := store.WithVerified(context.Background(), storageRuntime(), noMutation)
	close(release)
	first := <-result
	if first != nil || !errors.Is(err, ErrBusy) {
		t.Fatalf("lock admission: first=%v second=%v", first, err)
	}
	if err := store.WithVerified(context.Background(), storageRuntime(), noMutation); err != nil {
		t.Fatal("lock was not released", err)
	}
}

func TestStoreRejectsChangedRuntimeAndPartialPublication(t *testing.T) {
	store := Store{StateRoot: filepath.Join(t.TempDir(), "state")}
	runtime := storageRuntime()
	changed := runtime.snapshot
	changed.Root = "~/elsewhere"
	runtime.second = &changed
	if err := store.WithVerified(context.Background(), runtime, noMutation); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.StateRoot, "repo")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("published unstable identity", err)
	}
	failure := errors.New("injected before publication")
	store.beforePublish = func() error { return failure }
	if err := store.WithVerified(context.Background(), storageRuntime(), noMutation); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(store.StateRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "locks" && entry.Name() != "lock" {
			t.Fatal("partial workspace visible or staging leak:", entry.Name())
		}
	}
	store.beforePublish = nil
	if err := store.WithVerified(context.Background(), storageRuntime(), noMutation); err != nil {
		t.Fatal(err)
	}
}

func TestStoreRejectsUnsafeIdentityRecordsAndAliases(t *testing.T) {
	for _, kind := range []string{"mismatch", "symlink", "hardlink", "oversized", "unknown-field", "unsafe-mode"} {
		t.Run(kind, func(t *testing.T) {
			store := Store{StateRoot: filepath.Join(t.TempDir(), "state")}
			if err := store.WithVerified(context.Background(), storageRuntime(), noMutation); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.StateRoot, "repo", "workspace.json")
			switch kind {
			case "mismatch":
				if err := os.WriteFile(path, []byte("{\"version\":1,\"root\":\"~/other\",\"name\":\"repo\"}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(t.TempDir(), "record")
				if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, filepath.Join(t.TempDir(), "alias")); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.WriteFile(path, []byte(strings.Repeat(" ", 65537)), 0o600); err != nil {
					t.Fatal(err)
				}
			case "unknown-field":
				if err := os.WriteFile(path, []byte("{\"version\":1,\"root\":\"~/repo\",\"name\":\"repo\",\"extra\":true}"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "unsafe-mode":
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			err := store.WithVerified(context.Background(), storageRuntime(), func(*os.Root, Snapshot) error { called = true; return nil })
			if err == nil || called {
				t.Fatalf("unsafe identity accepted: %v %v", err, called)
			}
		})
	}
}

func TestStoreValidatesCallerAndDefaultStateRoot(t *testing.T) {
	runtime := storageRuntime()
	runtime.snapshot.Caller.Role = "unbound"
	store := Store{StateRoot: filepath.Join(t.TempDir(), "state")}
	if err := store.WithVerified(context.Background(), runtime, noMutation); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.StateRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid caller created state")
	}
	if got, err := DefaultStateRoot("/home/user", ""); err != nil || got != "/home/user/.local/state/tmux-a2a-postman" {
		t.Fatal(got, err)
	}
	for _, pair := range [][2]string{{"relative", ""}, {"/home/user", "relative"}} {
		if _, err := DefaultStateRoot(pair[0], pair[1]); err == nil {
			t.Fatal("relative state accepted")
		}
	}
}

func TestStoreNativeOwnershipRejectsOtherLiveSession(t *testing.T) {
	f := newTmuxFixture(t)
	store := Store{StateRoot: filepath.Join(t.TempDir(), "state")}
	first := f.adapter()
	if err := store.WithVerified(context.Background(), first, noMutation); err != nil {
		t.Fatal(err)
	}
	second := f.run(t, "new-session", "-d", "-s", "other", "-c", f.repo, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.run(t, "select-pane", "-t", second, "-T", "worker")
	next := f.adapter()
	next.Pane = second
	if err := store.WithVerified(context.Background(), next, noMutation); !errors.Is(err, ErrOwned) {
		t.Fatalf("live native owner: %v", err)
	}
	owner, err := first.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.run(t, "kill-session", "-t", owner.Caller.Owner.Session)
	if err := store.WithVerified(context.Background(), next, noMutation); err != nil {
		t.Fatalf("verified native takeover: %v", err)
	}
}

func TestLegacyLockFileNameUIDPolicy(t *testing.T) {
	root := t.TempDir()
	uid := os.Getuid()
	got, err := legacyLockFileName(root, uid, uid)
	if err != nil || got != fmt.Sprintf("user-%d.lock", uid) {
		t.Fatalf("legacy real-UID name: %q %v", got, err)
	}
	if _, err := legacyLockFileName(root, uid, uid+1); err == nil {
		t.Fatal("accepted split real/effective UID")
	}
}
