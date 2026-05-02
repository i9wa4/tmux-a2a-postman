package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPostFromPath_AcceptsOnlyMarkdownInsidePostDir(t *testing.T) {
	post, ok := PostFromPath(filepath.Join("state", "ctx", "review", "post", "20260502-120000-r1111-from-a-to-b.md"))
	if !ok {
		t.Fatal("PostFromPath() ok = false, want true")
	}
	if post.SessionDir != filepath.Join("state", "ctx", "review") {
		t.Fatalf("SessionDir = %q, want review session dir", post.SessionDir)
	}
	if post.PostDir != filepath.Join("state", "ctx", "review", "post") {
		t.Fatalf("PostDir = %q, want post dir", post.PostDir)
	}
	if post.Filename != "20260502-120000-r1111-from-a-to-b.md" {
		t.Fatalf("Filename = %q", post.Filename)
	}

	for _, path := range []string{
		filepath.Join("state", "ctx", "review", "inbox", "20260502-120000-r1111-from-a-to-b.md"),
		filepath.Join("state", "ctx", "review", "post", "ignore.txt"),
	} {
		if _, ok := PostFromPath(path); ok {
			t.Fatalf("PostFromPath(%q) ok = true, want false", path)
		}
	}
}

func TestListPendingPosts_SortsAndSkipsMissingPostDirs(t *testing.T) {
	tmpDir := t.TempDir()
	alpha := filepath.Join(tmpDir, "ctx", "alpha")
	bravo := filepath.Join(tmpDir, "ctx", "bravo")
	for _, dir := range []string{alpha, bravo} {
		if err := os.MkdirAll(filepath.Join(dir, "post"), 0o700); err != nil {
			t.Fatalf("MkdirAll(post): %v", err)
		}
	}

	files := map[string]string{
		filepath.Join(bravo, "post", "20260502-120200-r1111-from-b-to-a.md"): "",
		filepath.Join(alpha, "post", "20260502-120100-r1111-from-a-to-b.md"): "",
		filepath.Join(alpha, "post", "20260502-120000-r1111-from-a-to-c.md"): "",
		filepath.Join(alpha, "post", "ignore.txt"):                           "",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", path, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(bravo, "post", "nested"), 0o700); err != nil {
		t.Fatalf("MkdirAll(nested): %v", err)
	}

	posts, err := ListPendingPosts([]string{bravo, filepath.Join(tmpDir, "ctx", "missing"), alpha})
	if err != nil {
		t.Fatalf("ListPendingPosts() error = %v", err)
	}

	got := make([]string, 0, len(posts))
	for _, post := range posts {
		got = append(got, post.Path)
	}
	want := []string{
		filepath.Join(alpha, "post", "20260502-120000-r1111-from-a-to-c.md"),
		filepath.Join(alpha, "post", "20260502-120100-r1111-from-a-to-b.md"),
		filepath.Join(bravo, "post", "20260502-120200-r1111-from-b-to-a.md"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %#v, want %#v", got, want)
	}
}
