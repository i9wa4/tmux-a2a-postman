package projection

import (
	"encoding/json"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

const (
	AutoPingPendingEventType   = "auto_ping_pending"
	AutoPingDeliveredEventType = "auto_ping_delivered"
)

type AutoPingEventPayload struct {
	NodeKey      string  `json:"node_key"`
	SessionName  string  `json:"session_name,omitempty"`
	NodeName     string  `json:"node_name,omitempty"`
	PaneID       string  `json:"pane_id,omitempty"`
	Reason       string  `json:"reason,omitempty"`
	TriggeredAt  string  `json:"triggered_at,omitempty"`
	DelaySeconds float64 `json:"delay_seconds,omitempty"`
	NotBeforeAt  string  `json:"not_before_at,omitempty"`
	DeliveredAt  string  `json:"delivered_at,omitempty"`
}

type AutoPingNodeState struct {
	NodeKey      string
	SessionName  string
	NodeName     string
	PaneID       string
	Reason       string
	TriggeredAt  string
	DelaySeconds float64
	NotBeforeAt  string
	DeliveredAt  string
	Pending      bool
}

type AutoPingState struct {
	Nodes map[string]AutoPingNodeState
}

func ProjectAutoPingState(sessionDir string) (AutoPingState, bool, error) {
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
		case AutoPingPendingEventType, AutoPingDeliveredEventType:
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

		nodeState := AutoPingNodeState{
			NodeKey:      payload.NodeKey,
			SessionName:  payload.SessionName,
			NodeName:     payload.NodeName,
			PaneID:       payload.PaneID,
			Reason:       payload.Reason,
			TriggeredAt:  payload.TriggeredAt,
			DelaySeconds: payload.DelaySeconds,
			NotBeforeAt:  payload.NotBeforeAt,
			DeliveredAt:  payload.DeliveredAt,
			Pending:      event.Type == AutoPingPendingEventType,
		}
		projected.Nodes[payload.NodeKey] = nodeState
	}

	if !sawLease || !sawResolution {
		return AutoPingState{}, false, nil
	}

	return projected, true, nil
}
