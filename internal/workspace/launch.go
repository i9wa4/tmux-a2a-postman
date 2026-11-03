package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

const (
	LaunchConfigEnvironment = "TMUX_A2A_POSTMAN_CONFIG"
	launchOwnerEnvironment  = "TMUX_A2A_POSTMAN_LAUNCH_OWNER"
	launchWindowOwnerOption = "@tmux-a2a-postman-launch-owner"
)

type LaunchPlan struct {
	Preset        string                   `json:"preset"`
	Config        string                   `json:"config"`
	BuiltinConfig bool                     `json:"builtin,omitempty"`
	CWD           string                   `json:"cwd"`
	Workspace     string                   `json:"workspace"`
	Socket        string                   `json:"socket"`
	Session       string                   `json:"session"`
	WindowSession string                   `json:"window_session,omitempty"`
	Shell         string                   `json:"shell"`
	Columns       []config.LaunchColumn    `json:"columns"`
	Home          string                   `json:"-"`
	ExpectedPID   int                      `json:"-"`
	Topology      config.WorkspaceTopology `json:"-"`
}
type LaunchedPane struct {
	Role    string `json:"role"`
	Pane    string `json:"pane"`
	PID     string `json:"pid,omitempty"`
	Column  int    `json:"column"`
	Row     int    `json:"row"`
	Started bool   `json:"started"`
}
type LaunchResult struct {
	Invocation string         `json:"invocation"`
	Server     *StatusServer  `json:"server,omitempty"`
	Session    string         `json:"session"`
	SessionID  string         `json:"session_id,omitempty"`
	Window     string         `json:"window,omitempty"`
	Workspace  string         `json:"workspace"`
	Panes      []LaunchedPane `json:"panes"`
	Cleanup    string         `json:"cleanup,omitempty"`
	Readiness  string         `json:"readiness"`
}
type launchRecord struct {
	Version    int            `json:"version"`
	Invocation string         `json:"invocation"`
	Phase      string         `json:"phase"`
	Target     string         `json:"target"`
	Socket     string         `json:"socket"`
	Workspace  string         `json:"workspace"`
	Server     *StatusServer  `json:"server"`
	SessionID  string         `json:"session_id"`
	WindowID   string         `json:"window_id,omitempty"`
	WindowMode bool           `json:"window_mode,omitempty"`
	Panes      []LaunchedPane `json:"panes"`
	Error      string         `json:"error,omitempty"`
	Cleanup    string         `json:"cleanup,omitempty"`
}

func DefaultLaunchSession(root, preset string) string {
	sum := sha256.Sum256([]byte(root))
	return "postman-" + preset + "-" + hex.EncodeToString(sum[:5])
}

// InvokingLaunchSession resolves only a native pane on the selected TMUX server.
// The pane ID is a target, not evidence until the server returns it unchanged.
func InvokingLaunchSession(ctx context.Context, server ServerContext, pane string) (string, string, error) {
	if !nativeID(pane, '%') {
		return "", "", errors.New("invalid TMUX_PANE")
	}
	if current, err := launchServer(ctx, server); err != nil || current == nil {
		return "", "", ErrUnknown
	}
	out, err := tmuxOutput(ctx, server.Socket, "display-message", "-p", "-t", pane, "#{session_id}\t#{session_name}\t#{pane_id}")
	if err != nil {
		return "", "", err
	}
	fields := strings.Split(strings.TrimSuffix(out, "\n"), "\t")
	if len(fields) != 3 || !nativeID(fields[0], '$') || fields[1] == "" || len(fields[1]) > 128 || strings.ContainsAny(fields[1], "\x00\r\n") || fields[2] != pane {
		return "", "", ErrUnknown
	}
	return fields[0], fields[1], nil
}

func (s Store) launchDir(root string) (*os.Root, error) {
	if !filepath.IsAbs(s.StateRoot) {
		return nil, errors.New("launch state root must be absolute")
	}
	name, err := Name(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.StateRoot, 0o700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(s.StateRoot)
	if err != nil {
		return nil, err
	}
	if !stateRootInfo(info) {
		return nil, errors.New("unsafe launch state root")
	}
	base, err := os.OpenRoot(s.StateRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = base.Close() }()
	opened, err := base.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return nil, ErrUnknown
	}
	ns, err := privateDir(base, "%launch", true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = ns.Close() }()
	return privateDir(ns, name, true)
}

