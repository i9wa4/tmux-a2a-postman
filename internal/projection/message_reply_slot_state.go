package projection

import (
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
)

type MessageReplySlotState struct {
	UnreadCounts         map[string]int
	ActionRequiredCounts map[string]int
	WaitingOnReplyCounts map[string]int
	InfoUnreadCounts     map[string]int
}

type projectedReplySlot struct {
	MessageID   string
	ReplySlotID string
	From        string
	To          string
}

func ProjectMessageReplySlotState(sessionDir, sessionName string) (MessageReplySlotState, bool, error) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return MessageReplySlotState{}, false, nil
	}

	events, err := journal.Replay(sessionDir)
	if err != nil || len(events) == 0 {
		return MessageReplySlotState{}, false, err
	}

	projected := MessageReplySlotState{
		UnreadCounts:         make(map[string]int),
		ActionRequiredCounts: make(map[string]int),
		WaitingOnReplyCounts: make(map[string]int),
		InfoUnreadCounts:     make(map[string]int),
	}
	openInboundExact := make(map[string]projectedReplySlot)
	openInboundFallback := make(map[string]projectedReplySlot)
	openOutboundExact := make(map[string]projectedReplySlot)
	openOutboundFallback := make(map[string]projectedReplySlot)
	infoUnread := make(map[string]projectedReplySlot)
	sawLease := false
	sawResolution := false
	sawCompleteMailboxEvent := false

	for _, event := range events {
		if event.SessionKey != state.SessionKey || event.Generation != state.Generation {
			continue
		}

		switch event.Type {
		case "lease_acquired":
			sawLease = true
			continue
		case "session_resolved":
			sawResolution = true
			continue
		case MailboxProjectionPostConsumedEventType, MailboxProjectionDeliveredEventType, MailboxProjectionReadEventType:
		default:
			continue
		}

		payload, ok := decodeMailboxEventPayload(event.Payload)
		if !ok {
			continue
		}
		meta := replySlotMetadataFromPayload(payload)
		if meta.MessageID == "" {
			continue
		}
		if (event.Type == MailboxProjectionPostConsumedEventType || event.Type == MailboxProjectionDeliveredEventType) && payload.Content == "" {
			continue
		}
		sawCompleteMailboxEvent = true
		meta.From = simpleNameForSession(meta.From, sessionName)
		meta.To = simpleNameForSession(meta.To, sessionName)

		switch event.Type {
		case MailboxProjectionPostConsumedEventType:
			resolveInboundReplySlot(projected, openInboundExact, openInboundFallback, meta)
			if envelope.ResolveReplyPolicyFromMetadata(meta) == "required" {
				openReplySlot(openOutboundExact, openOutboundFallback, meta)
				projected.WaitingOnReplyCounts[meta.From]++
			}
		case MailboxProjectionDeliveredEventType:
			resolveOutboundReplySlot(projected, openOutboundExact, openOutboundFallback, meta)
			projected.UnreadCounts[meta.To]++
			if envelope.ResolveReplyPolicyFromMetadata(meta) == "required" {
				openReplySlot(openInboundExact, openInboundFallback, meta)
				projected.ActionRequiredCounts[meta.To]++
			} else {
				infoUnread[replySlotKey(meta.MessageID, meta.To)] = projectedReplySlot{MessageID: meta.MessageID, From: meta.From, To: meta.To}
				projected.InfoUnreadCounts[meta.To]++
			}
		case MailboxProjectionReadEventType:
			decrementCount(projected.UnreadCounts, meta.To)
			if replySlot, ok := infoUnread[replySlotKey(meta.MessageID, meta.To)]; ok {
				decrementCount(projected.InfoUnreadCounts, replySlot.To)
				delete(infoUnread, replySlotKey(meta.MessageID, meta.To))
			}
		}
	}

	if !sawLease || !sawResolution || !sawCompleteMailboxEvent {
		return MessageReplySlotState{}, false, nil
	}
	return projected, true, nil
}

