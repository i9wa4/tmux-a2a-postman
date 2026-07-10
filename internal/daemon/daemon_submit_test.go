package daemon

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/runtimeprofile"
)

func TestProcessDaemonSubmitRequest_SendWritesPostFile(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-send",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  "20260414-033100-from-orchestrator-to-worker.md",
		Content:   "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:31:00Z\n---\n\nsubmit payload\n",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}

	postPath := filepath.Join(sessionDir, "post", "20260414-033100-from-orchestrator-to-worker.md")
	got, err := os.ReadFile(postPath)
	if err != nil {
		t.Fatalf("ReadFile postPath: %v", err)
	}
	if string(got) != "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:31:00Z\n---\n\nsubmit payload\n" {
		t.Fatalf("post payload changed:\n got %q", string(got))
	}

	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-send"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Filename != "20260414-033100-from-orchestrator-to-worker.md" {
		t.Fatalf("response.Filename = %q", response.Filename)
	}
}

func TestProcessDaemonSubmitRequest_PopArchivesUnreadMessage(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	oldest := "20260414-033200-from-orchestrator-to-worker.md"
	newest := "20260414-033201-from-orchestrator-to-worker.md"
	if err := os.WriteFile(filepath.Join(inboxDir, oldest), []byte("---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:32:00Z\n---\n\noldest\n"), 0o600); err != nil {
		t.Fatalf("WriteFile oldest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inboxDir, newest), []byte("---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:32:01Z\n---\n\nnewest\n"), 0o600); err != nil {
		t.Fatalf("WriteFile newest: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}

	if _, err := os.Stat(filepath.Join(sessionDir, "read", oldest)); err != nil {
		t.Fatalf("archived read file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inboxDir, oldest)); !os.IsNotExist(err) {
		t.Fatalf("oldest inbox file still present or wrong error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inboxDir, newest)); err != nil {
		t.Fatalf("newest inbox file missing: %v", err)
	}

	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-pop"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Filename != oldest {
		t.Fatalf("response.Filename = %q, want %q", response.Filename, oldest)
	}
	if response.UnreadBefore != 2 {
		t.Fatalf("response.UnreadBefore = %d, want 2", response.UnreadBefore)
	}
}

func TestProcessDaemonSubmitRequest_PopRecordsReadBeforeProjectionSync(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	now := time.Date(2026, time.May, 4, 6, 5, 0, 0, time.UTC)
	installShadowJournalManager(sessionDir, "ctx-main", "review-session", now)
	t.Cleanup(journal.ClearProcessManager)

	filename := "20260504-150109-sfb93-r001f-from-orchestrator-to-worker.md"
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  messageId: " + filename + "\n  replyPolicy: required\n  timestamp: 2026-05-04T15:01:09+09:00\n---\n\nplease work\n"
	recordMailboxProjectionPayload(sessionDir, "review-session", projection.MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: filename,
		From:      "orchestrator",
		To:        "worker",
		Path:      filepath.Join("inbox", "worker", filename),
		Content:   content,
	})
	syncMailboxProjection(sessionDir)

	inboxPath := filepath.Join(sessionDir, "inbox", "worker", filename)
	if _, err := os.Stat(inboxPath); err != nil {
		t.Fatalf("projected inbox file missing before pop: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-project",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: now.Add(time.Second).UTC().Format(time.RFC3339),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	readPath := filepath.Join(sessionDir, "read", filename)
	if _, err := os.Stat(readPath); err != nil {
		t.Fatalf("read file missing after pop: %v", err)
	}
	if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
		t.Fatalf("inbox file still present after pop or wrong error: %v", err)
	}

	if err := projection.SyncMailboxProjection(sessionDir); err != nil {
		t.Fatalf("SyncMailboxProjection(after pop): %v", err)
	}
	if _, err := os.Stat(readPath); err != nil {
		t.Fatalf("read file missing after projection sync: %v", err)
	}
	if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
		t.Fatalf("projection sync resurrected popped inbox file or wrong error: %v", err)
	}

	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-pop-project"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Filename != filename {
		t.Fatalf("response.Filename = %q, want %q", response.Filename, filename)
	}
}

func TestProcessDaemonSubmitRequest_RuntimeProfileStdoutReturnsBoundedPayload(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID:          "req-profile",
		Command:            projection.DaemonSubmitRuntimeProfile,
		CreatedAt:          time.Now().UTC().Format(time.RFC3339),
		ProfileKind:        runtimeprofile.KindGoroutine,
		ProfileDestination: "stdout",
		ProfileMaxBytes:    runtimeprofile.DefaultMaxBytes,
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}

	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-profile"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Error != "" {
		t.Fatalf("response.Error = %q", response.Error)
	}
	if response.RuntimeProfile == nil {
		t.Fatal("RuntimeProfile = nil")
	}
	if response.RuntimeProfile.Kind != runtimeprofile.KindGoroutine ||
		response.RuntimeProfile.Destination != "stdout" ||
		response.RuntimeProfile.Encoding != "base64" ||
		response.RuntimeProfile.OutputPath != "" {
		t.Fatalf("RuntimeProfile metadata = %#v", response.RuntimeProfile)
	}
	data, err := base64.StdEncoding.DecodeString(response.RuntimeProfile.ContentBase64)
	if err != nil {
		t.Fatalf("DecodeString(ContentBase64): %v", err)
	}
	if len(data) == 0 || len(data) != response.RuntimeProfile.Bytes {
		t.Fatalf("profile payload bytes = %d, response bytes = %d", len(data), response.RuntimeProfile.Bytes)
	}
	if response.Content != "" || response.MarkdownPath != "" {
		t.Fatalf("profile response leaked message fields: content=%q markdown_path=%q", response.Content, response.MarkdownPath)
	}
}

