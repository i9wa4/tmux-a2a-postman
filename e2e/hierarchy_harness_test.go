package e2e_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
)

// hierarchyHarness extends agentSessionHarness's existing single-session
// pattern (session dirs via config.CreateSessionDirs, a
// map[string]discovery.NodeInfo keyed "session:node", an adjacency map,
// delivery purely via message.DeliverMessage) to three session
// directories instead of one (#768's first slice: happy-path three-level
// traversal). Each level shares the SAME mouthpiece role-template name
// ("mouthpiece"), differing only in sessionName/contextID/parent linkage
// (#768 AC bullet 1: "one common mouthpiece-based session template with
// no messenger/diplomat mode split").
type hierarchyHarness struct {
	baseDir   string
	contextID string
	// sessionDirs maps a level's bare session name to its on-disk
	// session directory.
	sessionDirs map[string]string
	nodes       map[string]discovery.NodeInfo
	adjacency   map[string][]string
	cfg         *config.Config
}

const (
	hierarchyContextID    = "hierarchy-test-ctx"
	hierarchyTopSession   = "level-top"
	hierarchyOwnerSession = "level-owner"
	hierarchyRepoSession  = "level-repo"
	hierarchyMouthpiece   = "mouthpiece"
)

// newHierarchyHarness builds three session directories (top, owner/group,
// repository), each with one "mouthpiece" node sharing the same role
// name, linked top->owner->repo and back. Uses t.TempDir() for automatic
// cleanup.
func newHierarchyHarness(t *testing.T) *hierarchyHarness {
	t.Helper()
	baseDir := t.TempDir()

	sessionDirs := map[string]string{}
	for _, session := range []string{hierarchyTopSession, hierarchyOwnerSession, hierarchyRepoSession} {
		dir := filepath.Join(baseDir, hierarchyContextID, session)
		if err := config.CreateSessionDirs(dir); err != nil {
			t.Fatalf("newHierarchyHarness: CreateSessionDirs(%s): %v", session, err)
		}
		sessionDirs[session] = dir
	}

	key := func(session string) string { return session + ":" + hierarchyMouthpiece }

	nodes := map[string]discovery.NodeInfo{
		key(hierarchyTopSession): {
			PaneID:      "pane-top",
			SessionName: hierarchyTopSession,
			SessionDir:  sessionDirs[hierarchyTopSession],
		},
		key(hierarchyOwnerSession): {
			PaneID:      "pane-owner",
			SessionName: hierarchyOwnerSession,
			SessionDir:  sessionDirs[hierarchyOwnerSession],
		},
		key(hierarchyRepoSession): {
			PaneID:      "pane-repo",
			SessionName: hierarchyRepoSession,
			SessionDir:  sessionDirs[hierarchyRepoSession],
		},
	}

	// Adjacency keys/values use the full "session:node" form throughout
	// (not bare node names), since all three levels share the same bare
	// "mouthpiece" name and would otherwise collide. message.DeliverMessage
	// resolves a bare sender against its own source session before falling
	// back to this full form, so using the full form everywhere here is
	// unambiguous regardless of which level's post/ a message originates
	// from.
	adjacency := map[string][]string{
		key(hierarchyTopSession):   {key(hierarchyOwnerSession)},
		key(hierarchyOwnerSession): {key(hierarchyTopSession), key(hierarchyRepoSession)},
		key(hierarchyRepoSession):  {key(hierarchyOwnerSession)},
	}

	cfg := &config.Config{
		EnterDelay:  0,
		TmuxTimeout: 0.1,
	}

	return &hierarchyHarness{
		baseDir:     baseDir,
		contextID:   hierarchyContextID,
		sessionDirs: sessionDirs,
		nodes:       nodes,
		adjacency:   adjacency,
		cfg:         cfg,
	}
}

// postAndDeliver writes a message into fromSession's post/ directory,
// addressed from the bare "mouthpiece" node in fromSession to the given
// full "session:node" recipient, and calls message.DeliverMessage
// synchronously -- exactly #768's own instruction to exercise routing
// "through the normal producer/consumer interfaces rather than
// hand-authoring impossible events". seq must be unique within a test.
func (h *hierarchyHarness) postAndDeliver(t *testing.T, fromSession, toFullName string, seq int, body string) {
	t.Helper()
	sessionDir := h.sessionDirs[fromSession]
	if sessionDir == "" {
		t.Fatalf("postAndDeliver(seq=%d): unknown fromSession %q", seq, fromSession)
	}
	ts := fmt.Sprintf("20260601-%06d", seq)
	filename := ts + "-from-" + hierarchyMouthpiece + "-to-" + toFullName + ".md"
	content := fmt.Sprintf(
		"---\nparams:\n  contextId: %s\n  from: %s\n  to: %s\n  timestamp: %s\n---\n\n%s\n",
		h.contextID, hierarchyMouthpiece, toFullName,
		time.Now().Format("2006-01-02T15:04:05"),
		body,
	)
	postPath := filepath.Join(sessionDir, "post", filename)
	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		t.Fatalf("postAndDeliver(seq=%d): writing post: %v", seq, err)
	}
	if err := message.DeliverMessage(
		postPath, h.contextID, h.nodes, h.adjacency, h.cfg,
		func(string) bool { return true },
		nil,
		idle.NewIdleTracker(),
		"",
	); err != nil {
		t.Fatalf("postAndDeliver(seq=%d): DeliverMessage: %v", seq, err)
	}
}

// inboxCount returns the number of files in the given level's mouthpiece
// inbox.
func (h *hierarchyHarness) inboxCount(t *testing.T, session string) int {
	t.Helper()
	dir := filepath.Join(h.sessionDirs[session], "inbox", hierarchyMouthpiece)
	return countFiles(t, dir)
}

// postCount returns the number of files still sitting in the given
// level's post/ directory (should be 0 once DeliverMessage has processed
// every posted file).
func (h *hierarchyHarness) postCount(t *testing.T, session string) int {
	t.Helper()
	dir := filepath.Join(h.sessionDirs[session], "post")
	return countFiles(t, dir)
}
