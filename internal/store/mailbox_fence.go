package store

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	admissionLockRootDirName  = "mailbox-locks"
	admissionFenceFileName    = ".admission.lock"
	admissionSequenceFileName = ".admission.seq"
	admissionSequenceTmpName  = ".admission.seq.tmp"
)

var (
	// ErrCorruptAdmissionSequence is returned when a persisted sequence
	// file exists but cannot be trusted (empty, unparsable, or negative),
	// or when admission state for an already-existing recipient is
	// missing its sequence file entirely. Either case fails closed rather
	// than defaulting to a fresh value, which would risk sequence reuse
	// (design §2.1/§4). Note that 0 is a valid, non-corrupt value: it is
	// the atomic-initialization value meaning "initialized, no sequence
	// issued yet".
	ErrCorruptAdmissionSequence = errors.New("admission sequence state is missing, empty, negative, or corrupt")

	// ErrInvalidRecipient is returned when a recipient is not a single
	// safe path segment.
	ErrInvalidRecipient = errors.New("recipient is not a single safe path segment")

	// ErrAdmissionFenceIdentity is returned when the fence file's identity
	// (device+inode) changed between being opened and the lock being
	// confirmed, closing the open-to-lock TOCTOU window.
	ErrAdmissionFenceIdentity = errors.New("admission fence file identity changed between open and lock")

	// ErrAdmissionPathNotRegular is returned when a lock, sequence, or
	// admission directory path exists but is a symlink or other
	// non-regular/non-directory entry, or remains group/other-accessible
	// after an attempted permission tightening.
	ErrAdmissionPathNotRegular = errors.New("admission path is not a regular file, not a real directory, or remains group/other-accessible")

	// ErrAdmissionSequenceOverflow is returned when advancing the
	// sequence would overflow.
	ErrAdmissionSequenceOverflow = errors.New("admission sequence would overflow")

	// errHandleInvalidated is returned by AdmissionHandle methods once
	// WithAdmissionFence's callback has returned and the fence has been
	// (or is being) released.
	errHandleInvalidated = errors.New("admission handle used after WithAdmissionFence returned")
)

// AdmissionLockFunc matches syscall.Flock's signature so tests can inject a
// fake lock without touching a real file descriptor.
type AdmissionLockFunc func(fd int, how int) error

// AdmissionOutcome is WithAdmissionFence's typed result. Admitted reflects
// commit-last semantics: it is true only if the callback called
// AdmissionHandle.MarkCommitted, which callers must do as the LAST
// statement of a successful admission, after the inbox file itself is
// durably committed. An at-cap arrival that is dead-lettered is NOT an
// admission and must not call NextAdmissionSequence or MarkCommitted at
// all (a later replay is admitted with a new sequence, design §2.1); for
// that path, AdmissionOutcome is simply the zero value.
type AdmissionOutcome struct {
	Admitted bool
	Sequence int
}

// AdmissionHandle is passed to WithAdmissionFence's callback. It is the
// ONLY way to read or advance a recipient's admission sequence; there is
// no exported raw-path Read, Persist, or Recover function, so a caller
// cannot touch sequence state without holding the fence (A1-F3). The
// handle is invalidated as soon as the callback returns: calling any
// method on a handle that escaped the callback (for example via a
// goroutine or a stored reference) returns an error and persists nothing.
type AdmissionHandle struct {
	lockDir     string
	invalidated bool
	used        bool
	committed   bool
	sequence    int
}

// NextAdmissionSequence returns the next valid admission sequence number
// for this recipient, durably persisting it (write, fsync, atomic rename,
// directory fsync) BEFORE returning, so if the process dies immediately
// after this call returns, the sequence is never reused. It may be called
// at most once per handle, and only while the handle is still valid (see
// AdmissionHandle). The durable high-water file is the ONLY authority for
// "never reused" -- this primitive does not accept or scan caller-supplied
// observed sequences; seeding historical maxima during a legacy-file
// migration is a part-2 concern, performed once under this same fence
// before any new sequence is ever requested for that recipient.
func (h *AdmissionHandle) NextAdmissionSequence() (int, error) {
	if h.invalidated {
		return 0, errHandleInvalidated
	}
	if h.used {
		return 0, errors.New("NextAdmissionSequence called more than once per handle")
	}

	persisted, err := readAdmissionSequenceFile(h.lockDir)
	if err != nil {
		return 0, err
	}
	if persisted == math.MaxInt {
		return 0, ErrAdmissionSequenceOverflow
	}
	next := persisted + 1
	if next <= persisted {
		return 0, ErrAdmissionSequenceOverflow
	}

	if err := persistAdmissionSequenceFile(h.lockDir, next); err != nil {
		return 0, err
	}
	h.used = true
	h.sequence = next
	return next, nil
}

