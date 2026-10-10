package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
)

// An unread inbox message has not been claimed with pop, so inspect-message must
// not expose its content (#875: pop is the only way to consume mail).
func TestRunInspectMessageRefusesUnpoppedInboxMessage(t *testing.T) {
	fixture := writeInspectMessageFixture(t)
	filename := "20260506-010101-from-orchestrator-to-worker.md"
	secretBody := "UNPOPPED-BODY-MARKER please inspect this"
	content := inspectMessageFixture("orchestrator", "worker", filename, map[string]string{
		"replyPolicy":      "required",
		"input_request_id": "ireq_123",
	}, secretBody)
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
	if got.Status != "not_claimed" || got.Message != nil || len(got.Matches) != 0 {
		t.Fatalf("inspect output = %#v, want not_claimed with no message content", got)
	}
	for _, leaked := range []string{secretBody, "ireq_123", inboxPath} {
		if strings.Contains(stdout, leaked) {
			t.Fatalf("not_claimed output leaked %q: %s", leaked, stdout)
		}
	}
	if _, err := os.Stat(inboxPath); err != nil {
		t.Fatalf("inspect-message must not move unread inbox file: %v", err)
	}

	for _, mode := range []string{"--path", "--body"} {
		stdout, _, err := captureCommandOutput(t, func() error {
			return RunInspectMessage([]string{
				"--context-id", fixture.contextID,
				"--session", fixture.sessionName,
				"--id", filename,
				mode,
			})
		})
		if err == nil || !strings.Contains(err.Error(), "not_claimed") {
			t.Fatalf("RunInspectMessage(%s) error = %v, want a not_claimed refusal", mode, err)
		}
		if strings.Contains(stdout, secretBody) || strings.Contains(stdout, inboxPath) {
			t.Fatalf("RunInspectMessage(%s) stdout leaked unpopped content: %q", mode, stdout)
		}
	}
}

// Once the message was popped (archived under read/), the read copy is the only
// match even if an unread copy with the same id is still in the inbox.
func TestRunInspectMessageIgnoresUnreadCopyWhenReadCopyExists(t *testing.T) {
	fixture := writeInspectMessageFixture(t)
	filename := "20260506-010108-from-orchestrator-to-worker.md"
	content := inspectMessageFixture("orchestrator", "worker", filename, nil, "popped body")
	readPath := filepath.Join(fixture.sessionDir, "read", filename)
	writeInspectMessageFile(t, readPath, content)
	writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "inbox", "worker", filename), content)

	got := runInspectMessageForFixture(t, fixture, filename)
	if got.Status != "found" || got.MatchCount != 1 || got.Message == nil || got.Message.StorageState != "read" || got.Message.MarkdownPath != readPath {
		t.Fatalf("inspect output = %#v, want exactly the read copy", got)
	}
}

