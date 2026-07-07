package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

type executeBashFixture struct {
	baseDir     string
	contextID   string
	sessionName string
	sessionDir  string
	now         time.Time
	policies    []config.CommandApprovalPolicy
	stdout      bytes.Buffer
	stderr      bytes.Buffer
	runCount    int
	commands    []string
	runStatus   int
	runErr      error
}

func newExecuteBashFixture(t *testing.T, policies ...config.CommandApprovalPolicy) *executeBashFixture {
	t.Helper()

	baseDir := t.TempDir()
	contextID := "ctx-484"
	sessionName := "test-session"
	return &executeBashFixture{
		baseDir:     baseDir,
		contextID:   contextID,
		sessionName: sessionName,
		sessionDir:  filepath.Join(baseDir, contextID, sessionName),
		now:         time.Date(2026, time.June, 1, 10, 0, 0, 0, time.UTC),
		policies:    policies,
	}
}

func (f *executeBashFixture) context() commandContext {
	return commandContext{
		stdout: &f.stdout,
		stderr: &f.stderr,
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{
				BaseDir:         f.baseDir,
				CommandApproval: f.policies,
			}, nil
		},
		getTmuxPaneName:    func() string { return "worker" },
		getTmuxSessionName: func() string { return f.sessionName },
		now:                func() time.Time { return f.now },
		runBash: func(command string, stdout, stderr io.Writer) (int, error) {
			f.runCount++
			f.commands = append(f.commands, command)
			_, _ = fmt.Fprint(stdout, "ran\n")
			return f.runStatus, f.runErr
		},
	}
}

func (f *executeBashFixture) args(extra ...string) []string {
	base := []string{
		"--context-id", f.contextID,
		"--session", f.sessionName,
		"--requester", "worker",
	}
	return append(base, extra...)
}

func TestRunExecuteBashAdvisoryRecordsRequestWithoutCommandTextAndRuns(t *testing.T) {
	fixture := newExecuteBashFixture(t)
	commandText := "printf secret-token"

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "low-risk",
		"--category", "diagnostic",
		"--reviewer", "orchestrator",
		"--mode", "advisory",
		"--reason", "collect harmless diagnostic",
		"--command", commandText,
	))
	if err != nil {
		t.Fatalf("runExecuteBashWithContext() error = %v", err)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount = %d, want 1", fixture.runCount)
	}
	if got := fixture.commands[0]; got != commandText {
		t.Fatalf("command = %q, want %q", got, commandText)
	}

	events := replayCommandEvents(t, fixture.sessionDir)
	var requestPayload journal.CommandApprovalRequestPayload
	foundRequest := false
	for _, event := range events {
		if bytes.Contains(event.Payload, []byte(commandText)) || bytes.Contains(event.Payload, []byte("command_text")) {
			t.Fatalf("event %s stored full command text by default: %s", event.Type, event.Payload)
		}
		if event.Type == journal.CommandApprovalRequestedEventType {
			foundRequest = true
			if err := json.Unmarshal(event.Payload, &requestPayload); err != nil {
				t.Fatalf("Unmarshal(request): %v", err)
			}
		}
	}
	if !foundRequest {
		t.Fatal("missing command approval request event")
	}
	if requestPayload.Requester != "worker" || requestPayload.Reviewer != "orchestrator" {
		t.Fatalf("request requester/reviewer = %q/%q", requestPayload.Requester, requestPayload.Reviewer)
	}
	if requestPayload.Mode != "advisory" || requestPayload.Label != "low-risk" || requestPayload.Category != "diagnostic" {
		t.Fatalf("request policy metadata = %#v", requestPayload)
	}
	if requestPayload.CommandHash == "" || requestPayload.Reason == "" || requestPayload.ExpiresAt == "" {
		t.Fatalf("request missing digest, reason, or expiry: %#v", requestPayload)
	}
}

func TestRunExecuteBashStoreCommandTextOptIn(t *testing.T) {
	fixture := newExecuteBashFixture(t)
	commandText := "printf audit-me"

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "diagnostic",
		"--mode", "advisory",
		"--reviewer", "orchestrator",
		"--store-command-text",
		"--command", commandText,
	))
	if err != nil {
		t.Fatalf("runExecuteBashWithContext() error = %v", err)
	}

	for _, event := range replayCommandEvents(t, fixture.sessionDir) {
		if bytes.Contains(event.Payload, []byte(commandText)) && bytes.Contains(event.Payload, []byte("command_text")) {
			return
		}
	}
	t.Fatal("no audit event stored command_text after explicit opt in")
}

