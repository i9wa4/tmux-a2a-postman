package projection

import (
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

func inputRequestContent(from, to, messageID, replyPolicy, replyTo, body string) string {
	return inputRequestContentWithExact(from, to, messageID, replyPolicy, replyTo, "", "", body)
}

func inputRequestContentWithExact(from, to, messageID, replyPolicy, replyTo, inputRequestID, fillsInputRequestID, body string) string {
	replyToLine := ""
	if replyTo != "" {
		replyToLine = "  replyTo: " + replyTo + "\n"
	}
	inputRequestIDLine := ""
	if inputRequestID != "" {
		inputRequestIDLine = "  input_request_id: " + inputRequestID + "\n"
	}
	fillsInputRequestIDLine := ""
	if fillsInputRequestID != "" {
		fillsInputRequestIDLine = "  fills_input_request_id: " + fillsInputRequestID + "\n"
	}
	return "---\nparams:\n" +
		"  from: " + from + "\n" +
		"  to: " + to + "\n" +
		"  messageId: " + messageID + "\n" +
		"  replyPolicy: " + replyPolicy + "\n" +
		replyToLine +
		inputRequestIDLine +
		fillsInputRequestIDLine +
		"---\n\n" + body + "\n"
}

func commandApprovalInputContent(from, to, messageID, replyPolicy, threadID, inputRequestID, fillsInputRequestID, commandHash, body string) string {
	inputRequestIDLine := ""
	if inputRequestID != "" {
		inputRequestIDLine = "  input_request_id: " + inputRequestID + "\n"
	}
	fillsInputRequestIDLine := ""
	if fillsInputRequestID != "" {
		fillsInputRequestIDLine = "  fills_input_request_id: " + fillsInputRequestID + "\n"
	}
	commandHashLine := ""
	if commandHash != "" {
		commandHashLine = "  command_hash: " + commandHash + "\n"
	}
	return "---\nparams:\n" +
		"  from: " + from + "\n" +
		"  to: " + to + "\n" +
		"  messageId: " + messageID + "\n" +
		"  replyPolicy: " + replyPolicy + "\n" +
		"  thread_id: " + threadID + "\n" +
		inputRequestIDLine +
		fillsInputRequestIDLine +
		commandHashLine +
		"---\n\n" + body + "\n"
}

func verdictContent(from, to, messageID, verdict, verdictOf, body string) string {
	return "---\nparams:\n" +
		"  from: " + from + "\n" +
		"  to: " + to + "\n" +
		"  messageId: " + messageID + "\n" +
		"  replyPolicy: none\n" +
		"  verdict: " + verdict + "\n" +
		"  verdictOf: " + verdictOf + "\n" +
		"---\n\n" + body + "\n"
}

func appendInputRequestMailboxEvent(t *testing.T, writer *journal.Writer, eventType string, messageID, from, to, content string, now time.Time) {
	t.Helper()
	if _, err := writer.AppendEvent(eventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: messageID,
		From:      from,
		To:        to,
		Content:   content,
	}, now); err != nil {
		t.Fatalf("AppendEvent(%s, %s): %v", eventType, messageID, err)
	}
}

func TestProjectMessageInputRequestState_ReplayFixturesRebuildOpenFilledAndUncertainStates(t *testing.T) {
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name                    string
		appendReply             func(t *testing.T, writer *journal.Writer, now time.Time)
		wantInputRequiredCount  int
		wantWaitingOnInputCount int
	}{
		{
			name:                    "open required request stays visible after replay",
			wantInputRequiredCount:  1,
			wantWaitingOnInputCount: 1,
		},
		{
			name: "exact fill closes the replayed request",
			appendReply: func(t *testing.T, writer *journal.Writer, now time.Time) {
				reply := inputRequestContentWithExact("worker", "orchestrator", "m2.md", "none", "", "", "ireq_replay_123", "DONE")
				appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))
				appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(5*time.Second))
			},
			wantInputRequiredCount:  0,
			wantWaitingOnInputCount: 0,
		},
		{
			name: "missing exact fill target keeps the replayed request open",
			appendReply: func(t *testing.T, writer *journal.Writer, now time.Time) {
				reply := inputRequestContentWithExact("worker", "orchestrator", "m2.md", "none", "", "", "ireq_missing", "DONE")
				appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))
				appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(5*time.Second))
			},
			wantInputRequiredCount:  1,
			wantWaitingOnInputCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionDir := t.TempDir()

			writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
			if err != nil {
				t.Fatalf("OpenShadowWriter() error = %v", err)
			}

			request := inputRequestContentWithExact("orchestrator", "worker", "m1.md", "required", "", "ireq_replay_123", "", "please work")
			appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
			appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))
			appendInputRequestMailboxEvent(t, writer, MailboxProjectionReadEventType, "m1.md", "orchestrator", "worker", request, now.Add(3*time.Second))

			if tt.appendReply != nil {
				tt.appendReply(t, writer, now)
			}

			events, err := journal.Replay(sessionDir)
			if err != nil {
				t.Fatalf("Replay() error = %v", err)
			}
			if len(events) == 0 {
				t.Fatal("Replay() returned no events, want persisted durable facts")
			}

			got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
			if err != nil {
				t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
			}
			if !ok {
				t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
			}
			if got.InputRequiredCounts["worker"] != tt.wantInputRequiredCount {
				t.Fatalf("worker action required = %d, want %d", got.InputRequiredCounts["worker"], tt.wantInputRequiredCount)
			}
			if got.WaitingOnInputCounts["orchestrator"] != tt.wantWaitingOnInputCount {
				t.Fatalf("orchestrator waiting = %d, want %d", got.WaitingOnInputCounts["orchestrator"], tt.wantWaitingOnInputCount)
			}
			if tt.wantInputRequiredCount == 1 {
				if len(got.InputRequired) != 1 || got.InputRequired[0].InputRequestID != "ireq_replay_123" {
					t.Fatalf("input required details = %#v, want replayed ireq_replay_123 left open", got.InputRequired)
				}
				if got.InputRequired[0].OpenedEventID == "" || got.InputRequired[0].ReadEventID == "" {
					t.Fatalf("input required detail = %#v, want replayable opened/read event ids", got.InputRequired[0])
				}
			}
		})
	}
}

