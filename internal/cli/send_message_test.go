package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

func shellSensitiveBodyForSendTest() string {
	return "literal command substitution: $(printf SHOULD_NOT_RUN)\n" +
		"literal backticks: `date`\n" +
		"literal HOME variable: $HOME\n" +
		"quotes: \"double\" and 'single'\n" +
		"code fence:\n" +
		"```sh\n" +
		"echo \"$HOME\" && printf '%s\\n' `date`\n" +
		"```\n" +
		"multiline shell example:\n" +
		"cat <<'EOF'\n" +
		"echo \"$HOME\"\n" +
		"EOF\n"
}

func withSendHeredocBody(t *testing.T, body string, fn func() error) error {
	t.Helper()

	previousStdin := sendBodyStdin
	previousIsTerminal := sendBodyStdinIsTerminal
	sendBodyStdin = strings.NewReader(body)
	sendBodyStdinIsTerminal = func() bool { return false }
	defer func() {
		sendBodyStdin = previousStdin
		sendBodyStdinIsTerminal = previousIsTerminal
	}()

	return fn()
}

func runSendHeredocWithBody(t *testing.T, body string, args []string) error {
	t.Helper()
	return withSendHeredocBody(t, body, func() error {
		return RunSendHeredoc(args)
	})
}

func captureSendHeredocWithBody(t *testing.T, body string, args []string) (string, string, error) {
	t.Helper()
	return captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, body, args)
	})
}

func testSendCommandContext(baseDir string, stdin io.Reader, stdout io.Writer) commandContext {
	return commandContext{
		stdin:  stdin,
		stdout: stdout,
		stderr: io.Discard,
		stdinIsTerminal: func(io.Reader) bool {
			return false
		},
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{
				BaseDir: baseDir,
				Edges:   []string{"messenger --- worker"},
			}, nil
		},
		resolveContextID: func(contextID string) (string, error) {
			return contextID, nil
		},
		contextHasLiveDaemon: func(string, string) bool {
			return false
		},
		getTmuxPaneName: func() string {
			return "messenger"
		},
		getTmuxSessionName: func() string {
			return "review"
		},
		getTmuxPaneID: func() string {
			return "%1"
		},
	}
}

func TestRunSendHeredocWithContextWritesJSONToConfiguredStdout(t *testing.T) {
	tmpDir := t.TempDir()
	var stdout strings.Builder

	err := runSendHeredocWithContext(testSendCommandContext(tmpDir, strings.NewReader("hello from context"), &stdout), []string{
		"--context-id", "ctx-send-context",
		"--to", "worker",
	})
	if err != nil {
		t.Fatalf("runSendHeredocWithContext: %v", err)
	}

	payload := decodeSendOutputForTest(t, stdout.String())
	if payload.Status != string(sendStatusQueued) {
		t.Fatalf("Status = %q, want %q", payload.Status, sendStatusQueued)
	}
	if payload.ContextID != "ctx-send-context" || payload.Session != "review" || payload.From != "messenger" || payload.To != "worker" {
		t.Fatalf("send output routing fields = %#v", payload)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-send-context", "review", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile sent message: %v", err)
	}
	if !strings.Contains(string(content), "hello from context") {
		t.Fatalf("sent message missing configured stdin body:\n%s", string(content))
	}
}

func TestRunSendHeredocWithContextRejectsConfiguredTerminalStdin(t *testing.T) {
	tmpDir := t.TempDir()
	var stdout strings.Builder
	ctx := testSendCommandContext(tmpDir, strings.NewReader("ignored"), &stdout)
	ctx.stdinIsTerminal = func(io.Reader) bool { return true }

	err := runSendHeredocWithContext(ctx, []string{"--to", "worker"})
	if err == nil {
		t.Fatal("runSendHeredocWithContext() error = nil, want terminal stdin rejection")
	}
	if !strings.Contains(err.Error(), "quoted heredoc") {
		t.Fatalf("error = %v, want quoted heredoc guidance", err)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunSendHeredocWithContextUsesDaemonSubmitDependencyWithoutDaemon(t *testing.T) {
	tmpDir := t.TempDir()
	var stdout strings.Builder
	var captured projection.DaemonSubmitRequest
	ctx := testSendCommandContext(tmpDir, strings.NewReader("daemon body"), &stdout)
	ctx.contextOwnsSession = func(string, string, string) bool { return true }
	ctx.roundTripDaemonSubmit = func(_ string, request projection.DaemonSubmitRequest, _ time.Duration) (projection.DaemonSubmitResponse, error) {
		captured = request
		return projection.DaemonSubmitResponse{
			Filename: request.Filename,
		}, nil
	}

	err := runSendHeredocWithContext(ctx, []string{
		"--context-id", "ctx-send-daemon",
		"--to", "worker",
	})
	if err != nil {
		t.Fatalf("runSendHeredocWithContext: %v", err)
	}
	if captured.Command != projection.DaemonSubmitSend {
		t.Fatalf("captured.Command = %q, want %q", captured.Command, projection.DaemonSubmitSend)
	}
	if !strings.Contains(captured.Content, "daemon body") {
		t.Fatalf("daemon-submit content missing body:\n%s", captured.Content)
	}

	payload := decodeSendOutputForTest(t, stdout.String())
	if payload.SubmitPath != projection.SubmitPathDaemon {
		t.Fatalf("SubmitPath = %q, want %q", payload.SubmitPath, projection.SubmitPathDaemon)
	}
}

func writeSendBodySourceConfig(t *testing.T, dir string) string {
	t.Helper()

	configPath := filepath.Join(dir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	return configPath
}

func TestRunSendMessage_InlineBodyFlagRejected(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	err := runSendHeredocWithBody(t, "hello", []string{"--to", "worker", "--body", "hello"})
	if err == nil {
		t.Fatal("RunSendMessage() error = nil, want undefined --body flag")
	}
	if !strings.Contains(err.Error(), "flag provided but not defined: -body") {
		t.Fatalf("RunSendMessage() error = %v, want undefined --body flag", err)
	}
}

func TestRunSendMessage_LegacySendWithHeredocBodyRejected(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)

	previousStdin := sendBodyStdin
	previousIsTerminal := sendBodyStdinIsTerminal
	sendBodyStdin = strings.NewReader("hello from heredoc")
	sendBodyStdinIsTerminal = func() bool { return false }
	t.Cleanup(func() {
		sendBodyStdin = previousStdin
		sendBodyStdinIsTerminal = previousIsTerminal
	})

	err := RunSendMessage([]string{"--to", "worker"})
	if err == nil {
		t.Fatal("RunSendMessage() error = nil, want legacy send rejection")
	}
	for _, want := range []string{"send no longer accepts message bodies", "send-heredoc", "shell-expansion"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("RunSendMessage() error = %v, want substring %q", err, want)
		}
	}
}

func TestRunSendMessage_FlagHelpOmitsHiddenAndRemovedFlags(t *testing.T) {
	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunSendHeredoc([]string{"-h"})
	})
	if err == nil {
		t.Fatal("RunSendHeredoc(-h) = nil, want help error")
	}
	if !strings.Contains(err.Error(), "flag: help requested") {
		t.Fatalf("RunSendHeredoc(-h) error = %v, want help requested", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "Usage of send-heredoc:") {
		t.Fatalf("stderr missing help header: %q", stderr)
	}
	if strings.Contains(stderr, "--context-id") {
		t.Fatalf("stderr still exposes hidden context override: %q", stderr)
	}
	for _, removed := range []string{"--config", "--json", "--params", "--idempotency-key", "--session", "--message-file", "--body"} {
		if strings.Contains(stderr, removed) {
			t.Fatalf("stderr still exposes hidden/removed flag %s: %q", removed, stderr)
		}
	}
}

func TestRunSendMessage_FromFlagRejected(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	err := runSendHeredocWithBody(t, "hello", []string{"--to", "worker", "--from", "orchestrator"})
	if err == nil {
		t.Fatal("expected unknown-flag error for --from, got nil")
	}
	if !strings.Contains(err.Error(), "flag provided but not defined: -from") {
		t.Fatalf("expected unknown --from flag error, got: %v", err)
	}
}