func acquireLaunchLock(dir *os.Root) (*os.File, error) {
	f, err := dir.OpenFile("setup.lock", os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if errors.Is(err, os.ErrExist) {
		f, err = dir.OpenFile("setup.lock", os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	}
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !privateInfo(info, false) {
		_ = f.Close()
		return nil, errors.New("unsafe launch setup lock")
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: another launch for this workspace is in progress", ErrBusy)
		}
		return nil, err
	}
	named, err := dir.Lstat("setup.lock")
	if err != nil || !os.SameFile(info, named) {
		_ = f.Close()
		return nil, ErrUnknown
	}
	return f, nil
}

func launchServer(ctx context.Context, server ServerContext) (*StatusServer, error) {
	out, err := tmuxOutput(ctx, server.Socket, "display-message", "-p", "#{socket_path}\t#{pid}\t#{start_time}")
	if err != nil {
		if _, statErr := os.Lstat(server.Socket); errors.Is(statErr, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("selected socket exists but its server cannot be verified: %w", err)
	}
	fields := strings.Split(strings.TrimSuffix(out, "\n"), "\t")
	if len(fields) != 3 {
		return nil, ErrUnknown
	}
	canonical, incarnation, err := serverIdentity(server.Socket, fields[0], fields[1], fields[2])
	if err != nil {
		return nil, err
	}
	pid, _ := strconv.Atoi(fields[1])
	if server.PID != 0 && pid != server.PID {
		return nil, errors.New("explicit server PID disagrees with native server")
	}
	return &StatusServer{Socket: server.Socket, CanonicalSocket: canonical, PID: pid, Incarnation: incarnation}, nil
}

func launchTarget(ctx context.Context, socket, name string) (string, error) {
	out, err := tmuxOutput(ctx, socket, "list-sessions", "-F", "#{session_id}\t#{session_name}")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 2 || !nativeID(f[0], '$') {
			return "", ErrUnknown
		}
		if f[1] == name {
			return f[0], nil
		}
	}
	return "", nil
}

func (s Store) checkLaunchOwner(ctx context.Context, rt Tmux, root string) error {
	name, err := Name(root)
	if err != nil {
		return err
	}
	// Only existing metadata is read. No invented caller or mail admission.
	base, err := os.OpenRoot(s.StateRoot)
	if err != nil {
		return err
	}
	defer func() { _ = base.Close() }()
	ws, err := privateDir(base, name, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = ws.Close() }()
	var identity workspaceRecord
	if readJSON(ws, "workspace.json", &identity) != nil || identity != (workspaceRecord{1, root, name}) {
		return ErrUnknown
	}
	runtime, err := privateDir(ws, "runtime", false)
	if err != nil {
		return err
	}
	defer func() { _ = runtime.Close() }()
	var previous runtimeRecord
	if readJSON(runtime, "binding.json", &previous) != nil || previous.Version != 1 || !previous.Owner.valid() || ValidateBindings(previous.Bindings) != nil {
		return ErrUnknown
	}
	state, err := rt.Probe(ctx, previous.Owner)
	if err != nil {
		return err
	}
	if state != Dead {
		return errors.New("workspace has a live or unverified owner; launch creates no duplicate team")
	}
	return nil
}

func launchShell(ctx context.Context, plan LaunchPlan, server *StatusServer) (string, error) {
	shell := plan.Shell
	if server != nil {
		out, err := tmuxOutput(ctx, plan.Socket, "show-options", "-gv", "default-shell")
		if err != nil {
			return "", err
		}
		shell = strings.TrimSuffix(out, "\n")
	}
	if shell == "" {
		shell = "/bin/sh"
	}
	if err := validateShell(shell); err != nil {
		return "", fmt.Errorf("launch-agents shell: %w", err)
	}
	return shell, nil
}

func verifyLaunchSession(ctx context.Context, record launchRecord) (bool, error) {
	if record.Server == nil || !nativeID(record.SessionID, '$') {
		return false, ErrUnknown
	}
	current, err := launchServer(ctx, ServerContext{Socket: record.Socket, PID: record.Server.PID})
	if err != nil {
		return false, err
	}
	if current == nil {
		return false, nil
	}
	if *current != *record.Server {
		return false, ErrUnknown
	}
	native, err := tmuxOutput(ctx, record.Socket, "list-sessions", "-F", "#{session_id}")
	if err != nil {
		return false, err
	}
	found := false
	for _, id := range strings.Split(strings.TrimSuffix(native, "\n"), "\n") {
		if !nativeID(id, '$') {
			return false, ErrUnknown
		}
		if id == record.SessionID {
			found = true
		}
	}
	if !found {
		return false, nil
	}
	id := record.SessionID
	out, err := tmuxOutput(ctx, record.Socket, "show-environment", "-t", id, launchOwnerEnvironment)
	if err != nil {
		return false, err
	}
	if strings.TrimSuffix(out, "\n") != launchOwnerEnvironment+"="+record.Invocation {
		return false, ErrUnknown
	}
	return true, nil
}

func verifyLaunchWindow(ctx context.Context, record launchRecord) (bool, error) {
	if record.Server == nil || !nativeID(record.SessionID, '$') || !nativeID(record.WindowID, '@') {
		return false, ErrUnknown
	}
	current, err := launchServer(ctx, ServerContext{Socket: record.Socket, PID: record.Server.PID})
	if err != nil {
		return false, err
	}
	if current == nil {
		return false, nil
	}
	if *current != *record.Server {
		return false, ErrUnknown
	}
	out, err := tmuxOutput(ctx, record.Socket, "list-windows", "-t", record.SessionID, "-F", "#{window_id}\t#{window_name}")
	if err != nil {
		return false, ErrUnknown
	}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 2 || !nativeID(fields[0], '@') {
			return false, ErrUnknown
		}
		if fields[0] != record.WindowID {
			continue
		}
		if fields[1] == record.Invocation {
			return true, nil // creation response was lost before the option was set
		}
		owner, err := tmuxOutput(ctx, record.Socket, "show-option", "-w", "-v", "-t", record.WindowID, launchWindowOwnerOption)
		if err != nil || strings.TrimSuffix(owner, "\n") != record.Invocation {
			return false, ErrUnknown
		}
		return true, nil
	}
	return false, nil
}