func TestProjectMessageInputRequestState_ProjectRequestSatisfaction(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	filledRequest := inputRequestContentWithExact("orchestrator", "worker", "m1.md", "required", "", "ireq_filled", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", filledRequest, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", filledRequest, now.Add(2*time.Second))
	filledReply := inputRequestContentWithExact("worker", "orchestrator", "m2.md", "none", "m1.md", "", "ireq_filled", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", filledReply, now.Add(10*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", filledReply, now.Add(11*time.Second))

	openRequest := inputRequestContentWithExact("orchestrator", "worker", "m3.md", "required", "", "ireq_open", "", "please also do this")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m3.md", "orchestrator", "worker", openRequest, now.Add(20*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m3.md", "orchestrator", "worker", openRequest, now.Add(30*time.Second))

	got, ok, err := ProjectMessageInputRequestStateAt(sessionDir, "review", now.Add(3700*time.Second), 3600)
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestStateAt() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestStateAt() ok = false, want true")
	}

	worker := got.RequestSatisfaction["worker"]
	if worker.OpenedCount != 2 || worker.FilledCount != 1 || worker.OpenCount != 1 || worker.StaleOpenCount != 1 {
		t.Fatalf("worker request satisfaction = %#v, want opened=2 filled=1 open=1 stale=1", worker)
	}
	if worker.AverageTimeToFillSeconds != 8 {
		t.Fatalf("worker average time to fill = %d, want 8", worker.AverageTimeToFillSeconds)
	}
	if worker.LongestOpenAgeSeconds != 3670 {
		t.Fatalf("worker longest open age = %d, want 3670", worker.LongestOpenAgeSeconds)
	}
	if worker.StaleAfterSeconds != 3600 {
		t.Fatalf("worker stale threshold = %d, want 3600", worker.StaleAfterSeconds)
	}
}

func TestProjectMessageInputRequestState_ProjectDeadLetteredRequestSatisfaction(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	deadLetteredRequest := inputRequestContentWithExact("orchestrator", "worker", "m1.md", "required", "", "ireq_deadlettered", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeadLetteredEventType, "m1.md", "orchestrator", "worker", deadLetteredRequest, now.Add(time.Second))

	got, ok, err := ProjectMessageInputRequestStateAt(sessionDir, "review", now.Add(3700*time.Second), 3600)
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestStateAt() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestStateAt() ok = false, want true")
	}

	worker := got.RequestSatisfaction["worker"]
	if worker.OpenedCount != 1 || worker.FilledCount != 0 || worker.OpenCount != 1 || worker.DeadLetteredCount != 1 || worker.StaleOpenCount != 1 {
		t.Fatalf("worker request satisfaction = %#v, want opened=1 filled=0 open=1 dead_lettered=1 stale=1", worker)
	}
	if worker.LongestOpenAgeSeconds != 3699 {
		t.Fatalf("worker longest open age = %d, want 3699", worker.LongestOpenAgeSeconds)
	}
}

func TestProjectMessageInputRequestState_CommandApprovalDeadLetterRequiresAcceptedTupleAndOrder(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	threadID := "command-approval-reply-slot"
	inputRequestID := "ireq_command_approval_tuple"
	commandHash := "sha256:tuple"
	requestMessageID := "request.md"
	request := commandApprovalInputContent("worker", "approver", requestMessageID, "required", threadID, inputRequestID, "", commandHash, "approval requested")
	if _, err := writer.AppendEventWithOptions(journal.CommandApprovalRequestedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalRequestPayload{
		Requester:              "worker",
		RequesterAddress:       "review:worker",
		Reviewer:               "orchestrator",
		CommandApproverNode:    "approver",
		CommandApproverAddress: "review:approver",
		Mode:                   "blocking",
		Label:                  "protected",
		CommandHash:            commandHash,
		InputRequestID:         inputRequestID,
	}, journal.AppendOptions{ThreadID: threadID}, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendEventWithOptions(request) error = %v", err)
	}
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, requestMessageID, "worker", "approver", request, now.Add(2*time.Second))

	staleMessageID := "stale-reused.md"
	if _, err := writer.AppendEventWithOptions(journal.CommandApprovalDecidedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalDecisionPayload{
		Reviewer:         "approver",
		ReviewerAddress:  "review:approver",
		RequesterAddress: "review:worker",
		Decision:         journal.ApprovalDecisionRejected,
		Reason:           "wrong stale hash",
		MessageID:        staleMessageID,
		InputRequestID:   inputRequestID,
		CommandHash:      "sha256:stale",
	}, journal.AppendOptions{ThreadID: threadID}, now.Add(3*time.Second)); err != nil {
		t.Fatalf("AppendEventWithOptions(stale decision) error = %v", err)
	}
	staleReply := commandApprovalInputContent("approver", "worker", staleMessageID, "none", threadID, "", inputRequestID, commandHash, "NOT APPROVED: stale message id")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeadLetteredEventType, staleMessageID, "approver", "worker", staleReply, now.Add(4*time.Second))
	assertCommandApprovalReplySlotForProjection(t, sessionDir, inputRequestID, true)

	reversedMessageID := "reversed.md"
	reversedReply := commandApprovalInputContent("approver", "worker", reversedMessageID, "none", threadID, "", inputRequestID, commandHash, "NOT APPROVED: reversed order")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeadLetteredEventType, reversedMessageID, "approver", "worker", reversedReply, now.Add(5*time.Second))
	if _, err := writer.AppendEventWithOptions(journal.CommandApprovalDecidedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalDecisionPayload{
		Reviewer:         "approver",
		ReviewerAddress:  "review:approver",
		RequesterAddress: "review:worker",
		Decision:         journal.ApprovalDecisionRejected,
		Reason:           "decision after dead-letter",
		MessageID:        reversedMessageID,
		InputRequestID:   inputRequestID,
		CommandHash:      commandHash,
	}, journal.AppendOptions{ThreadID: threadID}, now.Add(6*time.Second)); err != nil {
		t.Fatalf("AppendEventWithOptions(reversed decision) error = %v", err)
	}
	assertCommandApprovalReplySlotForProjection(t, sessionDir, inputRequestID, false)

	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeadLetteredEventType, reversedMessageID, "approver", "worker", reversedReply, now.Add(7*time.Second))
	assertCommandApprovalReplySlotForProjection(t, sessionDir, inputRequestID, false)
}

