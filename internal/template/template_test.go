package template

import (
	"context"
	"errors"
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

func TestExpandShellCommandsWithExecutor_ReplacesCommandsInOrder(t *testing.T) {
	var commands []string
	executor := func(ctx context.Context, command string) ([]byte, error) {
		commands = append(commands, command)
		return []byte(command), nil
	}

	got := expandShellCommandsWithExecutor(
		"First: $(one), Second: $(two)",
		5*time.Second,
		executor,
	)
	want := "First: one, Second: two"

	if got != want {
		t.Errorf("expandShellCommandsWithExecutor() = %q, want %q", got, want)
	}
	if len(commands) != 2 || commands[0] != "one" || commands[1] != "two" {
		t.Errorf("executor commands = %#v, want %#v", commands, []string{"one", "two"})
	}
}

func TestExpandShellCommandsWithExecutor_TrimTrailingNewlines(t *testing.T) {
	executor := func(ctx context.Context, command string) ([]byte, error) {
		return []byte("value\n\n"), nil
	}

	got := expandShellCommandsWithExecutor("Result: $(echo value)", 5*time.Second, executor)
	want := "Result: value"

	if got != want {
		t.Errorf("expandShellCommandsWithExecutor() = %q, want %q", got, want)
	}
}

func TestExpandShellCommandsWithExecutor_FailureExpandsEmpty(t *testing.T) {
	executor := func(ctx context.Context, command string) ([]byte, error) {
		return nil, errors.New("command failed")
	}

	got := expandShellCommandsWithExecutor("Result: $(exit 1)", 5*time.Second, executor)
	want := "Result: "

	if got != want {
		t.Errorf("expandShellCommandsWithExecutor() failure = %q, want %q", got, want)
	}
}

func TestExpandShellCommandsWithExecutor_TimeoutExpandsEmpty(t *testing.T) {
	timeout := 100 * time.Millisecond
	executor := func(ctx context.Context, command string) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("executor context has no deadline")
		}
		if remaining := time.Until(deadline); remaining <= 0 || remaining > timeout {
			t.Fatalf("executor deadline remaining = %v, want within %v", remaining, timeout)
		}
		return nil, context.DeadlineExceeded
	}

	got := expandShellCommandsWithExecutor("Result: $(slow)", timeout, executor)
	want := "Result: "

	if got != want {
		t.Errorf("expandShellCommandsWithExecutor() timeout = %q, want %q", got, want)
	}
}

func TestExpandTemplateWithExecutor_ShellDisabledDoesNotExecute(t *testing.T) {
	executor := func(ctx context.Context, command string) ([]byte, error) {
		t.Fatalf("executor called with %q while shell expansion is disabled", command)
		return nil, nil
	}
	vars := map[string]string{"node": "worker"}

	got := expandTemplateWithExecutor(
		"Command: $(echo no), Node: {node}",
		vars,
		5*time.Second,
		false,
		executor,
	)
	want := "Command: $(echo no), Node: worker"

	if got != want {
		t.Errorf("expandTemplateWithExecutor() = %q, want %q", got, want)
	}
}

func TestExpandTemplate_SanitizesInjection(t *testing.T) {
	tmpl := "Node: {node}"
	vars := map[string]string{
		"node": "evil$(rm -rf /)",
	}

	got := ExpandTemplate(tmpl, vars, 5*time.Second, false)
	want := "Node: evil"

	if got != want {
		t.Errorf("ExpandTemplate() injection = %q, want %q", got, want)
	}
}

func TestExpandTemplateWithExecutor_SanitizesInjection(t *testing.T) {
	executor := func(ctx context.Context, command string) ([]byte, error) {
		return []byte("ignored"), nil
	}
	tmpl := "Node: {node}"
	vars := map[string]string{
		"node": "evil$(rm -rf /)",
	}

	got := expandTemplateWithExecutor(tmpl, vars, 5*time.Second, true, executor)
	want := "Node: evil"

	if got != want {
		t.Errorf("expandTemplateWithExecutor() injection = %q, want %q", got, want)
	}
}

func TestExpandTemplateWithExecutor_ExpandsShellAndVariables(t *testing.T) {
	executor := func(ctx context.Context, command string) ([]byte, error) {
		if command != "whoami" {
			t.Fatalf("executor command = %q, want %q", command, "whoami")
		}
		return []byte("current-user\n"), nil
	}
	template := "User: $(whoami), Sender: {sender}, Recipient: {recipient}"
	vars := map[string]string{
		"sender":    "orchestrator",
		"recipient": "worker",
	}

	got := expandTemplateWithExecutor(template, vars, 5*time.Second, true, executor)
	want := "User: current-user, Sender: orchestrator, Recipient: worker"

	if got != want {
		t.Errorf("expandTemplateWithExecutor() = %q, want %q", got, want)
	}
}