func TestRunExecuteBashWarnOnlyRequiresOverride(t *testing.T) {
	policy := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "deploy",
		Mode:      "warn-only",
	}
	fixture := newExecuteBashFixture(t, policy)

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "deploy",
		"--command", "printf deploy",
	))
	if err == nil {
		t.Fatal("runExecuteBashWithContext() error = nil, want warn-only block")
	}
	if !strings.Contains(err.Error(), "warn-only mode requires --override-approval") {
		t.Fatalf("error = %v, want warn-only override guidance", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

func TestRunExecuteBashWarnOnlyOverrideRunsAndAudits(t *testing.T) {
	policy := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "deploy",
		Mode:      "warn-only",
	}
	fixture := newExecuteBashFixture(t, policy)

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "deploy",
		"--override-approval",
		"--command", "printf deploy",
	))
	if err != nil {
		t.Fatalf("runExecuteBashWithContext() error = %v", err)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount = %d, want 1", fixture.runCount)
	}
	decision := findExecutionDecisionPayload(t, fixture.sessionDir)
	if decision.Decision != "warn_override" || !decision.Override {
		t.Fatalf("execution decision = %#v, want warn override", decision)
	}
}

func TestRunExecuteBashBlockingRefusesInvalidApprovals(t *testing.T) {
	for _, tc := range []struct {
		name       string
		setup      func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string)
		command    string
		wantReason string
	}{
		{
			name: "absent",
			setup: func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string) {
			},
			command:    "printf absent",
			wantReason: "approval is absent",
		},
		{
			name: "stale",
			setup: func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string) {
				threadID := commandApprovalThreadID(policy, commandDigest(commandText))
				fixture.appendCommandApprovalDecisionOnly(t, threadID, "orchestrator", journal.ApprovalDecisionApproved)
			},
			command:    "printf stale",
			wantReason: "approval decision is stale",
		},
		{
			name: "rejected",
			setup: func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string) {
				fixture.appendCommandApproval(t, policy, commandText, journal.ApprovalDecisionRejected, "orchestrator", fixture.now.Add(15*time.Minute))
			},
			command:    "printf rejected",
			wantReason: "approval is rejected",
		},
		{
			name: "expired",
			setup: func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string) {
				fixture.appendCommandApproval(t, policy, commandText, journal.ApprovalDecisionApproved, "orchestrator", fixture.now.Add(-time.Second))
			},
			command:    "printf expired",
			wantReason: "approval is expired",
		},
		{
			name: "wrong reviewer",
			setup: func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string) {
				fixture.appendCommandApproval(t, policy, commandText, journal.ApprovalDecisionApproved, "critic", fixture.now.Add(15*time.Minute))
			},
			command:    "printf reviewer",
			wantReason: "reviewer does not match",
		},
		{
			name: "changed digest",
			setup: func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string) {
				fixture.appendCommandApproval(t, policy, "printf original", journal.ApprovalDecisionApproved, "orchestrator", fixture.now.Add(15*time.Minute))
			},
			command:    "printf changed",
			wantReason: "different command digest",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policyConfig := config.CommandApprovalPolicy{
				Requester: "worker",
				Reviewer:  "orchestrator",
				Label:     "protected",
				Category:  "release",
				Mode:      "blocking",
			}
			fixture := newExecuteBashFixture(t, policyConfig)
			policy := resolvedCommandApprovalPolicy{
				Requester: "worker",
				Reviewer:  "orchestrator",
				Mode:      "blocking",
				Label:     "protected",
				Category:  "release",
				TTL:       defaultCommandApprovalTTL,
			}
			tc.setup(t, fixture, policy, tc.command)

			err := runExecuteBashWithContext(fixture.context(), fixture.args(
				"--label", "protected",
				"--category", "release",
				"--command", tc.command,
			))
			if err == nil {
				t.Fatal("runExecuteBashWithContext() error = nil, want blocking refusal")
			}
			if !strings.Contains(err.Error(), tc.wantReason) {
				t.Fatalf("error = %v, want reason containing %q", err, tc.wantReason)
			}
			if fixture.runCount != 0 {
				t.Fatalf("runCount = %d, want 0", fixture.runCount)
			}
		})
	}
}

