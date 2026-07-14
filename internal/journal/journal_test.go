package journal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOpenShadowWriter_BootstrapsOwnerOnlyState(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 17, 0, 0, 0, time.UTC)

	writer, err := OpenShadowWriter(sessionDir, "ctx-main", "main", 4242, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	if writer.session.SessionKey == "" {
		t.Fatal("OpenShadowWriter() returned empty session key")
	}
	if writer.lease.LeaseID == "" {
		t.Fatal("OpenShadowWriter() returned empty lease ID")
	}
	if writer.lease.LeaseEpoch != 1 {
		t.Fatalf("OpenShadowWriter() lease epoch = %d, want 1", writer.lease.LeaseEpoch)
	}

	assertOwnerOnlyMode(t, filepath.Join(sessionDir, "journal"), 0o700)
	assertOwnerOnlyMode(t, filepath.Join(sessionDir, "journal", "records"), 0o700)
	assertOwnerOnlyMode(t, filepath.Join(sessionDir, "snapshot"), 0o700)
	assertOwnerOnlyMode(t, filepath.Join(sessionDir, "lease"), 0o700)
	assertOwnerOnlyMode(t, SessionStatePath(sessionDir), 0o600)
	assertOwnerOnlyMode(t, CurrentLeasePath(sessionDir), 0o600)

	events, err := Replay(sessionDir)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("Replay() returned %d events, want 2 bootstrap events", len(events))
	}
	if events[0].Type != leaseAcquiredEventType {
		t.Fatalf("events[0].Type = %q, want %q", events[0].Type, leaseAcquiredEventType)
	}
	if events[1].Type != sessionResolvedEventType {
		t.Fatalf("events[1].Type = %q, want %q", events[1].Type, sessionResolvedEventType)
	}
}

func TestRecordMailboxPayloadPersistsExactInputRequestFields(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 17, 2, 0, 0, time.UTC)

	manager := NewManager("ctx-main", 4242)
	if err := manager.RecordMailboxPayload(sessionDir, "main", "mailbox_projection_delivered", VisibilityMailboxProjection, MailboxEventPayload{
		MessageID:           "m1.md",
		From:                "orchestrator",
		To:                  "worker",
		ThreadID:            "thread_1",
		InputRequestID:      "ireq_123",
		FillsInputRequestID: "ireq_prev",
		InputRequestSetID:   "ireqset_1",
		BranchID:            "branch_1",
		CompletionRule:      "all",
		Content:             "payload",
	}, now); err != nil {
		t.Fatalf("RecordMailboxPayload() error = %v", err)
	}

	events, err := Replay(sessionDir)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("Replay() returned %d events, want 3", len(events))
	}
	if events[2].ThreadID != "thread_1" {
		t.Fatalf("event.ThreadID = %q, want thread_1", events[2].ThreadID)
	}
	var payload MailboxEventPayload
	if err := json.Unmarshal(events[2].Payload, &payload); err != nil {
		t.Fatalf("json.Unmarshal(payload): %v", err)
	}
	if payload.InputRequestID != "ireq_123" {
		t.Fatalf("payload.InputRequestID = %q, want ireq_123", payload.InputRequestID)
	}
	if payload.FillsInputRequestID != "ireq_prev" {
		t.Fatalf("payload.FillsInputRequestID = %q, want ireq_prev", payload.FillsInputRequestID)
	}
	if payload.InputRequestSetID != "ireqset_1" {
		t.Fatalf("payload.InputRequestSetID = %q, want ireqset_1", payload.InputRequestSetID)
	}
	if payload.BranchID != "branch_1" || payload.CompletionRule != "all" {
		t.Fatalf("group fields = %q/%q, want branch_1/all", payload.BranchID, payload.CompletionRule)
	}
}

