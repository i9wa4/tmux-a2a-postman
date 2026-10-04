package e2e_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
// directories instead of one.
//
// Guardian REVIEW-853 R0 correction (H-2): this is a CONSUMER-SIDE
// three-session transport baseline, not a #768 AC1/"hierarchy" fixture.
// There is no WorkspaceTree, Parent field, common session template, or
// alias-resolution mechanism here -- all three sessions share ONE flat
// hierarchyContextID and a hand-built adjacency map with no structural
// notion of "level" or "parent" at all; "top"/"owner"/"repo" are plain
// session-name strings this harness happens to use, not a modeled
// hierarchy. #768's actual AC1 (one common mouthpiece-based session
// template) and its own hierarchy/alias-resolution requirements remain
// OPEN, not satisfied by this harness, and explicitly wait on
// #764/#765/#766/#767 landing first.
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

// deadLetterCount returns the number of files in the given level's
// dead-letter directory (Guardian REVIEW-853 H-4: postCount==0 alone
// does not prove delivery, since DeliverMessage may dead-letter a
// message and still return nil -- a test must also prove the
// dead-letter directory stayed empty for a delivery it expects to
// succeed).
func (h *hierarchyHarness) deadLetterCount(t *testing.T, session string) int {
	t.Helper()
	dir := filepath.Join(h.sessionDirs[session], "dead-letter")
	return countFiles(t, dir)
}

// inboxEnvelope reads the single file expected in the given level's
// mouthpiece inbox and returns its raw content, failing the test if
// there isn't exactly one file (Guardian REVIEW-853 H-1: a count alone
// does not prove the right message, with the right from/to/body,
// actually arrived -- callers must assert against this content).
func (h *hierarchyHarness) inboxEnvelope(t *testing.T, session string) string {
	t.Helper()
	dir := filepath.Join(h.sessionDirs[session], "inbox", hierarchyMouthpiece)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("inboxEnvelope(%s): ReadDir: %v", session, err)
	}
	var files []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e)
		}
	}
	if len(files) != 1 {
		t.Fatalf("inboxEnvelope(%s): found %d files, want exactly 1", session, len(files))
	}
	raw, err := os.ReadFile(filepath.Join(dir, files[0].Name()))
	if err != nil {
		t.Fatalf("inboxEnvelope(%s): ReadFile: %v", session, err)
	}
	return string(raw)
}

// assertEnvelopeContains fails the test unless every given substring
// (typically the exact from:/to: header values and the body text) is
// present in the envelope content.
func assertEnvelopeContains(t *testing.T, envelope string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(envelope, w) {
			t.Fatalf("envelope does not contain %q; envelope:\n%s", w, envelope)
		}
	}
}
