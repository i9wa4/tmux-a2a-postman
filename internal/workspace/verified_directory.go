package workspace

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
)

// VerifiedDirectoryInterfaces uses the caller's loaded configuration for its
// own interface and each peer workspace's current registered configuration for
// its interface. A pane title or the caller's interface role cannot designate
// a peer interface on its own.
func (s Store) VerifiedDirectoryInterfaces(ctx context.Context, t Tmux, sourceRole string) ([]DirectoryInterface, error) {
	if !binding.ValidateNodeName(sourceRole) || !slices.Contains(t.Roles, sourceRole) || t.ConfigFingerprint == "" {
		return nil, ErrUnknown
	}
	caller, err := t.NamedCaller(ctx)
	if err != nil {
		return nil, err
	}
	origin, err := caller.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	out, err := tmuxOutput(ctx, t.Socket, "list-sessions", "-F", "#{session_id}")
	if err != nil {
		return nil, err
	}
	var interfaces []DirectoryInterface
	seenNames, seenRoots := map[string]bool{}, map[string]bool{}
	for _, sessionID := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if !nativeID(sessionID, '$') {
			return nil, ErrUnknown
		}
		panes, err := tmuxOutput(ctx, t.Socket, "list-panes", "-s", "-t", sessionID,
			"-F", "#{session_id}\t#{pane_id}\t#{pane_title}\t#{pane_dead}\t#{pane_pid}\t#{pane_current_path}")
		if err != nil {
			return nil, err
		}
		groups := map[string][]Binding{}
		for _, line := range strings.Split(strings.TrimSuffix(panes, "\n"), "\n") {
			parts := strings.Split(line, "\t")
			if len(parts) != 6 || parts[0] != sessionID || !nativeID(parts[1], '%') ||
				!positivePID(parts[4]) || (parts[3] != "0" && parts[3] != "1") {
				return nil, ErrUnknown
			}
			if parts[3] == "1" || !binding.ValidateNodeName(parts[2]) {
				continue
			}
			root, err := CanonicalRoot(parts[5], t.Home)
			if err != nil {
				continue
			}
			owner := origin.Caller.Owner
			owner.Session, owner.Pane = sessionID, parts[1]
			groups[root] = append(groups[root], Binding{Role: parts[2], Owner: owner, PanePID: parts[4]})
		}
		for root, native := range groups {
			if root != origin.Root && !directoryContains(root, origin.Root) && !directoryContains(origin.Root, root) {
				continue
			}
			role, roles := sourceRole, t.Roles
			peer := t
			var saved runtimeRecord
			if sessionID != origin.Caller.Owner.Session || root != origin.Root {
				var scope *StatusScope
				saved, scope, roles, err = s.readStatusAttribution(ctx, root,
					Owner{Server: origin.Caller.Owner.Server, Incarnation: origin.Caller.Owner.Incarnation, Session: sessionID}, native)
				if errors.Is(err, os.ErrNotExist) {
					continue // No registered interface in this workspace.
				}
				if err != nil || scope == nil || !binding.ValidateNodeName(scope.Interface) {
					return nil, ErrUnknown
				}
				role = scope.Interface
				peer.Roles, peer.ConfigPath, peer.BuiltinConfig, peer.ConfigFingerprint = roles, scope.Config, scope.Builtin, scope.Fingerprint
			}
			var selected Binding
			for _, candidate := range native {
				if candidate.Role != role {
					continue
				}
				if selected.Owner.valid() {
					return nil, errors.New("ambiguous interface role binding")
				}
				selected = candidate
			}
			if !selected.Owner.valid() {
				continue
			}
			if sessionID != origin.Caller.Owner.Session || root != origin.Root {
				if !slices.Contains(saved.Bindings, selected) {
					return nil, ErrUnknown
				}
			}
			name, err := nativeSessionName(ctx, t.Socket, sessionID)
			if err != nil || seenNames[name] || seenRoots[root] {
				return nil, ErrUnknown
			}
			peer.Pane = selected.Owner.Pane
			runtime := NamedRuntime{peer, name, role, selected.Owner.Server, selected.Owner.Incarnation, sessionID, selected.PanePID, false}
			fresh, err := runtime.Snapshot(ctx)
			if err != nil || fresh.Root != root {
				return nil, ErrUnknown
			}
			seenNames[name], seenRoots[root] = true, true
			interfaces = append(interfaces, DirectoryInterface{Session: name, Root: root, Runtime: runtime, InterfaceRole: role})
		}
	}
	return interfaces, nil
}

// VerifiedDirectoryInterfacesFrom enumerates direct-route candidates from a
// previously verified peer's own interface and configuration. Recipient
// contact guidance must use this view, since a sender below one child cannot
// see the recipient parent's other children from its own root.
func (s Store) VerifiedDirectoryInterfacesFrom(ctx context.Context, source NamedRuntime) ([]DirectoryInterface, error) {
	if _, err := source.Snapshot(ctx); err != nil {
		return nil, err
	}
	return s.VerifiedDirectoryInterfaces(ctx, source.tmux, source.role)
}
