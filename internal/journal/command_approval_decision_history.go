package journal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const commandApprovalDecisionHistorySchemaVersion = 1

type CommandApprovalDecisionHistoryEntry struct {
	SchemaVersion       int              `json:"schema_version"`
	SessionKey          string           `json:"session_key"`
	TmuxSessionName     string           `json:"tmux_session_name"`
	Generation          int              `json:"generation"`
	EventSequence       int              `json:"event_sequence"`
	EventID             string           `json:"event_id"`
	ThreadID            string           `json:"thread_id"`
	Decision            ApprovalDecision `json:"decision"`
	EffectiveStatus     string           `json:"effective_status"`
	Requester           string           `json:"requester,omitempty"`
	Reviewer            string           `json:"reviewer,omitempty"`
	CommandApproverNode string           `json:"command_approver_node,omitempty"`
	DecisionReviewer    string           `json:"decision_reviewer"`
	Mode                string           `json:"mode,omitempty"`
	Label               string           `json:"label,omitempty"`
	Category            string           `json:"category,omitempty"`
	CommandHash         string           `json:"command_hash,omitempty"`
	RequestReason       string           `json:"request_reason,omitempty"`
	DecisionReason      string           `json:"decision_reason,omitempty"`
	RequestedAt         string           `json:"requested_at,omitempty"`
	ExpiresAt           string           `json:"expires_at,omitempty"`
	DecidedAt           string           `json:"decided_at"`
	DecisionMessageID   string           `json:"decision_message_id,omitempty"`
	CommandText         string           `json:"command_text,omitempty"`
}

func CommandApprovalDecisionHistoryDir(sessionDir string) string {
	return filepath.Join(sessionDir, "command-approval-decisions")
}

func SyncCommandApprovalDecisionHistory(sessionDir string) error {
	events, err := Replay(sessionDir)
	if err != nil {
		return err
	}
	requests := make(map[string]CommandApprovalRequestPayload)
	requestedAt := make(map[string]string)
	for _, event := range events {
		switch event.Type {
		case CommandApprovalRequestedEventType:
			var payload CommandApprovalRequestPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return fmt.Errorf("command approval request %d decode: %w", event.Sequence, err)
			}
			requests[event.ThreadID] = payload
			requestedAt[event.ThreadID] = event.OccurredAt
		case CommandApprovalDecidedEventType:
			var payload CommandApprovalDecisionPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return fmt.Errorf("command approval decision %d decode: %w", event.Sequence, err)
			}
			entry := commandApprovalDecisionHistoryEntry(event, requests[event.ThreadID], requestedAt[event.ThreadID], payload)
			if err := writeJSONAtomically(commandApprovalDecisionHistoryPath(sessionDir, event), entry); err != nil {
				return fmt.Errorf("writing command approval decision history: %w", err)
			}
		}
	}
	return nil
}

func commandApprovalDecisionHistoryEntry(event Event, request CommandApprovalRequestPayload, requestOccurredAt string, decision CommandApprovalDecisionPayload) CommandApprovalDecisionHistoryEntry {
	return CommandApprovalDecisionHistoryEntry{
		SchemaVersion:       commandApprovalDecisionHistorySchemaVersion,
		SessionKey:          event.SessionKey,
		TmuxSessionName:     event.TmuxSessionName,
		Generation:          event.Generation,
		EventSequence:       event.Sequence,
		EventID:             event.EventID,
		ThreadID:            event.ThreadID,
		Decision:            decision.Decision,
		EffectiveStatus:     commandApprovalDecisionEffectiveStatus(request, decision),
		Requester:           request.Requester,
		Reviewer:            request.Reviewer,
		CommandApproverNode: request.CommandApproverNode,
		DecisionReviewer:    decision.Reviewer,
		Mode:                request.Mode,
		Label:               request.Label,
		Category:            request.Category,
		CommandHash:         request.CommandHash,
		RequestReason:       request.Reason,
		DecisionReason:      decision.Reason,
		RequestedAt:         requestOccurredAt,
		ExpiresAt:           request.ExpiresAt,
		DecidedAt:           event.OccurredAt,
		DecisionMessageID:   decision.MessageID,
		CommandText:         request.CommandText,
	}
}

func commandApprovalDecisionEffectiveStatus(request CommandApprovalRequestPayload, decision CommandApprovalDecisionPayload) string {
	if request.Requester == "" {
		return "stale"
	}
	if request.CommandApproverNode == "" || decision.Reviewer != request.CommandApproverNode {
		return "wrong_reviewer"
	}
	switch decision.Decision {
	case ApprovalDecisionApproved:
		return "approved"
	case ApprovalDecisionRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

func commandApprovalDecisionHistoryPath(sessionDir string, event Event) string {
	return filepath.Join(CommandApprovalDecisionHistoryDir(sessionDir), fmt.Sprintf("%012d-%s.json", event.Sequence, event.EventID))
}

func ListCommandApprovalDecisionHistory(sessionDir string) ([]CommandApprovalDecisionHistoryEntry, error) {
	entries, err := os.ReadDir(CommandApprovalDecisionHistoryDir(sessionDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	history := make([]CommandApprovalDecisionHistoryEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var record CommandApprovalDecisionHistoryEntry
		if err := readJSONFile(filepath.Join(CommandApprovalDecisionHistoryDir(sessionDir), entry.Name()), &record); err != nil {
			return nil, fmt.Errorf("reading command approval decision history %s: %w", entry.Name(), err)
		}
		history = append(history, record)
	}
	return history, nil
}
