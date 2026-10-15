package testfixture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// RealTmux owns one private tmux server and the processes started through it.
// It is test infrastructure, not a product session launcher.
type RealTmux struct {
	mu         sync.Mutex
	closeMu    sync.Mutex
	tb         testing.TB
	root       string
	rootInfo   os.FileInfo
	socket     string
	configPath string
	serverPID  int
	serverMade bool
	closing    bool
	closed     bool
	nextOpID   uint64
	operations map[uint64]*tmuxOperation
	beforeExec func(context.Context)
	sessions   []TmuxSession
	processes  []*fixtureProcess
}

type tmuxOperation struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type TmuxSession struct {
	Name   string
	ID     string
	PaneID string
}

type fixtureProcess struct {
	name   string
	cmd    *exec.Cmd
	done   chan error
	exited bool
}

// NewRealTmux registers cleanup before any fallible setup can leave a server.
// tmux is a required prerequisite for tests using this fixture.
func NewRealTmux(tb testing.TB) (*RealTmux, error) {
	return newRealTmux(tb, realTmuxHooks{})
}

type realTmuxHooks struct {
	afterStart   func(*RealTmux) error
	recordServer func(context.Context, *RealTmux) error
	afterServer  func() error
}

func newRealTmux(tb testing.TB, hooks realTmuxHooks) (*RealTmux, error) {
	tb.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil, fmt.Errorf("real tmux fixture requires tmux: %w", err)
	}
	root, err := os.MkdirTemp("", "p878-")
	if err != nil {
		return nil, err
	}
	f := &RealTmux{
		tb:         tb,
		root:       root,
		socket:     filepath.Join(root, "tmux.sock"),
		configPath: filepath.Join(root, "postman.toml"),
	}
	tb.Cleanup(func() {
		if err := f.Close(); err != nil {
			tb.Errorf("real tmux fixture cleanup: %v", err)
		}
	})
	f.rootInfo, err = os.Lstat(root)
	if err != nil {
		return f, err
	}
	for _, dir := range []string{"home", "postman", "config", "state", "cache"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o700); err != nil {
			return f, err
		}
	}
	if err := os.WriteFile(f.configPath, []byte("# fixed fixture configuration\n"), 0o600); err != nil {
		return f, err
	}
	f.serverMade = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := f.tmux(ctx, "-f", "/dev/null", "new-session", "-d", "-s", "fixture"); err != nil {
		return f, err
	}
	if hooks.afterStart != nil {
		if err := hooks.afterStart(f); err != nil {
			return f, err
		}
	}
	recordServer := f.recordServer
	if hooks.recordServer != nil {
		recordServer = func(ctx context.Context) error { return hooks.recordServer(ctx, f) }
	}
	if err := recordServer(ctx); err != nil {
		return f, err
	}
	if hooks.afterServer != nil {
		if err := hooks.afterServer(); err != nil {
			return f, err
		}
	}
	if _, err := f.recordSession(ctx, "fixture"); err != nil {
		return f, err
	}
	return f, nil
}

func (f *RealTmux) Root() string       { return f.root }
func (f *RealTmux) Socket() string     { return f.socket }
func (f *RealTmux) ConfigPath() string { return f.configPath }

// Env is a subprocess-local environment. Neither this fixture nor its tests
// change the caller's HOME, tmux socket, or XDG variables.
func (f *RealTmux) Env() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.envLocked()
}

// envLocked builds a child environment while the resource ledger is locked.
func (f *RealTmux) envLocked() []string {
	blocked := map[string]bool{
		"HOME": true, "POSTMAN_HOME": true, "XDG_CONFIG_HOME": true,
		"XDG_STATE_HOME": true, "XDG_CACHE_HOME": true,
		"TMUX": true, "TMUX_PANE": true, "TMUX_TMPDIR": true,
	}
	env := make([]string, 0, len(os.Environ())+8)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !blocked[key] {
			env = append(env, entry)
		}
	}
	env = append(env,
		"HOME="+filepath.Join(f.root, "home"),
		"POSTMAN_HOME="+filepath.Join(f.root, "postman"),
		"XDG_CONFIG_HOME="+filepath.Join(f.root, "config"),
		"XDG_STATE_HOME="+filepath.Join(f.root, "state"),
		"XDG_CACHE_HOME="+filepath.Join(f.root, "cache"),
	)
	if f.serverPID > 0 {
		env = append(env, fmt.Sprintf("TMUX=%s,%d,0", f.socket, f.serverPID))
	}
	if len(f.sessions) > 0 {
		env = append(env, "TMUX_PANE="+f.sessions[0].PaneID)
	}
	return env
}

