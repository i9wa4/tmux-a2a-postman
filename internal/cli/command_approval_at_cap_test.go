package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

// commandApprovalInboxQueueCap mirrors the mailbox cap enforced by the real
// trusted delivery path (internal/message inboxQueueCap = 20). The test fills
// the approver's real inbox directory to this size and drives the REAL
// delivery path (no stub), so a change of the production cap makes this
// acceptance proof fail loudly instead of silently testing a different cap.
const commandApprovalInboxQueueCap = 20

// TestRunExecuteBashBlockingApproverInboxAtCapFailsClosed is the #840 item 3
// acceptance proof: when the approver's inbox is at capacity, a blocking
// command-approval request must fail closed -- the command never runs, the
// requester gets a distinct, correlatable delivery_failed outcome, nothing is
// evicted or dead-lettered, the request identity is retained in the journal,
// and a later retry (after the inbox drains) reuses the SAME request instead
// of minting a new one. It is coverage for the existing behavior, not a
// capacity mitigation.
func TestRunExecuteBashBlockingApproverInboxAtCapFailsClosed(t *testing.T) {
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
	commandText := "printf approver-inbox-at-cap"
	threadID := commandApprovalThreadID(policy, commandDigest(commandText))
	args := fixture.args("--label", "protected", "--category", "release", "--command", commandText)

	// Fill the approver's real inbox to exactly the cap with unrelated unread
	// mail that must survive untouched.
	inboxDir := filepath.Join(fixture.sessionDir, "inbox", "orchestrator")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(inbox) error = %v", err)
	}
	queued := make([]string, 0, commandApprovalInboxQueueCap)
	for i := range commandApprovalInboxQueueCap {
		name := fmt.Sprintf("20260601-0959%02d-r%04d-from-worker-to-orchestrator.md", i, i)
		if err := os.WriteFile(filepath.Join(inboxDir, name), []byte("unread backlog\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(queued %d) error = %v", i, err)
		}
		queued = append(queued, name)
	}
	listInbox := func() []string {
		t.Helper()
		entries, err := os.ReadDir(inboxDir)
		if err != nil {
			t.Fatalf("ReadDir(inbox) error = %v", err)
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		return names
	}

	// First attempt: the inbox is full.
	ctx := fixture.context()
	ctx.sleep = func(context.Context, time.Duration) {
		t.Fatal("ctx.sleep called, want an at-cap delivery failure to fail fast without waiting")
	}
	err := runExecuteBashWithContext(ctx, args)
	if err == nil {
		t.Fatal("error = nil, want delivery_failed refusal at a full approver inbox")
	}
	var outcomeErr commandApprovalOutcomeError
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "delivery_failed" {
		t.Fatalf("error = %#v, want commandApprovalOutcomeError{status: delivery_failed}", err)
	}
	if outcomeErr.ExitCode() != 19 {
		t.Fatalf("ExitCode() = %d, want 19", outcomeErr.ExitCode())
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0 (no command may run without a recorded approval)", fixture.runCount)
	}

	// Nothing evicted, nothing added, nothing dead-lettered.
	if got := listInbox(); len(got) != commandApprovalInboxQueueCap {
		t.Fatalf("approver inbox entries = %d, want exactly %d (cap unchanged, no eviction, no overflow write): %v", len(got), commandApprovalInboxQueueCap, got)
	}
	for _, name := range queued {
		if _, statErr := os.Stat(filepath.Join(inboxDir, name)); statErr != nil {
			t.Fatalf("pre-existing unread message %s was not preserved: %v", name, statErr)
		}
	}
	deadEntries, readErr := os.ReadDir(filepath.Join(fixture.sessionDir, "dead-letter"))
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("ReadDir(dead-letter) error = %v", readErr)
	}
	if len(deadEntries) != 0 {
		t.Fatalf("dead-letter entries = %d, want 0 (command-approval at cap stays retryable, not dead-lettered)", len(deadEntries))
	}

	// The request identity is retained, correlatable by thread id.
	state0, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now)
	if err != nil || !ok {
		t.Fatalf("ProjectCommandApprovalState() = (%#v, %v, %v)", state0, ok, err)
	}
	firstInputRequestID := state0.Threads[threadID].InputRequestID
	if firstInputRequestID == "" {
		t.Fatal("at-cap failure did not retain the request identity for recovery")
	}
	// Each attempt must leave exactly one command_execution_decided event with
	// the same thread and command hash and a non-empty decision and reason, so
	// the at-cap refusal is auditable per attempt (Guardian F840A-1).
	// The expected hash comes from the command itself, never from an emitted
	// payload, so a consistently wrong recorded hash cannot pass.
	wantCommandHash := commandDigest(commandText)
	assertDecisionEvents := func(label string, want int) {
		t.Helper()
		var decided []journal.CommandExecutionDecisionPayload
		for _, event := range replayCommandEvents(t, fixture.sessionDir) {
			if event.Type != journal.CommandExecutionDecidedEventType {
				continue
			}
			var payload journal.CommandExecutionDecisionPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("%s: Unmarshal(command_execution_decided) error = %v", label, err)
			}
			decided = append(decided, payload)
		}
		if len(decided) != want {
			t.Fatalf("%s: command_execution_decided events = %d, want exactly %d: %#v", label, len(decided), want, decided)
		}
		for i, payload := range decided {
			if payload.ApprovalThread != threadID {
				t.Fatalf("%s: decided[%d].ApprovalThread = %q, want %q", label, i, payload.ApprovalThread, threadID)
			}
			if payload.CommandHash != wantCommandHash {
				t.Fatalf("%s: decided[%d].CommandHash = %q, want %q (same command on every attempt)", label, i, payload.CommandHash, wantCommandHash)
			}
			if payload.Decision == "" || payload.Reason == "" {
				t.Fatalf("%s: decided[%d] has empty decision %q or reason %q", label, i, payload.Decision, payload.Reason)
			}
		}
	}
	assertDecisionEvents("first at-cap attempt", 1)
	// Nothing else may leak into the mailbox pipeline at cap (Guardian F840A-3).
	for _, stray := range []string{"post", "draft"} {
		matches, globErr := filepath.Glob(filepath.Join(fixture.sessionDir, stray, "*"))
		if globErr != nil {
			t.Fatalf("Glob(%s) error = %v", stray, globErr)
		}
		if len(matches) != 0 {
			t.Fatalf("%s entries at cap = %v, want none", stray, matches)
		}
	}
	requestCount := func() int {
		count := 0
		for _, event := range replayCommandEvents(t, fixture.sessionDir) {
			if event.Type == journal.CommandApprovalRequestedEventType {
				count++
			}
		}
		return count
	}
	if got := requestCount(); got != 1 {
		t.Fatalf("command_approval_requested events = %d, want exactly 1", got)
	}

	// Retry while STILL full: same failure, same request, nothing new minted.
	err = runExecuteBashWithContext(fixture.context(), args)
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "delivery_failed" {
		t.Fatalf("retry at cap error = %#v, want delivery_failed again", err)
	}
	if got := requestCount(); got != 1 {
		t.Fatalf("command_approval_requested events after retry at cap = %d, want still 1", got)
	}
	assertDecisionEvents("retry at cap", 2)
	if fixture.runCount != 0 {
		t.Fatalf("runCount after retry at cap = %d, want 0", fixture.runCount)
	}

	// Operator drains ONE message: the next retry re-delivers the SAME request.
	if err := os.Remove(filepath.Join(inboxDir, queued[0])); err != nil {
		t.Fatalf("Remove(drain one) error = %v", err)
	}
	err = runExecuteBashWithContext(fixture.context(), args)
	if err == nil {
		t.Fatal("retry after drain error = nil, want a pending blocking refusal (delivered, awaiting decision)")
	}
	if errors.As(err, &outcomeErr) && outcomeErr.status == "delivery_failed" {
		t.Fatalf("retry after drain error = %#v, want delivery to succeed now that there is room", outcomeErr)
	}
	state1, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now)
	if err != nil || !ok {
		t.Fatalf("ProjectCommandApprovalState() (after drain) = (%#v, %v, %v)", state1, ok, err)
	}
	if got := state1.Threads[threadID].InputRequestID; got != firstInputRequestID {
		t.Fatalf("input_request_id after drain = %q, want unchanged %q", got, firstInputRequestID)
	}
	if got := requestCount(); got != 1 {
		t.Fatalf("command_approval_requested events after drain = %d, want exactly 1", got)
	}
	// The filler files are hand-written and not journaled, so the post-delivery
	// projection sync may reconcile them away; assert only that the request
	// itself reached the approver inbox, carrying the retained correlation.
	assertDecisionEvents("retry after drain", 3)
	// Anchor each frontmatter key to its own line: a bare substring check for
	// "input_request_id: <id>" would also match "fills_input_request_id: <id>"
	// (Guardian F840A-2).
	// Frontmatter params are indented by exactly two spaces; a looser pattern
	// could match an instruction line in the message body.
	threadLine := regexp.MustCompile(`(?m)^ {2}thread_id: ` + regexp.QuoteMeta(threadID) + `$`)
	requestLine := regexp.MustCompile(`(?m)^ {2}input_request_id: ` + regexp.QuoteMeta(firstInputRequestID) + `$`)
	delivered := false
	for _, name := range listInbox() {
		body, readErr := os.ReadFile(filepath.Join(inboxDir, name))
		if readErr != nil {
			t.Fatalf("ReadFile(%s) error = %v", name, readErr)
		}
		if threadLine.Match(body) && requestLine.Match(body) {
			delivered = true
		}
	}
	if !delivered {
		t.Fatalf("approver inbox after drain has no request carrying thread_id %s and input_request_id %s: %v", threadID, firstInputRequestID, listInbox())
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount after drain retry = %d, want 0 (approval still undecided)", fixture.runCount)
	}
}
