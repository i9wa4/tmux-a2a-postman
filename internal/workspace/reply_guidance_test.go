package workspace

import (
	"strings"
	"testing"
)

func TestReplyGuidanceV2ChecksDelimiterWithoutChangingV1(t *testing.T) {
	id := "m-20261120T140000.000000000Z-Ab12Cd"
	msg := Message{ID: id, NeedsReply: true, GuidanceVersion: 1}
	old := ReplyGuidance(msg)
	if !strings.Contains(old, "<<'POSTMAN_BODY'\n<your reply>\nPOSTMAN_BODY\n") || strings.Contains(old, "check that no complete line") {
		t.Fatal("legacy reply guidance changed", old)
	}
	msg.GuidanceVersion = 2
	marker := ReplyHeredocDelimiter(id)
	current := ReplyGuidance(msg)
	if !strings.Contains(current, "postman reply guidance v2") ||
		!strings.Contains(current, "<<'"+marker+"'\n<your reply>\n"+marker+"\n") ||
		!strings.Contains(current, "check that no complete line of your actual body equals") ||
		strings.Contains(current, "<<'POSTMAN_BODY'\n") {
		t.Fatal("current reply guidance lacks collision check", current)
	}
	msg.External = &ExternalMessage{Direction: "in"}
	external := ReplyGuidance(msg)
	if !strings.Contains(external, "channel-report-heredoc") || !strings.Contains(external, "<<'"+marker+"'") ||
		!strings.Contains(external, "check that no complete line") {
		t.Fatal("external report guidance lacks collision check", external)
	}
	msg.External = nil
	msg.NeedsReply = false
	optional := OptionalReplyGuidance(msg)
	if !strings.Contains(optional, "<<'"+marker+"'") || !strings.Contains(optional, "check that no complete line") {
		t.Fatal("optional reply guidance lacks collision check", optional)
	}
}
