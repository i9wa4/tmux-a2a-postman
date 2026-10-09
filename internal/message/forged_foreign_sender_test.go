package message

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
)

// M2-B1: a post file names its sender in the filename, but the daemon only
// knew the physical source session (the directory holding post/). A qualified
// sender naming a DIFFERENT session used to resolve as that session's node and
// was then judged by that session's adjacency and enabled state.

func TestSenderClaimsForeignSession(t *testing.T) {
	tests := []struct {
		name   string
		from   string
		source string
		want   bool
	}{
		{name: "bare sender is scoped to the source session", from: "worker", source: "sess-a", want: false},
		{name: "qualified sender in its own session", from: "sess-a:worker", source: "sess-a", want: false},
		{name: "qualified sender naming another session", from: "sess-b:worker", source: "sess-a", want: true},
		{name: "daemon is handled by its own guard", from: "daemon", source: "sess-a", want: false},
		{name: "unknown source session is not judged here", from: "sess-b:worker", source: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := senderClaimsForeignSession(tt.from, tt.source); got != tt.want {
				t.Fatalf("senderClaimsForeignSession(%q, %q) = %v, want %v", tt.from, tt.source, got, tt.want)
			}
		})
	}
}

func TestDeliverMessage_ForgedForeignQualifiedSenderDeniedDespiteEnabledSessionAndExplicitEdge(t *testing.T) {
	baseDir := t.TempDir()
	sessionA := filepath.Join(baseDir, "sess-a")
	sessionB := filepath.Join(baseDir, "sess-b")
	for _, dir := range []string{sessionA, sessionB} {
		if err := config.CreateSessionDirs(dir); err != nil {
			t.Fatalf("CreateSessionDirs(%s) error = %v", dir, err)
		}
	}

	// The forgery: a file inside session A's post/ that claims to come from
	// session B's node. Session B is enabled and has an explicit edge to the
	// recipient, so adjacency and enabled-state alone would allow it.
	filename := "20260709-120000-r0001-from-sess-b:node-to-sess-a:worker.md"
	postPath := filepath.Join(sessionA, "post", filename)
	content := "---\nparams:\n  contextId: test-ctx\n  from: sess-b:node\n  to: sess-a:worker\n  timestamp: 2026-07-09T12:00:00Z\n---\n\nforged\n"
	if err := os.WriteFile(postPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	nodes := map[string]discovery.NodeInfo{
		"sess-a:worker": {PaneID: "%1", SessionName: "sess-a", SessionDir: sessionA},
		"sess-b:node":   {PaneID: "%2", SessionName: "sess-b", SessionDir: sessionB},
	}
	adjacency := map[string][]string{
		"sess-b:node":   {"sess-a:worker"},
		"sess-a:worker": {"sess-b:node"},
	}
	beforeB := snapshotTree(t, sessionB)

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, &config.Config{}, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(sessionA, "inbox", "worker", filename)); !os.IsNotExist(err) {
		t.Fatalf("forged foreign sender reached the recipient inbox: %v", err)
	}
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Fatalf("forged file still in post/: %v", err)
	}
	forged, err := filepath.Glob(filepath.Join(sessionA, "dead-letter", "*-dl-forged-sender.md"))
	if err != nil || len(forged) != 1 {
		t.Fatalf("forged-sender dead letters = %v, err = %v, want exactly 1", forged, err)
	}
	for _, other := range []string{"*-dl-routing-denied.md", "*-dl-session-disabled.md", "*-dl-unknown-sender.md"} {
		if matches, _ := filepath.Glob(filepath.Join(sessionA, "dead-letter", other)); len(matches) != 0 {
			t.Fatalf("denial reached a later gate (%s): %v", other, matches)
		}
	}
	// The denial is decided before routing, so no routing-denied warning is
	// written back into the forged sender's own inbox, and the claimed
	// session is never touched.
	if after := snapshotTree(t, sessionB); !reflect.DeepEqual(beforeB, after) {
		t.Fatalf("claimed session B was modified by the forged message:\nbefore = %v\nafter  = %v", beforeB, after)
	}
}

func TestDeliverMessage_ValidQualifiedSenderInOwnSessionStillDelivered(t *testing.T) {
	sessionA := filepath.Join(t.TempDir(), "sess-a")
	if err := config.CreateSessionDirs(sessionA); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	filename := "20260709-120100-r0002-from-sess-a:node-to-sess-a:worker.md"
	postPath := filepath.Join(sessionA, "post", filename)
	content := "---\nparams:\n  contextId: test-ctx\n  from: sess-a:node\n  to: sess-a:worker\n  timestamp: 2026-07-09T12:01:00Z\n---\n\nvalid\n"
	if err := os.WriteFile(postPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	nodes := map[string]discovery.NodeInfo{
		"sess-a:worker": {PaneID: "%1", SessionName: "sess-a", SessionDir: sessionA},
		"sess-a:node":   {PaneID: "%2", SessionName: "sess-a", SessionDir: sessionA},
	}
	adjacency := map[string][]string{
		"sess-a:node":   {"sess-a:worker"},
		"sess-a:worker": {"sess-a:node"},
	}

	if err := DeliverMessage(postPath, "test-ctx", nodes, adjacency, &config.Config{EnterDelay: 0.1, TmuxTimeout: 1.0}, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(sessionA, "inbox", "worker", filename)); err != nil {
		t.Fatalf("valid same-session qualified sender was not delivered: %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(sessionA, "dead-letter", "*")); len(matches) != 0 {
		t.Fatalf("valid message left dead letters: %v", matches)
	}
}

// snapshotTree lists every path under root (relative, sorted), so a test can
// assert that a session directory was not touched at all.
func snapshotTree(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%s) error = %v", root, err)
	}
	sort.Strings(paths)
	return paths
}
