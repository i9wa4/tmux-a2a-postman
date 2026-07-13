package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDeadLetterPath(t *testing.T) {
	got := DeadLetterPath("/state/ctx/review", "20260502-120000-from-a-to-b.md", "-dl-test")
	want := filepath.Join("/state/ctx/review", "dead-letter", "20260502-120000-from-a-to-b-dl-test.md")
	if got != want {
		t.Fatalf("DeadLetterPath() = %q, want %q", got, want)
	}
}

func TestPlanDeadLetterMessage(t *testing.T) {
	plan := PlanDeadLetterMessage("/state/ctx/review", "20260502-120000-from-a-to-b.md", "-dl-test")
	if plan.DeadLetterDir != filepath.Join("/state/ctx/review", "dead-letter") {
		t.Fatalf("DeadLetterDir = %q", plan.DeadLetterDir)
	}
	if plan.DestinationPath != filepath.Join("/state/ctx/review", "dead-letter", "20260502-120000-from-a-to-b-dl-test.md") {
		t.Fatalf("DestinationPath = %q", plan.DestinationPath)
	}
	if plan.Filename != "20260502-120000-from-a-to-b.md" || plan.Suffix != "-dl-test" {
		t.Fatalf("filename/suffix = %q/%q", plan.Filename, plan.Suffix)
	}
}

func TestMoveToDeadLetterRejectsSymlinkedTargetDir(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "post.md")
	if err := os.WriteFile(src, []byte("payload"), 0o600); err != nil {
		t.Fatalf("WriteFile(src): %v", err)
	}
	realDir := filepath.Join(tmpDir, "real")
	if err := os.MkdirAll(realDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(real): %v", err)
	}
	linkDir := filepath.Join(tmpDir, "dead-letter")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatalf("Symlink(dead-letter): %v", err)
	}

	if err := MoveToDeadLetter(src, filepath.Join(linkDir, "post-dl-test.md")); err == nil {
		t.Fatal("MoveToDeadLetter() error = nil, want symlink rejection")
	}
}

func TestWriteDeadLetterFileWithOpsReturnsWriteErrorWithoutCreatingFile(t *testing.T) {
	tmpDir := t.TempDir()
	deadLetterDir := filepath.Join(tmpDir, "dead-letter")
	if err := os.MkdirAll(deadLetterDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(dead-letter): %v", err)
	}
	dst := filepath.Join(deadLetterDir, "post-dl-test.md")
	writeErr := errors.New("write failed")

	err := writeDeadLetterFileWithOps(dst, []byte("payload"), deadLetterFileOps{
		lstat: os.Lstat,
		writeFile: func(name string, data []byte, perm os.FileMode) error {
			if name != dst {
				t.Fatalf("write path = %q, want %q", name, dst)
			}
			return writeErr
		},
	})
	if !errors.Is(err, writeErr) {
		t.Fatalf("writeDeadLetterFileWithOps() error = %v, want %v", err, writeErr)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("dead-letter file exists or wrong error: %v", err)
	}
}

