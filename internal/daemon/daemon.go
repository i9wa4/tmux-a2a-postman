package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/i9wa4/tmux-a2a-postman/internal/alert"
	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/heartbeat"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/reminder"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
	"github.com/i9wa4/tmux-a2a-postman/internal/uinode"
)

const inboxCheckInterval = 30 * time.Second // Issue #239: ticker interval for inbox stagnation checks

// safeAfterFunc wraps time.AfterFunc with panic recovery (Issue #57).
func safeAfterFunc(d time.Duration, name string, events chan<- tui.DaemonEvent, fn func()) *time.Timer {
	return time.AfterFunc(d, func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				log.Printf("🚨 PANIC in timer callback %q: %v\n%s\n", name, r, string(stack))
				if events != nil {
					events <- tui.DaemonEvent{
						Type:    "error",
						Message: fmt.Sprintf("Internal error in %s (recovered)", name),
					}
				}
			}
		}()
		fn()
	})
}

// replaceWaitingState replaces the state field value within YAML frontmatter only,
// and updates state_updated_at to the current time (#175).
// Scoped to frontmatter to prevent accidental replacement of state mentions in message body.
func replaceWaitingState(content, oldState, newState string) string {
	// Find frontmatter boundaries
	first := strings.Index(content, "---\n")
	if first < 0 {
		return content
	}
	rest := content[first+4:]
	second := strings.Index(rest, "\n---")
	if second < 0 {
		return content
	}
	fm := rest[:second]
	after := rest[second:]

	// Replace state in frontmatter only
	fm = strings.Replace(fm, "state: "+oldState, "state: "+newState, 1)

	// Update state_updated_at
	now := time.Now().UTC().Format(time.RFC3339)
	if strings.Contains(fm, "state_updated_at: ") {
		lines := strings.Split(fm, "\n")
		for i, line := range lines {
			if strings.HasPrefix(line, "state_updated_at: ") {
				lines[i] = "state_updated_at: " + now
				break
			}
		}
		fm = strings.Join(lines, "\n")
	} else {
		fm += "\nstate_updated_at: " + now
	}

	return content[:first+4] + fm + after
}

func frontmatterBool(content, key string) bool {
	first := strings.Index(content, "---\n")
	if first < 0 {
		return false
	}
	rest := content[first+4:]
	second := strings.Index(rest, "\n---")
	if second < 0 {
		return false
	}
	for _, line := range strings.Split(rest[:second], "\n") {
		if strings.TrimSpace(line) == key+": true" {
			return true
		}
	}
	return false
}

func frontmatterValue(content, key string) string {
	first := strings.Index(content, "---\n")
	if first < 0 {
		return ""
	}
	rest := content[first+4:]
	second := strings.Index(rest, "\n---")
	if second < 0 {
		return ""
	}
	for _, line := range strings.Split(rest[:second], "\n") {
		prefix := key + ": "
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func recordCompatibilityMailboxPayload(sessionDir, sessionName, eventType string, visibility journal.Visibility, payload journal.MailboxEventPayload) {
	if err := journal.RecordProcessMailboxPayload(sessionDir, sessionName, eventType, visibility, payload, time.Now()); err != nil {
		log.Printf("postman: WARNING: compatibility mailbox append failed for %s: %v\n", eventType, err)
	}
}

func syncCompatibilityMailboxProjection(sessionDir string) {
	if err := projection.SyncCompatibilityMailbox(sessionDir); err != nil {
		log.Printf("postman: WARNING: compatibility mailbox sync failed for %s: %v\n", sessionDir, err)
	}
}

func compatibilityMailboxPayloadForFile(filename, relativePath, content string) journal.MailboxEventPayload {
	payload := journal.MailboxEventPayload{
		MessageID: filename,
		Path:      relativePath,
		Content:   content,
	}
	if info, err := message.ParseMessageFilename(filename); err == nil {
		payload.From = info.From
		payload.To = info.To
	}
	if metadata, err := message.ParseEnvelopeMetadata(content); err == nil {
		if payload.From == "" {
			payload.From = metadata.From
		}
		if payload.To == "" {
			payload.To = metadata.To
		}
		if metadata.ThreadID != "" {
			payload.ThreadID = metadata.ThreadID
		}
	}
	if payload.ThreadID == "" {
		payload.ThreadID = frontmatterValue(content, "thread_id")
	}
	return payload
}

func compatibilitySubmitSessionDir(requestPath string) (string, bool) {
	requestDir := filepath.Dir(requestPath)
	if filepath.Base(requestDir) != "requests" {
		return "", false
	}
	submitDir := filepath.Dir(requestDir)
	if filepath.Base(submitDir) != "compatibility-submit" {
		return "", false
	}
	snapshotDir := filepath.Dir(submitDir)
	if filepath.Base(snapshotDir) != "snapshot" {
		return "", false
	}
	sessionDir := filepath.Dir(snapshotDir)
	if sessionDir == "." || sessionDir == string(filepath.Separator) {
		return "", false
	}
	return sessionDir, true
}

func handleCompatibilitySubmitSend(sessionDir string, request projection.CompatibilitySubmitRequest) (projection.CompatibilitySubmitResponse, error) {
	if request.RequestID == "" {
		return projection.CompatibilitySubmitResponse{}, fmt.Errorf("compatibility submit send missing request_id")
	}
	if request.Filename == "" {
		return projection.CompatibilitySubmitResponse{}, fmt.Errorf("compatibility submit send missing filename")
	}
	if strings.ContainsAny(request.Filename, "/\\") {
		return projection.CompatibilitySubmitResponse{}, fmt.Errorf("compatibility submit send filename must not contain path separators")
	}
	if request.Content == "" {
		return projection.CompatibilitySubmitResponse{}, fmt.Errorf("compatibility submit send missing content")
	}
	postDir := filepath.Join(sessionDir, "post")
	if err := os.MkdirAll(postDir, 0o700); err != nil {
		return projection.CompatibilitySubmitResponse{}, fmt.Errorf("creating post directory: %w", err)
	}
	postPath := filepath.Join(postDir, request.Filename)
	if err := os.WriteFile(postPath, []byte(request.Content), 0o600); err != nil {
		return projection.CompatibilitySubmitResponse{}, fmt.Errorf("writing post message: %w", err)
	}
	return projection.CompatibilitySubmitResponse{
		RequestID: request.RequestID,
		Command:   request.Command,
		HandledAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  request.Filename,
	}, nil
}

func handleCompatibilitySubmitPop(sessionDir string, request projection.CompatibilitySubmitRequest) (projection.CompatibilitySubmitResponse, error) {
	if request.RequestID == "" {
		return projection.CompatibilitySubmitResponse{}, fmt.Errorf("compatibility submit pop missing request_id")
	}
	if request.Node == "" {
		return projection.CompatibilitySubmitResponse{}, fmt.Errorf("compatibility submit pop missing node")
	}
	inboxDir := filepath.Join(sessionDir, "inbox", request.Node)
	msgs := message.ScanInboxMessages(inboxDir)
	if len(msgs) == 0 {
		return projection.CompatibilitySubmitResponse{
			RequestID:    request.RequestID,
			Command:      request.Command,
			HandledAt:    time.Now().UTC().Format(time.RFC3339),
			Empty:        true,
			UnreadBefore: 0,
		}, nil
	}
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].Filename < msgs[j].Filename
	})

	abs := filepath.Join(inboxDir, msgs[0].Filename)
	data, err := os.ReadFile(abs)
	if err != nil {
		if !os.IsNotExist(err) {
			return projection.CompatibilitySubmitResponse{}, fmt.Errorf("reading pop message: %w", err)
		}
		msgs = message.ScanInboxMessages(inboxDir)
		if len(msgs) == 0 {
			return projection.CompatibilitySubmitResponse{
				RequestID:    request.RequestID,
				Command:      request.Command,
				HandledAt:    time.Now().UTC().Format(time.RFC3339),
				Empty:        true,
				UnreadBefore: 0,
			}, nil
		}
		sort.Slice(msgs, func(i, j int) bool {
			return msgs[i].Filename < msgs[j].Filename
		})
		abs = filepath.Join(inboxDir, msgs[0].Filename)
		data, err = os.ReadFile(abs)
		if err != nil {
			return projection.CompatibilitySubmitResponse{}, fmt.Errorf("reading pop message: %w", err)
		}
	}
	if _, err := message.ArchiveInboxMessage(abs, msgs[0].Filename); err != nil {
		return projection.CompatibilitySubmitResponse{}, err
	}
	return projection.CompatibilitySubmitResponse{
		RequestID:    request.RequestID,
		Command:      request.Command,
		HandledAt:    time.Now().UTC().Format(time.RFC3339),
		Filename:     msgs[0].Filename,
		Content:      string(data),
		UnreadBefore: len(msgs),
	}, nil
}

