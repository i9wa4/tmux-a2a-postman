package daemon

import (
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/verdictgate"
)

// #624/#700 A8 (R8): the own-session interface node keeps the verdict-debt
// exemption and the escalation target through ConfiguredInterfaceNode().
// Peer interface_node declarations and tree position grant NOTHING: a peer's
// declared node is neither exempt nor an escalation target.

func privilegeConfig() *config.Config {
	return &config.Config{
		InterfaceNode: "mouthpiece",
		WorkspaceTree: []config.WorkspaceTreeNodeConfig{
			{SessionName: "owner", InterfaceNode: "mouthpiece"},
			{SessionName: "top", InterfaceNode: "desk"},
			{SessionName: "repo", ParentSessionName: "owner", InterfaceNode: "scribe"},
		},
	}
}

func TestEscalationTarget_IsTheOwnSessionInterfaceNodeNeverAPeerDeclaration(t *testing.T) {
	cfg := privilegeConfig()
	nodes := map[string]discovery.NodeInfo{
		"top:desk":         {PaneID: "%1", SessionName: "top"},
		"repo:scribe":      {PaneID: "%2", SessionName: "repo"},
		"owner:mouthpiece": {PaneID: "%3", SessionName: "owner"},
	}
	got, ok := runtimeInterfaceNode(cfg, nodes, "owner")
	if !ok || got.NodeKey != "owner:mouthpiece" {
		t.Fatalf("escalation target = %q (found %v), want the own-session interface node owner:mouthpiece", got.NodeKey, ok)
	}

	// Only peers present: a parent's or child's declared interface node is not
	// an escalation target, even though it is declared and related by tree.
	peersOnly := map[string]discovery.NodeInfo{
		"top:desk":    {PaneID: "%1", SessionName: "top"},
		"repo:scribe": {PaneID: "%2", SessionName: "repo"},
	}
	if got, ok := runtimeInterfaceNode(cfg, peersOnly, "owner"); ok {
		t.Fatalf("a peer declaration became the escalation target: %q", got.NodeKey)
	}
	// Even a peer that declares the SAME bare name in another session is not
	// the own-session node.
	sameBare := map[string]discovery.NodeInfo{"top:mouthpiece": {PaneID: "%4", SessionName: "top"}}
	if got, ok := runtimeInterfaceNode(cfg, sameBare, "owner"); ok {
		t.Fatalf("a foreign session's same-named node became the escalation target: %q", got.NodeKey)
	}
}

func TestVerdictExemption_BelongsOnlyToTheOwnInterfaceNode(t *testing.T) {
	cfg := privilegeConfig()
	own := cfg.ConfiguredInterfaceNode()
	if own != "mouthpiece" {
		t.Fatalf("ConfiguredInterfaceNode = %q, want mouthpiece", own)
	}
	if !verdictgate.IsExemptSender("mouthpiece", own) {
		t.Fatal("the own-session interface node lost the verdict-debt exemption")
	}
	for _, peerDeclared := range []string{"desk", "scribe"} {
		if verdictgate.IsExemptSender(peerDeclared, own) {
			t.Fatalf("peer-declared interface node %q is exempt; peer declarations must grant nothing", peerDeclared)
		}
	}
}
