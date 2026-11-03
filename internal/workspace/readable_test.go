package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func TestReadableNamesDistinguishHyphensAndSeparators(t *testing.T) {
	seen := map[string]string{}
	for _, root := range []string{"~", "~/work/team-a", "~/work/team/a", "~/work/team%2Da", "~/Work/team-a", "~/work/チーム-a", "~/locks", "~/registry"} {
		legacy, err := Name(root)
		if err != nil {
			t.Fatal(err)
		}
		pretty, err := ReadableName(root)
		if err != nil {
			t.Fatal(err)
		}
		if previous := seen[pretty]; previous != "" {
			t.Fatalf("collision between %s and %s", previous, root)
		}
		seen[pretty] = root
		if strings.Contains(legacy, "%2D") {
			if strings.Contains(pretty, "%2D") || !strings.Contains(pretty, "--") || len(pretty) > 234 {
				t.Fatalf("unreadable alias %q", pretty)
			}
		} else if pretty != legacy {
			t.Fatalf("unnecessary alias for %q", root)
		}
	}
	pretty, err := ReadableName("~/" + strings.Repeat("long-", 80))
	if err != nil || len(pretty) > 234 {
		t.Fatalf("unbounded display name %q: %v", pretty, err)
	}
}

func TestReadableAliasPreservesHistoryOldPathsAndLocks(t *testing.T) {
	ctx := context.Background()
	store := Store{StateRoot: filepath.Join(t.TempDir(), "state")}
	if err := os.Mkdir(store.StateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(store.StateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	rt := storageRuntime()
	rt.snapshot.Root = "~/work/team-a"
	legacy, _ := Name(rt.snapshot.Root)
	pretty, _ := ReadableName(rt.snapshot.Root)
	if err := store.WithVerified(ctx, rt, func(ws *os.Root, _ Snapshot) error {
		if ws.Name() != filepath.Join(store.StateRoot, legacy) {
			t.Fatal("internal access followed an alias")
		}
		return ws.WriteFile("history.md", []byte("retained history"), 0o600)
	}); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(store.StateRoot, legacy, "history.md")
	oldInfo, _ := os.Stat(oldPath)
	lockPath := filepath.Join(store.StateRoot, "locks", legacy+".lock")
	lockInfo, _ := os.Stat(lockPath)
	identityPath := filepath.Join(store.StateRoot, legacy, "workspace.json")
	identityBefore, _ := os.ReadFile(identityPath)
	aliasPath := filepath.Join(store.StateRoot, pretty)
	target, err := os.Readlink(aliasPath)
	if err != nil || target != legacy {
		t.Fatalf("alias target %q: %v", target, err)
	}
	aliasInfo, err := os.Stat(filepath.Join(aliasPath, "history.md"))
	if err != nil || !os.SameFile(oldInfo, aliasInfo) {
		t.Fatal("alias does not expose the original archive", err)
	}
	// The state produced by a preceding binary (or interruption before alias
	// publication) is still the original real directory, with no alias.
	if err := os.Remove(aliasPath); err != nil {
		t.Fatal(err)
	}
	if err := store.withView(ctx, rt, func(ws *os.Root, _ Snapshot) error {
		_, err := ws.ReadFile("history.md")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(aliasPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read-only view created an alias", err)
	}
	// An old binary's lock continues to exclude the new admission path.
	lock, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if err := store.WithVerified(ctx, rt, noMutation); !errors.Is(err, ErrBusy) {
		t.Fatalf("did not honor the old lock: %v", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err := store.WithVerified(ctx, rt, noMutation); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(oldPath)
	lockAfter, _ := os.Stat(lockPath)
	identityAfter, _ := os.ReadFile(identityPath)
	rootInfo, _ := os.Stat(store.StateRoot)
	if !os.SameFile(oldInfo, after) || !oldInfo.ModTime().Equal(after.ModTime()) || !os.SameFile(lockInfo, lockAfter) || string(identityBefore) != string(identityAfter) || rootInfo.Mode().Perm() != 0o755 {
		t.Fatal("alias publication changed history, identity, lock or root mode")
	}
	if err := os.Remove(aliasPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../unrelated", aliasPath); err != nil {
		t.Fatal(err)
	}
	if err := store.WithVerified(ctx, rt, func(*os.Root, Snapshot) error {
		t.Fatal("conflicting alias reached a mailbox mutation")
		return nil
	}); err == nil {
		t.Fatal("accepted conflicting alias over existing history")
	}
	history, err := os.ReadFile(oldPath)
	if err != nil || string(history) != "retained history" {
		t.Fatal("alias conflict changed old history", err)
	}
}

func TestReadableAliasInterruptedAndConcurrentPublication(t *testing.T) {
	store := Store{StateRoot: t.TempDir()}
	failed := errors.New("interrupted publication")
	store.beforePublish = func() error { return failed }
	runtime := func() *storedRuntime {
		rt := storageRuntime()
		rt.snapshot.Root = "~/work/team-a"
		return rt
	}
	if err := store.WithVerified(context.Background(), runtime(), noMutation); !errors.Is(err, failed) {
		t.Fatal(err)
	}
	pretty, _ := ReadableName(runtime().snapshot.Root)
	if _, err := os.Lstat(filepath.Join(store.StateRoot, pretty)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("published alias before real workspace", err)
	}
	store.beforePublish = nil
	var ready sync.WaitGroup
	ready.Add(8)
	start := make(chan struct{})
	results := make(chan error, 8)
	for range 8 {
		go func() {
			ready.Done()
			<-start
			results <- store.WithVerified(context.Background(), runtime(), noMutation)
		}()
	}
	ready.Wait()
	close(start)
	succeeded := 0
	for range 8 {
		if err := <-results; err == nil {
			succeeded++
		} else if !errors.Is(err, ErrBusy) {
			t.Fatal(err)
		}
	}
	if succeeded == 0 {
		t.Fatal("no publisher admitted")
	}
	entries, err := os.ReadDir(store.StateRoot)
	if err != nil || len(entries) != 3 {
		t.Fatalf("duplicate or interrupted workspace remains: %v %v", entries, err)
	}
	canonical, _ := Name(runtime().snapshot.Root)
	link, err := os.Readlink(filepath.Join(store.StateRoot, pretty))
	if err != nil || link != canonical {
		t.Fatalf("concurrent publication: %q %v", link, err)
	}
}

func TestUnsafeReadableAliasesNeverInitializeOrMutateHistory(t *testing.T) {
	for _, kind := range []string{"file", "directory", "wrong", "absolute", "escape", "dangling"} {
		t.Run(kind, func(t *testing.T) {
			store := Store{StateRoot: t.TempDir()}
			rt := storageRuntime()
			rt.snapshot.Root = "~/work/team-a"
			legacy, _ := Name(rt.snapshot.Root)
			pretty, _ := ReadableName(rt.snapshot.Root)
			alias := filepath.Join(store.StateRoot, pretty)
			var err error
			switch kind {
			case "file":
				err = os.WriteFile(alias, []byte("unmanaged"), 0o600)
			case "directory":
				err = os.Mkdir(alias, 0o700)
			case "wrong":
				err = os.Symlink("another-workspace", alias)
			case "absolute":
				err = os.Symlink(filepath.Join(store.StateRoot, legacy), alias)
			case "escape":
				err = os.Symlink("../"+legacy, alias)
			case "dangling":
				err = os.Symlink(legacy, alias)
			}
			if err != nil {
				t.Fatal(err)
			}
			called := false
			err = store.WithVerified(context.Background(), rt, func(*os.Root, Snapshot) error { called = true; return nil })
			if err == nil || called {
				t.Fatalf("unsafe alias admitted: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(store.StateRoot, legacy)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("missing history was reinitialized", err)
			}
		})
	}
}

func TestReadableClaimPathUsesTheOneCanonicalMailbox(t *testing.T) {
	m := mailFixture(t)
	worker, peer := mailRuntime("worker"), mailRuntime("peer")
	worker.snapshot.Root, peer.snapshot.Root = "~/work/team-a", "~/work/team-a"
	msg, err := m.Send(context.Background(), worker, "peer", "original body", "", true)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := m.Claim(context.Background(), peer, "")
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v", err)
	}
	pretty, _ := ReadableName(worker.snapshot.Root)
	legacy, _ := Name(worker.snapshot.Root)
	want := filepath.Join(m.Store.StateRoot, pretty, "agents", "peer", "read", msg.ID+".md")
	oldPath := filepath.Join(m.Store.StateRoot, legacy, "agents", "peer", "read", msg.ID+".md")
	old, err := os.Stat(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.Stat(claimed.Path)
	if err != nil || claimed.Path != want || !os.SameFile(old, current) {
		t.Fatalf("claim path %s: %v", claimed.Path, err)
	}
	again, err := m.Claim(context.Background(), peer, "")
	if err != nil || again != nil {
		t.Fatal("alias replayed claimed work", err)
	}
}
