package store

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// ---------------------------------------------------------------------
// P2-1R item 1/6: the session-level mailbox roots gate primitive.
// ---------------------------------------------------------------------

// TestMailboxRootsGateSharedHoldersOverlap proves WithMailboxRootsShared
// does not serialize against itself: two concurrent shared holders must
// both be running inside fn at the same time.
func TestMailboxRootsGateSharedHoldersOverlap(t *testing.T) {
	sessionDir := t.TempDir()

	var inside int32
	bothIn := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := WithMailboxRootsShared(sessionDir, func() error {
				if atomic.AddInt32(&inside, 1) == 2 {
					close(bothIn)
				}
				<-release
				return nil
			}); err != nil {
				t.Errorf("WithMailboxRootsShared: %v", err)
			}
		}()
	}

	select {
	case <-bothIn:
	case <-time.After(2 * time.Second):
		t.Fatal("two shared holders never overlapped: gate appears to serialize shared locks against each other")
	}
	close(release)
	wg.Wait()
}

// TestMailboxRootsGateExclusiveBlocksShared covers P2-1R items 1/2/6: an
// exclusive holder must block a concurrent shared contender until it
// releases, proven deterministically via a channel handshake plus a
// non-blocking probe on the real lock file (not a sleep-and-hope timing
// assumption).
func TestMailboxRootsGateExclusiveBlocksShared(t *testing.T) {
	sessionDir := t.TempDir()
	root, err := ensureMailboxLocksRoot(sessionDir)
	if err != nil {
		t.Fatalf("ensureMailboxLocksRoot: %v", err)
	}
	lockPath := filepath.Join(root, mailboxRootsLockFileName)

	holding := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- WithMailboxRootsExclusive(sessionDir, func() error {
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding

	if err := probeLockNB(lockPath); !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("probeLockNB while exclusive held = %v, want EWOULDBLOCK", err)
	}

	contenderStarted := make(chan struct{})
	contenderDone := make(chan error, 1)
	go func() {
		contenderDone <- WithMailboxRootsShared(sessionDir, func() error {
			close(contenderStarted)
			return nil
		})
	}()

	select {
	case <-contenderStarted:
		t.Fatal("shared contender started while the exclusive gate was still held")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if err := <-holderDone; err != nil {
		t.Fatalf("holder WithMailboxRootsExclusive: %v", err)
	}
	if err := <-contenderDone; err != nil {
		t.Fatalf("contender WithMailboxRootsShared: %v", err)
	}
	select {
	case <-contenderStarted:
	default:
		t.Fatal("shared contender never started after the exclusive gate released")
	}
}

// TestMailboxRootsGateSharedBlocksExclusive is the mirror of
// TestMailboxRootsGateExclusiveBlocksShared: a shared holder must block a
// concurrent exclusive contender.
func TestMailboxRootsGateSharedBlocksExclusive(t *testing.T) {
	sessionDir := t.TempDir()
	root, err := ensureMailboxLocksRoot(sessionDir)
	if err != nil {
		t.Fatalf("ensureMailboxLocksRoot: %v", err)
	}
	lockPath := filepath.Join(root, mailboxRootsLockFileName)

	holding := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- WithMailboxRootsShared(sessionDir, func() error {
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding

	if err := probeLockNB(lockPath); !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("probeLockNB while shared held = %v, want EWOULDBLOCK", err)
	}

	contenderStarted := make(chan struct{})
	contenderDone := make(chan error, 1)
	go func() {
		contenderDone <- WithMailboxRootsExclusive(sessionDir, func() error {
			close(contenderStarted)
			return nil
		})
	}()

	select {
	case <-contenderStarted:
		t.Fatal("exclusive contender started while the shared gate was still held")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if err := <-holderDone; err != nil {
		t.Fatalf("holder WithMailboxRootsShared: %v", err)
	}
	if err := <-contenderDone; err != nil {
		t.Fatalf("contender WithMailboxRootsExclusive: %v", err)
	}
	select {
	case <-contenderStarted:
	default:
		t.Fatal("exclusive contender never started after the shared gate released")
	}
}

// TestMailboxRootsGateIdentityMismatchFailsClosed mirrors
// TestWithAdmissionFenceIdentityMismatchFailsClosed for the roots gate: a
// deterministic hook replaces the lock file's inode between open and
// lock, and the post-lock fd/path identity recheck must fail closed.
func TestMailboxRootsGateIdentityMismatchFailsClosed(t *testing.T) {
	sessionDir := t.TempDir()
	root, err := ensureMailboxLocksRoot(sessionDir)
	if err != nil {
		t.Fatalf("ensureMailboxLocksRoot: %v", err)
	}
	lockPath := filepath.Join(root, mailboxRootsLockFileName)
	if err := os.WriteFile(lockPath, []byte{}, 0o600); err != nil {
		t.Fatalf("pre-creating roots lock file: %v", err)
	}

	fnRan := false
	hook := func(path string) {
		if err := os.Rename(path, path+".stale"); err != nil {
			t.Fatalf("hook rename away: %v", err)
		}
		if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
			t.Fatalf("hook recreate: %v", err)
		}
	}

	err = withMailboxRootsLockSeam(sessionDir, syscall.LOCK_EX, syscall.Flock, hook, func() error {
		fnRan = true
		return nil
	})
	if !errors.Is(err, ErrMailboxRootsIdentity) {
		t.Fatalf("withMailboxRootsLockSeam error = %v, want wrapping ErrMailboxRootsIdentity", err)
	}
	if fnRan {
		t.Fatal("fn ran despite a detected post-lock identity mismatch")
	}
}

// TestMailboxRootsGateRejectsSymlinkedLockPath covers P2-1R item 6(d): a
// symlinked (or otherwise non-regular) .roots.lock path must be rejected,
// not followed.
func TestMailboxRootsGateRejectsSymlinkedLockPath(t *testing.T) {
	sessionDir := t.TempDir()
	root, err := ensureMailboxLocksRoot(sessionDir)
	if err != nil {
		t.Fatalf("ensureMailboxLocksRoot: %v", err)
	}
	target := filepath.Join(sessionDir, "elsewhere")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(root, mailboxRootsLockFileName)); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	err = WithMailboxRootsExclusive(sessionDir, func() error {
		t.Fatal("fn ran despite a symlinked roots-lock path")
		return nil
	})
	if !errors.Is(err, ErrAdmissionPathNotRegular) {
		t.Fatalf("WithMailboxRootsExclusive error = %v, want wrapping ErrAdmissionPathNotRegular", err)
	}
}

// TestMailboxRootsGateRejectsSymlinkedRoot covers P2-1R item 6(d): a
// symlinked mailbox-locks root itself must be rejected, and the symlink's
// target must be left untouched (not chmodded as a side effect of
// rejection).
func TestMailboxRootsGateRejectsSymlinkedRoot(t *testing.T) {
	sessionDir := t.TempDir()
	targetDir := filepath.Join(sessionDir, "elsewhere-root")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll (target): %v", err)
	}
	if err := os.Symlink(targetDir, filepath.Join(sessionDir, admissionLockRootDirName)); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	err := WithMailboxRootsExclusive(sessionDir, func() error {
		t.Fatal("fn ran despite a symlinked mailbox-locks root")
		return nil
	})
	if !errors.Is(err, ErrAdmissionPathNotRegular) {
		t.Fatalf("WithMailboxRootsExclusive error = %v, want wrapping ErrAdmissionPathNotRegular", err)
	}

	info, statErr := os.Stat(targetDir)
	if statErr != nil {
		t.Fatalf("Stat(targetDir): %v", statErr)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("symlink target mode = %o, want unchanged 0755 (the rejection must not have chmodded the followed target)", info.Mode().Perm())
	}
}

// TestMailboxRootsGateCreatesNoAdmissionState covers P2-1R item 6(e) at
// the primitive level: neither WithMailboxRootsShared nor
// WithMailboxRootsExclusive ever creates a per-recipient admission
// directory -- only mailbox-locks/.roots.lock itself.
func TestMailboxRootsGateCreatesNoAdmissionState(t *testing.T) {
	sessionDir := t.TempDir()

	if err := WithMailboxRootsExclusive(sessionDir, func() error { return nil }); err != nil {
		t.Fatalf("WithMailboxRootsExclusive: %v", err)
	}
	if err := WithMailboxRootsShared(sessionDir, func() error { return nil }); err != nil {
		t.Fatalf("WithMailboxRootsShared: %v", err)
	}

	root := filepath.Join(sessionDir, admissionLockRootDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(mailbox-locks): %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(entries) != 1 || entries[0].Name() != mailboxRootsLockFileName || entries[0].IsDir() {
		t.Fatalf("mailbox-locks entries = %v, want exactly [%s] (a file, no recipient directories)", names, mailboxRootsLockFileName)
	}
}
