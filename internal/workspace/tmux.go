package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
)

// Tmux verifies only the caller's server and session. Socket and Pane must come
// from the invocation's established tmux context, never from a pane title.
type Tmux struct {
	Socket string
	Pane   string
	Home   string
	Roles  []string
}

// Snapshot is fresh runtime evidence, not a durable registration or a lock.
type Snapshot struct {
	Root     string
	Caller   Binding
	Bindings []Binding
	// AbsentRoles are configured roles proven absent by a complete native pane
	// enumeration. A failed enumeration returns an error instead.
	AbsentRoles []string
}

// Snapshot uses tmux's session creation directory rather than pane_current_path.
// Rechecking both native identity and socket identity rejects an intervening
// server/session replacement. All probes have a shared two-second deadline.
func (t Tmux) Snapshot(ctx context.Context) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if !filepath.IsAbs(t.Socket) || !nativeID(t.Pane, '%') {
		return Snapshot{}, fmt.Errorf("%w: explicit tmux socket and native pane required", ErrUnknown)
	}
	roles := make(map[string]bool, len(t.Roles))
	for _, role := range t.Roles {
		if !binding.ValidateNodeName(role) || roles[role] {
			return Snapshot{}, fmt.Errorf("%w: invalid or duplicate configured role", ErrUnknown)
		}
		roles[role] = true
	}
	first, err := t.describe(ctx, t.Socket, t.Pane)
	if err != nil {
		return Snapshot{}, err
	}
	root, err := CanonicalRoot(first.root, t.Home)
	if err != nil {
		return Snapshot{}, err
	}
	out, err := tmuxOutput(ctx, t.Socket, "list-panes", "-s", "-t", first.owner.Session,
		"-F", "#{session_id}\t#{pane_id}\t#{pane_title}\t#{pane_dead}\t#{pane_pid}")
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: list caller session panes: %v", ErrUnknown, err)
	}
	result := Snapshot{Root: root}
	liveRoles := make(map[string]bool, len(roles))
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 5 || fields[0] != first.owner.Session || !nativeID(fields[1], '%') {
			return Snapshot{}, fmt.Errorf("%w: malformed native pane identity", ErrUnknown)
		}
		if !roles[fields[2]] {
			continue
		}
		if fields[3] == "1" {
			continue
		}
		if fields[3] != "0" {
			return Snapshot{}, fmt.Errorf("%w: malformed pane liveness", ErrUnknown)
		}
		owner := first.owner
		owner.Pane = fields[1]
		if !positivePID(fields[4]) {
			return Snapshot{}, fmt.Errorf("%w: malformed pane process identity", ErrUnknown)
		}
		item := Binding{Role: fields[2], Owner: owner, PanePID: fields[4]}
		result.Bindings = append(result.Bindings, item)
		liveRoles[fields[2]] = true
		if fields[1] == t.Pane {
			result.Caller = item
		}
	}
	if err := ValidateBindings(result.Bindings); err != nil {
		return Snapshot{}, err
	}
	for _, role := range t.Roles {
		if !liveRoles[role] {
			result.AbsentRoles = append(result.AbsentRoles, role)
		}
	}
	if !result.Caller.Owner.valid() {
		return Snapshot{}, fmt.Errorf("%w: caller pane has no unique configured role", ErrUnknown)
	}
	last, err := t.describe(ctx, t.Socket, t.Pane)
	if err != nil || first != last {
		return Snapshot{}, fmt.Errorf("%w: runtime changed during verification", ErrUnknown)
	}
	return result, nil
}

type tmuxDescription struct {
	owner   Owner
	root    string
	role    string
	panePID string
}

func (t Tmux) describe(ctx context.Context, socket, pane string) (tmuxDescription, error) {
	out, err := tmuxOutput(ctx, socket, "display-message", "-p", "-t", pane,
		"#{socket_path}\t#{pid}\t#{start_time}\t#{session_id}\t#{pane_id}\t#{session_path}\t#{pane_dead}\t#{pane_title}\t#{pane_pid}")
	if err != nil {
		return tmuxDescription{}, fmt.Errorf("%w: verify native tmux identity: %v", ErrUnknown, err)
	}
	fields := strings.Split(strings.TrimSuffix(out, "\n"), "\t")
	if len(fields) != 9 || !nativeID(fields[3], '$') || fields[4] != pane || fields[6] != "0" || !positivePID(fields[8]) {
		return tmuxDescription{}, fmt.Errorf("%w: incomplete tmux identity", ErrUnknown)
	}
	server, incarnation, err := serverIdentity(socket, fields[0], fields[1], fields[2])
	if err != nil {
		return tmuxDescription{}, err
	}
	return tmuxDescription{owner: Owner{server, incarnation, fields[3], fields[4]}, root: fields[5], role: fields[7], panePID: fields[8]}, nil
}

func positivePID(value string) bool {
	pid, err := strconv.Atoi(value)
	return err == nil && pid > 0 && strconv.Itoa(pid) == value
}

