package store

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// ---------------------------------------------------------------------
// R2-1 (supersedes A1-F2's first-use rule): atomic initialization.
// ---------------------------------------------------------------------

// TestAtomicInitClaimOnlyThenWriterGetsOne covers R2-1: a first call that
// never calls NextAdmissionSequence (a "claim-only" call, e.g. a
// callback that decides not to admit anything) must NOT brick the
// recipient -- atomic initialization means the sequence file (seq=0)
// always exists alongside the lock file, so a later writer still gets 1.
func TestAtomicInitClaimOnlyThenWriterGetsOne(t *testing.T) {
	sessionDir := t.TempDir()

	if _, err := WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
		return nil // claim-only: never calls NextAdmissionSequence
	}); err != nil {
		t.Fatalf("WithAdmissionFence (claim-only): %v", err)
	}

	outcome, err := WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
		seq, err := h.NextAdmissionSequence()
		if err != nil {
			return err
		}
		if seq != 1 {
			t.Fatalf("NextAdmissionSequence = %d, want 1", seq)
		}
		return h.MarkCommitted()
	})
	if err != nil {
		t.Fatalf("WithAdmissionFence (writer): %v", err)
	}
	if !outcome.Admitted || outcome.Sequence != 1 {
		t.Fatalf("outcome = %+v, want Admitted=true Sequence=1", outcome)
	}
}

// TestAtomicInitConcurrentFreshCallersBothSucceed covers R2-1: two
// concurrent first callers for a brand-new recipient must both succeed
// (one wins the directory-creation race, the other discards its own temp
// dir and uses the winner's), getting sequences {1, 2} with no error --
// not a spurious failure for the race loser.
func TestAtomicInitConcurrentFreshCallersBothSucceed(t *testing.T) {
	sessionDir := t.TempDir()

	var wg sync.WaitGroup
	seqs := make([]int, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			outcome, err := WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
				if _, err := h.NextAdmissionSequence(); err != nil {
					return err
				}
				return h.MarkCommitted()
			})
			seqs[idx] = outcome.Sequence
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	got := map[int]bool{seqs[0]: true, seqs[1]: true}
	if !got[1] || !got[2] || seqs[0] == seqs[1] {
		t.Fatalf("sequences = %v, want {1, 2}", seqs)
	}
}

// TestAtomicInitIgnoresStaleTempDir covers R2-1: a stale leftover
// .init-* temp dir (for example from a process killed mid-initialization
// before its rename) must not interfere with a fresh initialization.
func TestAtomicInitIgnoresStaleTempDir(t *testing.T) {
	sessionDir := t.TempDir()
	root := filepath.Join(sessionDir, admissionLockRootDirName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	stale := filepath.Join(root, ".init-node-12345-999")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatalf("MkdirAll (stale): %v", err)
	}
	if err := os.WriteFile(filepath.Join(stale, admissionFenceFileName), []byte{}, 0o600); err != nil {
		t.Fatalf("WriteFile (stale lock): %v", err)
	}

	outcome, err := WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
		if _, err := h.NextAdmissionSequence(); err != nil {
			return err
		}
		return h.MarkCommitted()
	})
	if err != nil {
		t.Fatalf("WithAdmissionFence: %v", err)
	}
	if outcome.Sequence != 1 {
		t.Fatalf("outcome.Sequence = %d, want 1 (stale temp dir must be ignored, not reused)", outcome.Sequence)
	}
	if _, statErr := os.Stat(stale); statErr != nil {
		t.Fatalf("stale temp dir was removed or altered unexpectedly: %v", statErr)
	}
}

