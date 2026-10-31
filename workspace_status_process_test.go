package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

// Status reads saved mail without starting a daemon, registering a workspace,
// recovering pending writes, or trusting a caller whose native pane changed.
func TestWorkspaceStatusProcessIsReadOnlyAndReportsUnknownActivity(t *testing.T) {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "pm937-status-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	home, stateHome := filepath.Join(dir, "home"), filepath.Join(dir, "state")
	work := filepath.Join(home, "work")
	for _, path := range []string{work, stateHome} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runTmux := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, tmuxPath, append([]string{"-S", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("owned tmux %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	worker := runTmux("-f", "/dev/null", "new-session", "-d", "-s", "work", "-c", work, "-P", "-F", "#{pane_id}", "exec sleep 120")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, tmuxPath, "-S", socket, "kill-server").Run()
	})
	runTmux("select-pane", "-t", worker, "-T", "worker")
	peer := runTmux("split-window", "-d", "-t", worker, "-P", "-F", "#{pane_id}", "exec sleep 120")
	runTmux("select-pane", "-t", peer, "-T", "peer")
	config := filepath.Join(dir, "postman.md")
	if err := os.WriteFile(config, []byte("## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `worker`\nWorker\n## `peer`\nPeer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const bodyToken = "private-status-payload-937-9"
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	type output struct {
		Status  string                   `json:"status"`
		Message *workspace.Message       `json:"message"`
		Self    workspace.SelfState      `json:"self"`
		Capture *workspace.CaptureResult `json:"capture"`
	}
	invoke := func(pane, body string, args ...string) (output, string, error) {
		t.Helper()
		encoded, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		child := exec.CommandContext(ctx, self, "-test.run=^TestIssue937PublicCLIChild$")
		child.Env = append(os.Environ(), "HOME="+home, "XDG_STATE_HOME="+stateHome,
			"TMUX="+socket+",0,0", "TMUX_PANE="+pane, "POSTMAN_TEST_PUBLIC_ARGS="+string(encoded))
		child.Stdin = strings.NewReader(body)
		var stdout, stderr bytes.Buffer
		child.Stdout, child.Stderr = &stdout, &stderr
		runErr := child.Run()
		if ctx.Err() != nil {
			t.Fatalf("public CLI %v timed out: %v, stderr: %s", args, ctx.Err(), stderr.String())
		}
		if len(args) > 0 && args[0] == "get-status" &&
			(strings.Contains(stdout.String(), bodyToken) || strings.Contains(stderr.String(), bodyToken)) {
			t.Fatalf("public status disclosed private mail body: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
		var result output
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatalf("public CLI JSON %v: %v, stdout: %q, stderr: %q", args, err, stdout.String(), stderr.String())
		}
		return result, stdout.String(), runErr
	}
	root, err := workspace.DefaultStateRoot(home, stateHome)
	if err != nil {
		t.Fatal(err)
	}
	missing, _, err := invoke(worker, "", "get-status", "--postman-md", config)
	if err == nil || missing.Status != "unavailable" || missing.Self.Known || missing.Self.Unread != nil ||
		missing.Self.Activity.Status != "unknown" || missing.Self.Activity.Reason == "" {
		t.Fatalf("missing state reported as known: %+v, %v", missing, err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("status created missing state root: %v", err)
	}

	body := "status must not return this mail body: " + bodyToken + "\n"
	sent, _, err := invoke(peer, body, "send-heredoc", "--postman-md", config, "--to", "worker", "--no-reply")
	if err != nil || sent.Status != "sent" || sent.Message == nil {
		t.Fatalf("fixture send: %+v, %v", sent, err)
	}
	before, err := snapshotRegularFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	known, raw, err := invoke(worker, "", "get-status", "--postman-md", config)
	if err != nil || known.Status != "ok" || !known.Self.Known || known.Self.Unread == nil || *known.Self.Unread != 1 ||
		known.Self.NextAction != "pop" || known.Self.Activity.Status != "unknown" ||
		known.Self.Activity.Reason != "no saved pane observation" {
		t.Fatalf("saved-file status: %+v, err=%v, raw=%s", known, err, raw)
	}
	after, err := snapshotRegularFiles(root)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("status mutated saved files: before=%v after=%v err=%v", before, after, err)
	}

	// Only owned disposable tmux panes exist; there is no Postman daemon in
	// this fixture. A public send writes a bounded saved capture receipt even
	// when native pane capture is unsupported.
	captured, _, err := invoke(worker, "private pane capture\n", "send-heredoc", "--postman-md", config,
		"--to", "peer", "--no-reply", "--capture-related-panes")
	if err != nil || captured.Status != "sent" || captured.Capture == nil || captured.Message == nil ||
		!regexp.MustCompile(`^m-[0-9a-f]{32}$`).MatchString(captured.Message.ID) ||
		captured.Capture.MessageID != captured.Message.ID || captured.Message.From != "worker" || captured.Message.To != "peer" {
		t.Fatalf("public capture fixture: %+v, %v", captured, err)
	}
	workspaceName, err := workspace.Name("~/work")
	if err != nil {
		t.Fatal(err)
	}
	expectedCapture := filepath.Join(root, workspaceName, "agents", "worker", "captures", captured.Message.ID+".json")
	if captured.Capture.Path != expectedCapture {
		t.Fatalf("capture path escaped owned fixture: got %q, want %q", captured.Capture.Path, expectedCapture)
	}
	entry, err := os.Lstat(expectedCapture)
	if err != nil || !entry.Mode().IsRegular() {
		t.Fatalf("capture is not an owned regular file: %v, %v", entry, err)
	}
	captureFile, err := os.OpenFile(expectedCapture, os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func(closeFn func() error) { _ = closeFn() }(captureFile.Close)
	opened, err := captureFile.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(entry, opened) {
		t.Fatalf("capture identity changed before access: %v, %v", opened, err)
	}
	writeCapture := func(data []byte) {
		t.Helper()
		if _, err := captureFile.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		if err := captureFile.Truncate(0); err != nil {
			t.Fatal(err)
		}
		if n, err := captureFile.Write(data); err != nil || n != len(data) {
			t.Fatalf("write owned capture: %d/%d, %v", n, len(data), err)
		}
	}
	checkActivity := func(reason string, minAge int64) {
		t.Helper()
		before, err := snapshotRegularFiles(root)
		if err != nil {
			t.Fatal(err)
		}
		status, raw, err := invoke(worker, "", "get-status", "--postman-md", config)
		if err != nil || status.Status != "ok" || !status.Self.Known ||
			status.Self.Activity.Status != "unknown" || status.Self.Activity.Reason != reason ||
			status.Self.Activity.ObservedAt == "" || status.Self.Activity.AgeSeconds == nil ||
			*status.Self.Activity.AgeSeconds < minAge || strings.Contains(raw, "private pane capture") {
			t.Fatalf("saved activity: %+v err=%v raw=%s", status, err, raw)
		}
		after, err := snapshotRegularFiles(root)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("status mutated activity evidence: before=%v after=%v err=%v", before, after, err)
		}
	}
	checkActivity("saved pane observation conflicts with current pane", 0)
	if _, err := captureFile.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(captureFile)
	if err != nil {
		t.Fatal(err)
	}
	var saved workspace.CaptureResult
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Observations) != 0 {
		t.Fatalf("failed native capture retained partial observations: %#v", saved)
	}
	// Add an explicit saved fixture observation to exercise status age and
	// incarnation checks; native capture itself remains fail-closed.
	saved.Observations = []workspace.PaneObservation{{
		Role: "worker", Pane: worker, Incarnation: saved.Incarnation,
		CapturedAt: saved.CapturedAt, Reason: "fixture native capture unavailable",
	}}
	changed, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	writeCapture(changed)
	checkActivity("saved pane observation unavailable", 0)
	fresh := saved.CapturedAt
	saved.CapturedAt = time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339Nano)
	for i := range saved.Observations {
		if saved.Observations[i].Role == "worker" {
			saved.Observations[i].CapturedAt = saved.CapturedAt
		}
	}
	changed, err = json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	writeCapture(changed)
	checkActivity("saved pane observation is stale", 600)
	saved.CapturedAt = fresh
	for i := range saved.Observations {
		if saved.Observations[i].Role == "worker" {
			saved.Observations[i].CapturedAt = fresh
		}
	}
	saved.Incarnation = "conflicting-incarnation"
	changed, err = json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	writeCapture(changed)
	checkActivity("saved pane observation conflicts with current incarnation", 0)
	before, err = snapshotRegularFiles(root)
	if err != nil {
		t.Fatal(err)
	}

	runTmux("select-pane", "-t", worker, "-T", "other")
	conflict, _, err := invoke(worker, "", "get-status", "--postman-md", config)
	if err == nil || conflict.Status != "unavailable" || conflict.Self.Known || conflict.Self.Unread != nil ||
		conflict.Self.Activity.Status != "unknown" {
		t.Fatalf("retitled caller reported as working: %+v, %v", conflict, err)
	}
	runTmux("select-pane", "-t", worker, "-T", "worker")
	runTmux("kill-pane", "-t", worker)
	stale, _, err := invoke(worker, "", "get-status", "--postman-md", config)
	if err == nil || stale.Status != "unavailable" || stale.Self.Known || stale.Self.Unread != nil ||
		stale.Self.Activity.Status != "unknown" {
		t.Fatalf("missing caller pane reported as working: %+v, %v", stale, err)
	}
	final, err := snapshotRegularFiles(root)
	if err != nil || !reflect.DeepEqual(before, final) {
		t.Fatalf("unavailable status mutated saved files: before=%v after=%v err=%v", before, final, err)
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
