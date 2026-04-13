package projection

import (
	"encoding/json"
	"os"

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
		case "compatibility_mailbox_delivered":
			recipient, ok := projectedRecipientName(event.Payload, sessionName)
			if !ok {
				return MailboxState{}, false, nil
			}
			projected.InboxCounts[recipient]++
		case "compatibility_mailbox_read":
			recipient, ok := projectedRecipientName(event.Payload, sessionName)
			if !ok {
				return MailboxState{}, false, nil
			}
			if projected.InboxCounts[recipient] == 0 {
				return MailboxState{}, false, nil
			}
			projected.InboxCounts[recipient]--
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

func projectedRecipientName(payload json.RawMessage, sessionName string) (string, bool) {
	var envelope struct {
		To string `json:"to"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return "", false
	}
	if envelope.To == "" {
		return "", false
	}

	fullRecipient := nodeaddr.Full(envelope.To, sessionName)
	recipientSession, recipientName, hasSession := nodeaddr.Split(fullRecipient)
	if !hasSession || recipientSession != sessionName || recipientName == "" {
		return "", false
	}

	return recipientName, true
}