// TestExistingDirMissingSequenceFailsClosed covers R2-1/A1-F2: an
// EXISTING admission dir (not a fresh atomic-init) with a missing
// sequence file -- for example after filesystem damage or manual
// deletion -- must fail closed (ErrCorruptAdmissionSequence), never
// silently default to a fresh value.
func TestExistingDirMissingSequenceFailsClosed(t *testing.T) {
	sessionDir := t.TempDir()

	// Create the admission state normally (atomic init writes seq=0).
	if _, err := WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
		return nil
	}); err != nil {
		t.Fatalf("WithAdmissionFence (create): %v", err)
	}

	lockDir, err := AdmissionLockDir(sessionDir, "node")
	if err != nil {
		t.Fatalf("AdmissionLockDir: %v", err)
	}
	if err := os.Remove(filepath.Join(lockDir, admissionSequenceFileName)); err != nil {
		t.Fatalf("simulating sequence-file loss: %v", err)
	}

	_, err = WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
		_, err := h.NextAdmissionSequence()
		return err
	})
	if !errors.Is(err, ErrCorruptAdmissionSequence) {
		t.Fatalf("WithAdmissionFence (missing sequence on existing dir) error = %v, want wrapping ErrCorruptAdmissionSequence", err)
	}
}

// ---------------------------------------------------------------------
// R2-2 (A1-F3): handle invalidation, commit-last semantics.
// ---------------------------------------------------------------------

// TestHandleInvalidatedAfterCallbackReturns covers R2-2: a handle that
// escapes the callback (for example stored in a variable captured by a
// closure) must error and persist nothing when used after
// WithAdmissionFence has already returned.
func TestHandleInvalidatedAfterCallbackReturns(t *testing.T) {
	sessionDir := t.TempDir()

	var escaped *AdmissionHandle
	if _, err := WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
		escaped = h
		return nil
	}); err != nil {
		t.Fatalf("WithAdmissionFence: %v", err)
	}

	if _, err := escaped.NextAdmissionSequence(); !errors.Is(err, errHandleInvalidated) {
		t.Fatalf("NextAdmissionSequence on escaped handle error = %v, want wrapping errHandleInvalidated", err)
	}
	if err := escaped.MarkCommitted(); !errors.Is(err, errHandleInvalidated) {
		t.Fatalf("MarkCommitted on escaped handle error = %v, want wrapping errHandleInvalidated", err)
	}

	lockDir, err := AdmissionLockDir(sessionDir, "node")
	if err != nil {
		t.Fatalf("AdmissionLockDir: %v", err)
	}
	seq, err := readAdmissionSequenceFile(lockDir)
	if err != nil {
		t.Fatalf("readAdmissionSequenceFile: %v", err)
	}
	if seq != 0 {
		t.Fatalf("sequence = %d after invalidated-handle use, want 0 (nothing persisted)", seq)
	}
}

// TestMarkCommittedWithoutNextErrors covers R2-2: MarkCommitted without a
// prior NextAdmissionSequence must error and leave AdmissionOutcome's
// Admitted false, Sequence 0 -- it must not report Admitted=true for a
// dead-lettered (non-admitted) arrival.
func TestMarkCommittedWithoutNextErrors(t *testing.T) {
	sessionDir := t.TempDir()

	var markErr error
	outcome, err := WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
		markErr = h.MarkCommitted()
		return nil
	})
	if err != nil {
		t.Fatalf("WithAdmissionFence: %v", err)
	}
	if markErr == nil {
		t.Fatal("MarkCommitted without NextAdmissionSequence unexpectedly succeeded")
	}
	if outcome.Admitted {
		t.Fatalf("outcome.Admitted = true, want false (MarkCommitted without Next must not admit)")
	}
	if outcome.Sequence != 0 {
		t.Fatalf("outcome.Sequence = %d, want 0", outcome.Sequence)
	}
}

