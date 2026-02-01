package main

import (
	"strings"
	"testing"
	"time"
)

func TestExpandVariables(t *testing.T) {
	template := "Message from {sender} to {recipient} at {timestamp}"
	vars := map[string]string{
		"sender":    "orchestrator",
		"recipient": "worker",
		"timestamp": "2026-02-02T01:00:00",
	}

	got := ExpandVariables(template, vars)
	want := "Message from orchestrator to worker at 2026-02-02T01:00:00"

	if got != want {
		t.Errorf("ExpandVariables() = %q, want %q", got, want)
	}
}

func TestExpandVariables_Undefined(t *testing.T) {
	template := "Known: {known}, Unknown: {unknown}"
	vars := map[string]string{
		"known": "value",
	}

	got := ExpandVariables(template, vars)
	want := "Known: value, Unknown: {unknown}"

	if got != want {
		t.Errorf("ExpandVariables() = %q, want %q", got, want)
	}
}

func TestExpandVariables_Empty(t *testing.T) {
	template := ""
	vars := map[string]string{}

	got := ExpandVariables(template, vars)
	want := ""

	if got != want {
		t.Errorf("ExpandVariables() = %q, want %q", got, want)
	}
}

func TestExpandShellCommands(t *testing.T) {
	template := "Current user: $(whoami)"
	timeout := 5 * time.Second

	got := ExpandShellCommands(template, timeout)

	// Verify the pattern was replaced (result should not contain $(...))
	if strings.Contains(got, "$(") {
		t.Errorf("ExpandShellCommands() still contains $(: %q", got)
	}

	// Verify it starts with expected prefix
	if !strings.HasPrefix(got, "Current user: ") {
		t.Errorf("ExpandShellCommands() = %q, want prefix %q", got, "Current user: ")
	}

	// Verify no trailing newline
	if strings.HasSuffix(got, "\n") {
		t.Errorf("ExpandShellCommands() has trailing newline: %q", got)
	}
}

func TestExpandShellCommands_Timeout(t *testing.T) {
	template := "Result: $(sleep 10 && echo done)"
	timeout := 100 * time.Millisecond

	got := ExpandShellCommands(template, timeout)
	want := "Result: "

	if got != want {
		t.Errorf("ExpandShellCommands() timeout = %q, want %q", got, want)
	}
}

func TestExpandShellCommands_Failure(t *testing.T) {
	template := "Result: $(exit 1)"
	timeout := 5 * time.Second

	got := ExpandShellCommands(template, timeout)
	want := "Result: "

	if got != want {
		t.Errorf("ExpandShellCommands() failure = %q, want %q", got, want)
	}
}

func TestExpandTemplate(t *testing.T) {
	template := "User: $(whoami), Sender: {sender}, Recipient: {recipient}"
	vars := map[string]string{
		"sender":    "orchestrator",
		"recipient": "worker",
	}
	timeout := 5 * time.Second

	got := ExpandTemplate(template, vars, timeout)

	// Verify shell command was executed (no $(...) left)
	if strings.Contains(got, "$(") {
		t.Errorf("ExpandTemplate() still contains $(: %q", got)
	}

	// Verify variables were expanded
	if !strings.Contains(got, "orchestrator") {
		t.Errorf("ExpandTemplate() missing 'orchestrator': %q", got)
	}
	if !strings.Contains(got, "worker") {
		t.Errorf("ExpandTemplate() missing 'worker': %q", got)
	}

	// Verify no {variable} patterns left (for defined variables)
	if strings.Contains(got, "{sender}") {
		t.Errorf("ExpandTemplate() still contains {sender}: %q", got)
	}
	if strings.Contains(got, "{recipient}") {
		t.Errorf("ExpandTemplate() still contains {recipient}: %q", got)
	}
}
