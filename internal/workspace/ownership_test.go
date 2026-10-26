package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		if entry.Name() != "locks" {
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
