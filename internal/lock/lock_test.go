package lock

import (
	"path/filepath"
	"testing"
)

func TestSessionLock_Acquire(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "postman.lock")

	// Normal acquire
	lock1, err := NewSessionLock(lockPath)
	if err != nil {
		t.Fatalf("first lock acquire failed: %v", err)
	}

	// Double-acquire should fail
	_, err = NewSessionLock(lockPath)
	if err == nil {
		t.Fatal("expected error on double acquire, got nil")
	}

	_ = lock1.Release()
}

func TestSessionLock_Release(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "postman.lock")

	lock1, err := NewSessionLock(lockPath)
	if err != nil {
		t.Fatalf("first lock acquire failed: %v", err)
	}

	if err := lock1.Release(); err != nil {
		t.Fatalf("release failed: %v", err)
	}

	// Re-acquire after release should succeed
	lock2, err := NewSessionLock(lockPath)
	if err != nil {
		t.Fatalf("re-acquire after release failed: %v", err)
	}
	_ = lock2.Release()
}
