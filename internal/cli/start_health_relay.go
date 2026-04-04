package cli

import (
	"context"
	"log"
	"sort"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

func relayDaemonEventsToTUI(ctx context.Context, rawEvents <-chan tui.DaemonEvent, tuiEvents chan<- tui.DaemonEvent, baseDir, contextID string, cfg *config.Config) {
	defer close(tuiEvents)

	knownSessions := make(map[string]struct{})

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-rawEvents:
			if !ok {
				return
			}

			tuiEvents <- event
			refreshKnownSessions(knownSessions, event)
			if !shouldRefreshSessionHealth(event.Type) || len(knownSessions) == 0 {
				continue
			}

			for _, sessionName := range sortedSessionNames(knownSessions) {
				health, err := collectSessionHealth(baseDir, contextID, sessionName, cfg)
				if err != nil {
					log.Printf("postman: session health relay skipped %s: %v\n", sessionName, err)
					continue
				}
				tuiEvents <- tui.DaemonEvent{
					Type: "session_health_update",
					Details: map[string]interface{}{
						"health": health,
					},
				}
			}
		}
	}
}

func refreshKnownSessions(knownSessions map[string]struct{}, event tui.DaemonEvent) {
	if event.Details == nil {
		return
	}
	sessionList, ok := event.Details["sessions"].([]tui.SessionInfo)
	if !ok {
		return
	}

	clear(knownSessions)
	for _, sessionInfo := range sessionList {
		knownSessions[sessionInfo.Name] = struct{}{}
	}
}

func sortedSessionNames(knownSessions map[string]struct{}) []string {
	sessionNames := make([]string, 0, len(knownSessions))
	for sessionName := range knownSessions {
		sessionNames = append(sessionNames, sessionName)
	}
	sort.Strings(sessionNames)
	return sessionNames
}

func shouldRefreshSessionHealth(eventType string) bool {
	switch eventType {
	case "status_update", "config_update", "ball_state_update", "waiting_state_update", "inbox_unread_count_update", "node_alive", "pane_disappeared", "pane_restart", "session_collapsed":
		return true
	default:
		return false
	}
}