// TestCommitLastSemanticsDistinguishAdmittedFromNotAdmitted covers R2-2/
// A1-F3's commit-last / typed-outcome requirement: consuming a sequence
// number without calling MarkCommitted must report Admitted=false, so a
// caller can tell a message was NOT actually committed even though a
// sequence number was consumed (for example because the inbox-file commit
// step failed after the sequence was durably persisted).
func TestCommitLastSemanticsDistinguishAdmittedFromNotAdmitted(t *testing.T) {
	sessionDir := t.TempDir()

	outcome, err := WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
		seq, err := h.NextAdmissionSequence()
		if err != nil {
			return err
		}
		if seq != 1 {
			t.Fatalf("NextAdmissionSequence = %d, want 1", seq)
		}
		// Deliberately do NOT call MarkCommitted, simulating a failed
		// inbox commit after the sequence was already persisted.
		return nil
	})
	if err != nil {
		t.Fatalf("WithAdmissionFence: %v", err)
	}
	if outcome.Admitted {
		t.Fatal("outcome.Admitted = true, want false (MarkCommitted was never called)")
	}
	if outcome.Sequence != 1 {
		t.Fatalf("outcome.Sequence = %d, want 1 (the sequence was still consumed and must never be reused)", outcome.Sequence)
	}
}

// TestNextAdmissionSequenceRejectsSecondCall covers A1-F3: at most one
// sequence number per handle.
func TestNextAdmissionSequenceRejectsSecondCall(t *testing.T) {
	sessionDir := t.TempDir()
	_, err := WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
		if _, err := h.NextAdmissionSequence(); err != nil {
			return err
		}
		_, err := h.NextAdmissionSequence()
		if err == nil {
			t.Fatal("second NextAdmissionSequence on the same handle unexpectedly succeeded")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithAdmissionFence: %v", err)
	}
}

// TestAdmissionSequencePersistRoundTrip covers the basic persist/read
// cycle through the handle API.
func TestAdmissionSequencePersistRoundTrip(t *testing.T) {
	sessionDir := t.TempDir()

	outcome, err := WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
		seq, err := h.NextAdmissionSequence()
		if err != nil {
			return err
		}
		if seq != 1 {
			t.Fatalf("first NextAdmissionSequence = %d, want 1", seq)
		}
		return h.MarkCommitted()
	})
	if err != nil {
		t.Fatalf("WithAdmissionFence: %v", err)
	}
	if !outcome.Admitted || outcome.Sequence != 1 {
		t.Fatalf("outcome = %+v, want Admitted=true Sequence=1", outcome)
	}

	outcome, err = WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
		seq, err := h.NextAdmissionSequence()
		if err != nil {
			return err
		}
		if seq != 2 {
			t.Fatalf("second NextAdmissionSequence = %d, want 2", seq)
		}
		return h.MarkCommitted()
	})
	if err != nil {
		t.Fatalf("WithAdmissionFence (second): %v", err)
	}
	if !outcome.Admitted || outcome.Sequence != 2 {
		t.Fatalf("outcome (second) = %+v, want Admitted=true Sequence=2", outcome)
	}

	lockDir, _ := AdmissionLockDir(sessionDir, "node")
	if _, err := os.Stat(filepath.Join(lockDir, admissionSequenceTmpName)); !os.IsNotExist(err) {
		t.Fatalf("temp file still present after persist: err=%v", err)
	}
}

// ---------------------------------------------------------------------
// A1-F2 persistence fault injection (unchanged mapping).
// ---------------------------------------------------------------------

func TestReadAdmissionSequenceFailsClosedOnCorruptFile(t *testing.T) {
	sessionDir := t.TempDir()
	lockDir, err := AdmissionLockDir(sessionDir, "node")
	if err != nil {
		t.Fatalf("AdmissionLockDir: %v", err)
	}
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(lockDir, admissionSequenceFileName)

	cases := []struct {
		name    string
		content string
	}{
		{"empty", ""},
		{"corrupt", "not-a-number"},
		{"negative", "-5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if _, err := readAdmissionSequenceFile(lockDir); !errors.Is(err, ErrCorruptAdmissionSequence) {
				t.Fatalf("readAdmissionSequenceFile(%q) error = %v, want wrapping ErrCorruptAdmissionSequence", tc.content, err)
			}
		})
	}

	// 0 is a valid value (the atomic-init "initialized, none issued"
	// state), not corrupt.
	if err := os.WriteFile(path, []byte("0"), 0o600); err != nil {
		t.Fatalf("WriteFile (zero): %v", err)
	}
	seq, err := readAdmissionSequenceFile(lockDir)
	if err != nil {
		t.Fatalf("readAdmissionSequenceFile(\"0\"): %v, want no error", err)
	}
	if seq != 0 {
		t.Fatalf("readAdmissionSequenceFile(\"0\") = %d, want 0", seq)
	}
}

