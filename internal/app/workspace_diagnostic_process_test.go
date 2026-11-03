package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/diagnostic"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

func invocationFiles(t *testing.T, f *publicApprovalFixture, role string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(f.box(role, "logs"), "*", "inv-*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func invocationEvents(t *testing.T, path string) []diagnostic.Event {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var events []diagnostic.Event
	for _, line := range bytes.Split(bytes.TrimSpace(b), []byte{'\n'}) {
		var e diagnostic.Event
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatal(err)
		}
		if e.Seq != len(events) {
			t.Fatal(e)
		}
		events = append(events, e)
	}
	if len(events) < 2 || events[0].Event != "start" || events[len(events)-1].Event != "end" {
		t.Fatal("incomplete normal invocation", path)
	}
	return events
}

func TestInvocationDiagnosticsRealSendPopEmptyAndReadOnlyCommands(t *testing.T) {
	f := newPublicApprovalFixture(t)
	f.writeConfig(false)
	secret := "PRIVATE_BODY_TOKEN_964"
	if got, err := f.run(f.worker, secret, "send-heredoc", "--config", f.config, "--to", "peer"); err != nil || !got.Success {
		t.Fatal(got, err)
	}
	original := f.sent("worker", secret)
	for range 2 {
		if got, err := f.run(f.peer, "", "pop", "--config", f.config); err != nil || !got.Success {
			t.Fatal(got, err)
		}
	}
	own := invocationFiles(t, f, "worker")
	received := invocationFiles(t, f, "peer")
	if len(own) != 1 || len(received) != 2 {
		t.Fatal("normal/empty invocation missing", own, received)
	}
	sent := invocationEvents(t, own[0])
	last := sent[len(sent)-1]
	if last.Facts.Mail != "committed" || last.Facts.Notification == "not_attempted" || last.Facts.Capture != "updated" {
		t.Fatal(last)
	}
	foundRefresh := false
	foundID := false
	for _, e := range sent {
		if e.Phase == "server_refresh" && e.Event == "phase_end" && e.Outcome == "updated" {
			foundRefresh = true
		}
		if e.Facts != nil && e.Facts.MessageID == original.ID {
			foundID = true
		}
	}
	if !foundRefresh || !foundID || sent[0].Identity.Node != "worker" || sent[0].Identity.ConfigFingerprint == "" {
		t.Fatal("missing correlation/context")
	}
	results := map[string]int{}
	for _, path := range received {
		e := invocationEvents(t, path)
		results[e[len(e)-1].Facts.Mail]++
	}
	if results["claimed"] != 1 || results["empty"] != 1 {
		t.Fatal(results)
	}
	for _, path := range append(own, received...) {
		b, _ := os.ReadFile(path)
		if bytes.Contains(b, []byte(secret)) {
			t.Fatal("body leaked")
		}
	}
	before, err := snapshotRegularFiles(f.stateRoot())
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"get-status", "--config", f.config}, {"get-status-oneline", "--config", f.config}, {"help"}, {"version"}, {}} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := f.child(ctx, f.worker, args...)
		_ = cmd.Run()
		cancel()
	}
	after, err := snapshotRegularFiles(f.stateRoot())
	if err != nil || len(before) != len(after) {
		t.Fatal("read-only command created logs", err)
	}
	for path, was := range before {
		if after[path] != was {
			t.Fatal("read-only command modified file", path)
		}
	}
}

func TestInvocationDiagnosticFailureDoesNotUndoClaimOrChangeChildExit(t *testing.T) {
	f := newPublicApprovalFixture(t)
	f.writeConfig(false)
	if got, err := f.run(f.peer, "claim exactly once", "send-heredoc", "--config", f.config, "--to", "worker"); err != nil || !got.Success {
		t.Fatal(got, err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, f.box("worker", "logs")); err != nil {
		t.Fatal(err)
	}
	got, err := f.run(f.worker, "", "pop", "--config", f.config)
	if err != nil || !got.Success || got.MarkdownPath == nil {
		t.Fatal("log failure reversed POP", got, err)
	}
	empty, err := f.run(f.worker, "", "pop", "--config", f.config)
	if err != nil || empty.MarkdownPath != nil {
		t.Fatal("claim replayed", empty, err)
	}
	executed, err := f.run(f.worker, "printf 'stdout-secret'; printf 'stderr-secret' >&2; exit 7\n", "execute-heredoc", "--config", f.config, "--reason", "reason-secret")
	assertExit(t, err, 7)
	if executed.Executed == nil || !*executed.Executed || executed.ExitCode == nil || *executed.ExitCode != 7 {
		t.Fatal(executed)
	}
	files, err := os.ReadDir(outside)
	if err != nil || len(files) != 0 {
		t.Fatal("logs wrote through symlink", files, err)
	}
}

func TestInvocationDiagnosticParallelProcessesUseSeparateFiles(t *testing.T) {
	f := newPublicApprovalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// Each OS process owns a file, even when both operate as the same actor.
	commands := []*struct{ done chan error }{}
	for range 4 {
		cmd := f.child(ctx, f.worker, "pop", "--config", f.config)
		cmd.Stdin = strings.NewReader("")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		entry := &struct{ done chan error }{make(chan error, 1)}
		commands = append(commands, entry)
		go func() { entry.done <- cmd.Wait() }()
	}
	for _, cmd := range commands {
		if err := <-cmd.done; err != nil {
			t.Fatal(err)
		}
	}
	files := invocationFiles(t, f, "worker")
	if len(files) != 4 {
		t.Fatal("parallel log files missing", files)
	}
	ids := map[string]bool{}
	for _, path := range files {
		e := invocationEvents(t, path)
		id := e[0].InvocationID
		if ids[id] {
			t.Fatal("ID reused")
		}
		ids[id] = true
	}
}

func TestInvocationDiagnosticCrashDuringApprovalLeavesPartialEvidence(t *testing.T) {
	f := newPublicApprovalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := f.execution(ctx, "touch must-not-execute\n")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if !stopped {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	// The approval-wait phase starts after the durable request's bounded
	// notification. Its eight-second budget must fit inside this observation;
	// five seconds from process startup could kill a healthy macOS invocation.
	f.waitRequest()
	var path string
	for deadline := time.Now().Add(workspace.PostCommitNoticeBudget + 5*time.Second); time.Now().Before(deadline); {
		files := invocationFiles(t, f, "worker")
		if len(files) == 1 {
			b, _ := os.ReadFile(files[0])
			if bytes.Contains(b, []byte(`"phase":"approval_wait"`)) {
				path = files[0]
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if path == "" {
		t.Fatal("approval wait start was not flushed")
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	stopped = true
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"event":"end"`)) {
		t.Fatal("crash invented a completed invocation")
	}
	request, found := f.requestExcept(map[string]bool{})
	if !found {
		t.Fatal("missing durable approval request")
	}
	var body struct {
		OperationID string `json:"operation_id"`
	}
	if err := json.Unmarshal([]byte(request.Body), &body); err != nil || body.OperationID == "" {
		t.Fatal("missing exact execution operation", err)
	}
	correlated := false
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		var event diagnostic.Event
		if json.Unmarshal(line, &event) != nil {
			t.Fatal("crash left invalid complete diagnostic line")
		}
		if event.Facts != nil && event.Facts.OperationID == body.OperationID && event.Facts.RequestID == request.ID {
			correlated = true
		}
	}
	if !correlated {
		t.Fatal("approval interruption lost its operation/request correlation")
	}

	if _, err := os.Stat(filepath.Join(f.home, "work", "must-not-execute")); !os.IsNotExist(err) {
		t.Fatal("unapproved command executed", err)
	}
}
