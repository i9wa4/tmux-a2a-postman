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

	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

// These tests run the public CLI in separate processes against an owned tmux
// server and state root. No Postman daemon participates in the decision path.
type executionProcessFixture struct {
	t         *testing.T
	socket    string
	home      string
	stateHome string
	worker    string
	peer      string
	config    string
	root      string
	self      string
}

func newExecutionProcessFixture(t *testing.T, reviewer bool) *executionProcessFixture {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "pm-exec-matrix-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &executionProcessFixture{
		t: t, socket: filepath.Join(dir, "s"), home: t.TempDir(),
		stateHome: t.TempDir(),
	}
	f.root = filepath.Join(f.home, "work")
	if err := os.Mkdir(f.root, 0o700); err != nil {
		t.Fatal(err)
	}
	f.worker = f.tmux("-f", "/dev/null", "new-session", "-d", "-s", "work", "-c", f.root, "-P", "-F", "#{pane_id}", "exec sleep 120")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "tmux", "-S", f.socket, "kill-server").Run()
	})
	f.tmux("select-pane", "-t", f.worker, "-T", "worker")
	edge := "worker --- peer"
	if reviewer {
		f.peer = f.tmux("split-window", "-d", "-t", f.worker, "-P", "-F", "#{pane_id}", "exec sleep 120")
		f.tmux("select-pane", "-t", f.peer, "-T", "peer")
		edge += ":::command_approver_node"
	}
	f.config = filepath.Join(dir, "postman.md")
	config := "## `edges`\n```mermaid\ngraph LR\n" + edge + "\n```\n## `worker`\nWorker\n## `peer`\nPeer\n"
	if err := os.WriteFile(f.config, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	f.self, err = os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *executionProcessFixture) tmux(args ...string) string {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", append([]string{"-S", f.socket}, args...)...).CombinedOutput()
	if err != nil {
		f.t.Fatalf("owned tmux %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *executionProcessFixture) child(ctx context.Context, command string) *exec.Cmd {
	f.t.Helper()
	args, err := json.Marshal([]string{"execute-bash", "--postman-md", f.config, "--command", command})
	if err != nil {
		f.t.Fatal(err)
	}
	child := exec.CommandContext(ctx, f.self, "-test.run=^TestIssue937PublicCLIChild$")
	child.Env = append(os.Environ(), "HOME="+f.home, "XDG_STATE_HOME="+f.stateHome,
		"TMUX="+f.socket+",0,0", "TMUX_PANE="+f.worker, "POSTMAN_TEST_PUBLIC_ARGS="+string(args))
	return child
}

func (f *executionProcessFixture) requests() []string {
	f.t.Helper()
	stateRoot, err := workspace.DefaultStateRoot(f.home, f.stateHome)
	if err != nil {
		f.t.Fatal(err)
	}
	inbox := filepath.Join(stateRoot, "work", "agents", "peer", "inbox")
	entries, err := os.ReadDir(inbox)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		f.t.Fatal(err)
	}
	var ids []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".md") {
			ids = append(ids, entry.Name())
		}
	}
	return ids
}

func (f *executionProcessFixture) awaitRequests(n int) []string {
	f.t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if ids := f.requests(); len(ids) >= n {
			return ids
		}
		time.Sleep(25 * time.Millisecond)
	}
	f.t.Fatalf("wanted %d reviewer requests, got %v", n, f.requests())
	return nil
}

