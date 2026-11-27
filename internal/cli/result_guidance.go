package cli

import (
	"fmt"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

// resultGuidance describes only actions supported by the operation result and
// the caller's own bounded summary. A nil count is unknown, never zero.
func resultGuidance(self workspace.NodeStatus, operation string, success, claimed bool, existing string, externalPending, externalMore bool) string {
	parts := []string{}
	if existing != "" {
		parts = append(parts, existing)
	}
	// A failed or empty POP is not a reason to immediately POP again, even
	// when a concurrent snapshot still sees unread mail.
	if operation == "pop" && (!success || !claimed) {
		return strings.Join(parts, " ")
	}
	if self.UnreadCount != nil && *self.UnreadCount > 0 {
		if operation == "pop" && claimed {
			parts = append(parts, fmt.Sprintf("Remaining unread messages after this POP: %d (self.unread_count). After fully reading and handling this archive, run `tmux-a2a-postman pop` again without waiting for another notification. Continue only while the latest operation reports a positive self.unread_count; stop on zero, unknown, or an empty POP. Do not poll an empty inbox.", *self.UnreadCount))
		} else {
			parts = append(parts, "Own unread mail remains (self.unread_count). Run `tmux-a2a-postman pop` to claim the next message, read its complete archive through EOF, and follow that archive's response contract. Continue only while the latest result reports a positive self.unread_count; stop on zero, unknown, or an empty POP. Do not poll an empty inbox.")
		}
	}
	if self.InboundCount != nil && *self.InboundCount > 0 {
		parts = append(parts, "Own required responses remain (self.inbound_count), including unread requests. Follow each claimed archive's response contract: a substantive exact reply or concrete blocker for ordinary requests; approve-execution or decline-execution for typed requests; the report or decision procedure for external requests. Keep long-running work pending until evidence is ready; do not send an ACK or invent completion to clear the count.")
	}
	if externalPending {
		parts = append(parts, "External decisions are pending in channel.pending. Follow the exact channel decision procedure and filenames; a pending report is not an ordinary reply or approval.")
	}
	if operation == "channel-sync" && success && externalMore {
		parts = append(parts, "Channel has more work (channel.more). After handling accepted mail, run `tmux-a2a-postman channel-sync` once for the next bounded scan, then reassess its result. Do not poll an empty POP.")
	}
	if self.OutboundCount != nil && *self.OutboundCount > 0 &&
		self.UnreadCount != nil && *self.UnreadCount == 0 &&
		self.InboundCount != nil && *self.InboundCount == 0 && !externalPending && !externalMore {
		parts = append(parts, "Sent requests await responses (self.outbound_count). Wait for a genuine reply while continuing any other work; do not resend, request status, or poll merely to clear this count.")
	}
	return strings.Join(parts, " ")
}
