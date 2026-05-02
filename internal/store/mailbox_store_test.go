package store

import (
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