func TestPersistAdmissionSequenceFileInjectedFailures(t *testing.T) {
	injected := errors.New("injected failure")

	phases := []struct {
		name       string
		mutate     func(ops *sequenceFileOps)
		wantAfter  int
		wantReason string
	}{
		{"create", func(ops *sequenceFileOps) {
			ops.create = func(path string) (*os.File, error) { return nil, injected }
		}, 7, "create/truncate failed before any write, so the prior final file is untouched"},
		{"write", func(ops *sequenceFileOps) {
			ops.write = func(f *os.File, data []byte) (int, error) { return 0, injected }
		}, 7, "write to the temp file failed before any rename, so the prior final file is untouched"},
		{"sync", func(ops *sequenceFileOps) {
			ops.sync = func(f *os.File) error { return injected }
		}, 7, "fsync of the temp file failed before any rename, so the prior final file is untouched"},
		{"rename", func(ops *sequenceFileOps) {
			ops.rename = func(oldpath, newpath string) error { return injected }
		}, 7, "the rename itself failed, so the prior final file is untouched"},
		{"syncDir", func(ops *sequenceFileOps) {
			ops.syncDir = func(f *os.File) error { return injected }
		}, 8, "the rename already completed before the directory fsync failed, so the NEW value is already the readable content; only its crash-durability guarantee is in question, which this primitive cannot observe from inside one process"},
	}

	for _, phase := range phases {
		t.Run(phase.name, func(t *testing.T) {
			lockDir := filepath.Join(t.TempDir(), "node")
			if err := os.MkdirAll(lockDir, 0o700); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := persistAdmissionSequenceFile(lockDir, 7); err != nil {
				t.Fatalf("seeding persistAdmissionSequenceFile: %v", err)
			}

			ops := osSequenceFileOps
			phase.mutate(&ops)
			err := persistAdmissionSequenceFileWithOps(lockDir, 8, ops)
			if !errors.Is(err, injected) {
				t.Fatalf("persistAdmissionSequenceFileWithOps error = %v, want wrapping the injected failure", err)
			}

			seq, readErr := readAdmissionSequenceFile(lockDir)
			if readErr != nil {
				t.Fatalf("readAdmissionSequenceFile after failed persist: %v", readErr)
			}
			if seq != phase.wantAfter {
				t.Fatalf("sequence after failed %s = %d, want %d (%s)", phase.name, seq, phase.wantAfter, phase.wantReason)
			}
		})
	}
}

// ---------------------------------------------------------------------
// A1-F1: fence identity, quarantine composability, symlink rejection.
// ---------------------------------------------------------------------

// TestWithAdmissionFenceSerializesRace covers design §4: two writers racing
// for the same last slot must result in exactly one admission.
func TestWithAdmissionFenceSerializesRace(t *testing.T) {
	sessionDir := t.TempDir()

	const wantAdmitted = 1
	var admitted int32
	var wg sync.WaitGroup
	results := make([]bool, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
				if atomic.LoadInt32(&admitted) < wantAdmitted {
					// R2-4: widen the window between the load and the
					// store so a broken (no-op) fence would be exposed
					// by this test, rather than relying purely on
					// incidental goroutine scheduling.
					time.Sleep(time.Millisecond)
					atomic.AddInt32(&admitted, 1)
					results[idx] = true
				}
				return nil
			})
			if err != nil {
				t.Errorf("WithAdmissionFence: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if admitted != wantAdmitted {
		t.Fatalf("admitted = %d, want exactly %d", admitted, wantAdmitted)
	}
	admittedCount := 0
	for _, r := range results {
		if r {
			admittedCount++
		}
	}
	if admittedCount != 1 {
		t.Fatalf("exactly one caller should have been admitted, got %d", admittedCount)
	}
}

