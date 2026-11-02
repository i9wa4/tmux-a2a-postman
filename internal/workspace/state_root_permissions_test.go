package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreKeepsReadableApplicationRootAndPrivateChildren(t *testing.T) {
	for _, mode := range []os.FileMode{0o700, 0o750, 0o755} {
		t.Run(mode.String(), func(t *testing.T) {
			store := Store{StateRoot: filepath.Join(t.TempDir(), "state")}
			if err := os.Mkdir(store.StateRoot, mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(store.StateRoot, mode); err != nil {
				t.Fatal(err)
			}
			rt := storageRuntime()
			if err := store.WithVerified(context.Background(), rt, noMutation); err != nil {
				t.Fatal(err)
			}
			read := false
			if err := store.withReadWorkspace(context.Background(), rt.snapshot.Root, func(*os.Root) error {
				read = true
				return nil
			}); err != nil || !read {
				t.Fatal("read-only state unavailable", err, read)
			}
			if info, err := os.Stat(store.StateRoot); err != nil || info.Mode().Perm() != mode {
				t.Fatal("application root permissions changed", info, err)
			}
			for _, child := range []string{"locks", "repo", "repo/runtime"} {
				info, err := os.Stat(filepath.Join(store.StateRoot, child))
				if err != nil || info.Mode().Perm() != 0o700 {
					t.Fatal("child directory is not private", child, info, err)
				}
			}
		})
	}
}

func TestStoreRejectsWritableOrSymlinkApplicationRoot(t *testing.T) {
	for _, kind := range []string{"group-write", "other-write", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			store := Store{StateRoot: filepath.Join(t.TempDir(), "state")}
			rt := storageRuntime()
			if err := store.WithVerified(context.Background(), rt, noMutation); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "group-write":
				if err := os.Chmod(store.StateRoot, 0o775); err != nil {
					t.Fatal(err)
				}
			case "other-write":
				if err := os.Chmod(store.StateRoot, 0o707); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := store.StateRoot + "-target"
				if err := os.Rename(store.StateRoot, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, store.StateRoot); err != nil {
					t.Fatal(err)
				}
			}
			mutated, read := false, false
			if err := store.WithVerified(context.Background(), rt, func(*os.Root, Snapshot) error {
				mutated = true
				return nil
			}); err == nil || mutated {
				t.Fatal("unsafe application root admitted a mutation", err, mutated)
			}
			if err := store.withReadWorkspace(context.Background(), rt.snapshot.Root, func(*os.Root) error {
				read = true
				return nil
			}); err == nil || read {
				t.Fatal("unsafe application root admitted a read", err, read)
			}
		})
	}
}

func TestReadableApplicationRootDoesNotRelaxChildPermissions(t *testing.T) {
	for _, child := range []string{"locks", "repo"} {
		t.Run(child, func(t *testing.T) {
			store := Store{StateRoot: filepath.Join(t.TempDir(), "state")}
			rt := storageRuntime()
			if err := store.WithVerified(context.Background(), rt, noMutation); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{store.StateRoot, filepath.Join(store.StateRoot, child)} {
				if err := os.Chmod(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.WithVerified(context.Background(), rt, noMutation); err == nil {
				t.Fatal("non-private child admitted a mutation")
			}
			if err := store.withReadWorkspace(context.Background(), rt.snapshot.Root, func(*os.Root) error { return nil }); err == nil {
				t.Fatal("non-private child admitted a read")
			}
		})
	}
}
