package projection

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
)

var errInvalidMailboxStateProjection = errors.New("invalid mailbox state projection")

type MailboxState struct {
	InboxCounts map[string]int
}

func ProjectMailboxState(sessionDir, sessionName string) (MailboxState, bool, error) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return MailboxState{}, false, nil
	}

	projected := MailboxState{
		InboxCounts: make(map[string]int),
	}
	unreadMessages := make(map[string]string)
	deliveredMessages := make(map[string]bool)
	readMessages := make(map[string]bool)
	sawDelivery := false
	sawEvent := false
	sawLease := false
	sawResolution := false

	err := journal.ReplayEach(sessionDir, func(event journal.Event) error {
		sawEvent = true
		if event.SessionKey != state.SessionKey || event.Generation != state.Generation {
			return nil
		}

		switch event.Type {
		case "lease_acquired":
			sawLease = true
		case "session_resolved":
			sawResolution = true
		case MailboxProjectionDeliveredEventType:
			payload, ok := projectedMailboxStatePayload(event.Payload)
			if !ok {
				return errInvalidMailboxStateProjection
			}
			recipient, ok := projectedRecipientName(payload, sessionName)
			if !ok {
				return errInvalidMailboxStateProjection
			}
			messageID, ok := projectedMessageIdentity(payload)
			if !ok {
				return errInvalidMailboxStateProjection
			}
			sawDelivery = true
			deliveredMessages[messageID] = true
			if readMessages[messageID] {
				return nil
			}
			if _, alreadyUnread := unreadMessages[messageID]; alreadyUnread {
				return nil
			}
			unreadMessages[messageID] = recipient
			projected.InboxCounts[recipient]++
		case MailboxProjectionReadEventType:
			payload, ok := projectedMailboxStatePayload(event.Payload)
			if !ok {
				return errInvalidMailboxStateProjection
			}
			messageID, ok := projectedMessageIdentity(payload)
			if !ok {
				if sawDelivery {
					return nil
				}
				return errInvalidMailboxStateProjection
			}
			recipient, unread := unreadMessages[messageID]
			if !unread {
				if !sawDelivery {
					return errInvalidMailboxStateProjection
				}
				if deliveredMessages[messageID] {
					readMessages[messageID] = true
				}
				return nil
			}
			projected.InboxCounts[recipient]--
			delete(unreadMessages, messageID)
			readMessages[messageID] = true
		}
		return nil
	})
	if err != nil || !sawEvent {
		return MailboxState{}, false, nil
	}

	if !sawLease || !sawResolution {
		return MailboxState{}, false, nil
	}

	return projected, true, nil
}

type mailboxStatePayload struct {
	MessageID string `json:"message_id,omitempty"`
	To        string `json:"to,omitempty"`
	Path      string `json:"path,omitempty"`
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

func CurrentSessionIdentity(sessionDir string) (sessionKey string, generation int, ok bool) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return "", 0, false
	}
	return state.SessionKey, state.Generation, true
}

func projectedMailboxStatePayload(raw json.RawMessage) (mailboxStatePayload, bool) {
	var payload mailboxStatePayload
	if len(raw) == 0 {
		return payload, true
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return mailboxStatePayload{}, false
	}
	return payload, true
}

func projectedRecipientName(payload mailboxStatePayload, sessionName string) (string, bool) {
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

func projectedMessageIdentity(payload mailboxStatePayload) (string, bool) {
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