// TestWithAdmissionFenceDeterministicHolderThenContenderHandshake is
// A1-F4's deterministic (non-sleep-based) exclusion proof: a holder blocks
// inside its callback until released; while it holds, a direct LOCK_NB
// probe on the SAME fence path must observe EWOULDBLOCK; after release,
// the same probe must succeed. Unlike a timing-widened race, this asserts
// on the lock's actual state, not on incidental scheduling.
func TestWithAdmissionFenceDeterministicHolderThenContenderHandshake(t *testing.T) {
	sessionDir := t.TempDir()
	lockDir, err := AdmissionLockDir(sessionDir, "node")
	if err != nil {
		t.Fatalf("AdmissionLockDir: %v", err)
	}

	holding := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		_, err := WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
			close(holding)
			<-release
			return nil
		})
		done <- err
	}()

	<-holding
	if err := probeLockNB(filepath.Join(lockDir, admissionFenceFileName)); !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("LOCK_NB probe while holder holds the fence: err = %v, want EWOULDBLOCK", err)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("holder WithAdmissionFence: %v", err)
	}

	if err := probeLockNB(filepath.Join(lockDir, admissionFenceFileName)); err != nil {
		t.Fatalf("LOCK_NB probe after release: %v, want success", err)
	}
}

// probeLockNB attempts a non-blocking exclusive lock on path and releases
// it immediately on success, reporting syscall.EWOULDBLOCK when another
// holder has it locked.
func probeLockNB(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return err
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return nil
}

// TestWithAdmissionFenceIdentityMismatchFailsClosed is A1-F1's TOCTOU
// closure test: a deterministic hook injected between the fence file
// being opened and the lock attempt simulates the file being replaced in
// that exact window. WithAdmissionFence must detect the identity change
// via the post-lock fd/path inode recheck and fail closed, never running
// fn.
func TestWithAdmissionFenceIdentityMismatchFailsClosed(t *testing.T) {
	sessionDir := t.TempDir()
	lockDir, err := AdmissionLockDir(sessionDir, "node")
	if err != nil {
		t.Fatalf("AdmissionLockDir: %v", err)
	}
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	fencePath := filepath.Join(lockDir, admissionFenceFileName)
	if err := os.WriteFile(fencePath, []byte{}, 0o600); err != nil {
		t.Fatalf("pre-creating fence file: %v", err)
	}

	fnRan := false
	// Replace the fence path with a new inode after open and before lock;
	// the fd still refers to the old inode, so the post-lock identity
	// recheck must fail closed.
	hook := func(path string) {
		if err := os.Rename(path, path+".stale"); err != nil {
			t.Fatalf("hook rename away: %v", err)
		}
		if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
			t.Fatalf("hook recreate: %v", err)
		}
	}

	_, err = withAdmissionFenceLock(sessionDir, "node", syscall.Flock, hook, func(h *AdmissionHandle) error {
		fnRan = true
		return nil
	})
	if !errors.Is(err, ErrAdmissionFenceIdentity) {
		t.Fatalf("withAdmissionFenceLock error = %v, want wrapping ErrAdmissionFenceIdentity", err)
	}
	if fnRan {
		t.Fatal("fn ran despite a detected post-lock identity mismatch")
	}
}

