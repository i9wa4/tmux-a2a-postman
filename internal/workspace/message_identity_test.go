package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/messageid"
)

func seedLegacyMessage(t *testing.T, m Mail, rt Runtime, msg Message, draft bool) {
	t.Helper()
	err := m.Store.WithVerified(context.Background(), rt, func(ws *os.Root, _ Snapshot) error {
		if draft {
			return m.stage(ws, msg)
		}
		return m.begin(ws, mailTransaction{Version: 1, Kind: "send", Message: msg, Body: msg.Body})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTimestampedMailMatchesCreationMetadataAndFixedInputToken(t *testing.T) {
	m := mailFixture(t)
	msg := sendMail(t, m)
	stamp, ok := messageid.Timestamp(msg.ID)
	created, err := time.Parse(time.RFC3339Nano, msg.CreatedAt)
	if err != nil || !ok || !stamp.Equal(created) || !requestID(msg.InputRequestID) || msg.InputRequestID != "ireq_"+messageid.Suffix(msg.ID) {
		t.Fatal("message filename, metadata and input correlation disagree", msg, err)
	}
	msg.CreatedAt = created.Add(time.Nanosecond).Format(time.RFC3339Nano)
	if err := validateMessage(msg); err == nil {
		t.Fatal("timestamp/metadata mismatch was accepted")
	}
}

func TestMixedLegacyAndTimestampedMailCanBeClaimedAndCombined(t *testing.T) {
	m := mailFixture(t)
	ctx := context.Background()
	legacy := Message{
		ID: "m-" + strings.Repeat("a", 32), From: "peer", To: "worker", NeedsReply: true,
		CreatedAt: "2026-01-01T00:00:00Z", Body: "legacy request\n", GuidanceVersion: 1,
		InputRequestID: "ireq_" + strings.Repeat("a", 32),
	}
	seedLegacyMessage(t, m, mailRuntime("peer"), legacy, false)
	before, err := os.ReadFile(mailPath(m, "peer", "sent", legacy.ID))
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := m.Send(ctx, mailRuntime("peer"), "worker", "new request\n", "", true)
	if err != nil {
		t.Fatal(err)
	}
	claimed := map[string]bool{}
	for range 2 {
		claim, err := m.Claim(ctx, mailRuntime("worker"), "")
		if err != nil || claim == nil {
			t.Fatal(claim, err)
		}
		claimed[claim.Message.ID] = true
	}
	if !claimed[legacy.ID] || !claimed[fresh.ID] {
		t.Fatal("mixed inventory lost a request", claimed)
	}
	reply, err := m.SendReply(ctx, mailRuntime("worker"), []string{legacy.ID, fresh.ID}, "combined result\n")
	if err != nil || len(reply.Replies) != 2 || reply.NeedsReply {
		t.Fatal(reply, err)
	}
	if _, ok := messageid.Timestamp(reply.ID); !ok {
		t.Fatal("combined reply did not use new identity", reply.ID)
	}
	after, err := os.ReadFile(mailPath(m, "peer", "sent", legacy.ID))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("legacy archive was rewritten", err)
	}
}

func TestLegacyExecutionRequestRetainsExactDecisionCorrelation(t *testing.T) {
	for _, approve := range []bool{true, false} {
		t.Run(map[bool]string{true: "approve", false: "decline"}[approve], func(t *testing.T) {
			m := mailFixture(t)
			operation := "m-" + strings.Repeat("b", 32)
			raw, err := json.Marshal(ApprovalRequest{
				Version: 2, Command: "printf fixture", OperationID: operation,
				Reason: "legacy invocation", CWD: t.TempDir(), Shell: "/bin/sh",
			})
			if err != nil {
				t.Fatal(err)
			}
			request := Message{
				ID: "m-" + strings.Repeat("c", 32), From: "worker", To: "peer", Kind: "execution-request",
				NeedsReply: true, InputRequestID: "ireq_" + strings.Repeat("c", 32), GuidanceVersion: 1,
				CreatedAt: "2026-01-01T00:00:00Z", Body: string(raw),
			}
			seedLegacyMessage(t, m, mailRuntime("worker"), request, false)
			claim, err := m.Claim(context.Background(), mailRuntime("peer"), "")
			if err != nil || claim == nil {
				t.Fatal(claim, err)
			}
			decision, err := m.DecideExecution(context.Background(), mailRuntime("peer"), request.ID, "exact reviewed source", approve)
			if err != nil || len(decision.Replies) != 1 || decision.Replies[0].MessageID != request.ID || decision.Replies[0].InputRequestID != request.InputRequestID {
				t.Fatal(decision, err)
			}
			var payload ApprovalDecision
			if err := json.Unmarshal([]byte(decision.Body), &payload); err != nil || payload.OperationID != operation || (payload.Decision == "approved") != approve {
				t.Fatal(payload, err)
			}
			if _, ok := messageid.Timestamp(decision.ID); !ok {
				t.Fatal("new decision lacks timestamp", decision.ID)
			}
		})
	}
}
