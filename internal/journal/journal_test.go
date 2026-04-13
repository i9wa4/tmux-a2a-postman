package journal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	if _, err := writer.AppendEvent("compatibility_mailbox_posted", VisibilityCompatibilityMailbox, map[string]string{"path": "post/test.md"}, now.Add(time.Second)); err != nil {
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

	if _, err := writer.AppendEvent("compatibility_mailbox_posted", VisibilityCompatibilityMailbox, map[string]string{"path": "post/test-2.md"}, now.Add(3*time.Second)); err == nil {
		t.Fatal("AppendEvent() error = nil, want lease mismatch")
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
		writeCommittedRecord(t, sessionDir, 2, "compatibility_mailbox_posted", "lease-b", 2)

		if _, err := Replay(sessionDir); err == nil || !strings.Contains(err.Error(), "lease mismatch") {
			t.Fatalf("Replay() error = %v, want lease mismatch", err)
		}
	})
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

func recordName(sequence int) string {
	return fmt.Sprintf("%012d-evt.json", sequence)
}
