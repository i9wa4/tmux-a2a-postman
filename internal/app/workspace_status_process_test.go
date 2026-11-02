package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

func TestWorkspaceStatusProcessIsReadOnlyAndReportsSavedFacts(t *testing.T) {
	f := newPublicApprovalFixture(t)
	missing, err := f.run(f.worker, "", "get-status", "--config", f.config)
	if err == nil || missing.Reason == "" || len(missing.Sessions) != 0 {
		t.Fatal("missing state not unknown", missing, err)
	}
	if _, err := os.Stat(f.stateRoot()); !os.IsNotExist(err) {
		t.Fatal("read-only status initialized state", err)
	}
	if got, err := f.run(f.peer, "private status body", "send-heredoc", "--config", f.config, "--to", "worker"); err != nil || !got.Success {
		t.Fatal(got, err)
	}
	before, err := snapshotRegularFiles(f.stateRoot())
	if err != nil {
		t.Fatal(err)
	}
	status, err := f.run(f.worker, "", "get-status", "--config", f.config)
	if err != nil {
		t.Fatal(status, err)
	}
	worker := findStatusPane(t, status.Sessions, f.worker).StatusValue()
	if worker.UnreadCount == nil || *worker.UnreadCount != 1 || worker.InboundCount == nil || *worker.InboundCount != 1 || worker.LastPopAt != nil || worker.LastCaptureAt == nil {
		t.Fatal(worker)
	}
	after, err := snapshotRegularFiles(f.stateRoot())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("status changed files", err)
	}
	// Corrupt one saved mail record. Its counts become unknown, not zero, while
	// independently readable node rows and observation timestamps remain useful.
	sent := f.sent("peer", "private status body")
	if err := os.WriteFile(filepath.Join(f.box("worker", "inbox"), sent.ID+".md"), []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	refreshPublicStatus(t, f)
	status, err = f.run(f.worker, "", "get-status", "--config", f.config)
	if err != nil {
		t.Fatal(status, err)
	}
	worker = findStatusPane(t, status.Sessions, f.worker).StatusValue()
	if worker.UnreadCount != nil || worker.InboundCount != nil || worker.Reason == "" {
		t.Fatal("corrupt inbox looked empty", worker)
	}
	peer := findStatusPane(t, status.Sessions, f.peer).StatusValue()
	if peer.OutboundCount == nil || *peer.OutboundCount != 1 {
		t.Fatal("damage contaminated another node", peer)
	}
}

func TestWorkspaceCaptureFailureDoesNotUndoMail(t *testing.T) {
	f := newPublicApprovalFixture(t)
	if got, err := f.run(f.peer, "capture failure must not lose mail", "send-heredoc", "--config", f.config, "--to", "worker"); err != nil || !got.Success {
		t.Fatal(got, err)
	}
	entries, err := os.ReadDir(filepath.Join(f.stateRoot(), "%status"))
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
	observations := filepath.Join(f.stateRoot(), "%status", entries[0].Name(), "history")
	if err := os.Rename(observations, observations+".saved"); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, observations); err != nil {
		t.Fatal(err)
	}
	got, err := f.run(f.worker, "", "pop", "--config", f.config)
	if err != nil || !got.Success || got.MarkdownPath == nil || got.Self.LastPopAt == nil {
		t.Fatal("supplemental capture reversed claim", got, err)
	}
	if readProtocolArchive(t, *got.MarkdownPath).Body != "capture failure must not lose mail" {
		t.Fatal("lost body")
	}
	files, err := os.ReadDir(outside)
	if err != nil || len(files) != 1 || files[0].Name() != "sentinel" {
		t.Fatal("capture wrote through symlink", files, err)
	}
}

type fileSnapshot struct {
	mode os.FileMode
	hash [32]byte
}

func snapshotRegularFiles(root string) (map[string]fileSnapshot, error) {
	result := make(map[string]fileSnapshot)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		snapshot := fileSnapshot{mode: info.Mode()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snapshot.hash = sha256.Sum256(data)
		}
		result[rel] = snapshot
		return nil
	})
	return result, err
}

type selectedStatusPane struct{ workspace.StatusPane }

func (p selectedStatusPane) StatusValue() workspace.NodeStatus {
	if p.Status == nil {
		return workspace.NodeStatus{Reason: "saved node unavailable"}
	}
	return *p.Status
}

func findStatusPane(t *testing.T, sessions []workspace.StatusSession, pane string) selectedStatusPane {
	t.Helper()
	var matches []workspace.StatusPane
	for _, s := range sessions {
		for _, w := range s.Windows {
			for _, p := range w.Panes {
				if p.ID == pane {
					matches = append(matches, p)
				}
			}
		}
	}
	if len(matches) != 1 {
		t.Fatalf("native pane %s matched %d saved rows", pane, len(matches))
	}
	return selectedStatusPane{matches[0]}
}

func refreshPublicStatus(t *testing.T, f *publicApprovalFixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := f.child(ctx, "", "refresh-status").Output()
	if err != nil {
		var result workspace.RefreshResult
		if json.Unmarshal(out, &result) != nil || result.Status != "partial" {
			t.Fatalf("explicit refresh: %v %s", err, out)
		}
	}
}