func TestProjectMessageInputRequestState_CommandApprovalReplyResolutionIsGatedAcrossMailboxEvents(t *testing.T) {
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	for _, eventType := range []string{MailboxProjectionPostConsumedEventType, MailboxProjectionDeliveredEventType, MailboxProjectionDeadLetteredEventType} {
		for _, decision := range []journal.ApprovalDecision{journal.ApprovalDecisionApproved, journal.ApprovalDecisionRejected} {
			for _, variant := range []string{"accepted", "wrong-decision-hash", "missing-message-id", "wrong-message-id", "wrong-decision-input", "reviewer-mismatch", "requester-mismatch", "missing-reply-input", "wrong-reply-input", "missing-reply-hash", "wrong-reply-hash", "participant-mismatch", "reply-before-decision"} {
				name := eventType + "/" + string(decision) + "/" + variant
				t.Run(name, func(t *testing.T) {
					sessionDir := t.TempDir()
					writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
					if err != nil {
						t.Fatalf("OpenShadowWriter() error = %v", err)
					}
					threadID, inputRequestID, commandHash := "command-approval-gated", "ireq_gated", "sha256:gated"
					request := commandApprovalInputContent("worker", "approver", "request.md", "required", threadID, inputRequestID, "", commandHash, "approval requested")
					if _, err := writer.AppendEventWithOptions(journal.CommandApprovalRequestedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalRequestPayload{
						Requester: "worker", RequesterAddress: "review:worker", Reviewer: "orchestrator", CommandApproverNode: "approver", CommandApproverAddress: "review:approver", Mode: "blocking", Label: "protected", CommandHash: commandHash, InputRequestID: inputRequestID,
					}, journal.AppendOptions{ThreadID: threadID}, now.Add(time.Second)); err != nil {
						t.Fatalf("AppendEventWithOptions(request) error = %v", err)
					}
					appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "request.md", "worker", "approver", request, now.Add(2*time.Second))

					replyID, decisionID, decisionHash, decisionInput := "decision.md", "decision.md", commandHash, inputRequestID
					if variant == "wrong-decision-hash" {
						decisionHash = "sha256:wrong"
					}
					if variant == "missing-message-id" {
						decisionID = ""
					}
					if variant == "wrong-message-id" {
						decisionID = "other-decision.md"
					}
					if variant == "wrong-decision-input" {
						decisionInput = "ireq_other"
					}
					reviewerAddress, requesterAddress := "review:approver", "review:worker"
					if variant == "reviewer-mismatch" {
						reviewerAddress = "review:other"
					}
					if variant == "requester-mismatch" {
						requesterAddress = "review:other"
					}
					replyFrom, replyTo, replyInput, replyHash := "approver", "worker", inputRequestID, commandHash
					if variant == "participant-mismatch" {
						replyFrom = "worker"
					}
					if variant == "missing-reply-input" {
						replyInput = ""
					}
					if variant == "wrong-reply-input" {
						replyInput = "ireq_other"
					}
					if variant == "missing-reply-hash" {
						replyHash = ""
					}
					if variant == "wrong-reply-hash" {
						replyHash = "sha256:wrong"
					}
					reply := commandApprovalInputContent(replyFrom, replyTo, replyID, "none", threadID, "", replyInput, replyHash, "decision")
					appendDecision := func(at time.Time) {
						t.Helper()
						if _, err := writer.AppendEventWithOptions(journal.CommandApprovalDecidedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalDecisionPayload{
							Reviewer: "approver", ReviewerAddress: reviewerAddress, RequesterAddress: requesterAddress, Decision: decision, Reason: "test", MessageID: decisionID, InputRequestID: decisionInput, CommandHash: decisionHash,
						}, journal.AppendOptions{ThreadID: threadID}, at); err != nil {
							t.Fatalf("AppendEventWithOptions(decision) error = %v", err)
						}
					}
					if variant == "reply-before-decision" {
						appendInputRequestMailboxEvent(t, writer, eventType, replyID, replyFrom, replyTo, reply, now.Add(3*time.Second))
						appendDecision(now.Add(4 * time.Second))
					} else {
						appendDecision(now.Add(3 * time.Second))
						appendInputRequestMailboxEvent(t, writer, eventType, replyID, replyFrom, replyTo, reply, now.Add(4*time.Second))
						// Replay the same reply event to prove the shared resolver is exact-once.
						appendInputRequestMailboxEvent(t, writer, eventType, replyID, replyFrom, replyTo, reply, now.Add(5*time.Second))
					}

					state, ok, err := ProjectMessageInputRequestStateAt(sessionDir, "review", now.Add(3700*time.Second), 3600)
					if err != nil || !ok {
						t.Fatalf("ProjectMessageInputRequestStateAt() = (%#v, %v, %v)", state, ok, err)
					}
					stats := state.RequestSatisfaction["approver"]
					if variant == "accepted" || variant == "reply-before-decision" {
						assertCommandApprovalReplySlotForProjection(t, sessionDir, inputRequestID, false)
						wantLatency := 1
						if variant == "reply-before-decision" {
							wantLatency = 2
						}
						if stats.OpenedCount != 1 || stats.FilledCount != 1 || stats.OpenCount != 0 || stats.StaleOpenCount != 0 || stats.TotalTimeToFillSeconds != wantLatency || stats.AverageTimeToFillSeconds != wantLatency {
							t.Fatalf("accepted %s satisfaction = %#v, want one exact fill", eventType, stats)
						}
					} else {
						assertCommandApprovalReplySlotForProjection(t, sessionDir, inputRequestID, true)
						if stats.OpenedCount != 1 || stats.FilledCount != 0 || stats.OpenCount != 1 || stats.StaleOpenCount != 1 || stats.TotalTimeToFillSeconds != 0 || stats.AverageTimeToFillSeconds != 0 {
							t.Fatalf("rejected %s satisfaction = %#v, want open stale request", eventType, stats)
						}
					}
				})
			}
		}
	}
}

