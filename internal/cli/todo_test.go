package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

func TestRunTodoSummary_ShowsConfiguredNodesAndLiveCounts(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	contextID := "ctx-todo-summary"
	configPath := filepath.Join(tmpDir, "postman.toml")
	sessionDir := filepath.Join(tmpDir, contextID, "test-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"messenger -- worker -- orchestrator\"]\n\n[messenger]\nrole = \"messenger\"\n\n[worker]\nrole = \"worker\"\n\n[orchestrator]\nrole = \"orchestrator\"\n"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "todo", "worker.md"), []byte("- [x] shipped\n- [ ] follow-up\n"), 0o600); err != nil {
		t.Fatalf("WriteFile worker todo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "todo", "orchestrator.md"), []byte("- [x] reviewed\n"), 0o600); err != nil {
		t.Fatalf("WriteFile orchestrator todo: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunTodo([]string{"--config", configPath, "--context-id", contextID, "summary"})
	})
	if err != nil {
		t.Fatalf("RunTodo summary: %v\nstderr=%s", err, stderr)
	}
	for _, want := range []string{
		"messenger [·] 0/0",
		"worker [-] 1/2",
		"orchestrator [x] 1/1",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestRunTodoSummary_JSONOutput(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	contextID := "ctx-todo-summary-json"
	configPath := filepath.Join(tmpDir, "postman.toml")
	sessionDir := filepath.Join(tmpDir, contextID, "test-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"worker -- orchestrator\"]\n\n[worker]\nrole = \"worker\"\n\n[orchestrator]\nrole = \"orchestrator\"\n"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "todo", "worker.md"), []byte("- [ ] draft\n"), 0o600); err != nil {
		t.Fatalf("WriteFile worker todo: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunTodo([]string{"--config", configPath, "--context-id", contextID, "summary", "--json"})
	})
	if err != nil {
		t.Fatalf("RunTodo summary --json: %v\nstderr=%s", err, stderr)
	}
	for _, want := range []string{`"node":"worker"`, `"token":"[ ]"`, `"total":1`} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestRunTodoWrite_UsesCallerSimpleNameAndReplacesOwnerFile(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "review:worker")

	contextID := "ctx-todo-write"
	configPath := filepath.Join(tmpDir, "postman.toml")
	sessionDir := filepath.Join(tmpDir, contextID, "test-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"worker -- orchestrator\"]\n\n[worker]\nrole = \"worker\"\n\n[orchestrator]\nrole = \"orchestrator\"\n"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	path := filepath.Join(sessionDir, "todo", "worker.md")
	if err := os.WriteFile(path, []byte("old content\n"), 0o600); err != nil {
		t.Fatalf("WriteFile old todo: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunTodo([]string{"--config", configPath, "--context-id", contextID, "write", "--body", "# TODO\n\n- [x] shipped\n"})
	})
	if err != nil {
		t.Fatalf("RunTodo write: %v\nstderr=%s", err, stderr)
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("RunTodo write unexpectedly wrote output:\nstdout=%q\nstderr=%q", stdout, stderr)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(todo): %v", err)
	}
	if string(data) != "# TODO\n\n- [x] shipped\n" {
		t.Fatalf("todo content = %q, want replacement body", string(data))
	}
}

func TestRunTodoShow_RejectsInvalidNodeNames(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	contextID := "ctx-todo-show-invalid"
	configPath := filepath.Join(tmpDir, "postman.toml")
	sessionDir := filepath.Join(tmpDir, contextID, "test-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"worker -- orchestrator\"]\n\n[worker]\nrole = \"worker\"\n\n[orchestrator]\nrole = \"orchestrator\"\n"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "read", "secret.md"), []byte("secret-from-read\n"), 0o600); err != nil {
		t.Fatalf("WriteFile secret: %v", err)
	}

	for _, node := range []string{"../read/secret", "worker!"} {
		stdout, stderr, err := captureCommandOutput(t, func() error {
			return RunTodo([]string{"--config", configPath, "--context-id", contextID, "show", "--node", node})
		})
		if err == nil {
			t.Fatalf("RunTodo show --node %q error = nil, want invalid-name rejection", node)
		}
		if stdout != "" {
			t.Fatalf("RunTodo show --node %q stdout = %q, want empty", node, stdout)
		}
		if stderr != "" {
			t.Fatalf("RunTodo show --node %q stderr = %q, want empty", node, stderr)
		}
		if !strings.Contains(err.Error(), "invalid todo node name") {
			t.Fatalf("RunTodo show --node %q error = %q, want invalid todo node name", node, err.Error())
		}
	}
}
