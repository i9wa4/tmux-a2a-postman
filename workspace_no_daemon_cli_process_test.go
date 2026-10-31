package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/testfixture"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

// rebuiltCLIFixture owns its tmux socket, HOME, state, and rebuilt executable.
// Every invocation below starts the actual public CLI in a fresh process.
type rebuiltCLIFixture struct {
	t               *testing.T
	dir             string
	bin             string
	tmuxFixture     *testfixture.RealTmux
	home            string
	stateHome       string
	config          string
	worker          string
	peer            string
	stateRoot       string
	modCacheCleaned bool
}

func newRebuiltCLIFixture(t *testing.T) *rebuiltCLIFixture {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go toolchain unavailable for CLI rebuild")
	}
	rt, err := testfixture.NewRealTmux(t)
	if err != nil {
		t.Fatal(err)
	}
	dir := rt.Root()
	f := &rebuiltCLIFixture{
		t: t, dir: dir, bin: filepath.Join(dir, "postman-cli"),
		tmuxFixture: rt, home: filepath.Join(dir, "home"),
		stateHome: filepath.Join(dir, "state"), config: filepath.Join(dir, "home", "work", "postman.md"),
	}
	t.Cleanup(func() {
		if err := f.cleanModCache(); err != nil {
			t.Errorf("private Go module cache cleanup: %v", err)
		}
	})
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
	buildCtx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", f.bin, ".")
	build.Env = f.buildEnv()
	if out, err := build.CombinedOutput(); err != nil || buildCtx.Err() != nil {
		t.Fatalf("rebuild public CLI: %v, context=%v: %s", err, buildCtx.Err(), out)
	}
	f.worker = f.tmux("new-session", "-d", "-s", "work", "-c", work, "-P", "-F", "#{pane_id}")
	f.tmux("select-pane", "-t", f.worker, "-T", "worker")
	f.peer = f.tmux("split-window", "-d", "-t", f.worker, "-P", "-F", "#{pane_id}")
	f.tmux("select-pane", "-t", f.peer, "-T", "peer")
	if err := os.WriteFile(f.config, []byte("## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `worker`\nWorker\n## `peer`\nPeer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *rebuiltCLIFixture) buildEnv() []string {
	return append(f.tmuxFixture.Env(),
		"GOCACHE="+filepath.Join(f.dir, "go-cache"),
		"GOMODCACHE="+filepath.Join(f.dir, "modcache"))
}

func (f *rebuiltCLIFixture) cleanModCache() error {
	if f.modCacheCleaned {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "clean", "-modcache")
	cmd.Env = f.buildEnv()
	if out, err := cmd.CombinedOutput(); err != nil || ctx.Err() != nil {
		return fmt.Errorf("go clean -modcache: %v, context=%v: %s", err, ctx.Err(), out)
	}
	f.modCacheCleaned = true
	return nil
}

func (f *rebuiltCLIFixture) tmux(args ...string) string {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := f.tmuxFixture.Tmux(ctx, args...)
	if err != nil {
		f.t.Fatalf("owned tmux %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(out)
}

type rebuiltCLIResult struct {
	stdout []byte
	stderr []byte
	exit   int
}

func (f *rebuiltCLIFixture) run(pane, body string, args ...string) rebuiltCLIResult {
	f.t.Helper()
	return f.runWithEnv(pane, body, nil, args...)
}

func (f *rebuiltCLIFixture) runWithEnv(pane, body string, extraEnv []string, args ...string) rebuiltCLIResult {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.bin, args...)
	cmd.Env = append(f.tmuxFixture.Env(), append([]string{"TMUX_PANE=" + pane, "POSTMAN_HOME="}, extraEnv...)...)
	cmd.Stdin = strings.NewReader(body)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		f.t.Fatalf("rebuilt CLI %v timed out: %v: %s; screen=%s", args, ctx.Err(), stderr.String(), f.tmux("capture-pane", "-p", "-t", f.peer))
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
	Status       string                  `json:"status"`
	Message      *workspace.Message      `json:"message"`
	MarkdownPath string                  `json:"markdown_absolute_path"`
	ReceiptPath  string                  `json:"pop_receipt_absolute_path"`
	Self         workspace.SelfState     `json:"self"`
	Notice       *workspace.NoticeResult `json:"notice"`
} {
	t.Helper()
	var result struct {
		Status       string                  `json:"status"`
		Message      *workspace.Message      `json:"message"`
		MarkdownPath string                  `json:"markdown_absolute_path"`
		ReceiptPath  string                  `json:"pop_receipt_absolute_path"`
		Self         workspace.SelfState     `json:"self"`
		Notice       *workspace.NoticeResult `json:"notice"`
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
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", replacement, ".")
	build.Env = f.buildEnv()
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
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "sentinel"), []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HOME", "POSTMAN_HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "TMUX", "TMUX_PANE", "TMUX_TMPDIR"} {
		t.Setenv(key, outside)
	}
	// Every core invocation must ignore even malformed obsolete TOML and the
	// fixture's distinct POSTMAN_HOME. Only explicit Markdown and XDG state count.
	configDir := filepath.Join(f.dir, "config", "tmux-a2a-postman")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "postman.toml"), []byte("[invalid TOML\nbase_dir = /old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.exerciseFourCommands(t, "before-replacement")
	f.replaceCLI(t)
	f.exerciseFourCommands(t, "after-replacement")
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 1 || entries[0].Name() != "sentinel" {
		t.Fatalf("outside fixture changed: entries=%v err=%v", entries, err)
	}
	if got, err := os.ReadFile(filepath.Join(outside, "sentinel")); err != nil || string(got) != "untouched" {
		t.Fatalf("outside fixture sentinel changed: %q, %v", got, err)
	}
	if err := f.cleanModCache(); err != nil {
		t.Fatalf("private Go module cache cleanup: %v", err)
	}
	root := f.tmuxFixture.Root()
	if err := f.tmuxFixture.Close(); err != nil {
		t.Fatalf("owned tmux cleanup: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned fixture root remains after cleanup: %v", err)
	}
}
