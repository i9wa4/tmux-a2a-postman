package journal

import "time"

// MailboxEventPayload carries the mailbox file snapshot needed to rebuild
// mailbox projection files from replay.
type MailboxEventPayload struct {
	Directory           string `json:"directory,omitempty"`
	ContextID           string `json:"context_id,omitempty"`
	MessageID           string `json:"message_id,omitempty"`
	From                string `json:"from,omitempty"`
	To                  string `json:"to,omitempty"`
	ReplyPolicy         string `json:"reply_policy,omitempty"`
	ReplyTo             string `json:"reply_to,omitempty"`
	MessageType         string `json:"message_type,omitempty"`
	Timestamp           string `json:"timestamp,omitempty"`
	ThreadID            string `json:"thread_id,omitempty"`
	TaskID              string `json:"task_id,omitempty"`
	RunID               string `json:"run_id,omitempty"`
	InputRequestID      string `json:"input_request_id,omitempty"`
	FillsInputRequestID string `json:"fills_input_request_id,omitempty"`
	InputRequestSetID   string `json:"input_request_set_id,omitempty"`
	BranchID            string `json:"branch_id,omitempty"`
	CompletionRule      string `json:"completion_rule,omitempty"`
	Path                string `json:"path,omitempty"`
	SourcePath          string `json:"source_path,omitempty"`
	FailureReason       string `json:"failure_reason,omitempty"`
	Content             string `json:"content,omitempty"`
}

func RecordProcessMailboxPayload(sessionDir, tmuxSessionName, eventType string, visibility Visibility, payload MailboxEventPayload, now time.Time) error {
	manager := currentProcessManager()
	if manager == nil {
		return nil
	}
	return manager.RecordMailboxPayload(sessionDir, tmuxSessionName, eventType, visibility, payload, now)
}

func RecordProcessMailboxPayloadIfAbsent(sessionDir, tmuxSessionName, eventType string, visibility Visibility, payload MailboxEventPayload, equivalent EventEquivalenceFunc, now time.Time) (bool, error) {
	manager := currentProcessManager()
	if manager == nil {
		return false, nil
	}
	return manager.RecordMailboxPayloadIfAbsent(sessionDir, tmuxSessionName, eventType, visibility, payload, equivalent, now)
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

func (m *Manager) RecordMailboxPayloadIfAbsent(sessionDir, tmuxSessionName, eventType string, visibility Visibility, payload MailboxEventPayload, equivalent EventEquivalenceFunc, now time.Time) (bool, error) {
	writer, err := m.writerFor(sessionDir, tmuxSessionName, now)
	if err != nil {
		return false, err
	}
	if payload.Directory == "" {
		payload.Directory = directoryNameFromEventType(eventType)
	}
	_, appended, err := writer.AppendCurrentSessionEventIfAbsent(eventType, visibility, payload, AppendOptions{
		ThreadID: payload.ThreadID,
	}, now, equivalent)
	return appended, err
}
