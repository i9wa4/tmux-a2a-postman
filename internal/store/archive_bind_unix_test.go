//go:build linux || darwin || freebsd || netbsd || openbsd

package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoverArchiveBindingsRestoresInterruptedStage(t *testing.T) {
	sessionDir, inboxPath, filename, content := writeArchiveBindingSource(t)
	stageName := stageArchiveBindingSource(t, inboxPath, content)

	if err := RecoverArchiveBindings(sessionDir); err != nil {
		t.Fatalf("RecoverArchiveBindings() error = %v", err)
	}
	assertFileContent(t, inboxPath, content)
	assertMissing(t, filepath.Join(filepath.Dir(inboxPath), stageName))
	assertMissing(t, filepath.Join(filepath.Dir(inboxPath), archiveBindingManifestName(stageName)))
	assertMissing(t, filepath.Join(sessionDir, "read", filename))
}

func TestRecoverArchiveBindingsRemovesCompletedStage(t *testing.T) {
	sessionDir, inboxPath, filename, content := writeArchiveBindingSource(t)
	stageName := stageArchiveBindingSource(t, inboxPath, content)
	readPath := filepath.Join(sessionDir, "read", filename)
	if err := os.MkdirAll(filepath.Dir(readPath), 0o700); err != nil {
		t.Fatalf("MkdirAll read: %v", err)
	}
	if err := os.WriteFile(readPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile read: %v", err)
	}

	if err := RecoverArchiveBindings(sessionDir); err != nil {
		t.Fatalf("RecoverArchiveBindings() error = %v", err)
	}
	assertFileContent(t, readPath, content)
	assertMissing(t, inboxPath)
	assertMissing(t, filepath.Join(filepath.Dir(inboxPath), stageName))
	assertMissing(t, filepath.Join(filepath.Dir(inboxPath), archiveBindingManifestName(stageName)))
}

func TestRecoverArchiveBindingsToleratesConcurrentStageCleanup(t *testing.T) {
	sessionDir, inboxPath, filename, content := writeArchiveBindingSource(t)
	stageName := stageArchiveBindingSource(t, inboxPath, content)
	stagePath := filepath.Join(filepath.Dir(inboxPath), stageName)
	readPath := filepath.Join(sessionDir, "read", filename)
	if err := os.MkdirAll(filepath.Dir(readPath), 0o700); err != nil {
		t.Fatalf("MkdirAll read: %v", err)
	}
	if err := os.WriteFile(readPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile read: %v", err)
	}

	removedByCleanup := false
	restore := beforeRecoverArchiveStageRemove
	beforeRecoverArchiveStageRemove = func(path string) {
		if path != stagePath || removedByCleanup {
			return
		}
		removedByCleanup = true
		if err := os.Remove(path); err != nil {
			t.Fatalf("Remove concurrent stage: %v", err)
		}
	}
	t.Cleanup(func() {
		beforeRecoverArchiveStageRemove = restore
	})

	if err := RecoverArchiveBindings(sessionDir); err != nil {
		t.Fatalf("RecoverArchiveBindings() error = %v", err)
	}
	if !removedByCleanup {
		t.Fatal("test hook did not force concurrent stage removal")
	}
	assertFileContent(t, readPath, content)
	assertMissing(t, inboxPath)
	assertMissing(t, stagePath)
	assertMissing(t, filepath.Join(filepath.Dir(inboxPath), archiveBindingManifestName(stageName)))
}

func TestBeginArchiveInboxMessageVerifiedReturnsBeforeCleanup(t *testing.T) {
	sessionDir, inboxPath, filename, content := writeArchiveBindingSource(t)
	readPath, cleanup, err := BeginArchiveInboxMessageVerified(inboxPath, filename, []byte(content))
	if err != nil {
		t.Fatalf("BeginArchiveInboxMessageVerified: %v", err)
	}
	if cleanup == nil {
		t.Fatal("BeginArchiveInboxMessageVerified cleanup is nil")
	}

	assertMissing(t, inboxPath)
	assertFileContent(t, readPath, content)
	stageName := singleArchiveBindingStageName(t, filepath.Dir(inboxPath))
	assertFileContent(t, filepath.Join(filepath.Dir(inboxPath), archiveBindingManifestName(stageName)), manifestContentForTest(t, inboxPath, stageName))

	if err := RecoverArchiveBindings(sessionDir); err != nil {
		t.Fatalf("RecoverArchiveBindings before cleanup: %v", err)
	}
	assertFileContent(t, readPath, content)
	assertMissing(t, filepath.Join(filepath.Dir(inboxPath), stageName))
	assertMissing(t, filepath.Join(filepath.Dir(inboxPath), archiveBindingManifestName(stageName)))

	if err := cleanup(); err != nil {
		t.Fatalf("cleanup after recovery should be idempotent: %v", err)
	}
}

