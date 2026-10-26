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
	"testing"
	"time"
)

type captureRuntime struct {
	*storedRuntime
	seen []Owner
	fail string
}

func (r *captureRuntime) CapturePane(_ context.Context, selected Binding) (string, error) {
	owner := selected.Owner
	r.seen = append(r.seen, owner)
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
	defer root.Close()
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
	if result.Path == "" || !strings.Contains(result.Observations[1].Reason, "fixture pane read failed") {
		t.Fatalf("missing failed-capture evidence: %#v", result)
	}
	if _, err := os.Stat(mailPath(m, "peer", "inbox", msg.ID)); err != nil {
		t.Fatalf("capture failure undid send: %v", err)
	}
	claim, err := m.Claim(context.Background(), mailRuntime("peer"), msg.ID)
	if err != nil || claim == nil {
		t.Fatalf("capture failure blocked POP: %#v %v", claim, err)
	}
	unknown := relatedCaptureRuntime()
	unknown.snapshot.AbsentRoles = []string{"peer"} // conflicts with live peer
	result = m.CaptureRelated(context.Background(), unknown, msg)
	if result.Reason != "native related-pane identity unknown" || len(unknown.seen) != 0 {
		t.Fatalf("unknown binding still read panes: %#v %#v", result, unknown.seen)
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
	defer root.Close()
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
	defer root.Close()
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
