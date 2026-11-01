package main

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWorkspaceStatusProcessIsReadOnlyAndReportsSavedFacts(t *testing.T) {
	f := newPublicApprovalFixture(t)
	missing, err := f.run(f.worker, "", "get-status", "--config", f.config)
	if err == nil || len(missing.Nodes) != 2 || missing.Nodes[0].UnreadCount != nil {
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
	worker := findProtocolNode(t, status.Nodes, "worker")
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
	status, err = f.run(f.worker, "", "get-status", "--config", f.config)
	if err != nil {
		t.Fatal(status, err)
	}
	worker = findProtocolNode(t, status.Nodes, "worker")
	if worker.UnreadCount != nil || worker.InboundCount != nil || worker.Reason == "" {
		t.Fatal("corrupt inbox looked empty", worker)
	}
	peer := findProtocolNode(t, status.Nodes, "peer")
	if peer.OutboundCount == nil || *peer.OutboundCount != 1 {
		t.Fatal("damage contaminated another node", peer)
	}
}

func TestWorkspaceCaptureFailureDoesNotUndoMail(t *testing.T) {
	f := newPublicApprovalFixture(t)
	if got, err := f.run(f.peer, "capture failure must not lose mail", "send-heredoc", "--config", f.config, "--to", "worker"); err != nil || !got.Success {
		t.Fatal(got, err)
	}
	observations := filepath.Join(f.stateRoot(), "work", "runtime", "observations")
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
	if err != nil || !got.Success || got.MarkdownPath == nil || got.Self.LastCaptureAt != nil || got.Self.LastPopAt == nil {
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
