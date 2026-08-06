package popnotice

import (
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

const (
	staleBacklogState   = "newer_unread_from_same_sender"
	staleBacklogReason  = "newer unread messages from the same sender and recipient remain queued; check current authority before acting on the popped message"
	maxNoticeMessageIDs = 5
)

func BuildStaleBacklogNotice(popped message.MessageInfo, remaining []message.MessageInfo) *projection.PopStaleBacklogNotice {
	if popped.Filename == "" || popped.From == "" || popped.To == "" {
		return nil
	}

	newerIDs := make([]string, 0, maxNoticeMessageIDs)
	newerCount := 0
	newest := ""
	for _, candidate := range remaining {
		if candidate.Filename <= popped.Filename {
			continue
		}
		if candidate.From != popped.From || candidate.To != popped.To {
			continue
		}
		newerCount++
		newest = candidate.Filename
		if len(newerIDs) < maxNoticeMessageIDs {
			newerIDs = append(newerIDs, candidate.Filename)
		}
	}
	if newerCount == 0 {
		return nil
	}

	return &projection.PopStaleBacklogNotice{
		State:            staleBacklogState,
		Reason:           staleBacklogReason,
		PoppedMessageID:  popped.Filename,
		Sender:           popped.From,
		Recipient:        popped.To,
		NewerUnreadCount: newerCount,
		NewestMessageID:  newest,
		NewerMessageIDs:  newerIDs,
	}
}
