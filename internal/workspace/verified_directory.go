package workspace

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
)

// VerifiedDirectoryInterfaces uses the caller's loaded configuration for its
// own interface and each peer workspace's current registered configuration for
// its interface. A pane title or the caller's interface role cannot designate
// a peer interface on its own.
func (s Store) VerifiedDirectoryInterfaces(ctx context.Context, t Tmux, sourceRole string) ([]DirectoryInterface, error) {
	return s.verifiedDirectoryInterfaces(ctx, t, sourceRole, "")
}

// VerifiedDirectoryInterfacesOnRoute verifies only roots on the addressed
// native corridor. Unrelated siblings and never-registered panes cannot veto
// a send; registered but stale intermediate roots block publication.
func (s Store) VerifiedDirectoryInterfacesOnRoute(ctx context.Context, t Tmux, sourceRole, targetRoot string) ([]DirectoryInterface, error) {
	if targetRoot == "" {
		return nil, ErrUnknown
	}
	return s.verifiedDirectoryInterfaces(ctx, t, sourceRole, targetRoot)
}

func (s Store) verifiedDirectoryInterfaces(ctx context.Context, t Tmux, sourceRole, targetRoot string) ([]DirectoryInterface, error) {
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
	type nativeGroup struct {
		sessionID, root string
		bindings        []Binding
	}
	var groupsToVerify []nativeGroup
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
			if targetRoot == "" {
				if root != origin.Root && !directoryContains(root, origin.Root) && !directoryContains(origin.Root, root) {
					continue
				}
			} else {
				onRoute := root == origin.Root || root == targetRoot ||
					directoryContains(origin.Root, root) && directoryContains(root, targetRoot) ||
					directoryContains(targetRoot, root) && directoryContains(root, origin.Root)
				if !onRoute {
					continue
				}
			}
			groupsToVerify = append(groupsToVerify, nativeGroup{sessionID, root, native})
		}
	}
	if targetRoot == "" {
		// Visit the immediate ancestor first and descendants from shallow to
		// deep. A confirmed or unavailable intermediate owns its subtree, so
		// deeper registrations cannot become direct contacts or veto this view.
		sort.Slice(groupsToVerify, func(i, j int) bool {
			a, b := groupsToVerify[i], groupsToVerify[j]
			class := func(root string) int {
				switch {
				case root == origin.Root:
					return 0
				case directoryContains(root, origin.Root):
					return 1
				default:
					return 2
				}
			}
			ca, cb := class(a.root), class(b.root)
			if ca != cb {
				return ca < cb
			}
			if len(a.root) != len(b.root) {
				if ca == 1 {
					return len(a.root) > len(b.root)
				}
				return len(a.root) < len(b.root)
			}
			return a.sessionID < b.sessionID
		})
	}
	var interfaces []DirectoryInterface
	seenNames, seenRoots := map[string]bool{}, map[string]bool{}
	var childFrontier []string
	parentFrontier := ""
	for _, group := range groupsToVerify {
		sessionID, root, native := group.sessionID, group.root, group.bindings
		if targetRoot == "" {
			if directoryContains(root, origin.Root) && parentFrontier != "" && root != parentFrontier {
				continue
			}
			blocked := false
			for _, frontier := range childFrontier {
				if directoryContains(frontier, root) {
					blocked = true
					break
				}
			}
			if blocked {
				continue
			}
		}
		role := sourceRole
		peer := t
		var saved runtimeRecord
		if sessionID != origin.Caller.Owner.Session || root != origin.Root {
			var scope *StatusScope
			var roles []string
			saved, scope, roles, err = s.readStatusAttribution(ctx, root,
				Owner{Server: origin.Caller.Owner.Server, Incarnation: origin.Caller.Owner.Incarnation, Session: sessionID}, native)
			if errors.Is(err, errUnregisteredWorkspace) {
				if root == targetRoot {
					return nil, ErrUnknown // An addressed interface must be registered.
				}
				continue // An ordinary pane without registration is not an interface.
			}
			if targetRoot == "" && errors.Is(err, errMissingConfigProvenance) {
				if directoryContains(origin.Root, root) {
					childFrontier = append(childFrontier, root)
				} else if directoryContains(root, origin.Root) {
					parentFrontier = root
				}
				continue // No current configuration means this sibling is not a verified contact.
			}
			if targetRoot == "" && root == "~" && err != nil && directoryContains(root, origin.Root) &&
				formerAncestorWithoutLiveRole(saved, native) {
				continue // An old registration cannot turn ordinary ancestor panes into an interface.
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
		if targetRoot == "" {
			if directoryContains(origin.Root, root) {
				childFrontier = append(childFrontier, root)
			} else if directoryContains(root, origin.Root) {
				parentFrontier = root
			}
		}
	}
	return interfaces, nil
}

// A former runtime can leave private records at HOME after its team exits.
// Only an ancestor with no native pane bearing any of its registered roles is
// ordinary now; a plausible live interface still fails closed on stale state.
func formerAncestorWithoutLiveRole(saved runtimeRecord, native []Binding) bool {
	if saved.Version != 1 || !saved.Owner.valid() || len(saved.Bindings) == 0 ||
		len(native) == 0 || sameRuntime(saved.Owner, native[0].Owner) ||
		ValidateBindings(saved.Bindings) != nil {
		return false
	}
	for _, former := range saved.Bindings {
		if !sameRuntime(saved.Owner, former.Owner) {
			return false
		}
		for _, current := range native {
			if current.Role == former.Role {
				return false
			}
		}
	}
	return true
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
