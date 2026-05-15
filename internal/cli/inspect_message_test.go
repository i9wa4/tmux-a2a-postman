package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunInspectMessageFindsUnreadMessageWithoutMovingIt(t *testing.T) {
	fixture := writeInspectMessageFixture(t)
	filename := "20260506-010101-from-orchestrator-to-worker.md"
	content := inspectMessageFixture("orchestrator", "worker", filename, map[string]string{
		"replyPolicy":      "required",
		"input_request_id": "ireq_123",
	}, "Please inspect this")
	inboxPath := filepath.Join(fixture.sessionDir, "inbox", "worker", filename)
	writeInspectMessageFile(t, inboxPath, content)

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunInspectMessage([]string{
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
			"--id", filename,
		})
	})
	if err != nil {
		t.Fatalf("RunInspectMessage() error = %v stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	got := decodeInspectMessageOutputForTest(t, stdout)
	if got.Status != "found" || got.MatchCount != 1 || got.Message == nil {
		t.Fatalf("inspect output = %#v, want one found message", got)
	}
	if got.Message.StorageState != "unread" || got.Message.Node != "worker" {
		t.Fatalf("message state/node = %q/%q, want unread/worker", got.Message.StorageState, got.Message.Node)
	}
	if got.Message.MarkdownPath != inboxPath {
		t.Fatalf("markdown_path = %q, want %q", got.Message.MarkdownPath, inboxPath)
	}
	if got.Message.MessageID != filename || got.Message.From != "orchestrator" || got.Message.To != "worker" {
		t.Fatalf("message routing = %#v, want parsed metadata", got.Message)
	}
	if got.Message.ReplyPolicy != "required" || got.Message.InputRequestID != "ireq_123" {
		t.Fatalf("input metadata = %#v, want reply policy and input request id", got.Message)
	}
	if _, err := os.Stat(inboxPath); err != nil {
		t.Fatalf("inspect-message must not move unread inbox file: %v", err)
	}
}

func TestRunInspectMessageFindsReadMessageAfterInputRequestSatisfied(t *testing.T) {
	fixture := writeInspectMessageFixture(t)
	filename := "20260506-010102-from-worker-to-orchestrator.md"
	content := inspectMessageFixture("worker", "orchestrator", filename, map[string]string{
		"replyPolicy":            "none",
		"replyTo":                "20260506-010101-from-orchestrator-to-worker.md",
		"fills_input_request_id": "ireq_123",
	}, "DONE: handled")
	readPath := filepath.Join(fixture.sessionDir, "read", filename)
	writeInspectMessageFile(t, readPath, content)

	got := runInspectMessageForFixture(t, fixture, filename)
	if got.Status != "found" || got.MatchCount != 1 || got.Message == nil {
		t.Fatalf("inspect output = %#v, want one found read message", got)
	}
	if got.Message.StorageState != "read" {
		t.Fatalf("storage_state = %q, want read", got.Message.StorageState)
	}
	if got.Message.MarkdownPath != readPath {
		t.Fatalf("markdown_path = %q, want %q", got.Message.MarkdownPath, readPath)
	}
	if got.Message.FillsInputRequestID != "ireq_123" || got.Message.ReplyTo != "20260506-010101-from-orchestrator-to-worker.md" {
		t.Fatalf("reply metadata = %#v, want satisfied input request metadata", got.Message)
	}
}

func TestRunInspectMessageFindsStoredMessageAfterInspectInputIsClosed(t *testing.T) {
	fixture := writeInspectInputFixture(t)
	messageID := "20260506-010105-from-critic-to-worker.md"
	inputRequestID := "ireq_closed"
	appendInspectInputRequest(t, fixture, messageID, inputRequestID)
	appendInspectInputResolution(t, fixture, "20260506-010106-from-worker-to-critic.md", messageID, inputRequestID)

	inspectInput := runInspectInputForFixture(t, fixture, messageID)
	if inspectInput.Status != "not_found" || inspectInput.MatchCount != 0 {
		t.Fatalf("inspect-input output = %#v, want closed input request not_found", inspectInput)
	}

	content := sessionStatusMessageContent("critic", "worker", messageID, map[string]string{
		"replyPolicy":      "required",
		"input_request_id": inputRequestID,
	}, "Original request body")
	readPath := filepath.Join(fixture.baseDir, fixture.contextID, fixture.sessionName, "read", messageID)
	writeInspectMessageFile(t, readPath, content)

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunInspectMessage([]string{
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
			"--config", fixture.configPath,
			"--id", messageID,
		})
	})
	if err != nil {
		t.Fatalf("RunInspectMessage() error = %v stderr=%q", err, stderr)
	}
	got := decodeInspectMessageOutputForTest(t, stdout)
	if got.Status != "found" || got.MatchCount != 1 || got.Message == nil {
		t.Fatalf("inspect-message output = %#v, want persisted read message", got)
	}
	if got.Message.MarkdownPath != readPath || got.Message.InputRequestID != inputRequestID {
		t.Fatalf("stored message = %#v, want read path and original input metadata", got.Message)
	}
}

