package workspacetree

import (
	"reflect"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

// chainConfig builds top <- owner <- repo (parent links), every entry
// declaring the bare interface node "mouthpiece", with the global
// interface_node also "mouthpiece". Mutators adjust it per case.
func chainConfig(mutate func(*config.Config)) *config.Config {
	cfg := &config.Config{
		InterfaceNode: "mouthpiece",
		WorkspaceTree: []config.WorkspaceTreeNodeConfig{
			{SessionName: "top", InterfaceNode: "mouthpiece"},
			{SessionName: "owner", ParentSessionName: "top", InterfaceNode: "mouthpiece"},
			{SessionName: "repo", ParentSessionName: "owner", InterfaceNode: "mouthpiece"},
		},
	}
	if mutate != nil {
		mutate(cfg)
	}
	return cfg
}

// liveAll is a view in which every node exists, every session is enabled and
// owned, and nothing is ambiguous.
func liveAll(own string) LiveView {
	return LiveView{
		OwnSession:     own,
		NodeExists:     func(string) bool { return true },
		SessionEnabled: func(string) bool { return true },
		SessionOwned:   func(string) bool { return true },
	}
}

func TestEvaluateInterfaceEdge_DirectParentChildBothDeclaredIsAllowedBothWays(t *testing.T) {
	cfg := chainConfig(nil)
	view := liveAll("owner")
	for _, tc := range []struct{ fromSession, to string }{
		{"top", "owner:mouthpiece"},
		{"owner", "top:mouthpiece"},
		{"owner", "repo:mouthpiece"},
		{"repo", "owner:mouthpiece"},
	} {
		got := EvaluateInterfaceEdge(cfg, view, tc.fromSession, "mouthpiece", tc.to)
		if !got.Allowed || got.Reason != InterfaceEdgeAllowed {
			t.Errorf("%s -> %s = %+v, want allowed", tc.fromSession, tc.to, got)
		}
	}
}

func TestEvaluateInterfaceEdge_OwnEntryMayOmitDeclaration(t *testing.T) {
	cfg := chainConfig(func(c *config.Config) { c.WorkspaceTree[1].InterfaceNode = "" })
	got := EvaluateInterfaceEdge(cfg, liveAll("owner"), "owner", "mouthpiece", "top:mouthpiece")
	if !got.Allowed {
		t.Fatalf("own entry without interface_node = %+v, want allowed (global designates it)", got)
	}
	// A PEER entry without a declaration is never inferred.
	got = EvaluateInterfaceEdge(cfg, liveAll("top"), "top", "mouthpiece", "owner:mouthpiece")
	if got.Allowed || got.Reason != InterfaceEdgeUndeclared {
		t.Fatalf("peer without interface_node = %+v, want denied undeclared", got)
	}
}

func TestEvaluateInterfaceEdge_DenialsAreAttributedAndFailClosed(t *testing.T) {
	cases := []struct {
		name           string
		mutate         func(*config.Config)
		view           func() LiveView
		from, node, to string
		want           InterfaceEdgeReason
		ownSession     string
	}{
		{
			name:   "global interface_node empty means no derived edges",
			mutate: func(c *config.Config) { c.InterfaceNode = "" },
			from:   "owner", node: "mouthpiece", to: "top:mouthpiece", want: InterfaceEdgeNoInterfaceNode, ownSession: "owner",
		},
		{
			name:   "global whitespace-only means none",
			mutate: func(c *config.Config) { c.InterfaceNode = "   " },
			from:   "owner", node: "mouthpiece", to: "top:mouthpiece", want: InterfaceEdgeNoInterfaceNode, ownSession: "owner",
		},
		{
			name:   "peer has not declared",
			mutate: func(c *config.Config) { c.WorkspaceTree[0].InterfaceNode = "" },
			from:   "owner", node: "mouthpiece", to: "top:mouthpiece", want: InterfaceEdgeUndeclared, ownSession: "owner",
		},
		{
			name: "transitive top to repo is never an edge",
			from: "top", node: "mouthpiece", to: "repo:mouthpiece", want: InterfaceEdgeNotDirectRelative, ownSession: "top",
		},
		{
			name: "siblings are not related",
			mutate: func(c *config.Config) {
				c.WorkspaceTree = append(c.WorkspaceTree, config.WorkspaceTreeNodeConfig{SessionName: "docs", ParentSessionName: "owner", InterfaceNode: "mouthpiece"})
			},
			from: "repo", node: "mouthpiece", to: "docs:mouthpiece", want: InterfaceEdgeNotDirectRelative, ownSession: "repo",
		},
		{
			name: "same session is not a cross-session edge",
			from: "owner", node: "mouthpiece", to: "owner:mouthpiece", want: InterfaceEdgeSameSession, ownSession: "owner",
		},
		{
			name: "bare recipient is not a derived edge",
			from: "owner", node: "mouthpiece", to: "mouthpiece", want: InterfaceEdgeUnqualified, ownSession: "owner",
		},
		{
			name: "sender is not its session's interface node",
			from: "owner", node: "worker", to: "top:mouthpiece", want: InterfaceEdgeNotInterfaceNode, ownSession: "owner",
		},
		{
			name: "recipient is not its session's interface node",
			from: "owner", node: "mouthpiece", to: "top:worker", want: InterfaceEdgeNotInterfaceNode, ownSession: "owner",
		},
		{
			name:   "own entry disagrees with the global",
			mutate: func(c *config.Config) { c.WorkspaceTree[1].InterfaceNode = "other" },
			from:   "owner", node: "mouthpiece", to: "top:mouthpiece", want: InterfaceEdgeUndeclared, ownSession: "owner",
		},
		{
			name:   "qualified peer declaration is rejected",
			mutate: func(c *config.Config) { c.WorkspaceTree[0].InterfaceNode = "top:mouthpiece" },
			from:   "owner", node: "mouthpiece", to: "top:mouthpiece", want: InterfaceEdgeUndeclared, ownSession: "owner",
		},
		{
			name: "duplicate entry for a session fails closed",
			mutate: func(c *config.Config) {
				c.WorkspaceTree = append(c.WorkspaceTree, config.WorkspaceTreeNodeConfig{SessionName: "top", InterfaceNode: "mouthpiece"})
			},
			from: "owner", node: "mouthpiece", to: "top:mouthpiece", want: InterfaceEdgeAmbiguousSession, ownSession: "owner",
		},
		{
			name: "recipient session not in the tree",
			from: "owner", node: "mouthpiece", to: "ghost:mouthpiece", want: InterfaceEdgeUnknownSession, ownSession: "owner",
		},
		{
			name: "node is gone",
			view: func() LiveView {
				v := liveAll("owner")
				v.NodeExists = func(a string) bool { return a != "top:mouthpiece" }
				return v
			},
			from: "owner", node: "mouthpiece", to: "top:mouthpiece", want: InterfaceEdgeNodeAbsent, ownSession: "owner",
		},
		{
			name: "session disabled",
			view: func() LiveView {
				v := liveAll("owner")
				v.SessionEnabled = func(s string) bool { return s != "top" }
				return v
			},
			from: "owner", node: "mouthpiece", to: "top:mouthpiece", want: InterfaceEdgeSessionDisabled, ownSession: "owner",
		},
		{
			name: "session not owned by this context (rebound elsewhere)",
			view: func() LiveView {
				v := liveAll("owner")
				v.SessionOwned = func(s string) bool { return s != "top" }
				return v
			},
			from: "owner", node: "mouthpiece", to: "top:mouthpiece", want: InterfaceEdgeSessionNotOwned, ownSession: "owner",
		},
		{
			name: "node discovery collision",
			view: func() LiveView {
				v := liveAll("owner")
				v.Ambiguous = func(a string) bool { return a == "top:mouthpiece" }
				return v
			},
			from: "owner", node: "mouthpiece", to: "top:mouthpiece", want: InterfaceEdgeNodeAmbiguous, ownSession: "owner",
		},
		{
			name: "nil live hooks fail closed",
			view: func() LiveView { return LiveView{OwnSession: "owner"} },
			from: "owner", node: "mouthpiece", to: "top:mouthpiece", want: InterfaceEdgeNodeAbsent, ownSession: "owner",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := chainConfig(tc.mutate)
			view := liveAll(tc.ownSession)
			if tc.view != nil {
				view = tc.view()
			}
			got := EvaluateInterfaceEdge(cfg, view, tc.from, tc.node, tc.to)
			if got.Allowed || got.Reason != tc.want {
				t.Fatalf("%s %s -> %s = %+v, want denied with reason %q", tc.from, tc.node, tc.to, got, tc.want)
			}
		})
	}
}