// A read/ file alone is not proof of a successful pop: a pop that failed after
// the archive rename can leave an orphan there (#802 rollback race). Archives
// from the receipt era need a valid pop receipt for this message id.
func TestRunInspectMessageRequiresPopReceiptForReceiptEraArchives(t *testing.T) {
	secretBody := "ORPHAN-BODY-MARKER"
	writeReceipt := func(t *testing.T, fixture inspectMessageFixtureState, filename, contents string) {
		t.Helper()
		stem := strings.TrimSuffix(filename, filepath.Ext(filename))
		writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "read", stem+".pop.json"), contents)
	}
	assertRefused := func(t *testing.T, fixture inspectMessageFixtureState, filename, wantReason string) {
		t.Helper()
		stdout, stderr, err := captureCommandOutput(t, func() error {
			return RunInspectMessage([]string{"--context-id", fixture.contextID, "--session", fixture.sessionName, "--id", filename})
		})
		if err != nil || stderr != "" {
			t.Fatalf("RunInspectMessage() error = %v stderr=%q", err, stderr)
		}
		got := decodeInspectMessageOutputForTest(t, stdout)
		if got.Status != "not_claimed" || got.Reason != wantReason || got.Message != nil || len(got.Matches) != 0 {
			t.Fatalf("inspect output = %#v, want not_claimed/%s with no message content", got, wantReason)
		}
		if strings.Contains(stdout, secretBody) || strings.Contains(stdout, "read/") {
			t.Fatalf("not_claimed output leaked content or a path: %s", stdout)
		}
		if _, _, err := captureCommandOutput(t, func() error {
			return RunInspectMessage([]string{"--context-id", fixture.contextID, "--session", fixture.sessionName, "--id", filename, "--body"})
		}); err == nil || !strings.Contains(err.Error(), "not_claimed") {
			t.Fatalf("RunInspectMessage(--body) error = %v, want a not_claimed refusal", err)
		}
	}

	t.Run("orphan read without receipt", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		filename := "20261009-120000-from-orchestrator-to-worker.md"
		writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "read", filename), inspectMessageFixture("orchestrator", "worker", filename, nil, secretBody))
		assertRefused(t, fixture, filename, "no_pop_receipt")
	})

	t.Run("orphan read plus unread inbox copy", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		filename := "20261009-120001-from-orchestrator-to-worker.md"
		content := inspectMessageFixture("orchestrator", "worker", filename, nil, secretBody)
		writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "read", filename), content)
		writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "inbox", "worker", filename), content)
		assertRefused(t, fixture, filename, "no_pop_receipt")
	})

	t.Run("receipt for a different message id", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		filename := "20261009-120002-from-orchestrator-to-worker.md"
		writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "read", filename), inspectMessageFixture("orchestrator", "worker", filename, nil, secretBody))
		writeReceipt(t, fixture, filename, `{"status":"message","message_id":"20261009-999999-from-someone-else.md"}`)
		assertRefused(t, fixture, filename, "no_pop_receipt")
	})

	t.Run("unparseable receipt", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		filename := "20261009-120003-from-orchestrator-to-worker.md"
		writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "read", filename), inspectMessageFixture("orchestrator", "worker", filename, nil, secretBody))
		writeReceipt(t, fixture, filename, "not json")
		assertRefused(t, fixture, filename, "no_pop_receipt")
	})

	t.Run("valid receipt is found", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		filename := "20261009-120004-from-orchestrator-to-worker.md"
		readPath := filepath.Join(fixture.sessionDir, "read", filename)
		writeInspectMessageFile(t, readPath, inspectMessageFixture("orchestrator", "worker", filename, nil, "popped body"))
		writeReceipt(t, fixture, filename, `{"status":"message","message_id":"`+filename+`"}`)
		got := runInspectMessageForFixture(t, fixture, filename)
		if got.Status != "found" || got.MatchCount != 1 || got.Message == nil || got.Message.MarkdownPath != readPath {
			t.Fatalf("inspect output = %#v, want the receipted read archive", got)
		}
	})

	t.Run("archive older than receipts needs none", func(t *testing.T) {
		fixture := writeInspectMessageFixture(t)
		filename := "20260501-120000-from-orchestrator-to-worker.md"
		readPath := filepath.Join(fixture.sessionDir, "read", filename)
		writeInspectMessageFile(t, readPath, inspectMessageFixture("orchestrator", "worker", filename, nil, "legacy body"))
		got := runInspectMessageForFixture(t, fixture, filename)
		if got.Status != "found" || got.Message == nil || got.Message.MarkdownPath != readPath {
			t.Fatalf("inspect output = %#v, want the legacy read archive found without a receipt", got)
		}
	})
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
		envelope.SenderBodyBoundaryForMessageID(filename),
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
		// Two archived files that carry the same messageId in their metadata.
		writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "read", filename), content)
		// The copy keeps a pre-receipt timestamp prefix so both archives count as claimed.
		writeInspectMessageFile(t, filepath.Join(fixture.sessionDir, "read", "20260504-000000-copy-of-"+filename), content)

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
