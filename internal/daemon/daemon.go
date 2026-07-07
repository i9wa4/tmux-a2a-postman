package daemon

import (
	"context"
	"encoding/base64"
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

	"github.com/fswatcher/fswatcher"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/msgtrace"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/runtimeprofile"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
	"github.com/i9wa4/tmux-a2a-postman/internal/uinode"
)

const (
	inboxCheckInterval                            = 30 * time.Second
	runtimeDiagnosticsLogInterval                 = 10 * time.Minute
	defaultDaemonSubmitQueueWarnThresholdMs int64 = 30_000
)

// daemonSubmitQueueWarnThresholdMs is the active queue wait WARNING threshold
// in milliseconds. Initialized from config at daemon startup; tests may
// override it directly. Defaults to defaultDaemonSubmitQueueWarnThresholdMs.
var daemonSubmitQueueWarnThresholdMs int64 = defaultDaemonSubmitQueueWarnThresholdMs

type filesystemWatcher interface {
	Add(string, fswatcher.Op) error
	Remove(string) error
}

func sessionScanInterval(cfg *config.Config) time.Duration {
	if cfg == nil {
		return time.Second
	}
	seconds := cfg.SessionScanInterval
	if seconds <= 0 {
		seconds = cfg.ScanInterval
	}
	if seconds <= 0 {
		return time.Second
	}
	interval := time.Duration(seconds * float64(time.Second))
	if interval <= 0 {
		return time.Second
	}
	return interval
}

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