func processCompatibilitySubmitRequest(requestPath string) error {
	sessionDir, ok := compatibilitySubmitSessionDir(requestPath)
	if !ok {
		return nil
	}
	request, err := projection.ReadCompatibilitySubmitRequest(requestPath)
	if err != nil {
		return err
	}

	var response projection.CompatibilitySubmitResponse
	switch request.Command {
	case projection.CompatibilitySubmitSend:
		response, err = handleCompatibilitySubmitSend(sessionDir, request)
	case projection.CompatibilitySubmitPop:
		response, err = handleCompatibilitySubmitPop(sessionDir, request)
	default:
		err = fmt.Errorf("unsupported compatibility submit command %q", request.Command)
		response = projection.CompatibilitySubmitResponse{
			RequestID: request.RequestID,
			Command:   request.Command,
			HandledAt: time.Now().UTC().Format(time.RFC3339),
		}
	}
	if err != nil {
		response.Error = err.Error()
	}
	if _, writeErr := projection.WriteCompatibilitySubmitResponse(sessionDir, response); writeErr != nil {
		return writeErr
	}
	if removeErr := os.Remove(requestPath); removeErr != nil && !os.IsNotExist(removeErr) {
		log.Printf("postman: WARNING: failed to remove compatibility submit request %s: %v\n", requestPath, removeErr)
	}
	return nil
}

func waitingFileContentForRead(info *message.MessageInfo, messageContent []byte, cfg *config.Config, now time.Time) (string, bool) {
	waitingSince := now.UTC().Format(time.RFC3339)
	threadLine := ""
	if metadata, err := message.ParseEnvelopeMetadata(string(messageContent)); err == nil && metadata.ThreadID != "" {
		threadLine = "thread_id: " + metadata.ThreadID + "\n"
	}
	if cfg != nil && cfg.UINode != "" && info.To == cfg.UINode {
		return fmt.Sprintf(
			"---\nfrom: %s\nto: %s\n%swaiting_since: %s\nstate: user_input\nstate_updated_at: %s\nexpects_reply: false\n---\n",
			info.From, info.To, threadLine, waitingSince, waitingSince,
		), true
	}
	if !frontmatterBool(string(messageContent), "expects_reply") {
		return "", false
	}
	return fmt.Sprintf(
		"---\nfrom: %s\nto: %s\n%swaiting_since: %s\nstate: composing\nstate_updated_at: %s\nexpects_reply: true\n---\n",
		info.From, info.To, threadLine, waitingSince, waitingSince,
	), true
}

func waitingSinceFromContent(content string) time.Time {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "waiting_since: ") {
			ts := strings.TrimPrefix(line, "waiting_since: ")
			if t, err := time.Parse(time.RFC3339, strings.TrimSpace(ts)); err == nil {
				return t
			}
			return time.Time{}
		}
	}
	return time.Time{}
}

func advanceWaitingState(content, paneState string, now time.Time, idleThreshold, spinningThreshold time.Duration, spinningEnabled bool) (string, bool) {
	if !frontmatterBool(content, "expects_reply") {
		return content, false
	}

	isComposing := strings.Contains(content, "state: composing")
	isSpinning := strings.Contains(content, "state: spinning")
	if !isComposing && !isSpinning {
		return content, false
	}

	if isSpinning {
		if paneState == "stale" {
			return replaceWaitingState(content, "spinning", "stalled"), true
		}
		return content, false
	}

	waitingSince := waitingSinceFromContent(content)
	if waitingSince.IsZero() {
		return content, false
	}
	if now.Sub(waitingSince) <= idleThreshold {
		return content, false
	}
	if paneState == "stale" {
		return replaceWaitingState(content, "composing", "stalled"), true
	}
	if spinningEnabled && now.Sub(waitingSince) > spinningThreshold && paneState == "active" {
		return replaceWaitingState(content, "composing", "spinning"), true
	}
	return content, false
}

func visibleWaitingState(content string) string {
	if strings.Contains(content, "state: user_input") {
		return "user_input"
	}
	if !frontmatterBool(content, "expects_reply") {
		return ""
	}
	switch {
	case strings.Contains(content, "state: stalled"), strings.Contains(content, "state: stuck"):
		return "stalled"
	case strings.Contains(content, "state: spinning"):
		return "spinning"
	case strings.Contains(content, "state: composing"):
		return "composing"
	default:
		return ""
	}
}

func sendSpinningAlertForWaitingFile(sessionDir, contextID, waitingFilename, waitingContent string, now time.Time, spinningThreshold time.Duration, cfg *config.Config, adjacency map[string][]string, nodes map[string]discovery.NodeInfo) error {
	if cfg == nil || cfg.UINode == "" || cfg.SpinningAlertTemplate == "" {
		return nil
	}

	requester := frontmatterValue(waitingContent, "from")
	awaitedNode := frontmatterValue(waitingContent, "to")
	waitingSince := waitingSinceFromContent(waitingContent)
	if requester == "" || awaitedNode == "" || waitingSince.IsZero() {
		return nil
	}

	age := now.Sub(waitingSince).Round(time.Second)
	threshold := spinningThreshold.Round(time.Second)
	alertVars := map[string]string{
		"node":               awaitedNode,
		"from":               requester,
		"requester":          requester,
		"to":                 awaitedNode,
		"awaited_node":       awaitedNode,
		"context_id":         contextID,
		"spinning_duration":  age.String(),
		"age":                age.String(),
		"threshold":          threshold.String(),
		"message_id":         waitingFilename,
		"message_identifier": waitingFilename,
	}
	body := template.ExpandVariables(cfg.SpinningAlertTemplate, alertVars)
	return sendAlertToUINode(sessionDir, contextID, cfg.UINode, body, "spinning", cfg, adjacency, nodes)
}

func sendStalledAlertForWaitingFile(sessionDir, contextID, waitingFilename, previousState, waitingContent string, now time.Time, idleThreshold time.Duration, cfg *config.Config, adjacency map[string][]string, nodes map[string]discovery.NodeInfo) error {
	if cfg == nil || cfg.UINode == "" || cfg.StalledAlertTemplate == "" {
		return nil
	}

	requester := frontmatterValue(waitingContent, "from")
	awaitedNode := frontmatterValue(waitingContent, "to")
	waitingSince := waitingSinceFromContent(waitingContent)
	if requester == "" || awaitedNode == "" || waitingSince.IsZero() {
		return nil
	}

	age := now.Sub(waitingSince).Round(time.Second)
	threshold := idleThreshold.Round(time.Second)
	alertVars := map[string]string{
		"node":               awaitedNode,
		"from":               requester,
		"requester":          requester,
		"to":                 awaitedNode,
		"awaited_node":       awaitedNode,
		"context_id":         contextID,
		"previous_state":     previousState,
		"stalled_duration":   age.String(),
		"age":                age.String(),
		"threshold":          threshold.String(),
		"message_id":         waitingFilename,
		"message_identifier": waitingFilename,
	}
	body := template.ExpandVariables(cfg.StalledAlertTemplate, alertVars)
	return sendAlertToUINode(sessionDir, contextID, cfg.UINode, body, "stalled", cfg, adjacency, nodes)
}

// EdgeActivity tracks communication timestamps for an edge (Issue #37).
type EdgeActivity struct {
	LastForwardAt  time.Time // A -> B last communication time
	LastBackwardAt time.Time // B -> A last communication time
}

// DaemonState manages daemon state (Issue #71).
type DaemonState struct {
	contextID                     string        // This daemon's contextID (for tmux option writes)
	startedAt                     time.Time     // Daemon start timestamp (#217)
	drainWindow                   time.Duration // Startup drain window duration (#217)
	edgeHistory                   map[string]EdgeActivity
	edgeHistoryMu                 sync.RWMutex
	enabledSessions               map[string]bool
	enabledSessionsMu             sync.RWMutex
	prevPaneStates                map[string]uinode.PaneInfo // Issue #98: Track previous pane states for restart detection
	prevPaneStatesMu              sync.RWMutex               // Issue #98: Mutex for prevPaneStates
	prevPaneToNode                map[string]string          // Track previous pane ID -> node key mapping for restart detection
	lastAlertTimestamp            map[string]time.Time       // Issue #118: Track last alert timestamps (alertKey -> time)
	lastAlertTimestampMu          sync.RWMutex               // Issue #118: Mutex for lastAlertTimestamp
	lastInboxUnreadCount          map[string]int             // Issue #264: per-node last alerted inbox count
	lastInboxUnreadCountMu        sync.RWMutex               // Issue #264
	lastDeliveryBySenderRecipient map[string]time.Time       // Issue #211: Rate limit duplicate deliveries (sender:recipient -> time)
	lastDeliveryMu                sync.RWMutex               // Issue #211: Mutex for lastDeliveryBySenderRecipient
	alertedReadFiles              map[string]struct{}        // Paths of read/ files already alerted (suppress repeats)
	alertedReadFilesMu            sync.Mutex                 // Mutex for alertedReadFiles
	swallowedRetryCount           map[string]int             // Issue #282: inbox file path -> re-delivery attempt count
	swallowedRetryCountMu         sync.Mutex                 // Issue #282
}