func TestBeginArchiveInboxMessageVerifiedCleanupFailureRetainsManifest(t *testing.T) {
	sessionDir, inboxPath, filename, content := writeArchiveBindingSource(t)
	readPath, cleanup, err := BeginArchiveInboxMessageVerified(inboxPath, filename, []byte(content))
	if err != nil {
		t.Fatalf("BeginArchiveInboxMessageVerified: %v", err)
	}
	stageName := singleArchiveBindingStageName(t, filepath.Dir(inboxPath))
	manifestPath := filepath.Join(filepath.Dir(inboxPath), archiveBindingManifestName(stageName))

	restore := syncArchiveBindingDirectory
	syncArchiveBindingDirectory = func(*os.File) error {
		return errors.New("forced two-phase cleanup sync failure")
	}
	err = cleanup()
	syncArchiveBindingDirectory = restore
	if err == nil || !strings.Contains(err.Error(), "stage removal") {
		t.Fatalf("cleanup error = %v, want stage removal sync failure", err)
	}
	assertFileContent(t, readPath, content)
	assertMissing(t, filepath.Join(filepath.Dir(inboxPath), stageName))
	assertFileContent(t, manifestPath, manifestContentForTest(t, inboxPath, stageName))

	if err := RecoverArchiveBindings(sessionDir); err != nil {
		t.Fatalf("RecoverArchiveBindings: %v", err)
	}
	assertFileContent(t, readPath, content)
	assertMissing(t, manifestPath)
}

func TestRecoverArchiveBindingsCompletesPreRenameCrash(t *testing.T) {
	sessionDir, inboxPath, _, content := writeArchiveBindingSource(t)
	source, err := openBoundArchiveSource(inboxPath, []byte(content))
	if err != nil {
		t.Fatalf("openBoundArchiveSource: %v", err)
	}
	stageName, err := privateArchiveStageName(filepath.Base(inboxPath))
	if err != nil {
		t.Fatalf("privateArchiveStageName: %v", err)
	}
	if err := source.writeBindingManifest(stageName); err != nil {
		t.Fatalf("writeBindingManifest: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("Close source: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := RecoverArchiveBindings(sessionDir); err != nil {
			t.Fatalf("RecoverArchiveBindings(%d) error = %v", i, err)
		}
	}
	assertFileContent(t, inboxPath, content)
	assertMissing(t, filepath.Join(filepath.Dir(inboxPath), stageName))
	assertMissing(t, filepath.Join(filepath.Dir(inboxPath), archiveBindingManifestName(stageName)))
}

func TestRecoverArchiveBindingsCompletesPostReadCleanupCrash(t *testing.T) {
	sessionDir, inboxPath, filename, content := writeArchiveBindingSource(t)
	stageName := stageArchiveBindingSource(t, inboxPath, content)
	stagePath := filepath.Join(filepath.Dir(inboxPath), stageName)
	readPath := filepath.Join(sessionDir, "read", filename)
	if err := os.MkdirAll(filepath.Dir(readPath), 0o700); err != nil {
		t.Fatalf("MkdirAll read: %v", err)
	}
	if err := os.WriteFile(readPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile read: %v", err)
	}
	if err := os.Remove(stagePath); err != nil {
		t.Fatalf("Remove stage: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := RecoverArchiveBindings(sessionDir); err != nil {
			t.Fatalf("RecoverArchiveBindings(%d) error = %v", i, err)
		}
	}
	assertFileContent(t, readPath, content)
	assertMissing(t, inboxPath)
	assertMissing(t, stagePath)
	assertMissing(t, filepath.Join(filepath.Dir(inboxPath), archiveBindingManifestName(stageName)))
}

func TestUnlinkStageRetainsManifestWhenStageRemovalSyncFails(t *testing.T) {
	_, inboxPath, filename, content := writeArchiveBindingSource(t)
	source, err := openBoundArchiveSource(inboxPath, []byte(content))
	if err != nil {
		t.Fatalf("openBoundArchiveSource: %v", err)
	}
	defer source.Close()
	stageName, err := source.Stage(nil)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	readPath := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(inboxPath))), "read", filename)
	if err := os.MkdirAll(filepath.Dir(readPath), 0o700); err != nil {
		t.Fatalf("MkdirAll read: %v", err)
	}
	if err := os.WriteFile(readPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile read: %v", err)
	}

	restore := syncArchiveBindingDirectory
	syncArchiveBindingDirectory = func(*os.File) error {
		return errors.New("forced stage removal sync failure")
	}
	t.Cleanup(func() {
		syncArchiveBindingDirectory = restore
	})

	err = source.UnlinkStage(stageName, nil)
	if err == nil || !strings.Contains(err.Error(), "stage removal") {
		t.Fatalf("UnlinkStage error = %v, want stage removal sync failure", err)
	}
	assertMissing(t, filepath.Join(filepath.Dir(inboxPath), stageName))
	assertFileContent(t, filepath.Join(filepath.Dir(inboxPath), archiveBindingManifestName(stageName)), manifestContentForTest(t, inboxPath, stageName))
}

