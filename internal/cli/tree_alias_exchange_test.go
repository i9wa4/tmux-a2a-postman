package cli

import (
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
// messenger, or exercises authorization; those wait on #890 and #700.

const (
	treeAliasContext = "ctx-tree-alias-exchange"
	treeAliasTop     = "level-top"
	treeAliasOwner   = "level-owner"
	treeAliasRepo    = "level-repo"
	treeAliasNode    = "mouthpiece"
)

// treeAliasConfig registers top <- owner <- repo with the SAME node name
// ("mouthpiece") in every session; only the session, label, and declared
// parent differ. Edges link adjacent levels only.
const treeAliasConfig = `[postman]
edges = [
  "level-top:mouthpiece --- level-owner:mouthpiece",
  "level-owner:mouthpiece --- level-repo:mouthpiece",
]

[[postman.workspace_tree]]
session = "level-top"
label = "top"
representative = "mouthpiece"

[[postman.workspace_tree]]
session = "level-owner"
label = "owner"
parent = "level-top"
representative = "mouthpiece"

[[postman.workspace_tree]]
session = "level-repo"
label = "repo"
parent = "level-owner"
representative = "mouthpiece"

["level-top:mouthpiece"]
role = "Top-level entry point."

["level-owner:mouthpiece"]
role = "Owner/group level."

["level-repo:mouthpiece"]
role = "Repository level."
`

type treeAliasFixture struct {
	home       string
	configPath string
	cfg        *config.Config
	nodes      map[string]discovery.NodeInfo
	adjacency  map[string][]string
}

func newTreeAliasFixture(t *testing.T) *treeAliasFixture {
	t.Helper()
	home := t.TempDir()
	t.Chdir(home)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	configPath := filepath.Join(home, "postman.toml")
	if err := os.WriteFile(configPath, []byte(treeAliasConfig), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}

	nodes := map[string]discovery.NodeInfo{}
	for _, session := range []string{treeAliasTop, treeAliasOwner, treeAliasRepo} {
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
	cfg := &config.Config{Edges: []string{
		"level-top:mouthpiece --- level-owner:mouthpiece",
		"level-owner:mouthpiece --- level-repo:mouthpiece",
	}, EnterDelay: 0, TmuxTimeout: 0.1}
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

// onlyPost returns the single draft the producer left in fromSession's post/.
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

// frontmatterValue returns the exact value of a top-level params key.
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

// assertDelivered proves one exchange end to end: the draft left post/, the
// message sits in exactly the expected recipient inbox under the same name
// with exact from/to values, and no session dead-lettered anything.
func (f *treeAliasFixture) assertDelivered(t *testing.T, postPath, toSession, wantTo, body string) {
	t.Helper()
	name := filepath.Base(postPath)
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Fatalf("draft still in post/ after delivery: %s", postPath)
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
	for _, session := range []string{treeAliasTop, treeAliasOwner, treeAliasRepo} {
		if dl := f.dirFiles(t, filepath.Join(f.sessionDir(session), "dead-letter")); len(dl) != 0 {
			t.Fatalf("%s dead-letter not empty: %v", session, dl)
		}
	}
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
			body := "alias exchange: " + tc.name
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

// The tree gives no shortcut: a grandparent, a grandchild, an unknown child,
// or a parent of the root is not reachable by alias, and the failed send
// leaves no draft behind.
func TestTreeAliasExchange_NonRelativesAreRejectedWithoutPost(t *testing.T) {
	cases := []struct {
		name, from, alias string
	}{
		{"root has no parent", treeAliasTop, "@parent"},
		{"top cannot skip to grandchild by label", treeAliasTop, "@child/repo"},
		{"top cannot skip to grandchild by session", treeAliasTop, "@child/level-repo/mouthpiece"},
		{"unknown child label", treeAliasOwner, "@child/nope/mouthpiece"},
		{"repo has no children", treeAliasRepo, "@child/anyone"},
		{"malformed alias", treeAliasOwner, "@child/repo/mouthpiece/extra"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newTreeAliasFixture(t)
			if err := f.send(t, tc.from, tc.alias, "must not be delivered"); err == nil {
				t.Fatalf("send %s -> %s succeeded, want an error", tc.from, tc.alias)
			}
			for _, session := range []string{treeAliasTop, treeAliasOwner, treeAliasRepo} {
				if got := f.dirFiles(t, filepath.Join(f.sessionDir(session), "post")); len(got) != 0 {
					t.Fatalf("%s post/ not empty after rejected send: %v", session, got)
				}
				if got := f.dirFiles(t, filepath.Join(f.sessionDir(session), "inbox", treeAliasNode)); len(got) != 0 {
					t.Fatalf("%s inbox not empty after rejected send: %v", session, got)
				}
			}
		})
	}
}

// A full round trip through the three levels, request down and result up,
// using only relationship aliases. Each hop lands in exactly the next
// session's same-named mouthpiece with exact from/to, and the other levels'
// inboxes stay untouched (no cross-delivery between same-named nodes).
func TestTreeAliasExchange_ThreeLevelRequestAndReturnByAlias(t *testing.T) {
	f := newTreeAliasFixture(t)
	hops := []struct {
		from, alias, toSession, body string
	}{
		{treeAliasTop, "@child/owner", treeAliasOwner, "request: top to owner"},
		{treeAliasOwner, "@child/repo", treeAliasRepo, "request: owner to repo"},
		{treeAliasRepo, "@parent", treeAliasOwner, "result: repo to owner"},
		{treeAliasOwner, "@parent", treeAliasTop, "result: owner to top"},
	}
	wantInbox := map[string]int{}
	for i, hop := range hops {
		if err := f.send(t, hop.from, hop.alias, hop.body); err != nil {
			t.Fatalf("hop %d send: %v", i+1, err)
		}
		post := f.onlyPost(t, hop.from)
		f.deliver(t, post)
		f.assertDelivered(t, post, hop.toSession, hop.toSession+":"+treeAliasNode, hop.body)
		wantInbox[hop.toSession]++
		for _, session := range []string{treeAliasTop, treeAliasOwner, treeAliasRepo} {
			got := len(f.dirFiles(t, filepath.Join(f.sessionDir(session), "inbox", treeAliasNode)))
			if got != wantInbox[session] {
				t.Fatalf("after hop %d: %s inbox has %d messages, want %d", i+1, session, got, wantInbox[session])
			}
		}
	}
}