func frontmatterValue(content, key string) string {
	frontmatter, _, ok, err := envelope.ScanFrontmatter(content)
	if !ok || err != nil {
		return ""
	}
	for _, line := range strings.Split(frontmatter, "\n") {
		prefix := key + ": "
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func recordMailboxProjectionPayload(sessionDir, sessionName, eventType string, visibility journal.Visibility, payload journal.MailboxEventPayload) {
	if err := journal.RecordProcessMailboxPayload(sessionDir, sessionName, eventType, visibility, payload, time.Now()); err != nil {
		log.Printf("postman: WARNING: component=%s event=append_failed mailbox_event=%s err=%v\n", projection.MailboxProjectionComponent, eventType, err)
	}
}

func syncMailboxProjection(sessionDir string) {
	syncMailboxProjectionWithTrace(sessionDir, msgtrace.Fields{TmuxSession: filepath.Base(sessionDir)})
}

var syncMailboxProjectionWithTraceFn = syncMailboxProjectionWithTrace

func syncMailboxProjectionWithTrace(sessionDir string, fields msgtrace.Fields) {
	if fields.TmuxSession == "" {
		fields.TmuxSession = filepath.Base(sessionDir)
	}
	emitTrace := msgtrace.HasMessageContext(fields)
	if err := projection.SyncMailboxProjection(sessionDir); err != nil {
		fields.Result = "error"
		fields.Reason = err.Error()
		if emitTrace {
			msgtrace.Log("projection_sync", fields)
		}
		log.Printf("postman: WARNING: component=%s event=sync_failed session_dir=%s err=%v\n", projection.MailboxProjectionComponent, sessionDir, err)
		return
	}
	fields.Result = "ok"
	if emitTrace {
		msgtrace.Log("projection_sync", fields)
	}
}

func mailboxProjectionPayloadForFile(filename, relativePath, content string) journal.MailboxEventPayload {
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

func daemonSubmitSessionDir(requestPath string) (string, bool) {
	requestDir := filepath.Dir(requestPath)
	if filepath.Base(requestDir) != "requests" {
		return "", false
	}
	submitDir := filepath.Dir(requestDir)
	if filepath.Base(submitDir) != string(projection.SubmitPathDaemon) {
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

func handleDaemonSubmitSend(sessionDir string, request projection.DaemonSubmitRequest) (projection.DaemonSubmitResponse, error) {
	if request.RequestID == "" {
		return projection.DaemonSubmitResponse{}, fmt.Errorf("daemon submit send missing request_id")
	}
	if request.Filename == "" {
		return projection.DaemonSubmitResponse{}, fmt.Errorf("daemon submit send missing filename")
	}
	if strings.ContainsAny(request.Filename, "/\\") {
		return projection.DaemonSubmitResponse{}, fmt.Errorf("daemon submit send filename must not contain path separators")
	}
	if request.Content == "" {
		return projection.DaemonSubmitResponse{}, fmt.Errorf("daemon submit send missing content")
	}
	postDir := filepath.Join(sessionDir, "post")
	if err := os.MkdirAll(postDir, 0o700); err != nil {
		return projection.DaemonSubmitResponse{}, fmt.Errorf("creating post directory: %w", err)
	}
	postPath := filepath.Join(postDir, request.Filename)
	fields := msgtrace.FromContent(request.Filename, filepath.Join("post", request.Filename), filepath.Base(sessionDir), request.Content)
	fields.DaemonSubmitRequestID = request.RequestID
	fields.DaemonSubmitCommand = string(request.Command)
	fields.SubmitPath = string(projection.SubmitPathDaemon)
	log.Printf("postman: component=%s event=send_write_start submit_path=%s session=%s request=%s file=%s bytes=%d\n",
		projection.SubmitPathDaemon, projection.SubmitPathDaemon, filepath.Base(sessionDir), request.RequestID, request.Filename, len(request.Content))
	if err := os.WriteFile(postPath, []byte(request.Content), 0o600); err != nil {
		return projection.DaemonSubmitResponse{}, fmt.Errorf("writing post message: %w", err)
	}
	msgtrace.Log("send_enqueue", fields)
	log.Printf("postman: component=%s event=send_write_done submit_path=%s session=%s request=%s file=%s\n",
		projection.SubmitPathDaemon, projection.SubmitPathDaemon, filepath.Base(sessionDir), request.RequestID, request.Filename)
	return projection.DaemonSubmitResponse{
		RequestID: request.RequestID,
		Command:   request.Command,
		HandledAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  request.Filename,
	}, nil
}

func handleDaemonSubmitPop(sessionDir string, request projection.DaemonSubmitRequest) (projection.DaemonSubmitResponse, error) {
	if request.RequestID == "" {
		return projection.DaemonSubmitResponse{}, fmt.Errorf("daemon submit pop missing request_id")
	}
	if request.Node == "" {
		return projection.DaemonSubmitResponse{}, fmt.Errorf("daemon submit pop missing node")
	}
	inboxDir := filepath.Join(sessionDir, "inbox", request.Node)
	msgs := message.ScanInboxMessages(inboxDir)
	if len(msgs) == 0 {
		return projection.DaemonSubmitResponse{
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
			return projection.DaemonSubmitResponse{}, fmt.Errorf("reading pop message: %w", err)
		}
		msgs = message.ScanInboxMessages(inboxDir)
		if len(msgs) == 0 {
			return projection.DaemonSubmitResponse{
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
			return projection.DaemonSubmitResponse{}, fmt.Errorf("reading pop message: %w", err)
		}
	}
	readPath, err := message.ArchiveInboxMessage(abs, msgs[0].Filename)
	if err != nil {
		return projection.DaemonSubmitResponse{}, err
	}
	fields := msgtrace.FromContent(msgs[0].Filename, filepath.Join("read", msgs[0].Filename), filepath.Base(sessionDir), string(data))
	fields.DaemonSubmitRequestID = request.RequestID
	fields.DaemonSubmitCommand = string(request.Command)
	fields.SubmitPath = string(projection.SubmitPathDaemon)
	msgtrace.Log("pop_read_archive", fields)
	recordDaemonSubmitPopRead(sessionDir, readPath, msgs[0].Filename, string(data))
	return projection.DaemonSubmitResponse{
		RequestID:    request.RequestID,
		Command:      request.Command,
		HandledAt:    time.Now().UTC().Format(time.RFC3339),
		Filename:     msgs[0].Filename,
		Content:      string(data),
		MarkdownPath: readPath,
		UnreadBefore: len(msgs),
	}, nil
}

func handleDaemonSubmitRuntimeProfile(_ string, request projection.DaemonSubmitRequest) (projection.DaemonSubmitResponse, error) {
	response := projection.DaemonSubmitResponse{
		RequestID: request.RequestID,
		Command:   request.Command,
		HandledAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if request.RequestID == "" {
		return response, fmt.Errorf("daemon submit runtime-profile missing request_id")
	}
	kind, err := runtimeprofile.NormalizeKind(request.ProfileKind)
	if err != nil {
		return response, err
	}
	maxBytes := request.ProfileMaxBytes
	if maxBytes <= 0 {
		maxBytes = runtimeprofile.DefaultMaxBytes
	}
	data, err := runtimeprofile.Capture(kind, maxBytes)
	if err != nil {
		return response, err
	}

	switch request.ProfileDestination {
	case "stdout":
		response.RuntimeProfile = &projection.RuntimeProfileCapture{
			Kind:          kind,
			Destination:   "stdout",
			Encoding:      "base64",
			ContentBase64: base64.StdEncoding.EncodeToString(data),
			Bytes:         len(data),
			MaxBytes:      maxBytes,
		}
	case "file":
		if request.ProfileOutputPath == "" {
			return response, fmt.Errorf("daemon submit runtime-profile file destination missing output path")
		}
		if err := writeRuntimeProfileFile(request.ProfileOutputPath, request.RequestID, data, request.ProfileForce); err != nil {
			return response, err
		}
		response.RuntimeProfile = &projection.RuntimeProfileCapture{
			Kind:        kind,
			Destination: "file",
			Bytes:       len(data),
			MaxBytes:    maxBytes,
			OutputPath:  request.ProfileOutputPath,
		}
	default:
		return response, fmt.Errorf("daemon submit runtime-profile requires explicit destination stdout or file")
	}
	return response, nil
}

func writeRuntimeProfileFile(outputPath, requestID string, data []byte, force bool) error {
	if outputPath == "" {
		return fmt.Errorf("profile output path is required")
	}
	if strings.ContainsAny(filepath.Base(outputPath), `/\`) {
		return fmt.Errorf("profile output path must name a file")
	}
	dir := filepath.Dir(outputPath)
	if dir == "." {
		dir = ""
	}
	if dir != "" {
		if info, err := os.Stat(dir); err != nil {
			return fmt.Errorf("profile output directory: %w", err)
		} else if !info.IsDir() {
			return fmt.Errorf("profile output directory is not a directory")
		}
	}
	if !force {
		if _, err := os.Stat(outputPath); err == nil {
			return fmt.Errorf("profile output file already exists: %s (use --force to overwrite)", outputPath)
		}
	}
	tmpPath := outputPath + "." + requestID + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("writing profile temp file: %w", err)
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("publishing profile file: %w", err)
	}
	return nil
}

func recordDaemonSubmitPopRead(sessionDir, readPath, filename, fallbackContent string) {
	content := fallbackContent
	if readContent, err := os.ReadFile(readPath); err == nil {
		content = string(readContent)
	}
	recordMailboxProjectionPayload(sessionDir, filepath.Base(sessionDir), projection.MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, mailboxProjectionPayloadForFile(
		filename,
		filepath.Join("read", filename),
		content,
	))
}

type daemonSubmitProcessResult struct {
	Command                  projection.DaemonSubmitCommand
	SessionDir               string
	Filename                 string
	PostPath                 string
	ProjectionSyncSessionDir string
}

func (r daemonSubmitProcessResult) hasPostDispatch() bool {
	return r.Command == projection.DaemonSubmitSend && r.PostPath != ""
}

func claimDaemonSubmitRequest(requestPath string) (string, bool, error) {
	if !strings.HasSuffix(filepath.Base(requestPath), ".json") {
		return "", false, nil
	}
	claimedPath := requestPath + ".processing"
	if err := os.Rename(requestPath, claimedPath); err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return claimedPath, true, nil
}

func processDaemonSubmitRequest(requestPath string) (daemonSubmitProcessResult, error) {
	claimedPath, claimed, err := claimDaemonSubmitRequest(requestPath)
	if err != nil || !claimed {
		return daemonSubmitProcessResult{}, err
	}

	sessionDir, ok := daemonSubmitSessionDir(claimedPath)
	if !ok {
		return daemonSubmitProcessResult{}, nil
	}
	request, err := projection.ReadDaemonSubmitRequest(claimedPath)
	if err != nil {
		return daemonSubmitProcessResult{}, err
	}
	requestTrace := msgtrace.FromContent(request.Filename, request.Filename, filepath.Base(sessionDir), request.Content)
	requestTrace.DaemonSubmitRequestID = request.RequestID
	requestTrace.DaemonSubmitCommand = string(request.Command)
	requestTrace.SubmitPath = string(projection.SubmitPathDaemon)
	msgtrace.Log("daemon_submit_accept", requestTrace)
	result := daemonSubmitProcessResult{
		Command:    request.Command,
		SessionDir: sessionDir,
	}
	processingStartedAt := time.Now()
	queueMs := daemonSubmitDurationMillis(daemonSubmitDurationSince(request.CreatedAt, processingStartedAt))
	log.Printf("postman: component=%s event=request_processing submit_path=%s command=%s session=%s request=%s file=%s queue_ms=%d\n",
		projection.SubmitPathDaemon, projection.SubmitPathDaemon, request.Command, filepath.Base(sessionDir), request.RequestID, request.Filename, queueMs)
	if queueMs >= daemonSubmitQueueWarnThresholdMs {
		log.Printf("postman: WARNING: component=%s event=queue_ms_threshold_exceeded submit_path=%s command=%s session=%s request=%s queue_ms=%d threshold_ms=%d\n",
			projection.SubmitPathDaemon, projection.SubmitPathDaemon, request.Command, filepath.Base(sessionDir), request.RequestID, queueMs, daemonSubmitQueueWarnThresholdMs)
	}

	var response projection.DaemonSubmitResponse
	switch request.Command {
	case projection.DaemonSubmitSend:
		response, err = handleDaemonSubmitSend(sessionDir, request)
		if err == nil && response.Filename != "" {
			result.Filename = response.Filename
			result.PostPath = filepath.Join(sessionDir, "post", response.Filename)
		}
	case projection.DaemonSubmitPop:
		response, err = handleDaemonSubmitPop(sessionDir, request)
		if err == nil && !response.Empty {
			result.ProjectionSyncSessionDir = sessionDir
		}
	case projection.DaemonSubmitRuntimeProfile:
		response, err = handleDaemonSubmitRuntimeProfile(sessionDir, request)
	default:
		err = fmt.Errorf("unsupported daemon submit command %q", request.Command)
		response = projection.DaemonSubmitResponse{
			RequestID: request.RequestID,
			Command:   request.Command,
			HandledAt: time.Now().UTC().Format(time.RFC3339),
		}
	}
	if err != nil {
		response.Error = err.Error()
	}
	if _, writeErr := projection.WriteDaemonSubmitResponse(sessionDir, response); writeErr != nil {
		return result, writeErr
	}
	responseWrittenAt := time.Now()
	handlerMs := daemonSubmitDurationMillis(responseWrittenAt.Sub(processingStartedAt))
	totalMs := daemonSubmitDurationMillis(daemonSubmitDurationSince(request.CreatedAt, responseWrittenAt))
	log.Printf("postman: component=%s event=response_written submit_path=%s command=%s session=%s request=%s file=%s error=%t queue_ms=%d handler_ms=%d total_ms=%d\n",
		projection.SubmitPathDaemon, projection.SubmitPathDaemon, request.Command, filepath.Base(sessionDir), request.RequestID, response.Filename, response.Error != "", queueMs, handlerMs, totalMs)
	resultTrace := requestTrace
	if response.Filename != "" {
		resultTrace.MessageID = response.Filename
	}
	if response.MarkdownPath != "" {
		resultTrace.MessagePath = filepath.Join("read", filepath.Base(response.MarkdownPath))
	}
	if response.Error != "" {
		resultTrace.Result = "error"
		resultTrace.Reason = response.Error
		msgtrace.Log("daemon_submit_reject", resultTrace)
	} else {
		resultTrace.Result = "ok"
		msgtrace.Log("daemon_submit_result", resultTrace)
	}
	if removeErr := os.Remove(claimedPath); removeErr != nil && !os.IsNotExist(removeErr) {
		log.Printf("postman: WARNING: component=%s event=request_remove_failed submit_path=%s path=%s err=%v\n", projection.SubmitPathDaemon, projection.SubmitPathDaemon, claimedPath, removeErr)
	}
	return result, nil
}

func daemonSubmitDurationSince(createdAt string, now time.Time) time.Duration {
	if createdAt == "" {
		return -1
	}
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return -1
	}
	return now.Sub(parsed)
}

func daemonSubmitDurationMillis(duration time.Duration) int64 {
	if duration < 0 {
		return -1
	}
	return duration.Milliseconds()
}

// DaemonState manages daemon state (Issue #71).
type DaemonState struct {
	contextID                     string        // This daemon's contextID (for tmux option writes)
	startedAt                     time.Time     // Daemon start timestamp (#217)
	drainWindow                   time.Duration // Startup drain window duration (#217)
	enabledSessions               map[string]bool
	enabledSessionsMu             sync.RWMutex
	prevPaneStates                map[string]uinode.PaneInfo // Issue #98: Track previous pane states for restart detection
	prevPaneStatesMu              sync.RWMutex               // Issue #98: Mutex for prevPaneStates
	prevPaneToNode                map[string]string          // Track previous pane ID -> node key mapping for restart detection
	lastDeliveryBySenderRecipient map[string]time.Time       // Issue #211: Rate limit duplicate deliveries (sender:recipient -> time)
	reservedDeliveryByRoute       map[string]time.Time       // Issue #393: in-flight rate-limit reservations (sender:recipient -> time)
	lastDeliveryMu                sync.RWMutex               // Issue #211: Mutex for lastDeliveryBySenderRecipient
	nonDaemonDeliveryBudget       *nonDaemonDeliveryBudget   // Issue #572: bounded concurrency for post/auto-PING/manual-PING delivery
	clock                         func() time.Time
}

// NewDaemonState creates a new DaemonState instance (Issue #71).
// drainWindowSeconds configures the startup drain window during which
// IsSessionEnabled returns true for all sessions (#217).
func NewDaemonState(drainWindowSeconds float64, contextID string) *DaemonState {
	return newDaemonStateWithClock(drainWindowSeconds, contextID, time.Now)
}

func newDaemonStateWithClock(drainWindowSeconds float64, contextID string, clock func() time.Time) *DaemonState {
	if clock == nil {
		clock = time.Now
	}
	return &DaemonState{
		contextID:                     contextID,
		startedAt:                     clock(),
		drainWindow:                   time.Duration(drainWindowSeconds * float64(time.Second)),
		enabledSessions:               make(map[string]bool),
		prevPaneStates:                make(map[string]uinode.PaneInfo), // Issue #98
		prevPaneToNode:                make(map[string]string),          // paneID -> nodeKey mapping
		lastDeliveryBySenderRecipient: make(map[string]time.Time),       // Issue #211
		reservedDeliveryByRoute:       make(map[string]time.Time),
		nonDaemonDeliveryBudget:       newNonDaemonDeliveryBudget(clock),
		clock:                         clock,
	}
}

// NonDaemonDeliveryWorkerLimit returns the shared worker limit applied to
// post/auto-PING/manual-PING delivery paths (Issue #572).
func (ds *DaemonState) NonDaemonDeliveryWorkerLimit() int {
	return ds.nonDaemonDeliveryBudgetForUse().workerLimit()
}

// BeginManualPingFanout reserves a bounded worker count for a manual PING
// fanout of the given size, returning the number of workers to actually
// spawn (capped at the shared delivery budget's worker limit).
func (ds *DaemonState) BeginManualPingFanout(total int) int {
	return ds.nonDaemonDeliveryBudgetForUse().beginManualFanout(total)
}

// StartManualPingDelivery attempts to acquire a manual-PING delivery slot.
func (ds *DaemonState) StartManualPingDelivery() bool {
	return ds.nonDaemonDeliveryBudgetForUse().tryStart(nonDaemonDeliveryPathManualPing)
}

// FinishManualPingDelivery releases a manual-PING delivery slot.
func (ds *DaemonState) FinishManualPingDelivery() {
	ds.nonDaemonDeliveryBudgetForUse().finish(nonDaemonDeliveryPathManualPing)
}

// UnqueueManualPingDelivery decrements the pending-manual-PING counter for a
// job that a worker has just picked up from the fanout queue.
func (ds *DaemonState) UnqueueManualPingDelivery() {
	ds.nonDaemonDeliveryBudgetForUse().unqueue(nonDaemonDeliveryPathManualPing)
}

// FinishManualPingFanout resets manual-PING fanout bookkeeping once a fanout
// round completes.
func (ds *DaemonState) FinishManualPingFanout() {
	ds.nonDaemonDeliveryBudgetForUse().finishManualFanout()
}

func (ds *DaemonState) nonDaemonDeliveryBudgetForUse() *nonDaemonDeliveryBudget {
	if ds == nil {
		return newNonDaemonDeliveryBudget(nil)
	}
	if ds.nonDaemonDeliveryBudget == nil {
		ds.nonDaemonDeliveryBudget = newNonDaemonDeliveryBudget(ds.clock)
	}
	return ds.nonDaemonDeliveryBudget
}

func (ds *DaemonState) now() time.Time {
	if ds.clock == nil {
		return time.Now()
	}
	return ds.clock()
}

func (ds *DaemonState) reserveDeliveryRoute(route string, gap time.Duration, now time.Time) (time.Duration, time.Time, bool) {
	ds.lastDeliveryMu.Lock()
	defer ds.lastDeliveryMu.Unlock()

	if reservedAt, reserved := ds.reservedDeliveryByRoute[route]; reserved {
		remaining := gap - now.Sub(reservedAt)
		if remaining <= 0 {
			remaining = gap
		}
		if remaining < 10*time.Millisecond {
			remaining = 10 * time.Millisecond
		}
		return remaining, time.Time{}, false
	}

	latest, exists := ds.lastDeliveryBySenderRecipient[route]
	if exists {
		remaining := gap - now.Sub(latest)
		if remaining > 0 {
			return remaining, time.Time{}, false
		}
	}

	ds.reservedDeliveryByRoute[route] = now
	return 0, now, true
}

func (ds *DaemonState) finishDeliveryRoute(route string, reservedAt time.Time, hasReservation, delivered bool, finishedAt time.Time) {
	ds.lastDeliveryMu.Lock()
	defer ds.lastDeliveryMu.Unlock()

	if hasReservation {
		if current, ok := ds.reservedDeliveryByRoute[route]; ok && current.Equal(reservedAt) {
			delete(ds.reservedDeliveryByRoute, route)
		}
	}
	if delivered {
		ds.lastDeliveryBySenderRecipient[route] = finishedAt
	}
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

// RunDaemonLoop runs the daemon event loop in a goroutine (Issue #71).
func RunDaemonLoop(
	ctx context.Context,
	baseDir string,
	sessionDir string,
	contextID string,
	cfg *config.Config,
	watcher *fswatcher.Watcher,
	adjacency map[string][]string,
	nodes map[string]discovery.NodeInfo,
	knownNodes map[string]bool,
	events chan<- tui.DaemonEvent,
	configPath string,
	configPaths []string,
	nodesDirs []string,
	daemonState *DaemonState,
	idleTracker *idle.IdleTracker,
	sharedNodes *atomic.Pointer[map[string]discovery.NodeInfo],
	selfSession string,
) {
	runDaemonLoopWithWatcherEvents(
		ctx,
		baseDir,
		sessionDir,
		contextID,
		cfg,
		watcher,
		watcher.Events,
		watcher.Errors,
		adjacency,
		nodes,
		knownNodes,
		events,
		configPath,
		configPaths,
		nodesDirs,
		daemonState,
		idleTracker,
		sharedNodes,
		selfSession,
	)
}

func runDaemonLoopWithWatcherEvents(
	ctx context.Context,
	baseDir string,
	sessionDir string,
	contextID string,
	cfg *config.Config,
	watcher filesystemWatcher,
	watcherEvents <-chan fswatcher.Event,
	watcherErrors <-chan error,
	adjacency map[string][]string,
	nodes map[string]discovery.NodeInfo,
	knownNodes map[string]bool,
	events chan<- tui.DaemonEvent,
	configPath string,
	configPaths []string,
	nodesDirs []string,
	daemonState *DaemonState,
	idleTracker *idle.IdleTracker,
	sharedNodes *atomic.Pointer[map[string]discovery.NodeInfo],
	selfSession string,
) {
	// Apply configurable queue warning threshold before any workers start.
	if cfg != nil && cfg.DaemonSubmitQueueWarnThresholdMs > 0 {
		daemonSubmitQueueWarnThresholdMs = cfg.DaemonSubmitQueueWarnThresholdMs
	}

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
		events,
		configPath,
		configPaths,
		nodesDirs,
		daemonState,
		idleTracker,
		sharedNodes,
		selfSession,
	)

	scanTicker := time.NewTicker(time.Duration(cfg.ScanInterval * float64(time.Second)))
	defer scanTicker.Stop()
	sessionScanTicker := time.NewTicker(sessionScanInterval(cfg))
	defer sessionScanTicker.Stop()
	inboxCheckTicker := time.NewTicker(inboxCheckInterval)
	defer inboxCheckTicker.Stop()
	runtimeDiagnosticsTicker := time.NewTicker(runtimeDiagnosticsLogInterval)
	defer runtimeDiagnosticsTicker.Stop()

	runtime.bootstrap()
	runtime.logRuntimeDiagnosticsSnapshot("startup", runtime.now())

	for {
		select {
		case <-ctx.Done():
			runtime.handleContextDone()
			runtime.waitForMailboxProjectionSyncs()
			return
		case event, ok := <-watcherEvents:
			if !ok {
				runtime.waitForMailboxProjectionSyncs()
				return
			}
			runtime.handleWatcherEvent(event)
		case err, ok := <-watcherErrors:
			if !ok {
				runtime.waitForMailboxProjectionSyncs()
				return
			}
			runtime.handleWatcherError(err)
		case workerResult := <-runtime.daemonSubmitResults:
			runtime.handleDaemonSubmitResult(workerResult)
		case <-scanTicker.C:
			runtime.handleScanTick()
		case <-sessionScanTicker.C:
			runtime.handleSessionScanTick()
		case <-inboxCheckTicker.C:
			runtime.handleInboxCheckTick()
		case <-runtimeDiagnosticsTicker.C:
			runtime.logRuntimeDiagnosticsSnapshot("interval", runtime.now())
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

// SetSessionEnabled sets the enabled/disabled state for a session (Issue #71).
func (ds *DaemonState) SetSessionEnabled(sessionName string, enabled bool) {
	ds.enabledSessionsMu.Lock()
	ds.enabledSessions[sessionName] = enabled
	ds.enabledSessionsMu.Unlock()
	log.Printf("postman: session state change: session=%s enabled=%v source=toggle ts=%s\n",
		sessionName, enabled, ds.now().UTC().Format(time.RFC3339Nano))
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
		sessionName, ds.now().UTC().Format(time.RFC3339Nano))
	ds.persistSessionEnabledMarker(sessionName, true)
}

func (ds *DaemonState) hasConfiguredSession(sessionName string) bool {
	ds.enabledSessionsMu.RLock()
	_, exists := ds.enabledSessions[sessionName]
	ds.enabledSessionsMu.RUnlock()
	return exists
}

// IsSessionEnabled checks if a session is enabled (Issue #71).
// During the startup drain window, returns true for all sessions to prevent
// the race where messages are rejected before sessions are registered (#217).
func (ds *DaemonState) IsSessionEnabled(sessionName string) bool {
	if ds.drainWindow > 0 && ds.now().Sub(ds.startedAt) < ds.drainWindow {
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

func messageEventSuppressesNormalDelivery(event message.DaemonEvent) bool {
	return event.Type == "message_received" && strings.HasPrefix(event.Message, "Dead-letter:")
}

func messageEventFailureReason(event message.DaemonEvent) string {
	if event.Details == nil {
		return ""
	}
	reason, _ := event.Details["failure_reason"].(string)
	return reason
}

// checkPaneRestarts detects pane restarts and sends PING (Issue #98).
// Detects restart by comparing current paneStates with previous paneStates.
func (ds *DaemonState) checkPaneRestarts(paneStates map[string]uinode.PaneInfo, paneToNode map[string]string, nodes map[string]discovery.NodeInfo, events chan<- tui.DaemonEvent) []string {
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
				inboxCount := countPendingFiles(nodeKey, knownNodes)

				details := map[string]interface{}{
					"pane_id": prevPaneID,
					"node":    nodeKey,
				}
				if inboxCount > 0 {
					details["pending_inbox_count"] = inboxCount
				}

				// Send pane_disappeared event to TUI
				events <- tui.DaemonEvent{
					Type:    "pane_disappeared",
					Message: fmt.Sprintf("Pane disappeared: %s (node: %s)", prevPaneID, nodeKey),
					Details: details,
				}
				log.Printf("postman: pane disappeared for node %s (paneID: %s, inbox: %d)\n", nodeKey, prevPaneID, inboxCount)

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

// countPendingFiles counts .md files in inbox/{node}/ for a given nodeKey.
// Used for post-collapse recovery hints (Issue #210).
func countPendingFiles(nodeKey string, knownNodes map[string]discovery.NodeInfo) int {
	nodeInfo, ok := knownNodes[nodeKey]
	if !ok {
		return 0
	}
	simpleName := nodeKey
	if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
		simpleName = parts[1]
	}

	// Count inbox files
	inboxCount := 0
	inboxDir := filepath.Join(nodeInfo.SessionDir, "inbox", simpleName)
	if entries, err := os.ReadDir(inboxDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				inboxCount++
			}
		}
	}

	return inboxCount
}
