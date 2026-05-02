package inputbroker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBrokerAcquireRejectsConcurrentPaneLease(t *testing.T) {
	now := time.Date(2026, 5, 2, 1, 0, 0, 0, time.UTC)
	broker := NewWithClock(t.TempDir(), func() time.Time { return now })

	lease, ok, err := broker.Acquire(Request{PaneID: "%11", NodeName: "worker", Owner: "first", TTL: time.Minute})
	if err != nil {
		t.Fatalf("Acquire(first) error = %v", err)
	}
	if !ok {
		t.Fatal("Acquire(first) ok = false, want true")
	}
	if _, ok, err := broker.Acquire(Request{PaneID: "%11", NodeName: "worker", Owner: "second", TTL: time.Minute}); err != nil || ok {
		t.Fatalf("Acquire(second) = ok %v, err %v; want busy without error", ok, err)
	}

	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, ok, err := broker.Acquire(Request{PaneID: "%11", NodeName: "worker", Owner: "second", TTL: time.Minute}); err != nil || !ok {
		t.Fatalf("Acquire(after release) = ok %v, err %v; want true", ok, err)
	}
}

func TestBrokerExpiredLeaseRecovers(t *testing.T) {
	now := time.Date(2026, 5, 2, 1, 0, 0, 0, time.UTC)
	broker := NewWithClock(t.TempDir(), func() time.Time { return now })

	oldLease, ok, err := broker.Acquire(Request{PaneID: "%11", NodeName: "worker", Owner: "old", TTL: time.Second})
	if err != nil || !ok {
		t.Fatalf("Acquire(old) = ok %v, err %v; want true", ok, err)
	}

	now = now.Add(2 * time.Second)
	newLease, ok, err := broker.Acquire(Request{PaneID: "%11", NodeName: "worker", Owner: "new", TTL: time.Minute})
	if err != nil || !ok {
		t.Fatalf("Acquire(new) = ok %v, err %v; want true", ok, err)
	}
	if err := oldLease.Release(); err != nil {
		t.Fatalf("Release(old) error = %v", err)
	}

	locks, err := ActiveLocks(broker.sessionDir, now)
	if err != nil {
		t.Fatalf("ActiveLocks() error = %v", err)
	}
	if len(locks) != 1 {
		t.Fatalf("len(locks) = %d, want 1: %#v", len(locks), locks)
	}
	if locks[0].Owner != "new" {
		t.Fatalf("Owner = %q, want new", locks[0].Owner)
	}
	if err := newLease.Release(); err != nil {
		t.Fatalf("Release(new) error = %v", err)
	}
}

func TestActiveLocksSortsAndRemovesExpired(t *testing.T) {
	now := time.Date(2026, 5, 2, 1, 0, 0, 0, time.UTC)
	sessionDir := t.TempDir()
	broker := NewWithClock(sessionDir, func() time.Time { return now })

	if _, ok, err := broker.Acquire(Request{PaneID: "%22", NodeName: "critic", Owner: "b", TTL: time.Minute}); err != nil || !ok {
		t.Fatalf("Acquire(%%22) = ok %v, err %v; want true", ok, err)
	}
	if _, ok, err := broker.Acquire(Request{PaneID: "%11", NodeName: "worker", Owner: "a", TTL: time.Minute}); err != nil || !ok {
		t.Fatalf("Acquire(%%11) = ok %v, err %v; want true", ok, err)
	}
	if _, ok, err := broker.Acquire(Request{PaneID: "%33", NodeName: "old", Owner: "expired", TTL: time.Second}); err != nil || !ok {
		t.Fatalf("Acquire(%%33) = ok %v, err %v; want true", ok, err)
	}

	locks, err := ActiveLocks(sessionDir, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("ActiveLocks() error = %v", err)
	}
	if len(locks) != 2 {
		t.Fatalf("len(locks) = %d, want 2: %#v", len(locks), locks)
	}
	if locks[0].PaneID != "%11" || locks[1].PaneID != "%22" {
		t.Fatalf("locks order = %#v, want %%11 then %%22", locks)
	}
	if _, err := ActiveLocks(filepath.Join(sessionDir, "missing"), now); err != nil {
		t.Fatalf("ActiveLocks(missing) error = %v", err)
	}
}

func TestBrokerRecoversFromCorruptLeaseFile(t *testing.T) {
	now := time.Date(2026, 5, 2, 1, 0, 0, 0, time.UTC)
	sessionDir := t.TempDir()
	lockDir := filepath.Join(sessionDir, "input-locks")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(input-locks): %v", err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, lockFilename("%11")), []byte("not-json"), 0o600); err != nil {
		t.Fatalf("WriteFile(corrupt lock): %v", err)
	}

	broker := NewWithClock(sessionDir, func() time.Time { return now })
	lease, ok, err := broker.Acquire(Request{PaneID: "%11", NodeName: "worker", Owner: "new", TTL: time.Minute})
	if err != nil || !ok {
		t.Fatalf("Acquire(after corrupt lock) = ok %v, err %v; want true", ok, err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}
