package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunHelp_DefaultOverview(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := runHelp(&stdout, &stderr, nil); err != nil || stderr.Len() != 0 {
		t.Fatalf("overview: %v %q", err, stderr.String())
	}
	for _, want := range []string{"file-first tmux workspace mail", "send-heredoc", "pop", "get-status", "execute-bash", "--postman-md"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("overview missing %q", want)
		}
	}
	for _, obsolete := range []string{"daemon-submit", "get-status-oneline", "tmux-a2a-postman start"} {
		if strings.Contains(stdout.String(), obsolete) {
			t.Fatalf("overview retains %q", obsolete)
		}
	}
}

func TestRunHelp_CommandsShowsFileFirstSurface(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := runHelp(&stdout, &stderr, []string{"commands"}); err != nil || stderr.Len() != 0 {
		t.Fatalf("commands: %v %q", err, stderr.String())
	}
	for _, want := range []string{"send-heredoc", "pop", "get-status", "execute-bash", "notice outcome", "--postman-md"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("commands missing %q", want)
		}
	}
	for _, obsolete := range []string{"daemon-submit", "--context-id", "--debug", "--label"} {
		if strings.Contains(stdout.String(), obsolete) {
			t.Fatalf("commands retain %q", obsolete)
		}
	}
}

func TestRunHelp_HelpTopicSeparatesFileFirstAndLegacy(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := runHelp(&stdout, &stderr, []string{"help"}); err != nil {
		t.Fatalf("runHelp: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	for _, want := range []string{"File-first topics:", "\n  execute-bash\n", "legacy daemon-backed commands"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q: %q", want, stdout.String())
		}
	}
}

func TestRunHelp_ConfigShowsFileFirstTopology(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := runHelp(&stdout, &stderr, []string{"config"}); err != nil {
		t.Fatalf("runHelp: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"file-first workspace topology",
		"$XDG_CONFIG_HOME/tmux-a2a-postman/postman.md",
		"--postman-md",
		"Mermaid subset",
		"command_approver_node",
		"legacy postman.toml daemon configuration",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout missing %q: %q", want, got)
		}
	}
	for _, stale := range []string{
		"visible_state in get-status JSON",
		"auto_approved_no_reviewer",
		"--override-approval",
	} {
		if strings.Contains(got, stale) {
			t.Fatalf("stdout still contains stale config field %q: %q", stale, got)
		}
	}
}

func TestRunHelp_UnknownTopicWritesGuidance(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := runHelp(&stdout, &stderr, []string{"mystery"})
	if err == nil {
		t.Fatal("runHelp returned nil error for unknown topic")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), `unknown help topic: "mystery"`) {
		t.Fatalf("stderr missing unknown-topic line: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Available topics:") {
		t.Fatalf("stderr missing available-topics section: %q", stderr.String())
	}
}
