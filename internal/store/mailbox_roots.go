package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// mailboxRootsLockFileName is the session-level roots gate file, created
// directly under the same mailbox-locks root the per-recipient admission
// fence uses (AdmissionLockDir), but never inside a per-recipient
// directory: mailbox-locks/.roots.lock (P2-1R item 1).
const mailboxRootsLockFileName = ".roots.lock"

// ErrMailboxRootsIdentity mirrors ErrAdmissionFenceIdentity for the
// session-level roots gate: returned when the gate file's identity
// (device+inode) changed between being opened and the lock being
// confirmed, closing the same open-to-lock TOCTOU window the admission
// fence closes.
var ErrMailboxRootsIdentity = errors.New("mailbox roots lock file identity changed between open and lock")

// WithMailboxRootsShared acquires the session-level mailbox roots gate
// (mailbox-locks/.roots.lock) with a shared (LOCK_SH) lock and runs fn
// while holding it. Every future mailbox writer and claim path must hold
// this gate shared before taking its own recipient admission fence (lock
// order: roots gate, then recipient fence -- P2-1R item 2;
// docs/design/mailbox-overflow-policy.md §2.1). It never creates or
// touches any per-recipient admission state.
//
// Do not call WithMailboxRootsShared, WithMailboxRootsExclusive, or
// SyncMailboxProjection (which may start a generation transition) while
// already holding any admission fence or roots gate in the same process:
// flock is scoped to the open file description, not the goroutine, so a
// second lock acquisition on the same file from the same process blocks
// forever against itself. Never "upgrade" a shared hold to exclusive;
// release the shared hold first and reacquire exclusively.
func WithMailboxRootsShared(sessionDir string, fn func() error) error {
	return withMailboxRootsLockSeam(sessionDir, syscall.LOCK_SH, syscall.Flock, nil, fn)
}

// WithMailboxRootsExclusive acquires the session-level mailbox roots gate
// with an exclusive (LOCK_EX) lock and runs fn while holding it. A
// generation transition (quarantine) holds this gate exclusively,
// continuously, from re-reading the session marker through moving every
// mailbox root and writing the new marker (P2-1R item 3), so that no
// shared holder's fenced commit, and no other transition, can interleave
// with it. See WithMailboxRootsShared for the lock-order and
// same-process-reentrancy constraints, which apply here identically.
func WithMailboxRootsExclusive(sessionDir string, fn func() error) error {
	return withMailboxRootsLockSeam(sessionDir, syscall.LOCK_EX, syscall.Flock, nil, fn)
}

// withMailboxRootsLockSeam is the shared implementation behind both
// WithMailboxRootsShared and WithMailboxRootsExclusive, following the same
// safety pattern as withAdmissionFenceLock: real owner-only directory,
// O_NOFOLLOW open, fd-based chmod, post-lock dev/inode identity recheck,
// and symlink/non-regular rejection. toctouHook is a test-only seam
// (mirroring withAdmissionFenceLock's) fired after open and before lock.
func withMailboxRootsLockSeam(
	sessionDir string,
	how int,
	lock AdmissionLockFunc,
	toctouHook func(path string),
	fn func() error,
) error {
	if lock == nil {
		lock = syscall.Flock
	}

	root, err := ensureMailboxLocksRoot(sessionDir)
	if err != nil {
		return err
	}
	path := filepath.Join(root, mailboxRootsLockFileName)

	if lst, statErr := os.Lstat(path); statErr == nil {
		if lst.Mode()&os.ModeSymlink != 0 || !lst.Mode().IsRegular() {
			return fmt.Errorf("%w: %s", ErrAdmissionPathNotRegular, path)
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("stat mailbox roots lock: %w", statErr)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("opening mailbox roots lock: %w", err)
	}
	defer func() { _ = file.Close() }()

	if toctouHook != nil {
		toctouHook(path)
	}

	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod mailbox roots lock: %w", err)
	}
	if err := lock(int(file.Fd()), how); err != nil {
		return fmt.Errorf("locking mailbox roots lock: %w", err)
	}
	defer func() {
		_ = lock(int(file.Fd()), syscall.LOCK_UN)
	}()

	var fdStat, pathStat syscall.Stat_t
	if err := syscall.Fstat(int(file.Fd()), &fdStat); err != nil {
		return fmt.Errorf("fstat mailbox roots lock: %w", err)
	}
	if err := syscall.Stat(path, &pathStat); err != nil {
		return fmt.Errorf("stat mailbox roots lock after lock: %w", err)
	}
	if fdStat.Dev != pathStat.Dev || fdStat.Ino != pathStat.Ino {
		return ErrMailboxRootsIdentity
	}

	return fn()
}

// ensureMailboxLocksRoot returns the session-level mailbox-locks directory,
// creating it (owner-only) if necessary and validating it is a real,
// owner-only directory and not a symlink. Shared by the per-recipient
// admission fence (ensureAdmissionDir) and the roots gate so both agree on
// exactly one safe root and exactly one validation path.
func ensureMailboxLocksRoot(sessionDir string) (string, error) {
	root := filepath.Join(sessionDir, admissionLockRootDirName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("ensuring mailbox-locks root dir: %w", err)
	}
	if err := validateRealOwnerOnlyDir(root); err != nil {
		return "", err
	}
	return root, nil
}
