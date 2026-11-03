package helpdocs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

func fixtureAssets(value string) (fstest.MapFS, []byte, string) {
	source := fstest.MapFS{"tools/helper": &fstest.MapFile{Data: []byte(value)}}
	id := sha256.Sum256([]byte(value))
	return source, []byte(`{"tools/helper":"0555"}`), hex.EncodeToString(id[:])
}

func TestResourcesPublishCompleteAndSeparateConcurrentGenerations(t *testing.T) {
	cache := t.TempDir()
	var group sync.WaitGroup
	for i := range 24 {
		group.Go(func() {
			value := "generation-a"
			if i%2 != 0 {
				value = "generation-b"
			}
			source, modes, identity := fixtureAssets(value)
			root, err := materialize(context.Background(), cache, identity, source, modes)
			if err != nil {
				t.Error(err)
				return
			}
			data, err := os.ReadFile(filepath.Join(root, "tools", "helper"))
			if err != nil || string(data) != value {
				t.Errorf("mixed or incomplete generation: %q, %v", data, err)
			}
		})
	}
	group.Wait()
	entries, err := os.ReadDir(filepath.Join(cache, "tmux-a2a-postman", "resources"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("publication leaked partial sets: %v, %v", entries, err)
	}
}

func TestResourcesRejectCorruptPartialAndSymlinkSetsWithoutOverwrite(t *testing.T) {
	for _, fault := range []string{"content", "partial", "extra", "symlink"} {
		t.Run(fault, func(t *testing.T) {
			cache := t.TempDir()
			source, modes, identity := fixtureAssets("correct")
			root, err := materialize(context.Background(), cache, identity, source, modes)
			if err != nil {
				t.Fatal(err)
			}
			helper := filepath.Join(root, "tools", "helper")
			switch fault {
			case "content":
				if err := os.Chmod(helper, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(helper, []byte("user evidence"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "partial":
				if err := os.Remove(helper); err != nil {
					t.Fatal(err)
				}
			case "extra":
				if err := os.Mkdir(filepath.Join(root, "unexpected"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(helper); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(t.TempDir(), "unrelated"), helper); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := materialize(context.Background(), cache, identity, source, modes); err == nil {
				t.Fatal("corrupt set was accepted or silently replaced")
			}
			if fault == "content" {
				data, _ := os.ReadFile(helper)
				if string(data) != "user evidence" {
					t.Fatal("modified resource was overwritten")
				}
			}
		})
	}
}

func TestResourcesValidateBeforeMutationAndRefuseManagedRootSymlink(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "not-created")
	source, modes, identity := fixtureAssets("helper")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := materialize(ctx, cache, identity, source, modes); err == nil {
		t.Fatal("cancelled extraction succeeded")
	}
	if _, err := os.Lstat(cache); !os.IsNotExist(err) {
		t.Fatal("cancelled extraction created cache")
	}
	for _, invalid := range []string{"../escape", "/absolute", `tools\escape`} {
		bad := fstest.MapFS{invalid: &fstest.MapFile{Data: []byte("bad")}}
		if _, err := materialize(context.Background(), cache, identity, bad, modes); err == nil {
			t.Fatalf("accepted invalid resource %q", invalid)
		}
	}
	outside := t.TempDir()
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cache, "tmux-a2a-postman")); err != nil {
		t.Fatal(err)
	}
	if _, err := materialize(context.Background(), cache, identity, source, modes); err == nil {
		t.Fatal("followed managed namespace symlink")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("wrote through unrelated symlink")
	}
}

func TestHelpReadsNeverMaterialize(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "absent-cache"))
	for _, name := range Topics() {
		data, err := Read(name)
		if err != nil || (!strings.HasPrefix(string(data), "# ") && !strings.HasPrefix(string(data), "---\n")) {
			t.Fatalf("topic %q: %v", name, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(home, "absent-cache")); !os.IsNotExist(err) {
		t.Fatal("help created resource cache")
	}
}

func TestResourceManifestSelectsHelpersAndRejectsUnsafeEntries(t *testing.T) {
	source := fstest.MapFS{
		"tools/helper":     &fstest.MapFile{Data: []byte("helper")},
		"help/overview.md": &fstest.MapFile{Data: []byte("CLI help is not a filesystem helper")},
		"tools/directory":  &fstest.MapFile{Mode: fs.ModeDir},
	}
	files, err := assetList(source, []byte(`{"tools/helper":"0444"}`))
	if err != nil || len(files) != 1 || files[0].name != "tools/helper" {
		t.Fatal(files, err)
	}
	for _, manifest := range []string{
		`{}`, `{"../escape":"0444"}`, `{"tools/helper":"0777"}`,
		`{"tools/missing":"0444"}`, `{"tools/directory":"0444"}`,
	} {
		if _, err := assetList(source, []byte(manifest)); err == nil {
			t.Fatal("accepted invalid resource manifest", manifest)
		}
	}
}
