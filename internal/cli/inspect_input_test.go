package cli

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

func TestRunInspectInputFindsOpenInboundAndOutboundByID(t *testing.T) {
	fixture := writeInspectInputFixture(t)
	appendInspectInputRequest(t, fixture, "m1.md", "ireq_123")

	for _, tc := range []struct {
		name      string
		id        string
		matchedBy string
	}{
		{name: "input request id", id: "ireq_123", matchedBy: "input_request_id"},
		{name: "message id", id: "m1.md", matchedBy: "message_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runInspectInputForFixture(t, fixture, tc.id)

			if got.Status != "found" || got.MatchCount != 2 {
				t.Fatalf("inspect output = %#v, want found with inbound and outbound matches", got)
			}
			matchesByNode := map[string]inspectInputMatch{}
			for _, match := range got.Matches {
				matchesByNode[match.Node] = match
				if match.MatchedBy != tc.matchedBy {
					t.Fatalf("match %#v matched_by = %q, want %q", match, match.MatchedBy, tc.matchedBy)
				}
				if match.InputRequest.MessageID != "m1.md" || match.InputRequest.InputRequestID != "ireq_123" || match.InputRequest.Sender != "critic" || match.InputRequest.Recipient != "worker" || match.InputRequest.ReplyPolicy != "required" {
					t.Fatalf("input request detail = %#v, want public identifiers", match.InputRequest)
				}
				if match.InputRequest.OpenedEventID == "" {
					t.Fatalf("input request detail = %#v, want opened_event_id", match.InputRequest)
				}
			}
			if matchesByNode["worker"].InputRequest.Direction != "inbound" {
				t.Fatalf("worker match = %#v, want inbound action", matchesByNode["worker"])
			}
			if matchesByNode["critic"].InputRequest.Direction != "outbound" {
				t.Fatalf("critic match = %#v, want outbound wait", matchesByNode["critic"])
			}
		})
	}
}

func TestRunInspectInputReturnsNotFoundForClosedWrongAndNoReplyIDs(t *testing.T) {
	t.Run("already satisfied", func(t *testing.T) {
		fixture := writeInspectInputFixture(t)
		appendInspectInputRequest(t, fixture, "m1.md", "ireq_123")
		appendInspectInputResolution(t, fixture, "m2.md", "m1.md", "ireq_123")

		got := runInspectInputForFixture(t, fixture, "ireq_123")
		if got.Status != "not_found" || got.MatchCount != 0 {
			t.Fatalf("inspect output = %#v, want not_found for satisfied input request", got)
		}
	})

	t.Run("wrong id", func(t *testing.T) {
		fixture := writeInspectInputFixture(t)
		appendInspectInputRequest(t, fixture, "m1.md", "ireq_123")

		got := runInspectInputForFixture(t, fixture, "missing")
		if got.Status != "not_found" || got.MatchCount != 0 {
			t.Fatalf("inspect output = %#v, want not_found for wrong id", got)
		}
	})

	t.Run("no reply traffic", func(t *testing.T) {
		fixture := writeInspectInputFixture(t)
		appendInspectInputInfo(t, fixture, "info.md")

		got := runInspectInputForFixture(t, fixture, "info.md")
		if got.Status != "not_found" || got.MatchCount != 0 {
			t.Fatalf("inspect output = %#v, want not_found for no-reply mail", got)
		}
	})
}

func writeInspectInputFixture(t *testing.T) sessionHealthProjectionFixture {
	t.Helper()
	return writeSessionHealthProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		map[string]int{"worker": 1},
		nil,
	)
}

func appendInspectInputRequest(t *testing.T, fixture sessionHealthProjectionFixture, messageID, inputRequestID string) {
	t.Helper()
	sessionDir := filepath.Join(fixture.baseDir, fixture.contextID, fixture.sessionName)
	now := time.Date(2026, time.April, 14, 6, 0, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, fixture.contextID, fixture.sessionName, 610, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	content := sessionHealthMessageContent("critic", "worker", messageID, map[string]string{
		"replyPolicy":      "required",
		"input_request_id": inputRequestID,
	}, "please review")
	appendSessionHealthObligationEvent(t, writer, projection.MailboxProjectionPostConsumedEventType, messageID, "critic", "worker", content, now.Add(time.Second))
	appendSessionHealthObligationEvent(t, writer, projection.MailboxProjectionDeliveredEventType, messageID, "critic", "worker", content, now.Add(2*time.Second))
}

func appendInspectInputResolution(t *testing.T, fixture sessionHealthProjectionFixture, messageID, replyTo, fillsInputRequestID string) {
	t.Helper()
	sessionDir := filepath.Join(fixture.baseDir, fixture.contextID, fixture.sessionName)
	now := time.Date(2026, time.April, 14, 6, 5, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, fixture.contextID, fixture.sessionName, 611, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	content := sessionHealthMessageContent("worker", "critic", messageID, map[string]string{
		"replyPolicy":            "none",
		"replyTo":                replyTo,
		"fills_input_request_id": fillsInputRequestID,
	}, "DONE")
	appendSessionHealthObligationEvent(t, writer, projection.MailboxProjectionPostConsumedEventType, messageID, "worker", "critic", content, now.Add(time.Second))
	appendSessionHealthObligationEvent(t, writer, projection.MailboxProjectionDeliveredEventType, messageID, "worker", "critic", content, now.Add(2*time.Second))
}

func appendInspectInputInfo(t *testing.T, fixture sessionHealthProjectionFixture, messageID string) {
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

func runInspectInputForFixture(t *testing.T, fixture sessionHealthProjectionFixture, id string) inspectInputOutput {
	t.Helper()
	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunInspectInput([]string{
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
			"--config", fixture.configPath,
			"--id", id,
		})
	})
	if err != nil {
		t.Fatalf("RunInspectInput() error = %v stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	var got inspectInputOutput
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	return got
}