func TestEvaluateInterfaceEdge_SameBareNameAcrossSessionsAndDirectionsAreIndependent(t *testing.T) {
	// repo declares a different bare name than its parent's; each endpoint is
	// checked against its OWN session's declaration.
	cfg := chainConfig(func(c *config.Config) { c.WorkspaceTree[2].InterfaceNode = "desk" })
	view := liveAll("owner")
	if got := EvaluateInterfaceEdge(cfg, view, "owner", "mouthpiece", "repo:desk"); !got.Allowed {
		t.Fatalf("owner:mouthpiece -> repo:desk = %+v, want allowed", got)
	}
	if got := EvaluateInterfaceEdge(cfg, view, "owner", "mouthpiece", "repo:mouthpiece"); got.Allowed || got.Reason != InterfaceEdgeNotInterfaceNode {
		t.Fatalf("owner:mouthpiece -> repo:mouthpiece = %+v, want not_interface_node", got)
	}
}

func TestDeclaredInterfaceEdges_AreOnlyDirectParentChildPairs(t *testing.T) {
	cfg := chainConfig(nil)
	got := DeclaredInterfaceEdges(cfg, "owner")
	want := []InterfaceEdge{
		{A: "owner:mouthpiece", B: "repo:mouthpiece"},
		{A: "top:mouthpiece", B: "owner:mouthpiece"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DeclaredInterfaceEdges = %+v, want %+v (A-B and B-C, never A-C)", got, want)
	}
	// Dropping the middle declaration removes BOTH edges and never creates top-repo.
	cfg = chainConfig(func(c *config.Config) { c.WorkspaceTree[1].InterfaceNode = "" })
	if got := DeclaredInterfaceEdges(cfg, "top"); len(got) != 0 {
		t.Fatalf("with an undeclared middle session, edges = %+v, want none", got)
	}
}

func TestActiveInterfaceEdges_AreTheDeclaredEdgesThatPassTheLiveView(t *testing.T) {
	cfg := chainConfig(nil)
	view := liveAll("owner")
	view.SessionOwned = func(s string) bool { return s != "repo" }
	got := ActiveInterfaceEdges(cfg, view)
	want := []InterfaceEdge{{A: "top:mouthpiece", B: "owner:mouthpiece"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ActiveInterfaceEdges = %+v, want %+v", got, want)
	}
}

func TestInterfaceNeighbors_MatchTheSendDecision(t *testing.T) {
	cfg := chainConfig(nil)
	view := liveAll("owner")
	got := InterfaceNeighbors(cfg, view, "owner", "mouthpiece")
	want := []string{"repo:mouthpiece", "top:mouthpiece"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InterfaceNeighbors(owner) = %v, want %v", got, want)
	}
	for _, address := range got {
		if d := EvaluateInterfaceEdge(cfg, view, "owner", "mouthpiece", address); !d.Allowed {
			t.Fatalf("neighbor %s is listed but EvaluateInterfaceEdge denies it: %+v", address, d)
		}
	}
	if got := InterfaceNeighbors(cfg, view, "top", "mouthpiece"); !reflect.DeepEqual(got, []string{"owner:mouthpiece"}) {
		t.Fatalf("InterfaceNeighbors(top) = %v, want only the direct child", got)
	}
	if got := InterfaceNeighbors(cfg, view, "owner", "worker"); len(got) != 0 {
		t.Fatalf("a non-interface node has neighbors %v, want none", got)
	}
}