func cleanupLaunch(record launchRecord) string {
	if record.SessionID == "" {
		return "no verified session acquired"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if record.WindowMode {
		exists, err := verifyLaunchWindow(ctx, record)
		if err != nil {
			return "window ownership unavailable; created resources require inspection"
		}
		if !exists {
			return "owned window already absent"
		}
		if _, err := tmuxOutput(ctx, record.Socket, "kill-window", "-t", record.WindowID); err != nil {
			return "owned window cleanup failed: " + err.Error()
		}
		exists, err = verifyLaunchWindow(ctx, record)
		if err != nil || exists {
			return "window removal requested; absence unverified"
		}
		return "owned window removed"
	}
	exists, err := verifyLaunchSession(ctx, record)
	if err != nil {
		return "ownership unavailable; created resources require inspection"
	}
	if !exists {
		return "owned session already absent"
	}
	if _, err := tmuxOutput(ctx, record.Socket, "kill-session", "-t", record.SessionID); err != nil {
		return "owned session cleanup failed: " + err.Error()
	}
	// Only the owned session is destroyed, never a borrowed server/sentinel.
	if processLiveness(record.Server.PID) == Dead {
		return "owned session removed"
	}
	server, err := launchServer(ctx, ServerContext{Socket: record.Socket})
	if server == nil && err == nil {
		return "owned session removed"
	}
	if err != nil {
		return "session removal requested; server absence could not be verified"
	}
	id, err := launchTarget(ctx, record.Socket, record.Target)
	if err != nil || id != "" {
		return "session removal requested; absence unverified"
	}
	return "owned session removed"
}

// LaunchAgents is an explicit one-shot operation. It creates an owned window
// in a verified invoking session or a dedicated owned session, registers real
// native bindings before commands, and exits.
// The command strings are executable user input, never message/role content.
func (s Store) LaunchAgents(parent context.Context, plan LaunchPlan) (result LaunchResult, retErr error) {
	result = LaunchResult{Session: plan.Session, Workspace: plan.Workspace, Panes: []LaunchedPane{}, Readiness: "not established; created processes are not authenticated or ready agents"}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	if !filepath.IsAbs(plan.CWD) || !filepath.IsAbs(plan.Config) || !filepath.IsAbs(plan.Socket) || len(plan.Columns) == 0 {
		return result, errors.New("invalid complete launch plan")
	}
	if plan.WindowSession == "" && !validNativeSessionName(plan.Session) || plan.WindowSession != "" && (!nativeID(plan.WindowSession, '$') || plan.Session == "" || len(plan.Session) > 128 || strings.ContainsAny(plan.Session, "\x00\r\n")) {
		return result, errors.New("invalid complete launch plan")
	}
	if plan.BuiltinConfig {
		fresh, err := (config.Source{Path: plan.Config, Builtin: true}).Load(ctx)
		if err != nil {
			return result, err
		}
		if StatusConfigFingerprint(fresh) != StatusConfigFingerprint(plan.Topology) {
			return result, ErrUnknown
		}
	}
	root, err := CanonicalRoot(plan.CWD, plan.Home)
	if err != nil || root != plan.Workspace {
		return result, errors.New("launch cwd must match its canonical workspace")
	}
	rt := Tmux{Socket: plan.Socket, Home: plan.Home, Roles: plan.Topology.Nodes, ConfigPath: plan.Config, BuiltinConfig: plan.BuiltinConfig, ConfigFingerprint: StatusConfigFingerprint(plan.Topology)}
	server, err := launchServer(ctx, ServerContext{Socket: plan.Socket, PID: plan.ExpectedPID})
	if err != nil {
		return result, err
	}
	if plan.WindowSession != "" {
		if server == nil {
			return result, errors.New("invoking tmux session requires a verified server")
		}
		name, err := tmuxOutput(ctx, plan.Socket, "display-message", "-p", "-t", plan.WindowSession, "#{session_name}")
		if err != nil || strings.TrimSuffix(name, "\n") != plan.Session {
			return result, errors.New("invoking tmux session changed before launch")
		}
	}
	shell, err := launchShell(ctx, plan, server)
	if err != nil {
		return result, err
	}
	sleeper, err := exec.LookPath("sleep")
	if err != nil {
		return result, errors.New("launch setup requires the normal sleep executable")
	}
	dir, err := s.launchDir(root)
	if err != nil {
		return result, err
	}
	defer func() { _ = dir.Close() }()
	lock, err := acquireLaunchLock(dir)
	if err != nil {
		return result, err
	}
	defer func() { _ = lock.Close() }()
	keySource := plan.Socket + "\x00" + plan.Session
	if plan.WindowSession != "" {
		keySource += "\x00" + plan.WindowSession
	}
	key := statusKey(keySource) + ".json"
	var previous launchRecord
	if err := readJSON(dir, key, &previous); err == nil {
		if previous.Version != 1 || previous.Invocation == "" || previous.Target != plan.Session || previous.Socket != plan.Socket || previous.Workspace != root || previous.WindowMode != (plan.WindowSession != "") {
			return result, errors.New("invalid prior launch ownership record")
		}
		for _, pane := range previous.Panes {
			if pane.Started {
				return result, errors.New("this target has prior command starts; inspect its outcome instead of replaying")
			}
		}

		if previous.Phase != "failed_before_start" || previous.Cleanup != map[bool]string{false: "owned session removed", true: "owned window removed"}[previous.WindowMode] {
			return result, errors.New("this launch target has prior or uncertain command starts; inspect its recorded outcome instead of replaying")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("launch ownership record unavailable: %w", err)
	}
	if err := s.checkLaunchOwner(ctx, rt, root); err != nil {
		return result, err
	}
	if server != nil {
		if plan.WindowSession == "" {
			id, err := launchTarget(ctx, plan.Socket, plan.Session)
			if err != nil {
				return result, err
			}
			if id != "" {
				return result, errors.New("target session already exists; no panes or commands were changed")
			}
		}
		native, err := rt.enumerateStatus(ctx, ServerContext{Socket: plan.Socket, PID: server.PID})
		if err != nil {
			return result, err
		}
		roles := map[string]bool{}
		for _, role := range plan.Topology.Nodes {
			roles[role] = true
		}
		for _, session := range native.Sessions {
			for _, window := range session.Windows {
				for _, pane := range window.Panes {
					if pane.Workspace == root && !pane.Dead && roles[pane.Title] {
						return result, errors.New("workspace already has a configured native role pane; no duplicate team created")
					}
				}
			}
		}
	}
	invocation, err := randomName()
	if err != nil {
		return result, err
	}
	invocation = "launch-" + strings.TrimPrefix(invocation, ".new-")
	result.Invocation = invocation
	record := launchRecord{Version: 1, Invocation: invocation, Phase: "creating", Target: plan.Session, Socket: plan.Socket, Workspace: root, Server: server, WindowMode: plan.WindowSession != "", Panes: []LaunchedPane{}}
	if record.WindowMode {
		record.SessionID = plan.WindowSession
		result.SessionID = plan.WindowSession
	}
	if err := replaceJSON(dir, key, record); err != nil {
		return result, err
	}
	starting := false
	defer func() {
		if retErr != nil && (record.SessionID == "" || record.WindowMode && record.WindowID == "" || record.Server == nil) {
			recovered := record
			if record.WindowMode {
				recovered = recoverLaunchWindow(record)
			} else {
				recovered = recoverLaunchSession(record)
			}
			if recovered.SessionID != "" {
				record = recovered
				result.SessionID = record.SessionID
				result.Server = record.Server
				result.Window = record.WindowID
			}
		}
		if retErr != nil {
			record.Error = retErr.Error()
			record.Cleanup = cleanupLaunch(record)
			result.Cleanup = record.Cleanup
			record.Phase = "failed_before_start"
			if starting {
				record.Phase = "failed_after_start"
			}
		}
		record.Panes = result.Panes
		record.Server = result.Server
		record.SessionID = result.SessionID
		record.WindowID = result.Window
		finalFailure := func(err error) {
			retErr = errors.Join(retErr, err)
			if result.Cleanup == "" {
				record.Cleanup = cleanupLaunch(record)
				result.Cleanup = record.Cleanup
			}
			record.Error = retErr.Error()
			record.Phase = "failed_before_start"
			if starting {
				record.Phase = "failed_after_start"
			}
		}
		if err := replaceJSON(dir, key, record); err != nil {
			finalFailure(fmt.Errorf("launch outcome persistence failed; starts may be uncertain: %w", err))
		}
		if err := createObservation(dir, invocation+".json", record); err != nil {
			finalFailure(fmt.Errorf("launch outcome history failed: %w", err))
		}
		if retErr != nil {
			if err := replaceJSON(dir, key, record); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("cleanup outcome persistence also failed: %w", err))
			}
		}
	}()
	maxRows := 0
	for _, column := range plan.Columns {
		if len(column.Panes) > maxRows {
			maxRows = len(column.Panes)
		}
	}
	height := max(48, maxRows*4)
	width := max(160, len(plan.Columns)*20)
	var out string
	if record.WindowMode {
		out, err = tmuxOutput(ctx, plan.Socket, "new-window", "-d", "-t", plan.WindowSession, "-n", invocation, "-c", plan.CWD, "-e", launchConfigEnvironment(plan), "-e", launchXDGEnvironment(plan), "-e", launchOwnerEnvironment+"="+invocation, "-P", "-F", "#{session_id}\t#{window_id}\t#{pane_id}", sleeper, "86400")
	} else {
		out, err = tmuxOutput(ctx, plan.Socket, "new-session", "-d", "-s", plan.Session, "-n", plan.Preset, "-c", plan.CWD, "-x", strconv.Itoa(width), "-y", strconv.Itoa(height), "-e", launchConfigEnvironment(plan), "-e", launchXDGEnvironment(plan), "-e", launchOwnerEnvironment+"="+invocation, "-P", "-F", "#{session_id}\t#{window_id}\t#{pane_id}", sleeper, "86400")
	}
	if err != nil {
		return result, fmt.Errorf("launch target creation failed; no launch commands dispatched: %w", err)
	}
	fields := strings.Split(strings.TrimSuffix(out, "\n"), "\t")
	if len(fields) != 3 || !nativeID(fields[0], '$') || !nativeID(fields[1], '@') || !nativeID(fields[2], '%') {
		return result, errors.New("created session identity unavailable; do not blindly repeat launch")
	}
	result.SessionID, result.Window = fields[0], fields[1]
	record.SessionID = fields[0]
	record.WindowID = fields[1]
	if record.WindowMode && fields[0] != plan.WindowSession {
		return result, errors.New("created window belongs to an unexpected session")
	}
	current, err := launchServer(ctx, ServerContext{Socket: plan.Socket})
	if err != nil || current == nil {
		return result, errors.New("created server identity unavailable")
	}
	if server != nil && *server != *current {
		return result, errors.New("native server changed during launch")
	}
	result.Server = current
	record.Server = current
	if err := replaceJSON(dir, key, record); err != nil {
		return result, err
	}
	if record.WindowMode {
		if _, err := tmuxOutput(ctx, plan.Socket, "set-option", "-w", "-t", result.Window, launchWindowOwnerOption, invocation); err != nil {
			return result, err
		}
		if _, err := tmuxOutput(ctx, plan.Socket, "set-option", "-w", "-t", result.Window, "automatic-rename", "off"); err != nil {
			return result, err
		}
		if _, err := tmuxOutput(ctx, plan.Socket, "rename-window", "-t", result.Window, plan.Preset); err != nil {
			return result, err
		}
	} else {
		for _, option := range [][2]string{{"default-shell", shell}, {"automatic-rename", "off"}} {
			if _, err := tmuxOutput(ctx, plan.Socket, "set-option", "-t", result.SessionID, option[0], option[1]); err != nil {
				return result, err
			}
		}
	}
	anchors := []string{fields[2]}
	for range len(plan.Columns) - 1 {
		pane, err := tmuxOutput(ctx, plan.Socket, "split-window", "-h", "-d", "-t", anchors[len(anchors)-1], "-c", plan.CWD, "-P", "-F", "#{pane_id}", sleeper, "86400")
		if err != nil {
			return result, fmt.Errorf("create column: %w", err)
		}
		pane = strings.TrimSuffix(pane, "\n")
		if !nativeID(pane, '%') {
			return result, ErrUnknown
		}
		anchors = append(anchors, pane)
		// Redistribute after each split; repeatedly halving the last column
		// otherwise runs out of room before an eight-column preset is built.
		if _, err := tmuxOutput(ctx, plan.Socket, "select-layout", "-t", result.Window, "even-horizontal"); err != nil {
			return result, err
		}
	}
	if _, err := tmuxOutput(ctx, plan.Socket, "select-layout", "-t", result.Window, "even-horizontal"); err != nil {
		return result, err
	}
	for ci, column := range plan.Columns {
		target := anchors[ci]
		for pi, item := range column.Panes {
			if pi > 0 {
				remaining := len(column.Panes) - pi
				pane, err := tmuxOutput(ctx, plan.Socket, "split-window", "-v", "-d", "-t", target, "-c", plan.CWD, "-l", strconv.Itoa(100*remaining/(remaining+1))+"%", "-P", "-F", "#{pane_id}", sleeper, "86400")
				if err != nil {
					return result, fmt.Errorf("create role %s: %w", item.Role, err)
				}
				target = strings.TrimSuffix(pane, "\n")
				if !nativeID(target, '%') {
					return result, ErrUnknown
				}
			}
			if _, err := tmuxOutput(ctx, plan.Socket, "select-pane", "-t", target, "-T", item.Role); err != nil {
				return result, err
			}
			result.Panes = append(result.Panes, LaunchedPane{Role: item.Role, Pane: target, Column: ci, Row: pi})
		}
	}
	rt.Pane = result.Panes[0].Pane
	if err := s.WithVerified(ctx, rt, func(*os.Root, Snapshot) error { return nil }); err != nil {
		return result, fmt.Errorf("native launch ownership admission failed before commands: %w", err)
	}
	record.Phase = "starting"
	record.Panes = result.Panes
	if err := replaceJSON(dir, key, record); err != nil {
		return result, err
	}
	starting = true
	index := 0
	for _, column := range plan.Columns {
		for _, item := range column.Panes {
			pane := &result.Panes[index]
			// Multiple tmux command arguments execute directly: the exact source is one
			// argument to the user's interactive/login shell, never naively word-split.
			if _, err := tmuxOutput(ctx, plan.Socket, "respawn-pane", "-k", "-t", pane.Pane, "-c", plan.CWD, "-e", launchConfigEnvironment(plan), "-e", launchXDGEnvironment(plan), shell, "-lic", item.Command); err != nil {
				return result, fmt.Errorf("role %s start uncertain: %w", item.Role, err)
			}
			pane.Started = true
			if out, err := tmuxOutput(ctx, plan.Socket, "display-message", "-p", "-t", pane.Pane, "#{pane_pid}"); err == nil {
				pid := strings.TrimSuffix(out, "\n")
				if positivePID(pid) {
					pane.PID = pid
				}
			}
			record.Panes = result.Panes
			if err := replaceJSON(dir, key, record); err != nil {
				return result, err
			}
			index++
		}
	}
	record.Phase = "started"
	// A short-lived configured command may already have exited. No resurrection,
	// authentication/readiness claim, monitoring or automatic retry follows.
	return result, nil
}