func TestProjectMessageInputRequestState_CommandApprovalOpeningRejectsUntrustedReplyThreadAcrossMailboxEvents(t *testing.T) {
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	for _, eventType := range []string{MailboxProjectionPostConsumedEventType, MailboxProjectionDeliveredEventType, MailboxProjectionDeadLetteredEventType} {
		for _, threadID := range []string{"", "ordinary-thread", "command-approval-other"} {
			t.Run(eventType+"/thread="+threadID, func(t *testing.T) {
				sessionDir := t.TempDir()
				writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
				if err != nil {
					t.Fatalf("OpenShadowWriter() error = %v", err)
				}
				const commandThread = "command-approval-opening-bound"
				const inputRequestID = "ireq_opening_bound"
				const commandHash = "sha256:opening-bound"
				request := commandApprovalInputContent("worker", "approver", "request.md", "required", commandThread, inputRequestID, "", commandHash, "approval requested")
				if _, err := writer.AppendEventWithOptions(journal.CommandApprovalRequestedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalRequestPayload{
					Requester: "worker", RequesterAddress: "review:worker", Reviewer: "orchestrator", CommandApproverNode: "approver", CommandApproverAddress: "review:approver", Mode: "blocking", Label: "protected", CommandHash: commandHash, InputRequestID: inputRequestID,
				}, journal.AppendOptions{ThreadID: commandThread}, now.Add(time.Second)); err != nil {
					t.Fatalf("AppendEventWithOptions(request) error = %v", err)
				}
				appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "request.md", "worker", "approver", request, now.Add(2*time.Second))
				if _, err := writer.AppendEventWithOptions(journal.CommandApprovalDecidedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalDecisionPayload{
					Reviewer: "approver", ReviewerAddress: "review:approver", RequesterAddress: "review:worker", Decision: journal.ApprovalDecisionRejected, Reason: "test", MessageID: "decision.md", InputRequestID: inputRequestID, CommandHash: commandHash,
				}, journal.AppendOptions{ThreadID: commandThread}, now.Add(3*time.Second)); err != nil {
					t.Fatalf("AppendEventWithOptions(decision) error = %v", err)
				}
				reply := commandApprovalInputContent("approver", "worker", "decision.md", "none", threadID, "", inputRequestID, commandHash, "decision")
				appendInputRequestMailboxEvent(t, writer, eventType, "decision.md", "approver", "worker", reply, now.Add(4*time.Second))
				state, ok, err := ProjectMessageInputRequestStateAt(sessionDir, "review", now.Add(3700*time.Second), 3600)
				if err != nil || !ok {
					t.Fatalf("ProjectMessageInputRequestStateAt() = (%#v, %v, %v)", state, ok, err)
				}
				assertCommandApprovalReplySlotForProjection(t, sessionDir, inputRequestID, true)
				stats := state.RequestSatisfaction["approver"]
				if stats.OpenedCount != 1 || stats.FilledCount != 0 || stats.OpenCount != 1 || stats.StaleOpenCount != 1 || stats.TotalTimeToFillSeconds != 0 || stats.AverageTimeToFillSeconds != 0 {
					t.Fatalf("untrusted %s thread %q satisfaction = %#v, want unchanged open request", eventType, threadID, stats)
				}
			})
		}
	}
}

func TestProjectMessageInputRequestState_CommandApprovalDecisionBeforeOpeningEventuallyResolves(t *testing.T) {
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	for _, eventType := range []string{MailboxProjectionPostConsumedEventType, MailboxProjectionDeliveredEventType, MailboxProjectionDeadLetteredEventType} {
		for _, decision := range []journal.ApprovalDecision{journal.ApprovalDecisionApproved, journal.ApprovalDecisionRejected} {
			t.Run(eventType+"/"+string(decision), func(t *testing.T) {
				sessionDir := t.TempDir()
				writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
				if err != nil {
					t.Fatalf("OpenShadowWriter() error = %v", err)
				}
				const threadID = "command-approval-decision-first"
				const inputRequestID = "ireq_decision_first"
				const commandHash = "sha256:decision-first"
				if _, err := writer.AppendEventWithOptions(journal.CommandApprovalRequestedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalRequestPayload{
					Requester: "worker", RequesterAddress: "review:worker", Reviewer: "orchestrator", CommandApproverNode: "approver", CommandApproverAddress: "review:approver", Mode: "blocking", Label: "protected", CommandHash: commandHash, InputRequestID: inputRequestID,
				}, journal.AppendOptions{ThreadID: threadID}, now.Add(time.Second)); err != nil {
					t.Fatalf("AppendEventWithOptions(request) error = %v", err)
				}
				if _, err := writer.AppendEventWithOptions(journal.CommandApprovalDecidedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalDecisionPayload{
					Reviewer: "approver", ReviewerAddress: "review:approver", RequesterAddress: "review:worker", Decision: decision, Reason: "test", MessageID: "decision.md", InputRequestID: inputRequestID, CommandHash: commandHash,
				}, journal.AppendOptions{ThreadID: threadID}, now.Add(2*time.Second)); err != nil {
					t.Fatalf("AppendEventWithOptions(decision) error = %v", err)
				}
				reply := commandApprovalInputContent("approver", "worker", "decision.md", "none", threadID, "", inputRequestID, commandHash, "decision")
				appendInputRequestMailboxEvent(t, writer, eventType, "decision.md", "approver", "worker", reply, now.Add(3*time.Second))
				request := commandApprovalInputContent("worker", "approver", "request.md", "required", threadID, inputRequestID, "", commandHash, "approval requested")
				appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "request.md", "worker", "approver", request, now.Add(4*time.Second))
				state, ok, err := ProjectMessageInputRequestStateAt(sessionDir, "review", now.Add(3700*time.Second), 3600)
				if err != nil || !ok {
					t.Fatalf("ProjectMessageInputRequestStateAt() = (%#v, %v, %v)", state, ok, err)
				}
				assertCommandApprovalReplySlotForProjection(t, sessionDir, inputRequestID, false)
				stats := state.RequestSatisfaction["approver"]
				if stats.OpenedCount != 1 || stats.FilledCount != 1 || stats.OpenCount != 0 || stats.StaleOpenCount != 0 || stats.TotalTimeToFillSeconds != 0 || stats.AverageTimeToFillSeconds != 0 {
					t.Fatalf("decision-before-opening %s satisfaction = %#v, want exact fill", eventType, stats)
				}
			})
		}
	}
}