// MarkCommitted records that the caller's final fallible step (committing
// the inbox file) succeeded. Call MarkCommitted only after the inbox file
// is durably committed. An at-cap arrival that is dead-lettered is not an
// admission: it must not call NextAdmissionSequence or MarkCommitted (a
// later replay is admitted with a new sequence, §2.1). MarkCommitted
// returns an error, and leaves AdmissionOutcome.Admitted false, if this
// handle never issued a sequence (via NextAdmissionSequence) or is no
// longer valid.
func (h *AdmissionHandle) MarkCommitted() error {
	if h.invalidated {
		return errHandleInvalidated
	}
	if !h.used {
		return errors.New("MarkCommitted called without a prior NextAdmissionSequence; an at-cap dead-letter is not an admission and must not call MarkCommitted")
	}
	h.committed = true
	return nil
}

// AdmissionLockDir returns the directory that holds a recipient's
// admission fence and sequence files: a dedicated "mailbox-locks" root at
// the session directory level, deliberately NOT inside any of the
// quarantine-eligible mailbox roots ("post", "inbox", "read",
// "dead-letter" -- see internal/projection/mailbox_projection.go:50,
// 425-466, whose quarantineMailboxProjectionTrees renames those roots
// wholesale on a session-generation change). This is exported only so
// callers (including, in part 2, quarantineMailboxProjectionTrees itself,
// which must acquire every affected recipient's fence, in sorted
// recipient order, before moving an inbox) can reason about the path; it
// is not an entry point for touching sequence state directly.
//
// recipient must be a known, configured node; validating that is the
// caller's duty, because this primitive creates admission state for any
// valid path segment regardless of whether it names a real node.
func AdmissionLockDir(sessionDir, recipient string) (string, error) {
	if err := validateRecipient(recipient); err != nil {
		return "", err
	}
	root := filepath.Join(sessionDir, admissionLockRootDirName)
	lockDir := filepath.Join(root, recipient)
	if filepath.Dir(filepath.Clean(lockDir)) != filepath.Clean(root) {
		return "", fmt.Errorf("%w: resolved outside the admission root", ErrInvalidRecipient)
	}
	return lockDir, nil
}

func validateRecipient(recipient string) error {
	if recipient == "" || recipient == "." || recipient == ".." {
		return fmt.Errorf("%w: %q", ErrInvalidRecipient, recipient)
	}
	if strings.ContainsRune(recipient, '/') || strings.ContainsRune(recipient, os.PathSeparator) {
		return fmt.Errorf("%w: %q contains a path separator", ErrInvalidRecipient, recipient)
	}
	if recipient != filepath.Base(recipient) {
		return fmt.Errorf("%w: %q is not a single path segment", ErrInvalidRecipient, recipient)
	}
	return nil
}

// WithAdmissionFence serializes every writer and claim path for a single
// recipient behind one blocking exclusive lock, following the same
// append-authority fence pattern as internal/journal/journal.go:728-758,
// strengthened with fd-based chmod, post-lock identity revalidation, and
// symlink/non-regular rejection (A1-F1, A1-F5). It validates recipient and
// resolves the lock directory itself (AdmissionLockDir); it never creates
// or touches any mailbox root (post, inbox, read, dead-letter).
//
// recipient must be a known, configured node; validating that is the
// caller's duty, because this primitive creates admission state for any
// valid path segment regardless of whether it names a real node.
//
// fn runs while the fence is held; the fence is released once fn returns,
// regardless of its error. The AdmissionHandle passed to fn is invalidated
// as soon as fn returns. The returned AdmissionOutcome is valid even when
// err is non-nil, reflecting whatever the callback had done (used a
// sequence, committed, or neither) before returning the error.
func WithAdmissionFence(sessionDir, recipient string, fn func(*AdmissionHandle) error) (AdmissionOutcome, error) {
	return withAdmissionFenceLock(sessionDir, recipient, syscall.Flock, nil, fn)
}