// NewDaemonState creates a new DaemonState instance (Issue #71).
// drainWindowSeconds configures the startup drain window during which
// IsSessionEnabled returns true for all sessions (#217).
func NewDaemonState(drainWindowSeconds float64, contextID string) *DaemonState {
	return &DaemonState{
		contextID:                     contextID,
		startedAt:                     time.Now(),
		drainWindow:                   time.Duration(drainWindowSeconds * float64(time.Second)),
		edgeHistory:                   make(map[string]EdgeActivity),
		enabledSessions:               make(map[string]bool),
		prevPaneStates:                make(map[string]uinode.PaneInfo), // Issue #98
		prevPaneToNode:                make(map[string]string),          // paneID -> nodeKey mapping
		lastAlertTimestamp:            make(map[string]time.Time),       // Issue #118
		lastDeliveryBySenderRecipient: make(map[string]time.Time),       // Issue #211
		lastInboxUnreadCount:          make(map[string]int),             // Issue #264
		alertedReadFiles:              make(map[string]struct{}),
		swallowedRetryCount:           make(map[string]int),
	}
}

// makeEdgeKey generates a sorted edge key for consistent lookups (Issue #37).
func makeEdgeKey(nodeA, nodeB string) string {
	nodes := []string{nodeA, nodeB}
	sort.Strings(nodes)
	return nodes[0] + ":" + nodes[1]
}

// RecordEdgeActivity records edge communication activity (Issue #37, #71).
func (ds *DaemonState) RecordEdgeActivity(from, to string, timestamp time.Time) {
	ds.edgeHistoryMu.Lock()
	defer ds.edgeHistoryMu.Unlock()

	key := makeEdgeKey(from, to)
	activity := ds.edgeHistory[key]

	// Determine direction: sort nodes and check if from is first
	nodes := []string{from, to}
	sort.Strings(nodes)
	if from == nodes[0] {
		activity.LastForwardAt = timestamp
	} else {
		activity.LastBackwardAt = timestamp
	}

	ds.edgeHistory[key] = activity
}

// ClearEdgeHistory clears all edge activity history (called on session switch).
func (ds *DaemonState) ClearEdgeHistory() {
	ds.edgeHistoryMu.Lock()
	defer ds.edgeHistoryMu.Unlock()
	ds.edgeHistory = make(map[string]EdgeActivity)
}

// BuildEdgeList builds edge list with activity data (Issue #37, #42, #71).
func (ds *DaemonState) BuildEdgeList(edges []string, cfg *config.Config) []tui.Edge {
	ds.edgeHistoryMu.RLock()
	defer ds.edgeHistoryMu.RUnlock()

	now := time.Now()
	activityWindow := time.Duration(cfg.EdgeActivitySeconds * float64(time.Second))

	edgeList := make([]tui.Edge, len(edges))
	for i, e := range edges {
		// Issue #42: Parse chain edge into node segments
		nodes := tui.ParseEdgeNodes(e)

		// Calculate direction for each segment
		var segmentDirections []string
		var lastActivityAt time.Time
		isActive := false

		// Process each adjacent pair
		for j := 0; j < len(nodes)-1; j++ {
			nodeA := nodes[j]
			nodeB := nodes[j+1]

			key := makeEdgeKey(nodeA, nodeB)
			activity, exists := ds.edgeHistory[key]

			segmentDir := "none"
			if exists && nodeA != "" && nodeB != "" {
				// Check if each direction is active (within activity window)
				forwardActive := !activity.LastForwardAt.IsZero() && now.Sub(activity.LastForwardAt) <= activityWindow
				backwardActive := !activity.LastBackwardAt.IsZero() && now.Sub(activity.LastBackwardAt) <= activityWindow

				// Update global last activity time
				if activity.LastForwardAt.After(lastActivityAt) {
					lastActivityAt = activity.LastForwardAt
				}
				if activity.LastBackwardAt.After(lastActivityAt) {
					lastActivityAt = activity.LastBackwardAt
				}

				// Determine segment direction
				// NOTE: forward/backward in edgeHistory are based on sorted node order:
				//   forward = sorted[0] -> sorted[1]
				//   backward = sorted[1] -> sorted[0]
				// We need to map this to nodeA->nodeB direction based on edge definition order.
				sortedNodes := []string{nodeA, nodeB}
				sort.Strings(sortedNodes)

				var nodeAtoB, nodeBtoA bool
				if nodeA == sortedNodes[0] {
					// nodeA is sorted[0], nodeB is sorted[1]
					nodeAtoB = forwardActive  // sorted[0]->sorted[1] = nodeA->nodeB
					nodeBtoA = backwardActive // sorted[1]->sorted[0] = nodeB->nodeA
				} else {
					// nodeA is sorted[1], nodeB is sorted[0]
					nodeAtoB = backwardActive // sorted[1]->sorted[0] = nodeA->nodeB
					nodeBtoA = forwardActive  // sorted[0]->sorted[1] = nodeB->nodeA
				}

				switch {
				case nodeAtoB && nodeBtoA:
					segmentDir = "bidirectional"
					isActive = true
				case nodeAtoB:
					segmentDir = "forward"
					isActive = true
				case nodeBtoA:
					segmentDir = "backward"
					isActive = true
				}
			}

			segmentDirections = append(segmentDirections, segmentDir)
		}

		// For backward compatibility, set Direction to first segment direction
		direction := "none"
		if len(segmentDirections) > 0 {
			direction = segmentDirections[0]
		}

		edgeList[i] = tui.Edge{
			Raw:               e,
			LastActivityAt:    lastActivityAt,
			IsActive:          isActive,
			Direction:         direction,
			SegmentDirections: segmentDirections,
		}
	}

	return edgeList
}

// filterNodesByEdges removes nodes from the map whose raw name (after session prefix)
// is not listed in the configured edges. Modifies the map in place.
func filterNodesByEdges(nodes map[string]discovery.NodeInfo, edges []string) {
	allowed := config.GetEdgeNodeNames(edges)
	for nodeName := range nodes {
		parts := strings.SplitN(nodeName, ":", 2)
		rawName := parts[len(parts)-1]
		if !allowed[nodeName] && !allowed[rawName] {
			delete(nodes, nodeName)
		}
	}
}

// mergePhonyNodes inserts phony NodeInfo entries from registry into nodes.
// Keys stay bare node names so bare phony recipients and session-prefixed
// phony aliases can both resolve back to the same binding-backed node (#306).
func mergePhonyNodes(nodes map[string]discovery.NodeInfo, registry *binding.BindingRegistry) {
	if registry == nil {
		return
	}
	for _, b := range registry.Bindings {
		nodes[b.NodeName] = discovery.NodeInfo{IsPhony: true}
	}
}

func bindingsWatchDir(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Dir(path)
}

func ensureWatchedPath(watchedPaths []string, path string, add func(string) error) ([]string, error) {
	if path == "" {
		return watchedPaths, nil
	}
	for _, watchedPath := range watchedPaths {
		if watchedPath == path {
			return watchedPaths, nil
		}
	}
	if err := add(path); err != nil {
		return watchedPaths, err
	}
	return append(watchedPaths, path), nil
}

func refreshNodesWithRegistry(nodes map[string]discovery.NodeInfo, registry *binding.BindingRegistry) map[string]discovery.NodeInfo {
	realNodes := make(map[string]discovery.NodeInfo)
	for nodeName, nodeInfo := range nodes {
		if nodeInfo.IsPhony {
			continue
		}
		realNodes[nodeName] = nodeInfo
	}
	mergePhonyNodes(realNodes, registry)
	return realNodes
}

func matchesBindingsEvent(eventPath, bindingsPath string) bool {
	if eventPath == "" || bindingsPath == "" {
		return false
	}
	watchDir := bindingsWatchDir(bindingsPath)
	if watchDir == "" {
		return false
	}
	if filepath.Clean(filepath.Dir(eventPath)) != filepath.Clean(watchDir) {
		return false
	}
	targetBase := filepath.Base(bindingsPath)
	eventBase := filepath.Base(eventPath)
	return eventBase == targetBase || eventBase == targetBase+".tmp"
}

