package projection

import (
	"encoding/json"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

const (
	AutoPingPendingEventType    = "auto_ping_pending"
	AutoPingDeliveredEventType  = "auto_ping_delivered"
	AutoPingSuppressedEventType = "auto_ping_suppressed"
	AutoPingBlockedEventType    = "auto_ping_blocked"
	AutoPingRetryingEventType   = "auto_ping_retrying"
)

const (
	AutoPingStatusPending           = "pending"
	AutoPingStatusDelivered         = "delivered"
	AutoPingStatusSuppressed        = "suppressed"
	AutoPingStatusOwnershipBlocked  = "ownership_blocked"
	AutoPingStatusRetryingFullInbox = "retrying_full_inbox"
)

type AutoPingEventPayload struct {
	NodeKey           string  `json:"node_key"`
	ContextID         string  `json:"context_id,omitempty"`
	SessionName       string  `json:"session_name,omitempty"`
	NodeName          string  `json:"node_name,omitempty"`
	PaneID            string  `json:"pane_id,omitempty"`
	Reason            string  `json:"reason,omitempty"`
	TriggeredAt       string  `json:"triggered_at,omitempty"`
	DelaySeconds      float64 `json:"delay_seconds,omitempty"`
	NotBeforeAt       string  `json:"not_before_at,omitempty"`
	DeliveredAt       string  `json:"delivered_at,omitempty"`
	SuppressedAt      string  `json:"suppressed_at,omitempty"`
	SuppressUntilAt   string  `json:"suppress_until_at,omitempty"`
	SuppressionReason string  `json:"suppression_reason,omitempty"`
	BlockedAt         string  `json:"blocked_at,omitempty"`
	BlockedReason     string  `json:"blocked_reason,omitempty"`
	RetryAt           string  `json:"retry_at,omitempty"`
	RetryReason       string  `json:"retry_reason,omitempty"`
}

type AutoPingNodeState struct {
	NodeKey           string
	ContextID         string
	SessionName       string
	NodeName          string
	PaneID            string
	Reason            string
	TriggeredAt       string
	DelaySeconds      float64
	NotBeforeAt       string
	DeliveredAt       string
	SuppressedAt      string
	SuppressUntilAt   string
	SuppressionReason string
	BlockedAt         string
	BlockedReason     string
	RetryAt           string
	RetryReason       string
	Status            string
	Pending           bool
}

type AutoPingState struct {
	Nodes map[string]AutoPingNodeState
}

func ProjectAutoPingState(sessionDir string) (AutoPingState, bool, error) {
	return projectAutoPingState(sessionDir, true)
}

func ProjectAutoPingHistory(sessionDir string) (AutoPingState, bool, error) {
	return projectAutoPingState(sessionDir, false)
}

func projectAutoPingState(sessionDir string, currentGenerationOnly bool) (AutoPingState, bool, error) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return AutoPingState{}, false, nil
	}

	events, err := journal.Replay(sessionDir)
	if err != nil || len(events) == 0 {
		return AutoPingState{}, false, nil
	}

	projected := AutoPingState{
		Nodes: make(map[string]AutoPingNodeState),
	}
	sawLease := false
	sawResolution := false

	for _, event := range events {
		if event.SessionKey != state.SessionKey {
			continue
		}
		currentGeneration := event.Generation == state.Generation

		switch event.Type {
		case "lease_acquired":
			if currentGeneration {
				sawLease = true
			}
			continue
		case "session_resolved":
			if currentGeneration {
				sawResolution = true
			}
			continue
		}
		if currentGenerationOnly && !currentGeneration {
			continue
		}

		switch event.Type {
		case AutoPingPendingEventType, AutoPingDeliveredEventType, AutoPingSuppressedEventType, AutoPingBlockedEventType, AutoPingRetryingEventType:
		default:
			continue
		}

		var payload AutoPingEventPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return AutoPingState{}, false, nil
		}
		if payload.NodeKey == "" {
			return AutoPingState{}, false, nil
		}

		previous := projected.Nodes[payload.NodeKey]
		nodeState := AutoPingNodeState{
			NodeKey:           payload.NodeKey,
			ContextID:         payload.ContextID,
			SessionName:       payload.SessionName,
			NodeName:          payload.NodeName,
			PaneID:            payload.PaneID,
			Reason:            payload.Reason,
			TriggeredAt:       payload.TriggeredAt,
			DelaySeconds:      payload.DelaySeconds,
			NotBeforeAt:       payload.NotBeforeAt,
			DeliveredAt:       payload.DeliveredAt,
			SuppressedAt:      payload.SuppressedAt,
			SuppressUntilAt:   payload.SuppressUntilAt,
			SuppressionReason: payload.SuppressionReason,
			BlockedAt:         payload.BlockedAt,
			BlockedReason:     payload.BlockedReason,
			RetryAt:           payload.RetryAt,
			RetryReason:       payload.RetryReason,
		}
		if nodeState.DeliveredAt == "" {
			nodeState.DeliveredAt = previous.DeliveredAt
		}
		switch event.Type {
		case AutoPingPendingEventType:
			nodeState.Status = AutoPingStatusPending
			nodeState.Pending = true
		case AutoPingDeliveredEventType:
			nodeState.Status = AutoPingStatusDelivered
			nodeState.Pending = false
		case AutoPingSuppressedEventType:
			nodeState.Status = AutoPingStatusSuppressed
			nodeState.Pending = false
		case AutoPingBlockedEventType:
			nodeState.Status = AutoPingStatusOwnershipBlocked
			nodeState.Pending = true
		case AutoPingRetryingEventType:
			nodeState.Status = AutoPingStatusRetryingFullInbox
			nodeState.Pending = true
		}
		projected.Nodes[payload.NodeKey] = nodeState
	}

	if !sawLease || !sawResolution {
		return AutoPingState{}, false, nil
	}

	return projected, true, nil
}