func TestProcessDaemonSubmitRequest_RuntimeProfileFileWritesExplicitPathOnly(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	outputPath := filepath.Join(t.TempDir(), "goroutine.pprof")

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID:          "req-profile-file",
		Command:            projection.DaemonSubmitRuntimeProfile,
		CreatedAt:          time.Now().UTC().Format(time.RFC3339),
		ProfileKind:        runtimeprofile.KindGoroutine,
		ProfileDestination: "file",
		ProfileOutputPath:  outputPath,
		ProfileMaxBytes:    runtimeprofile.DefaultMaxBytes,
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}

	written, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile profile output: %v", err)
	}
	if len(written) == 0 {
		t.Fatal("written profile is empty")
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-profile-file"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Error != "" {
		t.Fatalf("response.Error = %q", response.Error)
	}
	if response.RuntimeProfile == nil {
		t.Fatal("RuntimeProfile = nil")
	}
	if response.RuntimeProfile.ContentBase64 != "" {
		t.Fatal("file response should not include profile content")
	}
	if response.RuntimeProfile.OutputPath != outputPath {
		t.Fatalf("OutputPath = %q, want explicit output path %q", response.RuntimeProfile.OutputPath, outputPath)
	}
	if response.RuntimeProfile.Bytes != len(written) {
		t.Fatalf("response bytes = %d, written bytes = %d", response.RuntimeProfile.Bytes, len(written))
	}
}

func TestProcessDaemonSubmitRequest_RuntimeProfileRequiresExplicitDestination(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID:   "req-profile-no-destination",
		Command:     projection.DaemonSubmitRuntimeProfile,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		ProfileKind: runtimeprofile.KindGoroutine,
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-profile-no-destination"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Error == "" {
		t.Fatal("response.Error = empty, want destination error")
	}
	if response.RuntimeProfile != nil {
		t.Fatalf("RuntimeProfile = %#v, want nil on error", response.RuntimeProfile)
	}
}

func TestProcessDaemonSubmitRequest_QueueMsThresholdExceededEmitsWarning(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	var buf bytes.Buffer
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
	})

	// CreatedAt far in the past to guarantee queue_ms > 30,000 ms.
	staleCreatedAt := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano)
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-queue-warn",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: staleCreatedAt,
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}

	logged := buf.String()
	if !strings.Contains(logged, "event=queue_ms_threshold_exceeded") {
		t.Fatalf("expected queue_ms_threshold_exceeded WARNING in log; got:\n%s", logged)
	}
	if !strings.Contains(logged, "threshold_ms=30000") {
		t.Fatalf("expected threshold_ms=30000 in WARNING log; got:\n%s", logged)
	}
}

