package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanCaptureEntriesStopsAtBudget(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i <= captureScanLimit; i++ {
		name := filepath.Join(dir, fmt.Sprintf("%05d", i))
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	visited := 0
	err = scanCaptureEntries(root, func(os.DirEntry) error {
		visited++
		return nil
	})
	if !errors.Is(err, errCaptureScanBudget) {
		t.Fatalf("expected budget error, got %v", err)
	}
	if visited != captureScanLimit {
		t.Fatalf("visited %d entries, want %d", visited, captureScanLimit)
	}
}

func writeCaptureFixture(t *testing.T, root *os.Root, id string, when time.Time) {
	t.Helper()
	if err := writeJSON(root, id+".json", CaptureResult{
		Version: 1, MessageID: id, CapturedAt: when.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPruneCaptureCandidateLimitLeavesFinalsUntouched(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	now := time.Now().UTC()
	expiredID := fmt.Sprintf("m-%032x", 0)
	writeCaptureFixture(t, root, expiredID, now.Add(-captureRetention-time.Hour))
	before, err := root.ReadFile(expiredID + ".json")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2049; i++ {
		writeCaptureFixture(t, root, fmt.Sprintf("m-%032x", i), now)
	}
	if err := root.WriteFile("note.txt", []byte("leave me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := pruneCapturesKeeping(root, now, ""); err == nil || err.Error() != "capture record limit exceeded" {
		t.Fatalf("expected record limit, got %v", err)
	}
	after, err := root.ReadFile(expiredID + ".json")
	if err != nil || string(after) != string(before) {
		t.Fatalf("expired final changed: %v", err)
	}
	note, err := root.ReadFile("note.txt")
	if err != nil || string(note) != "leave me" {
		t.Fatalf("unrelated note changed: %v", err)
	}
}

func TestPruneCaptureLaterRemoveFailureReplays(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i := 0; i < captureLimit+2; i++ {
		writeCaptureFixture(t, root, fmt.Sprintf("m-%032x", i), now)
	}
	if err := root.WriteFile("note.txt", []byte("leave me"), 0o600); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected remove failure")
	removals := 0
	err = pruneCapturesKeepingWithRemove(root, now, "", func(name string) error {
		removals++
		if removals == 2 {
			return injected
		}
		return root.Remove(name)
	})
	if !errors.Is(err, injected) {
		t.Fatalf("expected later removal failure, got %v", err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	root, err = os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := pruneCapturesKeeping(root, now, ""); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != captureLimit+1 {
		t.Fatalf("got %d entries, want %d", len(entries), captureLimit+1)
	}
	note, err := root.ReadFile("note.txt")
	if err != nil || string(note) != "leave me" {
		t.Fatalf("unrelated note changed: %v", err)
	}
}
