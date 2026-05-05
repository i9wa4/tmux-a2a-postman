package journal

import "time"

// MailboxEventPayload carries the mailbox file snapshot needed to rebuild
// mailbox projection files from replay.
type MailboxEventPayload struct {
	Directory             string `json:"directory,omitempty"`
	MessageID             string `json:"message_id,omitempty"`
	From                  string `json:"from,omitempty"`
	To                    string `json:"to,omitempty"`
	ThreadID              string `json:"thread_id,omitempty"`
	ObligationID          string `json:"obligation_id,omitempty"`
	SatisfiesObligationID string `json:"satisfies_obligation_id,omitempty"`
	ObligationGroupID     string `json:"obligation_group_id,omitempty"`
	BranchID              string `json:"branch_id,omitempty"`
	CompletionRule        string `json:"completion_rule,omitempty"`
	Path                  string `json:"path,omitempty"`
	SourcePath            string `json:"source_path,omitempty"`
	Content               string `json:"content,omitempty"`
}

func RecordProcessMailboxPayload(sessionDir, tmuxSessionName, eventType string, visibility Visibility, payload MailboxEventPayload, now time.Time) error {
	processManager.RLock()
	manager := processManager.manager
	processManager.RUnlock()
	if manager == nil {
		return nil
	}
	return manager.RecordMailboxPayload(sessionDir, tmuxSessionName, eventType, visibility, payload, now)
}

func (m *Manager) RecordMailboxPayload(sessionDir, tmuxSessionName, eventType string, visibility Visibility, payload MailboxEventPayload, now time.Time) error {
	writer, err := m.writerFor(sessionDir, tmuxSessionName, now)
	if err != nil {
		return err
	}
	if payload.Directory == "" {
		payload.Directory = directoryNameFromEventType(eventType)
	}
	_, err = writer.AppendEventWithOptions(eventType, visibility, payload, AppendOptions{
		ThreadID: payload.ThreadID,
	}, now)
	return err
}