// TestWithAdmissionFenceRejectsSymlinkedLockPath covers A1-F1/A1-F5: a
// symlinked (or otherwise non-regular) fence path must be rejected, not
// followed.
func TestWithAdmissionFenceRejectsSymlinkedLockPath(t *testing.T) {
	sessionDir := t.TempDir()
	lockDir, err := AdmissionLockDir(sessionDir, "node")
	if err != nil {
		t.Fatalf("AdmissionLockDir: %v", err)
	}
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target := filepath.Join(sessionDir, "elsewhere")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(lockDir, admissionFenceFileName)); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	_, err = WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
		t.Fatal("fn ran despite a symlinked fence path")
		return nil
	})
	if !errors.Is(err, ErrAdmissionPathNotRegular) {
		t.Fatalf("WithAdmissionFence error = %v, want wrapping ErrAdmissionPathNotRegular", err)
	}
}

// TestQuarantineStyleActorComposesThroughSameFence demonstrates the
// composability A1-F1 requires of this primitive: any actor -- including
// a future quarantine-style caller that renames and recreates admission
// state -- that goes through WithAdmissionFence for the SAME
// (sessionDir, recipient) is mutually excluded with an ordinary holder.
// Actual quarantine wiring is part-2 (P2-1): quarantineMailboxProjection-
// Trees must acquire every affected recipient's fence, in sorted
// recipient order, before renaming the inbox root. This test proves the
// primitive already makes that wiring safe; it does not perform it.
func TestQuarantineStyleActorComposesThroughSameFence(t *testing.T) {
	sessionDir := t.TempDir()

	var order []string
	var mu sync.Mutex
	record := func(s string) {
		mu.Lock()
		order = append(order, s)
		mu.Unlock()
	}

	holderHolding := make(chan struct{})
	holderRelease := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		_, err := WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
			record("holder-start")
			close(holderHolding)
			<-holderRelease
			record("holder-end")
			return nil
		})
		holderDone <- err
	}()
	<-holderHolding

	quarantineDone := make(chan error, 1)
	go func() {
		_, err := WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
			record("quarantine-start")
			record("quarantine-end")
			return nil
		})
		quarantineDone <- err
	}()

	// Give the quarantine goroutine a moment to reach (and block on) the
	// fence; this does not gate correctness (the assertion below is on
	// final ordering, not timing), it only improves the odds of
	// exercising real contention rather than a lucky interleaving.
	time.Sleep(10 * time.Millisecond)
	close(holderRelease)

	if err := <-holderDone; err != nil {
		t.Fatalf("holder WithAdmissionFence: %v", err)
	}
	if err := <-quarantineDone; err != nil {
		t.Fatalf("quarantine-style WithAdmissionFence: %v", err)
	}

	want := []string{"holder-start", "holder-end", "quarantine-start", "quarantine-end"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

// TestWithAdmissionFenceLockFailureClaimsNothing covers design §2.1/§4
// (A1-F1/A1-F4): an injected fence (lock) failure leaves the arrival not
// admitted, and a claim path that cannot take the fence claims nothing --
// fn must not run.
func TestWithAdmissionFenceLockFailureClaimsNothing(t *testing.T) {
	sessionDir := t.TempDir()

	injectedErr := errors.New("injected lock failure")
	fnRan := false

	_, err := withAdmissionFenceLock(sessionDir, "node", func(fd int, how int) error {
		return injectedErr
	}, nil, func(h *AdmissionHandle) error {
		fnRan = true
		return nil
	})

	if !errors.Is(err, injectedErr) {
		t.Fatalf("withAdmissionFenceLock error = %v, want wrapping %v", err, injectedErr)
	}
	if fnRan {
		t.Fatal("fn ran despite a fence lock failure; a claim that cannot take the fence must claim nothing")
	}
}

// ---------------------------------------------------------------------
// A1-F4 cross-process proof.
// ---------------------------------------------------------------------

// TestWithAdmissionFenceCrossProcessExclusion is A1-F4's cross-process
// proof: the parent process holds the fence (blocking inside its
// callback until told to proceed), signals readiness via a marker file a
// subprocess polls for (bounded, not a correctness-bearing race), then
// the subprocess performs a non-blocking LOCK_NB probe directly on the
// same fence file and must observe EWOULDBLOCK. After the parent
// releases, a second subprocess probe must succeed. This proves
// exclusivity is a kernel/process property (flock), not an in-process
// mutex coincidence.
//
// Exercised locally on darwin/arm64 with go test -race; CI's nix build
// runs this package's tests on ubuntu-latest and macos-latest
// (.github/workflows/ci.yml matrix).
func TestWithAdmissionFenceCrossProcessExclusion(t *testing.T) {
	if os.Getenv("MAILBOX_FENCE_HELPER") == "1" {
		runAdmissionFenceProbeHelper()
		return
	}

	sessionDir := t.TempDir()
	readyPath := filepath.Join(sessionDir, "ready-marker")

	holding := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		_, err := WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
			if werr := os.WriteFile(readyPath, []byte("ready"), 0o600); werr != nil {
				return werr
			}
			close(holding)
			<-release
			return nil
		})
		holderDone <- err
	}()
	<-holding

	blockedOut, err := runFenceProbeSubprocess(t, sessionDir)
	if err != nil {
		t.Fatalf("probe subprocess (while held): %v", err)
	}
	if !strings.Contains(blockedOut, "BLOCKED\n") {
		t.Fatalf("probe subprocess output (while held) = %q, want it to contain \"BLOCKED\\n\"", blockedOut)
	}

	close(release)
	if err := <-holderDone; err != nil {
		t.Fatalf("holder WithAdmissionFence: %v", err)
	}

	acquiredOut, err := runFenceProbeSubprocess(t, sessionDir)
	if err != nil {
		t.Fatalf("probe subprocess (after release): %v", err)
	}
	if !strings.Contains(acquiredOut, "ACQUIRED\n") {
		t.Fatalf("probe subprocess output (after release) = %q, want it to contain \"ACQUIRED\\n\"", acquiredOut)
	}
}

