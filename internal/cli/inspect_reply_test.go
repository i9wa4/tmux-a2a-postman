package cli

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

func TestRunInspectReplyFindsOpenInboundAndOutboundByID(t *testing.T) {
	fixture := writeInspectReplyFixture(t)
	appendInspectReplyRequest(t, fixture, "m1.md", "rslot_123")

	for _, tc := range []struct {
		name      string
		id        string
		matchedBy string
	}{
		{name: "reply slot id", id: "rslot_123", matchedBy: "reply_slot_id"},
		{name: "message id", id: "m1.md", matchedBy: "message_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runInspectReplyForFixture(t, fixture, tc.id)

			if got.Status != "found" || got.MatchCount != 2 {
				t.Fatalf("inspect output = %#v, want found with inbound and outbound matches", got)
			}
			matchesByNode := map[string]inspectReplyMatch{}
			for _, match := range got.Matches {
				matchesByNode[match.Node] = match
				if match.MatchedBy != tc.matchedBy {
					t.Fatalf("match %#v matched_by = %q, want %q", match, match.MatchedBy, tc.matchedBy)
				}
				if match.ReplySlot.MessageID != "m1.md" || match.ReplySlot.ReplySlotID != "rslot_123" || match.ReplySlot.Sender != "critic" || match.ReplySlot.Recipient != "worker" || match.ReplySlot.ReplyPolicy != "required" {
					t.Fatalf("reply slot detail = %#v, want public identifiers", match.ReplySlot)
				}
			}
			if matchesByNode["worker"].ReplySlot.Direction != "inbound" {
				t.Fatalf("worker match = %#v, want inbound action", matchesByNode["worker"])
			}
			if matchesByNode["critic"].ReplySlot.Direction != "outbound" {
				t.Fatalf("critic match = %#v, want outbound wait", matchesByNode["critic"])
			}
		})
	}
}

func TestRunInspectReplyReturnsNotFoundForClosedWrongAndNoReplyIDs(t *testing.T) {
	t.Run("already satisfied", func(t *testing.T) {
		fixture := writeInspectReplyFixture(t)
		appendInspectReplyRequest(t, fixture, "m1.md", "rslot_123")
		appendInspectReplyResolution(t, fixture, "m2.md", "m1.md", "rslot_123")

		got := runInspectReplyForFixture(t, fixture, "rslot_123")
		if got.Status != "not_found" || got.MatchCount != 0 {
			t.Fatalf("inspect output = %#v, want not_found for satisfied reply slot", got)
		}
	})

	t.Run("wrong id", func(t *testing.T) {
		fixture := writeInspectReplyFixture(t)
		appendInspectReplyRequest(t, fixture, "m1.md", "rslot_123")

		got := runInspectReplyForFixture(t, fixture, "missing")
		if got.Status != "not_found" || got.MatchCount != 0 {
			t.Fatalf("inspect output = %#v, want not_found for wrong id", got)
		}
	})

	t.Run("no reply traffic", func(t *testing.T) {
		fixture := writeInspectReplyFixture(t)
		appendInspectReplyInfo(t, fixture, "info.md")

		got := runInspectReplyForFixture(t, fixture, "info.md")
		if got.Status != "not_found" || got.MatchCount != 0 {
			t.Fatalf("inspect output = %#v, want not_found for no-reply mail", got)
		}
	})
}

func writeInspectReplyFixture(t *testing.T) sessionHealthProjectionFixture {
	t.Helper()
	return writeSessionHealthProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		map[string]int{"worker": 1},
		nil,
	)
}

func appendInspectReplyRequest(t *testing.T, fixture sessionHealthProjectionFixture, messageID, replySlotID string) {
	t.Helper()
	sessionDir := filepath.Join(fixture.baseDir, fixture.contextID, fixture.sessionName)
	now := time.Date(2026, time.April, 14, 6, 0, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, fixture.contextID, fixture.sessionName, 610, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	content := sessionHealthMessageContent("critic", "worker", messageID, map[string]string{
		"replyPolicy":   "required",
		"reply_slot_id": replySlotID,
	}, "please review")
	appendSessionHealthObligationEvent(t, writer, projection.MailboxProjectionPostConsumedEventType, messageID, "critic", "worker", content, now.Add(time.Second))
	appendSessionHealthObligationEvent(t, writer, projection.MailboxProjectionDeliveredEventType, messageID, "critic", "worker", content, now.Add(2*time.Second))
}

func appendInspectReplyResolution(t *testing.T, fixture sessionHealthProjectionFixture, messageID, replyTo, fillsReplySlotID string) {
	t.Helper()
	sessionDir := filepath.Join(fixture.baseDir, fixture.contextID, fixture.sessionName)
	now := time.Date(2026, time.April, 14, 6, 5, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, fixture.contextID, fixture.sessionName, 611, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	content := sessionHealthMessageContent("worker", "critic", messageID, map[string]string{
		"replyPolicy":         "none",
		"replyTo":             replyTo,
		"fills_reply_slot_id": fillsReplySlotID,
	}, "DONE")
	appendSessionHealthObligationEvent(t, writer, projection.MailboxProjectionPostConsumedEventType, messageID, "worker", "critic", content, now.Add(time.Second))
	appendSessionHealthObligationEvent(t, writer, projection.MailboxProjectionDeliveredEventType, messageID, "worker", "critic", content, now.Add(2*time.Second))
}

func appendInspectReplyInfo(t *testing.T, fixture sessionHealthProjectionFixture, messageID string) {
	t.Helper()
	sessionDir := filepath.Join(fixture.baseDir, fixture.contextID, fixture.sessionName)
	now := time.Date(2026, time.April, 14, 6, 10, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, fixture.contextID, fixture.sessionName, 612, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	content := sessionHealthMessageContent("critic", "worker", messageID, map[string]string{
		"replyPolicy": "none",
	}, "FYI")
	appendSessionHealthObligationEvent(t, writer, projection.MailboxProjectionDeliveredEventType, messageID, "critic", "worker", content, now.Add(time.Second))
}

func runInspectReplyForFixture(t *testing.T, fixture sessionHealthProjectionFixture, id string) inspectReplyOutput {
	t.Helper()
	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunInspectReply([]string{
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
			"--config", fixture.configPath,
			"--id", id,
		})
	})
	if err != nil {
		t.Fatalf("RunInspectReply() error = %v stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	var got inspectReplyOutput
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	return got
}