func TestAppendCurrentSessionEventIfAbsentDedupesUnderAppendFence(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 17, 2, 30, 0, time.UTC)

	writer, err := OpenShadowWriter(sessionDir, "ctx-main", "main", 4242, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	payload := MailboxEventPayload{
		MessageID: "m1.verdict-none",
		From:      "orchestrator",
		To:        "worker",
		Content:   "verdictOf: ireq_1",
	}
	equivalent := func(event Event) (bool, error) {
		if event.Type != "verdict_none_timeout" {
			return false, nil
		}
		var got MailboxEventPayload
		if err := json.Unmarshal(event.Payload, &got); err != nil {
			return false, err
		}
		return got.MessageID == payload.MessageID, nil
	}

	if _, appended, err := writer.AppendCurrentSessionEventIfAbsent("verdict_none_timeout", VisibilityOperatorVisible, payload, AppendOptions{}, now.Add(time.Second), equivalent); err != nil {
		t.Fatalf("AppendCurrentSessionEventIfAbsent(first) error = %v", err)
	} else if !appended {
		t.Fatal("AppendCurrentSessionEventIfAbsent(first) appended = false, want true")
	}
	if _, appended, err := writer.AppendCurrentSessionEventIfAbsent("verdict_none_timeout", VisibilityOperatorVisible, payload, AppendOptions{}, now.Add(2*time.Second), equivalent); err != nil {
		t.Fatalf("AppendCurrentSessionEventIfAbsent(second) error = %v", err)
	} else if appended {
		t.Fatal("AppendCurrentSessionEventIfAbsent(second) appended = true, want false")
	}

	events, err := Replay(sessionDir)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	count := 0
	for _, event := range events {
		if event.Type == "verdict_none_timeout" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("verdict_none_timeout event count = %d, want 1", count)
	}
}

func TestRecordProcessHelpers_NoManagerNoop(t *testing.T) {
	ClearProcessManager()
	t.Cleanup(ClearProcessManager)

	now := time.Date(2026, time.April, 14, 17, 3, 0, 0, time.UTC)
	tests := []struct {
		name   string
		record func(sessionDir string) error
	}{
		{
			name: "mailbox event",
			record: func(sessionDir string) error {
				return RecordProcessMailboxEvent(sessionDir, "main", "mailbox_projection_read", VisibilityOperatorVisible, "m1.md", "worker", "orchestrator", filepath.Join("read", "m1.md"), now)
			},
		},
		{
			name: "event",
			record: func(sessionDir string) error {
				return RecordProcessEvent(sessionDir, "main", "mailbox_projection_posted", VisibilityMailboxProjection, map[string]string{"path": "post/m1.md"}, now)
			},
		},
		{
			name: "event with options",
			record: func(sessionDir string) error {
				return RecordProcessEventWithOptions(sessionDir, "main", "approval_requested", VisibilityOperatorVisible, map[string]string{"path": "post/m1.md"}, AppendOptions{ThreadID: "thread-1"}, now)
			},
		},
		{
			name: "mailbox payload",
			record: func(sessionDir string) error {
				return RecordProcessMailboxPayload(sessionDir, "main", "mailbox_projection_delivered", VisibilityMailboxProjection, MailboxEventPayload{
					MessageID: "m1.md",
					From:      "worker",
					To:        "orchestrator",
					Path:      filepath.Join("inbox", "orchestrator", "m1.md"),
				}, now)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionDir := t.TempDir()
			if err := tt.record(sessionDir); err != nil {
				t.Fatalf("record() error = %v", err)
			}
			if _, err := os.Stat(filepath.Join(sessionDir, "journal")); !os.IsNotExist(err) {
				t.Fatalf("process helper wrote journal state without manager: Stat() error = %v", err)
			}
		})
	}
}

func TestResolveSession_ResumeCurrentKeepsSessionKeyAndGeneration(t *testing.T) {
	sessionDir := t.TempDir()
	firstNow := time.Date(2026, time.April, 14, 17, 5, 0, 0, time.UTC)
	secondNow := firstNow.Add(5 * time.Minute)

	first, firstResult, err := ResolveSession(sessionDir, "main", ResolutionResumeCurrent, firstNow)
	if err != nil {
		t.Fatalf("ResolveSession(first) error = %v", err)
	}
	if firstResult != ResolutionCreated {
		t.Fatalf("ResolveSession(first) result = %q, want %q", firstResult, ResolutionCreated)
	}

	second, secondResult, err := ResolveSession(sessionDir, "main", ResolutionResumeCurrent, secondNow)
	if err != nil {
		t.Fatalf("ResolveSession(second) error = %v", err)
	}
	if secondResult != ResolutionResumed {
		t.Fatalf("ResolveSession(second) result = %q, want %q", secondResult, ResolutionResumed)
	}
	if second.SessionKey != first.SessionKey {
		t.Fatalf("session key changed on resume: got %q want %q", second.SessionKey, first.SessionKey)
	}
	if second.Generation != first.Generation {
		t.Fatalf("generation changed on resume: got %d want %d", second.Generation, first.Generation)
	}
}