func TestProjectMessageInputRequestState_CommandApprovalReplyBeforeOpeningEventuallyResolves(t *testing.T) {
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	for _, eventType := range []string{MailboxProjectionPostConsumedEventType, MailboxProjectionDeliveredEventType, MailboxProjectionDeadLetteredEventType} {
		for _, decision := range []journal.ApprovalDecision{journal.ApprovalDecisionApproved, journal.ApprovalDecisionRejected} {
			t.Run(eventType+"/"+string(decision), func(t *testing.T) {
				sessionDir := t.TempDir()
				writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
				if err != nil {
					t.Fatalf("OpenShadowWriter() error = %v", err)
				}
				const threadID = "command-approval-reply-first"
				const inputRequestID = "ireq_reply_first"
				const commandHash = "sha256:reply-first"
				if _, err := writer.AppendEventWithOptions(journal.CommandApprovalRequestedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalRequestPayload{
					Requester: "worker", RequesterAddress: "review:worker", Reviewer: "orchestrator", CommandApproverNode: "approver", CommandApproverAddress: "review:approver", Mode: "blocking", Label: "protected", CommandHash: commandHash, InputRequestID: inputRequestID,
				}, journal.AppendOptions{ThreadID: threadID}, now.Add(time.Second)); err != nil {
					t.Fatalf("AppendEventWithOptions(request) error = %v", err)
				}
				reply := commandApprovalInputContent("approver", "worker", "decision.md", "none", threadID, "", inputRequestID, commandHash, "decision")
				appendInputRequestMailboxEvent(t, writer, eventType, "decision.md", "approver", "worker", reply, now.Add(3*time.Second))
				if _, err := writer.AppendEventWithOptions(journal.CommandApprovalDecidedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalDecisionPayload{
					Reviewer: "approver", ReviewerAddress: "review:approver", RequesterAddress: "review:worker", Decision: decision, Reason: "test", MessageID: "decision.md", InputRequestID: inputRequestID, CommandHash: commandHash,
				}, journal.AppendOptions{ThreadID: threadID}, now.Add(4*time.Second)); err != nil {
					t.Fatalf("AppendEventWithOptions(decision) error = %v", err)
				}
				request := commandApprovalInputContent("worker", "approver", "request.md", "required", threadID, inputRequestID, "", commandHash, "approval requested")
				appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "request.md", "worker", "approver", request, now.Add(5*time.Second))
				state, ok, err := ProjectMessageInputRequestStateAt(sessionDir, "review", now.Add(3700*time.Second), 3600)
				if err != nil || !ok {
					t.Fatalf("ProjectMessageInputRequestStateAt() = (%#v, %v, %v)", state, ok, err)
				}
				assertCommandApprovalReplySlotForProjection(t, sessionDir, inputRequestID, false)
				stats := state.RequestSatisfaction["approver"]
				if stats.OpenedCount != 1 || stats.FilledCount != 1 || stats.OpenCount != 0 || stats.StaleOpenCount != 0 || stats.TotalTimeToFillSeconds != 0 || stats.AverageTimeToFillSeconds != 0 {
					t.Fatalf("reply-before-opening %s satisfaction = %#v, want exact-once fill", eventType, stats)
				}
			})
		}
	}
}

func TestProjectMessageInputRequestState_MalformedCommandOpeningCannotFallBackToOrdinaryResolution(t *testing.T) {
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	for _, eventType := range []string{MailboxProjectionPostConsumedEventType, MailboxProjectionDeliveredEventType, MailboxProjectionDeadLetteredEventType} {
		for _, malformed := range []string{"missing-thread", "wrong-thread", "missing-hash", "wrong-hash", "wrong-participant", "wrong-input"} {
			t.Run(eventType+"/"+malformed, func(t *testing.T) {
				sessionDir := t.TempDir()
				writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
				if err != nil {
					t.Fatalf("OpenShadowWriter() error = %v", err)
				}
				const threadID = "command-approval-authoritative"
				const inputRequestID = "ireq_authoritative"
				const commandHash = "sha256:authoritative"
				if _, err := writer.AppendEventWithOptions(journal.CommandApprovalRequestedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalRequestPayload{
					Requester: "worker", RequesterAddress: "review:worker", Reviewer: "orchestrator", CommandApproverNode: "approver", CommandApproverAddress: "review:approver", Mode: "blocking", Label: "protected", CommandHash: commandHash, InputRequestID: inputRequestID,
				}, journal.AppendOptions{ThreadID: threadID}, now.Add(time.Second)); err != nil {
					t.Fatalf("AppendEventWithOptions(request) error = %v", err)
				}
				openingThread, openingInput, openingHash, openingFrom := threadID, inputRequestID, commandHash, "worker"
				switch malformed {
				case "missing-thread":
					openingThread = ""
				case "wrong-thread":
					openingThread = "command-approval-other"
				case "missing-hash":
					openingHash = ""
				case "wrong-hash":
					openingHash = "sha256:wrong"
				case "wrong-participant":
					openingFrom = "other"
				case "wrong-input":
					openingInput = "ireq_other"
				}
				opening := commandApprovalInputContent(openingFrom, "approver", "request.md", "required", openingThread, openingInput, "", openingHash, "approval requested")
				appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "request.md", openingFrom, "approver", opening, now.Add(2*time.Second))
				if _, err := writer.AppendEventWithOptions(journal.CommandApprovalDecidedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalDecisionPayload{
					Reviewer: "approver", ReviewerAddress: "review:approver", RequesterAddress: "review:worker", Decision: journal.ApprovalDecisionRejected, Reason: "test", MessageID: "decision.md", InputRequestID: inputRequestID, CommandHash: commandHash,
				}, journal.AppendOptions{ThreadID: threadID}, now.Add(3*time.Second)); err != nil {
					t.Fatalf("AppendEventWithOptions(decision) error = %v", err)
				}
				reply := commandApprovalInputContent("approver", "worker", "decision.md", "none", threadID, "", openingInput, commandHash, "decision")
				appendInputRequestMailboxEvent(t, writer, eventType, "decision.md", "approver", "worker", reply, now.Add(4*time.Second))
				state, ok, err := ProjectMessageInputRequestStateAt(sessionDir, "review", now.Add(3700*time.Second), 3600)
				if err != nil || !ok {
					t.Fatalf("ProjectMessageInputRequestStateAt() = (%#v, %v, %v)", state, ok, err)
				}
				stats := state.RequestSatisfaction["approver"]
				if stats.OpenedCount != 1 || stats.FilledCount != 0 || stats.OpenCount != 1 || stats.StaleOpenCount != 1 || stats.TotalTimeToFillSeconds != 0 || stats.AverageTimeToFillSeconds != 0 {
					t.Fatalf("malformed opening %s/%s satisfaction = %#v, want unchanged", eventType, malformed, stats)
				}
				if len(state.InputRequired) != 1 || state.InputRequiredCounts["approver"] != 1 {
					t.Fatalf("malformed opening %s/%s inbound = %#v, want one open slot", eventType, malformed, state)
				}
			})
		}
	}
}