func TestDeliverPostToInboxAndCountInboxMessages(t *testing.T) {
	tmpDir := t.TempDir()
	postPath := filepath.Join(tmpDir, "post", "20260502-120000-r1111-from-a-to-b.md")
	if err := os.MkdirAll(filepath.Dir(postPath), 0o700); err != nil {
		t.Fatalf("MkdirAll(post): %v", err)
	}
	if err := os.WriteFile(postPath, []byte("payload"), 0o600); err != nil {
		t.Fatalf("WriteFile(post): %v", err)
	}

	inboxDir := filepath.Join(tmpDir, "inbox", "b")
	dst, err := DeliverPostToInbox(postPath, inboxDir, filepath.Base(postPath))
	if err != nil {
		t.Fatalf("DeliverPostToInbox() error = %v", err)
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != "payload" {
		t.Fatalf("ReadFile(dst) = %q, %v; want payload", string(got), err)
	}
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Fatalf("post still exists or wrong error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inboxDir, "ignore.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile(ignore): %v", err)
	}
	count, err := CountInboxMessages(inboxDir)
	if err != nil {
		t.Fatalf("CountInboxMessages() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("CountInboxMessages() = %d, want 1", count)
	}
}

func TestPlanArchiveInboxMessage(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "ctx", "review")
	filename := "20260502-120000-r1111-from-a-to-b.md"
	inboxPath := filepath.Join(sessionDir, "inbox", "worker", filename)

	plan, err := PlanArchiveInboxMessage(inboxPath, filename)
	if err != nil {
		t.Fatalf("PlanArchiveInboxMessage() error = %v", err)
	}
	if plan.SourcePath != inboxPath {
		t.Fatalf("SourcePath = %q, want %q", plan.SourcePath, inboxPath)
	}
	if plan.SessionDir != sessionDir {
		t.Fatalf("SessionDir = %q, want %q", plan.SessionDir, sessionDir)
	}
	if plan.ReadDir != filepath.Join(sessionDir, "read") {
		t.Fatalf("ReadDir = %q", plan.ReadDir)
	}
	if plan.ReadPath != filepath.Join(sessionDir, "read", filename) {
		t.Fatalf("ReadPath = %q", plan.ReadPath)
	}
	if _, err := os.Stat(plan.ReadDir); !os.IsNotExist(err) {
		t.Fatalf("plan created read dir or wrong error: %v", err)
	}
}

func TestArchiveInboxMessageMalformedAbsolutePathRejectedBeforeMutation(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "ctx", "review")
	filename := "20260502-120000-r1111-from-a-to-b.md"
	malformedPath := filepath.Join(sessionDir, "read", filename)
	if err := os.MkdirAll(filepath.Dir(malformedPath), 0o700); err != nil {
		t.Fatalf("MkdirAll(read): %v", err)
	}
	if err := os.WriteFile(malformedPath, []byte("payload"), 0o600); err != nil {
		t.Fatalf("WriteFile(read): %v", err)
	}

	if _, err := ArchiveInboxMessage(malformedPath, filename); err == nil {
		t.Fatal("ArchiveInboxMessage() error = nil, want malformed path rejection")
	}
	got, err := os.ReadFile(malformedPath)
	if err != nil {
		t.Fatalf("ReadFile(malformedPath): %v", err)
	}
	if string(got) != "payload" {
		t.Fatalf("malformed source content changed: %q", got)
	}
	unexpectedReadPath := filepath.Join(tmpDir, "ctx", "read", filename)
	if _, err := os.Stat(unexpectedReadPath); !os.IsNotExist(err) {
		t.Fatalf("unexpected read artifact or wrong error: %v", err)
	}
}

func TestArchiveInboxMessageMovesToReadAndRemovesDuplicate(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "ctx", "review")
	inboxPath := filepath.Join(sessionDir, "inbox", "worker", "20260502-120000-r1111-from-a-to-b.md")
	if err := os.MkdirAll(filepath.Dir(inboxPath), 0o700); err != nil {
		t.Fatalf("MkdirAll(inbox): %v", err)
	}
	if err := os.WriteFile(inboxPath, []byte("payload"), 0o600); err != nil {
		t.Fatalf("WriteFile(inbox): %v", err)
	}

	readPath, err := ArchiveInboxMessage(inboxPath, filepath.Base(inboxPath))
	if err != nil {
		t.Fatalf("ArchiveInboxMessage() error = %v", err)
	}
	if readPath != filepath.Join(sessionDir, "read", filepath.Base(inboxPath)) {
		t.Fatalf("readPath = %q", readPath)
	}
	if got, err := os.ReadFile(readPath); err != nil || string(got) != "payload" {
		t.Fatalf("ReadFile(read) = %q, %v; want payload", string(got), err)
	}

	if err := os.WriteFile(inboxPath, []byte("duplicate"), 0o600); err != nil {
		t.Fatalf("WriteFile(duplicate): %v", err)
	}
	readPath2, err := ArchiveInboxMessage(inboxPath, filepath.Base(inboxPath))
	if err != nil {
		t.Fatalf("ArchiveInboxMessage(duplicate) error = %v", err)
	}
	if readPath2 != readPath {
		t.Fatalf("duplicate readPath = %q, want %q", readPath2, readPath)
	}
	if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
		t.Fatalf("duplicate inbox still exists or wrong error: %v", err)
	}
}

