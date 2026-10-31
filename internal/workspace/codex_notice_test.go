package workspace

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A process-level protocol peer for fault injection; the separate smoke test
// uses the real Codex TUI and app-server binary against a deterministic model.
func TestCodexQueueServerChild(t *testing.T) {
	if os.Getenv("POSTMAN_CODEX_RPC_CHILD") != "1" {
		return
	}
	home := os.Getenv("CODEX_HOME")
	_ = os.WriteFile(filepath.Join(home, "last-pid"), []byte(fmt.Sprint(os.Getpid())), 0o600)
	var meta CodexNotifier
	raw, err := os.ReadFile(filepath.Join(home, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		var req struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scan.Bytes(), &req); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(filepath.Join(home, "rpc.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.Write(append(append([]byte{}, scan.Bytes()...), '\n'))
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if req.Method == "initialized" {
			continue
		}
		mode, _ := os.ReadFile(filepath.Join(home, "mode"))
		result := any(map[string]any{})
		switch req.Method {
		case "initialize":
		case "thread/read":
			id, root := meta.ThreadID, meta.Root
			if string(mode) == "wrong-thread" {
				id = "00000000-0000-0000-0000-000000000000"
			}
			if string(mode) == "wrong-root" {
				root = home
			}
			result = map[string]any{"thread": map[string]string{"id": id, "cwd": root}}
		case "thread/queue/add":
			if string(mode) == "disconnect" {
				os.Exit(0)
			}
			if string(mode) == "stall" {
				time.Sleep(30 * time.Second)
			}
			if string(mode) == "gate" {
				for {
					if _, err := os.Stat(filepath.Join(home, "release")); err == nil {
						break
					}
					time.Sleep(time.Millisecond)
				}
			}
			var params struct {
				Client string `json:"clientUserMessageId"`
			}
			_ = json.Unmarshal(req.Params, &params)
			if string(mode) == "wrong-receipt" {
				params.Client = "different-client"
			}
			result = map[string]any{"queuedSubmission": map[string]string{"id": "queue-" + params.Client, "clientUserMessageId": params.Client}}
		default:
			t.Fatal("unexpected runtime method (must never start/resume a thread/turn)", req.Method)
		}
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"id": req.ID, "result": result}); err != nil {
			t.Fatal(err)
		}
	}
	os.Exit(0)
}

type codexNoticeFixture struct {
	mail         Mail
	source       *storedRuntime
	registration CodexNotifier
	msg          Message
}