// serverIdentity combines the canonical socket namespace, server PID/start
// time, and socket device/inode/nanosecond modification time. PID or
// session name alone cannot identify a runtime. Socket ownership is verified.
func serverIdentity(requested, reported, pidText, started string) (string, string, error) {
	pid, pidErr := strconv.Atoi(pidText)
	start, startErr := strconv.ParseInt(started, 10, 64)
	if pidErr != nil || pid <= 0 || startErr != nil || start <= 0 {
		return "", "", ErrUnknown
	}
	socket, err := filepath.EvalSymlinks(requested)
	if err != nil || !filepath.IsAbs(reported) {
		return "", "", ErrUnknown
	}
	actual, err := filepath.EvalSymlinks(reported)
	if err != nil || actual != socket {
		return "", "", ErrUnknown
	}
	info, err := os.Lstat(socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return "", "", ErrUnknown
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return "", "", ErrUnknown
	}
	incarnation := fmt.Sprintf("%d:%d:%d:%d:%d", pid, start, stat.Dev, stat.Ino, info.ModTime().UnixNano())
	return socket, incarnation, nil
}

// Probe distinguishes verified absence from lookup failure. A live session
// retains ownership even when its former role pane has disappeared. A server
// lookup failure is never treated as death while its process may still exist.
func (t Tmux) Probe(ctx context.Context, old Owner) (Liveness, error) {
	if !old.valid() || !filepath.IsAbs(old.Server) {
		return Unknown, ErrUnknown
	}
	parts := strings.Split(old.Incarnation, ":")
	if len(parts) != 5 {
		return Unknown, ErrUnknown
	}
	for _, part := range parts {
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil || strconv.FormatUint(value, 10) != part {
			return Unknown, ErrUnknown
		}
	}
	pid, err := strconv.Atoi(parts[0])
	if err != nil || pid <= 0 {
		return Unknown, ErrUnknown
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, probeErr := tmuxOutput(ctx, old.Server, "display-message", "-p",
		"#{socket_path}\t#{pid}\t#{start_time}")
	if probeErr != nil {
		if processLiveness(pid) == Dead {
			return Dead, nil
		}
		return Unknown, fmt.Errorf("%w: cannot verify previous server", ErrUnknown)
	}
	fields := strings.Split(strings.TrimSuffix(out, "\n"), "\t")
	if len(fields) != 3 {
		return Unknown, ErrUnknown
	}
	server, incarnation, err := serverIdentity(old.Server, fields[0], fields[1], fields[2])
	if err != nil {
		return Unknown, err
	}
	if server != old.Server || incarnation != old.Incarnation {
		// A different start time with the same PID proves PID reuse. With a
		// different PID, require proof that the old process actually exited.
		if fields[1] == parts[0] && fields[2] != parts[1] || processLiveness(pid) == Dead {
			return Dead, nil
		}
		return Unknown, fmt.Errorf("%w: server incarnation differs but old process may exist", ErrUnknown)
	}
	out, err = tmuxOutput(ctx, old.Server, "list-sessions", "-F", "#{session_id}")
	if err != nil {
		return Unknown, fmt.Errorf("%w: cannot verify previous session", ErrUnknown)
	}
	for _, session := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if !nativeID(session, '$') {
			return Unknown, ErrUnknown
		}
		if session == old.Session {
			return Alive, nil
		}
	}
	return Dead, nil
}

// CapturePane fails closed until role registration is stable across both pane
// capture and every role rebind. Pane titles and pane PIDs do not provide that
// guarantee: a retitle and restoration can occur between endpoint probes.
// A caller-selected capture must never return potentially unrelated pane text.
func (t Tmux) CapturePane(ctx context.Context, selected Binding) (string, error) {
	return "", fmt.Errorf("%w: native pane capture requires stable role registration", ErrUnknown)
}

func processLiveness(pid int) Liveness {
	if pid <= 0 {
		return Unknown
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return Unknown
	}
	defer func() { _ = proc.Release() }()
	err = proc.Signal(syscall.Signal(0))
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return Alive
	case errors.Is(err, syscall.ESRCH), errors.Is(err, os.ErrProcessDone):
		return Dead
	default:
		return Unknown
	}
}

// Refuse oversized output rather than silently validating a truncated identity.
type tmuxBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *tmuxBuffer) Write(p []byte) (int, error) {
	limit := b.limit
	if limit == 0 {
		limit = 64 * 1024
	}
	if b.Len()+len(p) > limit {
		b.exceeded = true
		return 0, errors.New("tmux identity output exceeds limit")
	}
	return b.Buffer.Write(p)
}

func tmuxOutput(ctx context.Context, socket string, args ...string) (string, error) {
	return tmuxOutputLimited(ctx, 64*1024, socket, args...)
}

func tmuxOutputLimited(ctx context.Context, limit int, socket string, args ...string) (string, error) {
	// Preserve machine-readable tabs and UTF-8 paths independently of locale.
	cmd := exec.CommandContext(ctx, "tmux", append([]string{"-u", "-S", socket}, args...)...)
	cmd.WaitDelay = 100 * time.Millisecond
	stdout, stderr := tmuxBuffer{limit: limit}, tmuxBuffer{limit: 8 * 1024}
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	if stdout.exceeded || stderr.exceeded || stdout.Len() >= limit {
		return "", errors.New("tmux identity output exceeds limit")
	}
	return stdout.String(), nil
}