func TestRunExecuteBashBlockingRunsMatchingApprovedDigest(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	policy := resolvedCommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Mode:      "blocking",
		Label:     "protected",
		Category:  "release",
		TTL:       defaultCommandApprovalTTL,
	}
	commandText := "printf approved"
	fixture.appendCommandApproval(t, policy, commandText, journal.ApprovalDecisionApproved, "orchestrator", fixture.now.Add(15*time.Minute))

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--category", "release",
		"--command", commandText,
	))
	if err != nil {
		t.Fatalf("runExecuteBashWithContext() error = %v", err)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount = %d, want 1", fixture.runCount)
	}
}

func TestRunExecuteBashBlockingRejectsExplicitThreadIDWithMismatchedDigest(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	policy := resolvedCommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Mode:      "blocking",
		Label:     "protected",
		Category:  "release",
		TTL:       defaultCommandApprovalTTL,
	}

	// Approve the original command.
	originalCommand := "printf original-command"
	approvedThreadID := fixture.appendCommandApproval(t, policy, originalCommand, journal.ApprovalDecisionApproved, "orchestrator", fixture.now.Add(15*time.Minute))

	// Attempt to execute a different command using the approved thread ID.
	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--category", "release",
		"--thread-id", approvedThreadID,
		"--command", "printf attack-command",
	))
	if err == nil {
		t.Fatal("runExecuteBashWithContext() error = nil, want digest_mismatch block")
	}
	if !strings.Contains(err.Error(), "different command digest") {
		t.Fatalf("error = %v, want reason containing \"different command digest\"", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0; command must not execute on digest mismatch", fixture.runCount)
	}
}

func TestRunExecuteBashPropagatesExitStatus(t *testing.T) {
	fixture := newExecuteBashFixture(t)
	fixture.runStatus = 7

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "diagnostic",
		"--mode", "advisory",
		"--reviewer", "orchestrator",
		"--command", "exit 7",
	))
	if err == nil {
		t.Fatal("runExecuteBashWithContext() error = nil, want exit status")
	}
	var exitErr commandExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %T %v, want commandExitError", err, err)
	}
	if exitErr.ExitCode() != 7 {
		t.Fatalf("ExitCode() = %d, want 7", exitErr.ExitCode())
	}
	completed := findExecutionCompletedPayload(t, fixture.sessionDir)
	if completed.ExitStatus != 7 {
		t.Fatalf("completed exit status = %d, want 7", completed.ExitStatus)
	}
}

func TestRunExecuteBashRecordDecisionAndInspectCommandApprovals(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	policy := resolvedCommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Mode:      "blocking",
		Label:     "protected",
		TTL:       defaultCommandApprovalTTL,
	}
	commandText := "printf approve-me"
	threadID := fixture.appendCommandApprovalRequest(t, policy, commandText, time.Now().Add(time.Hour))

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--thread-id", threadID,
		"--reviewer", "orchestrator",
		"--record-decision", "approved",
		"--reason", "digest reviewed",
	))
	if err != nil {
		t.Fatalf("runExecuteBashWithContext(record decision) error = %v", err)
	}

	configPath := fixture.writeConfigFile(t)
	stdout, _, err := captureCommandOutput(t, func() error {
		return RunInspectCommandApprovals([]string{
			"--config", configPath,
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
		})
	})
	if err != nil {
		t.Fatalf("RunInspectCommandApprovals() error = %v", err)
	}
	var output inspectCommandApprovalsOutput
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatalf("Unmarshal(inspect output): %v\n%s", err, stdout)
	}
	thread, ok := output.Threads[threadID]
	if !ok {
		t.Fatalf("inspect output missing thread %q: %#v", threadID, output.Threads)
	}
	if thread.Status != projection.CommandApprovalStatusApproved {
		t.Fatalf("thread status = %q, want approved", thread.Status)
	}
	if thread.DecidedAt == "" {
		t.Fatalf("thread missing decided_at: %#v", thread)
	}
}

