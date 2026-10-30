package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type captureRuntime struct {
	*storedRuntime
	seen      []Owner
	fail      string
	onCapture func(Binding)
}

func (r *captureRuntime) CapturePane(_ context.Context, selected Binding) (string, error) {
	owner := selected.Owner
	r.seen = append(r.seen, owner)
	if r.onCapture != nil {
		r.onCapture(selected)
	}
	if owner.Pane == r.fail {
		return "", errors.New("fixture pane read failed")
	}
	return "screen for " + owner.Pane, nil
}

func relatedCaptureRuntime() *captureRuntime {
	base := mailRuntime("worker")
	unrelated := base.snapshot.Caller
	unrelated.Role, unrelated.Owner.Pane = "unrelated", "%3"
	base.snapshot.Bindings = append(base.snapshot.Bindings, unrelated)
	return &captureRuntime{storedRuntime: base}
}

func TestCaptureRelatedReadsOnlyMailPanesAndSavesIncarnation(t *testing.T) {
	m := mailFixture(t)
	rt := relatedCaptureRuntime()
	msg, err := m.Send(context.Background(), rt, "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	result := m.CaptureRelated(context.Background(), rt, msg)
	if result.Reason != "" || result.Path == "" || len(result.Observations) != 2 || len(rt.seen) != 2 {
		t.Fatalf("capture: %#v, calls: %#v", result, rt.seen)
	}
	if rt.seen[0].Pane != "%1" || rt.seen[1].Pane != "%2" ||
		result.Incarnation != rt.snapshot.Caller.Owner.Incarnation {
		t.Fatalf("unrelated pane or unknown incarnation: %#v", result)
	}
	var saved CaptureResult
	root, err := os.OpenRoot(filepath.Dir(result.Path))
	if err != nil {
		t.Fatal(err)
	}
	defer func(closeFn func() error) { _ = closeFn() }(root.Close)
	if err := readJSON(root, filepath.Base(result.Path), &saved); err != nil || saved.MessageID != msg.ID {
		t.Fatalf("saved evidence: %#v %v", saved, err)
	}
	if saved.Observations[0].Fingerprint == "" || saved.Observations[0].Fingerprint != result.Observations[0].Fingerprint {
		t.Fatalf("saved fingerprint differs: %#v", saved)
	}
}

func TestCaptureFailureCannotUndoCommittedMail(t *testing.T) {
	m := mailFixture(t)
	rt := relatedCaptureRuntime()
	rt.fail = "%2"
	msg, err := m.Send(context.Background(), rt, "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	result := m.CaptureRelated(context.Background(), rt, msg)
	if result.Path == "" || !strings.Contains(result.Reason, "fixture pane read failed") ||
		len(result.Observations) != 0 || filepath.Dir(result.Path) == filepath.Join(m.Store.StateRoot, "unknown-captures") {
		t.Fatalf("partial target escaped failed capture: %#v", result)
	}
	data, err := os.ReadFile(result.Path)
	if err != nil || strings.Contains(string(data), "screen for") {
		t.Fatalf("partial target escaped persisted failure receipt: %s %v", data, err)
	}
	if _, err := os.Stat(mailPath(m, "peer", "inbox", msg.ID)); err != nil {
		t.Fatalf("capture failure undid send: %v", err)
	}
	claim, err := m.Claim(context.Background(), mailRuntime("peer"), msg.ID)
	if err != nil || claim == nil {
		t.Fatalf("capture failure blocked POP: %#v %v", claim, err)
	}
	other, err := m.Send(context.Background(), relatedCaptureRuntime(), "peer", "other", "", false)
	if err != nil {
		t.Fatal(err)
	}
	unknown := relatedCaptureRuntime()
	unknown.snapshot.AbsentRoles = []string{"peer"} // conflicts with live peer
	result = m.CaptureRelated(context.Background(), unknown, other)
	if result.Reason != "native related-pane identity unknown" || len(unknown.seen) != 0 {
		t.Fatalf("unknown binding still read panes: %#v %#v", result, unknown.seen)
	}
}

// Prepublication failures leave no positive name or staging text. A fault
// after rename is rolled back before the capture returns.
func TestCaptureSaveFaultDoesNotPublishText(t *testing.T) {
	for _, phase := range []string{"capture-after-write", "capture-after-readback", "capture-after-publish"} {
		t.Run(phase, func(t *testing.T) {
			m := mailFixture(t)
			rt := relatedCaptureRuntime()
			msg, err := m.Send(context.Background(), rt, "peer", "body", "", false)
			if err != nil {
				t.Fatal(err)
			}
			m.afterStep = func(step string) error {
				if step == phase {
					return errors.New("injected final-save failure")
				}
				return nil
			}
			result := m.CaptureRelated(context.Background(), rt, msg)
			if result.Path != "" || len(result.Observations) != 0 ||
				!strings.Contains(result.Reason, "injected final-save failure") {
				t.Fatalf("failed save advertised capture: %#v", result)
			}
			dir := filepath.Join(m.Store.StateRoot, "repo", "agents", "worker", "captures")
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed save left text on disk: %#v %v", entries, err)
			}
			if _, err := os.Stat(mailPath(m, "peer", "inbox", msg.ID)); err != nil {
				t.Fatalf("capture failure undid send: %v", err)
			}
		})
	}
}

func TestCaptureRetentionFaultKeepsPublishedText(t *testing.T) {
	m := mailFixture(t)
	rt := relatedCaptureRuntime()
	msg, err := m.Send(context.Background(), rt, "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	m.afterStep = func(step string) error {
		if step == "capture-after-prune" {
			return errors.New("injected retention failure")
		}
		return nil
	}
	result := m.CaptureRelated(context.Background(), rt, msg)
	if result.Path == "" || len(result.Observations) != 2 ||
		!strings.Contains(result.Reason, "injected retention failure") {
		t.Fatalf("retention failure lost positive capture: %#v", result)
	}
	if _, err := os.Stat(result.Path); err != nil {
		t.Fatalf("published capture missing: %v", err)
	}
	if _, err := os.Stat(mailPath(m, "peer", "inbox", msg.ID)); err != nil {
		t.Fatalf("retention failure undid send: %v", err)
	}
}

func TestCaptureRejectsRelatedBindingChangeDuringRead(t *testing.T) {
	m := mailFixture(t)
	rt := relatedCaptureRuntime()
	msg, err := m.Send(context.Background(), rt, "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	rt.onCapture = func(selected Binding) {
		if selected.Role != "peer" {
			return
		}
		for i := range rt.snapshot.Bindings {
			if rt.snapshot.Bindings[i].Role == "peer" {
				rt.snapshot.Bindings[i].PanePID = "900"
				break
			}
		}
	}
	result := m.CaptureRelated(context.Background(), rt, msg)
	if result.Path != "" || len(result.Observations) != 0 ||
		!strings.Contains(result.Reason, "related capture binding changed") {
		t.Fatalf("changed target still published: %#v", result)
	}
	path := filepath.Join(m.Store.StateRoot, "repo", "agents", "worker", "captures", msg.ID+".json")
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("changed target left positive capture: %v", err)
	}
	if _, err := os.Stat(mailPath(m, "peer", "inbox", msg.ID)); err != nil {
		t.Fatalf("capture failure undid send: %v", err)
	}
	claim, err := m.Claim(context.Background(), mailRuntime("peer"), msg.ID)
	if err != nil || claim == nil {
		t.Fatalf("capture failure blocked POP: %#v %v", claim, err)
	}
}

