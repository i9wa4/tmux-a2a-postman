package message

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
)

// #624/#700 A3/A4/A5: the delivery side (authoritative) of the derived
// interface-edge decision. Sessions top <- owner <- repo each declare the bare
// interface node "mouthpiece"; there are NO static edges, so a delivery that
// succeeds can only have been authorized by the shared evaluator.

type interfaceEdgeEnv struct {
	baseDir  string
	dirs     map[string]string
	nodes    map[string]discovery.NodeInfo
	cfg      *config.Config
	enabled  func(string) bool
	ownedMap map[string]bool
}

func newInterfaceEdgeEnv(t *testing.T, mutate func(*config.Config)) *interfaceEdgeEnv {
	t.Helper()
	env := &interfaceEdgeEnv{
		baseDir:  t.TempDir(),
		dirs:     map[string]string{},
		nodes:    map[string]discovery.NodeInfo{},
		enabled:  func(string) bool { return true },
		ownedMap: map[string]bool{"top": true, "owner": true, "repo": true},
	}
	for _, session := range []string{"top", "owner", "repo"} {
		dir := filepath.Join(env.baseDir, session)
		if err := config.CreateSessionDirs(dir); err != nil {
			t.Fatalf("CreateSessionDirs(%s): %v", session, err)
		}
		env.dirs[session] = dir
		env.nodes[session+":mouthpiece"] = discovery.NodeInfo{PaneID: "%" + session, SessionName: session, SessionDir: dir}
	}
	env.cfg = &config.Config{
		InterfaceNode: "mouthpiece",
		WorkspaceTree: []config.WorkspaceTreeNodeConfig{
			{SessionName: "top", InterfaceNode: "mouthpiece"},
			{SessionName: "owner", ParentSessionName: "top", InterfaceNode: "mouthpiece"},
			{SessionName: "repo", ParentSessionName: "owner", InterfaceNode: "mouthpiece"},
		},
	}
	if mutate != nil {
		mutate(env.cfg)
	}
	previous := contextOwnsSessionForDelivery
	contextOwnsSessionForDelivery = func(_, _, session string) bool { return env.ownedMap[session] }
	t.Cleanup(func() { contextOwnsSessionForDelivery = previous })
	return env
}

// deliver posts one message from fromSession's bare mouthpiece to the
// qualified recipient and runs the real DeliverMessage consumer with the given
// daemon session. The static adjacency is always empty.
func (e *interfaceEdgeEnv) deliver(t *testing.T, fromSession, to, daemonSession string) (postPath, filename string) {
	t.Helper()
	filename = "20260709-120000-r0001-from-mouthpiece-to-" + to + ".md"
	content := "---\nparams:\n  contextId: test-ctx\n  from: mouthpiece\n  to: " + to + "\n  timestamp: 2026-07-09T12:00:00Z\n---\n\nderived edge\n"
	postPath = writePost(t, e.dirs[fromSession], filename, content)
	if err := DeliverMessage(postPath, "test-ctx", e.nodes, map[string][]string{}, e.cfg, e.enabled, nil, idle.NewIdleTracker(), daemonSession); err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}
	return postPath, filename
}

func (e *interfaceEdgeEnv) inboxHas(session, filename string) bool {
	_, err := os.Stat(filepath.Join(e.dirs[session], "inbox", "mouthpiece", filename))
	return err == nil
}

func (e *interfaceEdgeEnv) assertNoDelivery(t *testing.T, filename string) {
	t.Helper()
	for session := range e.dirs {
		if e.inboxHas(session, filename) {
			t.Fatalf("message reached %s inbox, want no delivery anywhere", session)
		}
	}
}