func TestRunInspectMessageOutputModes(t *testing.T) {
	fixture := writeInspectMessageFixture(t)
	filename := "20260506-010103-from-orchestrator-to-worker.md"
	content := inspectMessageFixture("orchestrator", "worker", filename, nil, "Body line one\n\nBody line two")
	readPath := filepath.Join(fixture.sessionDir, "read", filename)
	writeInspectMessageFile(t, readPath, content)

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunInspectMessage([]string{
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
			"--id", filename,
			"--path",
		})
	})
	if err != nil {
		t.Fatalf("RunInspectMessage(--path) error = %v stderr=%q", err, stderr)
	}
	if stdout != readPath+"\n" {
		t.Fatalf("--path stdout = %q, want %q", stdout, readPath+"\n")
	}

	stdout, stderr, err = captureCommandOutput(t, func() error {
		return RunInspectMessage([]string{
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
			"--id", filename,
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("RunInspectMessage(--json) error = %v stderr=%q", err, stderr)
	}
	got := decodeInspectMessageOutputForTest(t, stdout)
	if got.Status != "found" || got.Message == nil || got.Message.MarkdownPath != readPath {
		t.Fatalf("--json output = %#v, want structured found message", got)
	}

	stdout, stderr, err = captureCommandOutput(t, func() error {
		return RunInspectMessage([]string{
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
			"--id", filename,
			"--body",
		})
	})
	if err != nil {
		t.Fatalf("RunInspectMessage(--body) error = %v stderr=%q", err, stderr)
	}
	if stdout != "Body line one\n\nBody line two\n" {
		t.Fatalf("--body stdout = %q, want body only", stdout)
	}
}

func TestRunInspectMessageBodyReturnsSenderBodyAfterEnvelopeSeparator(t *testing.T) {
	fixture := writeInspectMessageFixture(t)
	filename := "20260506-010106-from-orchestrator-to-worker.md"
	senderBody := "# User Request\n\n---\n\n## Details\n\n```sh\n# keep literal\n```\n"
	content := strings.Join([]string{
		"---",
		"params:",
		"  from: orchestrator",
		"  to: worker",
		"  messageId: " + filename,
		"  timestamp: 2026-05-06T01:01:06Z",
		"---",
		"",
		"# Message",
		"",
		"## Recipient Instructions",
		"",
		"Generated guidance before body.",
		"",
		"## Sender Message",
		"",
		"---",
		"",
	}, "\n") + senderBody
	readPath := filepath.Join(fixture.sessionDir, "read", filename)
	writeInspectMessageFile(t, readPath, content)

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunInspectMessage([]string{
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
			"--id", filename,
			"--body",
		})
	})
	if err != nil {
		t.Fatalf("RunInspectMessage(--body) error = %v stderr=%q", err, stderr)
	}
	if stdout != senderBody {
		t.Fatalf("--body stdout changed sender body:\n got %q\nwant %q", stdout, senderBody)
	}
}