// Tmux always selects the private socket, even when the caller is inside an
// unrelated live tmux session.
func (f *RealTmux) Tmux(ctx context.Context, args ...string) (string, error) {
	return f.tmux(ctx, args...)
}

func (f *RealTmux) tmux(ctx context.Context, args ...string) (string, error) {
	f.mu.Lock()
	if f.closing || f.closed {
		f.mu.Unlock()
		return "", errors.New("real tmux fixture is closing")
	}
	opCtx, cancel := context.WithCancel(ctx)
	f.nextOpID++
	opID := f.nextOpID
	op := &tmuxOperation{cancel: cancel, done: make(chan struct{})}
	if f.operations == nil {
		f.operations = make(map[uint64]*tmuxOperation)
	}
	f.operations[opID] = op
	env := f.envLocked()
	beforeExec := f.beforeExec
	f.mu.Unlock()
	defer func() {
		cancel()
		f.mu.Lock()
		delete(f.operations, opID)
		close(op.done)
		f.mu.Unlock()
	}()
	if beforeExec != nil {
		beforeExec(opCtx)
	}
	if err := opCtx.Err(); err != nil {
		return "", fmt.Errorf("tmux %q: %w", args, err)
	}
	cmd := exec.CommandContext(opCtx, "tmux", append([]string{"-S", f.socket}, args...)...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("tmux %q: %w: %s", args, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (f *RealTmux) recordServer(ctx context.Context) error {
	out, err := f.tmux(ctx, "display-message", "-p", "#{pid}")
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || pid <= 0 {
		return fmt.Errorf("invalid fixture server pid %q", out)
	}
	f.mu.Lock()
	f.serverPID = pid
	f.mu.Unlock()
	return nil
}

func (f *RealTmux) recordSession(ctx context.Context, name string) (TmuxSession, error) {
	out, err := f.tmux(ctx, "display-message", "-p", "-t", name, "#{session_id}\t#{pane_id}")
	if err != nil {
		return TmuxSession{}, err
	}
	parts := strings.Split(strings.TrimSpace(out), "\t")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return TmuxSession{}, fmt.Errorf("invalid fixture session identity %q", out)
	}
	session := TmuxSession{Name: name, ID: parts[0], PaneID: parts[1]}
	f.mu.Lock()
	f.sessions = append(f.sessions, session)
	f.mu.Unlock()
	return session, nil
}

func (f *RealTmux) NewSession(ctx context.Context, name string) (TmuxSession, error) {
	if name == "" || strings.HasPrefix(name, "-") {
		return TmuxSession{}, fmt.Errorf("invalid fixture session name %q", name)
	}
	if _, err := f.tmux(ctx, "new-session", "-d", "-s", name); err != nil {
		return TmuxSession{}, err
	}
	return f.recordSession(ctx, name)
}

// StartHelper owns a direct child process. Callers must not Wait on it: Close
// reaps it, including on scenario failure.
func (f *RealTmux) StartHelper(name string, command string, args ...string) (int, error) {
	return f.startHelper(name, command, nil, args...)
}

// beforeRegister is a test seam for the successful-start registration window.
func (f *RealTmux) startHelper(name string, command string, beforeRegister func(int), args ...string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closing || f.closed {
		return 0, errors.New("real tmux fixture is closing")
	}
	cmd := exec.Command(command, args...)
	cmd.Env = f.envLocked()
	cmd.Dir = f.root
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	p := &fixtureProcess{name: name, cmd: cmd, done: make(chan error, 1)}
	go func() { p.done <- cmd.Wait() }()
	if beforeRegister != nil {
		beforeRegister(cmd.Process.Pid)
	}
	f.processes = append(f.processes, p)
	return cmd.Process.Pid, nil
}

type TmuxResources struct {
	Root      string
	Socket    string
	ServerPID int
	Sessions  []TmuxSession
	Helpers   []int
}

func (f *RealTmux) Resources() TmuxResources {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := TmuxResources{Root: f.root, Socket: f.socket, ServerPID: f.serverPID}
	r.Sessions = append(r.Sessions, f.sessions...)
	for _, p := range f.processes {
		r.Helpers = append(r.Helpers, p.cmd.Process.Pid)
	}
	return r
}

// Close is idempotent after success. A failed cleanup retains ownership
// evidence and can be retried; it never removes a possibly live server socket.
func (f *RealTmux) Close() error {
	f.closeMu.Lock()
	defer f.closeMu.Unlock()
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closing = true
	operations := make([]*tmuxOperation, 0, len(f.operations))
	for _, op := range f.operations {
		operations = append(operations, op)
		op.cancel()
	}
	f.mu.Unlock()
	if len(operations) > 0 {
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		for _, op := range operations {
			select {
			case <-op.done:
			case <-timer.C:
				return fmt.Errorf("timed out draining %d admitted tmux operations", len(operations))
			}
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var problems []error
	for _, p := range f.processes {
		if err := stopFixtureProcess(p); err != nil {
			problems = append(problems, fmt.Errorf("helper %s: %w", p.name, err))
		}
	}
	if f.serverMade {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		cmd := exec.CommandContext(ctx, "tmux", "-S", f.socket, "display-message", "-p", "#{pid}")
		cmd.Env = os.Environ()
		out, err := cmd.Output()
		cancel()
		if err == nil {
			got, parseErr := strconv.Atoi(strings.TrimSpace(string(out)))
			owned := false
			switch {
			case parseErr != nil || got <= 0:
				problems = append(problems, fmt.Errorf("invalid server identity %q", out))
			case f.serverPID == 0:
				if adoptErr := f.canAdoptServer(); adoptErr != nil {
					problems = append(problems, adoptErr)
				} else {
					f.serverPID = got
					owned = true
				}
			case got != f.serverPID:
				problems = append(problems, fmt.Errorf("server identity mismatch: got %q, recorded %d", out, f.serverPID))
			default:
				owned = true
			}
			if owned {
				killCtx, killCancel := context.WithTimeout(context.Background(), 3*time.Second)
				kill := exec.CommandContext(killCtx, "tmux", "-S", f.socket, "kill-server")
				if output, killErr := kill.CombinedOutput(); killErr != nil {
					problems = append(problems, fmt.Errorf("kill owned server: %w: %s", killErr, output))
				}
				killCancel()
			}
		} else if f.serverPID == 0 {
			// Setup failed before a server identity was recorded. A private
			// socket may still name a live server, so leave it for inspection.
			if _, statErr := os.Stat(f.socket); statErr == nil {
				problems = append(problems, fmt.Errorf("unverified server may remain at %s", f.socket))
			}
		}
		if f.serverPID > 0 && !waitPIDGone(f.serverPID, 3*time.Second) {
			problems = append(problems, fmt.Errorf("owned server pid %d still alive", f.serverPID))
		}
	}
	if len(problems) > 0 {
		return errors.Join(problems...)
	}
	if err := os.RemoveAll(f.root); err != nil {
		return err
	}
	f.closed = true
	return nil
}

// A server that started before PID recording can be adopted only through the
// original socket in the same private directory created for this fixture.
func (f *RealTmux) canAdoptServer() error {
	if filepath.Dir(f.socket) != f.root || f.rootInfo == nil {
		return fmt.Errorf("fixture socket ownership is unverified")
	}
	rootInfo, err := os.Lstat(f.root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode().Perm() != 0o700 || !os.SameFile(f.rootInfo, rootInfo) {
		return fmt.Errorf("fixture root ownership is unverified: %v", err)
	}
	socketInfo, err := os.Lstat(f.socket)
	if err != nil || socketInfo.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("fixture socket ownership is unverified: %v", err)
	}
	return nil
}

func stopFixtureProcess(p *fixtureProcess) error {
	if p.exited {
		return nil
	}
	select {
	case <-p.done:
		p.exited = true
		return nil
	default:
	}
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-p.done:
		p.exited = true
		return nil
	case <-time.After(2 * time.Second):
	}
	if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-p.done:
		p.exited = true
		return nil
	case <-time.After(2 * time.Second):
		return fmt.Errorf("process %d did not exit", p.cmd.Process.Pid)
	}
}

func waitPIDGone(pid int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for {
		proc, err := os.FindProcess(pid)
		if err == nil {
			err = proc.Signal(syscall.Signal(0))
		}
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}
