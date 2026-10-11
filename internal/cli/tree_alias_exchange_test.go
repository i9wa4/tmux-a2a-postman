package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
)

// #768 (Refs): deterministic producer-to-consumer scenarios for workspace-tree
// aliases across three explicitly registered levels. The producer is the real
// public command (RunSendHeredoc, as an agent would call send-heredoc --to
// <alias>); the consumer is the real delivery path (message.DeliverMessage).
// Nothing here hand-authors a post file, configures interface_node/ui_node/
// messenger, or exercises authorization; those wait on #890 and #700. Reply
// slots (pop, --reply-required, --reply-to, --fills-input-request-id) are not
// exercised either: these are independent directed sends, not a reply exchange.

const (
	treeAliasContext = "ctx-tree-alias-exchange"
	treeAliasTop     = "level-top"
	treeAliasOwner   = "level-owner"
	treeAliasRepo    = "level-repo"
	treeAliasNode    = "mouthpiece"
	treeAliasRole    = "Mouthpiece for this level."
)

var treeAliasSessions = []string{treeAliasTop, treeAliasOwner, treeAliasRepo}

// treeAliasAdjacentEdges link adjacent levels only. treeAliasDirectEdge adds a
// top<->repo edge so the edge layer would ALLOW a top-to-repo send; a rejection
// of that send can then only come from the tree.
var (
	treeAliasAdjacentEdges = []string{
		"level-top:mouthpiece --- level-owner:mouthpiece",
		"level-owner:mouthpiece --- level-repo:mouthpiece",
	}
	treeAliasDirectEdge = "level-top:mouthpiece --- level-repo:mouthpiece"
)

// treeAliasConfigTOML registers top <- owner <- repo with the same node name
// and the same role text in every session; only the session, label, and
// declared parent differ.
func treeAliasConfigTOML(edges []string) string {
	var b strings.Builder
	b.WriteString("[postman]\nedges = [\n")
	for _, edge := range edges {
		fmt.Fprintf(&b, "  %q,\n", edge)
	}
	b.WriteString("]\n\n")
	levels := []struct{ session, label, parent string }{
		{treeAliasTop, "top", ""},
		{treeAliasOwner, "owner", treeAliasTop},
		{treeAliasRepo, "repo", treeAliasOwner},
	}
	for _, level := range levels {
		b.WriteString("[[postman.workspace_tree]]\n")
		fmt.Fprintf(&b, "session = %q\nlabel = %q\n", level.session, level.label)
		if level.parent != "" {
			fmt.Fprintf(&b, "parent = %q\n", level.parent)
		}
		fmt.Fprintf(&b, "representative = %q\n\n", treeAliasNode)
	}
	for _, level := range levels {
		fmt.Fprintf(&b, "[%q]\nrole = %q\n\n", level.session+":"+treeAliasNode, treeAliasRole)
	}
	return b.String()
}

type treeAliasFixture struct {
	home       string
	configPath string
	cfg        *config.Config
	nodes      map[string]discovery.NodeInfo
	adjacency  map[string][]string
}

func newTreeAliasFixture(t *testing.T, edges ...string) *treeAliasFixture {
	t.Helper()
	if len(edges) == 0 {
		edges = treeAliasAdjacentEdges
	}
	home := t.TempDir()
	t.Chdir(home)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	configPath := filepath.Join(home, "postman.toml")
	if err := os.WriteFile(configPath, []byte(treeAliasConfigTOML(edges)), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}

	nodes := map[string]discovery.NodeInfo{}
	for _, session := range treeAliasSessions {
		dir := filepath.Join(home, treeAliasContext, session)
		if err := config.CreateSessionDirs(dir); err != nil {
			t.Fatalf("CreateSessionDirs(%s): %v", session, err)
		}
		nodes[session+":"+treeAliasNode] = discovery.NodeInfo{
			PaneID:      "pane-" + session,
			SessionName: session,
			SessionDir:  dir,
		}
	}
	// The consumer sees the same edge list that was written to the TOML the
	// producer loads, so the two cannot drift apart.
	cfg := &config.Config{Edges: edges, EnterDelay: 0, TmuxTimeout: 0.1}
	adjacency, err := config.ParseEdges(cfg.Edges)
	if err != nil {
		t.Fatalf("ParseEdges: %v", err)
	}
	return &treeAliasFixture{home: home, configPath: configPath, cfg: cfg, nodes: nodes, adjacency: adjacency}
}

