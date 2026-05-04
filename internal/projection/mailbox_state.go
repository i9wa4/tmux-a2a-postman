package projection

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
)

type MailboxState struct {
	InboxCounts map[string]int
}

func ProjectMailboxState(sessionDir, sessionName string) (MailboxState, bool, error) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return MailboxState{}, false, nil
	}

	events, err := journal.Replay(sessionDir)
	if err != nil || len(events) == 0 {
		return MailboxState{}, false, nil
	}

	projected := MailboxState{
		InboxCounts: make(map[string]int),
	}
	unreadMessages := make(map[string]string)
	deliveredMessages := make(map[string]bool)
	readMessages := make(map[string]bool)
	sawDelivery := false
	sawLease := false
	sawResolution := false

	for _, event := range events {
		if event.SessionKey != state.SessionKey || event.Generation != state.Generation {
			continue
		}

		switch event.Type {
		case "lease_acquired":
			sawLease = true
		case "session_resolved":
			sawResolution = true
		case MailboxProjectionDeliveredEventType:
			payload, ok := projectedMailboxPayload(event.Payload)
			if !ok {
				return MailboxState{}, false, nil
			}
			recipient, ok := projectedRecipientName(payload, sessionName)
			if !ok {
				return MailboxState{}, false, nil
			}
			messageID, ok := projectedMessageIdentity(payload)
			if !ok {
				return MailboxState{}, false, nil
			}
			sawDelivery = true
			deliveredMessages[messageID] = true
			if readMessages[messageID] {
				continue
			}
			if _, alreadyUnread := unreadMessages[messageID]; alreadyUnread {
				continue
			}
			unreadMessages[messageID] = recipient
			projected.InboxCounts[recipient]++
		case MailboxProjectionReadEventType:
			payload, ok := projectedMailboxPayload(event.Payload)
			if !ok {
				return MailboxState{}, false, nil
			}
			messageID, ok := projectedMessageIdentity(payload)
			if !ok {
				if sawDelivery {
					continue
				}
				return MailboxState{}, false, nil
			}
			recipient, unread := unreadMessages[messageID]
			if !unread {
				if !sawDelivery {
					return MailboxState{}, false, nil
				}
				if deliveredMessages[messageID] {
					readMessages[messageID] = true
				}
				continue
			}
			projected.InboxCounts[recipient]--
			delete(unreadMessages, messageID)
			readMessages[messageID] = true
		}
	}

	if !sawLease || !sawResolution {
		return MailboxState{}, false, nil
	}

	return projected, true, nil
}

func loadCurrentSessionState(sessionDir string) (journal.SessionState, bool) {
	data, err := os.ReadFile(journal.SessionStatePath(sessionDir))
	if err != nil {
		return journal.SessionState{}, false
	}

	var state journal.SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return journal.SessionState{}, false
	}
	if state.SessionKey == "" || state.Generation < 1 {
		return journal.SessionState{}, false
	}
	return state, true
}

func projectedMailboxPayload(raw json.RawMessage) (journal.MailboxEventPayload, bool) {
	payload, ok := decodeMailboxEventPayload(raw)
	if !ok {
		return journal.MailboxEventPayload{}, false
	}
	return payload, true
}

func projectedRecipientName(payload journal.MailboxEventPayload, sessionName string) (string, bool) {
	if payload.To == "" {
		return "", false
	}

	fullRecipient := nodeaddr.Full(payload.To, sessionName)
	recipientSession, recipientName, hasSession := nodeaddr.Split(fullRecipient)
	if !hasSession || recipientSession != sessionName || recipientName == "" {
		return "", false
	}

	return recipientName, true
}

func projectedMessageIdentity(payload journal.MailboxEventPayload) (string, bool) {
	if payload.MessageID != "" {
		return payload.MessageID, true
	}
	if !isAllowedProjectionPath(payload.Path) {
		return "", false
	}
	base := filepath.Base(pathKey(payload.Path))
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "", false
	}
	return base, true
}