// RunDaemonLoop runs the daemon event loop in a goroutine (Issue #71).
func RunDaemonLoop(
	ctx context.Context,
	baseDir string,
	sessionDir string,
	contextID string,
	cfg *config.Config,
	watcher *fsnotify.Watcher,
	adjacency map[string][]string,
	nodes map[string]discovery.NodeInfo,
	knownNodes map[string]bool,
	reminderState *reminder.ReminderState,
	events chan<- tui.DaemonEvent,
	configPath string,
	configPaths []string,
	nodesDirs []string,
	daemonState *DaemonState,
	idleTracker *idle.IdleTracker,
	alertRateLimiter *alert.AlertRateLimiter,
	sharedNodes *atomic.Pointer[map[string]discovery.NodeInfo],
	selfSession string,
) {
	// NOTE: Do not close(events) here. The channel is shared by multiple goroutines
	// (UI pane monitoring, TUI commands handler, daemon loop). Closing it would cause
	// "send on closed channel" panics. Let the channel be garbage collected when all
	// goroutines exit.
	runtime := newDaemonRuntime(
		baseDir,
		sessionDir,
		contextID,
		cfg,
		watcher,
		adjacency,
		nodes,
		knownNodes,
		reminderState,
		events,
		configPath,
		configPaths,
		nodesDirs,
		daemonState,
		idleTracker,
		alertRateLimiter,
		sharedNodes,
		selfSession,
	)

	scanTicker := time.NewTicker(time.Duration(cfg.ScanInterval * float64(time.Second)))
	defer scanTicker.Stop()
	inboxCheckTicker := time.NewTicker(inboxCheckInterval)
	defer inboxCheckTicker.Stop()

	runtime.bootstrap(ctx)

	for {
		select {
		case <-ctx.Done():
			runtime.handleContextDone()
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			runtime.handleWatcherEvent(event)
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			runtime.handleWatcherError(err)
		case <-scanTicker.C:
			runtime.handleScanTick()
		case <-inboxCheckTicker.C:
			runtime.handleInboxCheckTick()
		}
	}
}

// sendAlertToUINode sends an alert message to the ui_node inbox.
// Writes directly to post/ so the daemon delivery loop routes and notifies normally.
// Uses DaemonMessageTemplate with two-pass expansion (BuildEnvelope + Pass 2).
func sendAlertToUINode(sessionDir, contextID, uiNode, body, alertType string, cfg *config.Config, adjacency map[string][]string, nodes map[string]discovery.NodeInfo) error {
	tmpl := cfg.DaemonMessageTemplate
	if tmpl == "" {
		return nil // no template configured; silent no-op
	}
	sourceSessionName := filepath.Base(filepath.Dir(sessionDir))
	now := time.Now()
	ts := fmt.Sprintf("%s-%d", now.Format("20060102-150405"), now.UnixNano()%1000000)
	filename := fmt.Sprintf("%s-from-daemon-to-%s.md", ts, uiNode)
	postPath := filepath.Join(sessionDir, "post", filename)
	scaffolded := envelope.BuildDaemonEnvelope(
		cfg, tmpl, uiNode, "daemon",
		contextID, postPath,
		nil, adjacency, nodes, sourceSessionName,
		nil,
	)
	content := template.ExpandVariables(scaffolded, map[string]string{
		"message_type": "alert",
		"heading":      "Alert: " + alertType,
		"alert_type":   alertType,
		"message":      body,
		"role_content": envelope.BuildRoleContent(cfg, uiNode),
	})
	return os.WriteFile(postPath, []byte(content), 0o600)
}

// collectPendingStates scans inbox/ directories for unarchived messages
// and returns a map of sessionName:nodeName -> "pending" for nodes with messages
// waiting in their inbox. Only applies when the node has no worse waiting-file state.
func collectPendingStates(nodes map[string]discovery.NodeInfo, priority map[string]int) map[string]string {
	result := make(map[string]string)
	for nodeKey, nodeInfo := range nodes {
		parts := strings.SplitN(nodeKey, ":", 2)
		if len(parts) != 2 {
			continue
		}
		nodeName := parts[1]
		inboxDir := filepath.Join(nodeInfo.SessionDir, "inbox", nodeName)
		entries, err := os.ReadDir(inboxDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".md") {
				if priority["pending"] >= priority[result[nodeKey]] {
					result[nodeKey] = "pending"
				}
				break
			}
		}
	}
	return result
}

func uiNodeDiscovered(nodes map[string]discovery.NodeInfo, uiNode string) bool {
	if uiNode == "" {
		return false
	}
	for nodeName := range nodes {
		parts := strings.SplitN(nodeName, ":", 2)
		if parts[len(parts)-1] == uiNode {
			return true
		}
	}
	return false
}

func hasActiveAlertTimeouts(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	if cfg.NodeDefaults.IdleTimeoutSeconds > 0 || cfg.NodeDefaults.DroppedBallTimeoutSeconds > 0 {
		return true
	}
	for _, node := range cfg.Nodes {
		if node.IdleTimeoutSeconds > 0 || node.DroppedBallTimeoutSeconds > 0 {
			return true
		}
	}
	return false
}

func alertDeliveryStatus(cfg *config.Config, nodes map[string]discovery.NodeInfo) (string, string) {
	if cfg == nil || cfg.UINode == "" {
		return "", ""
	}
	if cfg.UINode != "" && !uiNodeDiscovered(nodes, cfg.UINode) {
		return "degraded:ui_node_missing", fmt.Sprintf(
			"postman: WARNING: alert delivery degraded: ui_node %q is not discoverable in this session. Alerts routed to %q may dead-letter until that pane is present.",
			cfg.UINode, cfg.UINode,
		)
	}
	if !hasActiveAlertTimeouts(cfg) {
		return "degraded:thresholds_disabled", "postman: WARNING: alert delivery degraded: no nodes have " +
			"idle_timeout_seconds or dropped_ball_timeout_seconds set. " +
			"Node-inactivity and unreplied-message alerts will not fire."
	}
	return "healthy", fmt.Sprintf(
		"postman: INFO: alert delivery recovered: ui_node %q is discoverable and per-node alert thresholds are active.",
		cfg.UINode,
	)
}

func syncAlertDeliveryStatus(prev string, cfg *config.Config, nodes map[string]discovery.NodeInfo, events chan<- tui.DaemonEvent) string {
	current, msg := alertDeliveryStatus(cfg, nodes)
	if current == "" {
		return ""
	}
	if strings.HasPrefix(current, "degraded:") {
		if current == prev {
			return current
		}
		log.Print(msg)
		events <- tui.DaemonEvent{Type: "alert_delivery_degraded", Message: msg}
		return current
	}
	if current == "healthy" {
		if strings.HasPrefix(prev, "degraded:") {
			log.Print(msg)
			events <- tui.DaemonEvent{Type: "alert_delivery_recovered", Message: msg}
		}
		return current
	}
	return current
}

// warnAlertConfig emits the initial alert-delivery degradation signal at startup.
// This is observability-only: no behavior is changed.
func warnAlertConfig(cfg *config.Config, nodes map[string]discovery.NodeInfo, events chan<- tui.DaemonEvent) string {
	return syncAlertDeliveryStatus("", cfg, nodes, events)
}