func TestProjectMessageInputRequestState_OrdinaryReplyResolutionRemainsBranchSpecific(t *testing.T) {
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	t.Run("post-consumed does not resolve outbound", func(t *testing.T) {
		sessionDir := t.TempDir()
		writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
		if err != nil {
			t.Fatalf("OpenShadowWriter() error = %v", err)
		}
		request := inputRequestContentWithExact("worker", "approver", "outbound.md", "required", "", "ireq_outbound", "", "request")
		appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "outbound.md", "worker", "approver", request, now.Add(time.Second))
		reply := inputRequestContentWithExact("approver", "worker", "reply.md", "none", "", "", "ireq_outbound", "reply")
		appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "reply.md", "approver", "worker", reply, now.Add(2*time.Second))
		state, ok, err := ProjectMessageInputRequestStateAt(sessionDir, "review", now.Add(10*time.Second), 3600)
		if err != nil || !ok {
			t.Fatalf("ProjectMessageInputRequestStateAt() = (%#v, %v, %v)", state, ok, err)
		}
		if !hasInputRequest(state.WaitingOnInput, "ireq_outbound") || state.WaitingOnInputCounts["worker"] != 1 {
			t.Fatalf("post-consumed changed outbound ordinary slot: %#v", state)
		}
	})
	t.Run("delivered does not resolve inbound", func(t *testing.T) {
		sessionDir := t.TempDir()
		writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
		if err != nil {
			t.Fatalf("OpenShadowWriter() error = %v", err)
		}
		request := inputRequestContentWithExact("worker", "approver", "inbound.md", "required", "", "ireq_inbound", "", "request")
		appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "inbound.md", "worker", "approver", request, now.Add(time.Second))
		reply := inputRequestContentWithExact("approver", "worker", "reply.md", "none", "", "", "ireq_inbound", "reply")
		appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "reply.md", "approver", "worker", reply, now.Add(2*time.Second))
		state, ok, err := ProjectMessageInputRequestStateAt(sessionDir, "review", now.Add(10*time.Second), 3600)
		if err != nil || !ok {
			t.Fatalf("ProjectMessageInputRequestStateAt() = (%#v, %v, %v)", state, ok, err)
		}
		if !hasInputRequest(state.InputRequired, "ireq_inbound") || state.InputRequiredCounts["approver"] != 1 {
			t.Fatalf("delivered changed inbound ordinary slot: %#v", state)
		}
		stats := state.RequestSatisfaction["approver"]
		if stats.OpenedCount != 1 || stats.FilledCount != 1 || stats.OpenCount != 0 || stats.TotalTimeToFillSeconds != 1 || stats.AverageTimeToFillSeconds != 1 {
			t.Fatalf("delivered ordinary satisfaction = %#v, want original delivered behavior", stats)
		}
	})
}

func assertCommandApprovalReplySlotForProjection(t *testing.T, sessionDir, inputRequestID string, wantOpen bool) {
	t.Helper()
	state, ok, err := ProjectMessageInputRequestStateAt(sessionDir, "review", time.Date(2026, time.May, 10, 12, 10, 0, 0, time.UTC), DefaultInputRequestStaleAfterSeconds)
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestStateAt() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestStateAt() ok = false, want true")
	}
	if got := hasInputRequest(state.InputRequired, inputRequestID); got != wantOpen {
		t.Fatalf("InputRequired has %q = %v, want %v; state=%#v", inputRequestID, got, wantOpen, state.InputRequired)
	}
	if got := hasInputRequest(state.WaitingOnInput, inputRequestID); got != wantOpen {
		t.Fatalf("WaitingOnInput has %q = %v, want %v; state=%#v", inputRequestID, got, wantOpen, state.WaitingOnInput)
	}
	wantCount := 0
	if wantOpen {
		wantCount = 1
	}
	if got := state.InputRequiredCounts["approver"]; got != wantCount {
		t.Fatalf("InputRequiredCounts[approver] = %d, want %d", got, wantCount)
	}
	if got := state.WaitingOnInputCounts["worker"]; got != wantCount {
		t.Fatalf("WaitingOnInputCounts[worker] = %d, want %d", got, wantCount)
	}
}

func hasInputRequest(requests []InputRequestDetail, inputRequestID string) bool {
	for _, request := range requests {
		if request.InputRequestID == inputRequestID {
			return true
		}
	}
	return false
}

