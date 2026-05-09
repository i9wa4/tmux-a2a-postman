package cli

import (
	"context"
	"log"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

type sessionHealthRefresher func(baseDir, contextID, sessionName string, cfg *config.Config) (status.SessionHealth, error)

const sessionHealthRefreshWorkerLimit = 4

type sessionHealthRequest struct {
	generation   uint64
	sessionNames []string
}

type sessionHealthResult struct {
	generation uint64
	session    string
	health     status.SessionHealth
	err        error
}

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
	healthRequests := make(chan sessionHealthRequest, 1)
	var latestHealthGeneration atomic.Uint64
	var healthWG sync.WaitGroup
	healthWG.Add(1)
	go func() {
		defer healthWG.Done()
		relaySessionHealthUpdates(ctx, healthRequests, tuiEvents, baseDir, contextID, cfg, refreshHealth, &latestHealthGeneration)
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

			request := sessionHealthRequest{
				generation:   latestHealthGeneration.Add(1),
				sessionNames: sortedSessionNames(knownSessions),
			}
			if !requestSessionHealthRefresh(ctx, healthRequests, request) {
				return
			}
		}
	}
}

func requestSessionHealthRefresh(ctx context.Context, healthRequests chan sessionHealthRequest, request sessionHealthRequest) bool {
	select {
	case healthRequests <- request:
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
	case healthRequests <- request:
		return true
	case <-ctx.Done():
		return false
	}
}

func relaySessionHealthUpdates(
	ctx context.Context,
	healthRequests <-chan sessionHealthRequest,
	tuiEvents chan tui.DaemonEvent,
	baseDir, contextID string,
	cfg *config.Config,
	refreshHealth sessionHealthRefresher,
	latestGeneration *atomic.Uint64,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case request, ok := <-healthRequests:
			if !ok {
				return
			}
			if !refreshSessionHealthBatch(ctx, request, tuiEvents, baseDir, contextID, cfg, refreshHealth, latestGeneration) {
				return
			}
		}
	}
}

func refreshSessionHealthBatch(
	ctx context.Context,
	request sessionHealthRequest,
	tuiEvents chan tui.DaemonEvent,
	baseDir, contextID string,
	cfg *config.Config,
	refreshHealth sessionHealthRefresher,
	latestGeneration *atomic.Uint64,
) bool {
	if len(request.sessionNames) == 0 {
		return true
	}

	workerCount := sessionHealthRefreshWorkerLimit
	if len(request.sessionNames) < workerCount {
		workerCount = len(request.sessionNames)
	}
	jobs := make(chan string)
	results := make(chan sessionHealthResult, len(request.sessionNames))

	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for sessionName := range jobs {
				health, err := refreshHealth(baseDir, contextID, sessionName, cfg)
				select {
				case results <- sessionHealthResult{
					generation: request.generation,
					session:    sessionName,
					health:     health,
					err:        err,
				}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, sessionName := range request.sessionNames {
			select {
			case jobs <- sessionName:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		workers.Wait()
		close(results)
	}()

	for {
		select {
		case <-ctx.Done():
			return false
		case result, ok := <-results:
			if !ok {
				return true
			}
			if result.generation != latestGeneration.Load() {
				continue
			}
			if result.err != nil {
				log.Printf("postman: session health relay skipped %s: %v\n", result.session, result.err)
				continue
			}
			if !forwardTUIEvent(ctx, tuiEvents, tui.DaemonEvent{
				Type: "session_health_update",
				Details: map[string]interface{}{
					"health": result.health,
				},
			}) {
				return false
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