func TestResolveSession_ExplicitRotationAdvancesGeneration(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 17, 10, 0, 0, time.UTC)

	initial, _, err := ResolveSession(sessionDir, "main", ResolutionResumeCurrent, now)
	if err != nil {
		t.Fatalf("ResolveSession(initial) error = %v", err)
	}

	rebound, reboundResult, err := ResolveSession(sessionDir, "main", ResolutionExplicitRebind, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ResolveSession(rebind) error = %v", err)
	}
	if reboundResult != ResolutionRotatedRebind {
		t.Fatalf("ResolveSession(rebind) result = %q, want %q", reboundResult, ResolutionRotatedRebind)
	}
	if rebound.SessionKey != initial.SessionKey {
		t.Fatalf("session key changed on explicit rebind: got %q want %q", rebound.SessionKey, initial.SessionKey)
	}
	if rebound.Generation != initial.Generation+1 {
		t.Fatalf("rebind generation = %d, want %d", rebound.Generation, initial.Generation+1)
	}

	rotated, rotatedResult, err := ResolveSession(sessionDir, "main", ResolutionExplicitNewSession, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("ResolveSession(new session) error = %v", err)
	}
	if rotatedResult != ResolutionRotatedNewSession {
		t.Fatalf("ResolveSession(new session) result = %q, want %q", rotatedResult, ResolutionRotatedNewSession)
	}
	if rotated.SessionKey != initial.SessionKey {
		t.Fatalf("session key changed on explicit new-session rotation: got %q want %q", rotated.SessionKey, initial.SessionKey)
	}
	if rotated.Generation != rebound.Generation+1 {
		t.Fatalf("new-session generation = %d, want %d", rotated.Generation, rebound.Generation+1)
	}
}

func TestAppendEvent_FailsWhenLeaseChanges(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 17, 15, 0, 0, time.UTC)

	writer, err := OpenShadowWriter(sessionDir, "ctx-main", "main", 111, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	if _, err := writer.AppendEvent("mailbox_projection_posted", VisibilityMailboxProjection, map[string]string{"path": "post/test.md"}, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendEvent(initial) error = %v", err)
	}

	replacedLease := Lease{
		SchemaVersion:     schemaVersion,
		LeaseID:           "replacement-lease",
		LeaseEpoch:        writer.lease.LeaseEpoch + 1,
		HolderContextID:   "ctx-other",
		HolderSessionName: "main",
		HolderPID:         222,
		AcquiredAt:        now.Add(2 * time.Second).Format(time.RFC3339),
	}
	if err := writeJSONAtomically(CurrentLeasePath(sessionDir), replacedLease); err != nil {
		t.Fatalf("writeJSONAtomically(current lease): %v", err)
	}

	if _, err := writer.AppendEvent("mailbox_projection_posted", VisibilityMailboxProjection, map[string]string{"path": "post/test-2.md"}, now.Add(3*time.Second)); err == nil {
		t.Fatal("AppendEvent() error = nil, want lease mismatch")
	}
}