func runFenceProbeSubprocess(t *testing.T, sessionDir string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestWithAdmissionFenceCrossProcessExclusion$")
	cmd.Env = append(os.Environ(),
		"MAILBOX_FENCE_HELPER=1",
		"MAILBOX_FENCE_HELPER_SESSIONDIR="+sessionDir,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("helper process failed: %w, output: %s", err, out)
	}
	return string(out), nil
}

// runAdmissionFenceProbeHelper is the subprocess body for
// TestWithAdmissionFenceCrossProcessExclusion. It polls (bounded) for the
// parent's readiness marker, then performs exactly one non-blocking
// LOCK_NB probe on the same fence path and prints BLOCKED or ACQUIRED.
func runAdmissionFenceProbeHelper() {
	sessionDir := os.Getenv("MAILBOX_FENCE_HELPER_SESSIONDIR")
	readyPath := filepath.Join(sessionDir, "ready-marker")

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			fmt.Fprintln(os.Stderr, "timed out waiting for ready marker")
			os.Exit(1)
		}
		time.Sleep(time.Millisecond)
	}

	lockDir, err := AdmissionLockDir(sessionDir, "node")
	if err != nil {
		fmt.Fprintf(os.Stderr, "AdmissionLockDir: %v\n", err)
		os.Exit(1)
	}
	err = probeLockNB(filepath.Join(lockDir, admissionFenceFileName))
	switch {
	case errors.Is(err, syscall.EWOULDBLOCK):
		fmt.Println("BLOCKED")
	case err == nil:
		fmt.Println("ACQUIRED")
	default:
		fmt.Fprintf(os.Stderr, "probeLockNB: %v\n", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------
// R2-3 (A1-F5): recipient/path containment, symlinked dir rejection.
// ---------------------------------------------------------------------

func TestAdmissionLockDirRejectsUnsafeRecipients(t *testing.T) {
	sessionDir := t.TempDir()
	for _, recipient := range []string{"", ".", "..", "a/b", "../escape", "/abs"} {
		if _, err := AdmissionLockDir(sessionDir, recipient); !errors.Is(err, ErrInvalidRecipient) {
			t.Fatalf("AdmissionLockDir(%q) error = %v, want wrapping ErrInvalidRecipient", recipient, err)
		}
	}
}

func TestWithAdmissionFenceNeverCreatesInboxDir(t *testing.T) {
	sessionDir := t.TempDir()
	_, err := WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
		_, err := h.NextAdmissionSequence()
		return err
	})
	if err != nil {
		t.Fatalf("WithAdmissionFence: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(sessionDir, "inbox")); !os.IsNotExist(statErr) {
		t.Fatalf("an \"inbox\" directory was created (or stat failed unexpectedly): %v", statErr)
	}
}

func TestWithAdmissionFenceTightensPermissiveExistingDir(t *testing.T) {
	sessionDir := t.TempDir()
	lockDir, err := AdmissionLockDir(sessionDir, "node")
	if err != nil {
		t.Fatalf("AdmissionLockDir: %v", err)
	}
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatalf("MkdirAll (permissive): %v", err)
	}

	_, err = WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error { return nil })
	if err != nil {
		t.Fatalf("WithAdmissionFence: %v", err)
	}

	info, err := os.Stat(lockDir)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("lock dir permissions = %o, want owner-only (no group/other bits)", info.Mode().Perm())
	}
}

