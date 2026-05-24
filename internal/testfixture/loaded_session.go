package testfixture

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

const (
	defaultLoadedSessionContextID              = "ctx-loaded-session"
	defaultLoadedSessionName                   = "loaded-session"
	defaultLoadedSessionMailboxEventRecords    = 10000
	defaultLoadedSessionReadArchiveRecords     = 500
	defaultLoadedSessionPostRecords            = 200
	defaultLoadedSessionDeadLetterRecords      = 100
	defaultLoadedSessionStatusSnapshots        = 8
	defaultLoadedSessionDaemonSubmitRequests   = 128
	defaultLoadedSessionDaemonSubmitResponses  = 128
	defaultLoadedSessionMessageBodyBytes       = 512
	defaultLoadedSessionDaemonSnapshotBodySize = 256
)

var defaultLoadedSessionNodes = []string{"worker", "critic", "guardian", "boss"}

type LoadedSessionConfig struct {
	ContextID             string
	SessionName           string
	Nodes                 []string
	MailboxEventRecords   int
	ReadArchiveRecords    int
	PostRecords           int
	DeadLetterRecords     int
	StatusSnapshots       int
	DaemonSubmitRequests  int
	DaemonSubmitResponses int
	MessageBodyBytes      int
	Now                   time.Time
}

type LoadedSession struct {
	BaseDir               string
	ContextID             string
	SessionName           string
	SessionDir            string
	Nodes                 []string
	MailboxEventRecords   int
	DeliveredRecords      int
	ReadArchiveRecords    int
	PostRecords           int
	DeadLetterRecords     int
	StatusSnapshots       int
	DaemonSubmitRequests  int
	DaemonSubmitResponses int
	MessageBodyBytes      int
	Now                   time.Time
}

func DefaultLoadedSessionConfig() LoadedSessionConfig {
	return LoadedSessionConfig{
		ContextID:             defaultLoadedSessionContextID,
		SessionName:           defaultLoadedSessionName,
		Nodes:                 append([]string(nil), defaultLoadedSessionNodes...),
		MailboxEventRecords:   defaultLoadedSessionMailboxEventRecords,
		ReadArchiveRecords:    defaultLoadedSessionReadArchiveRecords,
		PostRecords:           defaultLoadedSessionPostRecords,
		DeadLetterRecords:     defaultLoadedSessionDeadLetterRecords,
		StatusSnapshots:       defaultLoadedSessionStatusSnapshots,
		DaemonSubmitRequests:  defaultLoadedSessionDaemonSubmitRequests,
		DaemonSubmitResponses: defaultLoadedSessionDaemonSubmitResponses,
		MessageBodyBytes:      defaultLoadedSessionMessageBodyBytes,
		Now:                   time.Date(2026, time.May, 21, 9, 30, 0, 0, time.UTC),
	}
}

