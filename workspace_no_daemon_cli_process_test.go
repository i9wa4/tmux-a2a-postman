package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

// rebuiltCLIFixture owns its tmux socket, HOME, state, and rebuilt executable.
// Every invocation below starts the actual public CLI in a fresh process.
type rebuiltCLIFixture struct {
	t         *testing.T
	dir       string
	bin       string
	socket    string
	home      string
	stateHome string
	config    string
	worker    string
	peer      string
	stateRoot string
}

func newRebuiltCLIFixture(t *testing.T) *rebuiltCLIFixture {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go toolchain unavailable for CLI rebuild")
	}
	dir, err := os.MkdirTemp("/tmp", "pm937-rebuilt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &rebuiltCLIFixture{
		t: t, dir: dir, bin: filepath.Join(dir, "postman"),
		socket: filepath.Join(dir, "s"), home: filepath.Join(dir, "home"),
		stateHome: filepath.Join(dir, "state"), config: filepath.Join(dir, "postman.md"),
	}
	work := filepath.Join(f.home, "work")
	for _, path := range []string{work, f.stateHome} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f.stateRoot, err = workspace.DefaultStateRoot(f.home, f.stateHome)
	if err != nil {
		t.Fatal(err)
	}
	buildCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", f.bin, ".")
	if out, err := build.CombinedOutput(); err != nil || buildCtx.Err() != nil {
		t.Fatalf("rebuild public CLI: %v, context=%v: %s", err, buildCtx.Err(), out)
	}
	f.worker = f.tmux("-f", "/dev/null", "new-session", "-d", "-s", "work", "-c", work,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "tmux", "-S", f.socket, "kill-server").Run()
	})
	f.tmux("select-pane", "-t", f.worker, "-T", "worker")
	f.peer = f.tmux("split-window", "-d", "-t", f.worker, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.tmux("select-pane", "-t", f.peer, "-T", "peer")
	if err := os.WriteFile(f.config, []byte("## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `worker`\nWorker\n## `peer`\nPeer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *rebuiltCLIFixture) tmux(args ...string) string {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", append([]string{"-S", f.socket}, args...)...).CombinedOutput()
	if err != nil {
		f.t.Fatalf("owned tmux %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

type rebuiltCLIResult struct {
	stdout []byte
	stderr []byte
	exit   int
}

func (f *rebuiltCLIFixture) run(pane, body string, args ...string) rebuiltCLIResult {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.bin, args...)
	cmd.Env = append(os.Environ(), "HOME="+f.home, "XDG_STATE_HOME="+f.stateHome,
		"TMUX="+f.socket+",0,0", "TMUX_PANE="+pane)
	cmd.Stdin = strings.NewReader(body)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		f.t.Fatalf("rebuilt CLI %v timed out: %v: %s", args, ctx.Err(), stderr.String())
	}
	result := rebuiltCLIResult{stdout: stdout.Bytes(), stderr: stderr.Bytes()}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			f.t.Fatalf("rebuilt CLI %v failed to start: %v", args, err)
		}
		result.exit = exit.ExitCode()
	}
	return result
}

func (f *rebuiltCLIFixture) jsonResult(t *testing.T, got rebuiltCLIResult) struct {
	Status       string              `json:"status"`
	Message      *workspace.Message  `json:"message"`
	MarkdownPath string              `json:"markdown_absolute_path"`
	ReceiptPath  string              `json:"pop_receipt_absolute_path"`
	Self         workspace.SelfState `json:"self"`
} {
	t.Helper()
	var result struct {
		Status       string              `json:"status"`
		Message      *workspace.Message  `json:"message"`
		MarkdownPath string              `json:"markdown_absolute_path"`
		ReceiptPath  string              `json:"pop_receipt_absolute_path"`
		Self         workspace.SelfState `json:"self"`
	}
	if err := json.Unmarshal(got.stdout, &result); err != nil {
		t.Fatalf("rebuilt CLI invalid JSON: %v; stdout=%q stderr=%q", err, got.stdout, got.stderr)
	}
	return result
}

func (f *rebuiltCLIFixture) exerciseFourCommands(t *testing.T, phase string) {
	body := "rebuilt CLI mail payload 937-1-" + phase + "\n"
	send := f.run(f.peer, body, "send-heredoc", "--postman-md", f.config, "--to", "worker", "--no-reply")
	sent := f.jsonResult(t, send)
	if send.exit != 0 || sent.Status != "sent" || sent.Message == nil || sent.Message.ID == "" {
		t.Fatalf("send-heredoc exit=%d result=%+v stderr=%q", send.exit, sent, send.stderr)
	}
	status := f.run(f.worker, "", "get-status", "--postman-md", f.config)
	known := f.jsonResult(t, status)
	if status.exit != 0 || known.Status != "ok" || !known.Self.Known || known.Self.Unread == nil || *known.Self.Unread != 1 {
		t.Fatalf("get-status exit=%d result=%+v stderr=%q", status.exit, known, status.stderr)
	}
	pop := f.run(f.worker, "", "pop", "--postman-md", f.config, sent.Message.ID)
	claimed := f.jsonResult(t, pop)
	if pop.exit != 0 || claimed.Status != "message" || claimed.Message == nil || claimed.Message.ID != sent.Message.ID || claimed.MarkdownPath == "" || claimed.ReceiptPath == "" {
		t.Fatalf("pop exit=%d result=%+v stderr=%q", pop.exit, claimed, pop.stderr)
	}
	archive, err := os.ReadFile(claimed.MarkdownPath)
	if err != nil || !strings.Contains(string(archive), body) {
		t.Fatalf("pop archive does not hold sent body: %v", err)
	}
	if _, err := os.Stat(claimed.ReceiptPath); err != nil {
		t.Fatalf("pop receipt missing: %v", err)
	}
	marker := filepath.Join(f.dir, phase+"-executed")
	execute := f.run(f.worker, "", "execute-bash", "--postman-md", f.config,
		"--command", "printf executed > "+marker)
	if execute.exit != 0 || !bytes.Contains(execute.stderr, []byte(`"status":"executed"`)) {
		t.Fatalf("execute-bash exit=%d stdout=%q stderr=%q", execute.exit, execute.stdout, execute.stderr)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "executed" {
		t.Fatalf("execute-bash durable marker: %q, %v", got, err)
	}
}

func (f *rebuiltCLIFixture) replaceCLI(t *testing.T) {
	t.Helper()
	before, err := os.Stat(f.bin)
	if err != nil {
		t.Fatal(err)
	}
	replacement := f.bin + ".replacement"
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", replacement, ".")
	if out, err := build.CombinedOutput(); err != nil || ctx.Err() != nil {
		t.Fatalf("rebuild replacement CLI: %v, context=%v: %s", err, ctx.Err(), out)
	}
	if err := os.Rename(replacement, f.bin); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(f.bin)
	if err != nil || os.SameFile(before, after) {
		t.Fatalf("CLI executable was not replaced: %v", err)
	}
}

func TestWorkspaceRebuiltCLIFourNoDaemonCommands(t *testing.T) {
	f := newRebuiltCLIFixture(t)
	f.exerciseFourCommands(t, "before-replacement")
	f.replaceCLI(t)
	f.exerciseFourCommands(t, "after-replacement")
}
