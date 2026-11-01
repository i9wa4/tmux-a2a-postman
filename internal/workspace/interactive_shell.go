package workspace

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type ShellInvocation struct {
	Source string `json:"source"`
	Reason string `json:"reason"`
	CWD    string `json:"cwd"`
	Shell  string `json:"shell"`
}

// ResolveShell follows tmux's normal default-shell selection. Shell grammar is
// explicit: Bash, Zsh and POSIX shells use the same literal source contract.
func (t Tmux) ResolveShell(ctx context.Context) (string, error) {
	snapshot, err := t.Snapshot(ctx)
	if err != nil {
		return "", err
	}
	out, err := tmuxOutput(ctx, t.Socket, "show-options", "-Av", "-t", snapshot.Caller.Owner.Session, "default-shell")
	if err != nil {
		return "", err
	}
	path := strings.TrimSuffix(out, "\n")
	if err := validateShell(path); err != nil {
		return "", err
	}
	return path, nil
}

func validateShell(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("tmux default-shell must be an absolute executable path")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return errors.New("tmux default-shell is not executable")
	}
	switch filepath.Base(path) {
	case "bash", "zsh", "sh", "dash", "ksh":
		return nil
	}
	return errors.New("execute-heredoc requires a Bash, Zsh or POSIX-compatible tmux default-shell")
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

// A private PTY lets normal startup and first-prompt hooks (including direnv)
// run. The command's two output streams use separate inherited pipes. Only
// control framing uses the PTY; the source is never typed into another pane.
func runInteractive(ctx context.Context, invocation ShellInvocation, stdout, stderr io.Writer) (code int, started bool, retErr error) {
	if err := validateShell(invocation.Shell); err != nil {
		return 127, false, err
	}
	if !filepath.IsAbs(invocation.CWD) || strings.ContainsRune(invocation.Source, 0) {
		return 127, false, ErrUnknown
	}
	dir, err := os.MkdirTemp("", "postman-execution-")
	if err != nil {
		return 127, false, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte(invocation.Source), 0o600); err != nil {
		return 127, false, err
	}
	nonce, err := randomName()
	if err != nil {
		return 127, false, err
	}
	nonce = strings.TrimPrefix(nonce, ".new-")
	var reads, writes []*os.File
	for range 3 {
		r, w, err := os.Pipe()
		if err != nil {
			for _, f := range append(reads, writes...) {
				_ = f.Close()
			}
			return 127, false, err
		}
		reads = append(reads, r)
		writes = append(writes, w)
	}
	defer func() {
		for _, f := range append(reads, writes...) {
			_ = f.Close()
		}
	}()
	cmd := exec.Command(invocation.Shell, "-il")
	cmd.Dir = invocation.CWD
	cmd.ExtraFiles = writes
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 120})
	if err != nil {
		return 127, false, err
	}
	for _, f := range writes {
		_ = f.Close()
	}
	defer func() { _ = terminal.Close() }()
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}()
	var lastOutput atomic.Int64
	lastOutput.Store(time.Now().UnixNano())
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := terminal.Read(buf)
			if err != nil {
				return
			}
			lastOutput.Store(time.Now().UnixNano())
		}
	}()
	streamDone := make(chan struct{})
	var streams sync.WaitGroup
	streams.Add(2)
	go func() { defer streams.Done(); _, _ = io.Copy(stdout, reads[0]) }()
	go func() { defer streams.Done(); _, _ = io.Copy(stderr, reads[1]) }()
	go func() { streams.Wait(); close(streamDone) }()
	defer func() { _ = reads[0].Close(); _ = reads[1].Close(); <-streamDone }()
	frames := make(chan string, 8)
	go func() {
		defer close(frames)
		scan := bufio.NewScanner(reads[2])
		for scan.Scan() {
			select {
			case frames <- scan.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	// This first line is queued even during slow startup. It is read only after
	// the interactive shell finishes its startup files, and causes another
	// normal prompt so directory-dependent hooks see the approved cwd.
	builtin := "builtin "
	if base := filepath.Base(invocation.Shell); base == "sh" || base == "dash" {
		builtin = "command "
	}
	bootstrap := builtin + "set +m; " + builtin + "cd -- " + shellQuote(invocation.CWD) + " && " + builtin + "printf '" + nonce + ":ready\\n' >&5\n"
	if _, err = io.WriteString(terminal, bootstrap); err != nil {
		return 127, false, err
	}
	initCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		select {
		case <-initCtx.Done():
			return 127, false, fmt.Errorf("interactive shell initialization: %w", initCtx.Err())
		case frame, ok := <-frames:
			if !ok {
				return 127, false, errors.New("interactive shell exited before initialization")
			}
			if frame == nonce+":ready" {
				goto ready
			}
		}
	}
ready:
	// First-prompt hooks may print or refresh the environment. Give that prompt
	// a short quiet interval; never use a hardcoded PS1 or user's dotfile format.
	quiet := time.NewTicker(25 * time.Millisecond)
	defer quiet.Stop()
	for {
		select {
		case <-initCtx.Done():
			return 127, false, initCtx.Err()
		case <-quiet.C:
			if time.Since(time.Unix(0, lastOutput.Load())) >= 180*time.Millisecond {
				goto dispatch
			}
		}
	}
dispatch:
	wrapper := builtin + "printf '" + nonce + ":started\\n' >&5; ( . " + shellQuote(source) + " ) >&3 2>&4; postman_execution_code=$?; " + builtin + "printf '" + nonce + ":exit:%s\\n' \"$postman_execution_code\" >&5; " + builtin + "exit\n"
	if _, err = io.WriteString(terminal, wrapper); err != nil {
		return 127, false, err
	}
	for {
		select {
		case <-ctx.Done():
			return 127, started, ctx.Err()
		case frame, ok := <-frames:
			if !ok {
				return 127, started, errors.New("interactive shell exited without a completion record")
			}
			if frame == nonce+":started" {
				started = true
			}
			if strings.HasPrefix(frame, nonce+":exit:") {
				value, err := strconv.Atoi(strings.TrimPrefix(frame, nonce+":exit:"))
				if err != nil || value < 0 || value > 255 {
					return 127, started, ErrUnknown
				}
				// The shell has closed the command's streams. Bound draining in case
				// a background descendant inherited them; do not wait indefinitely.
				drain := time.NewTimer(2 * time.Second)
				defer drain.Stop()
				select {
				case <-streamDone:
				case <-drain.C:
					return value, true, errors.New("command exited but a descendant retained output pipes")
				case <-ctx.Done():
					return value, true, ctx.Err()
				}
				return value, true, nil
			}
		}
	}
}