func TestAppendEvent_FencesLeaseAuthorityThroughCommit(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 17, 16, 0, 0, time.UTC)

	writer, err := OpenShadowWriter(sessionDir, "ctx-main", "main", 111, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	beforeWrite := make(chan struct{}, 1)
	acquired := make(chan Lease, 1)
	acquireErr := make(chan error, 1)

	appendEventBeforeWriteHook = func() error {
		beforeWrite <- struct{}{}
		go func() {
			lease, err := AcquireLease(sessionDir, writer.session, "ctx-other", "main", 222, now.Add(2*time.Second))
			if err != nil {
				acquireErr <- err
				return
			}
			acquired <- lease
		}()

		select {
		case err := <-acquireErr:
			return fmt.Errorf("AcquireLease() completed before append commit: %w", err)
		case lease := <-acquired:
			return fmt.Errorf("AcquireLease() completed before append commit with lease %s/%d", lease.LeaseID, lease.LeaseEpoch)
		case <-time.After(150 * time.Millisecond):
		}

		current, err := readCurrentLease(sessionDir)
		if err != nil {
			return err
		}
		if current.LeaseID != writer.lease.LeaseID || current.LeaseEpoch != writer.lease.LeaseEpoch {
			return fmt.Errorf("current lease changed before append commit: got %s/%d want %s/%d", current.LeaseID, current.LeaseEpoch, writer.lease.LeaseID, writer.lease.LeaseEpoch)
		}
		return nil
	}
	defer func() {
		appendEventBeforeWriteHook = nil
	}()

	event, err := writer.AppendEvent("mailbox_projection_posted", VisibilityMailboxProjection, map[string]string{"path": "post/test-fenced.md"}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	select {
	case <-beforeWrite:
	default:
		t.Fatal("AppendEvent() did not reach before-write hook")
	}

	select {
	case err := <-acquireErr:
		t.Fatalf("AcquireLease() error = %v", err)
	case newLease := <-acquired:
		if newLease.LeaseEpoch != writer.lease.LeaseEpoch+1 {
			t.Fatalf("AcquireLease() lease epoch = %d, want %d", newLease.LeaseEpoch, writer.lease.LeaseEpoch+1)
		}
		current, err := readCurrentLease(sessionDir)
		if err != nil {
			t.Fatalf("readCurrentLease() error = %v", err)
		}
		if current.LeaseID != newLease.LeaseID || current.LeaseEpoch != newLease.LeaseEpoch {
			t.Fatalf("current lease = %s/%d, want %s/%d", current.LeaseID, current.LeaseEpoch, newLease.LeaseID, newLease.LeaseEpoch)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AcquireLease() did not complete after append commit")
	}

	if event.LeaseID != writer.lease.LeaseID || event.LeaseEpoch != writer.lease.LeaseEpoch {
		t.Fatalf("event lease = %s/%d, want writer lease %s/%d", event.LeaseID, event.LeaseEpoch, writer.lease.LeaseID, writer.lease.LeaseEpoch)
	}
}

func TestWithAppendAuthorityFence_ReturnsLockFailure(t *testing.T) {
	lockCalls := 0
	err := withAppendAuthorityFenceLock(t.TempDir(), func(fd int, how int) error {
		if how != syscall.LOCK_EX {
			t.Fatalf("lock called with how = %d, want LOCK_EX", how)
		}
		lockCalls++
		return errors.New("lock denied")
	}, func() error {
		t.Fatal("critical section ran after lock failure")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "locking append authority fence: lock denied") {
		t.Fatalf("withAppendAuthorityFenceLock() error = %v, want lock denied", err)
	}
	if lockCalls != 1 {
		t.Fatalf("lockCalls = %d, want 1", lockCalls)
	}
}

func TestReplay_FailsClosedOnSequenceAndLeaseDefects(t *testing.T) {
	t.Run("sequence gap", func(t *testing.T) {
		sessionDir := t.TempDir()
		writeCommittedRecord(t, sessionDir, 1, leaseAcquiredEventType, "lease-a", 1)
		writeCommittedRecord(t, sessionDir, 3, sessionResolvedEventType, "lease-a", 1)

		if _, err := Replay(sessionDir); err == nil || !strings.Contains(err.Error(), "sequence gap") {
			t.Fatalf("Replay() error = %v, want sequence gap", err)
		}
	})

	t.Run("duplicate sequence", func(t *testing.T) {
		sessionDir := t.TempDir()
		writeCommittedRecordNamed(t, sessionDir, "000000000001-a.json", 1, leaseAcquiredEventType, "lease-a", 1)
		writeCommittedRecordNamed(t, sessionDir, "000000000001-b.json", 1, sessionResolvedEventType, "lease-a", 1)

		if _, err := Replay(sessionDir); err == nil || !strings.Contains(err.Error(), "duplicate sequence") {
			t.Fatalf("Replay() error = %v, want duplicate sequence", err)
		}
	})

	t.Run("malformed committed record", func(t *testing.T) {
		sessionDir := t.TempDir()
		if err := os.MkdirAll(RecordsDir(sessionDir), 0o700); err != nil {
			t.Fatalf("MkdirAll(records): %v", err)
		}
		if err := os.WriteFile(filepath.Join(RecordsDir(sessionDir), "000000000001-bad.json"), []byte("{"), 0o600); err != nil {
			t.Fatalf("WriteFile(malformed): %v", err)
		}

		if _, err := Replay(sessionDir); err == nil || !strings.Contains(err.Error(), "unexpected end of JSON input") {
			t.Fatalf("Replay() error = %v, want malformed JSON failure", err)
		}
	})

	t.Run("lease mismatch", func(t *testing.T) {
		sessionDir := t.TempDir()
		writeCommittedRecord(t, sessionDir, 1, leaseAcquiredEventType, "lease-a", 1)
		writeCommittedRecord(t, sessionDir, 2, "mailbox_projection_posted", "lease-b", 2)

		if _, err := Replay(sessionDir); err == nil || !strings.Contains(err.Error(), "lease mismatch") {
			t.Fatalf("Replay() error = %v, want lease mismatch", err)
		}
	})

	t.Run("non monotonic lease epoch", func(t *testing.T) {
		sessionDir := t.TempDir()
		writeCommittedRecord(t, sessionDir, 1, leaseAcquiredEventType, "lease-a", 2)
		writeCommittedRecord(t, sessionDir, 2, leaseAcquiredEventType, "lease-b", 1)

		if _, err := Replay(sessionDir); err == nil || !strings.Contains(err.Error(), "non-monotonic lease epoch") {
			t.Fatalf("Replay() error = %v, want non-monotonic lease epoch", err)
		}
	})
}

func TestReplayEach_StreamsValidatedEventsAndStopsOnCallbackError(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 17, 25, 0, 0, time.UTC)
	writer, err := OpenShadowWriter(sessionDir, "ctx-main", "main", 111, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	if _, err := writer.AppendEvent("mailbox_projection_posted", VisibilityMailboxProjection, map[string]string{"path": "post/test.md"}, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	wantErr := errors.New("stop streaming")
	var sequences []int
	err = ReplayEach(sessionDir, func(event Event) error {
		sequences = append(sequences, event.Sequence)
		if event.Sequence == 2 {
			return wantErr
		}
		return nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ReplayEach() error = %v, want callback error", err)
	}
	if len(sequences) != 2 || sequences[0] != 1 || sequences[1] != 2 {
		t.Fatalf("ReplayEach() streamed sequences %#v, want [1 2]", sequences)
	}

	events, err := Replay(sessionDir)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("Replay() returned %d events, want 3", len(events))
	}
}

func assertOwnerOnlyMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func writeCommittedRecord(t *testing.T, sessionDir string, sequence int, eventType, leaseID string, leaseEpoch int) {
	t.Helper()
	writeCommittedRecordNamed(t, sessionDir, recordName(sequence), sequence, eventType, leaseID, leaseEpoch)
}

func writeCommittedRecordNamed(t *testing.T, sessionDir, name string, sequence int, eventType, leaseID string, leaseEpoch int) {
	t.Helper()

	if err := os.MkdirAll(RecordsDir(sessionDir), 0o700); err != nil {
		t.Fatalf("MkdirAll(records): %v", err)
	}
	event := Event{
		SchemaVersion:   schemaVersion,
		Sequence:        sequence,
		EventID:         "evt",
		Type:            eventType,
		Visibility:      VisibilityControlPlaneOnly,
		SessionKey:      "session-key",
		TmuxSessionName: "main",
		Generation:      1,
		LeaseID:         leaseID,
		LeaseEpoch:      leaseEpoch,
		OccurredAt:      time.Date(2026, time.April, 14, 17, 20, 0, 0, time.UTC).Format(time.RFC3339),
		Payload:         mustMarshalPayload(t, map[string]string{"ok": "true"}),
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal(event): %v", err)
	}
	if err := os.WriteFile(filepath.Join(RecordsDir(sessionDir), name), data, 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", name, err)
	}
}

func mustMarshalPayload(t *testing.T, value interface{}) []byte {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(payload): %v", err)
	}
	return data
}

func readCurrentLease(sessionDir string) (Lease, error) {
	var lease Lease
	if err := readJSONFile(CurrentLeasePath(sessionDir), &lease); err != nil {
		return Lease{}, err
	}
	return lease, nil
}

func recordName(sequence int) string {
	return fmt.Sprintf("%012d-evt.json", sequence)
}
