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
	MessageID string
	From      string
	To        string
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
	openInbound := make(map[string]projectedObligation)
	openOutbound := make(map[string]projectedObligation)
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
			resolveInboundObligation(projected, openInbound, meta.ReplyTo, meta.From)
			if envelope.ResolveReplyPolicyFromMetadata(meta) == "required" {
				openOutbound[obligationKey(meta.MessageID, meta.To)] = projectedObligation{MessageID: meta.MessageID, From: meta.From, To: meta.To}
				projected.WaitingOnReplyCounts[meta.From]++
			}
		case MailboxProjectionDeliveredEventType:
			resolveOutboundObligation(projected, openOutbound, meta.ReplyTo, meta.From)
			projected.UnreadCounts[meta.To]++
			if envelope.ResolveReplyPolicyFromMetadata(meta) == "required" {
				openInbound[obligationKey(meta.MessageID, meta.To)] = projectedObligation{MessageID: meta.MessageID, From: meta.From, To: meta.To}
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
	return meta
}

func resolveInboundObligation(state MessageObligationState, openInbound map[string]projectedObligation, replyTo, from string) {
	if replyTo == "" {
		return
	}
	key, obligation, ok := findObligation(openInbound, replyTo, from)
	if !ok {
		return
	}
	decrementCount(state.ActionRequiredCounts, obligation.To)
	delete(openInbound, key)
}

func resolveOutboundObligation(state MessageObligationState, openOutbound map[string]projectedObligation, replyTo, from string) {
	if replyTo == "" {
		return
	}
	key, obligation, ok := findObligation(openOutbound, replyTo, from)
	if !ok {
		return
	}
	decrementCount(state.WaitingOnReplyCounts, obligation.From)
	delete(openOutbound, key)
}

func findObligation(open map[string]projectedObligation, messageID, participant string) (string, projectedObligation, bool) {
	if obligation, ok := open[obligationKey(messageID, participant)]; ok {
		return obligationKey(messageID, participant), obligation, true
	}

	var foundKey string
	var foundObligation projectedObligation
	found := false
	for key, obligation := range open {
		if obligation.MessageID != messageID || !sameParticipant(obligation.To, participant) {
			continue
		}
		if found {
			return "", projectedObligation{}, false
		}
		foundKey = key
		foundObligation = obligation
		found = true
	}
	return foundKey, foundObligation, found
}

func sameParticipant(left, right string) bool {
	return left == right || nodeaddr.Simple(left) == nodeaddr.Simple(right)
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