// BuildLoadedSession creates the shared loaded-session shape used by memory and
// daemon-submit latency benchmarks. Keep the default dimensions aligned with
// the #486 loaded-session benchmark shape unless a benchmark needs a smaller
// explicit variant.
func BuildLoadedSession(tb testing.TB, cfg LoadedSessionConfig) LoadedSession {
	tb.Helper()

	cfg = normalizeLoadedSessionConfig(cfg)
	baseDir := tb.TempDir()
	sessionDir := filepath.Join(baseDir, cfg.ContextID, cfg.SessionName)
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		tb.Fatalf("CreateSessionDirs: %v", err)
	}

	restoreDurableWrites := journal.SetDurableWritesForTesting(false)
	tb.Cleanup(restoreDurableWrites)

	writer, err := journal.OpenShadowWriter(sessionDir, cfg.ContextID, cfg.SessionName, os.Getpid(), cfg.Now)
	if err != nil {
		tb.Fatalf("OpenShadowWriter: %v", err)
	}

	fixture := LoadedSession{
		BaseDir:          baseDir,
		ContextID:        cfg.ContextID,
		SessionName:      cfg.SessionName,
		SessionDir:       sessionDir,
		Nodes:            append([]string(nil), cfg.Nodes...),
		MessageBodyBytes: cfg.MessageBodyBytes,
		Now:              cfg.Now,
	}

	eventCount := 0
	for eventCount < cfg.MailboxEventRecords && fixture.PostRecords < cfg.PostRecords {
		index := eventCount
		node := cfg.Nodes[index%len(cfg.Nodes)]
		filename := MessageFilename(index, "orchestrator", node)
		appendLoadedMailboxEvent(tb, writer, projection.MailboxProjectionPostedEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
			MessageID: filename,
			From:      "orchestrator",
			To:        node,
			Path:      filepath.Join("post", filename),
			Content:   MessageContent(filename, cfg.MessageBodyBytes),
		}, cfg.Now.Add(time.Duration(eventCount+1)*time.Millisecond))
		fixture.PostRecords++
		eventCount++
	}

	for eventCount < cfg.MailboxEventRecords && fixture.DeadLetterRecords < cfg.DeadLetterRecords {
		index := eventCount
		node := cfg.Nodes[index%len(cfg.Nodes)]
		filename := MessageFilename(index, "orchestrator", node)
		deadLetterName := strings.TrimSuffix(filename, ".md") + "-dl-routing-denied.md"
		appendLoadedMailboxEvent(tb, writer, projection.MailboxProjectionDeadLetteredEventType, journal.VisibilityOperatorVisible, journal.MailboxEventPayload{
			MessageID:  deadLetterName,
			From:       "orchestrator",
			To:         node,
			Path:       filepath.Join("dead-letter", deadLetterName),
			SourcePath: filepath.Join("post", filename),
			Content:    MessageContent(deadLetterName, cfg.MessageBodyBytes),
		}, cfg.Now.Add(time.Duration(eventCount+1)*time.Millisecond))
		fixture.DeadLetterRecords++
		eventCount++
	}

	messageIndex := 0
	for eventCount < cfg.MailboxEventRecords {
		node := cfg.Nodes[messageIndex%len(cfg.Nodes)]
		filename := MessageFilename(messageIndex+cfg.MailboxEventRecords, "orchestrator", node)
		content := MessageContent(filename, cfg.MessageBodyBytes)
		appendLoadedMailboxEvent(tb, writer, projection.MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
			MessageID: filename,
			From:      "orchestrator",
			To:        node,
			Path:      filepath.Join("inbox", node, filename),
			Content:   content,
		}, cfg.Now.Add(time.Duration(eventCount+1)*time.Millisecond))
		fixture.DeliveredRecords++
		eventCount++

		if eventCount < cfg.MailboxEventRecords && fixture.ReadArchiveRecords < cfg.ReadArchiveRecords {
			readRel := filepath.Join("read", filename)
			writeFixtureFile(tb, filepath.Join(sessionDir, readRel), content)
			appendLoadedMailboxEvent(tb, writer, projection.MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, journal.MailboxEventPayload{
				MessageID: filename,
				From:      "orchestrator",
				To:        node,
				Path:      readRel,
				Content:   content,
			}, cfg.Now.Add(time.Duration(eventCount+1)*time.Millisecond))
			fixture.ReadArchiveRecords++
			eventCount++
		}
		messageIndex++
	}
	fixture.MailboxEventRecords = eventCount

	for i := 0; i < cfg.StatusSnapshots; i++ {
		snapshot := LoadedSessionStatusSnapshot(fixture, i)
		if _, err := writer.AppendEvent(projection.SessionStatusSnapshotEventType, journal.VisibilityControlPlaneOnly, snapshot, cfg.Now.Add(time.Duration(eventCount+i+1)*time.Millisecond)); err != nil {
			tb.Fatalf("AppendEvent(session status snapshot %d): %v", i, err)
		}
		fixture.StatusSnapshots++
	}

	writeDaemonSubmitBacklog(tb, sessionDir, cfg, &fixture)

	return fixture
}