// checkInboxStagnation checks inbox unread count for all nodes and sends an alert to
// cfg.UINode when the count reaches cfg.InboxUnreadThreshold.
// Only the inbox_unread_summary count-based path is restored here (design doc #245).
// Three guards are enforced:
//   - Guard 1: alertRateLimiter.Allow — per-recipient cooldown
//   - Guard 2: idleTracker.GetLastReceived — suppress if UINode received recently
//   - Guard 3: count-based signal (distinct from stagnation / node_inactivity)
func checkInboxStagnation(nodes map[string]discovery.NodeInfo, cfg *config.Config, events chan<- tui.DaemonEvent, sessionDir, contextID string, adjacency map[string][]string, idleTracker *idle.IdleTracker, alertRateLimiter *alert.AlertRateLimiter, ds *DaemonState) {
	if cfg.UINode == "" || cfg.InboxUnreadThreshold <= 0 {
		return
	}

	now := time.Now()

	for nodeKey, nodeInfo := range nodes {
		simpleName := nodeKey
		if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
			simpleName = parts[1]
		}

		// Do not alert about UINode's own inbox here
		if simpleName == cfg.UINode {
			continue
		}

		inboxPath := filepath.Join(nodeInfo.SessionDir, "inbox", simpleName)
		entries, err := os.ReadDir(inboxPath)
		if err != nil {
			continue
		}

		inboxCount := 0
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
				inboxCount++
			}
		}
		if inboxCount < cfg.InboxUnreadThreshold {
			ds.lastInboxUnreadCountMu.Lock()
			delete(ds.lastInboxUnreadCount, simpleName)
			ds.lastInboxUnreadCountMu.Unlock()
			continue
		}
		ds.lastInboxUnreadCountMu.RLock()
		lastCount := ds.lastInboxUnreadCount[simpleName]
		ds.lastInboxUnreadCountMu.RUnlock()
		if inboxCount <= lastCount {
			continue
		}
		ds.lastInboxUnreadCountMu.Lock()
		ds.lastInboxUnreadCount[simpleName] = inboxCount
		ds.lastInboxUnreadCountMu.Unlock()

		// Send TUI event unconditionally (no rate limit for TUI display)
		alertVars := map[string]string{
			"node":      simpleName,
			"count":     fmt.Sprintf("%d", inboxCount),
			"threshold": fmt.Sprintf("%d", cfg.InboxUnreadThreshold),
		}
		msg := template.ExpandVariables(cfg.InboxUnreadSummaryAlertTemplate, alertVars)
		events <- tui.DaemonEvent{
			Type:    "inbox_unread_summary",
			Message: msg,
			Details: map[string]interface{}{
				"node":      simpleName,
				"count":     inboxCount,
				"threshold": cfg.InboxUnreadThreshold,
			},
		}

		// Guard 1: per-recipient cooldown
		if !alertRateLimiter.Allow(cfg.UINode, now) {
			continue
		}

		// Guard 2: suppress if UINode received a message recently
		// Use session-prefixed key matching UpdateReceiveActivity convention
		uiNodeFullKey := nodeInfo.SessionName + ":" + cfg.UINode
		deliveryWindow := time.Duration(cfg.AlertDeliveryWindowSeconds) * time.Second
		if deliveryWindow > 0 && time.Since(idleTracker.GetLastReceived(uiNodeFullKey)) < deliveryWindow {
			continue
		}

		// Build action text
		var replyCmd string
		if cfg.ReplyCommand != "" {
			replyCmd = envelope.RenderReplyCommand(cfg.ReplyCommand, contextID, simpleName)
			replyCmd = strings.ReplaceAll(replyCmd, "<recipient>", simpleName)
		} else {
			replyCmd = fmt.Sprintf(
				"nix run github:i9wa4/tmux-a2a-postman -- send --to %s --body \"<your reply>\"",
				simpleName,
			)
		}
		canReach := false
		for _, neighbor := range adjacency[cfg.UINode] {
			if neighbor == simpleName {
				canReach = true
				break
			}
		}
		actionVars := map[string]string{
			"node":          simpleName,
			"reply_command": replyCmd,
		}
		var actionText string
		if canReach && cfg.AlertActionReachableTemplate != "" {
			actionText = template.ExpandVariables(cfg.AlertActionReachableTemplate, actionVars)
		} else if !canReach && cfg.AlertActionUnreachableTemplate != "" {
			actionText = template.ExpandVariables(cfg.AlertActionUnreachableTemplate, actionVars)
		}

		if err := sendAlertToUINode(sessionDir, contextID, cfg.UINode, msg+actionText, "inbox_unread_summary", cfg, adjacency, nodes); err == nil {
			alertRateLimiter.Record(cfg.UINode, now)
		}
	}
}

// scanLiveInboxCounts returns the current .md file count per node from the
// inbox filesystem, keyed by session-prefixed node key (e.g. "session:worker").
// Used to update the TUI unread inbox depth display with live data (Issue #283).
func scanLiveInboxCounts(nodes map[string]discovery.NodeInfo) map[string]int {
	counts := make(map[string]int, len(nodes))
	for nodeKey, nodeInfo := range nodes {
		simpleName := nodeKey
		if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
			simpleName = parts[1]
		}
		inboxPath := filepath.Join(nodeInfo.SessionDir, "inbox", simpleName)
		entries, err := os.ReadDir(inboxPath)
		if err != nil {
			counts[nodeKey] = 0
			continue
		}
		n := 0
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
				n++
			}
		}
		counts[nodeKey] = n
	}
	return counts
}

// checkNodeInactivity alerts UINode when a monitored node has been inactive
// (no send + no receive) for longer than its configured IdleTimeoutSeconds.
// Three guards: TUI event (unconditional), Guard 1 (rate limiter), Guard 2 (delivery window).
// Guard 3 (signal): excludes nodes with state:user_input waiting files.
func checkNodeInactivity(nodes map[string]discovery.NodeInfo, cfg *config.Config, events chan<- tui.DaemonEvent, sessionDir, contextID string, adjacency map[string][]string, idleTracker *idle.IdleTracker, alertRateLimiter *alert.AlertRateLimiter) {
	if cfg.UINode == "" || cfg.NodeInactivityAlertTemplate == "" {
		return
	}

	now := time.Now()

	for nodeKey, nodeInfo := range nodes {
		simpleName := nodeKey
		if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
			simpleName = parts[1]
		}

		if simpleName == cfg.UINode {
			continue
		}

		nodeConfig, ok := cfg.Nodes[simpleName]
		if !ok || nodeConfig.IdleTimeoutSeconds <= 0 {
			continue
		}

		// Exclude nodes with state:user_input waiting files
		waitingDir := filepath.Join(nodeInfo.SessionDir, "waiting")
		waitingEntries, err := os.ReadDir(waitingDir)
		if err == nil {
			userInputFound := false
			for _, entry := range waitingEntries {
				if !strings.HasSuffix(entry.Name(), ".md") {
					continue
				}
				filePath := filepath.Join(waitingDir, entry.Name())
				fileContent, readErr := os.ReadFile(filePath)
				if readErr != nil {
					continue
				}
				contentStr := string(fileContent)
				if strings.Contains(contentStr, "state: user_input") {
					if fi, fiErr := message.ParseMessageFilename(entry.Name()); fiErr == nil && fi.From == simpleName {
						userInputFound = true
						break
					}
				}
			}
			if userInputFound {
				continue
			}
		}

		nodeStates := idleTracker.GetNodeStates()
		activity, actOk := nodeStates[nodeKey]
		if !actOk {
			continue
		}
		lastAct := activity.LastSent
		if activity.LastReceived.After(lastAct) {
			lastAct = activity.LastReceived
		}
		if lastAct.IsZero() {
			continue
		}
		idleDuration := time.Since(lastAct)
		threshold := time.Duration(nodeConfig.IdleTimeoutSeconds * float64(time.Second))
		if idleDuration < threshold {
			continue
		}

		alertVars := map[string]string{
			"node":     simpleName,
			"duration": idleDuration.Round(time.Second).String(),
		}
		msg := template.ExpandVariables(cfg.NodeInactivityAlertTemplate, alertVars)
		events <- tui.DaemonEvent{
			Type:    "node_inactivity",
			Message: msg,
			Details: map[string]interface{}{
				"node":     simpleName,
				"duration": idleDuration.String(),
			},
		}

		// Guard 1: per-recipient cooldown
		if !alertRateLimiter.Allow(cfg.UINode, now) {
			continue
		}

		// Guard 2: suppress if UINode received a message recently
		uiNodeFullKey := nodeInfo.SessionName + ":" + cfg.UINode
		deliveryWindow := time.Duration(cfg.AlertDeliveryWindowSeconds) * time.Second
		if deliveryWindow > 0 && time.Since(idleTracker.GetLastReceived(uiNodeFullKey)) < deliveryWindow {
			continue
		}

		var replyCmd string
		if cfg.ReplyCommand != "" {
			replyCmd = envelope.RenderReplyCommand(cfg.ReplyCommand, contextID, simpleName)
			replyCmd = strings.ReplaceAll(replyCmd, "<recipient>", simpleName)
		} else {
			replyCmd = fmt.Sprintf(
				"nix run github:i9wa4/tmux-a2a-postman -- send --to %s --body \"<your reply>\"",
				simpleName,
			)
		}
		canReach := false
		for _, neighbor := range adjacency[cfg.UINode] {
			if neighbor == simpleName {
				canReach = true
				break
			}
		}
		actionVars := map[string]string{
			"node":          simpleName,
			"reply_command": replyCmd,
		}
		var actionText string
		if canReach && cfg.AlertActionReachableTemplate != "" {
			actionText = template.ExpandVariables(cfg.AlertActionReachableTemplate, actionVars)
		} else if !canReach && cfg.AlertActionUnreachableTemplate != "" {
			actionText = template.ExpandVariables(cfg.AlertActionUnreachableTemplate, actionVars)
		}

		if err := sendAlertToUINode(sessionDir, contextID, cfg.UINode, msg+actionText, "node_inactivity", cfg, adjacency, nodes); err == nil {
			alertRateLimiter.Record(cfg.UINode, now)
		}
	}
}