func TestProcessDaemonSubmitRequest_QueueMsBelowThresholdNoWarning(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	var buf bytes.Buffer
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
	})

	// CreatedAt is current — queue_ms will be well below the 30,000 ms threshold.
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-queue-no-warn",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}

	logged := buf.String()
	if strings.Contains(logged, "event=queue_ms_threshold_exceeded") {
		t.Fatalf("unexpected queue_ms_threshold_exceeded WARNING for fast request; got:\n%s", logged)
	}
}

func TestProcessDaemonSubmitRequest_ConfiguredThresholdIsHonored(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "cfg-threshold-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	// Override the package-level threshold to 1 000 ms (1 s) to prove the
	// configured value is used instead of the default 30 000 ms.
	original := daemonSubmitQueueWarnThresholdMs
	daemonSubmitQueueWarnThresholdMs = 1_000
	t.Cleanup(func() { daemonSubmitQueueWarnThresholdMs = original })

	var buf bytes.Buffer
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
	})

	// CreatedAt 5 s ago: queue_ms ~5 000, which is above 1 000 but well below
	// the default 30 000, proving the custom threshold fires.
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-cfg-threshold",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: time.Now().Add(-5 * time.Second).UTC().Format(time.RFC3339Nano),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}

	logged := buf.String()
	if !strings.Contains(logged, "event=queue_ms_threshold_exceeded") {
		t.Fatalf("expected queue_ms_threshold_exceeded WARNING for custom 1 000 ms threshold; got:\n%s", logged)
	}
	if !strings.Contains(logged, "threshold_ms=1000") {
		t.Fatalf("expected threshold_ms=1000 in WARNING log; got:\n%s", logged)
	}
}