func TestProjectVerdictDebtState_OutgoingVerdictStampClearsDebt(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContentWithExact("orchestrator", "worker", "m1.md", "required", "", "ireq_verdict", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	fill := inputRequestContentWithExact("worker", "orchestrator", "m2.md", "none", "", "", "ireq_verdict", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", fill, now.Add(2*time.Second))

	before, ok, err := ProjectVerdictDebtState(sessionDir, "review", now.Add(10*time.Second), 3600)
	if err != nil {
		t.Fatalf("ProjectVerdictDebtState(before) error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectVerdictDebtState(before) ok = false, want true")
	}
	if before.Requesters["orchestrator"].UnstampedCount != 1 {
		t.Fatalf("before debt = %#v, want one unstamped fill", before.Requesters["orchestrator"])
	}

	verdict := verdictContent("orchestrator", "worker", "m3.md", "pass", "ireq_verdict", "accepted")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m3.md", "orchestrator", "worker", verdict, now.Add(3*time.Second))

	after, ok, err := ProjectVerdictDebtState(sessionDir, "review", now.Add(10*time.Second), 3600)
	if err != nil {
		t.Fatalf("ProjectVerdictDebtState(after) error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectVerdictDebtState(after) ok = false, want true")
	}
	if after.Requesters["orchestrator"].UnstampedCount != 0 {
		t.Fatalf("after debt = %#v, want verdict stamp to clear debt", after.Requesters["orchestrator"])
	}
}

func TestProjectVerdictDebtState_WrongRecipientVerdictDoesNotClearDebt(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContentWithExact("orchestrator", "worker", "m1.md", "required", "", "ireq_verdict_wrong_to", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	fill := inputRequestContentWithExact("worker", "orchestrator", "m2.md", "none", "", "", "ireq_verdict_wrong_to", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", fill, now.Add(2*time.Second))
	verdict := verdictContent("orchestrator", "critic", "m3.md", "pass", "ireq_verdict_wrong_to", "accepted by wrong recipient")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m3.md", "orchestrator", "critic", verdict, now.Add(3*time.Second))

	after, ok, err := ProjectVerdictDebtState(sessionDir, "review", now.Add(10*time.Second), 3600)
	if err != nil {
		t.Fatalf("ProjectVerdictDebtState(after) error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectVerdictDebtState(after) ok = false, want true")
	}
	if after.Requesters["orchestrator"].UnstampedCount != 1 {
		t.Fatalf("after debt = %#v, want wrong-recipient verdict to leave debt", after.Requesters["orchestrator"])
	}
}

func TestInputRequestMetadataFromPayloadUsesDurableMetadataFallbacks(t *testing.T) {
	meta := inputRequestMetadataFromPayload(journal.MailboxEventPayload{
		ContextID:           "ctx-replay",
		MessageID:           "m1.md",
		From:                "orchestrator",
		To:                  "worker",
		ReplyPolicy:         "required",
		ReplyTo:             "previous.md",
		MessageType:         "task",
		Timestamp:           "2026-05-10T08:00:00Z",
		InputRequestID:      "ireq_123",
		FillsInputRequestID: "ireq_prev",
		InputRequestSetID:   "ireqset_1",
		BranchID:            "branch_1",
		CompletionRule:      "all",
	})

	if meta.ContextID != "ctx-replay" || meta.MessageID != "m1.md" || meta.From != "orchestrator" || meta.To != "worker" {
		t.Fatalf("identity metadata = %#v, want durable payload fallbacks", meta)
	}
	if meta.ReplyPolicy != "required" || meta.ReplyTo != "previous.md" || meta.MessageType != "task" || meta.Timestamp != "2026-05-10T08:00:00Z" {
		t.Fatalf("lifecycle metadata = %#v, want durable payload fallbacks", meta)
	}
	if meta.InputRequestID != "ireq_123" || meta.FillsInputRequestID != "ireq_prev" || meta.InputRequestSetID != "ireqset_1" || meta.BranchID != "branch_1" || meta.CompletionRule != "all" {
		t.Fatalf("input request metadata = %#v, want durable payload fallbacks", meta)
	}
}

func TestProjectMessageInputRequestState_RepliesResolveRequiredMessages(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 20, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContent("orchestrator", "worker", "m1.md", "required", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionReadEventType, "m1.md", "orchestrator", "worker", request, now.Add(3*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.InputRequiredCounts["worker"] != 1 {
		t.Fatalf("worker action required after read = %d, want 1", got.InputRequiredCounts["worker"])
	}
	if got.WaitingOnInputCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting after send = %d, want 1", got.WaitingOnInputCounts["orchestrator"])
	}
	if len(got.InputRequired) != 1 {
		t.Fatalf("action required details = %#v, want one detail", got.InputRequired)
	}
	action := got.InputRequired[0]
	if action.Direction != "inbound" || action.MessageID != "m1.md" || action.Sender != "orchestrator" || action.Recipient != "worker" || action.ReplyPolicy != "required" {
		t.Fatalf("action detail = %#v, want inbound m1 orchestrator->worker required", action)
	}
	if action.OpenedAt != now.Add(2*time.Second).Format(time.RFC3339Nano) || action.OpenedAtSource != MailboxProjectionDeliveredEventType {
		t.Fatalf("action opened evidence = %#v, want delivered timestamp/source", action)
	}
	if action.OpenedEventID == "" {
		t.Fatalf("action opened_event_id is empty, want durable journal event id")
	}
	if action.ReadAt != now.Add(3*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("action read_at = %q, want read timestamp", action.ReadAt)
	}
	if action.ReadEventID == "" {
		t.Fatalf("action read_event_id is empty, want durable journal event id")
	}
	if len(got.WaitingOnInput) != 1 {
		t.Fatalf("waiting details = %#v, want one detail", got.WaitingOnInput)
	}
	waiting := got.WaitingOnInput[0]
	if waiting.Direction != "outbound" || waiting.MessageID != "m1.md" || waiting.Sender != "orchestrator" || waiting.Recipient != "worker" || waiting.ReplyPolicy != "required" {
		t.Fatalf("waiting detail = %#v, want outbound m1 orchestrator->worker required", waiting)
	}
	if waiting.OpenedAt != now.Add(time.Second).Format(time.RFC3339Nano) || waiting.OpenedAtSource != MailboxProjectionPostConsumedEventType {
		t.Fatalf("waiting opened evidence = %#v, want post-consumed timestamp/source", waiting)
	}
	if waiting.OpenedEventID == "" {
		t.Fatalf("waiting opened_event_id is empty, want durable journal event id")
	}
	if waiting.ReadAt != now.Add(3*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("waiting read_at = %q, want recipient read timestamp", waiting.ReadAt)
	}
	if waiting.ReadEventID == "" {
		t.Fatalf("waiting read_event_id is empty, want durable journal event id")
	}
	if action.OpenedEventID == waiting.OpenedEventID {
		t.Fatalf("opened event ids should point to distinct delivered/post-consumed events, got %q", action.OpenedEventID)
	}
	if action.ReadEventID != waiting.ReadEventID {
		t.Fatalf("read event ids should point to the same read event: action=%q waiting=%q", action.ReadEventID, waiting.ReadEventID)
	}

	reply := inputRequestContent("worker", "orchestrator", "m2.md", "none", "m1.md", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(5*time.Second))

	got, ok, err = ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() after reply error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() after reply ok = false, want true")
	}
	if got.InputRequiredCounts["worker"] != 0 {
		t.Fatalf("worker action required after reply = %d, want 0", got.InputRequiredCounts["worker"])
	}
	if got.WaitingOnInputCounts["orchestrator"] != 0 {
		t.Fatalf("orchestrator waiting after reply = %d, want 0", got.WaitingOnInputCounts["orchestrator"])
	}
	if got.InfoUnreadCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator info unread = %d, want 1", got.InfoUnreadCounts["orchestrator"])
	}
	if len(got.InputRequired) != 0 || len(got.WaitingOnInput) != 0 {
		t.Fatalf("input request details after reply = action:%#v waiting:%#v, want empty", got.InputRequired, got.WaitingOnInput)
	}
}

func TestProjectMessageInputRequestState_ReplyWithoutReplyToDoesNotResolve(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 25, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContent("orchestrator", "worker", "m1.md", "required", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))

	reply := inputRequestContent("worker", "orchestrator", "m2.md", "none", "", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(3*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.InputRequiredCounts["worker"] != 1 {
		t.Fatalf("worker action required = %d, want 1", got.InputRequiredCounts["worker"])
	}
	if got.WaitingOnInputCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnInputCounts["orchestrator"])
	}
}

func TestProjectMessageInputRequestState_ExactFillResolvesRequiredMessage(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 24, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContentWithExact("orchestrator", "worker", "m1.md", "required", "", "ireq_123", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))

	reply := inputRequestContentWithExact("worker", "orchestrator", "m2.md", "none", "", "", "ireq_123", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(3*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.InputRequiredCounts["worker"] != 0 {
		t.Fatalf("worker action required = %d, want 0", got.InputRequiredCounts["worker"])
	}
	if got.WaitingOnInputCounts["orchestrator"] != 0 {
		t.Fatalf("orchestrator waiting = %d, want 0", got.WaitingOnInputCounts["orchestrator"])
	}
}

func TestProjectMessageInputRequestState_ExactFillWithMatchingReplyToResolvesRequiredMessage(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 24, 15, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContentWithExact("orchestrator", "worker", "m1.md", "required", "", "ireq_123", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))

	reply := inputRequestContentWithExact("worker", "orchestrator", "m2.md", "none", "m1.md", "", "ireq_123", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(3*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.InputRequiredCounts["worker"] != 0 {
		t.Fatalf("worker action required = %d, want 0", got.InputRequiredCounts["worker"])
	}
	if got.WaitingOnInputCounts["orchestrator"] != 0 {
		t.Fatalf("orchestrator waiting = %d, want 0", got.WaitingOnInputCounts["orchestrator"])
	}
}

func TestProjectMessageInputRequestState_ExactInputRequestIgnoresReplyToFallback(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 24, 30, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContentWithExact("orchestrator", "worker", "m1.md", "required", "", "ireq_123", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))

	reply := inputRequestContent("worker", "orchestrator", "m2.md", "none", "m1.md", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(3*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.InputRequiredCounts["worker"] != 1 {
		t.Fatalf("worker action required = %d, want 1", got.InputRequiredCounts["worker"])
	}
	if got.WaitingOnInputCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnInputCounts["orchestrator"])
	}
}

func TestProjectMessageInputRequestState_MismatchedReplyToFailsExactClose(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 24, 45, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContentWithExact("orchestrator", "worker", "m1.md", "required", "", "ireq_123", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))

	reply := inputRequestContentWithExact("worker", "orchestrator", "m2.md", "none", "other.md", "", "ireq_123", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(3*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.InputRequiredCounts["worker"] != 1 {
		t.Fatalf("worker action required = %d, want 1", got.InputRequiredCounts["worker"])
	}
	if got.WaitingOnInputCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnInputCounts["orchestrator"])
	}
}

func TestProjectMessageInputRequestState_ReplyToMissDoesNotResolve(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 26, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContent("orchestrator", "worker", "m1.md", "required", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(2*time.Second))

	reply := inputRequestContent("worker", "orchestrator", "m2.md", "none", "missing.md", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "worker", "orchestrator", reply, now.Add(3*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(4*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.InputRequiredCounts["worker"] != 1 {
		t.Fatalf("worker action required = %d, want 1", got.InputRequiredCounts["worker"])
	}
	if got.WaitingOnInputCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnInputCounts["orchestrator"])
	}
}

func TestProjectMessageInputRequestState_TracksMultipleRecipients(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 30, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	workerRequest := inputRequestContent("orchestrator", "worker", "m1.md", "required", "", "please work")
	criticRequest := inputRequestContent("orchestrator", "critic", "m2.md", "required", "", "please review")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", workerRequest, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", workerRequest, now.Add(2*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m2.md", "orchestrator", "critic", criticRequest, now.Add(3*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "orchestrator", "critic", criticRequest, now.Add(4*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.WaitingOnInputCounts["orchestrator"] != 2 {
		t.Fatalf("orchestrator waiting = %d, want 2", got.WaitingOnInputCounts["orchestrator"])
	}
	if got.InputRequiredCounts["worker"] != 1 || got.InputRequiredCounts["critic"] != 1 {
		t.Fatalf("action counts = %#v, want worker=1 critic=1", got.InputRequiredCounts)
	}

	workerReply := inputRequestContent("worker", "orchestrator", "m3.md", "none", "m1.md", "ACK")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m3.md", "worker", "orchestrator", workerReply, now.Add(5*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m3.md", "worker", "orchestrator", workerReply, now.Add(6*time.Second))

	got, ok, err = ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() after reply error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() after reply ok = false, want true")
	}
	if got.WaitingOnInputCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting after one reply = %d, want 1", got.WaitingOnInputCounts["orchestrator"])
	}
	if got.InputRequiredCounts["worker"] != 0 || got.InputRequiredCounts["critic"] != 1 {
		t.Fatalf("action counts after one reply = %#v, want worker=0 critic=1", got.InputRequiredCounts)
	}
}