// TestWithAdmissionFenceRejectsSymlinkedRecipientDir covers R2-3: a
// symlinked recipient directory must be rejected, and the symlink
// target's own mode must be left unchanged (proving the rejection
// happens before any chmod/open attempt follows the link).
func TestWithAdmissionFenceRejectsSymlinkedRecipientDir(t *testing.T) {
	sessionDir := t.TempDir()
	root := filepath.Join(sessionDir, admissionLockRootDirName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("MkdirAll (root): %v", err)
	}
	targetDir := filepath.Join(sessionDir, "elsewhere-dir")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll (target): %v", err)
	}
	if err := os.Symlink(targetDir, filepath.Join(root, "node")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	_, err := WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
		t.Fatal("fn ran despite a symlinked recipient dir")
		return nil
	})
	if !errors.Is(err, ErrAdmissionPathNotRegular) {
		t.Fatalf("WithAdmissionFence error = %v, want wrapping ErrAdmissionPathNotRegular", err)
	}

	info, statErr := os.Stat(targetDir)
	if statErr != nil {
		t.Fatalf("Stat(targetDir): %v", statErr)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("symlink target mode = %o, want unchanged 0755 (the rejection must not have chmodded the followed target)", info.Mode().Perm())
	}
}

// TestWithAdmissionFenceRejectsSymlinkedRoot covers R2-3: a symlinked
// mailbox-locks root itself must be rejected.
func TestWithAdmissionFenceRejectsSymlinkedRoot(t *testing.T) {
	sessionDir := t.TempDir()
	targetDir := filepath.Join(sessionDir, "elsewhere-root")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll (target): %v", err)
	}
	if err := os.Symlink(targetDir, filepath.Join(sessionDir, admissionLockRootDirName)); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	_, err := WithAdmissionFence(sessionDir, "node", func(h *AdmissionHandle) error {
		t.Fatal("fn ran despite a symlinked mailbox-locks root")
		return nil
	})
	if !errors.Is(err, ErrAdmissionPathNotRegular) {
		t.Fatalf("WithAdmissionFence error = %v, want wrapping ErrAdmissionPathNotRegular", err)
	}

	info, statErr := os.Stat(targetDir)
	if statErr != nil {
		t.Fatalf("Stat(targetDir): %v", statErr)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("symlink target mode = %o, want unchanged 0755", info.Mode().Perm())
	}
}
