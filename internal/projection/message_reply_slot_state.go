package projection

import (
	"sort"

	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
)

type MessageReplySlotState struct {
	UnreadCounts         map[string]int
	ActionRequiredCounts map[string]int
	WaitingOnReplyCounts map[string]int
	InfoUnreadCounts     map[string]int
	ActionRequired       []ReplySlotDetail
	WaitingOnReply       []ReplySlotDetail
}

type ReplySlotDetail struct {
	Direction      string
	MessageID      string
	ReplySlotID    string
	Sender         string
	Recipient      string
	ReplyPolicy    string
	OpenedAt       string
	OpenedAtSource string
	ReadAt         string
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
	openInboundExact := make(map[string]ReplySlotDetail)
	openInboundFallback := make(map[string]ReplySlotDetail)
	openOutboundExact := make(map[string]ReplySlotDetail)
	openOutboundFallback := make(map[string]ReplySlotDetail)
	infoUnread := make(map[string]ReplySlotDetail)
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
				openReplySlot(openOutboundExact, openOutboundFallback, meta, "outbound", event.OccurredAt, event.Type)
				projected.WaitingOnReplyCounts[meta.From]++
			}
		case MailboxProjectionDeliveredEventType:
			resolveOutboundReplySlot(projected, openOutboundExact, openOutboundFallback, meta)
			projected.UnreadCounts[meta.To]++
			if envelope.ResolveReplyPolicyFromMetadata(meta) == "required" {
				openReplySlot(openInboundExact, openInboundFallback, meta, "inbound", event.OccurredAt, event.Type)
				projected.ActionRequiredCounts[meta.To]++
			} else {
				infoUnread[replySlotKey(meta.MessageID, meta.To)] = ReplySlotDetail{MessageID: meta.MessageID, Sender: meta.From, Recipient: meta.To}
				projected.InfoUnreadCounts[meta.To]++
			}
		case MailboxProjectionReadEventType:
			decrementCount(projected.UnreadCounts, meta.To)
			markReplySlotRead(openInboundExact, openInboundFallback, meta, event.OccurredAt)
			markReplySlotRead(openOutboundExact, openOutboundFallback, meta, event.OccurredAt)
			if replySlot, ok := infoUnread[replySlotKey(meta.MessageID, meta.To)]; ok {
				decrementCount(projected.InfoUnreadCounts, replySlot.Recipient)
				delete(infoUnread, replySlotKey(meta.MessageID, meta.To))
			}
		}
	}

	if !sawLease || !sawResolution || !sawCompleteMailboxEvent {
		return MessageReplySlotState{}, false, nil
	}
	projected.ActionRequired = sortedReplySlotDetails(openInboundExact, openInboundFallback)
	projected.WaitingOnReply = sortedReplySlotDetails(openOutboundExact, openOutboundFallback)
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

func openReplySlot(openExact, openFallback map[string]ReplySlotDetail, meta envelope.Metadata, direction, openedAt, openedAtSource string) {
	replySlot := ReplySlotDetail{
		Direction:      direction,
		MessageID:      meta.MessageID,
		ReplySlotID:    meta.ReplySlotID,
		Sender:         meta.From,
		Recipient:      meta.To,
		ReplyPolicy:    envelope.ResolveReplyPolicyFromMetadata(meta),
		OpenedAt:       openedAt,
		OpenedAtSource: openedAtSource,
	}
	if meta.ReplySlotID != "" {
		openExact[meta.ReplySlotID] = replySlot
		return
	}
	openFallback[replySlotKey(meta.MessageID, meta.To)] = replySlot
}

func resolveInboundReplySlot(state MessageReplySlotState, openExact, openFallback map[string]ReplySlotDetail, meta envelope.Metadata) {
	if meta.FillsReplySlotID != "" {
		key, replySlot, ok := findExactReplySlot(openExact, meta.FillsReplySlotID, meta.ReplyTo, meta.From)
		if !ok {
			return
		}
		decrementCount(state.ActionRequiredCounts, replySlot.Recipient)
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
	decrementCount(state.ActionRequiredCounts, replySlot.Recipient)
	delete(openFallback, key)
}

func resolveOutboundReplySlot(state MessageReplySlotState, openExact, openFallback map[string]ReplySlotDetail, meta envelope.Metadata) {
	if meta.FillsReplySlotID != "" {
		key, replySlot, ok := findExactReplySlot(openExact, meta.FillsReplySlotID, meta.ReplyTo, meta.From)
		if !ok {
			return
		}
		decrementCount(state.WaitingOnReplyCounts, replySlot.Sender)
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
	decrementCount(state.WaitingOnReplyCounts, replySlot.Sender)
	delete(openFallback, key)
}

func markReplySlotRead(openExact, openFallback map[string]ReplySlotDetail, meta envelope.Metadata, readAt string) {
	if readAt == "" {
		return
	}
	if meta.ReplySlotID != "" {
		if replySlot, ok := openExact[meta.ReplySlotID]; ok {
			replySlot.ReadAt = readAt
			openExact[meta.ReplySlotID] = replySlot
			return
		}
	}
	for key, replySlot := range openExact {
		if replySlot.MessageID == meta.MessageID && replySlot.Recipient == meta.To {
			replySlot.ReadAt = readAt
			openExact[key] = replySlot
			return
		}
	}
	key := replySlotKey(meta.MessageID, meta.To)
	if replySlot, ok := openFallback[key]; ok {
		replySlot.ReadAt = readAt
		openFallback[key] = replySlot
	}
}

func findExactReplySlot(open map[string]ReplySlotDetail, replySlotID, replyTo, participant string) (string, ReplySlotDetail, bool) {
	replySlot, ok := open[replySlotID]
	if !ok {
		return "", ReplySlotDetail{}, false
	}
	if replySlot.Recipient != participant {
		return "", ReplySlotDetail{}, false
	}
	if replyTo != "" && replyTo != replySlot.MessageID {
		return "", ReplySlotDetail{}, false
	}
	return replySlotID, replySlot, true
}

func findFallbackReplySlot(open map[string]ReplySlotDetail, messageID, participant string) (string, ReplySlotDetail, bool) {
	key := replySlotKey(messageID, participant)
	replySlot, ok := open[key]
	return key, replySlot, ok
}

func sortedReplySlotDetails(exact, fallback map[string]ReplySlotDetail) []ReplySlotDetail {
	if len(exact) == 0 && len(fallback) == 0 {
		return nil
	}
	result := make([]ReplySlotDetail, 0, len(exact)+len(fallback))
	for _, replySlot := range exact {
		result = append(result, replySlot)
	}
	for _, replySlot := range fallback {
		result = append(result, replySlot)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].OpenedAt != result[j].OpenedAt {
			return result[i].OpenedAt < result[j].OpenedAt
		}
		if result[i].MessageID != result[j].MessageID {
			return result[i].MessageID < result[j].MessageID
		}
		if result[i].ReplySlotID != result[j].ReplySlotID {
			return result[i].ReplySlotID < result[j].ReplySlotID
		}
		if result[i].Sender != result[j].Sender {
			return result[i].Sender < result[j].Sender
		}
		return result[i].Recipient < result[j].Recipient
	})
	return result
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