func LoadedSessionStatusSnapshot(fixture LoadedSession, generation int) status.SessionStatus {
	nodes := make([]status.NodeStatus, 0, len(fixture.Nodes))
	windows := []status.SessionWindow{{Index: "0"}}
	for i, nodeName := range fixture.Nodes {
		inboxCount := fixture.DeliveredRecords / len(fixture.Nodes)
		if i < fixture.DeliveredRecords%len(fixture.Nodes) {
			inboxCount++
		}
		if i < fixture.ReadArchiveRecords%len(fixture.Nodes) {
			inboxCount--
		}
		if inboxCount < 0 {
			inboxCount = 0
		}
		nodes = append(nodes, status.NodeStatus{
			Name:         nodeName,
			PaneID:       fmt.Sprintf("%%%d", 100+i),
			PaneState:    "idle",
			VisibleState: "idle",
			InboxCount:   inboxCount,
		})
		windows[0].Nodes = append(windows[0].Nodes, status.WindowNode{Name: nodeName})
	}
	return status.SessionStatus{
		SchemaVersion: status.SchemaVersion,
		ContextID:     fixture.ContextID,
		SessionName:   fixture.SessionName,
		NodeCount:     len(nodes),
		VisibleState:  "idle",
		Compact:       fmt.Sprintf("loaded-%d", generation),
		Queues: status.SessionQueues{
			PostCount:       fixture.PostRecords,
			InboxCount:      fixture.DeliveredRecords - fixture.ReadArchiveRecords,
			DeadLetterCount: fixture.DeadLetterRecords,
		},
		Nodes:   nodes,
		Windows: windows,
	}
}

func MessageFilename(index int, from, to string) string {
	return fmt.Sprintf("20260521-%06d-r%04x-from-%s-to-%s.md", index, index%0xffff, from, to)
}

func MessageContent(messageID string, bytesHint int) string {
	header := fmt.Sprintf("---\nparams:\n  messageId: %s\n---\n\n", messageID)
	if bytesHint <= len(header) {
		return header
	}
	return header + strings.Repeat("x", bytesHint-len(header))
}

func WriteInboxMessage(tb testing.TB, sessionDir, node string, index int, content string) string {
	tb.Helper()
	filename := MessageFilename(index, "orchestrator", node)
	if content == "" {
		content = MessageContent(filename, defaultLoadedSessionMessageBodyBytes)
	}
	writeFixtureFile(tb, filepath.Join(sessionDir, "inbox", node, filename), content)
	return filename
}

func SeedInboxBacklog(tb testing.TB, sessionDir, node string, count int, startIndex int) []string {
	tb.Helper()
	filenames := make([]string, 0, count)
	for i := 0; i < count; i++ {
		filenames = append(filenames, WriteInboxMessage(tb, sessionDir, node, startIndex+i, "benchmark pop\n"))
	}
	return filenames
}

func normalizeLoadedSessionConfig(cfg LoadedSessionConfig) LoadedSessionConfig {
	defaults := DefaultLoadedSessionConfig()
	if cfg.ContextID == "" {
		cfg.ContextID = defaults.ContextID
	}
	if cfg.SessionName == "" {
		cfg.SessionName = defaults.SessionName
	}
	if len(cfg.Nodes) == 0 {
		cfg.Nodes = defaults.Nodes
	}
	if cfg.MailboxEventRecords <= 0 {
		cfg.MailboxEventRecords = defaults.MailboxEventRecords
	}
	if cfg.ReadArchiveRecords < 0 {
		cfg.ReadArchiveRecords = 0
	}
	if cfg.ReadArchiveRecords == 0 {
		cfg.ReadArchiveRecords = defaults.ReadArchiveRecords
	}
	if cfg.PostRecords < 0 {
		cfg.PostRecords = 0
	}
	if cfg.PostRecords == 0 {
		cfg.PostRecords = defaults.PostRecords
	}
	if cfg.DeadLetterRecords < 0 {
		cfg.DeadLetterRecords = 0
	}
	if cfg.DeadLetterRecords == 0 {
		cfg.DeadLetterRecords = defaults.DeadLetterRecords
	}
	if cfg.StatusSnapshots < 0 {
		cfg.StatusSnapshots = 0
	}
	if cfg.StatusSnapshots == 0 {
		cfg.StatusSnapshots = defaults.StatusSnapshots
	}
	if cfg.DaemonSubmitRequests < 0 {
		cfg.DaemonSubmitRequests = 0
	}
	if cfg.DaemonSubmitRequests == 0 {
		cfg.DaemonSubmitRequests = defaults.DaemonSubmitRequests
	}
	if cfg.DaemonSubmitResponses < 0 {
		cfg.DaemonSubmitResponses = 0
	}
	if cfg.DaemonSubmitResponses == 0 {
		cfg.DaemonSubmitResponses = defaults.DaemonSubmitResponses
	}
	if cfg.MessageBodyBytes <= 0 {
		cfg.MessageBodyBytes = defaults.MessageBodyBytes
	}
	if cfg.Now.IsZero() {
		cfg.Now = defaults.Now
	}
	return cfg
}

