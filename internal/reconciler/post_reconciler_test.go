package reconciler

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/store"
)

func TestPostReconciler_ReconcilePostsCoalescesSameRoute(t *testing.T) {
	posts := []store.PendingPost{
		{Path: "/state/ctx/review/post/20260502-120000-r1111-from-a-to-b.md", Filename: "20260502-120000-r1111-from-a-to-b.md"},
		{Path: "/state/ctx/review/post/20260502-120001-r2222-from-a-to-b.md", Filename: "20260502-120001-r2222-from-a-to-b.md"},
		{Path: "/state/ctx/review/post/20260502-120002-r3333-from-a-to-c.md", Filename: "20260502-120002-r3333-from-a-to-c.md"},
		{Path: "/state/ctx/review/post/bad.md", Filename: "bad.md"},
	}

	var got []string
	count := PostReconciler{
		CoalesceRateLimitedRoutes: true,
		RouteKey: func(post store.PendingPost) (string, bool) {
			switch post.Filename {
			case "20260502-120000-r1111-from-a-to-b.md", "20260502-120001-r2222-from-a-to-b.md":
				return "a:b", true
			case "20260502-120002-r3333-from-a-to-c.md":
				return "a:c", true
			default:
				return "", false
			}
		},
	}.ReconcilePosts(posts, func(post store.PendingPost) {
		got = append(got, post.Filename)
	})

	want := []string{
		"20260502-120000-r1111-from-a-to-b.md",
		"20260502-120002-r3333-from-a-to-c.md",
		"bad.md",
	}
	if count != len(want) {
		t.Fatalf("count = %d, want %d", count, len(want))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dispatched = %#v, want %#v", got, want)
	}
}

func TestPostReconciler_ReconcilePathRequiresExistingPostFile(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "ctx", "review")
	postDir := filepath.Join(sessionDir, "post")
	if err := os.MkdirAll(postDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(post): %v", err)
	}
	postPath := filepath.Join(postDir, "20260502-120000-r1111-from-a-to-b.md")
	if err := os.WriteFile(postPath, []byte("payload"), 0o600); err != nil {
		t.Fatalf("WriteFile(post): %v", err)
	}

	var got []string
	ok, err := PostReconciler{}.ReconcilePath(postPath, func(post store.PendingPost) {
		got = append(got, post.Path)
	})
	if err != nil {
		t.Fatalf("ReconcilePath() error = %v", err)
	}
	if !ok {
		t.Fatal("ReconcilePath() ok = false, want true")
	}
	if !reflect.DeepEqual(got, []string{postPath}) {
		t.Fatalf("handled = %#v, want %q", got, postPath)
	}

	ok, err = PostReconciler{}.ReconcilePath(filepath.Join(postDir, "missing.md"), func(post store.PendingPost) {
		t.Fatalf("unexpected handler call for %s", post.Path)
	})
	if err != nil {
		t.Fatalf("ReconcilePath(missing) error = %v", err)
	}
	if ok {
		t.Fatal("ReconcilePath(missing) ok = true, want false")
	}

	ok, err = PostReconciler{}.ReconcilePath(filepath.Join(sessionDir, "inbox", "20260502-120000-r1111-from-a-to-b.md"), func(post store.PendingPost) {
		t.Fatalf("unexpected handler call for %s", post.Path)
	})
	if err != nil {
		t.Fatalf("ReconcilePath(non-post) error = %v", err)
	}
	if ok {
		t.Fatal("ReconcilePath(non-post) ok = true, want false")
	}
}