func TestCaptureRetentionPrunesOnlyOwnedSnapshots(t *testing.T) {
	rootPath := filepath.Join(t.TempDir(), "captures")
	if err := os.Mkdir(rootPath, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func(closeFn func() error) { _ = closeFn() }(root.Close)
	now := time.Now().UTC()
	oldID := fmt.Sprintf("m-%032x", 1)
	if err := writeJSON(root, oldID+".json", CaptureResult{Version: 1, MessageID: oldID, CapturedAt: now.Add(-8 * 24 * time.Hour).Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= captureLimit+2; i++ {
		id := fmt.Sprintf("m-%032x", i)
		stamp := now.Add(time.Duration(i) * time.Millisecond).Add(-time.Hour)
		if err := writeJSON(root, id+".json", CaptureResult{Version: 1, MessageID: id, CapturedAt: stamp.Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := root.WriteFile("operator-note.txt", []byte("retain"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := pruneCaptures(root, now); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Lstat(oldID + ".json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("old owned capture survived")
	}
	if got, err := root.ReadFile("operator-note.txt"); err != nil || string(got) != "retain" {
		t.Fatal("unrelated file was pruned", err)
	}
	entries, err := os.ReadDir(rootPath)
	if err != nil || len(entries) != captureLimit+1 {
		t.Fatalf("retention count: %d %v", len(entries), err)
	}
}

func TestCaptureRetentionCountsFutureOwnedSnapshotsAfterClockRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "captures")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func(closeFn func() error) { _ = closeFn() }(root.Close)
	now := time.Now().UTC()
	for i := 0; i < captureLimit+2; i++ {
		id := fmt.Sprintf("m-%032x", i)
		when := now.Add(time.Hour + time.Duration(i)*time.Second)
		if err := writeJSON(root, id+".json", CaptureResult{Version: 1, MessageID: id, CapturedAt: when.Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := root.WriteFile("operator-note.txt", []byte("retain"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := pruneCaptures(root, now); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != captureLimit+1 {
		t.Fatalf("rollback count: %d %v", len(entries), err)
	}
	if got, err := root.ReadFile("operator-note.txt"); err != nil || string(got) != "retain" {
		t.Fatalf("unrelated file changed: %q %v", got, err)
	}
}

func TestCaptureRelatedKeepsFreshReceiptAfterClockRollback(t *testing.T) {
	m := mailFixture(t)
	rt := relatedCaptureRuntime()
	msg, err := m.Send(context.Background(), rt, "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.Store.StateRoot, "repo", "agents", "worker", "captures")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i := 0; i < captureLimit; i++ {
		id := fmt.Sprintf("m-%032x", i)
		when := now.Add(time.Hour + time.Duration(i)*time.Second)
		if err := writeJSON(root, id+".json", CaptureResult{Version: 1, MessageID: id, CapturedAt: when.Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := root.WriteFile("operator-note.txt", []byte("retain"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	result := m.CaptureRelated(context.Background(), rt, msg)
	if result.Reason != "" || result.Path == "" {
		t.Fatalf("fresh capture did not persist: %#v", result)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestCaptureReceiptReader$")
	cmd.Env = append(os.Environ(), "POSTMAN_CAPTURE_RECEIPT="+result.Path, "POSTMAN_CAPTURE_MESSAGE_ID="+msg.ID)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("separate-process receipt read: %s: %v", output, err)
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != captureLimit+1 {
		t.Fatalf("owned cap plus unrelated note: %d %v", len(entries), err)
	}
	if note, err := os.ReadFile(filepath.Join(path, "operator-note.txt")); err != nil || string(note) != "retain" {
		t.Fatalf("unrelated note changed: %q %v", note, err)
	}
}

func TestCaptureReceiptReader(t *testing.T) {
	path := os.Getenv("POSTMAN_CAPTURE_RECEIPT")
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved CaptureResult
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.MessageID != os.Getenv("POSTMAN_CAPTURE_MESSAGE_ID") || len(saved.Observations) != 2 {
		if wantReason := os.Getenv("POSTMAN_CAPTURE_EXPECT_REASON"); wantReason != "" &&
			saved.MessageID == os.Getenv("POSTMAN_CAPTURE_MESSAGE_ID") &&
			saved.Reason == wantReason && len(saved.Observations) == 0 && !strings.Contains(string(data), "screen for") {
			info, err := os.Stat(path)
			if err == nil && privateInfo(info, false) {
				return
			}
		}
		t.Fatalf("wrong persisted mail evidence: %#v", saved)
	}
	for _, observation := range saved.Observations {
		digest := sha256.Sum256([]byte(observation.Text))
		want := "sha256:" + hex.EncodeToString(digest[:])
		if observation.Fingerprint != want {
			t.Fatalf("fingerprint mismatch for %s: %q != %q", observation.Role, observation.Fingerprint, want)
		}
	}
}

func TestCaptureUnknownUsesPrivateReasonOnlyReceipt(t *testing.T) {
	m := mailFixture(t)
	rt := relatedCaptureRuntime()
	msg, err := m.Send(context.Background(), rt, "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	rt.snapshot.AbsentRoles = []string{"peer"} // conflicts with live peer
	result := m.CaptureRelated(context.Background(), rt, msg)
	if result.Reason != "native related-pane identity unknown" || result.Path == "" || len(rt.seen) != 0 {
		t.Fatalf("unknown snapshot receipt: %#v", result)
	}
	if filepath.Dir(result.Path) != filepath.Join(m.Store.StateRoot, "unknown-captures") {
		t.Fatalf("unknown receipt escaped independent root: %q", result.Path)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestCaptureReceiptReader$")
	cmd.Env = append(os.Environ(), "POSTMAN_CAPTURE_RECEIPT="+result.Path,
		"POSTMAN_CAPTURE_MESSAGE_ID="+msg.ID, "POSTMAN_CAPTURE_EXPECT_REASON="+result.Reason)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("separate-process unknown receipt: %s: %v", output, err)
	}
	unsupported := relatedCaptureRuntime()
	other, err := m.Send(context.Background(), unsupported, "peer", "other", "", false)
	if err != nil {
		t.Fatal(err)
	}
	missing := m.CaptureRelated(context.Background(), unsupported.storedRuntime, other)
	if missing.Reason != "pane capture unsupported by runtime" || missing.Path == "" {
		t.Fatalf("unsupported runtime receipt: %#v", missing)
	}
	cmd = exec.Command(os.Args[0], "-test.run=^TestCaptureReceiptReader$")
	cmd.Env = append(os.Environ(), "POSTMAN_CAPTURE_RECEIPT="+missing.Path,
		"POSTMAN_CAPTURE_MESSAGE_ID="+other.ID, "POSTMAN_CAPTURE_EXPECT_REASON="+missing.Reason)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("separate-process unsupported receipt: %s: %v", output, err)
	}
	m.Store.StateRoot = filepath.Join(t.TempDir(), "missing")
	unavailable := m.CaptureRelated(context.Background(), unsupported.storedRuntime, other)
	if unavailable.Path != "" || !strings.Contains(unavailable.Reason, "durability unavailable") {
		t.Fatalf("invented receipt for missing state root: %#v", unavailable)
	}
}

func captureTestPaths(t *testing.T, m Mail, id string) (string, string) {
	t.Helper()
	dir := filepath.Join(m.Store.StateRoot, "repo", "agents", "worker", "captures")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "."+id+".json.pending"), filepath.Join(dir, id+".json")
}

func TestCapturePendingCollisionPreservesUnrelatedOccupant(t *testing.T) {
	m := mailFixture(t)
	rt := relatedCaptureRuntime()
	msg, err := m.Send(context.Background(), rt, "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	pending, final := captureTestPaths(t, m, msg.ID)
	if err := os.WriteFile(pending, []byte("operator sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := m.CaptureRelated(context.Background(), rt, msg)
	if got.Path != final || got.Reason != "" || len(got.Observations) != 2 {
		t.Fatalf("collision blocked independent retry: %#v", got)
	}
	data, err := os.ReadFile(pending)
	if err != nil || string(data) != "operator sentinel" {
		t.Fatalf("colliding occupant removed: %q %v", data, err)
	}
	if _, err := os.Lstat(final); err != nil {
		t.Fatalf("independent retry did not publish final: %v", err)
	}
}

func TestCaptureNonceCrashBoundariesRetry(t *testing.T) {
	for _, boundary := range []string{"unmarked empty pending", "marked partial pending", "owner-only marker"} {
		t.Run(boundary, func(t *testing.T) {
			m := mailFixture(t)
			rt := relatedCaptureRuntime()
			msg, err := m.Send(context.Background(), rt, "peer", "body", "", false)
			if err != nil {
				t.Fatal(err)
			}
			pendingPath, final := captureTestPaths(t, m, msg.ID)
			root, err := os.OpenRoot(filepath.Dir(pendingPath))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			pending, err := newPendingCaptureName(root, msg.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch boundary {
			case "unmarked empty pending":
				err = root.WriteFile(pending, nil, 0o600)
			case "marked partial pending", "owner-only marker":
				_, _, err = writePendingCapture(root, pending, CaptureResult{Version: 1, MessageID: msg.ID})
				if err == nil && boundary == "marked partial pending" {
					err = root.WriteFile(pending, []byte("partial plaintext"), 0o600)
				}
				if err == nil && boundary == "owner-only marker" {
					err = root.Remove(pending)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			got := m.CaptureRelated(context.Background(), rt, msg)
			if got.Path != final || got.Reason != "" || len(got.Observations) != 2 {
				t.Fatalf("same-ID retry failed after %s: %#v", boundary, got)
			}
			_, pendingErr := root.Lstat(pending)
			_, ownerErr := root.Lstat(pendingOwnerName(pending))
			switch boundary {
			case "unmarked empty pending":
				if pendingErr != nil || !errors.Is(ownerErr, os.ErrNotExist) {
					t.Fatalf("unmarked occupant changed: pending=%v owner=%v", pendingErr, ownerErr)
				}
			case "marked partial pending":
				if !errors.Is(pendingErr, os.ErrNotExist) || !errors.Is(ownerErr, os.ErrNotExist) {
					t.Fatalf("marked orphan remains: pending=%v owner=%v", pendingErr, ownerErr)
				}
			default:
				if !errors.Is(pendingErr, os.ErrNotExist) || ownerErr != nil {
					t.Fatalf("owner-only marker changed: pending=%v owner=%v", pendingErr, ownerErr)
				}
			}
		})
	}
}

func TestCaptureRecoversPendingOnlyCrash(t *testing.T) {
	m := mailFixture(t)
	rt := relatedCaptureRuntime()
	msg, err := m.Send(context.Background(), rt, "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	pending, final := captureTestPaths(t, m, msg.ID)
	old := CaptureResult{Version: 1, MessageID: msg.ID, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := func() error {
		root, err := os.OpenRoot(filepath.Dir(pending))
		if err != nil {
			return err
		}
		defer func() { _ = root.Close() }()
		_, _, err = writePendingCapture(root, filepath.Base(pending), old)
		return err
	}(); err != nil {
		t.Fatal(err)
	}
	got := m.CaptureRelated(context.Background(), rt, msg)
	if got.Path != final || got.Reason != "" || len(got.Observations) != 2 {
		t.Fatalf("pending-only crash did not retry: %#v", got)
	}
	for _, name := range []string{pending, pending + ".owner"} {
		if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("orphan pending remains %s: %v", name, err)
		}
	}
	root, err := os.OpenRoot(filepath.Dir(final))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	var saved CaptureResult
	if err := readJSON(root, filepath.Base(final), &saved); err != nil || saved.MessageID != msg.ID {
		t.Fatalf("retry final unreadable: %#v %v", saved, err)
	}
}

func TestCaptureRecoversLinkedPairCrash(t *testing.T) {
	m := mailFixture(t)
	rt := relatedCaptureRuntime()
	msg, err := m.Send(context.Background(), rt, "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	pending, final := captureTestPaths(t, m, msg.ID)
	old := CaptureResult{Version: 1, MessageID: msg.ID, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := func() error {
		root, err := os.OpenRoot(filepath.Dir(pending))
		if err != nil {
			return err
		}
		defer func() { _ = root.Close() }()
		_, _, err = writePendingCapture(root, filepath.Base(pending), old)
		return err
	}(); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(pending, final); err != nil {
		t.Fatal(err)
	}
	got := m.CaptureRelated(context.Background(), rt, msg)
	if got.Path != final || len(got.Observations) != 0 || !strings.Contains(got.Reason, "prior published capture recovered") {
		t.Fatalf("linked-pair crash produced false result: %#v", got)
	}
	for _, name := range []string{pending, pending + ".owner"} {
		if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("linked pending remains %s: %v", name, err)
		}
	}
	root, err := os.OpenRoot(filepath.Dir(final))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	var saved CaptureResult
	if err := readJSON(root, filepath.Base(final), &saved); err != nil || saved.MessageID != msg.ID {
		t.Fatalf("recovered final unreadable: %#v %v", saved, err)
	}
}

func TestCaptureRecoversMarkedNonceLinkedPair(t *testing.T) {
	m := mailFixture(t)
	rt := relatedCaptureRuntime()
	msg, err := m.Send(context.Background(), rt, "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	pendingPath, final := captureTestPaths(t, m, msg.ID)
	root, err := os.OpenRoot(filepath.Dir(pendingPath))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	pending, err := newPendingCaptureName(root, msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	old := CaptureResult{Version: 1, MessageID: msg.ID, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if _, _, err := writePendingCapture(root, pending, old); err != nil {
		t.Fatal(err)
	}
	if err := root.Link(pending, filepath.Base(final)); err != nil {
		t.Fatal(err)
	}
	got := m.CaptureRelated(context.Background(), rt, msg)
	if got.Path != final || len(got.Observations) != 0 || !strings.Contains(got.Reason, "prior published capture recovered") {
		t.Fatalf("marked nonce linked pair was not recovered: %#v", got)
	}
	for _, name := range []string{pending, pendingOwnerName(pending)} {
		if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("marked nonce residue remains %s: %v", name, err)
		}
	}
}

func TestCapturePrunesOnlyExpiredPrivatePending(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	now := time.Now().UTC()
	oldID := fmt.Sprintf("m-%032x", 900)
	freshID := fmt.Sprintf("m-%032x", 901)
	unrelatedID := fmt.Sprintf("m-%032x", 902)
	oldName := "." + oldID + ".json.pending"
	freshName := "." + freshID + ".json.pending"
	unrelatedName := "." + unrelatedID + ".json.pending"
	created, marked, err := writePendingCapture(root, oldName, CaptureResult{Version: 1, MessageID: oldID})
	if err != nil || !created || !marked {
		t.Fatalf("create owned pending: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, oldName), []byte("partial private staged text"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{freshName: "fresh sentinel", unrelatedName: "old unrelated sentinel"} {
		if err := root.WriteFile(name, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{oldName, unrelatedName} {
		if err := os.Chtimes(filepath.Join(dir, name), now.Add(-8*24*time.Hour), now.Add(-8*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := pruneCaptures(root, now); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{oldName, pendingOwnerName(oldName)} {
		if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expired owned pending remains %s: %v", name, err)
		}
	}
	for name, want := range map[string]string{freshName: "fresh sentinel", unrelatedName: "old unrelated sentinel"} {
		if data, err := root.ReadFile(name); err != nil || string(data) != want {
			t.Fatalf("unrelated pending changed %s: %q %v", name, data, err)
		}
	}
}

func TestCapturePartialWriteChild(t *testing.T) {
	dir := os.Getenv("POSTMAN_CAPTURE_KILL_DIR")
	if dir == "" {
		return
	}
	id := os.Getenv("POSTMAN_CAPTURE_KILL_ID")
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	pending, err := newPendingCaptureName(root, id)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = writePendingCaptureWithHook(root, pending,
		CaptureResult{
			Version: 1, MessageID: id, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano),
			Observations: []PaneObservation{{Text: strings.Repeat("sensitive pane text ", 100)}},
		},
		func() {
			if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
				t.Fatal(err)
			}
		})
	t.Fatalf("partial-write kill did not occur: %v", err)
}

func spawnKilledPartialCapture(t *testing.T, dir, id string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCapturePartialWriteChild$")
	cmd.Env = append(os.Environ(), "POSTMAN_CAPTURE_KILL_DIR="+dir, "POSTMAN_CAPTURE_KILL_ID="+id)
	err := cmd.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("child did not fail by signal: %v", err)
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || status.Signal() != syscall.SIGKILL {
		t.Fatalf("child exit was not SIGKILL: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	pending := ""
	for _, entry := range entries {
		if pendingID, attempt, ok := capturePendingIdentity(entry.Name()); ok && pendingID == id && attempt != "" {
			pending = entry.Name()
			break
		}
	}
	if pending == "" {
		t.Fatal("killed child left no nonce pending capture")
	}
	data, err := os.ReadFile(filepath.Join(dir, pending))
	if err != nil || !strings.Contains(string(data), "sensitive pane text") || json.Valid(data) {
		t.Fatalf("child did not leave partial pane text: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, pendingOwnerName(pending))); err != nil {
		t.Fatalf("durable owner missing: %v", err)
	}
	return pending
}

func TestCapturePartialWriteKillRecovery(t *testing.T) {
	m := mailFixture(t)
	rt := relatedCaptureRuntime()
	msg, err := m.Send(context.Background(), rt, "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	pendingPath, final := captureTestPaths(t, m, msg.ID)
	pending := spawnKilledPartialCapture(t, filepath.Dir(pendingPath), msg.ID)
	got := m.CaptureRelated(context.Background(), rt, msg)
	if got.Path != final || got.Reason != "" || len(got.Observations) != 2 {
		t.Fatalf("retry after partial write failed: %#v", got)
	}
	for _, name := range []string{pending, pendingOwnerName(pending)} {
		if _, err := os.Lstat(filepath.Join(filepath.Dir(pendingPath), name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("partial staging remains %s: %v", name, err)
		}
	}
}

func TestCapturePartialWriteIdleRetention(t *testing.T) {
	dir := t.TempDir()
	id := fmt.Sprintf("m-%032x", 903)
	pending := spawnKilledPartialCapture(t, dir, id)
	old := time.Now().UTC().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, pending), old, old); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	// Retention runs without a later capture or successful publication.
	if err := pruneCaptures(root, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{pending, pendingOwnerName(pending)} {
		if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("idle partial staging remains %s: %v", name, err)
		}
	}
}

func TestCaptureUnmarkedPendingNeverBecomesRecoveryAuthority(t *testing.T) {
	for _, attempt := range []string{"legacy", "nonce"} {
		for _, linked := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-linked-%v", attempt, linked), func(t *testing.T) {
				m := mailFixture(t)
				rt := relatedCaptureRuntime()
				msg, err := m.Send(context.Background(), rt, "peer", "body", "", false)
				if err != nil {
					t.Fatal(err)
				}
				pendingPath, final := captureTestPaths(t, m, msg.ID)
				root, err := os.OpenRoot(filepath.Dir(pendingPath))
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = root.Close() }()
				pending := filepath.Base(pendingPath)
				if attempt == "nonce" {
					pending = "." + msg.ID + ".json.pending." + strings.Repeat("a", 32)
				}
				old := CaptureResult{Version: 1, MessageID: msg.ID, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano)}
				data, err := json.Marshal(old)
				if err != nil {
					t.Fatal(err)
				}
				if err := root.WriteFile(pending, append(data, '\n'), 0o600); err != nil {
					t.Fatal(err)
				}
				if linked {
					if err := root.Link(pending, filepath.Base(final)); err != nil {
						t.Fatal(err)
					}
				}
				got := m.CaptureRelated(context.Background(), rt, msg)
				if linked {
					if got.Path != "" || len(got.Observations) != 0 || !strings.Contains(got.Reason, "unmarked linked capture is ambiguous") {
						t.Fatalf("unmarked linked pair was trusted: %#v", got)
					}
				} else if got.Path != final || got.Reason != "" {
					t.Fatalf("unmarked orphan blocked fresh attempt: %#v", got)
				}
				saved, err := root.ReadFile(pending)
				if err != nil || string(saved) != string(append(data, '\n')) {
					t.Fatalf("unmarked pending was changed: %q %v", saved, err)
				}
				if linked {
					infoA, errA := root.Lstat(pending)
					infoB, errB := root.Lstat(filepath.Base(final))
					if errA != nil || errB != nil || !os.SameFile(infoA, infoB) {
						t.Fatal("unmarked linked files changed", errA, errB)
					}
				}
			})
		}
	}
}

func TestCaptureCrashResidueBeyondMailSnapshotLimit(t *testing.T) {
	m := mailFixture(t)
	rt := relatedCaptureRuntime()
	msg, err := m.Send(context.Background(), rt, "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	pendingPath, final := captureTestPaths(t, m, msg.ID)
	dir := filepath.Dir(pendingPath)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	for i := range 2050 {
		id := fmt.Sprintf("m-%032x", i+10000)
		name := "." + id + ".json.pending." + strings.Repeat("a", 28) + fmt.Sprintf("%04x", i)
		if err := root.WriteFile(name, []byte("unrelated pending bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldID := fmt.Sprintf("m-%032x", 9999)
	oldName := "." + oldID + ".json.pending." + strings.Repeat("b", 32)
	if _, _, err := writePendingCapture(root, oldName, CaptureResult{Version: 1, MessageID: oldID}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, oldName), old, old); err != nil {
		t.Fatal(err)
	}
	captureStart := time.Now()
	got := m.CaptureRelated(context.Background(), rt, msg)
	t.Logf("capture with 2050 unrelated pending names: %s", time.Since(captureStart))
	if got.Path != final || got.Reason != "" || len(got.Observations) != 2 {
		t.Fatalf("pending backlog blocked capture: %#v", got)
	}
	for _, name := range []string{oldName, pendingOwnerName(oldName)} {
		if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expired marked residue remains %s: %v", name, err)
		}
	}
	statusStart := time.Now()
	state := mustState(t, m, "worker")
	t.Logf("activity read with 2050 unrelated pending names: %s", time.Since(statusStart))
	if state.Activity.ObservedAt == "" || state.Activity.Reason == "saved pane evidence unavailable" || state.Activity.Reason == "saved pane evidence invalid" {
		t.Fatalf("pending backlog hid committed capture: %+v", state.Activity)
	}
}

func TestCaptureUnlinkAndRollbackUncertainty(t *testing.T) {
	for _, phase := range []string{"capture-before-pending-remove", "capture-before-rollback-remove", "capture-before-rollback-sync"} {
		t.Run(phase, func(t *testing.T) {
			m := mailFixture(t)
			rt := relatedCaptureRuntime()
			msg, err := m.Send(context.Background(), rt, "peer", "body", "", false)
			if err != nil {
				t.Fatal(err)
			}
			m.afterStep = func(step string) error {
				if step == phase || (phase != "capture-before-pending-remove" && step == "capture-after-publish") {
					return errors.New("injected " + step)
				}
				return nil
			}
			got := m.CaptureRelated(context.Background(), rt, msg)
			pending, final := captureTestPaths(t, m, msg.ID)
			if len(got.Observations) != 0 || !strings.Contains(got.Reason, "injected") {
				t.Fatalf("fault advertised observations: %#v", got)
			}
			switch phase {
			case "capture-before-rollback-remove":
				if got.Path != final {
					t.Fatalf("rollback uncertainty lost possible path: %#v", got)
				}
				if _, err := os.Lstat(final); err != nil {
					t.Fatalf("uncertain final absent: %v", err)
				}
			case "capture-before-rollback-sync":
				if got.Path != final {
					t.Fatalf("sync uncertainty lost possible path: %#v", got)
				}
			default:
				if got.Path != "" {
					t.Fatalf("successful rollback advertised capture: %#v", got)
				}
			}
			if _, err := os.Lstat(pending); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("owned pending not cleaned: %v", err)
			}
		})
	}
}
