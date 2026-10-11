package config

import (
	"fmt"
	"strings"
)

// WorkspaceTreeEntryForSession returns the single [[postman.workspace_tree]]
// entry for sessionName. found is false when there is none; ambiguous is true
// when the session appears more than once (never resolved by picking one: a
// duplicate declaration fails closed, #624/#700 R4).
func (cfg *Config) WorkspaceTreeEntryForSession(sessionName string) (entry WorkspaceTreeNodeConfig, found, ambiguous bool) {
	if cfg == nil {
		return WorkspaceTreeNodeConfig{}, false, false
	}
	matches := 0
	for _, candidate := range cfg.WorkspaceTree {
		if strings.TrimSpace(candidate.SessionName) != sessionName {
			continue
		}
		matches++
		entry = candidate
	}
	switch matches {
	case 0:
		return WorkspaceTreeNodeConfig{}, false, false
	case 1:
		return entry, true, false
	default:
		return WorkspaceTreeNodeConfig{}, true, true
	}
}

// DeclaredInterfaceNode returns the bare interface node that session
// declares for the purpose of derived cross-session edges, and whether one is
// declared.
//
//   - The daemon's own session (ownSession) is designated by the global
//     [postman] interface_node (ConfiguredInterfaceNode). Its workspace_tree
//     entry may omit interface_node; if it sets one that differs, the
//     declaration is inconsistent and nothing is declared (fail closed).
//   - Every other session is designated only by its own entry's bare
//     interface_node. A peer's declaration is never inferred from
//     representative, a node name, or any sender claim.
func (cfg *Config) DeclaredInterfaceNode(sessionName, ownSession string) (name string, declared bool) {
	entry, found, ambiguous := cfg.WorkspaceTreeEntryForSession(sessionName)
	if !found || ambiguous {
		return "", false
	}
	entryNode := strings.TrimSpace(entry.InterfaceNode)
	if sessionName == ownSession && ownSession != "" {
		global := cfg.ConfiguredInterfaceNode()
		if global == "" {
			return "", false
		}
		if entryNode != "" && entryNode != global {
			return "", false
		}
		return global, true
	}
	if entryNode == "" || strings.Contains(entryNode, ":") {
		return "", false
	}
	return entryNode, true
}

// SessionEnabledByMarker reports whether the persisted enabled-session marker
// names contextID as the owner of sessionName. It is the CLI's conservative
// stand-in for the daemon's in-memory enabled set (#624/#700): it never treats
// a session as enabled merely because the daemon's own PID is live, so a CLI
// pre-check can only be stricter than delivery, never more permissive.
func SessionEnabledByMarker(baseDir, contextID, sessionName string) bool {
	return contextID != "" && sessionName != "" && enabledSessionOwner(baseDir, sessionName) == contextID
}

// ValidateOwnInterfaceDeclaration reports an error when the daemon's own
// workspace_tree entry sets an interface_node that differs from the global
// [postman] interface_node, or sets one while the global is empty. ownSession
// is only known at start time, so this is checked there rather than in
// ValidateConfig. An own entry that omits interface_node is valid.
func ValidateOwnInterfaceDeclaration(cfg *Config, ownSession string) error {
	entry, found, ambiguous := cfg.WorkspaceTreeEntryForSession(ownSession)
	if !found {
		return nil
	}
	if ambiguous {
		return fmt.Errorf("workspace_tree has more than one entry for session %q; interface edges fail closed", ownSession)
	}
	entryNode := strings.TrimSpace(entry.InterfaceNode)
	if entryNode == "" {
		return nil
	}
	global := cfg.ConfiguredInterfaceNode()
	if global == "" {
		return fmt.Errorf("workspace_tree entry for own session %q declares interface_node %q but [postman] interface_node is empty", ownSession, entryNode)
	}
	if entryNode != global {
		return fmt.Errorf("workspace_tree entry for own session %q declares interface_node %q but [postman] interface_node is %q; they must be equal", ownSession, entryNode, global)
	}
	return nil
}
