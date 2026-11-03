package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/messageid"
)

func legacyExternalInput(b ExternalBinding, name, body string) Message {
	id := "m-" + externalHash(b.key() + "\x00" + strings.ToLower(name))[:32]
	return Message{
		ID: id, From: externalRole(b.key()), To: b.Node, NeedsReply: true, InputRequestID: "ireq_" + id[2:],
		CreatedAt: "2026-01-01T00:00:00Z", Body: body, GuidanceVersion: 1,
		External: &ExternalMessage{Binding: b.key(), Filename: name, ExchangeID: id, Direction: "in"},
	}
}

func TestLegacyExternalAssignmentsSurviveSentAndDraftRetries(t *testing.T) {
	for _, draft := range []bool{false, true} {
		t.Run(map[bool]string{false: "sent", true: "draft"}[draft], func(t *testing.T) {
			ctx := context.Background()
			m, rt, b := externalFixture(t)
			old := legacyExternalInput(b, "legacy.md", "legacy literal input\n")
			seedLegacyMessage(t, m, rt, old, draft)
			publishExternal(t, b, old.External.Filename, old.Body)
			if _, err := m.SyncExternal(ctx, rt, b); err != nil {
				t.Fatal(err)
			}
			claim := claimExternal(t, m, rt)
			if !sameMessage(claim.Message, old) {
				t.Fatal("legacy incoming assignment was replaced", claim.Message)
			}
			state, scope, body := "pending", externalHash("legacy scope"), "legacy question"
			id := "m-" + externalHash(b.key() + "\x00" + old.ID + "\x00" + state + "\x00" + scope + "\x00" + body)[:32]
			report := Message{
				ID: id, From: b.Node, To: old.From, CreatedAt: "2026-01-02T00:00:00Z", Body: body,
				External: &ExternalMessage{Binding: b.key(), Filename: old.External.Filename, ExchangeID: old.ID, Direction: "out", State: state, Scope: scope},
			}
			seedLegacyMessage(t, m, rt, report, draft)
			got, err := m.ReportExternal(ctx, rt, b, old.ID, state, scope, body)
			if err != nil || !sameMessage(got, report) {
				t.Fatal("legacy outgoing assignment was replaced", got, err)
			}
			again, err := m.ReportExternal(ctx, rt, b, old.ID, state, scope, body)
			if err != nil || !sameMessage(again, report) {
				t.Fatal("legacy retry changed its identity", again, err)
			}
			data, err := os.ReadFile(mailPath(m, b.Node, "sent", report.ID))
			if err != nil || strings.Contains(string(data), "source_message_id") {
				t.Fatal("legacy metadata was upgraded in place", err)
			}
		})
	}
}

func TestExternalRandomReportIdentityRetainsTheExactReplySource(t *testing.T) {
	ctx := context.Background()
	m, rt, b := externalFixture(t)
	publishExternal(t, b, "request.md", "request")
	if _, err := m.SyncExternal(ctx, rt, b); err != nil {
		t.Fatal(err)
	}
	request := claimExternal(t, m, rt)
	scope := externalHash("same action")
	first, err := m.ReportExternal(ctx, rt, b, request.Message.ID, "pending", scope, "same question")
	if err != nil {
		t.Fatal(err)
	}
	publishExternal(t, b, decisionFilename(first.ID, scope, "approve"), "exact reason")
	if _, err := m.SyncExternal(ctx, rt, b); err != nil {
		t.Fatal(err)
	}
	answer := claimExternal(t, m, rt)
	second, err := m.ReportExternal(ctx, rt, b, answer.Message.ID, "pending", scope, "same question")
	if err != nil || second.ID == first.ID || second.External.SourceMessageID != answer.Message.ID {
		t.Fatal("identical report text aliased a different exact source", first, second, err)
	}
	if _, ok := messageid.Timestamp(second.ID); !ok {
		t.Fatal("report did not use a timestamp/random ID", second.ID)
	}
	again, err := m.ReportExternal(ctx, rt, b, answer.Message.ID, "pending", scope, "same question")
	if err != nil || again.ID != second.ID {
		t.Fatal("same-source retry created a new report", again, err)
	}
}

func TestExternalReadWithoutSentRefusesReplayWithRandomIDs(t *testing.T) {
	ctx := context.Background()
	m, rt, b := externalFixture(t)
	publishExternal(t, b, "request.md", "request")
	if _, err := m.SyncExternal(ctx, rt, b); err != nil {
		t.Fatal(err)
	}
	claim := claimExternal(t, m, rt)
	if err := os.Remove(mailPath(m, externalRole(b.key()), "sent", claim.Message.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SyncExternal(ctx, rt, b); err == nil {
		t.Fatal("missing accepted state was treated as a fresh input")
	}
	if next, err := m.Claim(ctx, rt, ""); err != nil || next != nil {
		t.Fatal("a claim was resurrected", next, err)
	}
	if _, err := os.Stat(filepath.Join(b.Root, "read", "request.md")); err != nil {
		t.Fatal("external history was removed", err)
	}
}

func TestExternalDecisionFilenameAdmitsOnlyTheExactExtendedShape(t *testing.T) {
	id, err := messageid.New(time.Date(2026, 11, 2, 0, 0, 0, 1, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	scope := strings.Repeat("a", 64)
	name := decisionFilename(id, scope, "approve")
	if !externalFilename(name) || len(name) >= 128 || !externalFilename("task-v2_3.md") {
		t.Fatal("valid decision/input rejected", name)
	}
	for _, bad := range []string{
		"task.with.dot.md", strings.Repeat("a", 129) + ".md", "../" + name,
		strings.Replace(name, "20261102", "20260230", 1),
		decisionFilename(id, "../"+scope, "approve"), decisionFilename(id, scope, "approved"),
	} {
		if externalFilename(bad) {
			t.Fatal("broadened arbitrary producer filename grammar", bad)
		}
	}
}