// checkUnrepliedMessages alerts UINode when a monitored node has messages in
// read/ that are older than DroppedBallTimeoutSeconds without a reply.
// Excludes daemon-generated messages (From == "postman" in filename).
// Three guards: TUI event (unconditional), Guard 1 (rate limiter), Guard 2 (delivery window).
func checkUnrepliedMessages(nodes map[string]discovery.NodeInfo, cfg *config.Config, events chan<- tui.DaemonEvent, sessionDir, contextID string, adjacency map[string][]string, idleTracker *idle.IdleTracker, alertRateLimiter *alert.AlertRateLimiter, daemonState *DaemonState) {
	if cfg.UINode == "" || cfg.UnrepliedMessageAlertTemplate == "" {
		return
	}

	now := time.Now()

	for nodeKey, nodeInfo := range nodes {
		simpleName := nodeKey
		if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
			simpleName = parts[1]
		}

		if simpleName == cfg.UINode {
			continue
		}

		nodeConfig, ok := cfg.Nodes[simpleName]
		if !ok || nodeConfig.DroppedBallTimeoutSeconds <= 0 {
			continue
		}

		readDir := filepath.Join(nodeInfo.SessionDir, "read")
		entries, err := os.ReadDir(readDir)
		if err != nil {
			continue
		}

		unrepliedCount := 0
		var (
			oldestFrom          string
			oldestTimeSinceRead time.Duration
			newAlertPaths       []string
		)
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			fileInfo, parseErr := message.ParseMessageFilename(entry.Name())
			if parseErr != nil {
				continue
			}
			if fileInfo.From == "postman" {
				continue
			}
			if fileInfo.To != simpleName {
				continue
			}
			entryInfo, infoErr := entry.Info()
			if infoErr != nil {
				continue
			}
			absPath := filepath.Join(readDir, entry.Name())
			daemonState.alertedReadFilesMu.Lock()
			_, alreadyAlerted := daemonState.alertedReadFiles[absPath]
			daemonState.alertedReadFilesMu.Unlock()
			if alreadyAlerted {
				continue
			}
			if time.Since(entryInfo.ModTime()) >= time.Duration(nodeConfig.DroppedBallTimeoutSeconds)*time.Second {
				unrepliedCount++
				age := time.Since(entryInfo.ModTime())
				if unrepliedCount == 1 || age > oldestTimeSinceRead {
					oldestTimeSinceRead = age
					oldestFrom = fileInfo.From
				}
				newAlertPaths = append(newAlertPaths, absPath)
			}
		}
		if unrepliedCount == 0 {
			continue
		}

		alertVars := map[string]string{
			"node":            simpleName,
			"count":           fmt.Sprintf("%d", unrepliedCount),
			"time_since_read": oldestTimeSinceRead.Round(time.Second).String(),
			"from":            oldestFrom,
			"threshold":       fmt.Sprintf("%d", nodeConfig.DroppedBallTimeoutSeconds),
		}
		msg := template.ExpandVariables(cfg.UnrepliedMessageAlertTemplate, alertVars)
		events <- tui.DaemonEvent{
			Type:    "unreplied_message",
			Message: msg,
			Details: map[string]interface{}{
				"node":  simpleName,
				"count": unrepliedCount,
			},
		}
		// Record newly alerted files to suppress future repeats.
		daemonState.alertedReadFilesMu.Lock()
		for _, p := range newAlertPaths {
			daemonState.alertedReadFiles[p] = struct{}{}
		}
		daemonState.alertedReadFilesMu.Unlock()

		// Guard 1: per-recipient cooldown
		if !alertRateLimiter.Allow(cfg.UINode, now) {
			continue
		}

		// Guard 2: suppress if UINode received a message recently
		uiNodeFullKey := nodeInfo.SessionName + ":" + cfg.UINode
		deliveryWindow := time.Duration(cfg.AlertDeliveryWindowSeconds) * time.Second
		if deliveryWindow > 0 && time.Since(idleTracker.GetLastReceived(uiNodeFullKey)) < deliveryWindow {
			continue
		}

		var replyCmd string
		if cfg.ReplyCommand != "" {
			replyCmd = envelope.RenderReplyCommand(cfg.ReplyCommand, contextID, simpleName)
			replyCmd = strings.ReplaceAll(replyCmd, "<recipient>", simpleName)
		} else {
			replyCmd = fmt.Sprintf(
				"nix run github:i9wa4/tmux-a2a-postman -- send --to %s --body \"<your reply>\"",
				simpleName,
			)
		}
		canReach := false
		for _, neighbor := range adjacency[cfg.UINode] {
			if neighbor == simpleName {
				canReach = true
				break
			}
		}
		actionVars := map[string]string{
			"node":          simpleName,
			"reply_command": replyCmd,
		}
		var actionText string
		if canReach && cfg.AlertActionReachableTemplate != "" {
			actionText = template.ExpandVariables(cfg.AlertActionReachableTemplate, actionVars)
		} else if !canReach && cfg.AlertActionUnreachableTemplate != "" {
			actionText = template.ExpandVariables(cfg.AlertActionUnreachableTemplate, actionVars)
		}

		if err := sendAlertToUINode(sessionDir, contextID, cfg.UINode, msg+actionText, "unreplied_message", cfg, adjacency, nodes); err == nil {
			alertRateLimiter.Record(cfg.UINode, now)
		}
	}
}

// checkSwallowedMessages detects inbox messages likely swallowed by a busy agent pane
// and re-delivers the notification. Detection: inbox file older than delivery_idle_timeout_seconds
// AND pane idle AND node has not sent since file landed in inbox. Issue #282.
func checkSwallowedMessages(
	nodes map[string]discovery.NodeInfo,
	cfg *config.Config,
	events chan<- tui.DaemonEvent,
	contextID string,
	adjacency map[string][]string,
	idleTracker *idle.IdleTracker,
	daemonState *DaemonState,
) {
	paneStatus := idleTracker.GetPaneActivityStatus(cfg)
	livenessMap := idleTracker.GetLivenessMap()

	for nodeKey, nodeInfo := range nodes {
		simpleName := nodeKey
		if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
			simpleName = parts[1]
		}

		nodeCfg := cfg.GetNodeConfig(simpleName)
		if nodeCfg.DeliveryIdleTimeoutSeconds <= 0 {
			continue
		}

		retryMax := nodeCfg.DeliveryIdleRetryMax
		if retryMax <= 0 {
			retryMax = 3
		}

		paneState := paneStatus[nodeInfo.PaneID]
		if paneState != "idle" && paneState != "stale" {
			continue
		}

		timeout := time.Duration(nodeCfg.DeliveryIdleTimeoutSeconds * float64(time.Second))
		inboxDir := filepath.Join(nodeInfo.SessionDir, "inbox", simpleName)
		entries, err := os.ReadDir(inboxDir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			fileInfo, parseErr := message.ParseMessageFilename(entry.Name())
			if parseErr != nil {
				continue
			}
			if fileInfo.From == "postman" || fileInfo.From == "daemon" {
				continue
			}

			entryInfo, infoErr := entry.Info()
			if infoErr != nil {
				continue
			}
			deliveryTime := entryInfo.ModTime()

			if time.Since(deliveryTime) < timeout {
				continue
			}

			if daemonState.hasNodeSentSince(simpleName, deliveryTime) {
				continue
			}

			inboxPath := filepath.Join(inboxDir, entry.Name())
			daemonState.swallowedRetryCountMu.Lock()
			count := daemonState.swallowedRetryCount[inboxPath]
			daemonState.swallowedRetryCountMu.Unlock()
			if count >= retryMax {
				continue
			}

			notificationMsg := notification.BuildNotification(
				cfg, adjacency, nodes, contextID,
				simpleName, fileInfo.From,
				nodeInfo.SessionName, entry.Name(),
				livenessMap,
			)
			enterDelay := time.Duration(cfg.EnterDelay * float64(time.Second))
			if nodeCfg.EnterDelay != 0 {
				enterDelay = time.Duration(nodeCfg.EnterDelay * float64(time.Second))
			}
			tmuxTimeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
			enterCount := nodeCfg.EnterCount
			if enterCount == 0 {
				enterCount = 1
			}

			verifyDelay := time.Duration(cfg.EnterVerifyDelay * float64(time.Second))
			log.Printf("postman: notification: swallowed detected for %s: %s (age=%s, pane=%s)\n",
				simpleName, entry.Name(), time.Since(deliveryTime).Truncate(time.Second), nodeInfo.PaneID)
			_ = notification.SendToPane(nodeInfo.PaneID, notificationMsg, enterDelay, tmuxTimeout, enterCount, true, verifyDelay, cfg.EnterRetryMax)

			daemonState.swallowedRetryCountMu.Lock()
			daemonState.swallowedRetryCount[inboxPath]++
			daemonState.swallowedRetryCountMu.Unlock()

			log.Printf("postman: swallowed message re-delivered to %s (pane=%s): %s (attempt %d/%d)\n",
				simpleName, nodeInfo.PaneID, entry.Name(), count+1, retryMax)
			events <- tui.DaemonEvent{
				Type:    "swallowed_redelivery",
				Message: fmt.Sprintf("Re-delivered to %s: %s (attempt %d/%d)", simpleName, entry.Name(), count+1, retryMax),
				Details: map[string]interface{}{
					"node":    nodeKey,
					"file":    entry.Name(),
					"attempt": count + 1,
					"max":     retryMax,
				},
			}
		}
	}
}