func TestRestoreRetainsManifestWhenRestoreSyncFails(t *testing.T) {
	_, inboxPath, _, content := writeArchiveBindingSource(t)
	source, err := openBoundArchiveSource(inboxPath, []byte(content))
	if err != nil {
		t.Fatalf("openBoundArchiveSource: %v", err)
	}
	defer source.Close()
	stageName, err := source.Stage(nil)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	restore := syncArchiveBindingDirectory
	syncArchiveBindingDirectory = func(*os.File) error {
		return errors.New("forced restore sync failure")
	}
	t.Cleanup(func() {
		syncArchiveBindingDirectory = restore
	})

	err = source.Restore(stageName)
	if err == nil || !strings.Contains(err.Error(), "after restore") {
		t.Fatalf("Restore error = %v, want restore sync failure", err)
	}
	assertFileContent(t, inboxPath, content)
	assertFileContent(t, filepath.Join(filepath.Dir(inboxPath), archiveBindingManifestName(stageName)), manifestContentForTest(t, inboxPath, stageName))
}

func TestRecoverArchiveBindingsRetainsManifestWhenStageRemovalSyncFails(t *testing.T) {
	sessionDir, inboxPath, filename, content := writeArchiveBindingSource(t)
	stageName := stageArchiveBindingSource(t, inboxPath, content)
	stagePath := filepath.Join(filepath.Dir(inboxPath), stageName)
	readPath := filepath.Join(sessionDir, "read", filename)
	if err := os.MkdirAll(filepath.Dir(readPath), 0o700); err != nil {
		t.Fatalf("MkdirAll read: %v", err)
	}
	if err := os.WriteFile(readPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile read: %v", err)
	}

	restore := syncDirectory
	syncDirectory = func(string) error {
		return errors.New("forced recovery stage removal sync failure")
	}
	t.Cleanup(func() {
		syncDirectory = restore
	})

	err := RecoverArchiveBindings(sessionDir)
	if err == nil || !strings.Contains(err.Error(), "stage removal") {
		t.Fatalf("RecoverArchiveBindings error = %v, want stage removal sync failure", err)
	}
	assertMissing(t, stagePath)
	assertFileContent(t, filepath.Join(filepath.Dir(inboxPath), archiveBindingManifestName(stageName)), manifestContentForTest(t, inboxPath, stageName))
}

func TestRecoverArchiveBindingsRetainsManifestWhenRestoreSyncFails(t *testing.T) {
	sessionDir, inboxPath, _, content := writeArchiveBindingSource(t)
	stageName := stageArchiveBindingSource(t, inboxPath, content)

	restore := syncDirectory
	syncDirectory = func(string) error {
		return errors.New("forced recovery restore sync failure")
	}
	t.Cleanup(func() {
		syncDirectory = restore
	})

	err := RecoverArchiveBindings(sessionDir)
	if err == nil || !strings.Contains(err.Error(), "restore") {
		t.Fatalf("RecoverArchiveBindings error = %v, want restore sync failure", err)
	}
	assertFileContent(t, inboxPath, content)
	assertFileContent(t, filepath.Join(filepath.Dir(inboxPath), archiveBindingManifestName(stageName)), manifestContentForTest(t, inboxPath, stageName))
}

func TestRecoverArchiveBindingsRejectsSymlinkStage(t *testing.T) {
	sessionDir, inboxPath, _, content := writeArchiveBindingSource(t)
	stageName := stageArchiveBindingSource(t, inboxPath, content)
	stagePath := filepath.Join(filepath.Dir(inboxPath), stageName)
	if err := os.Remove(stagePath); err != nil {
		t.Fatalf("Remove stage: %v", err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), stagePath); err != nil {
		t.Fatalf("Symlink stage: %v", err)
	}

	err := RecoverArchiveBindings(sessionDir)
	if err == nil || !strings.Contains(err.Error(), "refusing non-regular file") {
		t.Fatalf("RecoverArchiveBindings() error = %v, want non-regular rejection", err)
	}
	assertMissing(t, inboxPath)
}

func TestRecoverArchiveBindingsRejectsNonRegularStage(t *testing.T) {
	sessionDir, inboxPath, _, content := writeArchiveBindingSource(t)
	stageName := stageArchiveBindingSource(t, inboxPath, content)
	stagePath := filepath.Join(filepath.Dir(inboxPath), stageName)
	if err := os.Remove(stagePath); err != nil {
		t.Fatalf("Remove stage: %v", err)
	}
	if err := os.Mkdir(stagePath, 0o700); err != nil {
		t.Fatalf("Mkdir stage: %v", err)
	}

	err := RecoverArchiveBindings(sessionDir)
	if err == nil || !strings.Contains(err.Error(), "refusing non-regular file") {
		t.Fatalf("RecoverArchiveBindings() error = %v, want non-regular rejection", err)
	}
	assertMissing(t, inboxPath)
}

