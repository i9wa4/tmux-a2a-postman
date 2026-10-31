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
}

func (r NamedRuntime) Snapshot(ctx context.Context) (Snapshot, error) {
	snapshot, err := r.tmux.Snapshot(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	owner := snapshot.Caller.Owner
	if snapshot.Caller.Role != r.role || owner.Server != r.server ||
		owner.Incarnation != r.incarnation || owner.Session != r.sessionID {
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
	runtime := NamedRuntime{t, name, snapshot.Caller.Role, owner.Server, owner.Incarnation, owner.Session}
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
		"-F", "#{session_id}\t#{pane_id}\t#{pane_title}\t#{pane_dead}")
	if err != nil {
		return NamedRuntime{}, err
	}
	var pane string
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 || fields[0] != selected || !nativeID(fields[1], '%') ||
			(fields[3] != "0" && fields[3] != "1") {
			return NamedRuntime{}, ErrUnknown
		}
		if fields[2] == role && fields[3] == "0" {
			if pane != "" {
				return NamedRuntime{}, errors.New("ambiguous peer role binding")
			}
			pane = fields[1]
		}
	}
	if pane == "" {
		return NamedRuntime{}, errors.New("peer interface role is absent")
	}
	peer := t
	peer.Pane = pane
	owner := caller.Caller.Owner
	runtime := NamedRuntime{peer, sessionName, role, owner.Server, owner.Incarnation, selected}
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