// A tmux client may lose its creation response after the server accepted it.
// Recover only a fresh native session carrying this attempt's unpredictable
// creation token. This never borrows a similarly named session or runs commands.
func recoverLaunchSession(record launchRecord) launchRecord {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	server, err := launchServer(ctx, ServerContext{Socket: record.Socket})
	if err != nil || server == nil {
		return record
	}
	if record.Server != nil && *record.Server != *server {
		return record
	}
	out, err := tmuxOutput(ctx, record.Socket, "list-sessions", "-F", "#{session_id}")
	if err != nil {
		return record
	}
	ids := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(ids) > 1024 {
		return record
	}
	for _, id := range ids {
		if !nativeID(id, '$') {
			return record
		}
		env, err := tmuxOutput(ctx, record.Socket, "show-environment", "-t", id, launchOwnerEnvironment)
		if err == nil && strings.TrimSuffix(env, "\n") == launchOwnerEnvironment+"="+record.Invocation {
			record.Server = server
			record.SessionID = id
			return record
		}
	}
	return record
}

func recoverLaunchWindow(record launchRecord) launchRecord {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	server, err := launchServer(ctx, ServerContext{Socket: record.Socket, PID: record.Server.PID})
	if err != nil || server == nil || *server != *record.Server {
		return record
	}
	out, err := tmuxOutput(ctx, record.Socket, "list-windows", "-t", record.SessionID, "-F", "#{window_id}\t#{window_name}")
	if err != nil {
		return record
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) > 1024 {
		return record
	}
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 2 || !nativeID(fields[0], '@') {
			return record
		}
		if fields[1] != record.Invocation {
			continue
		}
		if record.WindowID != "" && record.WindowID != fields[0] {
			return record
		}
		record.WindowID = fields[0]
		return record
	}
	return record
}

func launchConfigEnvironment(plan LaunchPlan) string {
	if plan.BuiltinConfig {
		return LaunchConfigEnvironment + "="
	}
	return LaunchConfigEnvironment + "=" + plan.Config
}

func launchXDGEnvironment(plan LaunchPlan) string {
	if plan.BuiltinConfig {
		return "XDG_CONFIG_HOME=" + filepath.Dir(filepath.Dir(plan.Config))
	}
	return "XDG_CONFIG_HOME=" + os.Getenv("XDG_CONFIG_HOME")
}