func TestRecoverArchiveBindingsRejectsObjectReplacement(t *testing.T) {
	sessionDir, inboxPath, _, content := writeArchiveBindingSource(t)
	stageName := stageArchiveBindingSource(t, inboxPath, content)
	stagePath := filepath.Join(filepath.Dir(inboxPath), stageName)
	if err := os.Remove(stagePath); err != nil {
		t.Fatalf("Remove stage: %v", err)
	}
	if err := os.WriteFile(stagePath, []byte("replacement payload"), 0o600); err != nil {
		t.Fatalf("WriteFile replacement stage: %v", err)
	}

	err := RecoverArchiveBindings(sessionDir)
	if err == nil || (!strings.Contains(err.Error(), "object identity changed") && !strings.Contains(err.Error(), "content hash changed")) {
		t.Fatalf("RecoverArchiveBindings() error = %v, want replacement rejection", err)
	}
	assertMissing(t, inboxPath)
}

func TestRecoverArchiveBindingsRejectsCorruptManifest(t *testing.T) {
	sessionDir, inboxPath, _, content := writeArchiveBindingSource(t)
	stageName := stageArchiveBindingSource(t, inboxPath, content)
	manifestPath := filepath.Join(filepath.Dir(inboxPath), archiveBindingManifestName(stageName))
	if err := os.WriteFile(manifestPath, []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("WriteFile corrupt manifest: %v", err)
	}

	err := RecoverArchiveBindings(sessionDir)
	if err == nil || !strings.Contains(err.Error(), "decoding archive binding manifest") {
		t.Fatalf("RecoverArchiveBindings() error = %v, want corrupt manifest rejection", err)
	}
	assertMissing(t, inboxPath)
	assertFileContent(t, filepath.Join(filepath.Dir(inboxPath), stageName), content)
}

func TestRecoverArchiveBindingsRejectsAmbiguousSourceAndStage(t *testing.T) {
	sessionDir, inboxPath, _, content := writeArchiveBindingSource(t)
	stageName := stageArchiveBindingSource(t, inboxPath, content)
	if err := os.WriteFile(inboxPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile restored source collision: %v", err)
	}

	err := RecoverArchiveBindings(sessionDir)
	if err == nil || (!strings.Contains(err.Error(), "source and stage both exist") && !strings.Contains(err.Error(), "object identity changed")) {
		t.Fatalf("RecoverArchiveBindings() error = %v, want ambiguity rejection", err)
	}
	assertFileContent(t, inboxPath, content)
	assertFileContent(t, filepath.Join(filepath.Dir(inboxPath), stageName), content)
}

func writeArchiveBindingSource(t *testing.T) (string, string, string, string) {
	t.Helper()
	sessionDir := filepath.Join(t.TempDir(), "ctx", "session")
	filename := "20260806-070000-from-orchestrator-to-worker.md"
	inboxPath := filepath.Join(sessionDir, "inbox", "worker", filename)
	content := "archive binding payload"
	if err := os.MkdirAll(filepath.Dir(inboxPath), 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	if err := os.WriteFile(inboxPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}
	return sessionDir, inboxPath, filename, content
}

func stageArchiveBindingSource(t *testing.T, inboxPath, content string) string {
	t.Helper()
	source, err := openBoundArchiveSource(inboxPath, []byte(content))
	if err != nil {
		t.Fatalf("openBoundArchiveSource: %v", err)
	}
	stageName, err := source.Stage(nil)
	if closeErr := source.Close(); closeErr != nil {
		t.Fatalf("Close source: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	assertMissing(t, inboxPath)
	assertFileContent(t, filepath.Join(filepath.Dir(inboxPath), stageName), content)
	return stageName
}

func singleArchiveBindingStageName(t *testing.T, inboxDir string) string {
	t.Helper()
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		t.Fatalf("ReadDir inbox: %v", err)
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.Contains(name, ".archive-") && !strings.HasSuffix(name, ".bind.json") {
			names = append(names, name)
		}
	}
	if len(names) != 1 {
		t.Fatalf("archive stage count = %d, want 1: %v", len(names), names)
	}
	return names[0]
}

func manifestContentForTest(t *testing.T, inboxPath, stageName string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(inboxPath), archiveBindingManifestName(stageName)))
	if err != nil {
		t.Fatalf("ReadFile manifest: %v", err)
	}
	return string(data)
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("ReadFile(%s) = %q, want %q", path, got, want)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("Lstat(%s) = %v, want missing", path, err)
	}
}