func (f *treeAliasFixture) sessionDir(session string) string {
	return filepath.Join(f.home, treeAliasContext, session)
}

func (f *treeAliasFixture) dirFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// send runs the public send-heredoc producer as the mouthpiece of
// fromSession, addressed to the given alias or explicit address.
func (f *treeAliasFixture) send(t *testing.T, fromSession, to, body string) error {
	t.Helper()
	installFakeTmuxForCLI(t, f.home, fromSession, treeAliasNode)
	return runSendHeredocWithBody(t, body, []string{
		"--config", f.configPath,
		"--context-id", treeAliasContext,
		"--to", to,
	})
}

// onlyPost returns the single post the producer left in fromSession's post/.
func (f *treeAliasFixture) onlyPost(t *testing.T, fromSession string) string {
	t.Helper()
	files := f.dirFiles(t, filepath.Join(f.sessionDir(fromSession), "post"))
	if len(files) != 1 {
		t.Fatalf("%s post/ has %d files, want exactly 1: %v", fromSession, len(files), files)
	}
	return filepath.Join(f.sessionDir(fromSession), "post", files[0])
}

// deliver hands one post to the real delivery consumer.
func (f *treeAliasFixture) deliver(t *testing.T, postPath string) {
	t.Helper()
	if err := message.DeliverMessage(
		postPath, treeAliasContext, f.nodes, f.adjacency, f.cfg,
		func(string) bool { return true }, nil, idle.NewIdleTracker(), "",
	); err != nil {
		t.Fatalf("DeliverMessage(%s): %v", filepath.Base(postPath), err)
	}
}

// treeAliasFrontmatterValue returns the exact value of a top-level params key.
func treeAliasFrontmatterValue(t *testing.T, raw, key string) string {
	t.Helper()
	parts := strings.SplitN(raw, "---\n", 3)
	if len(parts) != 3 {
		t.Fatalf("envelope lacks two --- delimiters:\n%s", raw)
	}
	for _, line := range strings.Split(parts[1], "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key+":") {
			return strings.TrimSpace(strings.TrimPrefix(line, key+":"))
		}
	}
	t.Fatalf("envelope has no %q field:\n%s", key, raw)
	return ""
}

// assertNothingElseTouched fails unless every directory the exchange could
// have written to is empty, for every session, except the one inbox that is
// allowed to hold exactly the expected delivered message. An empty allowInbox
// means nothing may exist anywhere (used by rejection cases).
func (f *treeAliasFixture) assertNothingElseTouched(t *testing.T, allowInbox string) {
	t.Helper()
	for _, session := range treeAliasSessions {
		for _, sub := range []string{"post", "draft", "dead-letter"} {
			if got := f.dirFiles(t, filepath.Join(f.sessionDir(session), sub)); len(got) != 0 {
				t.Fatalf("%s %s/ not empty: %v", session, sub, got)
			}
		}
		if session == allowInbox {
			continue
		}
		if got := f.dirFiles(t, filepath.Join(f.sessionDir(session), "inbox", treeAliasNode)); len(got) != 0 {
			t.Fatalf("%s inbox not empty: %v", session, got)
		}
	}
}

