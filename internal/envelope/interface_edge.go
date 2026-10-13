package envelope

import (
	"slices"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspacetree"
)

func hasDerivedAddress(values []string, want string) bool {
	return slices.Contains(values, want)
}

// derivedTalksTo lists the "session:node" addresses the recipient may reach
// over derived interface edges right now (#624/#700), using the one shared
// evaluator with a per-call live view. It is informational: delivery
// re-evaluates every send. No workspace_tree or interface_node means none.
func derivedTalksTo(cfg *config.Config, contextID, recipient, sourceSessionName string, nodes map[string]discovery.NodeInfo) []string {
	if cfg == nil || len(cfg.WorkspaceTree) == 0 || cfg.ConfiguredInterfaceNode() == "" {
		return nil
	}
	session, node, qualified := nodeaddr.Split(recipient)
	if !qualified {
		session, node = sourceSessionName, recipient
	}
	if session == "" || node == "" {
		return nil
	}
	baseDir := config.ResolveBaseDir(cfg.BaseDir)
	view := workspacetree.LiveView{
		OwnSession: config.FindContextSessionName(baseDir, contextID),
		NodeExists: func(address string) bool {
			_, found := nodes[address]
			return found
		},
		Ambiguous:      func(address string) bool { return nodes[address].Collision },
		SessionEnabled: func(s string) bool { return config.SessionEnabledByMarker(baseDir, contextID, s) },
		SessionOwned:   func(s string) bool { return config.ContextOwnsSession(baseDir, contextID, s) },
	}
	return workspacetree.InterfaceNeighbors(cfg, view, session, node)
}
