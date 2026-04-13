package projection

import (
	"encoding/json"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

const TimelineRedactedValue = "[REDACTED]"

type TimelineOptions struct {
	IncludeControlPlane bool
}

type TimelineEntry struct {
	Sequence   int                    `json:"sequence"`
	EventID    string                 `json:"event_id"`
	Type       string                 `json:"type"`
	Visibility journal.Visibility     `json:"visibility"`
	ThreadID   string                 `json:"thread_id,omitempty"`
	OccurredAt string                 `json:"occurred_at"`
	Payload    map[string]interface{} `json:"payload,omitempty"`
}

type Timeline struct {
	Entries []TimelineEntry `json:"entries"`
}

func ProjectTimeline(sessionDir string, options TimelineOptions) (Timeline, bool, error) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return Timeline{}, false, nil
	}

	events, err := journal.Replay(sessionDir)
	if err != nil {
		return Timeline{}, false, err
	}
	if len(events) == 0 {
		return Timeline{}, false, nil
	}

	projected := Timeline{
		Entries: []TimelineEntry{},
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
		}

		if event.Visibility == journal.VisibilityControlPlaneOnly && !options.IncludeControlPlane {
			continue
		}

		payload, err := timelinePayload(event.Payload)
		if err != nil {
			return Timeline{}, false, err
		}
		projected.Entries = append(projected.Entries, TimelineEntry{
			Sequence:   event.Sequence,
			EventID:    event.EventID,
			Type:       event.Type,
			Visibility: event.Visibility,
			ThreadID:   event.ThreadID,
			OccurredAt: event.OccurredAt,
			Payload:    redactTimelinePayload(payload),
		})
	}

	if !sawLease || !sawResolution || len(projected.Entries) == 0 {
		return Timeline{}, false, nil
	}
	return projected, true, nil
}

func timelinePayload(raw json.RawMessage) (map[string]interface{}, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func redactTimelinePayload(payload map[string]interface{}) map[string]interface{} {
	if len(payload) == 0 {
		return payload
	}
	redacted := make(map[string]interface{}, len(payload))
	for key, value := range payload {
		if isTimelineSensitiveKey(key) {
			redacted[key] = TimelineRedactedValue
			continue
		}
		switch typed := value.(type) {
		case map[string]interface{}:
			redacted[key] = redactTimelinePayload(typed)
		case []interface{}:
			redacted[key] = redactTimelineArray(typed)
		default:
			redacted[key] = value
		}
	}
	return redacted
}

func redactTimelineArray(values []interface{}) []interface{} {
	redacted := make([]interface{}, len(values))
	for idx, value := range values {
		switch typed := value.(type) {
		case map[string]interface{}:
			redacted[idx] = redactTimelinePayload(typed)
		case []interface{}:
			redacted[idx] = redactTimelineArray(typed)
		default:
			redacted[idx] = value
		}
	}
	return redacted
}

func isTimelineSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
	for _, token := range []string{
		"content",
		"prompt",
		"tool_argument",
		"tool_arguments",
		"tool_args",
		"secret",
		"token",
		"password",
		"credential",
		"authorization",
		"api_key",
		"cookie",
	} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}
