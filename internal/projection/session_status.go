package projection

import (
	"encoding/json"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

const SessionStatusSnapshotEventType = "session_status_snapshot"

// LegacySessionHealthSnapshotEventType is retained only for read-only archive
// replay of pre-v4 journals. New writers must use SessionStatusSnapshotEventType.
const LegacySessionHealthSnapshotEventType = "session_health_snapshot"

func ProjectSessionStatus(sessionDir string) (status.SessionStatus, bool, error) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return status.SessionStatus{}, false, nil
	}

	events, err := journal.Replay(sessionDir)
	if err != nil || len(events) == 0 {
		return status.SessionStatus{}, false, nil
	}

	var projected status.SessionStatus
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
		case SessionStatusSnapshotEventType:
			if err := json.Unmarshal(event.Payload, &projected); err != nil {
				return status.SessionStatus{}, false, nil
			}
			if projected.SessionName == "" {
				return status.SessionStatus{}, false, nil
			}
			projected.SchemaVersion = status.SchemaVersion
			sawSnapshot = true
		case LegacySessionHealthSnapshotEventType:
			if sawSnapshot {
				continue
			}
			if err := json.Unmarshal(event.Payload, &projected); err != nil {
				return status.SessionStatus{}, false, nil
			}
			if projected.SessionName == "" {
				return status.SessionStatus{}, false, nil
			}
			projected.SchemaVersion = status.SchemaVersion
			sawSnapshot = true
		}
	}

	if !sawLease || !sawResolution || !sawSnapshot {
		return status.SessionStatus{}, false, nil
	}

	return projected, true, nil
}