func TestRunInspectMessageBodyKeepsOrdinaryMarkdownHorizontalRule(t *testing.T) {
	fixture := writeInspectMessageFixture(t)
	filename := "20260506-010107-from-orchestrator-to-worker.md"
	body := "Intro\n\n---\n\nDetails"
	content := inspectMessageFixture("orchestrator", "worker", filename, nil, body)
	readPath := filepath.Join(fixture.sessionDir, "read", filename)
	writeInspectMessageFile(t, readPath, content)

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunInspectMessage([]string{
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
			"--id", filename,
			"--body",
		})
	})
	if err != nil {
		t.Fatalf("RunInspectMessage(--body) error = %v stderr=%q", err, stderr)
	}
	if stdout != body+"\n" {
		t.Fatalf("--body stdout changed ordinary body:\n got %q\nwant %q", stdout, body+"\n")
	}
}

func TestRunInspectMessageReturnsNotFoundAndAmbiguous(t *testing.T) {
	t.Run("wrong id", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		got := runInspectMessageForFixture(t, fixture, "missing.md")
		if got.Status != "not_found" || got.MatchCount != 0 || got.Message != nil {
			t.Fatalf("inspect output = %#v, want not_found", got)
		}
	})

	t.Run("ambiguous id", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		filename := "20260506-010104-from-orchestrator-to-worker.md"
		content := inspectMessageFixture("orchestrator", "worker", filename, nil, "duplicate")
		writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "read", filename), content)
		writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "inbox", "worker", filename), content)

		got := runInspectMessageForFixture(t, fixture, filename)
		if got.Status != "ambiguous" || got.MatchCount != 2 || got.Message != nil {
			t.Fatalf("inspect output = %#v, want ambiguous two matches", got)
		}
		if len(got.Matches) != 2 {
			t.Fatalf("matches len = %d, want 2", len(got.Matches))
		}
	})
}

type inspectMessageFixtureState struct {
	baseDir     string
	contextID   string
	sessionName string
	sessionDir  string
}

func writeInspectMessageFixture(t *testing.T) inspectMessageFixtureState {
	t.Helper()
	baseDir := t.TempDir()
	contextID := "ctx-inspect-message"
	sessionName := "test-session"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	installFakeTmuxForCLI(t, baseDir, sessionName, "worker")
	if err := os.MkdirAll(filepath.Join(sessionDir, "inbox", "worker"), 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sessionDir, "read"), 0o700); err != nil {
		t.Fatalf("MkdirAll read: %v", err)
	}
	return inspectMessageFixtureState{
		baseDir:     baseDir,
		contextID:   contextID,
		sessionName: sessionName,
		sessionDir:  sessionDir,
	}
}

func inspectMessageFixture(from, to, messageID string, fields map[string]string, body string) string {
	var builder strings.Builder
	builder.WriteString("---\nparams:\n")
	builder.WriteString("  from: " + from + "\n")
	builder.WriteString("  to: " + to + "\n")
	builder.WriteString("  messageId: " + messageID + "\n")
	builder.WriteString("  timestamp: 2026-05-06T01:01:01Z\n")
	for _, key := range []string{"replyPolicy", "replyTo", "input_request_id", "fills_input_request_id", "input_request_set_id", "branch_id", "completion_rule"} {
		if value := fields[key]; value != "" {
			builder.WriteString("  " + key + ": " + value + "\n")
		}
	}
	builder.WriteString("---\n\n")
	builder.WriteString(body)
	builder.WriteString("\n")
	return builder.String()
}

func writeInspectMessageFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func runInspectMessageForFixture(t *testing.T, fixture inspectMessageFixtureState, id string) inspectMessageOutput {
	t.Helper()
	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunInspectMessage([]string{
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
			"--id", id,
		})
	})
	if err != nil {
		t.Fatalf("RunInspectMessage() error = %v stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	return decodeInspectMessageOutputForTest(t, stdout)
}

func decodeInspectMessageOutputForTest(t *testing.T, stdout string) inspectMessageOutput {
	t.Helper()
	var got inspectMessageOutput
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	return got
}