func TestDeliverMessage_DerivedInterfaceEdgeDeliversBothDirections(t *testing.T) {
	for _, tc := range []struct{ from, to, toSession string }{
		{"top", "owner:mouthpiece", "owner"},
		{"owner", "top:mouthpiece", "top"},
		{"owner", "repo:mouthpiece", "repo"},
		{"repo", "owner:mouthpiece", "owner"},
	} {
		t.Run(tc.from+" to "+tc.to, func(t *testing.T) {
			env := newInterfaceEdgeEnv(t, nil)
			postPath, filename := env.deliver(t, tc.from, tc.to, "owner")
			if !env.inboxHas(tc.toSession, filename) {
				t.Fatalf("derived-edge message not delivered to %s", tc.toSession)
			}
			if _, err := os.Stat(postPath); !os.IsNotExist(err) {
				t.Fatalf("post still present after delivery: %v", err)
			}
			assertDeadLetterSuffixCount(t, env.dirs[tc.from], dlSuffixRoutingDenied, 0)
		})
	}
}

// Without a workspace_tree the derived path is a no-op: the pre-change
// default-deny result is unchanged (the fixture that must fail on ecb426f
// when the tree IS present, and pass unchanged when it is not).
func TestDeliverMessage_NoWorkspaceTreeKeepsDefaultDeny(t *testing.T) {
	env := newInterfaceEdgeEnv(t, func(c *config.Config) { c.WorkspaceTree = nil })
	_, filename := env.deliver(t, "owner", "top:mouthpiece", "owner")
	env.assertNoDelivery(t, filename)
	assertDeadLetterSuffixCount(t, env.dirs["owner"], dlSuffixRoutingDenied, 1)
}

// A2/A3/A5: every case below is denied by the tree/live evaluator, lands in
// the SOURCE session's dead-letter, and reaches no inbox.
func TestDeliverMessage_DerivedInterfaceEdgeDenials(t *testing.T) {
	cases := []struct {
		name          string
		mutate        func(*config.Config)
		prepare       func(*interfaceEdgeEnv)
		from, to      string
		daemonSession string
		wantRoutingDL int
	}{
		{name: "transitive top to repo", from: "top", to: "repo:mouthpiece", daemonSession: "owner", wantRoutingDL: 1},
		{
			name: "peer did not declare", mutate: func(c *config.Config) { c.WorkspaceTree[0].InterfaceNode = "" },
			from: "owner", to: "top:mouthpiece", daemonSession: "owner", wantRoutingDL: 1,
		},
		{
			name: "global interface_node empty", mutate: func(c *config.Config) { c.InterfaceNode = "" },
			from: "owner", to: "top:mouthpiece", daemonSession: "owner", wantRoutingDL: 1,
		},
		{
			name: "recipient is not the declared interface node",
			prepare: func(e *interfaceEdgeEnv) {
				e.nodes["top:worker"] = discovery.NodeInfo{PaneID: "%w", SessionName: "top", SessionDir: e.dirs["top"]}
			},
			from: "owner", to: "top:worker", daemonSession: "owner", wantRoutingDL: 1,
		},
		{
			name:    "recipient session not owned by this context",
			prepare: func(e *interfaceEdgeEnv) { e.ownedMap["top"] = false },
			from:    "owner", to: "top:mouthpiece", daemonSession: "owner", wantRoutingDL: 1,
		},
		{
			name:   "own entry disagrees with the global",
			mutate: func(c *config.Config) { c.WorkspaceTree[1].InterfaceNode = "desk" },
			from:   "owner", to: "top:mouthpiece", daemonSession: "owner", wantRoutingDL: 1,
		},
		{
			name: "duplicate workspace_tree entry",
			mutate: func(c *config.Config) {
				c.WorkspaceTree = append(c.WorkspaceTree, config.WorkspaceTreeNodeConfig{SessionName: "top", InterfaceNode: "mouthpiece"})
			},
			from: "owner", to: "top:mouthpiece", daemonSession: "owner", wantRoutingDL: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newInterfaceEdgeEnv(t, tc.mutate)
			if tc.prepare != nil {
				tc.prepare(env)
			}
			_, filename := env.deliver(t, tc.from, tc.to, tc.daemonSession)
			env.assertNoDelivery(t, filename)
			assertDeadLetterSuffixCount(t, env.dirs[tc.from], dlSuffixRoutingDenied, tc.wantRoutingDL)
		})
	}
}

