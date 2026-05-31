package tmuxrunner_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/tmuxrunner"
	"github.com/i9wa4/tmux-a2a-postman/internal/tmuxtest"
)

func TestCombinedOutputReturnsSuccessfulOutputAndRecordsInvocation(t *testing.T) {
	fake := tmuxtest.Install(t, tmuxtest.WithCommand(tmuxtest.Command{
		Args:   []string{"display-message", "-p", "ok"},
		Stdout: "ok\n",
	}))

	out, err := tmuxrunner.CombinedOutput("display-message", "-p", "ok")
	if err != nil {
		t.Fatalf("CombinedOutput: %v\n%s", err, string(out))
	}
	if string(out) != "ok\n" {
		t.Fatalf("output = %q, want ok newline", string(out))
	}

	invocations := fake.Invocations()
	if len(invocations) != 1 || invocations[0] != "display-message -p ok" {
		t.Fatalf("invocations = %#v, want one display-message command", invocations)
	}
}

func TestCombinedOutputReturnsStderrOnCommandError(t *testing.T) {
	tmuxtest.Install(t, tmuxtest.WithCommand(tmuxtest.Command{
		Args:     []string{"display-message", "-p", "bad"},
		Stdout:   "stdout\n",
		Stderr:   "stderr\n",
		ExitCode: 7,
	}))

	out, err := tmuxrunner.CombinedOutput("display-message", "-p", "bad")
	if err == nil {
		t.Fatalf("CombinedOutput succeeded unexpectedly with output %q", string(out))
	}
	if !strings.Contains(string(out), "stdout\n") {
		t.Fatalf("output = %q, want stdout", string(out))
	}
	if !strings.Contains(string(out), "stderr\n") {
		t.Fatalf("output = %q, want stderr", string(out))
	}
}

func TestCommandCombinedOutputTimesOut(t *testing.T) {
	binPath := writeExecutable(t, "tmux", "#!/bin/sh\nwhile :; do :; done\n")

	start := time.Now()
	out, err := tmuxrunner.Command{
		Binary:  binPath,
		Timeout: 20 * time.Millisecond,
	}.CombinedOutput("list-panes")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("CombinedOutput succeeded unexpectedly with output %q", string(out))
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
	if elapsed > time.Second {
		t.Fatalf("timeout took %s, want under 1s", elapsed)
	}
}

func writeExecutable(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	return path
}