func appendLoadedMailboxEvent(tb testing.TB, writer *journal.Writer, eventType string, visibility journal.Visibility, payload journal.MailboxEventPayload, now time.Time) {
	tb.Helper()
	if payload.Directory == "" {
		payload.Directory = mailboxDirectoryName(eventType)
	}
	if _, err := writer.AppendEvent(eventType, visibility, payload, now); err != nil {
		tb.Fatalf("AppendEvent(%s %s): %v", eventType, payload.MessageID, err)
	}
}

func mailboxDirectoryName(eventType string) string {
	switch eventType {
	case projection.MailboxProjectionPostedEventType, projection.MailboxProjectionPostConsumedEventType:
		return "post"
	case projection.MailboxProjectionDeliveredEventType:
		return "inbox"
	case projection.MailboxProjectionReadEventType:
		return "read"
	case projection.MailboxProjectionDeadLetteredEventType:
		return "dead-letter"
	default:
		return ""
	}
}

func writeDaemonSubmitBacklog(tb testing.TB, sessionDir string, cfg LoadedSessionConfig, fixture *LoadedSession) {
	tb.Helper()
	for i := 0; i < cfg.DaemonSubmitRequests; i++ {
		_, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
			RequestID: fmt.Sprintf("fixture-request-%06d", i),
			Command:   projection.DaemonSubmitSend,
			CreatedAt: cfg.Now.Add(time.Duration(i) * time.Millisecond).UTC().Format(time.RFC3339Nano),
			Filename:  MessageFilename(i, "orchestrator", "worker"),
			Content:   strings.Repeat("q", defaultLoadedSessionDaemonSnapshotBodySize),
		})
		if err != nil {
			tb.Fatalf("WriteDaemonSubmitRequest(%d): %v", i, err)
		}
		fixture.DaemonSubmitRequests++
	}
	for i := 0; i < cfg.DaemonSubmitResponses; i++ {
		_, err := projection.WriteDaemonSubmitResponse(sessionDir, projection.DaemonSubmitResponse{
			RequestID:    fmt.Sprintf("fixture-response-%06d", i),
			Command:      projection.DaemonSubmitPop,
			HandledAt:    cfg.Now.Add(time.Duration(i) * time.Millisecond).UTC().Format(time.RFC3339Nano),
			Empty:        i%2 == 0,
			Filename:     MessageFilename(i, "orchestrator", "worker"),
			Content:      strings.Repeat("r", defaultLoadedSessionDaemonSnapshotBodySize),
			UnreadBefore: i,
		})
		if err != nil {
			tb.Fatalf("WriteDaemonSubmitResponse(%d): %v", i, err)
		}
		fixture.DaemonSubmitResponses++
	}
}

func writeFixtureFile(tb testing.TB, path, content string) {
	tb.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		tb.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		tb.Fatalf("WriteFile(%s): %v", path, err)
	}
}
