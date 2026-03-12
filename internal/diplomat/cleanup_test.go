package diplomat

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// writeTestRegistration writes a ContextRegistration JSON file to the active-contexts dir.
func writeTestRegistration(t *testing.T, baseDir string, reg ContextRegistration) {
	t.Helper()
	dir := filepath.Join(baseDir, "diplomat", "active-contexts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data, _ := json.MarshalIndent(reg, "", "  ")
	path := filepath.Join(dir, reg.ContextID+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestStartDiplomatCleanup_StaleRemoved(t *testing.T) {
	baseDir := t.TempDir()

	// Register a stale context: PID 0 → always reported dead
	reg := ContextRegistration{
		ContextID:    "stale-ctx",
		SessionName:  "session-a",
		PID:          0, // never alive
		DiplomatNode: "node-x",
		StartedAt:    time.Now(),
	}
	writeTestRegistration(t, baseDir, reg)

	var callCount int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartDiplomatCleanup(ctx, baseDir, 0.05, func(contextID string) {
		if contextID == "stale-ctx" {
			atomic.AddInt32(&callCount, 1)
		}
	})

	// Wait for at least one tick
	time.Sleep(200 * time.Millisecond)
	cancel()

	if atomic.LoadInt32(&callCount) == 0 {
		t.Error("onStaleRemoved was not called for stale context")
	}

	// File should have been removed
	jsonFile := filepath.Join(baseDir, "diplomat", "active-contexts", "stale-ctx.json")
	if _, err := os.Stat(jsonFile); !os.IsNotExist(err) {
		t.Errorf("stale registration file should have been removed")
	}
}

func TestStartDiplomatCleanup_ENOENT(t *testing.T) {
	baseDir := t.TempDir()

	// Write a registration, then remove it manually before cleanup runs
	reg := ContextRegistration{
		ContextID:    "gone-ctx",
		PID:          0,
		DiplomatNode: "node-y",
		StartedAt:    time.Now(),
	}
	writeTestRegistration(t, baseDir, reg)

	// Remove the file before cleanup goroutine can act on it
	jsonFile := filepath.Join(baseDir, "diplomat", "active-contexts", "gone-ctx.json")
	if err := os.Remove(jsonFile); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	var callCount int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cleanup should not call onStaleRemoved (file already gone → ENOENT, no event)
	StartDiplomatCleanup(ctx, baseDir, 0.05, func(contextID string) {
		atomic.AddInt32(&callCount, 1)
	})

	time.Sleep(200 * time.Millisecond)
	cancel()

	// LoadActiveContexts returns empty (file was already removed), so no removal
	// attempt is made → callback count stays 0
	if atomic.LoadInt32(&callCount) != 0 {
		t.Errorf("onStaleRemoved called %d times, want 0", atomic.LoadInt32(&callCount))
	}
}

func TestStartDiplomatCleanup_AliveNotRemoved(t *testing.T) {
	baseDir := t.TempDir()

	// Register this process (alive)
	reg := ContextRegistration{
		ContextID:    "alive-ctx",
		PID:          os.Getpid(),
		DiplomatNode: "node-z",
		StartedAt:    time.Now(),
	}
	writeTestRegistration(t, baseDir, reg)

	var callCount int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartDiplomatCleanup(ctx, baseDir, 0.05, func(_ string) {
		atomic.AddInt32(&callCount, 1)
	})

	time.Sleep(200 * time.Millisecond)
	cancel()

	if atomic.LoadInt32(&callCount) != 0 {
		t.Errorf("onStaleRemoved called for alive context, want 0 calls")
	}

	// File should still exist
	jsonFile := filepath.Join(baseDir, "diplomat", "active-contexts", "alive-ctx.json")
	if _, err := os.Stat(jsonFile); err != nil {
		t.Errorf("alive registration file should still exist: %v", err)
	}
}