// SetSessionEnabled sets the enabled/disabled state for a session (Issue #71).
func (ds *DaemonState) SetSessionEnabled(sessionName string, enabled bool) {
	ds.enabledSessionsMu.Lock()
	ds.enabledSessions[sessionName] = enabled
	ds.enabledSessionsMu.Unlock()
	log.Printf("postman: session state change: session=%s enabled=%v source=toggle ts=%s\n",
		sessionName, enabled, time.Now().UTC().Format(time.RFC3339Nano))
	ds.persistSessionEnabledMarker(sessionName, enabled)
}

func (ds *DaemonState) persistSessionEnabledMarker(sessionName string, enabled bool) {
	// Persist cross-daemon state in tmux server option (best-effort).
	key := "@a2a_session_on_" + sessionName
	if enabled {
		val := ds.contextID + ":" + strconv.Itoa(os.Getpid())
		_ = exec.Command("tmux", "set-option", "-g", key, val).Run()
	} else {
		_ = exec.Command("tmux", "set-option", "-gu", key).Run()
	}
}

// AutoEnableSessionIfNew enables a session if it has never been configured (Issue #91).
// Called on first discovery of a new pane to allow auto-PING without TUI intervention.
// Does nothing if the session is already tracked (operator's explicit state is preserved).
func (ds *DaemonState) AutoEnableSessionIfNew(sessionName string) {
	ds.enabledSessionsMu.Lock()
	if _, exists := ds.enabledSessions[sessionName]; exists {
		ds.enabledSessionsMu.Unlock()
		return
	}
	ds.enabledSessions[sessionName] = true
	ds.enabledSessionsMu.Unlock()
	log.Printf("postman: session state change: session=%s enabled=true source=auto-enable ts=%s\n",
		sessionName, time.Now().UTC().Format(time.RFC3339Nano))
	ds.persistSessionEnabledMarker(sessionName, true)
}

// IsSessionEnabled checks if a session is enabled (Issue #71).
// During the startup drain window, returns true for all sessions to prevent
// the race where messages are rejected before sessions are registered (#217).
func (ds *DaemonState) IsSessionEnabled(sessionName string) bool {
	if ds.drainWindow > 0 && time.Since(ds.startedAt) < ds.drainWindow {
		return true
	}
	ds.enabledSessionsMu.RLock()
	defer ds.enabledSessionsMu.RUnlock()
	enabled, exists := ds.enabledSessions[sessionName]
	if !exists {
		return false // Default: disabled
	}
	return enabled
}

// GetConfiguredSessionEnabled returns the explicitly configured session state,
// ignoring the startup drain window. Use for TUI display only.
func (ds *DaemonState) GetConfiguredSessionEnabled(sessionName string) bool {
	ds.enabledSessionsMu.RLock()
	defer ds.enabledSessionsMu.RUnlock()
	enabled, exists := ds.enabledSessions[sessionName]
	if !exists {
		return false // Default: disabled
	}
	return enabled
}

// hasNodeSentSince returns true if the node has sent a message after the given time.
// Issue #282: Used to detect swallowed deliveries.
func (ds *DaemonState) hasNodeSentSince(nodeName string, since time.Time) bool {
	ds.lastDeliveryMu.RLock()
	defer ds.lastDeliveryMu.RUnlock()
	prefix := nodeName + ":"
	for key, t := range ds.lastDeliveryBySenderRecipient {
		if strings.HasPrefix(key, prefix) && t.After(since) {
			return true
		}
	}
	return false
}

// ShouldSendAlert checks if enough time has passed since the last alert (Issue #118).
// Returns true if the alert should be sent (cooldown expired or first time).
func (ds *DaemonState) ShouldSendAlert(alertKey string, cooldownSeconds float64) bool {
	ds.lastAlertTimestampMu.Lock()
	defer ds.lastAlertTimestampMu.Unlock()

	if cooldownSeconds <= 0 {
		return true
	}

	lastSent, exists := ds.lastAlertTimestamp[alertKey]
	if !exists {
		return true
	}

	return time.Since(lastSent) > time.Duration(cooldownSeconds*float64(time.Second))
}

// MarkAlertSent records the current time as the last alert sent time (Issue #118).
func (ds *DaemonState) MarkAlertSent(alertKey string) {
	ds.lastAlertTimestampMu.Lock()
	defer ds.lastAlertTimestampMu.Unlock()
	ds.lastAlertTimestamp[alertKey] = time.Now()
}

// reminderShouldIncrement returns true if the message sender should trigger the reminder counter.
// Daemon-generated messages (from="postman" or from="daemon") are excluded.
func reminderShouldIncrement(from string) bool {
	return from != "postman" && from != "daemon"
}

func messageEventSuppressesNormalDelivery(event message.DaemonEvent) bool {
	return event.Type == "message_received" && strings.HasPrefix(event.Message, "Dead-letter:")
}

// startHeartbeatTrigger periodically sends heartbeat triggers to the configured LLM node.
// Goroutine lifecycle: exits cleanly on ctx.Done() (consistent with daemon.go:275 pattern).
func startHeartbeatTrigger(ctx context.Context, sharedNodes *atomic.Pointer[map[string]discovery.NodeInfo], contextID string, cfg *config.Config, adjacency map[string][]string) {
	interval := time.Duration(cfg.Heartbeat.IntervalSeconds * float64(time.Second))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := heartbeat.SendHeartbeatTrigger(sharedNodes, contextID, cfg.Heartbeat.LLMNode, cfg.Heartbeat.Prompt, cfg.Heartbeat.IntervalSeconds, cfg, adjacency); err != nil {
				log.Printf("heartbeat: trigger error: %v", err)
			}
		}
	}
}

