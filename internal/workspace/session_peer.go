package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
)

// NamedRuntime rechecks a native session name and interface role on every
// Snapshot. A name is only a route selector; the server incarnation, session
// ID, pane ID and role binding remain the authority for a file operation.
type NamedRuntime struct {
	tmux           Tmux
	name           string
	role           string
	server         string
	incarnation    string
	sessionID      string
	panePID        string
	directoryRoute bool
}

// DirectoryInterface is a verified live interface in a native tmux session.
// Root is the canonical HOME-relative directory of its interface pane.
type DirectoryInterface struct {
	Session string
	Root    string
	Runtime NamedRuntime
}

// DirectoryInterfaces discovers only live panes bearing the configured
// interface role on this server. Each candidate is rechecked as a named
// runtime; duplicate roles, roots, or session names fail closed.
func (t Tmux) DirectoryInterfaces(ctx context.Context, role string) ([]DirectoryInterface, error) {
	if !binding.ValidateNodeName(role) {
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
			"-F", "#{session_id}\t#{pane_id}\t#{pane_title}\t#{pane_dead}\t#{pane_pid}")
		if err != nil {
			return nil, err
		}
		count := 0
		var interfacePane, interfacePID string
		for _, pane := range strings.Split(strings.TrimSuffix(panes, "\n"), "\n") {
			parts := strings.Split(pane, "\t")
			if len(parts) != 5 || parts[0] != sessionID || !nativeID(parts[1], '%') ||
				!positivePID(parts[4]) || (parts[3] != "0" && parts[3] != "1") {
				return nil, ErrUnknown
			}
			if parts[2] == role && parts[3] == "0" {
				count++
				interfacePane, interfacePID = parts[1], parts[4]
			}
		}
		if count == 0 {
			continue
		}
		name, err := nativeSessionName(ctx, t.Socket, sessionID)
		if err != nil || seenNames[name] {
			return nil, ErrUnknown
		}
		seenNames[name] = true
		if count != 1 {
			return nil, errors.New("ambiguous interface role binding")
		}
		peer := t
		peer.Pane = interfacePane
		owner := origin.Caller.Owner
		runtime := NamedRuntime{peer, name, role, owner.Server, owner.Incarnation, sessionID, interfacePID, false}
		snapshot, err := runtime.Snapshot(ctx)
		if err != nil {
			return nil, err
		}
		if sessionID == owner.Session && snapshot.Root != origin.Root {
			return nil, ErrUnknown
		}
		if seenRoots[snapshot.Root] {
			return nil, errors.New("ambiguous interface workspace root")
		}
		seenRoots[snapshot.Root] = true
		interfaces = append(interfaces, DirectoryInterface{name, snapshot.Root, runtime})
	}
	return interfaces, nil
}

// DirectDirectoryPeer selects one parent or child by canonical directory
// containment. A live interface strictly between the pair blocks shortcuts.
func DirectDirectoryPeer(interfaces []DirectoryInterface, sender, target string) (DirectoryInterface, error) {
	var from, to *DirectoryInterface
	seenSessions, seenRoots := map[string]bool{}, map[string]bool{}
	for i := range interfaces {
		if seenSessions[interfaces[i].Session] || seenRoots[interfaces[i].Root] {
			return DirectoryInterface{}, errors.New("ambiguous directory interfaces")
		}
		seenSessions[interfaces[i].Session], seenRoots[interfaces[i].Root] = true, true
		if interfaces[i].Session == sender {
			from = &interfaces[i]
		}
		if interfaces[i].Session == target {
			to = &interfaces[i]
		}
	}
	if from == nil || to == nil || from == to || !directoryContains(from.Root, to.Root) && !directoryContains(to.Root, from.Root) {
		return DirectoryInterface{}, errors.New("no direct directory interface edge")
	}
	parent, child := from.Root, to.Root
	if directoryContains(to.Root, from.Root) {
		parent, child = to.Root, from.Root
	}
	for _, middle := range interfaces {
		if middle.Session != sender && middle.Session != target &&
			directoryContains(parent, middle.Root) && directoryContains(middle.Root, child) {
			return DirectoryInterface{}, errors.New("intermediate live interface blocks directory route")
		}
	}
	return *to, nil
}

func directoryContains(parent, child string) bool {
	if parent == "~" {
		return strings.HasPrefix(child, "~/")
	}
	return strings.HasPrefix(child, parent+"/")
}

