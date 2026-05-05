package projection

import (
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
)

type MessageObligationState struct {
	UnreadCounts         map[string]int
	ActionRequiredCounts map[string]int
	WaitingOnReplyCounts map[string]int
	InfoUnreadCounts     map[string]int
}

type projectedObligation struct {
	MessageID    string
	ObligationID string
	From         string
	To           string
}

func ProjectMessageObligationState(sessionDir, sessionName string) (MessageObligationState, bool, error) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return MessageObligationState{}, false, nil
	}

	events, err := journal.Replay(sessionDir)
	if err != nil || len(events) == 0 {
		return MessageObligationState{}, false, err
	}

	projected := MessageObligationState{
		UnreadCounts:         make(map[string]int),
		ActionRequiredCounts: make(map[string]int),
		WaitingOnReplyCounts: make(map[string]int),
		InfoUnreadCounts:     make(map[string]int),
	}
	openInboundExact := make(map[string]projectedObligation)
	openInboundLegacy := make(map[string]projectedObligation)
	openOutboundExact := make(map[string]projectedObligation)
	openOutboundLegacy := make(map[string]projectedObligation)
	infoUnread := make(map[string]projectedObligation)
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
		meta := obligationMetadataFromPayload(payload)
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
			resolveInboundObligation(projected, openInboundExact, openInboundLegacy, meta)
			if envelope.ResolveReplyPolicyFromMetadata(meta) == "required" {
				openObligation(openOutboundExact, openOutboundLegacy, meta)
				projected.WaitingOnReplyCounts[meta.From]++
			}
		case MailboxProjectionDeliveredEventType:
			resolveOutboundObligation(projected, openOutboundExact, openOutboundLegacy, meta)
			projected.UnreadCounts[meta.To]++
			if envelope.ResolveReplyPolicyFromMetadata(meta) == "required" {
				openObligation(openInboundExact, openInboundLegacy, meta)
				projected.ActionRequiredCounts[meta.To]++
			} else {
				infoUnread[obligationKey(meta.MessageID, meta.To)] = projectedObligation{MessageID: meta.MessageID, From: meta.From, To: meta.To}
				projected.InfoUnreadCounts[meta.To]++
			}
		case MailboxProjectionReadEventType:
			decrementCount(projected.UnreadCounts, meta.To)
			if obligation, ok := infoUnread[obligationKey(meta.MessageID, meta.To)]; ok {
				decrementCount(projected.InfoUnreadCounts, obligation.To)
				delete(infoUnread, obligationKey(meta.MessageID, meta.To))
			}
		}
	}

	if !sawLease || !sawResolution || !sawCompleteMailboxEvent {
		return MessageObligationState{}, false, nil
	}
	return projected, true, nil
}

func obligationMetadataFromPayload(payload journal.MailboxEventPayload) envelope.Metadata {
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
	if meta.ObligationID == "" {
		meta.ObligationID = payload.ObligationID
	}
	if meta.SatisfiesObligationID == "" {
		meta.SatisfiesObligationID = payload.SatisfiesObligationID
	}
	if meta.ObligationGroupID == "" {
		meta.ObligationGroupID = payload.ObligationGroupID
	}
	if meta.BranchID == "" {
		meta.BranchID = payload.BranchID
	}
	if meta.CompletionRule == "" {
		meta.CompletionRule = payload.CompletionRule
	}
	return meta
}

func openObligation(openExact, openLegacy map[string]projectedObligation, meta envelope.Metadata) {
	obligation := projectedObligation{
		MessageID:    meta.MessageID,
		ObligationID: meta.ObligationID,
		From:         meta.From,
		To:           meta.To,
	}
	if meta.ObligationID != "" {
		openExact[meta.ObligationID] = obligation
		return
	}
	openLegacy[obligationKey(meta.MessageID, meta.To)] = obligation
}

func resolveInboundObligation(state MessageObligationState, openExact, openLegacy map[string]projectedObligation, meta envelope.Metadata) {
	if meta.SatisfiesObligationID != "" {
		key, obligation, ok := findExactObligation(openExact, meta.SatisfiesObligationID, meta.ReplyTo, meta.From)
		if !ok {
			return
		}
		decrementCount(state.ActionRequiredCounts, obligation.To)
		delete(openExact, key)
		return
	}
	if meta.ReplyTo == "" {
		return
	}
	key, obligation, ok := findLegacyObligation(openLegacy, meta.ReplyTo, meta.From)
	if !ok {
		return
	}
	decrementCount(state.ActionRequiredCounts, obligation.To)
	delete(openLegacy, key)
}

func resolveOutboundObligation(state MessageObligationState, openExact, openLegacy map[string]projectedObligation, meta envelope.Metadata) {
	if meta.SatisfiesObligationID != "" {
		key, obligation, ok := findExactObligation(openExact, meta.SatisfiesObligationID, meta.ReplyTo, meta.From)
		if !ok {
			return
		}
		decrementCount(state.WaitingOnReplyCounts, obligation.From)
		delete(openExact, key)
		return
	}
	if meta.ReplyTo == "" {
		return
	}
	key, obligation, ok := findLegacyObligation(openLegacy, meta.ReplyTo, meta.From)
	if !ok {
		return
	}
	decrementCount(state.WaitingOnReplyCounts, obligation.From)
	delete(openLegacy, key)
}

func findExactObligation(open map[string]projectedObligation, obligationID, replyTo, participant string) (string, projectedObligation, bool) {
	obligation, ok := open[obligationID]
	if !ok {
		return "", projectedObligation{}, false
	}
	if obligation.To != participant {
		return "", projectedObligation{}, false
	}
	if replyTo != "" && replyTo != obligation.MessageID {
		return "", projectedObligation{}, false
	}
	return obligationID, obligation, true
}

func findLegacyObligation(open map[string]projectedObligation, messageID, participant string) (string, projectedObligation, bool) {
	key := obligationKey(messageID, participant)
	obligation, ok := open[key]
	return key, obligation, ok
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

func obligationKey(messageID, nodeName string) string {
	return messageID + "\x00" + nodeName
}
