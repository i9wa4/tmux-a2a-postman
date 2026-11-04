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
	tmux        Tmux
	name        string
	role        string
	server      string
	incarnation string
	sessionID   string
	panePID     string
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
	out, err := tmuxOutput(ctx, t.Socket, "list-sessions", "-F", "#{session_id}\t#{session_name}")
	if err != nil {
		return nil, err
	}
	var interfaces []DirectoryInterface
	seenNames, seenRoots := map[string]bool{}, map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 2 || !nativeID(fields[0], '$') || !validNativeSessionName(fields[1]) || seenNames[fields[1]] {
			return nil, ErrUnknown
		}
		seenNames[fields[1]] = true
		panes, err := tmuxOutput(ctx, t.Socket, "list-panes", "-s", "-t", fields[0],
			"-F", "#{session_id}\t#{pane_id}\t#{pane_title}\t#{pane_dead}\t#{pane_pid}")
		if err != nil {
			return nil, err
		}
		count := 0
		var interfacePane, interfacePID string
		for _, pane := range strings.Split(strings.TrimSuffix(panes, "\n"), "\n") {
			parts := strings.Split(pane, "\t")
			if len(parts) != 5 || parts[0] != fields[0] || !nativeID(parts[1], '%') ||
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
		if count != 1 {
			return nil, errors.New("ambiguous interface role binding")
		}
		peer := t
		peer.Pane = interfacePane
		owner := origin.Caller.Owner
		runtime := NamedRuntime{peer, fields[1], role, owner.Server, owner.Incarnation, fields[0], interfacePID}
		snapshot, err := runtime.Snapshot(ctx)
		if err != nil {
			return nil, err
		}
		if fields[0] == owner.Session && snapshot.Root != origin.Root {
			return nil, ErrUnknown
		}
		if seenRoots[snapshot.Root] {
			return nil, errors.New("ambiguous interface workspace root")
		}
		seenRoots[snapshot.Root] = true
		interfaces = append(interfaces, DirectoryInterface{fields[1], snapshot.Root, runtime})
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
	runtime := NamedRuntime{t, name, snapshot.Caller.Role, owner.Server, owner.Incarnation, owner.Session, snapshot.Caller.PanePID}
	if _, err := runtime.Snapshot(ctx); err != nil {
		return NamedRuntime{}, err
	}
	return runtime, nil
}

func (r NamedRuntime) Name() string { return r.name }

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
	out, err := tmuxOutput(ctx, t.Socket, "list-sessions", "-F", "#{session_id}\t#{session_name}")
	if err != nil {
		return NamedRuntime{}, err
	}
	var selected string
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 2 || !nativeID(fields[0], '$') || !validNativeSessionName(fields[1]) {
			return NamedRuntime{}, ErrUnknown
		}
		if fields[1] == sessionName {
			if selected != "" {
				return NamedRuntime{}, errors.New("ambiguous named session")
			}
			selected = fields[0]
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
	runtime := NamedRuntime{peer, sessionName, role, owner.Server, owner.Incarnation, selected, panePID}
	if _, err := runtime.Snapshot(ctx); err != nil {
		return NamedRuntime{}, err
	}
	return runtime, nil
}

func nativeSessionName(ctx context.Context, socket, sessionID string) (string, error) {
	out, err := tmuxOutput(ctx, socket, "display-message", "-p", "-t", sessionID,
		"#{session_id}\t#{session_name}")
	if err != nil {
		return "", err
	}
	fields := strings.Split(strings.TrimSuffix(out, "\n"), "\t")
	if len(fields) != 2 || fields[0] != sessionID || !validNativeSessionName(fields[1]) {
		return "", ErrUnknown
	}
	return fields[1], nil
}

func validNativeSessionName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, ":/\\ \t\r\n\x00")
}