// A3: a node that is gone, or a session that is disabled, is denied on the
// very next decision with nothing cached from an earlier allowed delivery.
func TestDeliverMessage_DerivedInterfaceEdgeIsReevaluatedPerDecision(t *testing.T) {
	env := newInterfaceEdgeEnv(t, nil)
	_, first := env.deliver(t, "owner", "top:mouthpiece", "owner")
	if !env.inboxHas("top", first) {
		t.Fatal("first derived-edge delivery should succeed")
	}

	// The recipient session is now owned by another context (rebound elsewhere).
	env.ownedMap["top"] = false
	second := "20260709-120001-r0002-from-mouthpiece-to-top:mouthpiece.md"
	content := "---\nparams:\n  contextId: test-ctx\n  from: mouthpiece\n  to: top:mouthpiece\n  timestamp: 2026-07-09T12:00:01Z\n---\n\nsecond\n"
	postPath := writePost(t, env.dirs["owner"], second, content)
	if err := DeliverMessage(postPath, "test-ctx", env.nodes, map[string][]string{}, env.cfg, env.enabled, nil, idle.NewIdleTracker(), "owner"); err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}
	if env.inboxHas("top", second) {
		t.Fatal("second delivery succeeded after the recipient session was no longer owned; an edge was cached")
	}
	assertDeadLetterSuffixCount(t, env.dirs["owner"], dlSuffixRoutingDenied, 1)
}

// A5: the foreign-sender guard still runs BEFORE routing. A message in
// top's post/ claiming to come from owner's declared interface node (a
// perfectly valid derived edge to top's child) is dead-lettered as a forged
// sender with no inbox side effect anywhere.
func TestDeliverMessage_ForgedSenderIsStillDeniedBeforeDerivedEdges(t *testing.T) {
	env := newInterfaceEdgeEnv(t, nil)
	filename := "20260709-120000-r0003-from-owner:mouthpiece-to-top:mouthpiece.md"
	content := "---\nparams:\n  contextId: test-ctx\n  from: owner:mouthpiece\n  to: top:mouthpiece\n  timestamp: 2026-07-09T12:00:00Z\n---\n\nforged\n"
	postPath := writePost(t, env.dirs["repo"], filename, content)
	before := hashTree(t, env.dirs["owner"])
	if err := DeliverMessage(postPath, "test-ctx", env.nodes, map[string][]string{}, env.cfg, env.enabled, nil, idle.NewIdleTracker(), "owner"); err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}
	env.assertNoDelivery(t, filename)
	assertDeadLetterSuffixCount(t, env.dirs["repo"], dlSuffixForgedSender, 1)
	assertDeadLetterSuffixCount(t, env.dirs["repo"], dlSuffixRoutingDenied, 0)
	if after := hashTree(t, env.dirs["owner"]); !reflect.DeepEqual(before, after) {
		t.Fatalf("the claimed session's content changed after the forged message:\nbefore = %v\nafter  = %v", before, after)
	}
}

// R5/A5: a sender cannot grant itself a derived edge by claiming to be the
// declared interface node of a related session from a non-interface node: the
// physical source session decides, and the sender node must be ITS session's
// declared interface node.
func TestDeliverMessage_SenderThatIsNotTheDeclaredInterfaceNodeIsDenied(t *testing.T) {
	env := newInterfaceEdgeEnv(t, nil)
	env.nodes["owner:worker"] = discovery.NodeInfo{PaneID: "%ow", SessionName: "owner", SessionDir: env.dirs["owner"]}
	filename := "20260709-120000-r0004-from-worker-to-top:mouthpiece.md"
	content := "---\nparams:\n  contextId: test-ctx\n  from: worker\n  to: top:mouthpiece\n  timestamp: 2026-07-09T12:00:00Z\n---\n\nnot the interface node\n"
	postPath := writePost(t, env.dirs["owner"], filename, content)
	if err := DeliverMessage(postPath, "test-ctx", env.nodes, map[string][]string{}, env.cfg, env.enabled, nil, idle.NewIdleTracker(), "owner"); err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}
	env.assertNoDelivery(t, filename)
	assertDeadLetterSuffixCount(t, env.dirs["owner"], dlSuffixRoutingDenied, 1)
}
