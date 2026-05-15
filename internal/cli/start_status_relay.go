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

type sessionStatusRefresher func(baseDir, contextID, sessionName string, cfg *config.Config) (status.SessionStatus, error)

const sessionStatusRefreshWorkerLimit = 4

type sessionStatusRequest struct {
	generation   uint64
	sessionNames []string
}

type sessionStatusResult struct {
	generation uint64
	session    string
	status     status.SessionStatus
	err        error
}

func relayDaemonEventsToTUI(ctx context.Context, rawEvents <-chan tui.DaemonEvent, tuiEvents chan tui.DaemonEvent, baseDir, contextID string, cfg *config.Config) {
	relayDaemonEventsToTUIWithStatusRefresher(ctx, rawEvents, tuiEvents, baseDir, contextID, cfg, refreshProjectedSessionStatus)
}

func relayDaemonEventsToTUIWithStatusRefresher(
	ctx context.Context,
	rawEvents <-chan tui.DaemonEvent,
	tuiEvents chan tui.DaemonEvent,
	baseDir, contextID string,
	cfg *config.Config,
	refreshStatus sessionStatusRefresher,
) {
	if tuiEvents != nil {
		defer close(tuiEvents)
	}

	knownSessions := make(map[string]struct{})
	statusRequests := make(chan sessionStatusRequest, 1)
	var latestStatusGeneration atomic.Uint64
	var statusWG sync.WaitGroup
	statusWG.Add(1)
	go func() {
		defer statusWG.Done()
		relaySessionStatusUpdates(ctx, statusRequests, tuiEvents, baseDir, contextID, cfg, refreshStatus, &latestStatusGeneration)
	}()
	defer func() {
		close(statusRequests)
		statusWG.Wait()
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
			if !shouldRefreshSessionStatus(event.Type) || len(knownSessions) == 0 {
				continue
			}

			request := sessionStatusRequest{
				generation:   latestStatusGeneration.Add(1),
				sessionNames: sortedSessionNames(knownSessions),
			}
			if !requestSessionStatusRefresh(ctx, statusRequests, request) {
				return
			}
		}
	}
}

func requestSessionStatusRefresh(ctx context.Context, statusRequests chan sessionStatusRequest, request sessionStatusRequest) bool {
	select {
	case statusRequests <- request:
		return true
	case <-ctx.Done():
		return false
	default:
	}

	select {
	case <-statusRequests:
	case <-ctx.Done():
		return false
	default:
	}

	select {
	case statusRequests <- request:
		return true
	case <-ctx.Done():
		return false
	}
}

func relaySessionStatusUpdates(
	ctx context.Context,
	statusRequests <-chan sessionStatusRequest,
	tuiEvents chan tui.DaemonEvent,
	baseDir, contextID string,
	cfg *config.Config,
	refreshStatus sessionStatusRefresher,
	latestGeneration *atomic.Uint64,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case request, ok := <-statusRequests:
			if !ok {
				return
			}
			if !refreshSessionStatusBatch(ctx, request, tuiEvents, baseDir, contextID, cfg, refreshStatus, latestGeneration) {
				return
			}
		}
	}
}

func refreshSessionStatusBatch(
	ctx context.Context,
	request sessionStatusRequest,
	tuiEvents chan tui.DaemonEvent,
	baseDir, contextID string,
	cfg *config.Config,
	refreshStatus sessionStatusRefresher,
	latestGeneration *atomic.Uint64,
) bool {
	if len(request.sessionNames) == 0 {
		return true
	}

	workerCount := sessionStatusRefreshWorkerLimit
	if len(request.sessionNames) < workerCount {
		workerCount = len(request.sessionNames)
	}
	jobs := make(chan string)
	results := make(chan sessionStatusResult, len(request.sessionNames))

	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for sessionName := range jobs {
				sessionStatus, err := refreshStatus(baseDir, contextID, sessionName, cfg)
				select {
				case results <- sessionStatusResult{
					generation: request.generation,
					session:    sessionName,
					status:     sessionStatus,
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
				log.Printf("postman: session status relay skipped %s: %v\n", result.session, result.err)
				continue
			}
			if !forwardTUIEvent(ctx, tuiEvents, tui.DaemonEvent{
				Type: "session_status_update",
				Details: map[string]interface{}{
					"status": result.status,
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

func shouldRefreshSessionStatus(eventType string) bool {
	switch eventType {
	case "status_update", "config_update", "node_activity_update", "inbox_unread_count_update", "node_alive", "pane_disappeared", "pane_restart", "session_collapsed":
		return true
	default:
		return false
	}
}
