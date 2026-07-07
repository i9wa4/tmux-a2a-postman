package projection

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

type CommandApprovalStatus string

const (
	CommandApprovalStatusPending       CommandApprovalStatus = "pending"
	CommandApprovalStatusApproved      CommandApprovalStatus = "approved"
	CommandApprovalStatusRejected      CommandApprovalStatus = "rejected"
	CommandApprovalStatusExpired       CommandApprovalStatus = "expired"
	CommandApprovalStatusWrongReviewer CommandApprovalStatus = "wrong_reviewer"
	CommandApprovalStatusStale         CommandApprovalStatus = "stale"
)

type CommandApprovalThread struct {
	ThreadID  string `json:"thread_id"`
	Requester string `json:"requester,omitempty"`
	Reviewer  string `json:"reviewer,omitempty"`
	// ReviewerNode is the config-resolved, validated reviewer_node captured
	// at request time (#626). Decision validation MUST compare against this
	// field, never Reviewer (a requester-influenceable audit label) — see
	// applyCommandApprovalDecision.
	ReviewerNode      string                `json:"reviewer_node,omitempty"`
	Mode              string                `json:"mode,omitempty"`
	Label             string                `json:"label,omitempty"`
	Category          string                `json:"category,omitempty"`
	CommandHash       string                `json:"command_hash,omitempty"`
	Status            CommandApprovalStatus `json:"status"`
	Reason            string                `json:"reason,omitempty"`
	RequestedAt       string                `json:"requested_at,omitempty"`
	ExpiresAt         string                `json:"expires_at,omitempty"`
	DecidedAt         string                `json:"decided_at,omitempty"`
	DecisionMessageID string                `json:"decision_message_id,omitempty"`
}

type CommandApprovalState struct {
	Threads map[string]CommandApprovalThread `json:"threads"`
}

func ProjectCommandApprovalState(sessionDir string, now time.Time) (CommandApprovalState, bool, error) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return CommandApprovalState{}, false, nil
	}

	events, err := journal.Replay(sessionDir)
	if err != nil {
		return CommandApprovalState{}, false, err
	}
	if len(events) == 0 {
		return CommandApprovalState{}, false, nil
	}

	projected := CommandApprovalState{Threads: make(map[string]CommandApprovalThread)}
	sawLease := false
	sawResolution := false
	sawCommandApproval := false

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
		case journal.CommandApprovalRequestedEventType:
			sawCommandApproval = true
			if err := applyCommandApprovalRequest(projected.Threads, event); err != nil {
				return CommandApprovalState{}, false, err
			}
		case journal.CommandApprovalDecidedEventType:
			sawCommandApproval = true
			if err := applyCommandApprovalDecision(projected.Threads, event); err != nil {
				return CommandApprovalState{}, false, err
			}
		}
	}

	if !sawLease || !sawResolution || !sawCommandApproval {
		return CommandApprovalState{}, false, nil
	}
	for threadID, thread := range projected.Threads {
		if commandApprovalExpired(thread, now) {
			thread.Status = CommandApprovalStatusExpired
			projected.Threads[threadID] = thread
		}
	}
	return projected, true, nil
}

func applyCommandApprovalRequest(threads map[string]CommandApprovalThread, event journal.Event) error {
	if event.ThreadID == "" {
		return fmt.Errorf("command approval request %d missing thread_id", event.Sequence)
	}
	var payload journal.CommandApprovalRequestPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("command approval request %d decode: %w", event.Sequence, err)
	}
	if payload.Requester == "" || payload.Reviewer == "" || payload.Label == "" || payload.CommandHash == "" {
		return fmt.Errorf("command approval request %d missing requester, reviewer, label, or command_hash", event.Sequence)
	}

	threads[event.ThreadID] = CommandApprovalThread{
		ThreadID:     event.ThreadID,
		Requester:    payload.Requester,
		Reviewer:     payload.Reviewer,
		ReviewerNode: payload.ReviewerNode,
		Mode:         payload.Mode,
		Label:        payload.Label,
		Category:     payload.Category,
		CommandHash:  payload.CommandHash,
		Status:       CommandApprovalStatusPending,
		Reason:       payload.Reason,
		RequestedAt:  event.OccurredAt,
		ExpiresAt:    payload.ExpiresAt,
	}
	return nil
}

func applyCommandApprovalDecision(threads map[string]CommandApprovalThread, event journal.Event) error {
	if event.ThreadID == "" {
		return fmt.Errorf("command approval decision %d missing thread_id", event.Sequence)
	}
	var payload journal.CommandApprovalDecisionPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("command approval decision %d decode: %w", event.Sequence, err)
	}
	if payload.Reviewer == "" {
		return fmt.Errorf("command approval decision %d missing reviewer", event.Sequence)
	}

	thread, exists := threads[event.ThreadID]
	if !exists || thread.Requester == "" {
		threads[event.ThreadID] = CommandApprovalThread{
			ThreadID:  event.ThreadID,
			Reviewer:  payload.Reviewer,
			Status:    CommandApprovalStatusStale,
			Reason:    payload.Reason,
			DecidedAt: event.OccurredAt,
		}
		return nil
	}
	// #626 B1: validate against thread.ReviewerNode (the config-resolved,
	// validated reviewer_node captured at request time), never
	// thread.Reviewer (a plain requester-influenceable audit label). Both
	// sides of the old thread.Reviewer comparison were requester-controlled
	// — the requester's own policy match set thread.Reviewer, and the
	// requester's own --record-decision/--reviewer or reply "from" set
	// payload.Reviewer — making self-approval trivial. An empty
	// thread.ReviewerNode (no valid reviewer_node was configured for this
	// request) must never be treated as a match; it means this thread was
	// created without a real reviewer, so no decision on it can be trusted.
	if thread.ReviewerNode == "" || payload.Reviewer != thread.ReviewerNode {
		thread.Status = CommandApprovalStatusWrongReviewer
		thread.Reason = payload.Reason
		thread.DecidedAt = event.OccurredAt
		threads[event.ThreadID] = thread
		return nil
	}

	switch payload.Decision {
	case journal.ApprovalDecisionApproved:
		thread.Status = CommandApprovalStatusApproved
	case journal.ApprovalDecisionRejected:
		thread.Status = CommandApprovalStatusRejected
	default:
		return fmt.Errorf("command approval decision %d has unknown decision %q", event.Sequence, payload.Decision)
	}
	thread.Reason = payload.Reason
	thread.DecidedAt = event.OccurredAt
	threads[event.ThreadID] = thread
	return nil
}

func commandApprovalExpired(thread CommandApprovalThread, now time.Time) bool {
	if thread.ExpiresAt == "" || now.IsZero() {
		return false
	}
	if thread.Status != CommandApprovalStatusPending && thread.Status != CommandApprovalStatusApproved {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, thread.ExpiresAt)
	if err != nil {
		return false
	}
	return !now.Before(expiresAt)
}