func TestArchiveInboxMessageFailedRenamePreservesOriginalContent(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "ctx", "review")
	filename := "20260502-120000-r1111-from-a-to-b.md"
	inboxPath := filepath.Join(sessionDir, "inbox", "worker", filename)
	if err := os.MkdirAll(filepath.Dir(inboxPath), 0o700); err != nil {
		t.Fatalf("MkdirAll(inbox): %v", err)
	}
	if err := os.WriteFile(inboxPath, []byte("payload"), 0o600); err != nil {
		t.Fatalf("WriteFile(inbox): %v", err)
	}
	plan, err := PlanArchiveInboxMessage(inboxPath, filename)
	if err != nil {
		t.Fatalf("PlanArchiveInboxMessage() error = %v", err)
	}
	renameErr := errors.New("rename failed")

	readPath, err := archiveInboxMessageWithOps(plan, archiveFileOps{
		mkdirAll: os.MkdirAll,
		stat: func(name string) (os.FileInfo, error) {
			return nil, os.ErrNotExist
		},
		remove: os.Remove,
		rename: func(oldPath, newPath string) error {
			if oldPath != inboxPath || newPath != plan.ReadPath {
				t.Fatalf("rename = %q -> %q, want %q -> %q", oldPath, newPath, inboxPath, plan.ReadPath)
			}
			return renameErr
		},
	})
	if readPath != "" {
		t.Fatalf("readPath = %q, want empty on failure", readPath)
	}
	if !errors.Is(err, renameErr) {
		t.Fatalf("archiveInboxMessageWithOps() error = %v, want %v", err, renameErr)
	}
	got, readErr := os.ReadFile(inboxPath)
	if readErr != nil {
		t.Fatalf("ReadFile(inbox): %v", readErr)
	}
	if string(got) != "payload" {
		t.Fatalf("source content changed: %q", got)
	}
}

func TestPlanPopReceipt(t *testing.T) {
	tmpDir := t.TempDir()
	readDir := filepath.Join(tmpDir, "ctx", "review", "read")
	markdownPath := filepath.Join(readDir, "20260502-120000-r1111-from-a-to-b.md")

	plan := PlanPopReceipt(markdownPath)
	if plan.MarkdownPath != markdownPath {
		t.Fatalf("MarkdownPath = %q, want %q", plan.MarkdownPath, markdownPath)
	}
	if plan.ReadDir != readDir {
		t.Fatalf("ReadDir = %q, want %q", plan.ReadDir, readDir)
	}
	if plan.ReceiptPath != filepath.Join(readDir, "20260502-120000-r1111-from-a-to-b.pop.json") {
		t.Fatalf("ReceiptPath = %q", plan.ReceiptPath)
	}
	if _, err := os.Stat(readDir); !os.IsNotExist(err) {
		t.Fatalf("plan created read dir or wrong error: %v", err)
	}
}

func TestPlanPopReceiptIgnoresNonReadArchivePath(t *testing.T) {
	markdownPath := filepath.Join(t.TempDir(), "ctx", "review", "inbox", "worker", "message.md")
	plan := PlanPopReceipt(markdownPath)
	if plan.MarkdownPath != markdownPath {
		t.Fatalf("MarkdownPath = %q, want %q", plan.MarkdownPath, markdownPath)
	}
	if plan.ReadDir != "" || plan.ReceiptPath != "" {
		t.Fatalf("plan = %#v, want no receipt for non-read path", plan)
	}
}