func TestWorkspaceExecuteBashProcessMatrixAdvisoryAbsent(t *testing.T) {
	f := newExecutionProcessFixture(t, false)
	marker := filepath.Join(filepath.Dir(f.config), "advisory-ran")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	child := f.child(ctx, "printf advisory > "+marker)
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	if err := child.Run(); err != nil || ctx.Err() != nil {
		t.Fatalf("advisory CLI: err=%v ctx=%v stdout=%q stderr=%q", err, ctx.Err(), stdout.String(), stderr.String())
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "advisory" {
		t.Fatalf("advisory command did not run: %q, %v", got, err)
	}
	if ids := f.requests(); len(ids) != 0 {
		t.Fatalf("absent reviewer generated approval requests: %v", ids)
	}
	jsonStart := bytes.IndexByte(stderr.Bytes(), '{')
	if jsonStart < 0 {
		t.Fatalf("missing public JSON result: %q", stderr.String())
	}
	var result struct {
		Status    string                    `json:"status"`
		Execution workspace.ExecutionResult `json:"execution"`
	}
	if err := json.NewDecoder(bytes.NewReader(stderr.Bytes()[jsonStart:])).Decode(&result); err != nil ||
		result.Status != "executed" || result.Execution.RequestID != "" || result.Execution.Reviewer != "" {
		t.Fatalf("unexpected advisory result: %+v, err=%v, stderr=%q", result, err, stderr.String())
	}
}

func TestWorkspaceExecuteBashProcessMatrixPresentAndDisappeared(t *testing.T) {
	f := newExecutionProcessFixture(t, true)
	marker := filepath.Join(filepath.Dir(f.config), "reviewed-ran")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	child := f.child(ctx, "printf reviewed > "+marker)
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	requests := f.awaitRequests(1)
	if len(requests) != 1 {
		t.Fatalf("want one fresh approval request, got %v", requests)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("command ran before reviewer decision: %v", err)
	}
	f.tmux("kill-pane", "-t", f.peer)
	if err := child.Wait(); err == nil || ctx.Err() != nil {
		t.Fatalf("disappeared reviewer: err=%v ctx=%v stderr=%q", err, ctx.Err(), stderr.String())
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("command ran after reviewer disappeared: %v", err)
	}
}

func TestWorkspaceExecuteBashProcessMatrixProbeFailure(t *testing.T) {
	f := newExecutionProcessFixture(t, true)
	marker := filepath.Join(filepath.Dir(f.config), "probe-ran")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	child := f.child(ctx, "printf reviewed > "+marker)
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if requests := f.awaitRequests(1); len(requests) != 1 {
		t.Fatalf("want one fresh approval request, got %v", requests)
	}
	// Hide only the socket path. The owned tmux server and caller pane remain
	// alive, but the public CLI cannot probe its pinned runtime.
	hidden := f.socket + ".hidden"
	if err := os.Rename(f.socket, hidden); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Rename(hidden, f.socket); err != nil {
			t.Errorf("restore owned tmux socket: %v", err)
		}
	}()
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer probeCancel()
	probe, err := exec.CommandContext(probeCtx, "tmux", "-S", hidden, "display-message", "-p", "-t", f.worker, "#{pane_dead}").CombinedOutput()
	if err != nil || strings.TrimSpace(string(probe)) != "0" {
		t.Fatalf("caller pane did not remain live: %q %v", probe, err)
	}
	if err := child.Wait(); err == nil || ctx.Err() != nil {
		t.Fatalf("lost runtime probe: err=%v ctx=%v stderr=%q", err, ctx.Err(), stderr.String())
	}
	var result publicApprovalOutput
	if err := json.NewDecoder(bytes.NewReader(stderr.Bytes())).Decode(&result); err != nil || result.Status != "unavailable" ||
		result.Execution == nil || result.Execution.RequestID == "" || result.Execution.ReplyID != "" {
		t.Fatalf("public probe failure: %+v decode=%v stderr=%q", result, err, stderr.String())
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("command ran after runtime probe failed: %v", err)
	}
}

