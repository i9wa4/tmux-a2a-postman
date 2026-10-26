package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

// Status reads saved mail without starting a daemon, registering a workspace,
// recovering pending writes, or trusting a caller whose native pane changed.
func TestWorkspaceStatusProcessIsReadOnlyAndReportsUnknownActivity(t *testing.T) {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "pm937-status-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	home, stateHome := filepath.Join(dir, "home"), filepath.Join(dir, "state")
	work := filepath.Join(home, "work")
	for _, path := range []string{work, stateHome} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runTmux := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, tmuxPath, append([]string{"-S", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("owned tmux %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	worker := runTmux("-f", "/dev/null", "new-session", "-d", "-s", "work", "-c", work, "-P", "-F", "#{pane_id}", "exec sleep 120")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, tmuxPath, "-S", socket, "kill-server").Run()
	})
	runTmux("select-pane", "-t", worker, "-T", "worker")
	peer := runTmux("split-window", "-d", "-t", worker, "-P", "-F", "#{pane_id}", "exec sleep 120")
	runTmux("select-pane", "-t", peer, "-T", "peer")
	config := filepath.Join(dir, "postman.md")
	if err := os.WriteFile(config, []byte("## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `worker`\nWorker\n## `peer`\nPeer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const bodyToken = "private-status-payload-937-9"
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	type output struct {
		Status  string              `json:"status"`
		Message *workspace.Message  `json:"message"`
		Self    workspace.SelfState `json:"self"`
	}
	invoke := func(pane, body string, args ...string) (output, string, error) {
		t.Helper()
		encoded, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		child := exec.CommandContext(ctx, self, "-test.run=^TestIssue937PublicCLIChild$")
		child.Env = append(os.Environ(), "HOME="+home, "XDG_STATE_HOME="+stateHome,
			"TMUX="+socket+",0,0", "TMUX_PANE="+pane, "POSTMAN_TEST_PUBLIC_ARGS="+string(encoded))
		child.Stdin = strings.NewReader(body)
		var stdout, stderr bytes.Buffer
		child.Stdout, child.Stderr = &stdout, &stderr
		runErr := child.Run()
		if ctx.Err() != nil {
			t.Fatalf("public CLI %v timed out: %v, stderr: %s", args, ctx.Err(), stderr.String())
		}
		if len(args) > 0 && args[0] == "get-status" &&
			(strings.Contains(stdout.String(), bodyToken) || strings.Contains(stderr.String(), bodyToken)) {
			t.Fatalf("public status disclosed private mail body: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
		var result output
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatalf("public CLI JSON %v: %v, stdout: %q, stderr: %q", args, err, stdout.String(), stderr.String())
		}
		return result, stdout.String(), runErr
	}
	root, err := workspace.DefaultStateRoot(home, stateHome)
	if err != nil {
		t.Fatal(err)
	}
	missing, _, err := invoke(worker, "", "get-status", "--postman-md", config)
	if err == nil || missing.Status != "unavailable" || missing.Self.Known || missing.Self.Unread != nil {
		t.Fatalf("missing state reported as known: %+v, %v", missing, err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("status created missing state root: %v", err)
	}

	body := "status must not return this mail body: " + bodyToken + "\n"
	sent, _, err := invoke(peer, body, "send-heredoc", "--postman-md", config, "--to", "worker", "--no-reply")
	if err != nil || sent.Status != "sent" || sent.Message == nil {
		t.Fatalf("fixture send: %+v, %v", sent, err)
	}
	before, err := snapshotRegularFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	known, raw, err := invoke(worker, "", "get-status", "--postman-md", config)
	if err != nil || known.Status != "ok" || !known.Self.Known || known.Self.Unread == nil || *known.Self.Unread != 1 ||
		known.Self.NextAction != "pop" {
		t.Fatalf("saved-file status: %+v, err=%v, raw=%s", known, err, raw)
	}
	after, err := snapshotRegularFiles(root)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("status mutated saved files: before=%v after=%v err=%v", before, after, err)
	}

	runTmux("select-pane", "-t", worker, "-T", "other")
	conflict, _, err := invoke(worker, "", "get-status", "--postman-md", config)
	if err == nil || conflict.Status != "unavailable" || conflict.Self.Known || conflict.Self.Unread != nil {
		t.Fatalf("retitled caller reported as working: %+v, %v", conflict, err)
	}
	runTmux("select-pane", "-t", worker, "-T", "worker")
	runTmux("kill-pane", "-t", worker)
	stale, _, err := invoke(worker, "", "get-status", "--postman-md", config)
	if err == nil || stale.Status != "unavailable" || stale.Self.Known || stale.Self.Unread != nil {
		t.Fatalf("missing caller pane reported as working: %+v, %v", stale, err)
	}
	final, err := snapshotRegularFiles(root)
	if err != nil || !reflect.DeepEqual(before, final) {
		t.Fatalf("unavailable status mutated saved files: before=%v after=%v err=%v", before, final, err)
	}
}