// assertDelivered proves one exchange end to end: the post left post/, the
// message sits in exactly the expected recipient inbox under the same name
// with exact from/to values, and no other inbox, post, draft or dead-letter
// directory in any session holds anything.
func (f *treeAliasFixture) assertDelivered(t *testing.T, postPath, toSession, wantTo, body string) {
	t.Helper()
	name := filepath.Base(postPath)
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Fatalf("post still in post/ after delivery: %s", postPath)
	}
	raw, err := os.ReadFile(filepath.Join(f.sessionDir(toSession), "inbox", treeAliasNode, name))
	if err != nil {
		t.Fatalf("message not in %s inbox: %v", toSession, err)
	}
	if got := treeAliasFrontmatterValue(t, string(raw), "to"); got != wantTo {
		t.Fatalf("to = %q, want %q", got, wantTo)
	}
	if got := treeAliasFrontmatterValue(t, string(raw), "from"); got != treeAliasNode {
		t.Fatalf("from = %q, want %q", got, treeAliasNode)
	}
	if !strings.Contains(string(raw), body) {
		t.Fatalf("delivered message lacks body %q:\n%s", body, raw)
	}
	if got := f.dirFiles(t, filepath.Join(f.sessionDir(toSession), "inbox", treeAliasNode)); len(got) != 1 {
		t.Fatalf("%s inbox has %d files, want exactly 1: %v", toSession, len(got), got)
	}
	f.assertNothingElseTouched(t, toSession)
}

// Every alias form compiles, through the real producer, to the explicit
// recipient of the declared relationship and is accepted by the real consumer.
func TestTreeAliasExchange_AliasFormsReachDeclaredRelative(t *testing.T) {
	cases := []struct {
		name, from, alias, toSession string
	}{
		{"parent default representative", treeAliasOwner, "@parent", treeAliasTop},
		{"parent explicit node", treeAliasOwner, "@parent/mouthpiece", treeAliasTop},
		{"child by label default representative", treeAliasOwner, "@child/repo", treeAliasRepo},
		{"child by session", treeAliasOwner, "@child/level-repo", treeAliasRepo},
		{"child single-child node form", treeAliasOwner, "@child/mouthpiece", treeAliasRepo},
		{"child by label and node", treeAliasOwner, "@child/repo/mouthpiece", treeAliasRepo},
		{"top addresses its child", treeAliasTop, "@child/owner", treeAliasOwner},
		{"repo addresses its parent", treeAliasRepo, "@parent", treeAliasOwner},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newTreeAliasFixture(t)
			body := "alias send: " + tc.name
			if err := f.send(t, tc.from, tc.alias, body); err != nil {
				t.Fatalf("send %s -> %s: %v", tc.from, tc.alias, err)
			}
			post := f.onlyPost(t, tc.from)
			wantTo := tc.toSession + ":" + treeAliasNode
			if !strings.HasSuffix(filepath.Base(post), "-to-"+wantTo+".md") {
				t.Fatalf("post filename %q does not carry compiled recipient %q", filepath.Base(post), wantTo)
			}
			f.deliver(t, post)
			f.assertDelivered(t, post, tc.toSession, wantTo, body)
		})
	}
}