func withAdmissionFenceLock(
	sessionDir, recipient string,
	lock AdmissionLockFunc,
	toctouHook func(path string),
	fn func(*AdmissionHandle) error,
) (AdmissionOutcome, error) {
	if lock == nil {
		lock = syscall.Flock
	}

	lockDir, err := ensureAdmissionDir(sessionDir, recipient)
	if err != nil {
		return AdmissionOutcome{}, err
	}

	path := filepath.Join(lockDir, admissionFenceFileName)
	if lst, statErr := os.Lstat(path); statErr == nil {
		if lst.Mode()&os.ModeSymlink != 0 || !lst.Mode().IsRegular() {
			return AdmissionOutcome{}, fmt.Errorf("%w: %s", ErrAdmissionPathNotRegular, path)
		}
	} else if !os.IsNotExist(statErr) {
		return AdmissionOutcome{}, fmt.Errorf("stat admission fence: %w", statErr)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return AdmissionOutcome{}, fmt.Errorf("opening admission fence: %w", err)
	}
	defer func() { _ = file.Close() }()

	if toctouHook != nil {
		// Test-only seam: replace the fence path with a new inode after
		// open and before lock; the fd still refers to the old inode, so
		// the post-lock identity recheck below must fail closed.
		toctouHook(path)
	}

	if err := file.Chmod(0o600); err != nil {
		return AdmissionOutcome{}, fmt.Errorf("chmod admission fence: %w", err)
	}
	if err := lock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return AdmissionOutcome{}, fmt.Errorf("locking admission fence: %w", err)
	}
	defer func() {
		_ = lock(int(file.Fd()), syscall.LOCK_UN)
	}()

	// A1-F1: revalidate identity after acquiring the lock, closing the
	// open-to-lock TOCTOU window. Never replace or remove the lock file
	// in normal operation; this check only ever rejects, it never
	// repairs.
	var fdStat, pathStat syscall.Stat_t
	if err := syscall.Fstat(int(file.Fd()), &fdStat); err != nil {
		return AdmissionOutcome{}, fmt.Errorf("fstat admission fence: %w", err)
	}
	if err := syscall.Stat(path, &pathStat); err != nil {
		return AdmissionOutcome{}, fmt.Errorf("stat admission fence after lock: %w", err)
	}
	if fdStat.Dev != pathStat.Dev || fdStat.Ino != pathStat.Ino {
		return AdmissionOutcome{}, ErrAdmissionFenceIdentity
	}

	handle := &AdmissionHandle{lockDir: lockDir}
	fnErr := fn(handle)
	handle.invalidated = true
	outcome := AdmissionOutcome{Admitted: handle.committed, Sequence: handle.sequence}
	return outcome, fnErr
}

// validateRealOwnerOnlyDir Lstats path, rejecting a symlink or
// non-directory outright. If the directory exists but has group/other
// permission bits set, it tightens them to owner-only and re-Lstats to
// confirm the tightening actually took effect (and that the path still
// isn't a symlink), failing closed rather than ignoring a chmod error or
// a still-permissive result.
func validateRealOwnerOnlyDir(path string) error {
	lst, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat admission dir: %w", err)
	}
	if lst.Mode()&os.ModeSymlink != 0 || !lst.IsDir() {
		return fmt.Errorf("%w: %s", ErrAdmissionPathNotRegular, path)
	}
	if lst.Mode().Perm()&0o077 == 0 {
		return nil
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("tightening admission dir permissions: %w", err)
	}
	lst2, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("re-stat admission dir after chmod: %w", err)
	}
	if lst2.Mode()&os.ModeSymlink != 0 || !lst2.IsDir() {
		return fmt.Errorf("%w: %s", ErrAdmissionPathNotRegular, path)
	}
	if lst2.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: %s still group/other-accessible after chmod", ErrAdmissionPathNotRegular, path)
	}
	return nil
}

