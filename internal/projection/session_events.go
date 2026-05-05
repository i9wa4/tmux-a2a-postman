package projection

import "github.com/i9wa4/tmux-a2a-postman/internal/journal"

func replayCurrentSessionEvents(sessionDir, sessionKey string, generation int) ([]journal.Event, error) {
	events, err := journal.Replay(sessionDir)
	if err != nil {
		return nil, err
	}
	filtered := make([]journal.Event, 0, len(events))
	for _, event := range events {
		if event.SessionKey != sessionKey || event.Generation != generation {
			continue
		}
		filtered = append(filtered, event)
	}
	return filtered, nil
}
