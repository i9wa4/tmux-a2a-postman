package projection

import (
	"encoding/json"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

const SessionHealthSnapshotEventType = "session_health_snapshot"

func ProjectSessionHealth(sessionDir string) (status.SessionHealth, bool, error) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return status.SessionHealth{}, false, nil
	}

	events, err := journal.Replay(sessionDir)
	if err != nil || len(events) == 0 {
		return status.SessionHealth{}, false, nil
	}

	var projected status.SessionHealth
	sawLease := false
	sawResolution := false
	sawSnapshot := false

	for _, event := range events {
		if event.SessionKey != state.SessionKey || event.Generation != state.Generation {
			continue
		}

		switch event.Type {
		case "lease_acquired":
			sawLease = true
		case "session_resolved":
			sawResolution = true
		case SessionHealthSnapshotEventType:
			if err := json.Unmarshal(event.Payload, &projected); err != nil {
				return status.SessionHealth{}, false, nil
			}
			if projected.SessionName == "" {
				return status.SessionHealth{}, false, nil
			}
			sawSnapshot = true
		}
	}

	if !sawLease || !sawResolution || !sawSnapshot {
		return status.SessionHealth{}, false, nil
	}

	return projected, true, nil
}