// ensureAdmissionDir returns the (session-level, non-quarantined) lock
// directory for recipient, creating it via ATOMIC INITIALIZATION if it
// does not exist yet: a uniquely-named temp dir is populated with a fresh
// lock file and a high-water sequence file containing "0" (fsynced), the
// temp dir itself is fsynced, then it is renamed into place. If another
// caller's initialization wins the race (the rename target already
// exists), this caller's temp dir is discarded and the existing directory
// is used. This closes the liveness/race defect of a prior
// create-dir-then-maybe-lose-the-lock-race design: there is never a
// window where the directory exists without its sequence file.
func ensureAdmissionDir(sessionDir, recipient string) (string, error) {
	lockDir, err := AdmissionLockDir(sessionDir, recipient)
	if err != nil {
		return "", err
	}
	root := filepath.Dir(lockDir)

	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("ensuring admission root dir: %w", err)
	}
	if err := validateRealOwnerOnlyDir(root); err != nil {
		return "", err
	}

	if lst, statErr := os.Lstat(lockDir); statErr == nil {
		if lst.Mode()&os.ModeSymlink != 0 || !lst.IsDir() {
			return "", fmt.Errorf("%w: %s", ErrAdmissionPathNotRegular, lockDir)
		}
		if err := validateRealOwnerOnlyDir(lockDir); err != nil {
			return "", err
		}
		return lockDir, nil
	} else if !os.IsNotExist(statErr) {
		return "", fmt.Errorf("stat admission dir: %w", statErr)
	}

	tmpDir, err := os.MkdirTemp(root, ".init-"+recipient+"-")
	if err != nil {
		return "", fmt.Errorf("creating admission init temp dir: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	if err := os.WriteFile(filepath.Join(tmpDir, admissionFenceFileName), []byte{}, 0o600); err != nil {
		return "", fmt.Errorf("creating admission init lock file: %w", err)
	}
	if err := persistAdmissionSequenceFile(tmpDir, 0); err != nil {
		return "", fmt.Errorf("initializing admission sequence: %w", err)
	}

	tdir, err := os.Open(tmpDir)
	if err != nil {
		return "", fmt.Errorf("opening init temp dir for fsync: %w", err)
	}
	syncErr := tdir.Sync()
	_ = tdir.Close()
	if syncErr != nil {
		return "", fmt.Errorf("fsyncing init temp dir: %w", syncErr)
	}

	renameErr := os.Rename(tmpDir, lockDir)
	switch {
	case renameErr == nil:
		cleanup = false
	case errors.Is(renameErr, fs.ErrExist) || errors.Is(renameErr, syscall.EEXIST) || errors.Is(renameErr, syscall.ENOTEMPTY):
		// Another caller's initialization won; our temp dir is discarded
		// by the deferred cleanup above, and we fall through to use the
		// now-existing directory.
	default:
		return "", fmt.Errorf("renaming admission init dir into place: %w", renameErr)
	}

	rdir, err := os.Open(root)
	if err != nil {
		return "", fmt.Errorf("opening admission root for fsync: %w", err)
	}
	syncErr2 := rdir.Sync()
	_ = rdir.Close()
	if syncErr2 != nil {
		return "", fmt.Errorf("fsyncing admission root dir: %w", syncErr2)
	}

	lst, statErr := os.Lstat(lockDir)
	if statErr != nil {
		return "", fmt.Errorf("stat admission dir after init: %w", statErr)
	}
	if lst.Mode()&os.ModeSymlink != 0 || !lst.IsDir() {
		return "", fmt.Errorf("%w: %s", ErrAdmissionPathNotRegular, lockDir)
	}
	if err := validateRealOwnerOnlyDir(lockDir); err != nil {
		return "", err
	}
	return lockDir, nil
}