func (f *executeBashFixture) appendCommandApproval(t *testing.T, policy resolvedCommandApprovalPolicy, commandText string, decision journal.ApprovalDecision, decisionReviewer string, expiresAt time.Time) string {
	t.Helper()

	threadID := f.appendCommandApprovalRequest(t, policy, commandText, expiresAt)
	f.appendCommandApprovalDecisionOnly(t, threadID, decisionReviewer, decision)
	return threadID
}

func (f *executeBashFixture) appendCommandApprovalRequest(t *testing.T, policy resolvedCommandApprovalPolicy, commandText string, expiresAt time.Time) string {
	t.Helper()

	writer := f.openWriter(t)
	commandHash := commandDigest(commandText)
	threadID := commandApprovalThreadID(policy, commandHash)
	_, err := writer.AppendEventWithOptions(
		journal.CommandApprovalRequestedEventType,
		journal.VisibilityOperatorVisible,
		journal.CommandApprovalRequestPayload{
			Requester:   policy.Requester,
			Reviewer:    policy.Reviewer,
			Mode:        policy.Mode,
			Label:       policy.Label,
			Category:    policy.Category,
			CommandHash: commandHash,
			Reason:      "review requested",
			ExpiresAt:   expiresAt.UTC().Format(time.RFC3339Nano),
		},
		journal.AppendOptions{ThreadID: threadID},
		f.now,
	)
	if err != nil {
		t.Fatalf("AppendEventWithOptions(request): %v", err)
	}
	return threadID
}

func (f *executeBashFixture) appendCommandApprovalDecisionOnly(t *testing.T, threadID, reviewer string, decision journal.ApprovalDecision) {
	t.Helper()

	writer := f.openWriter(t)
	_, err := writer.AppendEventWithOptions(
		journal.CommandApprovalDecidedEventType,
		journal.VisibilityOperatorVisible,
		journal.CommandApprovalDecisionPayload{
			Reviewer: reviewer,
			Decision: decision,
			Reason:   "reviewed",
		},
		journal.AppendOptions{ThreadID: threadID},
		f.now,
	)
	if err != nil {
		t.Fatalf("AppendEventWithOptions(decision): %v", err)
	}
}

func (f *executeBashFixture) openWriter(t *testing.T) *journal.Writer {
	t.Helper()

	writer, err := journal.OpenCurrentWriter(f.sessionDir)
	if err == nil {
		return writer
	}
	writer, err = journal.OpenShadowWriter(f.sessionDir, f.contextID, f.sessionName, os.Getpid(), f.now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	return writer
}

func (f *executeBashFixture) writeConfigFile(t *testing.T) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "postman.toml")
	content := fmt.Sprintf("[postman]\nbase_dir = %q\nedges = [\"worker --- orchestrator\"]\n", f.baseDir)
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}
	return configPath
}

func replayCommandEvents(t *testing.T, sessionDir string) []journal.Event {
	t.Helper()

	events, err := journal.Replay(sessionDir)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	var commandEvents []journal.Event
	for _, event := range events {
		if strings.HasPrefix(event.Type, "command_") {
			commandEvents = append(commandEvents, event)
		}
	}
	return commandEvents
}

func findExecutionDecisionPayload(t *testing.T, sessionDir string) journal.CommandExecutionDecisionPayload {
	t.Helper()

	for _, event := range replayCommandEvents(t, sessionDir) {
		if event.Type != journal.CommandExecutionDecidedEventType {
			continue
		}
		var payload journal.CommandExecutionDecisionPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("Unmarshal(execution decision): %v", err)
		}
		return payload
	}
	t.Fatal("missing command execution decision event")
	return journal.CommandExecutionDecisionPayload{}
}

func findExecutionCompletedPayload(t *testing.T, sessionDir string) journal.CommandExecutionCompletedPayload {
	t.Helper()

	for _, event := range replayCommandEvents(t, sessionDir) {
		if event.Type != journal.CommandExecutionCompletedEventType {
			continue
		}
		var payload journal.CommandExecutionCompletedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("Unmarshal(execution completed): %v", err)
		}
		return payload
	}
	t.Fatal("missing command execution completed event")
	return journal.CommandExecutionCompletedPayload{}
}