func (r NamedRuntime) Snapshot(ctx context.Context) (Snapshot, error) {
	snapshot, err := r.tmux.Snapshot(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	owner := snapshot.Caller.Owner
	if snapshot.Caller.Role != r.role || owner.Server != r.server ||
		owner.Incarnation != r.incarnation || owner.Session != r.sessionID || snapshot.Caller.PanePID != r.panePID {
		return Snapshot{}, fmt.Errorf("%w: named runtime binding changed", ErrUnknown)
	}
	name, err := nativeSessionName(ctx, r.tmux.Socket, owner.Session)
	if err != nil || name != r.name {
		return Snapshot{}, fmt.Errorf("%w: named session changed", ErrUnknown)
	}
	return snapshot, nil
}

func (r NamedRuntime) Probe(ctx context.Context, owner Owner) (Liveness, error) {
	return r.tmux.Probe(ctx, owner)
}

// NamedCaller anchors the invocation's own pane to its current exact native
// session name. This is the source half of a cross-session interface route.
func (t Tmux) NamedCaller(ctx context.Context) (NamedRuntime, error) {
	snapshot, err := t.Snapshot(ctx)
	if err != nil {
		return NamedRuntime{}, err
	}
	name, err := nativeSessionName(ctx, t.Socket, snapshot.Caller.Owner.Session)
	if err != nil {
		return NamedRuntime{}, err
	}
	owner := snapshot.Caller.Owner
	runtime := NamedRuntime{t, name, snapshot.Caller.Role, owner.Server, owner.Incarnation, owner.Session, snapshot.Caller.PanePID, false}
	if _, err := runtime.Snapshot(ctx); err != nil {
		return NamedRuntime{}, err
	}
	return runtime, nil
}

func (r NamedRuntime) Name() string { return r.name }

// WithDirectoryRoute requires a fresh direct-directory relation at the mail
// publication boundary. Explicit workspace-tree routes do not set this flag.
func (r NamedRuntime) WithDirectoryRoute() NamedRuntime {
	r.directoryRoute = true
	return r
}

func verifyDirectoryRoute(ctx context.Context, source, destination NamedRuntime, from, to Snapshot) error {
	if source.role != destination.role {
		return ErrUnknown
	}
	interfaces, err := source.tmux.DirectoryInterfaces(ctx, source.role)
	if err != nil {
		return err
	}
	selected, err := DirectDirectoryPeer(interfaces, source.name, destination.name)
	if err != nil {
		return err
	}
	if selected.Root != to.Root || selected.Runtime.sessionID != destination.sessionID ||
		selected.Runtime.panePID != destination.panePID {
		return ErrUnknown
	}
	for _, candidate := range interfaces {
		if candidate.Session == source.name {
			if candidate.Root != from.Root || candidate.Runtime.sessionID != source.sessionID ||
				candidate.Runtime.panePID != source.panePID {
				return ErrUnknown
			}
			return nil
		}
	}
	return ErrUnknown
}

// FindNamedPeer uses a complete bounded native enumeration, then checks the
// candidate through the same Snapshot contract as the caller. Ambiguous or
// absent role bindings never select an arbitrary pane.
func (t Tmux) FindNamedPeer(ctx context.Context, sessionName, role string) (NamedRuntime, error) {
	if !validNativeSessionName(sessionName) || !binding.ValidateNodeName(role) {
		return NamedRuntime{}, ErrUnknown
	}
	caller, err := t.Snapshot(ctx)
	if err != nil {
		return NamedRuntime{}, err
	}
	out, err := tmuxOutput(ctx, t.Socket, "list-sessions", "-F", "#{session_id}")
	if err != nil {
		return NamedRuntime{}, err
	}
	var selected string
	for _, sessionID := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if !nativeID(sessionID, '$') {
			return NamedRuntime{}, ErrUnknown
		}
		name, err := rawNativeSessionName(ctx, t.Socket, sessionID)
		if err != nil {
			return NamedRuntime{}, err
		}
		if name == sessionName {
			if selected != "" {
				return NamedRuntime{}, errors.New("ambiguous named session")
			}
			selected = sessionID
		}
	}
	if selected == "" || selected == caller.Caller.Owner.Session {
		return NamedRuntime{}, errors.New("peer session is absent or is the caller session")
	}
	out, err = tmuxOutput(ctx, t.Socket, "list-panes", "-s", "-t", selected,
		"-F", "#{session_id}\t#{pane_id}\t#{pane_title}\t#{pane_dead}\t#{pane_pid}")
	if err != nil {
		return NamedRuntime{}, err
	}
	var pane, panePID string
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 5 || fields[0] != selected || !nativeID(fields[1], '%') || !positivePID(fields[4]) ||
			(fields[3] != "0" && fields[3] != "1") {
			return NamedRuntime{}, ErrUnknown
		}
		if fields[2] == role && fields[3] == "0" {
			if pane != "" {
				return NamedRuntime{}, errors.New("ambiguous peer role binding")
			}
			pane = fields[1]
			panePID = fields[4]
		}
	}
	if pane == "" {
		return NamedRuntime{}, errors.New("peer interface role is absent")
	}
	peer := t
	peer.Pane = pane
	owner := caller.Caller.Owner
	runtime := NamedRuntime{peer, sessionName, role, owner.Server, owner.Incarnation, selected, panePID, false}
	if _, err := runtime.Snapshot(ctx); err != nil {
		return NamedRuntime{}, err
	}
	return runtime, nil
}

func nativeSessionName(ctx context.Context, socket, sessionID string) (string, error) {
	name, err := rawNativeSessionName(ctx, socket, sessionID)
	if err != nil || !validNativeSessionName(name) {
		return "", ErrUnknown
	}
	return name, nil
}

func rawNativeSessionName(ctx context.Context, socket, sessionID string) (string, error) {
	out, err := tmuxOutput(ctx, socket, "display-message", "-p", "-t", sessionID,
		"#{session_id}\t#{session_name}")
	if err != nil {
		return "", err
	}
	identity, name, ok := strings.Cut(strings.TrimSuffix(out, "\n"), "\t")
	if !ok || identity != sessionID {
		return "", ErrUnknown
	}
	return name, nil
}

func validNativeSessionName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, ":/\\ \t\r\n\x00")
}
