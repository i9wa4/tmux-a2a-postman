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

// Run the real public main in a child process so its ExitCode forwarding and
// stdout/stderr contract are observed across an OS process boundary.
func TestIssue937PublicCLIChild(t *testing.T) {
	raw := os.Getenv("POSTMAN_TEST_PUBLIC_ARGS")
	if raw == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		t.Fatal(err)
	}
	os.Args = append([]string{"postman"}, args...)
	main()
	os.Exit(0)
}

func TestWorkspaceExecuteBashProcessPreservesExitAndStreams(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "pm937-exec-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	home := t.TempDir()
	stateHome := t.TempDir()
	root := filepath.Join(home, "work")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	runTmux := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "tmux", append([]string{"-S", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("owned tmux %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	pane := runTmux("-f", "/dev/null", "new-session", "-d", "-s", "work", "-c", root, "-P", "-F", "#{pane_id}", "exec sleep 120")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "tmux", "-S", socket, "kill-server").Run()
	})
	runTmux("select-pane", "-t", pane, "-T", "worker")
	config := filepath.Join(t.TempDir(), "postman.md")
	if err := os.WriteFile(config, []byte("## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `worker`\nWorker\n## `peer`\nPeer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A matching historical approval-looking file must have no authority over
	// a fresh file-first invocation. Its bytes must remain untouched.
	legacyDir := filepath.Join(stateHome, "tmux-a2a-postman", "legacy-context", "work", "journal")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(legacyDir, "approved.json")
	legacyBody := []byte(`{"thread_id":"command-approval-old","decision":"approved","command":"printf protocol-output; printf diagnostic >&2; exit 7"}`)
	if err := os.WriteFile(legacy, legacyBody, 0o600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := "printf protocol-output; printf diagnostic >&2; exit 7"
	args, err := json.Marshal([]string{"execute-bash", "--postman-md", config, "--command", command})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, self, "-test.run=^TestIssue937PublicCLIChild$")
	child.Env = append(os.Environ(), "HOME="+home, "XDG_STATE_HOME="+stateHome, "TMUX="+socket+",0,0", "TMUX_PANE="+pane, "POSTMAN_TEST_PUBLIC_ARGS="+string(args))
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	err = child.Run()
	var exit *exec.ExitError
	if ctx.Err() != nil || !strings.Contains(stderr.String(), "diagnostic") || stdout.String() != "protocol-output" ||
		!errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("public process: err=%v, context=%v, stdout=%q, stderr=%q", err, ctx.Err(), stdout.String(), stderr.String())
	}
	jsonStart := bytes.IndexByte(stderr.Bytes(), '{')
	if jsonStart < 0 {
		t.Fatalf("missing JSON result on stderr: %q", stderr.String())
	}
	var result struct {
		Status    string                    `json:"status"`
		Execution workspace.ExecutionResult `json:"execution"`
	}
	if err := json.NewDecoder(bytes.NewReader(stderr.Bytes()[jsonStart:])).Decode(&result); err != nil ||
		result.Status != "exited" || result.Execution.ExitStatus == nil || *result.Execution.ExitStatus != 7 || result.Execution.RequestID != "" {
		t.Fatalf("public JSON: %+v, err=%v, stderr=%q", result, err, stderr.String())
	}
	if got, err := os.ReadFile(legacy); err != nil || !bytes.Equal(got, legacyBody) {
		t.Fatalf("historical approval changed: %q, %v", got, err)
	}
}
