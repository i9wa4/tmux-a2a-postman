package cli

import (
	"context"
	"log"
	"sort"
	"sync"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

type sessionHealthRefresher func(baseDir, contextID, sessionName string, cfg *config.Config) (status.SessionHealth, error)

func relayDaemonEventsToTUI(ctx context.Context, rawEvents <-chan tui.DaemonEvent, tuiEvents chan tui.DaemonEvent, baseDir, contextID string, cfg *config.Config) {
	relayDaemonEventsToTUIWithHealthRefresher(ctx, rawEvents, tuiEvents, baseDir, contextID, cfg, refreshProjectedSessionHealth)
}

func relayDaemonEventsToTUIWithHealthRefresher(
	ctx context.Context,
	rawEvents <-chan tui.DaemonEvent,
	tuiEvents chan tui.DaemonEvent,
	baseDir, contextID string,
	cfg *config.Config,
	refreshHealth sessionHealthRefresher,
) {
	if tuiEvents != nil {
		defer close(tuiEvents)
	}

	knownSessions := make(map[string]struct{})
	healthRequests := make(chan []string, 1)
	var healthWG sync.WaitGroup
	healthWG.Add(1)
	go func() {
		defer healthWG.Done()
		relaySessionHealthUpdates(ctx, healthRequests, tuiEvents, baseDir, contextID, cfg, refreshHealth)
	}()
	defer func() {
		close(healthRequests)
		healthWG.Wait()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-rawEvents:
			if !ok {
				return
			}

			if !forwardTUIEvent(ctx, tuiEvents, event) {
				return
			}
			refreshKnownSessions(knownSessions, event)
			if !shouldRefreshSessionHealth(event.Type) || len(knownSessions) == 0 {
				continue
			}

			if !requestSessionHealthRefresh(ctx, healthRequests, sortedSessionNames(knownSessions)) {
				return
			}
		}
	}
}

func requestSessionHealthRefresh(ctx context.Context, healthRequests chan []string, sessionNames []string) bool {
	select {
	case healthRequests <- sessionNames:
		return true
	case <-ctx.Done():
		return false
	default:
	}

	select {
	case <-healthRequests:
	case <-ctx.Done():
		return false
	default:
	}

	select {
	case healthRequests <- sessionNames:
		return true
	case <-ctx.Done():
		return false
	default:
		return true
	}
}

func relaySessionHealthUpdates(
	ctx context.Context,
	healthRequests <-chan []string,
	tuiEvents chan tui.DaemonEvent,
	baseDir, contextID string,
	cfg *config.Config,
	refreshHealth sessionHealthRefresher,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case sessionNames, ok := <-healthRequests:
			if !ok {
				return
			}
			for _, sessionName := range sessionNames {
				select {
				case <-ctx.Done():
					return
				default:
				}
				health, err := refreshHealth(baseDir, contextID, sessionName, cfg)
				if err != nil {
					log.Printf("postman: session health relay skipped %s: %v\n", sessionName, err)
					continue
				}
				if !forwardTUIEvent(ctx, tuiEvents, tui.DaemonEvent{
					Type: "session_health_update",
					Details: map[string]interface{}{
						"health": health,
					},
				}) {
					return
				}
			}
		}
	}
}

func forwardTUIEvent(ctx context.Context, tuiEvents chan tui.DaemonEvent, event tui.DaemonEvent) bool {
	if tuiEvents == nil {
		return true
	}
	select {
	case tuiEvents <- event:
		return true
	case <-ctx.Done():
		return false
	default:
		if !isSessionSnapshotEvent(event) || cap(tuiEvents) == 0 {
			return true
		}
	}

	select {
	case <-tuiEvents:
	case <-ctx.Done():
		return false
	default:
	}

	select {
	case tuiEvents <- event:
		return true
	case <-ctx.Done():
		return false
	default:
		return true
	}
}

func isSessionSnapshotEvent(event tui.DaemonEvent) bool {
	if event.Type != "status_update" && event.Type != "config_update" {
		return false
	}
	if event.Details == nil {
		return false
	}
	_, ok := event.Details["sessions"].([]tui.SessionInfo)
	return ok
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
	case "status_update", "config_update", "node_activity_update", "inbox_unread_count_update", "node_alive", "pane_disappeared", "pane_restart", "session_collapsed":
		return true
	default:
		return false
	}
}