func replySlotMetadataFromPayload(payload journal.MailboxEventPayload) envelope.Metadata {
	meta, err := envelope.ParseMetadata(payload.Content)
	if err != nil {
		meta = envelope.Metadata{Body: envelope.BodyFromContent(payload.Content)}
	}
	if meta.MessageID == "" {
		meta.MessageID = payload.MessageID
	}
	if meta.From == "" {
		meta.From = payload.From
	}
	if meta.To == "" {
		meta.To = payload.To
	}
	if meta.ReplySlotID == "" {
		meta.ReplySlotID = payload.ReplySlotID
	}
	if meta.FillsReplySlotID == "" {
		meta.FillsReplySlotID = payload.FillsReplySlotID
	}
	if meta.ReplySetID == "" {
		meta.ReplySetID = payload.ReplySetID
	}
	if meta.BranchID == "" {
		meta.BranchID = payload.BranchID
	}
	if meta.CompletionRule == "" {
		meta.CompletionRule = payload.CompletionRule
	}
	return meta
}

func openReplySlot(openExact, openFallback map[string]projectedReplySlot, meta envelope.Metadata) {
	replySlot := projectedReplySlot{
		MessageID:   meta.MessageID,
		ReplySlotID: meta.ReplySlotID,
		From:        meta.From,
		To:          meta.To,
	}
	if meta.ReplySlotID != "" {
		openExact[meta.ReplySlotID] = replySlot
		return
	}
	openFallback[replySlotKey(meta.MessageID, meta.To)] = replySlot
}

func resolveInboundReplySlot(state MessageReplySlotState, openExact, openFallback map[string]projectedReplySlot, meta envelope.Metadata) {
	if meta.FillsReplySlotID != "" {
		key, replySlot, ok := findExactReplySlot(openExact, meta.FillsReplySlotID, meta.ReplyTo, meta.From)
		if !ok {
			return
		}
		decrementCount(state.ActionRequiredCounts, replySlot.To)
		delete(openExact, key)
		return
	}
	if meta.ReplyTo == "" {
		return
	}
	key, replySlot, ok := findFallbackReplySlot(openFallback, meta.ReplyTo, meta.From)
	if !ok {
		return
	}
	decrementCount(state.ActionRequiredCounts, replySlot.To)
	delete(openFallback, key)
}

func resolveOutboundReplySlot(state MessageReplySlotState, openExact, openFallback map[string]projectedReplySlot, meta envelope.Metadata) {
	if meta.FillsReplySlotID != "" {
		key, replySlot, ok := findExactReplySlot(openExact, meta.FillsReplySlotID, meta.ReplyTo, meta.From)
		if !ok {
			return
		}
		decrementCount(state.WaitingOnReplyCounts, replySlot.From)
		delete(openExact, key)
		return
	}
	if meta.ReplyTo == "" {
		return
	}
	key, replySlot, ok := findFallbackReplySlot(openFallback, meta.ReplyTo, meta.From)
	if !ok {
		return
	}
	decrementCount(state.WaitingOnReplyCounts, replySlot.From)
	delete(openFallback, key)
}

func findExactReplySlot(open map[string]projectedReplySlot, replySlotID, replyTo, participant string) (string, projectedReplySlot, bool) {
	replySlot, ok := open[replySlotID]
	if !ok {
		return "", projectedReplySlot{}, false
	}
	if replySlot.To != participant {
		return "", projectedReplySlot{}, false
	}
	if replyTo != "" && replyTo != replySlot.MessageID {
		return "", projectedReplySlot{}, false
	}
	return replySlotID, replySlot, true
}

func findFallbackReplySlot(open map[string]projectedReplySlot, messageID, participant string) (string, projectedReplySlot, bool) {
	key := replySlotKey(messageID, participant)
	replySlot, ok := open[key]
	return key, replySlot, ok
}

func simpleNameForSession(name, sessionName string) string {
	fullName := nodeaddr.Full(name, sessionName)
	recipientSession, recipientName, hasSession := nodeaddr.Split(fullName)
	if hasSession && recipientSession == sessionName {
		return recipientName
	}
	return name
}

func decrementCount(counts map[string]int, key string) {
	if counts[key] <= 0 {
		return
	}
	counts[key]--
}

func replySlotKey(messageID, nodeName string) string {
	return messageID + "\x00" + nodeName
}