func TestProcessDaemonSubmitRequest_AlreadyClaimedRequestIsNoop(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	oldest := "20260414-033300-from-orchestrator-to-worker.md"
	newest := "20260414-033301-from-orchestrator-to-worker.md"
	if err := os.WriteFile(filepath.Join(inboxDir, oldest), []byte("oldest"), 0o600); err != nil {
		t.Fatalf("WriteFile oldest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inboxDir, newest), []byte("newest"), 0o600); err != nil {
		t.Fatalf("WriteFile newest: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-once",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest(first): %v", err)
	}
	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest(second): %v", err)
	}
	if _, err := os.Stat(filepath.Join(inboxDir, newest)); err != nil {
		t.Fatalf("newest inbox file should not be popped by duplicate processing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "read", oldest)); err != nil {
		t.Fatalf("oldest read file missing: %v", err)
	}
}

func TestProcessDaemonSubmitRequest_ConcurrentClaimsPopOnlyOnce(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	oldest := "20260414-033400-from-orchestrator-to-worker.md"
	newest := "20260414-033401-from-orchestrator-to-worker.md"
	if err := os.WriteFile(filepath.Join(inboxDir, oldest), []byte("oldest"), 0o600); err != nil {
		t.Fatalf("WriteFile oldest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inboxDir, newest), []byte("newest"), 0o600); err != nil {
		t.Fatalf("WriteFile newest: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-concurrent",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := processDaemonSubmitRequest(requestPath)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("processDaemonSubmitRequest concurrent error: %v", err)
		}
	}

	if _, err := os.Stat(filepath.Join(sessionDir, "read", oldest)); err != nil {
		t.Fatalf("oldest read file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inboxDir, newest)); err != nil {
		t.Fatalf("newest inbox file should not be popped by duplicate concurrent processing: %v", err)
	}
	if _, err := os.Stat(requestPath); !os.IsNotExist(err) {
		t.Fatalf("request file still present or wrong error: %v", err)
	}
	if _, err := os.Stat(requestPath + ".processing"); !os.IsNotExist(err) {
		t.Fatalf("claimed request file still present or wrong error: %v", err)
	}

	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-pop-concurrent"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Filename != oldest {
		t.Fatalf("response.Filename = %q, want %q", response.Filename, oldest)
	}
}

func TestProcessDaemonSubmitRequest_RuntimeProfileFileRefusesOverwriteByDefault(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	outputPath := filepath.Join(t.TempDir(), "goroutine.pprof")
	if err := os.WriteFile(outputPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("WriteFile existing: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID:          "req-profile-no-overwrite",
		Command:            projection.DaemonSubmitRuntimeProfile,
		CreatedAt:          time.Now().UTC().Format(time.RFC3339),
		ProfileKind:        runtimeprofile.KindGoroutine,
		ProfileDestination: "file",
		ProfileOutputPath:  outputPath,
		ProfileMaxBytes:    runtimeprofile.DefaultMaxBytes,
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-profile-no-overwrite"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Error == "" {
		t.Fatal("response.Error = empty, want overwrite refusal error")
	}
	if !strings.Contains(response.Error, "already exists") {
		t.Fatalf("response.Error = %q, want 'already exists'", response.Error)
	}
	got, _ := os.ReadFile(outputPath)
	if string(got) != "existing" {
		t.Fatalf("existing file was modified: %q", string(got))
	}
}

func TestProcessDaemonSubmitRequest_RuntimeProfileFileForceOverwrites(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	outputPath := filepath.Join(t.TempDir(), "goroutine.pprof")
	if err := os.WriteFile(outputPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("WriteFile existing: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID:          "req-profile-force",
		Command:            projection.DaemonSubmitRuntimeProfile,
		CreatedAt:          time.Now().UTC().Format(time.RFC3339),
		ProfileKind:        runtimeprofile.KindGoroutine,
		ProfileDestination: "file",
		ProfileOutputPath:  outputPath,
		ProfileMaxBytes:    runtimeprofile.DefaultMaxBytes,
		ProfileForce:       true,
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-profile-force"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Error != "" {
		t.Fatalf("response.Error = %q, want empty (force overwrite succeeded)", response.Error)
	}
	written, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile profile output: %v", err)
	}
	if string(written) == "existing" {
		t.Fatal("file was not overwritten by --force")
	}
	if len(written) == 0 {
		t.Fatal("overwritten profile is empty")
	}
}

// TestRecordDaemonSubmitPopRead_PrefersFallbackOverTruncatedReadPath
// reproduces the #633 race: readPath is caught mid-truncation (0 bytes) by
// a concurrent projection sync at the exact moment the pop path records its
// read event. The known-good content read from the inbox message before
// archiving (fallbackContent) must win instead of the torn on-disk read.
func TestRecordDaemonSubmitPopRead_PrefersFallbackOverTruncatedReadPath(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	now := time.Date(2026, time.July, 10, 15, 2, 6, 0, time.UTC)
	installShadowJournalManager(sessionDir, "ctx-main", "review-session", now)
	t.Cleanup(journal.ClearProcessManager)

	filename := "20260710-000149-s7c1c-ra364-from-orchestrator-to-guardian.md"
	readPath := filepath.Join(sessionDir, "read", filename)
	if err := os.MkdirAll(filepath.Dir(readPath), 0o700); err != nil {
		t.Fatalf("MkdirAll(read): %v", err)
	}
	// Simulate the torn/truncated file a racing projection sync can leave
	// visible mid-rewrite: present on disk, but 0 bytes.
	if err := os.WriteFile(readPath, nil, 0o600); err != nil {
		t.Fatalf("WriteFile(truncated readPath): %v", err)
	}

	recordDaemonSubmitPopRead(sessionDir, readPath, filename, "full correct body")

	events, err := journal.Replay(sessionDir)
	if err != nil {
		t.Fatalf("journal.Replay() error = %v", err)
	}
	last := events[len(events)-1]
	if last.Type != projection.MailboxProjectionReadEventType {
		t.Fatalf("last event type = %q, want %s", last.Type, projection.MailboxProjectionReadEventType)
	}
	var payload map[string]string
	if err := json.Unmarshal(last.Payload, &payload); err != nil {
		t.Fatalf("json.Unmarshal(payload): %v", err)
	}
	if payload["content"] != "full correct body" {
		t.Fatalf("recorded read content = %q, want full correct body (torn read must not win)", payload["content"])
	}
}
