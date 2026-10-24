package message

import (
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspacetree"
)

// contextOwnsSessionForDelivery is the ownership probe the derived-edge
// decision uses. It is a variable only so tests can supply ownership without a
// live daemon pid file; production always uses config.ContextOwnsSession.
var contextOwnsSessionForDelivery = config.ContextOwnsSession

// SetContextOwnsSessionForTest replaces the ownership probe used by the
// derived-interface-edge decision and returns a restore function. Test-only:
// it lets a cross-package fixture drive delivery with a controlled ownership
// view instead of a live daemon pid file.
func SetContextOwnsSessionForTest(fn func(baseDir, contextID, sessionName string) bool) func() {
	previous := contextOwnsSessionForDelivery
	contextOwnsSessionForDelivery = fn
	return func() { contextOwnsSessionForDelivery = previous }
}

// derivedInterfaceEdgeAllowed is the delivery-side (authoritative) call site of
// the shared derived-interface-edge evaluator (#624/#700). It is consulted only
// after the static cfg.Edges check denied, and only for qualified recipients.
// The sender is the physical source session plus the resolved node, never a
// claim in the message. With no workspace_tree it is a no-op, so existing
// configurations behave exactly as before.
func derivedInterfaceEdgeAllowed(
	cfg *config.Config,
	contextID, daemonSession string,
	knownNodes map[string]discovery.NodeInfo,
	isSessionEnabled func(string) bool,
	sourceSessionName, senderFullName, recipientFullName string,
) bool {
	if cfg == nil || len(cfg.WorkspaceTree) == 0 || isSessionEnabled == nil {
		return false
	}
	_, senderNode, ok := nodeaddr.Split(senderFullName)
	if !ok || senderNode == "" {
		return false
	}
	baseDir := config.ResolveBaseDir(cfg.BaseDir)
	view := workspacetree.LiveView{
		OwnSession: daemonSession,
		NodeExists: func(address string) bool {
			_, found := knownNodes[address]
			return found
		},
		// A node that collided (more than one live pane claimed the key) is not
		// a unique identity: deny (#624/#700 R2-1).
		Ambiguous:      func(address string) bool { return knownNodes[address].Collision },
		SessionEnabled: isSessionEnabled,
		SessionOwned: func(session string) bool {
			return contextOwnsSessionForDelivery(baseDir, contextID, session)
		},
	}
	return workspacetree.EvaluateInterfaceEdge(cfg, view, sourceSessionName, senderNode, recipientFullName).Allowed
}
