package daemon

import (
	"reflect"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

const whitespaceInterfaceNodeTopology = "edges = [\"messenger --- worker\"]\n\n[messenger]\n[worker]\n"

// TestInterfaceNodeWhitespace_VerdictExemption_F002 (#764 F-002): the verdict
// exemption the daemon installs is the trimmed interface node. " messenger " is
// the messenger node; a whitespace-only value exempts nobody.
func TestInterfaceNodeWhitespace_VerdictExemption_F002(t *testing.T) {
	original := verdictExemptInterfaceNode
	t.Cleanup(func() { verdictExemptInterfaceNode = original })

	configureVerdictGateFromConfig(&config.Config{InterfaceNode: " messenger "})
	if verdictExemptInterfaceNode != "messenger" {
		t.Fatalf("verdictExemptInterfaceNode = %q for interface_node=\" messenger \", want messenger", verdictExemptInterfaceNode)
	}

	configureVerdictGateFromConfig(&config.Config{InterfaceNode: "   "})
	if verdictExemptInterfaceNode != "" {
		t.Fatalf("verdictExemptInterfaceNode = %q for a whitespace-only interface_node, want no exemption", verdictExemptInterfaceNode)
	}
}

// TestInterfaceNodeWhitespace_StartupPingSelector_F002: the startup PING
// selector uses the trimmed node. An explicit whitespace-only value keeps the
// existing fan-out, like an explicit empty value.
func TestInterfaceNodeWhitespace_StartupPingSelector_F002(t *testing.T) {
	nodes := map[string]discovery.NodeInfo{
		"review:messenger": {},
		"review:worker":    {},
	}
	all := []string{"review:messenger", "review:worker"}

	t.Run("padded name narrows to that node", func(t *testing.T) {
		cfg := loadInterfaceNodeTestConfig(t, "[postman]\ninterface_node = \" messenger \"\n"+whitespaceInterfaceNodeTopology)
		got := startupAutoPingNodeKeys(nodes, cfg)
		if want := []string{"review:messenger"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("startupAutoPingNodeKeys(\" messenger \") = %v, want %v", got, want)
		}
	})
	t.Run("whitespace only keeps fan-out", func(t *testing.T) {
		cfg := loadInterfaceNodeTestConfig(t, "[postman]\ninterface_node = \"   \"\n"+whitespaceInterfaceNodeTopology)
		got := startupAutoPingNodeKeys(nodes, cfg)
		if !reflect.DeepEqual(got, all) {
			t.Fatalf("startupAutoPingNodeKeys(\"   \") = %v, want %v", got, all)
		}
	})
}

// TestInterfaceNodeWhitespace_EscalationTarget_F002: the escalation target is
// resolved from the trimmed node, and a whitespace-only value has no target.
func TestInterfaceNodeWhitespace_EscalationTarget_F002(t *testing.T) {
	nodes := map[string]discovery.NodeInfo{
		"review:messenger": {SessionName: "review"},
		"review:worker":    {SessionName: "review"},
	}

	got, ok := runtimeInterfaceNode(&config.Config{InterfaceNode: " messenger "}, nodes, "review")
	if !ok || got.NodeKey != "review:messenger" {
		t.Fatalf("runtimeInterfaceNode(\" messenger \") = (%q, %v), want (review:messenger, true)", got.NodeKey, ok)
	}

	if _, ok := runtimeInterfaceNode(&config.Config{InterfaceNode: "   "}, nodes, "review"); ok {
		t.Fatal("runtimeInterfaceNode(whitespace only) found a target, want none")
	}
}
