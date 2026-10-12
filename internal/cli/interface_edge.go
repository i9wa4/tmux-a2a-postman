package cli

import (
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspacetree"
)

// daemonOwnSession is the daemon's own session for this context, or "" when no
// daemon session can be found. It is the ONLY value that may bind a privilege
// (the verdict-debt exemption): an unknown own session must exempt nobody, so
// this never falls back to the sender's session (#624/#700 R3-2).
func (ctx commandContext) daemonOwnSession(baseDir, contextID string) string {
	return ctx.daemonSessionName(baseDir, contextID)
}

// ownSessionForInterfaceEdges is the session whose global [postman]
// interface_node designates its own declaration for the derived-edge VIEW: the
// daemon's session for this context (what delivery uses), falling back to the
// sender's session only when no daemon session can be found (single-session
// use). The fallback is for the advisory edge view only; it must never be used
// for a privilege decision (use daemonOwnSession for the verdict-debt
// exemption).
func (ctx commandContext) ownSessionForInterfaceEdges(baseDir, contextID, fallbackSession string) string {
	if own := ctx.daemonSessionName(baseDir, contextID); own != "" {
		return own
	}
	return fallbackSession
}

// interfaceEdgeView builds the live view the shared derived-interface-edge
// evaluator needs at a CLI send (#624/#700). Everything is read per call and
// nothing is cached. The CLI result is advisory (fail early); delivery decides,
// so every input here is chosen to be at least as strict as delivery's:
//   - discovery runs with the DAEMON's session as selfSession (the same own-
//     session fast path delivery's discovery uses), so a pane claimed by another
//     context in the sender's session is excluded exactly as delivery excludes it;
//   - a node that collided (more than one live pane) is ambiguous (deny);
//   - "enabled" is the persisted enabled-session marker, never the PID-based
//     ownership fallback, and "owned" is a separate ownership check.
func (ctx commandContext) interfaceEdgeView(baseDir, contextID, senderSession string) workspacetree.LiveView {
	own := ctx.ownSessionForInterfaceEdges(baseDir, contextID, senderSession)
	nodes, _ := ctx.discoverNodes(baseDir, contextID, own)
	return workspacetree.LiveView{
		OwnSession: own,
		NodeExists: func(address string) bool {
			_, found := nodes[address]
			return found
		},
		Ambiguous: func(address string) bool { return nodes[address].Collision },
		SessionEnabled: func(session string) bool {
			return ctx.sessionEnabledMarker(baseDir, contextID, session)
		},
		SessionOwned: func(session string) bool {
			return ctx.contextOwnsSession(baseDir, contextID, session)
		},
	}
}

// hasInterfaceEdgeConfig reports whether any derived edge can exist; with no
// workspace_tree the CLI does no discovery or ownership work at all.
func hasInterfaceEdgeConfig(cfg *config.Config) bool {
	return cfg != nil && len(cfg.WorkspaceTree) > 0 && cfg.ConfiguredInterfaceNode() != ""
}

// derivedInterfaceEdgeAllowedForSend is the CLI pre-check call site of the
// shared evaluator. recipient may be bare (same session, never derived) or
// already compiled from an alias to "session:node".
func (ctx commandContext) derivedInterfaceEdgeAllowedForSend(cfg *config.Config, baseDir, contextID, senderSession, senderNode, recipient string) bool {
	if !hasInterfaceEdgeConfig(cfg) {
		return false
	}
	if _, _, qualified := nodeaddr.Split(recipient); !qualified {
		return false
	}
	view := ctx.interfaceEdgeView(baseDir, contextID, senderSession)
	return workspacetree.EvaluateInterfaceEdge(cfg, view, senderSession, senderNode, recipient).Allowed
}

// interfaceNeighborsForSend lists the derived neighbors of a node for the
// "You can talk to" hints, from the same evaluator the send decision uses.
func (ctx commandContext) interfaceNeighborsForSend(cfg *config.Config, baseDir, contextID, discoverSession, viewSession, viewNode string) []string {
	if !hasInterfaceEdgeConfig(cfg) || viewSession == "" || viewNode == "" {
		return nil
	}
	view := ctx.interfaceEdgeView(baseDir, contextID, discoverSession)
	return workspacetree.InterfaceNeighbors(cfg, view, viewSession, viewNode)
}

// nodesAmbiguous returns an Ambiguous hook over a discovered node map.
func nodesAmbiguous(nodes map[string]discovery.NodeInfo) func(string) bool {
	return func(address string) bool { return nodes[address].Collision }
}