// checkPaneRestarts detects pane restarts and sends PING (Issue #98).
// Detects restart by comparing current paneStates with previous paneStates.
// Issue #118: Added sessionDir for alert messaging.
func (ds *DaemonState) checkPaneRestarts(paneStates map[string]uinode.PaneInfo, paneToNode map[string]string, nodes map[string]discovery.NodeInfo, cfg *config.Config, events chan<- tui.DaemonEvent, contextID, sessionDir string, adjacency map[string][]string, idleTracker *idle.IdleTracker) []string {
	ds.prevPaneStatesMu.Lock()
	defer ds.prevPaneStatesMu.Unlock()

	var restartedNodeKeys []string

	for currentPaneID, currentInfo := range paneStates {
		nodeKey, exists := paneToNode[currentPaneID]
		if !exists {
			continue // No node mapped to this pane
		}

		_, nodeExists := nodes[nodeKey]
		if !nodeExists {
			continue // Node not found
		}

		// Check if this pane existed before
		_, prevExists := ds.prevPaneStates[currentPaneID]

		if prevExists {
			// Pane existed before - no restart detected
			continue
		}

		// New pane detected - check if this is a restart
		// Restart criteria: A node that previously had a different paneID now has a new paneID
		// Search for previous pane with the same node
		var oldPaneID string
		for oldID := range ds.prevPaneStates {
			if oldNodeKey, found := ds.prevPaneToNode[oldID]; found && oldNodeKey == nodeKey {
				// Found old pane for the same node
				oldPaneID = oldID
				break
			}
		}

		if oldPaneID != "" {
			if _, oldStillLive := paneStates[oldPaneID]; oldStillLive {
				continue
			}

			// Restart detected: node had oldPaneID, now has currentPaneID
			log.Printf("postman: pane restart detected for %s (old: %s, new: %s)\n", nodeKey, oldPaneID, currentPaneID)
			restartedNodeKeys = append(restartedNodeKeys, nodeKey)

			// Send TUI event
			events <- tui.DaemonEvent{
				Type:    "pane_restart",
				Message: fmt.Sprintf("Pane restart detected: %s (old: %s, new: %s)", nodeKey, oldPaneID, currentPaneID),
				Details: map[string]interface{}{
					"node":        nodeKey,
					"old_pane_id": oldPaneID,
					"new_pane_id": currentPaneID,
					"pane_info":   currentInfo,
				},
			}

			// Issue #213: Requeue waiting/ files for restarted node back to inbox/
			if nodeInfo, found := nodes[nodeKey]; found {
				simpleName := nodeKey
				if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
					simpleName = parts[1]
				}
				waitingDir := filepath.Join(nodeInfo.SessionDir, "waiting")
				if entries, readErr := os.ReadDir(waitingDir); readErr == nil {
					requeueCount := 0
					deadLetterCount := 0
					for _, e := range entries {
						if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
							continue
						}
						if !strings.Contains(e.Name(), "-to-"+simpleName) {
							continue
						}
						if err := requeueWaitingMessage(nodeInfo.SessionDir, nodeInfo.SessionName, simpleName, e.Name()); err != nil {
							continue
						}
						deadLetterPath := filepath.Join(nodeInfo.SessionDir, "dead-letter", deadLetterMissingOriginalName(e.Name()))
						if _, err := os.Stat(deadLetterPath); err == nil {
							deadLetterCount++
						} else {
							requeueCount++
						}
					}
					if requeueCount > 0 {
						log.Printf("postman: pane restart requeued %d waiting/ files for %s\n", requeueCount, nodeKey)
					}
					if deadLetterCount > 0 {
						log.Printf("postman: pane restart dead-lettered %d waiting/ files for %s (missing original artifact)\n", deadLetterCount, nodeKey)
					}
				}
			}
		}
	}

	// Update prevPaneStates
	ds.prevPaneStates = make(map[string]uinode.PaneInfo)
	for paneID, info := range paneStates {
		ds.prevPaneStates[paneID] = info
	}

	// Update prevPaneToNode
	ds.prevPaneToNode = make(map[string]string)
	for paneID, nodeKey := range paneToNode {
		ds.prevPaneToNode[paneID] = nodeKey
	}

	return restartedNodeKeys
}

// checkPaneDisappearance detects disappeared panes and marks corresponding nodes as inactive.
// When a pane is killed, it no longer appears in GetAllPanesInfo() output.
// This function compares previous pane states with current pane states to detect disappearances.
func (ds *DaemonState) checkPaneDisappearance(currentPaneStates map[string]uinode.PaneInfo, prevPaneToNode map[string]string, knownNodes map[string]discovery.NodeInfo, events chan<- tui.DaemonEvent) {
	ds.prevPaneStatesMu.RLock()
	defer ds.prevPaneStatesMu.RUnlock()

	// Collect disappeared panes grouped by session (Issue #209)
	disappearedBySession := make(map[string][]string) // session -> []nodeKey

	// Find panes that existed before but don't exist now
	for prevPaneID := range ds.prevPaneStates {
		if _, stillExists := currentPaneStates[prevPaneID]; !stillExists {
			// Pane disappeared - find the node it belonged to
			if nodeKey, found := prevPaneToNode[prevPaneID]; found {
				// Issue #210: Count pending inbox/waiting files for recovery hint
				inboxCount, waitingCount := countPendingFiles(nodeKey, knownNodes)

				details := map[string]interface{}{
					"pane_id": prevPaneID,
					"node":    nodeKey,
				}
				if inboxCount > 0 {
					details["pending_inbox_count"] = inboxCount
				}
				if waitingCount > 0 {
					details["pending_waiting_count"] = waitingCount
				}

				// Send pane_disappeared event to TUI
				events <- tui.DaemonEvent{
					Type:    "pane_disappeared",
					Message: fmt.Sprintf("Pane disappeared: %s (node: %s)", prevPaneID, nodeKey),
					Details: details,
				}
				log.Printf("postman: pane disappeared for node %s (paneID: %s, inbox: %d, waiting: %d)\n", nodeKey, prevPaneID, inboxCount, waitingCount)

				// Group by session name
				sessionName := nodeKey
				if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
					sessionName = parts[0]
				}
				disappearedBySession[sessionName] = append(disappearedBySession[sessionName], nodeKey)
			}
		}
	}

	// Emit session_collapsed event when 2+ panes from same session disappeared (Issue #209)
	for sessionName, collapsedNodes := range disappearedBySession {
		if len(collapsedNodes) >= 2 {
			events <- tui.DaemonEvent{
				Type:    "session_collapsed",
				Message: fmt.Sprintf("Session collapsed: %s (%d panes disappeared)", sessionName, len(collapsedNodes)),
				Details: map[string]interface{}{
					"session": sessionName,
					"nodes":   collapsedNodes,
					"count":   len(collapsedNodes),
				},
			}
			log.Printf("postman: session collapsed: %s (%d panes disappeared: %v)\n", sessionName, len(collapsedNodes), collapsedNodes)
		}
	}
}

// countPendingFiles counts .md files in inbox/{node}/ and waiting/ for a given nodeKey.
// Used for post-collapse recovery hints (Issue #210).
func countPendingFiles(nodeKey string, knownNodes map[string]discovery.NodeInfo) (inboxCount, waitingCount int) {
	nodeInfo, ok := knownNodes[nodeKey]
	if !ok {
		return 0, 0
	}
	simpleName := nodeKey
	if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
		simpleName = parts[1]
	}

	// Count inbox files
	inboxDir := filepath.Join(nodeInfo.SessionDir, "inbox", simpleName)
	if entries, err := os.ReadDir(inboxDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				inboxCount++
			}
		}
	}

	// Count waiting files addressed to this node
	waitingDir := filepath.Join(nodeInfo.SessionDir, "waiting")
	if entries, err := os.ReadDir(waitingDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") && strings.Contains(e.Name(), "-to-"+simpleName) {
				waitingCount++
			}
		}
	}
	return inboxCount, waitingCount
}

func requeueWaitingMessage(sessionDir, sessionName, simpleName, filename string) error {
	waitingPath := filepath.Join(sessionDir, "waiting", filename)
	inboxDir := filepath.Join(sessionDir, "inbox", simpleName)
	inboxPath := filepath.Join(inboxDir, filename)
	readPath := filepath.Join(sessionDir, "read", filename)
	waitingContent, err := os.ReadFile(waitingPath)
	if err != nil {
		return err
	}
	waitingPayload := compatibilityMailboxPayloadForFile(filename, filepath.Join("waiting", filename), string(waitingContent))

	if _, err := os.Stat(inboxPath); err == nil {
		if err := os.Remove(waitingPath); err != nil {
			return err
		}
		recordCompatibilityMailboxPayload(sessionDir, sessionName, "compatibility_mailbox_waiting_cleared", journal.VisibilityOperatorVisible, waitingPayload)
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	if data, err := os.ReadFile(readPath); err == nil {
		if err := os.MkdirAll(inboxDir, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(inboxPath, data, 0o600); err != nil {
			return err
		}
		if err := os.Remove(waitingPath); err != nil {
			return err
		}
		recordCompatibilityMailboxPayload(sessionDir, sessionName, "compatibility_mailbox_waiting_cleared", journal.VisibilityOperatorVisible, waitingPayload)
		recordCompatibilityMailboxPayload(sessionDir, sessionName, "compatibility_mailbox_delivered", journal.VisibilityCompatibilityMailbox, compatibilityMailboxPayloadForFile(filename, filepath.Join("inbox", simpleName, filename), string(data)))
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.MkdirAll(filepath.Join(sessionDir, "dead-letter"), 0o700); err != nil {
		return err
	}
	deadLetterPath := filepath.Join(sessionDir, "dead-letter", deadLetterMissingOriginalName(filename))
	if err := os.Rename(waitingPath, deadLetterPath); err != nil {
		return err
	}
	recordCompatibilityMailboxPayload(sessionDir, sessionName, "compatibility_mailbox_waiting_cleared", journal.VisibilityOperatorVisible, waitingPayload)
	deadLetterPayload := compatibilityMailboxPayloadForFile(filename, filepath.Join("dead-letter", deadLetterMissingOriginalName(filename)), string(waitingContent))
	deadLetterPayload.SourcePath = waitingPayload.Path
	recordCompatibilityMailboxPayload(sessionDir, sessionName, "compatibility_mailbox_dead_lettered", journal.VisibilityOperatorVisible, deadLetterPayload)
	return nil
}

func deadLetterMissingOriginalName(filename string) string {
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	return base + "-dl-missing-original" + ext
}
