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

// envelopeFilename reconstructs the deterministic filename postAndDeliver
// builds for the given seq and recipient; message.DeliverMessage preserves
// this basename unchanged when it moves a file into inbox/ or dead-letter/
// (store.DeliverPostToInbox keeps filename; store.DeadLetterPath appends a
// reason suffix before the extension), so callers can locate one specific
// delivered or dead-lettered message without relying on count alone.
func envelopeFilename(seq int, toFullName string) string {
	ts := fmt.Sprintf("20260601-%06d", seq)
	return ts + "-from-" + hierarchyMouthpiece + "-to-" + toFullName + ".md"
}

// postAndDeliver writes a message into fromSession's post/ directory,
// addressed from the bare "mouthpiece" node in fromSession to the given
// full "session:node" recipient, and calls message.DeliverMessage
// synchronously. This is a handcrafted post exercising the delivery
// consumer (message.DeliverMessage), not a producer under test (Guardian
// REVIEW-853 R1 G-3: the message is hand-assembled via os.WriteFile, not
// emitted by any real sender component). seq must be unique within a test.
func (h *hierarchyHarness) postAndDeliver(t *testing.T, fromSession, toFullName string, seq int, body string) {
	t.Helper()
	sessionDir := h.sessionDirs[fromSession]
	if sessionDir == "" {
		t.Fatalf("postAndDeliver(seq=%d): unknown fromSession %q", seq, fromSession)
	}
	filename := envelopeFilename(seq, toFullName)
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

// inboxEnvelopeForSeq reads one specific delivered message's content out of
// the given level's mouthpiece inbox, identified by the seq and recipient
// used when it was posted (Guardian REVIEW-853 R1 H-1: inboxEnvelope's
// "exactly one file" precondition cannot read a specific hop's envelope
// once more than one message has accumulated in the same inbox, e.g.
// owner's inbox after both hop 1 and hop 3).
func (h *hierarchyHarness) inboxEnvelopeForSeq(t *testing.T, session string, seq int, toFullName string) string {
	t.Helper()
	path := filepath.Join(h.sessionDirs[session], "inbox", hierarchyMouthpiece, envelopeFilename(seq, toFullName))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("inboxEnvelopeForSeq(%s, seq=%d): ReadFile(%s): %v", session, seq, path, err)
	}
	return string(raw)
}

// deadLetterFilenames returns the basenames of every file in the given
// level's dead-letter directory (Guardian REVIEW-853 R1 G-2: a bare count
// cannot distinguish a routing-denial dead-letter from any of
// message.DeliverMessage's other dead-letter reasons, nor confirm which
// specific message it was).
func (h *hierarchyHarness) deadLetterFilenames(t *testing.T, session string) []string {
	t.Helper()
	dir := filepath.Join(h.sessionDirs[session], "dead-letter")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("deadLetterFilenames(%s): ReadDir: %v", session, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// parsedEnvelope holds a delivered message's exact frontmatter from:/to:
// values and its trimmed body, for exact-value comparison.
type parsedEnvelope struct {
	from string
	to   string
	body string
}

// parseEnvelope parses the frontmatter/body format postAndDeliver writes
// ("---\n<frontmatter>\n---\n\n<body>\n"). It fails the test if the raw
// content does not have exactly that two-delimiter shape.
func parseEnvelope(t *testing.T, raw string) parsedEnvelope {
	t.Helper()
	parts := strings.SplitN(raw, "---\n", 3)
	if len(parts) != 3 {
		t.Fatalf("parseEnvelope: expected content delimited by two \"---\\n\" markers, got %d part(s); raw:\n%s", len(parts), raw)
	}
	var p parsedEnvelope
	for _, line := range strings.Split(parts[1], "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "from:"):
			p.from = strings.TrimSpace(strings.TrimPrefix(line, "from:"))
		case strings.HasPrefix(line, "to:"):
			p.to = strings.TrimSpace(strings.TrimPrefix(line, "to:"))
		}
	}
	p.body = strings.TrimSpace(parts[2])
	return p
}

// assertEnvelopeExact fails the test unless the envelope's parsed from:/to:
// header values and body text are each exactly equal to the given wants
// (Guardian REVIEW-853 R1 G-4: a raw substring match on "from: mouthpiece"
// would also match a hypothetical "from: mouthpiece-evil" line; exact
// per-field comparison after parsing rules that out).
func assertEnvelopeExact(t *testing.T, envelope, wantFrom, wantTo, wantBody string) {
	t.Helper()
	p := parseEnvelope(t, envelope)
	if p.from != wantFrom {
		t.Fatalf("envelope from = %q, want %q; envelope:\n%s", p.from, wantFrom, envelope)
	}
	if p.to != wantTo {
		t.Fatalf("envelope to = %q, want %q; envelope:\n%s", p.to, wantTo, envelope)
	}
	if p.body != wantBody {
		t.Fatalf("envelope body = %q, want %q; envelope:\n%s", p.body, wantBody, envelope)
	}
}