func TestRunSendMessage_InvalidToNodeName(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := writeMinimalNodeConfig(t, tmpDir)
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	err := runSendHeredocWithBody(t, "hello", []string{
		"--config", configPath,
		"--context-id", "ctx-send-invalid-to",
		"--to", "worker_alt",
	})
	if err == nil {
		t.Fatal("expected invalid --to node name error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid node name") {
		t.Fatalf("expected invalid node name error, got: %v", err)
	}

	assertNoMarkdownFilesInTree(t, filepath.Join(tmpDir, "ctx-send-invalid-to", "test-session"))
}

func TestRunSendMessage_InvalidReplyToRejectedBeforeWriting(t *testing.T) {
	tests := []struct {
		name    string
		replyTo string
		want    string
	}{
		{
			name:    "path",
			replyTo: "../previous.md",
			want:    "not a path",
		},
		{
			name:    "multi_token",
			replyTo: "previous message.md",
			want:    "single message id token",
		},
		{
			name:    "bad_filename",
			replyTo: "previous.md",
			want:    "valid message id",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv("POSTMAN_HOME", tmpDir)
			err := runSendHeredocWithBody(t, "hello", []string{
				"--to", "worker",
				"--reply-to", tt.replyTo,
			})
			if err == nil {
				t.Fatal("RunSendMessage() error = nil, want validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("RunSendMessage() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestRunSendMessage_InvalidFillsInputRequestIDRejectedBeforeWriting(t *testing.T) {
	tests := []struct {
		name           string
		flag           string
		inputRequestID string
		want           string
	}{
		{
			name:           "canonical path",
			flag:           "--fills-input-request-id",
			inputRequestID: "../ireq_123",
			want:           "path separators",
		},
		{
			name:           "canonical multi token",
			flag:           "--fills-input-request-id",
			inputRequestID: "rslot 123",
			want:           "whitespace",
		},
		{
			name: "empty",
			flag: "--fills-input-request-id",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv("POSTMAN_HOME", tmpDir)
			args := []string{
				"--to", "worker",
				tt.flag, tt.inputRequestID,
			}
			err := runSendHeredocWithBody(t, "hello", args)
			if tt.inputRequestID == "" {
				if err != nil && strings.Contains(err.Error(), tt.flag) {
					t.Fatalf("RunSendMessage() error = %v, want no input request validation error", err)
				}
				return
			}
			if err == nil {
				t.Fatal("RunSendMessage() error = nil, want validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("RunSendMessage() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestRunSendMessage_LegacyInputRequestFlagRejected(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	err := runSendHeredocWithBody(t, "hello", []string{
		"--to", "worker",
		"--satisfies" + "-obligation-id", "ireq_123",
	})
	if err == nil {
		t.Fatal("RunSendMessage() error = nil, want undefined flag error")
	}
	if !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("RunSendMessage() error = %v, want undefined flag", err)
	}
}

func TestRunSendMessage_ReplyPolicyFlagsAreMutuallyExclusive(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	err := runSendHeredocWithBody(t, "hello", []string{
		"--to", "worker",
		"--no-reply",
		"--reply-required",
	})
	if err == nil {
		t.Fatal("RunSendMessage() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("RunSendMessage() error = %v, want mutually exclusive", err)
	}
}

func TestRunSendMessage_QuotedHeredocPreservesShellSensitiveText(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := writeSendBodySourceConfig(t, tmpDir)
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	body := shellSensitiveBodyForSendTest()

	stdout, _, err := captureSendHeredocWithBody(t, body, []string{
		"--config", configPath,
		"--context-id", "ctx-quoted-heredoc",
		"--to", "worker",
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-quoted-heredoc", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	if !strings.Contains(string(content), body) {
		t.Fatalf("post content did not preserve quoted heredoc text:\n%s", string(content))
	}
}

func TestRunSendMessage_BodyStdinFlagRejected(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)

	err := runSendHeredocWithBody(t, "hello", []string{"--to", "worker", "--body-stdin"})
	if err == nil {
		t.Fatal("RunSendMessage() error = nil, want undefined --body-stdin flag")
	}
	if !strings.Contains(err.Error(), "flag provided but not defined: -body-stdin") {
		t.Fatalf("RunSendMessage() error = %v, want undefined --body-stdin flag", err)
	}
}

func TestRunSendMessage_DefaultStdinPreservesShellSensitiveText(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := writeSendBodySourceConfig(t, tmpDir)
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	body := shellSensitiveBodyForSendTest()
	previousStdin := sendBodyStdin
	previousIsTerminal := sendBodyStdinIsTerminal
	sendBodyStdin = strings.NewReader(body)
	sendBodyStdinIsTerminal = func() bool { return false }
	t.Cleanup(func() {
		sendBodyStdin = previousStdin
		sendBodyStdinIsTerminal = previousIsTerminal
	})

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunSendHeredoc([]string{
			"--config", configPath,
			"--context-id", "ctx-default-stdin",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-default-stdin", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	if !strings.Contains(string(content), body) {
		t.Fatalf("post content did not preserve default stdin text:\n%s", string(content))
	}
}

func TestRunSendMessage_StdinAliasFlagRejected(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)

	err := runSendHeredocWithBody(t, "hello", []string{"--to", "worker", "--stdin"})
	if err == nil {
		t.Fatal("RunSendMessage() error = nil, want undefined --stdin flag")
	}
	if !strings.Contains(err.Error(), "flag provided but not defined: -stdin") {
		t.Fatalf("RunSendMessage() error = %v, want undefined --stdin flag", err)
	}
}

func TestRunSendMessage_DefaultStdinRejectsInteractiveTerminal(t *testing.T) {
	previousStdin := sendBodyStdin
	previousIsTerminal := sendBodyStdinIsTerminal
	sendBodyStdin = strings.NewReader("ignored")
	sendBodyStdinIsTerminal = func() bool { return true }
	t.Cleanup(func() {
		sendBodyStdin = previousStdin
		sendBodyStdinIsTerminal = previousIsTerminal
	})

	err := RunSendHeredoc([]string{"--to", "worker"})
	if err == nil {
		t.Fatal("RunSendMessage() error = nil, want body-source guidance")
	}
	if !strings.Contains(err.Error(), "quoted heredoc") {
		t.Fatalf("RunSendMessage() error = %v, want quoted heredoc guidance", err)
	}
	if !strings.Contains(err.Error(), "send-heredoc") || !strings.Contains(err.Error(), "shell-expansion") {
		t.Fatalf("RunSendMessage() error = %v, want send-heredoc shell-expansion guidance", err)
	}
}

func TestRunSendMessage_BodyFileFlagRejected(t *testing.T) {
	tmpDir := t.TempDir()
	bodyPath := filepath.Join(tmpDir, "body.md")
	if err := os.WriteFile(bodyPath, []byte("from file"), 0o600); err != nil {
		t.Fatalf("WriteFile body: %v", err)
	}

	err := runSendHeredocWithBody(t, "hello", []string{"--to", "worker", "--body-file", bodyPath})
	if err == nil {
		t.Fatal("RunSendMessage() error = nil, want undefined --body-file flag")
	}
	if !strings.Contains(err.Error(), "flag provided but not defined: -body-file") {
		t.Fatalf("RunSendMessage() error = %v, want undefined --body-file flag", err)
	}
}

func TestRunSendMessage_RegularFileStdinAcceptedForZshHeredocCompatibility(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := writeSendBodySourceConfig(t, tmpDir)
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	body := "from regular-file stdin"
	bodyPath := filepath.Join(tmpDir, "body.md")
	if err := os.WriteFile(bodyPath, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile body: %v", err)
	}
	bodyFile, err := os.Open(bodyPath)
	if err != nil {
		t.Fatalf("Open body: %v", err)
	}
	defer func() { _ = bodyFile.Close() }()
	previousStdin := sendBodyStdin
	previousIsTerminal := sendBodyStdinIsTerminal
	sendBodyStdin = bodyFile
	sendBodyStdinIsTerminal = func() bool { return false }
	t.Cleanup(func() {
		sendBodyStdin = previousStdin
		sendBodyStdinIsTerminal = previousIsTerminal
	})

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunSendHeredoc([]string{
			"--config", configPath,
			"--context-id", "ctx-regular-file-stdin",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendHeredoc() error = %v, want regular file stdin accepted", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-regular-file-stdin", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	if !strings.Contains(string(content), body) {
		t.Fatalf("post content did not preserve regular-file stdin body:\n%s", string(content))
	}
}

func TestRunSendMessage_QuotedHeredocDashRemainsLiteralBody(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := writeSendBodySourceConfig(t, tmpDir)
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	stdout, _, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "-", []string{
			"--config", configPath,
			"--context-id", "ctx-body-dash",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-body-dash", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	stored := string(content)
	separator := "\n---\n\n"
	separatorIndex := strings.LastIndex(stored, separator)
	if separatorIndex < 0 {
		t.Fatalf("stored message missing visible body separator:\n%s", stored)
	}
	if bodyAfterSeparator := stored[separatorIndex+len(separator):]; bodyAfterSeparator != "-" {
		t.Fatalf("quoted heredoc dash content was not preserved as literal body:\n got %q\ncontent:\n%s", bodyAfterSeparator, stored)
	}
}

func TestRunSendMessage_InvalidAutoDetectedPaneTitle(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := writeMinimalNodeConfig(t, tmpDir)
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger_alt")

	err := runSendHeredocWithBody(t, "hello", []string{
		"--config", configPath,
		"--context-id", "ctx-send-invalid-pane",
		"--to", "worker",
	})
	if err == nil {
		t.Fatal("expected invalid auto-detected pane title error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid node name") {
		t.Fatalf("expected invalid node name error, got: %v", err)
	}

	assertNoMarkdownFilesInTree(t, filepath.Join(tmpDir, "ctx-send-invalid-pane", "test-session"))
}

func TestSendMessage_MissingSender(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["orchestrator --- worker"]

[messenger]
role = "messenger"

[orchestrator]
role = "orchestrator"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	err := runSendHeredocWithBody(t, "hello", []string{
		"--config", configPath,
		"--context-id", "ctx-send-missing-sender",
		"--to", "worker",
	})
	if err == nil {
		t.Fatal("expected missing sender error, got nil")
	}
	if !strings.Contains(err.Error(), "missing sender") {
		t.Fatalf("expected missing sender error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "\"messenger\"") {
		t.Fatalf("expected missing sender error to name messenger, got: %v", err)
	}

	assertNoMarkdownFilesInTree(t, filepath.Join(tmpDir, "ctx-send-missing-sender", "test-session"))
}

func TestSendMessage_MissingReceiver(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- orchestrator"]

[messenger]
role = "messenger"

[orchestrator]
role = "orchestrator"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	err := runSendHeredocWithBody(t, "hello", []string{
		"--config", configPath,
		"--context-id", "ctx-send-missing-receiver",
		"--to", "worker",
	})
	if err == nil {
		t.Fatal("expected missing receiver error, got nil")
	}
	if !strings.Contains(err.Error(), "missing receiver") {
		t.Fatalf("expected missing receiver error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "\"worker\"") {
		t.Fatalf("expected missing receiver error to name worker, got: %v", err)
	}

	assertNoMarkdownFilesInTree(t, filepath.Join(tmpDir, "ctx-send-missing-receiver", "test-session"))
}

func TestSendMessage_InvalidEdge(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- orchestrator --- worker"]

[messenger]
role = "messenger"

[orchestrator]
role = "orchestrator"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	err := runSendHeredocWithBody(t, "hello", []string{
		"--config", configPath,
		"--context-id", "ctx-send-invalid-edge",
		"--to", "worker",
	})
	if err == nil {
		t.Fatal("expected edge violation error, got nil")
	}
	if !strings.Contains(err.Error(), "edge violation") {
		t.Fatalf("expected edge violation error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "allowed recipients: orchestrator") {
		t.Fatalf("expected edge violation error to name allowed recipients, got: %v", err)
	}

	assertNoMarkdownFilesInTree(t, filepath.Join(tmpDir, "ctx-send-invalid-edge", "test-session"))
}

func TestSendMessage_AllowsSessionPrefixedGraphKeys(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["test-session:messenger --- review-session:worker"]

["test-session:messenger"]
role = "messenger"

["review-session:worker"]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	if err := runSendHeredocWithBody(t, "hello", []string{
		"--config", configPath,
		"--context-id", "ctx-send-prefixed-graph",
		"--to", "review-session:worker",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-send-prefixed-graph", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}
	if !strings.Contains(entries[0].Name(), "-to-review-session:worker.md") {
		t.Fatalf("post filename missing session-prefixed recipient: %q", entries[0].Name())
	}
}

func TestSendMessage_WorkspaceParentAliasCompilesToExplicitRecipient(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["test-session:messenger --- repo-session:orchestrator"]

[[postman.workspace_tree]]
session = "repo-session"
label = "repo"

[[postman.workspace_tree]]
session = "test-session"
label = "api"
parent = "repo-session"

["test-session:messenger"]
role = "messenger"

["repo-session:orchestrator"]
role = "orchestrator"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	if err := runSendHeredocWithBody(t, "hello", []string{
		"--config", configPath,
		"--context-id", "ctx-send-workspace-parent",
		"--to", "@parent/orchestrator",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-send-workspace-parent", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}
	if !strings.Contains(entries[0].Name(), "-to-repo-session:orchestrator.md") {
		t.Fatalf("post filename missing compiled recipient: %q", entries[0].Name())
	}
	content, err := os.ReadFile(filepath.Join(postDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile sent message: %v", err)
	}
	if !strings.Contains(string(content), "to: repo-session:orchestrator") {
		t.Fatalf("message frontmatter missing compiled recipient: %s", string(content))
	}
}

func TestSendMessage_WorkspaceParentAliasDefaultsToRepresentativeAndHintsChildAlias(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
message_footer = """You can talk to:
{contacts_section}
"""
edges = ["test-session:messenger --- repo-session:orchestrator"]

[[postman.workspace_tree]]
session = "repo-session"
label = "repo"
representative = "orchestrator"

[[postman.workspace_tree]]
session = "test-session"
label = "api"
parent = "repo-session"
representative = "messenger"

["test-session:messenger"]
role = "Project representative."

["repo-session:orchestrator"]
role = "Root representative."
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	if err := runSendHeredocWithBody(t, "hello", []string{
		"--config", configPath,
		"--context-id", "ctx-send-workspace-parent-default",
		"--to", "@parent",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-send-workspace-parent-default", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}
	if !strings.Contains(entries[0].Name(), "-to-repo-session:orchestrator.md") {
		t.Fatalf("post filename missing compiled representative: %q", entries[0].Name())
	}
	content, err := os.ReadFile(filepath.Join(postDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile sent message: %v", err)
	}
	if !strings.Contains(string(content), "to: repo-session:orchestrator") {
		t.Fatalf("message frontmatter missing compiled representative: %s", string(content))
	}
	if !strings.Contains(string(content), "- @child/api: test-session:messenger - Project representative.") {
		t.Fatalf("message footer missing child relationship alias: %s", string(content))
	}
}

func TestSendMessage_WorkspaceChildAliasDefaultsToRepresentativeAndHintsParentAlias(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
message_footer = """You can talk to:
{contacts_section}
"""
edges = ["repo-session:orchestrator --- test-session:worker"]

[[postman.workspace_tree]]
session = "repo-session"
label = "repo"
representative = "orchestrator"

[[postman.workspace_tree]]
session = "test-session"
label = "api"
parent = "repo-session"
representative = "worker"

["repo-session:orchestrator"]
role = "Root representative."

["test-session:worker"]
role = "Project representative."
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "repo-session", "orchestrator")

	if err := runSendHeredocWithBody(t, "hello", []string{
		"--config", configPath,
		"--context-id", "ctx-send-workspace-child-default",
		"--to", "@child/api",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-send-workspace-child-default", "repo-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}
	if !strings.Contains(entries[0].Name(), "-to-test-session:worker.md") {
		t.Fatalf("post filename missing compiled representative: %q", entries[0].Name())
	}
	content, err := os.ReadFile(filepath.Join(postDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile sent message: %v", err)
	}
	if !strings.Contains(string(content), "to: test-session:worker") {
		t.Fatalf("message frontmatter missing compiled representative: %s", string(content))
	}
	if !strings.Contains(string(content), "- @parent: repo-session:orchestrator - Root representative.") {
		t.Fatalf("message footer missing parent relationship alias: %s", string(content))
	}
}

func TestSendMessage_AllowsMixedSenderGraphKeys(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = [
  "messenger --- orchestrator",
  "test-session:messenger --- review-session:worker",
]

[messenger]
role = "messenger"

[orchestrator]
role = "orchestrator"

["test-session:messenger"]
role = "messenger"

["review-session:worker"]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	if err := runSendHeredocWithBody(t, "hello", []string{
		"--config", configPath,
		"--context-id", "ctx-send-mixed-sender-graph",
		"--to", "review-session:worker",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-send-mixed-sender-graph", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}
	if !strings.Contains(entries[0].Name(), "-to-review-session:worker.md") {
		t.Fatalf("post filename missing session-prefixed recipient: %q", entries[0].Name())
	}
}

func TestSendMessage_PrefixedRecipientRequiresExplicitGraphKey(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["test-session:messenger --- worker"]

[messenger]
role = "messenger"

["test-session:messenger"]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	err := runSendHeredocWithBody(t, "hello", []string{
		"--config", configPath,
		"--context-id", "ctx-send-prefixed-recipient-needs-explicit-edge",
		"--to", "review-session:worker",
	})
	if err == nil {
		t.Fatal("expected missing receiver error, got nil")
	}
	if !strings.Contains(err.Error(), "missing receiver") {
		t.Fatalf("expected missing receiver error, got: %v", err)
	}

	assertNoMarkdownFilesInTree(t, filepath.Join(tmpDir, "ctx-send-prefixed-recipient-needs-explicit-edge", "test-session"))
}

func TestSendMessage_AllowsSameSessionFullGraphKeyForBareRecipient(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["test-session:messenger --- test-session:worker"]

["test-session:messenger"]
role = "messenger"

["test-session:worker"]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	if err := runSendHeredocWithBody(t, "hello", []string{
		"--config", configPath,
		"--context-id", "ctx-send-full-same-session-graph",
		"--to", "worker",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-send-full-same-session-graph", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}
	if !strings.Contains(entries[0].Name(), "-to-worker.md") {
		t.Fatalf("post filename missing bare recipient: %q", entries[0].Name())
	}
}

func TestSendMessage_AllowsBareGraphKeyForSameSessionPrefixedRecipient(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	if err := runSendHeredocWithBody(t, "hello", []string{
		"--config", configPath,
		"--context-id", "ctx-send-bare-graph-same-session-prefixed-recipient",
		"--to", "test-session:worker",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-send-bare-graph-same-session-prefixed-recipient", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}
	if !strings.Contains(entries[0].Name(), "-to-test-session:worker.md") {
		t.Fatalf("post filename missing session-prefixed recipient: %q", entries[0].Name())
	}
}

func TestResolveInboxPath_InvalidAutoDetectedPaneTitle(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := writeMinimalNodeConfig(t, tmpDir)
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker_alt")

	_, err := cliutil.ResolveInboxPath([]string{
		"--config", configPath,
		"--context-id", "ctx-resolve-invalid-pane",
	})
	if err == nil {
		t.Fatal("expected invalid auto-detected pane title error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid node name") {
		t.Fatalf("expected invalid node name error, got: %v", err)
	}
}

func TestRunSendMessage_IdempotencyKeyFlagRejected(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	err := runSendHeredocWithBody(t, "hello", []string{"--to", "worker", "--idempotency-key", "key-abc-123"})
	if err == nil {
		t.Fatal("expected unknown-flag error for --idempotency-key, got nil")
	}
	if !strings.Contains(err.Error(), "flag provided but not defined: -idempotency-key") {
		t.Fatalf("expected unknown --idempotency-key flag error, got: %v", err)
	}
}

func TestRunSendMessage_DraftTemplateExpandsReplyCommand(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
reply_command = "send-heredoc --to <recipient>"
draft_template = "{reply_command}"
message_footer = ""
edges = ["orchestrator --- worker"]

[orchestrator]
role = "orchestrator"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "orchestrator")

	if err := runSendHeredocWithBody(t, "hello", []string{
		"--config", configPath,
		"--context-id", "ctx-draft-reply",
		"--to", "worker",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-draft-reply", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}

	draftPath := filepath.Join(postDir, entries[0].Name())
	draftContent, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatalf("ReadFile draft: %v", err)
	}
	if !strings.Contains(string(draftContent), "send-heredoc --to worker") {
		t.Fatalf("draft content missing reply command: %q", string(draftContent))
	}
}

func TestRunSendMessage_DraftTemplatePreservesMultilineReplyCommand(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
reply_command = """
tmux-a2a-postman send-heredoc --to <recipient> <<'POSTMAN_BODY'
<your message>
POSTMAN_BODY
"""
draft_template = "{reply_command}"
message_footer = ""
edges = ["orchestrator --- worker"]

[orchestrator]
role = "orchestrator"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "orchestrator")

	if err := runSendHeredocWithBody(t, "hello", []string{
		"--config", configPath,
		"--context-id", "ctx-draft-multiline",
		"--to", "worker",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-draft-multiline", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}

	draftPath := filepath.Join(postDir, entries[0].Name())
	draftContent, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatalf("ReadFile draft: %v", err)
	}
	want := "tmux-a2a-postman send-heredoc --to worker <<'POSTMAN_BODY'\n<your message>\nPOSTMAN_BODY"
	if !strings.Contains(string(draftContent), want) {
		t.Fatalf("draft content missing preserved multiline reply command:\n%s", string(draftContent))
	}
}

func TestRunSendMessage_MessageFooterUsesRecipientReachability(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
reply_command = "tmux-a2a-postman send-heredoc --to <recipient>"
draft_template = "# Content\n\n"
message_footer = """You can talk to: {can_talk_to}
Reply: {reply_command}
"""
edges = ["messenger --- orchestrator --- boss"]

[messenger]
role = "messenger"

[orchestrator]
role = "orchestrator"

[boss]
role = "boss"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	if err := runSendHeredocWithBody(t, "hello", []string{
		"--config", configPath,
		"--context-id", "ctx-footer-recipient",
		"--to", "orchestrator",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-footer-recipient", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}

	draftPath := filepath.Join(postDir, entries[0].Name())
	draftContent, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatalf("ReadFile draft: %v", err)
	}
	content := string(draftContent)
	if !strings.Contains(content, "You can talk to: messenger, boss") {
		t.Fatalf("footer missing recipient reachability:\n%s", content)
	}
	if strings.Contains(content, "You can talk to: orchestrator") {
		t.Fatalf("footer still contains sender reachability:\n%s", content)
	}
	if !strings.Contains(content, "Reply: tmux-a2a-postman send-heredoc --to messenger") {
		t.Fatalf("footer missing recipient-scoped reply command:\n%s", content)
	}
	if strings.Contains(content, "Reply: tmux-a2a-postman send-heredoc --to orchestrator") {
		t.Fatalf("footer still contains sender-scoped reply command:\n%s", content)
	}
}

func TestRunSendMessage_MessageFooterUsesSessionPrefixedRecipientReachability(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
reply_command = "tmux-a2a-postman send-heredoc --to <recipient>"
draft_template = "# Content\n\n"
message_footer = """You can talk to: {can_talk_to}
Reply: {reply_command}
"""
edges = ["messenger --- review-session:orchestrator --- boss"]

[messenger]
role = "messenger"

["review-session:orchestrator"]
role = "orchestrator"

[boss]
role = "boss"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	if err := runSendHeredocWithBody(t, "hello", []string{
		"--config", configPath,
		"--context-id", "ctx-footer-prefixed-recipient",
		"--to", "review-session:orchestrator",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-footer-prefixed-recipient", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}
	if !strings.Contains(entries[0].Name(), "-to-review-session:orchestrator.md") {
		t.Fatalf("post filename missing session-prefixed recipient: %q", entries[0].Name())
	}

	draftPath := filepath.Join(postDir, entries[0].Name())
	draftContent, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatalf("ReadFile draft: %v", err)
	}
	content := string(draftContent)
	if !strings.Contains(content, "You can talk to: messenger, boss") {
		t.Fatalf("footer missing session-prefixed recipient reachability:\n%s", content)
	}
	if strings.Contains(content, "You can talk to: review-session:orchestrator") {
		t.Fatalf("footer unexpectedly used recipient key instead of neighbor list:\n%s", content)
	}
	if !strings.Contains(content, "Reply: tmux-a2a-postman send-heredoc --to messenger") {
		t.Fatalf("footer missing recipient-scoped reply command:\n%s", content)
	}
}

func TestRunSendMessage_DefaultMessageFooterUsesConfiguredReplyCommand(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
reply_command = "custom-reply --context {context_id} --to <recipient>"
draft_template = "# Content\n\n"
edges = ["messenger --- orchestrator"]

[messenger]
role = "messenger"

[orchestrator]
role = "orchestrator"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	if err := runSendHeredocWithBody(t, "hello", []string{
		"--config", configPath,
		"--context-id", "ctx-default-footer-reply",
		"--to", "orchestrator",
	}); err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}

	postDir := filepath.Join(tmpDir, "ctx-default-footer-reply", "test-session", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(entries))
	}

	draftPath := filepath.Join(postDir, entries[0].Name())
	draftContent, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatalf("ReadFile draft: %v", err)
	}
	content := string(draftContent)
	if !strings.Contains(content, "Reply with quoted heredoc:\ncustom-reply --context ctx-default-footer-reply --to messenger --reply-to ") ||
		!strings.Contains(content, " <<'POSTMAN_BODY'") {
		t.Fatalf("default footer missing configured reply command:\n%s", content)
	}
	if strings.Contains(content, "Reply: tmux-a2a-postman send-heredoc --to <receiver>") {
		t.Fatalf("default footer still contains hard-coded placeholder reply command:\n%s", content)
	}
	if strings.Contains(content, "Required-reply completion gate:") {
		t.Fatalf("default footer included required-reply gate for no-reply message:\n%s", content)
	}
}

func TestRunSendMessage_DefaultEnvelopePreservesSenderMarkdownAfterSeparator(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := "[postman]\n" +
		"edges = [\"messenger --- worker\"]\n\n" +
		"[messenger]\n" +
		"role = \"messenger\"\n\n" +
		"[worker]\n" +
		"role = \"worker\"\n" +
		"template = \"\"\"# Worker Role\n\n" +
		"## Checklist\n\n" +
		"```sh\n" +
		"# keep role code literal\n" +
		"```\n" +
		"\"\"\"\n"
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	body := "# User Request\n\n---\n\n## Details\n\n```sh\n# keep body code literal\n```\n"
	stdout, _, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, body, []string{
			"--config", configPath,
			"--context-id", "ctx-markdown-envelope",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	contentBytes, err := os.ReadFile(filepath.Join(tmpDir, "ctx-markdown-envelope", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	content := string(contentBytes)
	for _, want := range []string{
		"## Recipient Instructions",
		"### Worker Role",
		"#### Checklist",
		"```sh\n# keep role code literal\n```",
		"## Sender Message",
		"You can talk to:\n- messenger: messenger",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("content missing %q:\n%s", want, content)
		}
	}
	for _, unwanted := range []string{
		"\n# Worker Role",
		"\n## Checklist",
	} {
		if strings.Contains(content, unwanted) {
			t.Fatalf("content contains unwanted %q:\n%s", unwanted, content)
		}
	}
	separator := "\n---\n\n"
	senderMessageIndex := strings.Index(content, "## Sender Message")
	if senderMessageIndex < 0 {
		t.Fatalf("content missing sender message section:\n%s", content)
	}
	separatorOffset := strings.Index(content[senderMessageIndex:], separator)
	if separatorOffset < 0 {
		t.Fatalf("content missing visible body separator:\n%s", content)
	}
	separatorIndex := senderMessageIndex + separatorOffset
	bodyAfterSeparator := content[separatorIndex+len(separator):]
	if bodyAfterSeparator != body {
		t.Fatalf("sender body after separator changed:\n got %q\nwant %q\ncontent:\n%s", bodyAfterSeparator, body, content)
	}
}

func TestRunSendMessage_DefaultEnvelopeNormalizesRecipientInstructionHeadings(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := "[postman]\n" +
		"edges = [\"messenger --- worker\"]\n\n" +
		"[messenger]\n" +
		"role = \"messenger\"\n\n" +
		"[worker]\n" +
		"role = \"worker\"\n" +
		"template = \"\"\"### 2.1. Recipient Rule\n\n" +
		"#### Detail\n\n" +
		"```text\n" +
		"### keep role code literal\n" +
		"```\n" +
		"\"\"\"\n"
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	body := "# User Request\n\n## Details\n"
	stdout, _, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, body, []string{
			"--config", configPath,
			"--context-id", "ctx-heading-envelope",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	contentBytes, err := os.ReadFile(filepath.Join(tmpDir, "ctx-heading-envelope", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	content := string(contentBytes)
	for _, want := range []string{
		"## Recipient Instructions\n\n### 2.1. Recipient Rule",
		"\n#### Detail",
		"```text\n### keep role code literal\n```",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("content missing %q:\n%s", want, content)
		}
	}
	for _, unwanted := range []string{
		"## Recipient Instructions\n\n##### 2.1. Recipient Rule",
		"\n###### Detail",
		"```text\n##### keep role code literal",
	} {
		if strings.Contains(content, unwanted) {
			t.Fatalf("content contains unwanted %q:\n%s", unwanted, content)
		}
	}
	senderMessageIndex := strings.Index(content, "## Sender Message")
	if senderMessageIndex < 0 {
		t.Fatalf("content missing sender message section:\n%s", content)
	}
	separator := "\n---\n\n"
	separatorOffset := strings.Index(content[senderMessageIndex:], separator)
	if separatorOffset < 0 {
		t.Fatalf("content missing visible body separator:\n%s", content)
	}
	bodyAfterSeparator := content[senderMessageIndex+separatorOffset+len(separator):]
	if bodyAfterSeparator != body {
		t.Fatalf("sender body after separator changed:\n got %q\nwant %q\ncontent:\n%s", bodyAfterSeparator, body, content)
	}
}

func TestRunSendMessage_AttachesSnapshotWithoutSenderRuntimeMarkdownAndPreservesSenderBody(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := writeSendBodySourceConfig(t, tmpDir)
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	body := "# User Request\n\nKeep this body verbatim.\n"
	stdout, stderr, err := captureSendHeredocWithBody(t, body, []string{
		"--config", configPath,
		"--context-id", "ctx-runtime-context-send",
		"--to", "worker",
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v\nstderr=%s", err, stderr)
	}
	payload := decodeSendOutputForTest(t, stdout)
	contentBytes, err := os.ReadFile(filepath.Join(tmpDir, "ctx-runtime-context-send", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	content := string(contentBytes)
	senderMessageIndex := strings.Index(content, "## Sender Message")
	if strings.Contains(content, "## Sender Runtime Context") {
		t.Fatalf("content rendered noisy Sender Runtime Context block:\n%s", content)
	}
	if senderMessageIndex < 0 {
		t.Fatalf("content missing Sender Message block:\n%s", content)
	}
	separator := "\n---\n\n"
	separatorOffset := strings.Index(content[senderMessageIndex:], separator)
	if separatorOffset < 0 {
		t.Fatalf("content missing visible body separator:\n%s", content)
	}
	if got := content[senderMessageIndex+separatorOffset+len(separator):]; got != body {
		t.Fatalf("sender body after separator changed:\n got %q\nwant %q\ncontent:\n%s", got, body, content)
	}

	metadata, err := envelope.ParseMetadata(content)
	if err != nil {
		t.Fatalf("ParseMetadata: %v", err)
	}
	if metadata.RuntimeContextID == "" {
		t.Fatalf("RuntimeContextID is empty in metadata:\n%s", content)
	}
	if metadata.RuntimeContextScope != "sender" {
		t.Fatalf("RuntimeContextScope = %q, want sender", metadata.RuntimeContextScope)
	}
	if metadata.RuntimeContextCapturedAt == "" || metadata.RuntimeContextHash == "" {
		t.Fatalf("runtime context refs missing captured/hash: %#v", metadata)
	}
	snapshotPath := filepath.Join(tmpDir, "ctx-runtime-context-send", "test-session", "snapshot", "runtime-context", metadata.RuntimeContextID+".json")
	snapshotBytes, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("ReadFile runtime context snapshot: %v", err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(snapshotBytes, &snapshot); err != nil {
		t.Fatalf("json.Unmarshal snapshot: %v", err)
	}
	if snapshot["semantics"] != "metadata_not_instructions" {
		t.Fatalf("snapshot semantics = %#v, want metadata_not_instructions", snapshot["semantics"])
	}
	if snapshot["content_hash"] != metadata.RuntimeContextHash {
		t.Fatalf("snapshot content_hash = %#v, metadata hash = %q", snapshot["content_hash"], metadata.RuntimeContextHash)
	}
	if _, ok := snapshot["content"].(string); ok {
		t.Fatalf("snapshot unexpectedly stores sender body content: %s", snapshotBytes)
	}
}

func TestRunSendMessage_CustomTemplatePlaceholderAfterFrontmatterKeepsFooterVisible(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]
message_footer = """Generated guidance:
{reply_command}{reply_arguments}
Can talk to: {can_talk_to}
"""
draft_template = """---
params:
  contextId: {context_id}
  from: {sender}
  to: {recipient}
  messageId: {message_id}
  replyPolicy: {reply_policy}
  timestamp: {timestamp}
  tmuxSession: {session_name}
---

<!-- write here -->
"""

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	body := "# User Request\n\n---\n\n## Details\n\n```sh\n# keep body code literal\n```\n"
	stdout, _, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, body, []string{
			"--config", configPath,
			"--context-id", "ctx-frontmatter-placeholder",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	contentBytes, err := os.ReadFile(filepath.Join(tmpDir, "ctx-frontmatter-placeholder", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	content := string(contentBytes)
	frontmatterClose := strings.Index(content, "\n---\n")
	footerIndex := strings.Index(content, "Generated guidance:")
	if frontmatterClose < 0 || footerIndex < 0 {
		t.Fatalf("content missing frontmatter close or generated footer:\n%s", content)
	}
	if footerIndex < frontmatterClose {
		t.Fatalf("generated footer was inserted into frontmatter:\n%s", content)
	}
	separator := "\n---\n\n"
	separatorOffset := strings.Index(content[footerIndex:], separator)
	if separatorOffset < 0 {
		t.Fatalf("content missing visible body separator after generated footer:\n%s", content)
	}
	separatorIndex := footerIndex + separatorOffset
	bodyAfterSeparator := content[separatorIndex+len(separator):]
	if bodyAfterSeparator != body {
		t.Fatalf("sender body after separator changed:\n got %q\nwant %q\ncontent:\n%s", bodyAfterSeparator, body, content)
	}
}

func TestRunSendMessage_StoresReplyPolicyMetadataAndReplyToFooter(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	stdout, _, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "please do this", []string{
			"--config", configPath,
			"--context-id", "ctx-reply-policy",
			"--to", "worker", "--reply-required",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "required" {
		t.Fatalf("payload.ReplyPolicy = %q, want required", payload.ReplyPolicy)
	}
	if payload.InputRequestID == "" {
		t.Fatal("payload.InputRequestID is empty, want generated input request id")
	}
	if err := envelope.ValidateInputRequestToken(payload.InputRequestID); err != nil {
		t.Fatalf("payload.InputRequestID = %q is invalid: %v", payload.InputRequestID, err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-reply-policy", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	for _, want := range []string{
		"messageId: " + payload.Sent,
		"replyPolicy: required",
		"input_request_id: " + payload.InputRequestID,
		"Reply with quoted heredoc:\ntmux-a2a-postman send-heredoc --to messenger --fills-input-request-id " + payload.InputRequestID + " --reply-to " + payload.Sent + " <<'POSTMAN_BODY'",
		"<your message>\nPOSTMAN_BODY",
		"Required-reply completion gate:",
		"Filling this input request closes transport, not task acceptance.",
		"DONE requires original checklist verification plus: Task artifact, Original checklist: PASS, Evidence, Remaining blockers: none.",
		"Use BLOCKED with Original checklist: FAIL when any requested item is unresolved or unverified.",
		"Add --reply-required only when your reply needs a response.",
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("content missing %q:\n%s", want, string(content))
		}
	}
	for _, unwanted := range []string{"obligation" + "_id:", "satisfies" + "_obligation" + "_id:", "fills_input_request_id:"} {
		if strings.Contains(string(content), unwanted) {
			t.Fatalf("content contains unexpected %q:\n%s", unwanted, string(content))
		}
	}
}

func TestRunSendMessage_StoresFillsInputRequestIDForExactReply(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	const inputRequestID = "ireq_exact_123"
	stdout, _, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "DONE", []string{
			"--config", configPath,
			"--context-id", "ctx-exact-reply",
			"--to", "messenger", "--reply-to", "20260503-090000-sabcd-r1234-from-messenger-to-worker.md",
			"--fills-input-request-id", inputRequestID,
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.InputRequestID != "" {
		t.Fatalf("payload.InputRequestID = %q, want empty for no-reply exact reply", payload.InputRequestID)
	}
	if payload.FillsInputRequestID != inputRequestID {
		t.Fatalf("payload.FillsInputRequestID = %q, want %q", payload.FillsInputRequestID, inputRequestID)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-exact-reply", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	for _, want := range []string{
		"replyPolicy: none",
		"replyTo: 20260503-090000-sabcd-r1234-from-messenger-to-worker.md",
		"fills_input_request_id: " + inputRequestID,
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("content missing %q:\n%s", want, string(content))
		}
	}
	if strings.Contains(string(content), "\n  "+"obligation"+"_id:") {
		t.Fatalf("content contains unexpected legacy reply identity field:\n%s", string(content))
	}
	if strings.Contains(string(content), "\n  "+"satisfies"+"_obligation"+"_id:") {
		t.Fatalf("content contains unexpected legacy reply identity field:\n%s", string(content))
	}
}

func TestRunSendMessage_ExactFillOutputReportsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["critic --- worker"]

[critic]
role = "critic"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	const contextID = "ctx-exact-fill-output"
	const originalMessageID = "20260503-090000-sabcd-r1234-from-critic-to-worker.md"
	const inputRequestID = "ireq_exact_123"
	sessionDir := filepath.Join(tmpDir, contextID, "test-session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("MkdirAll sessionDir: %v", err)
	}
	if err := config.WriteSessionPIDFile(filepath.Join(sessionDir, "postman.pid"), os.Getpid()); err != nil {
		t.Fatalf("WriteFile postman.pid: %v", err)
	}
	now := time.Date(2026, time.May, 3, 9, 0, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, contextID, "test-session", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	requestContent := sessionStatusMessageContent("critic", "worker", originalMessageID, map[string]string{
		"replyPolicy":      "required",
		"input_request_id": inputRequestID,
	}, "please finish")
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionPostConsumedEventType, originalMessageID, "critic", "worker", requestContent, now.Add(time.Second))
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionDeliveredEventType, originalMessageID, "critic", "worker", requestContent, now.Add(2*time.Second))
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionReadEventType, originalMessageID, "critic", "worker", requestContent, now.Add(3*time.Second))

	requestSeen := make(chan projection.DaemonSubmitRequest, 1)
	go func() {
		requestPath, request := awaitDaemonSubmitRequest(t, sessionDir, time.Second)
		requestSeen <- request
		appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionPostConsumedEventType, request.Filename, "worker", "critic", request.Content, now.Add(4*time.Second))
		appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionDeliveredEventType, request.Filename, "worker", "critic", request.Content, now.Add(5*time.Second))
		if err := os.Remove(requestPath); err != nil && !os.IsNotExist(err) {
			t.Errorf("Remove requestPath: %v", err)
		}
		if _, err := projection.WriteDaemonSubmitResponse(sessionDir, projection.DaemonSubmitResponse{
			RequestID: request.RequestID,
			Command:   request.Command,
			HandledAt: time.Now().UTC().Format(time.RFC3339),
			Filename:  request.Filename,
		}); err != nil {
			t.Errorf("WriteDaemonSubmitResponse: %v", err)
		}
	}()

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "DONE: complete", []string{
			"--config", configPath,
			"--context-id", contextID,
			"--to", "critic",
			"--reply-to", originalMessageID,
			"--fills-input-request-id", inputRequestID,
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v\nstderr=%s", err, stderr)
	}
	request := <-requestSeen
	payload := decodeSendOutputForTest(t, stdout)
	if payload.Sent != request.Filename {
		t.Fatalf("payload.Sent = %q, want %q", payload.Sent, request.Filename)
	}
	if payload.Fill == nil {
		t.Fatalf("payload.Fill = nil, want fill summary: %#v", payload)
	}
	if payload.Fill.Requested != inputRequestID || payload.Fill.State != "closed" || payload.Fill.Closed == nil || !*payload.Fill.Closed {
		t.Fatalf("payload.Fill = %#v, want closed exact fill", payload.Fill)
	}
	if payload.RequiredInput == nil || payload.RequiredInput.State != "known" || payload.RequiredInput.Open != 0 {
		t.Fatalf("payload.RequiredInput = %#v, want known open=0", payload.RequiredInput)
	}
	if payload.Notice != "" {
		t.Fatalf("payload.Notice = %q, want empty", payload.Notice)
	}
}

func TestRunSendMessage_TerminalWrongReplyToOutputShowsOpenRequiredInput(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["critic --- worker"]

[critic]
role = "critic"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "worker")

	const contextID = "ctx-wrong-reply-to-output"
	const originalMessageID = "20260503-090000-sabcd-r1234-from-critic-to-worker.md"
	const wrongMessageID = "20260503-090100-sabcd-r5678-from-critic-to-worker.md"
	const inputRequestID = "ireq_exact_123"
	sessionDir := filepath.Join(tmpDir, contextID, "test-session")
	now := time.Date(2026, time.May, 3, 9, 10, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, contextID, "test-session", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	requestContent := sessionStatusMessageContent("critic", "worker", originalMessageID, map[string]string{
		"replyPolicy":      "required",
		"input_request_id": inputRequestID,
	}, "please finish")
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionPostConsumedEventType, originalMessageID, "critic", "worker", requestContent, now.Add(time.Second))
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionDeliveredEventType, originalMessageID, "critic", "worker", requestContent, now.Add(2*time.Second))
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionReadEventType, originalMessageID, "critic", "worker", requestContent, now.Add(3*time.Second))

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "DONE: complete", []string{
			"--config", configPath,
			"--context-id", contextID,
			"--to", "critic",
			"--reply-to", wrongMessageID,
			"--fills-input-request-id", inputRequestID,
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v\nstderr=%s", err, stderr)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.Fill == nil {
		t.Fatalf("payload.Fill = nil, want fill summary: %#v", payload)
	}
	if payload.Fill.Requested != inputRequestID || payload.Fill.State != "open" || payload.Fill.Closed == nil || *payload.Fill.Closed || payload.Fill.Reason != "wrong_reply_to" {
		t.Fatalf("payload.Fill = %#v, want open wrong_reply_to", payload.Fill)
	}
	if payload.RequiredInput == nil || payload.RequiredInput.State != "known" || payload.RequiredInput.Open != 1 {
		t.Fatalf("payload.RequiredInput = %#v, want known open=1", payload.RequiredInput)
	}
	if got := payload.RequiredInput.Remaining; len(got) != 1 || got[0].InputRequestID != inputRequestID || got[0].MessageID != originalMessageID {
		t.Fatalf("payload.RequiredInput.Remaining = %#v, want original open input request", got)
	}
	if !strings.Contains(payload.Notice, "required input remains open") {
		t.Fatalf("payload.Notice = %q, want remaining-input notice", payload.Notice)
	}
	for _, want := range []string{"send-heredoc", "--fills-input-request-id " + inputRequestID, "--reply-to " + originalMessageID, "POSTMAN_BODY"} {
		if !strings.Contains(payload.SuggestedNextAction, want) {
			t.Fatalf("payload.SuggestedNextAction = %q, want %q", payload.SuggestedNextAction, want)
		}
	}
}

func TestRunSendMessage_DefaultStoresNoReplyPolicy(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	stdout, _, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "plain update", []string{
			"--config", configPath,
			"--context-id", "ctx-default-reply-policy",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "none" {
		t.Fatalf("payload.ReplyPolicy = %q, want none", payload.ReplyPolicy)
	}
}

func TestRunSendMessage_StrictMessageTypeStoresRequiredPolicy(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]
draft_template = """---
params:
  contextId: {context_id}
  from: {sender}
  to: {recipient}
  timestamp: {timestamp}
  messageType: status_request
  input_request_id: {input_request_id}
---

<!-- write here -->
"""

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	stdout, _, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "current status?", []string{
			"--config", configPath,
			"--context-id", "ctx-message-type-policy",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "required" {
		t.Fatalf("payload.ReplyPolicy = %q, want required", payload.ReplyPolicy)
	}
	if payload.InputRequestID == "" {
		t.Fatal("payload.InputRequestID is empty, want generated input request id")
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-message-type-policy", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	for _, want := range []string{
		"messageType: status_request",
		"replyPolicy: required",
		"input_request_id: " + payload.InputRequestID,
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("content missing %q:\n%s", want, string(content))
		}
	}
}

func TestRunSendMessage_StrictMessageTypeOverridesGeneratedReplyPolicyPlaceholder(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]
draft_template = """---
params:
  contextId: {context_id}
  from: {sender}
  to: {recipient}
  messageId: {message_id}
  replyPolicy: {reply_policy}
  timestamp: {timestamp}
  messageType: status_request
---

<!-- write here -->
"""

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	stdout, _, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "current status?", []string{
			"--config", configPath,
			"--context-id", "ctx-message-type-placeholder-policy",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "required" {
		t.Fatalf("payload.ReplyPolicy = %q, want required", payload.ReplyPolicy)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-message-type-placeholder-policy", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	for _, want := range []string{
		"messageType: status_request",
		"replyPolicy: required",
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("content missing %q:\n%s", want, string(content))
		}
	}
	if strings.Contains(string(content), "replyPolicy: none") {
		t.Fatalf("content kept generated replyPolicy none:\n%s", string(content))
	}
	assertNoGeneratedReplyPolicyMarker(t, string(content), "post content")
}

func TestRunSendMessage_StrictMessageTypeOverridesSpacedGeneratedReplyPolicyPlaceholder(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]
draft_template = """---
params:
  contextId: {context_id}
  from: {sender}
  to: {recipient}
  messageId: {message_id}
  replyPolicy:  {reply_policy}
  timestamp: {timestamp}
  messageType: status_request
---

<!-- write here -->
"""

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	stdout, _, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "current status?", []string{
			"--config", configPath,
			"--context-id", "ctx-message-type-spaced-placeholder-policy",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "required" {
		t.Fatalf("payload.ReplyPolicy = %q, want required", payload.ReplyPolicy)
	}
}

func TestRunSendMessage_StrictMessageTypePreservesExplicitNoReplyWhenPlaceholderIsNotInParams(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]
draft_template = """---
params:
  contextId: {context_id}
  from: {sender}
  to: {recipient}
  messageId: {message_id}
  replyPolicy: none
  timestamp: {timestamp}
  messageType: status_request
---

replyPolicy: {reply_policy}
<!-- write here -->
"""

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	stdout, _, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "current status?", []string{
			"--config", configPath,
			"--context-id", "ctx-explicit-no-reply-body-placeholder",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "none" {
		t.Fatalf("payload.ReplyPolicy = %q, want none", payload.ReplyPolicy)
	}
}

func TestRunSendMessage_StrictMessageTypePreservesExplicitPolicyWithPlaceholderAlias(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]
draft_template = """---
params:
  contextId: {context_id}
  from: {sender}
  to: {recipient}
  messageId: {message_id}
  replyPolicy: required
  reply_policy: {reply_policy}
  timestamp: {timestamp}
  messageType: status_update
---

<!-- write here -->
"""

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	stdout, _, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "please reply", []string{
			"--config", configPath,
			"--context-id", "ctx-explicit-policy-placeholder-alias",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "required" {
		t.Fatalf("payload.ReplyPolicy = %q, want required", payload.ReplyPolicy)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-explicit-policy-placeholder-alias", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	for _, want := range []string{
		"replyPolicy: required",
		"reply_policy: required",
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("content missing %q:\n%s", want, string(content))
		}
	}
}

func TestRunSendMessage_StrictMessageTypePreservesExplicitNoReplyWithPlaceholderAlias(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]
draft_template = """---
params:
  contextId: {context_id}
  from: {sender}
  to: {recipient}
  messageId: {message_id}
  replyPolicy: none
  reply_policy: {reply_policy}
  timestamp: {timestamp}
  messageType: status_request
---

<!-- write here -->
"""

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	stdout, _, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "status-only", []string{
			"--config", configPath,
			"--context-id", "ctx-explicit-no-reply-placeholder-alias",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "none" {
		t.Fatalf("payload.ReplyPolicy = %q, want none", payload.ReplyPolicy)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-explicit-no-reply-placeholder-alias", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	for _, want := range []string{
		"replyPolicy: none",
		"reply_policy: none",
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("content missing %q:\n%s", want, string(content))
		}
	}
}

func TestRunSendMessage_PreservesShellExpandedExplicitReplyPolicy(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]
allow_shell_templates = true
draft_template = """---
params:
  contextId: {context_id}
  from: {sender}
  to: {recipient}
  messageId: {message_id}
  replyPolicy: $(printf required)
  timestamp: {timestamp}
  messageType: status_update
---

<!-- write here -->
"""

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	stdout, _, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "please reply", []string{
			"--config", configPath,
			"--context-id", "ctx-shell-expanded-reply-policy",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "required" {
		t.Fatalf("payload.ReplyPolicy = %q, want required", payload.ReplyPolicy)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-shell-expanded-reply-policy", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	if !strings.Contains(string(content), "replyPolicy: required") {
		t.Fatalf("content missing expanded replyPolicy:\n%s", string(content))
	}
}

func TestRunSendMessage_PreservesShellGeneratedReplyPolicyLine(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]
allow_shell_templates = true
draft_template = """---
params:
  contextId: {context_id}
  from: {sender}
  to: {recipient}
  messageId: {message_id}
  replyPolicy: {reply_policy}
$(printf '  reply_policy: none')
  timestamp: {timestamp}
  messageType: status_request
---

<!-- write here -->
"""

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	stdout, _, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "status only", []string{
			"--config", configPath,
			"--context-id", "ctx-shell-generated-reply-policy-line",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "none" {
		t.Fatalf("payload.ReplyPolicy = %q, want none", payload.ReplyPolicy)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-shell-generated-reply-policy-line", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	if !strings.Contains(string(content), "reply_policy: none") {
		t.Fatalf("content missing shell-generated reply_policy:\n%s", string(content))
	}
	assertNoGeneratedReplyPolicyMarker(t, string(content), "post content")
}

func TestRunSendMessage_PreservesLastExplicitPolicyWhenShellAddsPolicyFields(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]
allow_shell_templates = true
draft_template = """---
params:
  contextId: {context_id}
  from: {sender}
  to: {recipient}
  messageId: {message_id}
  replyPolicy: $(printf 'required\n  replyPolicy: none')
  reply_policy: required
  timestamp: {timestamp}
  messageType: status_update
---

<!-- write here -->
"""

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	stdout, _, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "please reply", []string{
			"--config", configPath,
			"--context-id", "ctx-shell-added-reply-policy-fields",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "required" {
		t.Fatalf("payload.ReplyPolicy = %q, want required", payload.ReplyPolicy)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-shell-added-reply-policy-fields", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	if !strings.Contains(string(content), "reply_policy: required") {
		t.Fatalf("content missing reply_policy required:\n%s", string(content))
	}
	assertNoGeneratedReplyPolicyMarker(t, string(content), "post content")
}

func TestRunSendMessage_ReplyPolicyFlagsUpdatePlaceholderAliases(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantPolicy  string
		wantContext string
	}{
		{
			name:        "no reply",
			args:        []string{"--no-reply"},
			wantPolicy:  "none",
			wantContext: "ctx-flag-no-reply-placeholder-alias",
		},
		{
			name:        "reply required",
			args:        []string{"--reply-required"},
			wantPolicy:  "required",
			wantContext: "ctx-flag-reply-required-placeholder-alias",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Chdir(tmpDir)
			t.Setenv("HOME", tmpDir)
			t.Setenv("XDG_CONFIG_HOME", tmpDir)
			configPath := filepath.Join(tmpDir, "postman.toml")
			configContent := `[postman]
edges = ["messenger --- worker"]
draft_template = """---
params:
  contextId: {context_id}
  from: {sender}
  to: {recipient}
  messageId: {message_id}
  reply_policy: {reply_policy}
  timestamp: {timestamp}
---

<!-- write here -->
"""

[messenger]
role = "messenger"

[worker]
role = "worker"
`
			if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
				t.Fatalf("WriteFile config: %v", err)
			}
			installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

			args := []string{
				"--config", configPath,
				"--context-id", tt.wantContext,
				"--to", "worker",
			}
			args = append(args, tt.args...)
			stdout, _, err := captureCommandOutput(t, func() error {
				return runSendHeredocWithBody(t, "flag-controlled policy", args)
			})
			if err != nil {
				t.Fatalf("RunSendMessage: %v", err)
			}
			payload := decodeSendOutputForTest(t, stdout)
			if payload.ReplyPolicy != tt.wantPolicy {
				t.Fatalf("payload.ReplyPolicy = %q, want %q", payload.ReplyPolicy, tt.wantPolicy)
			}

			content, err := os.ReadFile(filepath.Join(tmpDir, tt.wantContext, "test-session", "post", payload.Sent))
			if err != nil {
				t.Fatalf("ReadFile post: %v", err)
			}
			if !strings.Contains(string(content), "reply_policy: "+tt.wantPolicy) {
				t.Fatalf("content missing reply_policy %q:\n%s", tt.wantPolicy, string(content))
			}
		})
	}
}

func TestRunSendMessage_NoReplyFlagStoresNoReplyPolicy(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	stdout, _, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "status-only update", []string{
			"--config", configPath,
			"--context-id", "ctx-no-reply-policy",
			"--to", "worker", "--no-reply",
			"--reply-to", "20260503-090000-sabcd-r1234-from-worker-to-messenger.md",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v", err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.ReplyPolicy != "none" {
		t.Fatalf("payload.ReplyPolicy = %q, want none", payload.ReplyPolicy)
	}
	if payload.ReplyTo != "20260503-090000-sabcd-r1234-from-worker-to-messenger.md" {
		t.Fatalf("payload.ReplyTo = %q, want previous message id", payload.ReplyTo)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "ctx-no-reply-policy", "test-session", "post", payload.Sent))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	for _, want := range []string{
		"replyPolicy: none",
		"replyTo: 20260503-090000-sabcd-r1234-from-worker-to-messenger.md",
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("content missing %q:\n%s", want, string(content))
		}
	}
}

func TestRunSendMessage_DefaultJSONReportsQueuedWhenOnlyLocalHandoffIsConfirmed(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "hello queued json", []string{
			"--config", configPath,
			"--context-id", "ctx-send-queued-json",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v\nstderr=%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	var payload struct {
		Sent       string                `json:"sent"`
		Status     string                `json:"status"`
		SubmitPath projection.SubmitPath `json:"submit_path"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	if payload.Sent == "" {
		t.Fatalf("payload.Sent = empty, want filename")
	}
	if payload.Status != string(sendStatusQueued) {
		t.Fatalf("payload.Status = %q, want %q", payload.Status, sendStatusQueued)
	}
	if payload.SubmitPath != projection.SubmitPathPost {
		t.Fatalf("payload.SubmitPath = %q, want %q", payload.SubmitPath, projection.SubmitPathPost)
	}

	postDir := filepath.Join(tmpDir, "ctx-send-queued-json", "test-session", "post")
	postEntries, err := os.ReadDir(postDir)
	if err != nil {
		t.Fatalf("ReadDir post: %v", err)
	}
	if len(postEntries) != 1 {
		t.Fatalf("post entry count = %d, want 1", len(postEntries))
	}
	if postEntries[0].Name() != payload.Sent {
		t.Fatalf("post filename = %q, want %q", postEntries[0].Name(), payload.Sent)
	}
}

func TestRunSendMessage_DefaultJSONReportsProcessedWhenDaemonConsumesDirectPost(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	sessionDir := filepath.Join(tmpDir, "ctx-send-direct-processed", "test-session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("MkdirAll sessionDir: %v", err)
	}
	ownerSessionDir := filepath.Join(tmpDir, "ctx-send-direct-processed", "owner-session")
	if err := os.MkdirAll(ownerSessionDir, 0o700); err != nil {
		t.Fatalf("MkdirAll ownerSessionDir: %v", err)
	}
	if err := config.WriteSessionPIDFile(filepath.Join(ownerSessionDir, "postman.pid"), os.Getpid()); err != nil {
		t.Fatalf("WriteFile postman.pid: %v", err)
	}

	go func() {
		postDir := filepath.Join(sessionDir, "post")
		filename := awaitMarkdownFile(t, postDir, time.Second)
		inboxDir := filepath.Join(sessionDir, "inbox", "worker")
		if err := os.MkdirAll(inboxDir, 0o700); err != nil {
			t.Errorf("MkdirAll inboxDir: %v", err)
			return
		}
		if err := os.Rename(filepath.Join(postDir, filename), filepath.Join(inboxDir, filename)); err != nil {
			t.Errorf("Rename post to inbox: %v", err)
		}
	}()

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "hello processed json", []string{
			"--config", configPath,
			"--context-id", "ctx-send-direct-processed",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v\nstderr=%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	var payload struct {
		Sent       string                `json:"sent"`
		Status     string                `json:"status"`
		SubmitPath projection.SubmitPath `json:"submit_path"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	if payload.Status != string(sendStatusProcessed) {
		t.Fatalf("payload.Status = %q, want %q", payload.Status, sendStatusProcessed)
	}
	if payload.SubmitPath != projection.SubmitPathPost {
		t.Fatalf("payload.SubmitPath = %q, want %q", payload.SubmitPath, projection.SubmitPathPost)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "inbox", "worker", payload.Sent)); err != nil {
		t.Fatalf("Stat delivered inbox file: %v", err)
	}
}

func TestRunSendMessage_ReturnsErrorWhenDaemonDeadLettersDirectPost(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	sessionDir := filepath.Join(tmpDir, "ctx-send-direct-dead-letter", "test-session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("MkdirAll sessionDir: %v", err)
	}
	ownerSessionDir := filepath.Join(tmpDir, "ctx-send-direct-dead-letter", "owner-session")
	if err := os.MkdirAll(ownerSessionDir, 0o700); err != nil {
		t.Fatalf("MkdirAll ownerSessionDir: %v", err)
	}
	if err := config.WriteSessionPIDFile(filepath.Join(ownerSessionDir, "postman.pid"), os.Getpid()); err != nil {
		t.Fatalf("WriteFile postman.pid: %v", err)
	}

	go func() {
		postDir := filepath.Join(sessionDir, "post")
		filename := awaitMarkdownFile(t, postDir, time.Second)
		deadLetterDir := filepath.Join(sessionDir, "dead-letter")
		if err := os.MkdirAll(deadLetterDir, 0o700); err != nil {
			t.Errorf("MkdirAll deadLetterDir: %v", err)
			return
		}
		dst := filepath.Join(deadLetterDir, strings.TrimSuffix(filename, ".md")+"-dl-routing-denied.md")
		if err := os.Rename(filepath.Join(postDir, filename), dst); err != nil {
			t.Errorf("Rename post to dead-letter: %v", err)
		}
	}()

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "hello dead letter", []string{
			"--config", configPath,
			"--context-id", "ctx-send-direct-dead-letter",
			"--to", "worker",
		})
	})
	if err == nil {
		t.Fatal("RunSendMessage() = nil, want dead-letter error")
	}
	if !strings.Contains(err.Error(), "message dead-lettered:") {
		t.Fatalf("RunSendMessage() error = %v, want dead-letter wording", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestRunSendMessage_UsesDaemonSubmitWhenDaemonOwnsSession(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	sessionDir := filepath.Join(tmpDir, "ctx-send-submit", "test-session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("MkdirAll sessionDir: %v", err)
	}
	if err := config.WriteSessionPIDFile(filepath.Join(sessionDir, "postman.pid"), os.Getpid()); err != nil {
		t.Fatalf("WriteFile postman.pid: %v", err)
	}
	if !config.ContextOwnsSession(tmpDir, "ctx-send-submit", "test-session") {
		t.Fatal("ContextOwnsSession() = false, want true")
	}

	requestSeen := make(chan projection.DaemonSubmitRequest, 1)
	go func() {
		requestPath, request := awaitDaemonSubmitRequest(t, sessionDir, time.Second)
		requestSeen <- request
		postDir := filepath.Join(sessionDir, "post")
		if err := os.MkdirAll(postDir, 0o700); err != nil {
			t.Errorf("MkdirAll postDir: %v", err)
			return
		}
		postPath := filepath.Join(postDir, request.Filename)
		if err := os.WriteFile(postPath, []byte(request.Content), 0o600); err != nil {
			t.Errorf("WriteFile postPath: %v", err)
			return
		}
		if err := os.Remove(requestPath); err != nil && !os.IsNotExist(err) {
			t.Errorf("Remove requestPath: %v", err)
		}
		inboxDir := filepath.Join(sessionDir, "inbox", "worker")
		if err := os.MkdirAll(inboxDir, 0o700); err != nil {
			t.Errorf("MkdirAll inboxDir: %v", err)
			return
		}
		if err := os.Rename(postPath, filepath.Join(inboxDir, request.Filename)); err != nil {
			t.Errorf("Rename post to inbox: %v", err)
		}
		if _, err := projection.WriteDaemonSubmitResponse(sessionDir, projection.DaemonSubmitResponse{
			RequestID: request.RequestID,
			Command:   request.Command,
			HandledAt: time.Now().UTC().Format(time.RFC3339),
			Filename:  request.Filename,
		}); err != nil {
			t.Errorf("WriteDaemonSubmitResponse: %v", err)
		}
	}()

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "hello through submit", []string{
			"--config", configPath,
			"--context-id", "ctx-send-submit",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v\nstderr=%s", err, stderr)
	}
	request := <-requestSeen
	if request.Command != projection.DaemonSubmitSend {
		t.Fatalf("request.Command = %q, want %q", request.Command, projection.DaemonSubmitSend)
	}
	if !strings.Contains(request.Filename, "-to-worker.md") {
		t.Fatalf("request filename missing recipient: %q", request.Filename)
	}
	if !strings.Contains(request.Content, "hello through submit") {
		t.Fatalf("request content missing body:\n%s", request.Content)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.Sent != request.Filename {
		t.Fatalf("payload.Sent = %q, want %q", payload.Sent, request.Filename)
	}
	if payload.Status != string(sendStatusProcessed) {
		t.Fatalf("payload.Status = %q, want %q", payload.Status, sendStatusProcessed)
	}
	if payload.ContextID != "ctx-send-submit" {
		t.Fatalf("payload.ContextID = %q, want ctx-send-submit", payload.ContextID)
	}
	if payload.Session != "test-session" {
		t.Fatalf("payload.Session = %q, want test-session", payload.Session)
	}
	if payload.From != "messenger" {
		t.Fatalf("payload.From = %q, want messenger", payload.From)
	}
	if payload.To != "worker" {
		t.Fatalf("payload.To = %q, want worker", payload.To)
	}
	if payload.SubmitPath != projection.SubmitPathDaemon {
		t.Fatalf("payload.SubmitPath = %q, want %q", payload.SubmitPath, projection.SubmitPathDaemon)
	}
	postEntries, err := os.ReadDir(filepath.Join(sessionDir, "post"))
	if err == nil && len(postEntries) != 0 {
		t.Fatalf("direct post write bypassed daemon submit: found %d post entries", len(postEntries))
	}
}

func TestRunSendMessage_UsesDaemonSubmitForOwnedSessionInLegacyMode(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	sessionDir := filepath.Join(tmpDir, "ctx-send-submit-legacy", "test-session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("MkdirAll sessionDir: %v", err)
	}
	if err := config.WriteSessionPIDFile(filepath.Join(sessionDir, "postman.pid"), os.Getpid()); err != nil {
		t.Fatalf("WriteFile postman.pid: %v", err)
	}
	if !config.ContextOwnsSession(tmpDir, "ctx-send-submit-legacy", "test-session") {
		t.Fatal("ContextOwnsSession() = false, want true")
	}

	requestSeen := make(chan projection.DaemonSubmitRequest, 1)
	serveDone := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		requestsDir := projection.DaemonSubmitRequestsDir(sessionDir)
		for {
			entries, err := os.ReadDir(requestsDir)
			if err == nil {
				for _, entry := range entries {
					if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
						continue
					}
					requestPath := filepath.Join(requestsDir, entry.Name())
					request, readErr := projection.ReadDaemonSubmitRequest(requestPath)
					if readErr != nil {
						serveDone <- fmt.Errorf("ReadDaemonSubmitRequest(%s): %w", requestPath, readErr)
						return
					}
					requestSeen <- request
					postDir := filepath.Join(sessionDir, "post")
					if err := os.MkdirAll(postDir, 0o700); err != nil {
						serveDone <- fmt.Errorf("MkdirAll postDir: %w", err)
						return
					}
					postPath := filepath.Join(postDir, request.Filename)
					if err := os.WriteFile(postPath, []byte(request.Content), 0o600); err != nil {
						serveDone <- fmt.Errorf("WriteFile postPath: %w", err)
						return
					}
					if err := os.Remove(requestPath); err != nil && !os.IsNotExist(err) {
						serveDone <- fmt.Errorf("Remove requestPath: %w", err)
						return
					}
					inboxDir := filepath.Join(sessionDir, "inbox", "worker")
					if err := os.MkdirAll(inboxDir, 0o700); err != nil {
						serveDone <- fmt.Errorf("MkdirAll inboxDir: %w", err)
						return
					}
					if err := os.Rename(postPath, filepath.Join(inboxDir, request.Filename)); err != nil {
						serveDone <- fmt.Errorf("Rename post to inbox: %w", err)
						return
					}
					if _, err := projection.WriteDaemonSubmitResponse(sessionDir, projection.DaemonSubmitResponse{
						RequestID: request.RequestID,
						Command:   request.Command,
						HandledAt: time.Now().UTC().Format(time.RFC3339),
						Filename:  request.Filename,
					}); err != nil {
						serveDone <- fmt.Errorf("WriteDaemonSubmitResponse: %w", err)
						return
					}
					serveDone <- nil
					return
				}
			}
			if time.Now().After(deadline) {
				serveDone <- fmt.Errorf("timed out waiting for daemon submit request in %s", requestsDir)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "hello through submit in legacy mode", []string{
			"--config", configPath,
			"--context-id", "ctx-send-submit-legacy",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v\nstderr=%s", err, stderr)
	}
	if serveErr := <-serveDone; serveErr != nil {
		t.Fatal(serveErr)
	}
	request := <-requestSeen
	if request.Command != projection.DaemonSubmitSend {
		t.Fatalf("request.Command = %q, want %q", request.Command, projection.DaemonSubmitSend)
	}
	if !strings.Contains(request.Content, "hello through submit in legacy mode") {
		t.Fatalf("request content missing body:\n%s", request.Content)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.Status != string(sendStatusProcessed) {
		t.Fatalf("payload.Status = %q, want %q", payload.Status, sendStatusProcessed)
	}
	if payload.Session != "test-session" {
		t.Fatalf("payload.Session = %q, want test-session", payload.Session)
	}
	if payload.ContextID != "ctx-send-submit-legacy" {
		t.Fatalf("payload.ContextID = %q, want ctx-send-submit-legacy", payload.ContextID)
	}
	if payload.SubmitPath != projection.SubmitPathDaemon {
		t.Fatalf("payload.SubmitPath = %q, want %q", payload.SubmitPath, projection.SubmitPathDaemon)
	}
	postEntries, err := os.ReadDir(filepath.Join(sessionDir, "post"))
	if err == nil && len(postEntries) != 0 {
		t.Fatalf("direct post write bypassed daemon submit: found %d post entries", len(postEntries))
	}
}

func TestRunSendMessage_DefaultJSONUsesDaemonSubmitWhenDaemonOwnsSession(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	sessionDir := filepath.Join(tmpDir, "ctx-send-submit-json", "test-session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("MkdirAll sessionDir: %v", err)
	}
	if err := config.WriteSessionPIDFile(filepath.Join(sessionDir, "postman.pid"), os.Getpid()); err != nil {
		t.Fatalf("WriteFile postman.pid: %v", err)
	}
	if !config.ContextOwnsSession(tmpDir, "ctx-send-submit-json", "test-session") {
		t.Fatal("ContextOwnsSession() = false, want true")
	}

	requestSeen := make(chan projection.DaemonSubmitRequest, 1)
	go func() {
		requestPath, request := awaitDaemonSubmitRequest(t, sessionDir, time.Second)
		requestSeen <- request
		postDir := filepath.Join(sessionDir, "post")
		if err := os.MkdirAll(postDir, 0o700); err != nil {
			t.Errorf("MkdirAll postDir: %v", err)
			return
		}
		postPath := filepath.Join(postDir, request.Filename)
		if err := os.WriteFile(postPath, []byte(request.Content), 0o600); err != nil {
			t.Errorf("WriteFile postPath: %v", err)
			return
		}
		if err := os.Remove(requestPath); err != nil && !os.IsNotExist(err) {
			t.Errorf("Remove requestPath: %v", err)
		}
		inboxDir := filepath.Join(sessionDir, "inbox", "worker")
		if err := os.MkdirAll(inboxDir, 0o700); err != nil {
			t.Errorf("MkdirAll inboxDir: %v", err)
			return
		}
		if err := os.Rename(postPath, filepath.Join(inboxDir, request.Filename)); err != nil {
			t.Errorf("Rename post to inbox: %v", err)
		}
		if _, err := projection.WriteDaemonSubmitResponse(sessionDir, projection.DaemonSubmitResponse{
			RequestID: request.RequestID,
			Command:   request.Command,
			HandledAt: time.Now().UTC().Format(time.RFC3339),
			Filename:  request.Filename,
		}); err != nil {
			t.Errorf("WriteDaemonSubmitResponse: %v", err)
		}
	}()

	replyTo := "20260503-090000-sabcd-r1234-from-worker-to-messenger.md"
	stdout, stderr, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "hello through submit json", []string{
			"--config", configPath,
			"--context-id", "ctx-send-submit-json",
			"--to", "worker", "--reply-required",
			"--reply-to", replyTo,
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v\nstderr=%s", err, stderr)
	}
	request := <-requestSeen
	if request.Command != projection.DaemonSubmitSend {
		t.Fatalf("request.Command = %q, want %q", request.Command, projection.DaemonSubmitSend)
	}
	if !strings.Contains(request.Filename, "-to-worker.md") {
		t.Fatalf("request filename missing recipient: %q", request.Filename)
	}
	if !strings.Contains(request.Content, "hello through submit json") {
		t.Fatalf("request content missing body:\n%s", request.Content)
	}
	for _, want := range []string{
		"replyPolicy: required",
		"replyTo: " + replyTo,
	} {
		if !strings.Contains(request.Content, want) {
			t.Fatalf("request content missing %q:\n%s", want, request.Content)
		}
	}
	assertNoGeneratedReplyPolicyMarker(t, request.Content, "daemon submit request content")
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.Sent != request.Filename {
		t.Fatalf("payload.Sent = %q, want %q", payload.Sent, request.Filename)
	}
	if payload.Status != string(sendStatusProcessed) {
		t.Fatalf("payload.Status = %q, want %q", payload.Status, sendStatusProcessed)
	}
	if payload.ContextID != "ctx-send-submit-json" {
		t.Fatalf("payload.ContextID = %q, want ctx-send-submit-json", payload.ContextID)
	}
	if payload.Session != "test-session" {
		t.Fatalf("payload.Session = %q, want test-session", payload.Session)
	}
	if payload.From != "messenger" {
		t.Fatalf("payload.From = %q, want messenger", payload.From)
	}
	if payload.To != "worker" {
		t.Fatalf("payload.To = %q, want worker", payload.To)
	}
	if payload.ReplyPolicy != "required" {
		t.Fatalf("payload.ReplyPolicy = %q, want required", payload.ReplyPolicy)
	}
	if payload.ReplyTo != replyTo {
		t.Fatalf("payload.ReplyTo = %q, want %q", payload.ReplyTo, replyTo)
	}
	if payload.SubmitPath != projection.SubmitPathDaemon {
		t.Fatalf("payload.SubmitPath = %q, want %q", payload.SubmitPath, projection.SubmitPathDaemon)
	}
	postEntries, err := os.ReadDir(filepath.Join(sessionDir, "post"))
	if err == nil && len(postEntries) != 0 {
		t.Fatalf("direct post write bypassed daemon submit: found %d post entries", len(postEntries))
	}
}

func TestRunSendMessage_DaemonSubmitResolvesGeneratedReplyPolicyMarker(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")
	configContent := `[postman]
edges = ["messenger --- worker"]
draft_template = """---
params:
  contextId: {context_id}
  from: {sender}
  to: {recipient}
  messageId: {message_id}
  replyPolicy: {reply_policy}
  timestamp: {timestamp}
  messageType: status_request
---

<!-- write here -->
"""

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	installFakeTmuxForCLI(t, tmpDir, "test-session", "messenger")

	sessionDir := filepath.Join(tmpDir, "ctx-send-submit-marker-resolution", "test-session")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatalf("MkdirAll sessionDir: %v", err)
	}
	if err := config.WriteSessionPIDFile(filepath.Join(sessionDir, "postman.pid"), os.Getpid()); err != nil {
		t.Fatalf("WriteFile postman.pid: %v", err)
	}
	if !config.ContextOwnsSession(tmpDir, "ctx-send-submit-marker-resolution", "test-session") {
		t.Fatal("ContextOwnsSession() = false, want true")
	}

	requestSeen := make(chan projection.DaemonSubmitRequest, 1)
	go func() {
		requestPath, request := awaitDaemonSubmitRequest(t, sessionDir, time.Second)
		requestSeen <- request
		postDir := filepath.Join(sessionDir, "post")
		if err := os.MkdirAll(postDir, 0o700); err != nil {
			t.Errorf("MkdirAll postDir: %v", err)
			return
		}
		postPath := filepath.Join(postDir, request.Filename)
		if err := os.WriteFile(postPath, []byte(request.Content), 0o600); err != nil {
			t.Errorf("WriteFile postPath: %v", err)
			return
		}
		if err := os.Remove(requestPath); err != nil && !os.IsNotExist(err) {
			t.Errorf("Remove requestPath: %v", err)
		}
		inboxDir := filepath.Join(sessionDir, "inbox", "worker")
		if err := os.MkdirAll(inboxDir, 0o700); err != nil {
			t.Errorf("MkdirAll inboxDir: %v", err)
			return
		}
		if err := os.Rename(postPath, filepath.Join(inboxDir, request.Filename)); err != nil {
			t.Errorf("Rename post to inbox: %v", err)
		}
		if _, err := projection.WriteDaemonSubmitResponse(sessionDir, projection.DaemonSubmitResponse{
			RequestID: request.RequestID,
			Command:   request.Command,
			HandledAt: time.Now().UTC().Format(time.RFC3339),
			Filename:  request.Filename,
		}); err != nil {
			t.Errorf("WriteDaemonSubmitResponse: %v", err)
		}
	}()

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return runSendHeredocWithBody(t, "current status?", []string{
			"--config", configPath,
			"--context-id", "ctx-send-submit-marker-resolution",
			"--to", "worker",
		})
	})
	if err != nil {
		t.Fatalf("RunSendMessage: %v\nstderr=%s", err, stderr)
	}
	request := <-requestSeen
	if request.Command != projection.DaemonSubmitSend {
		t.Fatalf("request.Command = %q, want %q", request.Command, projection.DaemonSubmitSend)
	}
	for _, want := range []string{
		"messageType: status_request",
		"replyPolicy: required",
	} {
		if !strings.Contains(request.Content, want) {
			t.Fatalf("request content missing %q:\n%s", want, request.Content)
		}
	}
	assertNoGeneratedReplyPolicyMarker(t, request.Content, "daemon submit request content")
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.Sent != request.Filename {
		t.Fatalf("payload.Sent = %q, want %q", payload.Sent, request.Filename)
	}
	if payload.Status != string(sendStatusProcessed) {
		t.Fatalf("payload.Status = %q, want %q", payload.Status, sendStatusProcessed)
	}
	if payload.ReplyPolicy != "required" {
		t.Fatalf("payload.ReplyPolicy = %q, want required", payload.ReplyPolicy)
	}
	if payload.SubmitPath != projection.SubmitPathDaemon {
		t.Fatalf("payload.SubmitPath = %q, want %q", payload.SubmitPath, projection.SubmitPathDaemon)
	}
}

func TestPerformCLINotification_SkippedWhenPaneEmpty(t *testing.T) {
	var called bool
	fn := func(_ string, _ string, _ time.Duration, _ time.Duration, _ int, _ bool, _ time.Duration, _ int) error {
		called = true
		return nil
	}
	status := performCLINotification("", "msg", 0, 0, 1, true, 0, 0, fn)
	if status != cliNotifySkipped {
		t.Errorf("status = %q, want %q", status, cliNotifySkipped)
	}
	if called {
		t.Error("sendToPaneFunc should not be called when paneID is empty")
	}
}

func TestPerformCLINotification_OKOnSuccess(t *testing.T) {
	var gotPaneID string
	fn := func(paneID string, _ string, _ time.Duration, _ time.Duration, _ int, _ bool, _ time.Duration, _ int) error {
		gotPaneID = paneID
		return nil
	}
	status := performCLINotification("%1", "msg", 0, 0, 1, true, 0, 0, fn)
	if status != cliNotifyOK {
		t.Errorf("status = %q, want %q", status, cliNotifyOK)
	}
	if gotPaneID != "%1" {
		t.Errorf("gotPaneID = %q, want %%1", gotPaneID)
	}
}

func TestPerformCLINotification_FailedOnError(t *testing.T) {
	fn := func(_ string, _ string, _ time.Duration, _ time.Duration, _ int, _ bool, _ time.Duration, _ int) error {
		return fmt.Errorf("tmux error")
	}
	status := performCLINotification("%gone", "msg", 0, 0, 1, true, 0, 0, fn)
	if status != cliNotifyFailed {
		t.Errorf("status = %q, want %q", status, cliNotifyFailed)
	}
}

func TestWriteSendResult_ProcessedWithNotifyOK(t *testing.T) {
	stdout, _, err := captureCommandOutput(t, func() error {
		return writeSendResult("test.md", sendStatusProcessed, cliNotifyOK)
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.Sent != "test.md" {
		t.Errorf("payload.Sent = %q, want test.md", payload.Sent)
	}
	if payload.Notify != "OK" {
		t.Errorf("payload.Notify = %q, want OK", payload.Notify)
	}
}

func TestWriteSendResult_ProcessedWithNotifyFailed(t *testing.T) {
	stdout, _, err := captureCommandOutput(t, func() error {
		return writeSendResult("test.md", sendStatusProcessed, cliNotifyFailed)
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.Notify != "FAILED" {
		t.Errorf("payload.Notify = %q, want FAILED", payload.Notify)
	}
}

func TestWriteSendResult_ProcessedWithNotifySkipped(t *testing.T) {
	stdout, _, err := captureCommandOutput(t, func() error {
		return writeSendResult("test.md", sendStatusProcessed, cliNotifySkipped)
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.Notify != "SKIPPED" {
		t.Errorf("payload.Notify = %q, want SKIPPED", payload.Notify)
	}
}

func TestWriteSendResult_QueuedHasNoNotifySuffix(t *testing.T) {
	stdout, _, err := captureCommandOutput(t, func() error {
		return writeSendResult("test.md", sendStatusQueued, cliNotifyNone)
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := decodeSendOutputForTest(t, stdout)
	if payload.Status != string(sendStatusQueued) {
		t.Errorf("payload.Status = %q, want %q", payload.Status, sendStatusQueued)
	}
	if payload.Notify != "" {
		t.Errorf("payload.Notify = %q, want empty", payload.Notify)
	}
}

func decodeSendOutputForTest(t *testing.T, stdout string) sendOutput {
	t.Helper()
	assertNoGeneratedReplyPolicyMarker(t, stdout, "send stdout")
	var payload sendOutput
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	if strings.Contains(stdout, "Sent: ") || strings.Contains(stdout, "Queued: ") {
		t.Fatalf("stdout unexpectedly used human output: %q", stdout)
	}
	return payload
}

func assertNoGeneratedReplyPolicyMarker(t *testing.T, value, label string) {
	t.Helper()
	if strings.Contains(value, "__TMUX_A2A_POSTMAN_GENERATED_REPLY_POLICY_") {
		t.Fatalf("%s leaked generated reply policy marker:\n%s", label, value)
	}
	if strings.Contains(value, "__TMUX_A2A_POSTMAN_GENERATED_REPLY_SLOT_ID_") {
		t.Fatalf("%s leaked generated input request id marker:\n%s", label, value)
	}
}