func codexNoticeFixtureFor(t *testing.T) codexNoticeFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, "repo")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	codexHome := filepath.Join(home, ".codex")
	if err := os.Mkdir(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(home, "codex")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := "#!/bin/sh\nexport POSTMAN_CODEX_RPC_CHILD=1\nexec " + quote(executable) + " -test.run='^TestCodexQueueServerChild$'\n"
	if err := os.WriteFile(program, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	n := CodexNotifier{ThreadID: "01234567-89ab-4cde-8123-456789abcdef", Home: codexHome, Program: program, Root: root, Topology: filepath.Join(root, "postman.md")}
	raw, _ := json.Marshal(n)
	if err := os.WriteFile(filepath.Join(codexHome, "meta.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	m := mailFixture(t)
	if err := m.Store.RegisterCodexNotifier(context.Background(), mailRuntime("peer"), n); err != nil {
		t.Fatal(err)
	}
	source := mailRuntime("worker")
	msg, err := m.Send(context.Background(), source, "peer", "private complete body that stays in mail", "", false)
	if err != nil {
		t.Fatal(err)
	}
	return codexNoticeFixture{m, source, n, msg}
}

func (f codexNoticeFixture) mode(t *testing.T, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.registration.Home, "mode"), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f codexNoticeFixture) queueCalls(t *testing.T) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.registration.Home, "rpc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	scan := bufio.NewScanner(strings.NewReader(string(raw)))
	for scan.Scan() {
		var v struct {
			Method string `json:"method"`
			Params struct {
				Thread string `json:"threadId"`
				Input  []struct {
					Text string `json:"text"`
				} `json:"input"`
			} `json:"params"`
		}
		if err := json.Unmarshal(scan.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if v.Method == "thread/queue/add" {
			count++
			if v.Params.Thread != f.registration.ThreadID || len(v.Params.Input) != 1 || !strings.Contains(v.Params.Input[0].Text, f.msg.ID) || strings.Contains(v.Params.Input[0].Text, f.msg.Body) {
				t.Fatal("notice lost target/correlation or leaked mail body", v)
			}
		}
	}
	return count
}

func TestCodexNoticeTargetsExistingThreadAndDoesNotReplay(t *testing.T) {
	f := codexNoticeFixtureFor(t)
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	first := f.mail.Store.NotifyCommittedAfterCommit(parent, f.source, f.source, f.msg)
	if first.Status != "accepted" || first.ThreadID != f.registration.ThreadID || first.QueueID == "" || first.Runtime != "codex" {
		t.Fatal(first)
	}
	second := f.mail.Store.NotifyCommittedAfterCommit(context.Background(), f.source, f.source, f.msg)
	if second.Status != "accepted" || second.QueueID != first.QueueID || f.queueCalls(t) != 1 {
		t.Fatal(second)
	}
	if _, err := os.Stat(mailPath(f.mail, "peer", "inbox", f.msg.ID)); err != nil {
		t.Fatal("queue acceptance incorrectly POPed mail", err)
	}
}

func TestCodexNoticeAmbiguityIsNeverReplayed(t *testing.T) {
	for _, mode := range []string{"disconnect", "wrong-receipt", "stall"} {
		t.Run(mode, func(t *testing.T) {
			f := codexNoticeFixtureFor(t)
			f.mode(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			result := f.mail.Store.notifyCommitted(ctx, f.source, f.source, f.msg)
			cancel()
			if result.Status != "unknown" {
				t.Fatal(result)
			}
			pidText, err := os.ReadFile(filepath.Join(f.registration.Home, "last-pid"))
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(string(pidText))
			if err != nil || syscall.Kill(pid, 0) != syscall.ESRCH {
				t.Fatal("short-lived queue helper was not reaped", pid, err)
			}
			f.mode(t, "")
			again := f.mail.Store.NotifyCommittedAfterCommit(context.Background(), f.source, f.source, f.msg)
			if again.Status != "unknown" || f.queueCalls(t) != 1 {
				t.Fatal("ambiguous enqueue was replayed", again)
			}
			if _, err := os.Stat(mailPath(f.mail, "peer", "inbox", f.msg.ID)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCodexConcurrentDuplicateNoticeQueuesOnce(t *testing.T) {
	f := codexNoticeFixtureFor(t)
	f.mode(t, "gate")
	results := make(chan NoticeResult, 2)
	go func() {
		results <- f.mail.Store.NotifyCommittedAfterCommit(context.Background(), mailRuntime("worker"), mailRuntime("worker"), f.msg)
	}()
	deadline := time.Now().Add(time.Second)
	for f.queueCalls(t) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if f.queueCalls(t) != 1 {
		t.Fatal("first attempt did not reach queue")
	}
	go func() {
		results <- f.mail.Store.NotifyCommittedAfterCommit(context.Background(), mailRuntime("worker"), mailRuntime("worker"), f.msg)
	}()
	if got := <-results; got.Status != "unknown" {
		t.Fatal("concurrent call must not replay the pending attempt", got)
	}
	if err := os.WriteFile(filepath.Join(f.registration.Home, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := <-results; got.Status != "accepted" || f.queueCalls(t) != 1 {
		t.Fatal(got)
	}
}

func TestCodexStalePaneRegistrationCannotQueue(t *testing.T) {
	f := codexNoticeFixtureFor(t)
	f.source.snapshot.Bindings[1].PanePID = "different-incarnation"
	if got := f.mail.Store.NotifyCommittedAfterCommit(context.Background(), f.source, f.source, f.msg); got.Status != "unavailable" || f.queueCalls(t) != 0 {
		t.Fatal(got)
	}
}

func TestCodexNoticeRejectsWrongRuntimeIdentityBeforeQueue(t *testing.T) {
	for _, mode := range []string{"wrong-thread", "wrong-root"} {
		t.Run(mode, func(t *testing.T) {
			f := codexNoticeFixtureFor(t)
			f.mode(t, mode)
			result := f.mail.Store.NotifyCommittedAfterCommit(context.Background(), f.source, f.source, f.msg)
			if result.Status != "unavailable" || f.queueCalls(t) != 0 {
				t.Fatal(result)
			}
			f.mode(t, "")
			if result := f.mail.Store.NotifyCommittedAfterCommit(context.Background(), f.source, f.source, f.msg); result.Status != "accepted" {
				t.Fatal(result)
			}
		})
	}
}

func TestCodexNoticeHoldsNoMailboxLockDuringRuntimeCall(t *testing.T) {
	f := codexNoticeFixtureFor(t)
	f.mode(t, "gate")
	result := make(chan NoticeResult, 1)
	go func() {
		result <- f.mail.Store.NotifyCommittedAfterCommit(context.Background(), f.source, f.source, f.msg)
	}()
	deadline := time.Now().Add(time.Second)
	for f.queueCalls(t) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	// A separate command can publish while this RPC is blocked in the receiver.
	_, sendErr := f.mail.Send(context.Background(), mailRuntime("worker"), "peer", "concurrent message", "", false)
	if err := os.WriteFile(filepath.Join(f.registration.Home, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got := <-result
	if sendErr != nil || got.Status != "accepted" {
		t.Fatal(sendErr, got)
	}
}

func TestCodexRegistrationRejectsInvalidThreadAndUnsafeHome(t *testing.T) {
	f := codexNoticeFixtureFor(t)
	n := f.registration
	n.ThreadID = "friendly-session-name"
	if err := f.mail.Store.RegisterCodexNotifier(context.Background(), mailRuntime("peer"), n); err == nil {
		t.Fatal("accepted ambiguous session name")
	}
	n = f.registration
	if err := os.Chmod(n.Home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := n.validate(); err == nil {
		t.Fatal("accepted shared Codex home")
	}
	if err := os.Chmod(n.Home, 0o700); err != nil {
		t.Fatal(err)
	}
}
