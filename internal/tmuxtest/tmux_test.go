package tmuxtest

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestFakeTmuxProvidesPaneOutputsAndRecordsInvocations(t *testing.T) {
	fake := Install(t, WithPane(Pane{
		ID:             "%42",
		SessionName:    "review",
		Title:          "worker",
		ContextID:      "ctx-test",
		CurrentCommand: "bash",
		Capture:        "captured pane\n",
	}))

	assertTmuxOutput(t, "review\n", "display-message", "-t", "%42", "-p", "#{session_name}")
	assertTmuxOutput(t, "worker\n", "display-message", "-t", "%42", "-p", "#{pane_title}")
	assertTmuxOutput(t, "%42\n", "display-message", "-t", "%42", "-p", "#{pane_id}")
	assertTmuxOutput(t, "bash\n", "display-message", "-t", "%42", "-p", "#{pane_current_command}")
	assertTmuxOutput(t, "%42\tctx-test\treview\tworker\n", "list-panes", "-a", "-F", "#{pane_id}\t#{@a2a_context_id}\t#{session_name}\t#{pane_title}")
	assertTmuxOutput(t, "%42 worker\n", "list-panes", "-s", "-t", "review", "-F", "#{pane_id} #{pane_title}")
	assertTmuxOutput(t, "captured pane\n", "capture-pane", "-p", "-t", "%42")
	runTmux(t, "send-keys", "-t", "%42", "C-m")
	runTmux(t, "set-option", "-p", "-t", "%42", "@a2a_context_id", "ctx-test")

	want := []string{
		"display-message -t %42 -p #{session_name}",
		"display-message -t %42 -p #{pane_title}",
		"display-message -t %42 -p #{pane_id}",
		"display-message -t %42 -p #{pane_current_command}",
		"list-panes -a -F #{pane_id}\t#{@a2a_context_id}\t#{session_name}\t#{pane_title}",
		"list-panes -s -t review -F #{pane_id} #{pane_title}",
		"capture-pane -p -t %42",
		"send-keys -t %42 C-m",
		"set-option -p -t %42 @a2a_context_id ctx-test",
	}
	if got := fake.Invocations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Invocations() = %#v, want %#v", got, want)
	}
}

func TestFakeTmuxReturnsConfiguredCommandErrors(t *testing.T) {
	Install(t, WithCommand(Command{
		Args:     []string{"display-message", "-p", "boom"},
		Stderr:   "tmux failed\n",
		ExitCode: 7,
	}))

	cmd := exec.Command("tmux", "display-message", "-p", "boom")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("tmux command error = nil, want failure; output=%q", out)
	}
	if !strings.Contains(string(out), "tmux failed") {
		t.Fatalf("tmux combined output = %q, want stderr", out)
	}
}

func TestConfiguredCommandsOverridePaneDefaults(t *testing.T) {
	Install(
		t,
		WithCommand(Command{
			Args:   []string{"display-message", "-p", "#{session_name}"},
			Stdout: "override\n",
		}),
		WithPane(Pane{SessionName: "default-session"}),
	)

	assertTmuxOutput(t, "override\n", "display-message", "-p", "#{session_name}")
}

func TestInstallMissingTmuxHidesSystemTmux(t *testing.T) {
	InstallMissing(t)

	if path, err := exec.LookPath("tmux"); err == nil {
		t.Fatalf("LookPath(tmux) = %q, want missing tmux", path)
	}
}

func assertTmuxOutput(t *testing.T, want string, args ...string) {
	t.Helper()
	got := runTmux(t, args...)
	if got != want {
		t.Fatalf("tmux %v output = %q, want %q", args, got, want)
	}
}

func runTmux(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}
