package cutover

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	legacy := filepath.Join(base, "legacy")
	target := filepath.Join(base, "target")
	for _, path := range []string{legacy, target} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return legacy, target
}

func writeFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryIsDeterministicAndReadOnly(t *testing.T) {
	legacy, target := fixture(t)
	writeFixture(t, legacy, "b/message", "second")
	writeFixture(t, legacy, "a/message", "first")
	mapping := []Mapping{{Legacy: "b/message", Target: "b/new"}, {Legacy: "a/message", Target: "a/new"}}
	first, err := BuildInventory(legacy, target, mapping)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildInventory(legacy, target, mapping)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("inventory changed on rerun: %#v, %#v", first, second)
	}
	if len(first.Entries) != 2 || first.Entries[0].Legacy != "a/message" || first.Entries[1].Legacy != "b/message" {
		t.Fatalf("entries not sorted: %#v", first.Entries)
	}
	hash := sha256.Sum256([]byte("first"))
	if first.Entries[0].SHA256 != hex.EncodeToString(hash[:]) || first.Entries[0].Size != 5 {
		t.Fatalf("wrong source observation: %#v", first.Entries[0])
	}
	if first.CutoverAllowed || first.RequiredEvidence == "" {
		t.Fatalf("inventory implied authorization: %#v", first)
	}
	for _, rel := range []string{"a/new", "b/new"} {
		if _, err := os.Lstat(filepath.Join(target, rel)); !os.IsNotExist(err) {
			t.Fatalf("inventory wrote target %s: %v", rel, err)
		}
	}
	bytes, err := os.ReadFile(filepath.Join(legacy, "a/message"))
	if err != nil || string(bytes) != "first" {
		t.Fatalf("source changed: %q, %v", bytes, err)
	}
}

func TestInventoryRejectsUnsafeMappingsAndTargets(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, legacy, target string)
		maps  []Mapping
		want  string
	}{
		{name: "unmapped", setup: func(t *testing.T, l, _ string) { writeFixture(t, l, "one", "x") }, want: "unmapped"},
		{name: "missing source", maps: []Mapping{{Legacy: "one", Target: "two"}}, want: "no source"},
		{name: "traversal", maps: []Mapping{{Legacy: "../one", Target: "two"}}, want: "invalid component"},
		{name: "duplicate destination", maps: []Mapping{{Legacy: "one", Target: "same"}, {Legacy: "two", Target: "same"}}, want: "duplicate target"},
		{name: "occupied target", setup: func(t *testing.T, l, n string) { writeFixture(t, l, "one", "x"); writeFixture(t, n, "two", "old") }, maps: []Mapping{{Legacy: "one", Target: "two"}}, want: "destination exists"},
		{name: "target symlink parent", setup: func(t *testing.T, l, n string) {
			writeFixture(t, l, "one", "x")
			if err := os.Symlink(l, filepath.Join(n, "link")); err != nil {
				t.Fatal(err)
			}
		}, maps: []Mapping{{Legacy: "one", Target: "link/two"}}, want: "not a real directory"},
		{name: "source symlink", setup: func(t *testing.T, l, _ string) {
			if err := os.Symlink("missing", filepath.Join(l, "one")); err != nil {
				t.Fatal(err)
			}
		}, maps: []Mapping{{Legacy: "one", Target: "two"}}, want: "link in legacy root"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			legacy, target := fixture(t)
			if tc.setup != nil {
				tc.setup(t, legacy, target)
			}
			_, err := BuildInventory(legacy, target, tc.maps)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got error %v, want %q", err, tc.want)
			}
		})
	}
}

func TestInventoryRejectsOverlappingRoots(t *testing.T) {
	legacy, target := fixture(t)
	if _, err := BuildInventory(legacy, legacy, nil); err == nil {
		t.Fatal("same root accepted")
	}
	if _, err := BuildInventory(filepath.Dir(legacy), target, nil); err == nil {
		t.Fatal("nested roots accepted")
	}
	alias := filepath.Join(filepath.Dir(legacy), "alias")
	if err := os.Symlink(legacy, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildInventory(alias, target, nil); err == nil {
		t.Fatal("symlink root accepted")
	}
}