// The tree gives no shortcut and the rejection is attributable to the tree:
// each case asserts the alias-layer reason (or the alias syntax error), not
// just "some error". Cases marked directEdge run with an explicit top<->repo
// edge, so the producer's independent edge check would allow a top-to-repo
// send; the tree alone must still refuse it. A failed send leaves nothing
// behind in any session (post, inbox, draft, dead-letter).
func TestTreeAliasExchange_NonRelativesAreRejectedByTheTree(t *testing.T) {
	cases := []struct {
		name, from, alias string
		directEdge        bool
		wantErr           string
	}{
		{"root has no parent", treeAliasTop, "@parent", false, `workspace tree alias "@parent" failed: no_parent`},
		{"root has no parent even with a direct edge", treeAliasTop, "@parent", true, `workspace tree alias "@parent" failed: no_parent`},
		{"top cannot reach grandchild by session and node", treeAliasTop, "@child/level-repo/mouthpiece", false, `failed: no_child`},
		{"top cannot reach grandchild by session and node with a direct edge", treeAliasTop, "@child/level-repo/mouthpiece", true, `failed: no_child`},
		{"top cannot reach grandchild by label and node with a direct edge", treeAliasTop, "@child/repo/mouthpiece", true, `failed: no_child`},
		{"unknown child label", treeAliasOwner, "@child/nope/mouthpiece", false, `failed: no_child`},
		{"leaf has no children", treeAliasRepo, "@child/anyone", false, `failed: no_child`},
		// With exactly one child, a bare @child/<x> that matches no label,
		// session or id is read as a node name on that one child, so the
		// alias compiles to level-owner:repo, never to the grandchild's
		// session. The producer then refuses it because that node is not
		// configured, even when a direct top<->repo edge exists.
		{"top single-child bare form never targets the grandchild", treeAliasTop, "@child/repo", false, `missing receiver: "level-owner:repo"`},
		{"top single-child bare form never targets the grandchild with a direct edge", treeAliasTop, "@child/repo", true, `missing receiver: "level-owner:repo"`},
		{"malformed alias", treeAliasOwner, "@child/repo/mouthpiece/extra", false, `workspace tree alias: invalid child alias`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var f *treeAliasFixture
			if tc.directEdge {
				f = newTreeAliasFixture(t, append(append([]string{}, treeAliasAdjacentEdges...), treeAliasDirectEdge)...)
			} else {
				f = newTreeAliasFixture(t)
			}
			err := f.send(t, tc.from, tc.alias, "must not be delivered")
			if err == nil {
				t.Fatalf("send %s -> %s succeeded, want an error containing %q", tc.from, tc.alias, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("send %s -> %s error = %q, want it to contain %q", tc.from, tc.alias, err.Error(), tc.wantErr)
			}
			f.assertNothingElseTouched(t, "")
		})
	}
}

// Four independent directed alias sends through the three levels (down, down,
// up, up) using only relationship aliases. Each lands in exactly the next
// session's same-named mouthpiece with exact from/to, and the other levels'
// inboxes stay untouched (no cross-delivery between same-named nodes). These
// are plain sends: no pop, no reply slot, no --reply-to, no
// --fills-input-request-id, so reply-slot return routing is NOT exercised here.
func TestTreeAliasExchange_FourDirectedAliasSendsAcrossThreeLevels(t *testing.T) {
	f := newTreeAliasFixture(t)
	hops := []struct {
		from, alias, toSession, body string
	}{
		{treeAliasTop, "@child/owner", treeAliasOwner, "down: top to owner"},
		{treeAliasOwner, "@child/repo", treeAliasRepo, "down: owner to repo"},
		{treeAliasRepo, "@parent", treeAliasOwner, "up: repo to owner"},
		{treeAliasOwner, "@parent", treeAliasTop, "up: owner to top"},
	}
	wantInbox := map[string]int{}
	for i, hop := range hops {
		if err := f.send(t, hop.from, hop.alias, hop.body); err != nil {
			t.Fatalf("hop %d send: %v", i+1, err)
		}
		post := f.onlyPost(t, hop.from)
		f.deliver(t, post)
		wantInbox[hop.toSession]++
		raw, err := os.ReadFile(filepath.Join(f.sessionDir(hop.toSession), "inbox", treeAliasNode, filepath.Base(post)))
		if err != nil {
			t.Fatalf("hop %d: message not in %s inbox: %v", i+1, hop.toSession, err)
		}
		if got := treeAliasFrontmatterValue(t, string(raw), "to"); got != hop.toSession+":"+treeAliasNode {
			t.Fatalf("hop %d: to = %q", i+1, got)
		}
		if !strings.Contains(string(raw), hop.body) {
			t.Fatalf("hop %d: body %q missing:\n%s", i+1, hop.body, raw)
		}
		for _, session := range treeAliasSessions {
			got := len(f.dirFiles(t, filepath.Join(f.sessionDir(session), "inbox", treeAliasNode)))
			if got != wantInbox[session] {
				t.Fatalf("after hop %d: %s inbox has %d messages, want %d", i+1, session, got, wantInbox[session])
			}
			for _, sub := range []string{"post", "draft", "dead-letter"} {
				if left := f.dirFiles(t, filepath.Join(f.sessionDir(session), sub)); len(left) != 0 {
					t.Fatalf("after hop %d: %s %s/ not empty: %v", i+1, session, sub, left)
				}
			}
		}
	}
}