func readAdmissionSequenceFile(lockDir string) (int, error) {
	path := filepath.Join(lockDir, admissionSequenceFileName)
	if lst, err := os.Lstat(path); err == nil {
		if lst.Mode()&os.ModeSymlink != 0 || !lst.Mode().IsRegular() {
			return 0, fmt.Errorf("%w: %s", ErrAdmissionPathNotRegular, path)
		}
	} else if os.IsNotExist(err) {
		return 0, fmt.Errorf("%w: %s missing for existing admission state (needs operator repair)", ErrCorruptAdmissionSequence, path)
	} else {
		return 0, fmt.Errorf("stat admission sequence: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("reading admission sequence: %w", err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return 0, fmt.Errorf("%w: %s is empty", ErrCorruptAdmissionSequence, path)
	}
	seq, err := strconv.Atoi(trimmed)
	if err != nil || seq < 0 {
		return 0, fmt.Errorf("%w: %s: value %q", ErrCorruptAdmissionSequence, path, trimmed)
	}
	return seq, nil
}

// sequenceFileOps isolates every fallible filesystem phase of persisting
// the admission sequence (create/truncate, write, sync, close, rename,
// open containing dir, sync dir) behind function fields, so tests can
// inject a failure at each individual phase (A1-F2/A1-F4) without faking
// the filesystem wholesale.
type sequenceFileOps struct {
	create  func(path string) (*os.File, error)
	write   func(f *os.File, data []byte) (int, error)
	sync    func(f *os.File) error
	close   func(f *os.File) error
	rename  func(oldpath, newpath string) error
	openDir func(path string) (*os.File, error)
	syncDir func(f *os.File) error
}

var osSequenceFileOps = sequenceFileOps{
	create: func(path string) (*os.File, error) {
		return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, 0o600)
	},
	write:   func(f *os.File, data []byte) (int, error) { return f.Write(data) },
	sync:    func(f *os.File) error { return f.Sync() },
	close:   func(f *os.File) error { return f.Close() },
	rename:  os.Rename,
	openDir: os.Open,
	syncDir: func(f *os.File) error { return f.Sync() },
}

// persistAdmissionSequenceFile durably writes seq (0 for atomic
// initialization, or a positive advanced value) to lockDir's sequence
// file: write-to-temp, fsync the temp file, atomically rename it over the
// real path, then fsync the containing directory so the rename itself
// survives a crash. The prior final file is left untouched on any
// pre-rename failure. seq must not be negative; this is an internal
// helper only ever called by ensureAdmissionDir (with 0) or
// NextAdmissionSequence (with an already-validated, monotonically
// advanced value).
func persistAdmissionSequenceFile(lockDir string, seq int) error {
	return persistAdmissionSequenceFileWithOps(lockDir, seq, osSequenceFileOps)
}

func persistAdmissionSequenceFileWithOps(lockDir string, seq int, ops sequenceFileOps) error {
	if seq < 0 {
		return fmt.Errorf("refusing to persist a negative admission sequence %d", seq)
	}
	tmpPath := filepath.Join(lockDir, admissionSequenceTmpName)
	finalPath := filepath.Join(lockDir, admissionSequenceFileName)

	f, err := ops.create(tmpPath)
	if err != nil {
		return fmt.Errorf("creating admission sequence temp file: %w", err)
	}
	if _, err := ops.write(f, []byte(strconv.Itoa(seq))); err != nil {
		_ = ops.close(f)
		return fmt.Errorf("writing admission sequence temp file: %w", err)
	}
	if err := ops.sync(f); err != nil {
		_ = ops.close(f)
		return fmt.Errorf("fsyncing admission sequence temp file: %w", err)
	}
	if err := ops.close(f); err != nil {
		return fmt.Errorf("closing admission sequence temp file: %w", err)
	}
	if err := ops.rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("renaming admission sequence into place: %w", err)
	}

	dir, err := ops.openDir(lockDir)
	if err != nil {
		return fmt.Errorf("opening admission lock dir for fsync: %w", err)
	}
	defer func() { _ = dir.Close() }()
	if err := ops.syncDir(dir); err != nil {
		return fmt.Errorf("fsyncing admission lock dir: %w", err)
	}
	return nil
}