func TestProjectMessageInputRequestState_ReplyToDoesNotMatchSessionQualifiedRecipientBySimpleName(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 50, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContent("orchestrator", "remote:worker", "m1.md", "required", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "remote:worker", request, now.Add(time.Second))
	reply := inputRequestContent("worker", "orchestrator", "m2.md", "none", "m1.md", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(2*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.WaitingOnInputCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnInputCounts["orchestrator"])
	}
}

func TestProjectMessageInputRequestState_ReplyToMatchesSessionQualifiedParticipant(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 51, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	request := inputRequestContent("orchestrator", "remote:worker", "m1.md", "required", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "remote:worker", request, now.Add(time.Second))
	reply := inputRequestContent("remote:worker", "orchestrator", "m2.md", "none", "m1.md", "DONE")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "remote:worker", "orchestrator", reply, now.Add(2*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.WaitingOnInputCounts["orchestrator"] != 0 {
		t.Fatalf("orchestrator waiting = %d, want 0", got.WaitingOnInputCounts["orchestrator"])
	}
}

func TestProjectMessageInputRequestState_SkipsIncompleteMailboxEvents(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 55, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	if _, err := writer.AppendEvent(MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, map[string]string{
		"to": "worker",
	}, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendEvent(incomplete delivered): %v", err)
	}
	content := inputRequestContent("orchestrator", "worker", "m1.md", "required", "", "please work")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "m1.md", "orchestrator", "worker", content, now.Add(2*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", content, now.Add(3*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.InputRequiredCounts["worker"] != 1 {
		t.Fatalf("worker action required = %d, want 1", got.InputRequiredCounts["worker"])
	}
	if got.WaitingOnInputCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnInputCounts["orchestrator"])
	}
}

func TestProjectMessageInputRequestState_KeysInputRequestsByMessageAndRecipient(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.May, 3, 9, 40, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	workerRequest := inputRequestContent("orchestrator", "worker", "broadcast.md", "required", "", "please work")
	criticRequest := inputRequestContent("orchestrator", "critic", "broadcast.md", "required", "", "please review")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "broadcast.md", "orchestrator", "worker", workerRequest, now.Add(time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "broadcast.md", "orchestrator", "worker", workerRequest, now.Add(2*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "broadcast.md", "orchestrator", "critic", criticRequest, now.Add(3*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "broadcast.md", "orchestrator", "critic", criticRequest, now.Add(4*time.Second))

	workerReply := inputRequestContent("worker", "orchestrator", "worker-reply.md", "none", "broadcast.md", "ACK")
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionPostConsumedEventType, "worker-reply.md", "worker", "orchestrator", workerReply, now.Add(5*time.Second))
	appendInputRequestMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "worker-reply.md", "worker", "orchestrator", workerReply, now.Add(6*time.Second))

	got, ok, err := ProjectMessageInputRequestState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestState() ok = false, want true")
	}
	if got.WaitingOnInputCounts["orchestrator"] != 1 {
		t.Fatalf("orchestrator waiting = %d, want 1", got.WaitingOnInputCounts["orchestrator"])
	}
	if got.InputRequiredCounts["worker"] != 0 || got.InputRequiredCounts["critic"] != 1 {
		t.Fatalf("action counts = %#v, want worker=0 critic=1", got.InputRequiredCounts)
	}
}