func TestWorkspaceExecuteBashProcessMatrixReviewerProbeFailure(t *testing.T) {
	f := newExecutionProcessFixture(t, true)
	marker := filepath.Join(filepath.Dir(f.config), "reviewer-probe-ran")
	shimDir := filepath.Join(filepath.Dir(f.config), "tmux-shim")
	if err := os.Mkdir(shimDir, 0o700); err != nil {
		t.Fatal(err)
	}
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	trigger := filepath.Join(shimDir, "fail-reviewer")
	used := filepath.Join(shimDir, "shim-used")
	shim := fmt.Sprintf(`#!/bin/sh
for arg in "$@"; do
  if [ "$arg" = list-panes ] && [ -f %q ]; then
    printf used > %q
    %q "$@" | awk 'BEGIN { FS = OFS = "\t" } $3 == "peer" { $5 = "bad-pid" } { print }'
    exit
  fi
done
exec %q "$@"
`, trigger, used, realTmux, realTmux)
	if err := os.WriteFile(filepath.Join(shimDir, "tmux"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	child := f.child(ctx, "printf reviewed > "+marker)
	child.Env = append(child.Env, "PATH="+shimDir+":"+os.Getenv("PATH"))
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	requests := f.awaitRequests(1)
	if len(requests) != 1 {
		t.Fatalf("want one correlated reviewer request, got %v", requests)
	}
	requestID := strings.TrimSuffix(requests[0], ".md")
	if err := os.WriteFile(trigger, []byte("fail"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := f.tmux("display-message", "-p", "-t", f.worker, "#{pane_dead}"); got != "0" {
		t.Fatalf("caller probe failed independently: %q", got)
	}
	if err := child.Wait(); err == nil || ctx.Err() != nil {
		t.Fatalf("reviewer probe failure: err=%v ctx=%v stderr=%q", err, ctx.Err(), stderr.String())
	}
	if _, err := os.Stat(used); err != nil {
		t.Fatalf("reviewer-only probe shim was not used: %v", err)
	}
	var result publicApprovalOutput
	if err := json.NewDecoder(bytes.NewReader(stderr.Bytes())).Decode(&result); err != nil ||
		result.Status != "unavailable" || !strings.Contains(result.Reason, "malformed pane process identity") ||
		result.Execution == nil || result.Execution.RequestID != requestID || result.Execution.Reviewer != "peer" ||
		result.Execution.ReplyID != "" {
		t.Fatalf("reviewer-only public failure: %+v decode=%v stderr=%q", result, err, stderr.String())
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("command ran after reviewer probe failed: %v", err)
	}
}

func TestWorkspaceExecuteBashProcessMatrixCallerCancellation(t *testing.T) {
	f := newExecutionProcessFixture(t, true)
	marker := filepath.Join(filepath.Dir(f.config), "cancelled-ran")
	ctx, cancel := context.WithCancel(context.Background())
	child := f.child(ctx, "printf reviewed > "+marker)
	if err := child.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	if requests := f.awaitRequests(1); len(requests) != 1 {
		cancel()
		t.Fatalf("want one fresh approval request, got %v", requests)
	}
	cancel()
	_ = child.Wait()
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("command ran after caller cancelled wait: %v", err)
	}
}

func TestWorkspaceExecuteBashProcessMatrixRepeatedInvocation(t *testing.T) {
	f := newExecutionProcessFixture(t, true)
	marker := filepath.Join(filepath.Dir(f.config), "repeated-ran")
	command := "printf reviewed > " + marker
	for n := 1; n <= 2; n++ {
		ctx, cancel := context.WithCancel(context.Background())
		child := f.child(ctx, command)
		if err := child.Start(); err != nil {
			cancel()
			t.Fatal(err)
		}
		requests := f.awaitRequests(n)
		if len(requests) != n {
			cancel()
			t.Fatalf("invocation %d: want %d independent requests, got %v", n, n, requests)
		}
		cancel()
		_ = child.Wait()
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("identical repeated command ran without its own decision: %v", err)
	}
}

func TestWorkspaceExecuteBashProcessMatrixRepeatedReply(t *testing.T) {
	f := newPublicApprovalFixture(t)
	marker := filepath.Join(f.home, "executed")
	command := "printf x >> " + marker
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	first := f.child(ctx, f.worker, "execute-bash", "--postman-md", f.config, "--command", command)
	var firstErr bytes.Buffer
	first.Stderr = &firstErr
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	old := f.waitRequest()
	claimed, err := f.runWhenAvailable(f.peer, "", "pop", "--postman-md", f.config)
	if err != nil || claimed.Message == nil || claimed.Message.ID != old.ID {
		t.Fatalf("first public reviewer POP: %+v %v", claimed, err)
	}
	f.reply(old, command, "approved")
	if err := first.Wait(); err != nil || ctx.Err() != nil {
		t.Fatalf("first approval: err=%v ctx=%v stderr=%q", err, ctx.Err(), firstErr.String())
	}
	duplicate := f.reply(old, command, "approved")

	second := f.child(ctx, f.worker, "execute-bash", "--postman-md", f.config, "--command", command)
	var secondErr bytes.Buffer
	second.Stderr = &secondErr
	if err := second.Start(); err != nil {
		t.Fatal(err)
	}
	fresh := f.waitRequestExcept(map[string]bool{old.ID: true})
	if fresh.InputRequestID == old.InputRequestID {
		t.Fatal("fresh request reused completed input ID")
	}
	claimed, err = f.runWhenAvailable(f.peer, "", "pop", "--postman-md", f.config)
	if err != nil || claimed.Message == nil || claimed.Message.ID != fresh.ID {
		t.Fatalf("fresh public reviewer POP: %+v %v", claimed, err)
	}
	rejected := f.reply(fresh, command, "rejected")
	if err := second.Wait(); err == nil || ctx.Err() != nil {
		t.Fatalf("fresh rejection: err=%v ctx=%v stderr=%q", err, ctx.Err(), secondErr.String())
	}
	var result publicApprovalOutput
	if err := json.NewDecoder(&secondErr).Decode(&result); err != nil || result.Status != "rejected" ||
		result.Execution == nil || result.Execution.RequestID != fresh.ID || result.Execution.ReplyID != rejected.ID {
		t.Fatalf("fresh exact reply: %+v decode=%v stderr=%q", result, err, secondErr.String())
	}
	root, err := workspace.DefaultStateRoot(f.home, f.stateHome)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "work", "agents", "worker", "inbox", duplicate.ID+".md")); err != nil {
		t.Fatalf("fresh call consumed repeated reply: %v", err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "x" {
		t.Fatalf("repeated reply dispatched command twice: %q %v", data, err)
	}
}
