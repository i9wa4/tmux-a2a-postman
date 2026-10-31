package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/cli"
)

func TestSplitCommand_RequiresExplicitSubcommand(t *testing.T) {
	command, args, ok := splitCommand(nil)
	if ok {
		t.Fatalf("splitCommand(nil) ok = true, want false (command=%q args=%v)", command, args)
	}

	command, args, ok = splitCommand([]string{"send", "--to", "worker"})
	if !ok {
		t.Fatal("splitCommand returned ok = false, want true")
	}
	if command != "send" {
		t.Fatalf("command = %q, want send", command)
	}
	if len(args) != 2 || args[0] != "--to" || args[1] != "worker" {
		t.Fatalf("args = %v, want [--to worker]", args)
	}
}

func TestPrintUsage_ShowsReducedPublicSurface(t *testing.T) {
	var stderr bytes.Buffer
	var helpOverview bytes.Buffer

	printUsage(&stderr)
	if err := cli.WriteHelp(&helpOverview, io.Discard, nil); err != nil {
		t.Fatalf("WriteHelp: %v", err)
	}

	got := stderr.String()
	if got != helpOverview.String() {
		t.Fatalf("usage should be the help overview SSOT\nusage:\n%s\nhelp:\n%s", got, helpOverview.String())
	}
	for _, want := range []string{
		"Bare tmux-a2a-postman prints usage.",
		"send-heredoc", "pop", "get-status", "execute-bash", "version",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("usage missing active command guidance %q: %q", want, got)
		}
	}
	for _, hidden := range []string{"\n  status", "\n  read", "\n  todo", "timeline", "replay", "schema", "bind", "supervisor-drain"} {
		if strings.Contains(got, hidden) {
			t.Fatalf("usage exposes hidden surface %q: %q", hidden, got)
		}
	}
	if strings.Contains(got, "Start tmux-a2a-postman daemon (default)") {
		t.Fatalf("usage still claims start is the default: %q", got)
	}
	if strings.Contains(got, "--context-id") {
		t.Fatalf("usage still exposes hidden context override: %q", got)
	}
	for _, removed := range []string{"--no-tui", "--log-file", "--base-dir", "--state-home"} {
		if strings.Contains(got, removed) {
			t.Fatalf("usage still exposes removed root flag %s: %q", removed, got)
		}
	}
	if strings.Contains(got, "get-context-id") {
		t.Fatalf("usage still exposes get-context-id in the default operator surface: %q", got)
	}
}
