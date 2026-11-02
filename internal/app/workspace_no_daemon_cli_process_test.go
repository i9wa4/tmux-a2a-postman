package app

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

// go test runs this package from internal/app. Standalone CLI rebuilds and
// repository-owned Python fixtures must still run from the module root.
func repositorySource(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(root, "go.mod")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("cannot locate module root from integration package: %s: %v", root, err)
	}
	return root
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
	build.Dir = repositorySource(t)
	build.Env = f.buildEnv()
	if out, err := build.CombinedOutput(); err != nil || buildCtx.Err() != nil {
		t.Fatalf("rebuild public CLI: %v, context=%v: %s", err, buildCtx.Err(), out)
	}
	f.worker = f.tmux("new-session", "-d", "-s", "work", "-c", work, "-P", "-F", "#{pane_id}")
	f.tmux("select-pane", "-t", f.worker, "-T", "worker")
	f.peer = f.tmux("split-window", "-d", "-t", f.worker, "-c", work, "-P", "-F", "#{pane_id}")
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

func (f *rebuiltCLIFixture) jsonResult(t *testing.T, got rebuiltCLIResult) protocolOutput {
	t.Helper()
	var result protocolOutput
	if err := json.Unmarshal(got.stdout, &result); err != nil {
		t.Fatalf("CLI invalid JSON: %v stdout=%q stderr=%q", err, got.stdout, got.stderr)
	}
	return result
}

func (f *rebuiltCLIFixture) exerciseFourCommands(t *testing.T, phase string) {
	body := "rebuilt CLI payload " + phase + "\n"
	send := f.run(f.peer, body, "send-heredoc", "--config", f.config, "--to", "worker")
	sent := f.jsonResult(t, send)
	if send.exit != 0 || !sent.Success {
		t.Fatal(send, sent)
	}
	status := f.run(f.worker, "", "get-status", "--config", f.config)
	known := f.jsonResult(t, status)
	worker := findStatusPane(t, known.Sessions, f.worker).StatusValue()
	if status.exit != 0 || worker.UnreadCount == nil || *worker.UnreadCount != 1 {
		t.Fatal(status, known)
	}
	pop := f.run(f.worker, "", "pop", "--config", f.config)
	claimed := f.jsonResult(t, pop)
	if pop.exit != 0 || !claimed.Success || claimed.MarkdownPath == nil {
		t.Fatal(pop, claimed)
	}
	msg := readProtocolArchive(t, *claimed.MarkdownPath)
	if msg.Body != body {
		t.Fatal("body was rewritten", msg.Body)
	}
	receipt := strings.TrimSuffix(*claimed.MarkdownPath, ".md") + ".pop.json"
	if _, err := os.Stat(receipt); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(f.dir, phase+"-executed")
	execution := f.run(f.worker, "printf executed > "+marker, "execute-heredoc", "--config", f.config, "--reason", "rebuild test")
	result := decodeExecutionOutput(t, execution.stderr)
	if execution.exit != 0 || !result.Success || result.Executed == nil || !*result.Executed {
		t.Fatalf("execution: %+v %q", result, execution.stderr)
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "executed" {
		t.Fatal(string(raw), err)
	}
}

func TestRebuiltCLIUsesExistingReadableStateRoot(t *testing.T) {
	f := newRebuiltCLIFixture(t)
	if err := os.Mkdir(f.stateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(f.stateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	f.exerciseFourCommands(t, "readable-state-root")
	if info, err := os.Stat(f.stateRoot); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatal("CLI changed application root permissions", info, err)
	}
	if err := filepath.Walk(f.stateRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || path == f.stateRoot {
			return err
		}
		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Errorf("non-private state child %s: %s", path, info.Mode())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func findProtocolNode(t *testing.T, nodes []workspace.NodeStatus, name string) workspace.NodeStatus {
	t.Helper()
	for _, node := range nodes {
		if node.Node == name {
			return node
		}
	}
	t.Fatalf("node %s missing from %+v", name, nodes)
	return workspace.NodeStatus{}
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
	build.Dir = repositorySource(t)
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
