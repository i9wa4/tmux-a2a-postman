package projection

import (
	"encoding/json"
	"fmt"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

type ApprovalStatus string

const (
	ApprovalStatusPending  ApprovalStatus = "pending"
	ApprovalStatusApproved ApprovalStatus = "approved"
	ApprovalStatusRejected ApprovalStatus = "rejected"
)

type ApprovalThreadState struct {
	ThreadID          string
	Requester         string
	Reviewer          string
	Status            ApprovalStatus
	RequestMessageID  string
	DecisionMessageID string
	RequestedAt       string
	DecidedAt         string
}

type ThreadApproval struct {
	Threads map[string]ApprovalThreadState
}

func ProjectThreadApproval(sessionDir string) (ThreadApproval, bool, error) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return ThreadApproval{}, false, nil
	}

	events, err := journal.Replay(sessionDir)
	if err != nil {
		return ThreadApproval{}, false, err
	}
	if len(events) == 0 {
		return ThreadApproval{}, false, nil
	}

	projected := ThreadApproval{
		Threads: make(map[string]ApprovalThreadState),
	}
	sawLease := false
	sawResolution := false
	sawApproval := false

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
		}

		switch event.Type {
		case journal.ApprovalRequestedEventType:
			sawApproval = true
			if err := applyApprovalRequest(projected.Threads, event); err != nil {
				return ThreadApproval{}, false, err
			}
		case journal.ApprovalDecidedEventType:
			sawApproval = true
			if err := applyApprovalDecision(projected.Threads, event); err != nil {
				return ThreadApproval{}, false, err
			}
		}
	}

	if !sawLease || !sawResolution || !sawApproval {
		return ThreadApproval{}, false, nil
	}

	return projected, true, nil
}

func applyApprovalRequest(threads map[string]ApprovalThreadState, event journal.Event) error {
	var payload journal.ApprovalRequestPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("approval request %d decode: %w", event.Sequence, err)
	}
	if payload.Requester == "" || payload.Reviewer == "" {
		return fmt.Errorf("approval request %d missing requester or reviewer", event.Sequence)
	}
	if _, exists := threads[event.ThreadID]; exists {
		return fmt.Errorf("approval request %d duplicates thread_id %q", event.Sequence, event.ThreadID)
	}

	threads[event.ThreadID] = ApprovalThreadState{
		ThreadID:         event.ThreadID,
		Requester:        payload.Requester,
		Reviewer:         payload.Reviewer,
		Status:           ApprovalStatusPending,
		RequestMessageID: payload.MessageID,
		RequestedAt:      event.OccurredAt,
	}
	return nil
}

func applyApprovalDecision(threads map[string]ApprovalThreadState, event journal.Event) error {
	var payload journal.ApprovalDecisionPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("approval decision %d decode: %w", event.Sequence, err)
	}
	thread, exists := threads[event.ThreadID]
	if !exists {
		return fmt.Errorf("approval decision %d references unknown thread_id %q", event.Sequence, event.ThreadID)
	}
	if payload.Reviewer == "" {
		return fmt.Errorf("approval decision %d missing reviewer", event.Sequence)
	}
	if payload.Reviewer != thread.Reviewer {
		return fmt.Errorf("approval decision %d reviewer %q does not match thread reviewer %q", event.Sequence, payload.Reviewer, thread.Reviewer)
	}
	if thread.Status != ApprovalStatusPending {
		return fmt.Errorf("approval decision %d resolves non-pending thread_id %q", event.Sequence, event.ThreadID)
	}

	switch payload.Decision {
	case journal.ApprovalDecisionApproved:
		thread.Status = ApprovalStatusApproved
	case journal.ApprovalDecisionRejected:
		thread.Status = ApprovalStatusRejected
	default:
		return fmt.Errorf("approval decision %d has unknown decision %q", event.Sequence, payload.Decision)
	}

	thread.DecisionMessageID = payload.MessageID
	thread.DecidedAt = event.OccurredAt
	threads[event.ThreadID] = thread
	return nil
}
