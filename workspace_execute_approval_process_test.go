package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

type publicApprovalFixture struct {
	t         *testing.T
	socket    string
	home      string
	stateHome string
	config    string
	worker    string
	peer      string
	self      string
}

type publicApprovalOutput struct {
	Status    string                     `json:"status"`
	Message   *workspace.Message         `json:"message"`
	Execution *workspace.ExecutionResult `json:"execution"`
	Reason    string                     `json:"reason"`
}

func newPublicApprovalFixture(t *testing.T) *publicApprovalFixture {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "pm937-approval-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &publicApprovalFixture{t: t, socket: filepath.Join(dir, "s"), home: t.TempDir(), stateHome: t.TempDir()}
	f.self, err = os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(f.home, "work")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	f.worker = f.tmux("-f", "/dev/null", "new-session", "-d", "-s", "work", "-c", root, "-P", "-F", "#{pane_id}", "exec sleep 120")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "tmux", "-S", f.socket, "kill-server").Run()
	})
	f.tmux("select-pane", "-t", f.worker, "-T", "worker")
	f.peer = f.tmux("split-window", "-d", "-t", f.worker, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.tmux("select-pane", "-t", f.peer, "-T", "peer")
	f.config = filepath.Join(t.TempDir(), "postman.md")
	if err := os.WriteFile(f.config, []byte("## `edges`\n```mermaid\ngraph LR\nworker --- peer:::command_approver_node\n```\n## `worker`\nWorker\n## `peer`\nPeer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *publicApprovalFixture) tmux(args ...string) string {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", append([]string{"-S", f.socket}, args...)...).CombinedOutput()
	if err != nil {
		f.t.Fatalf("owned tmux %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *publicApprovalFixture) child(ctx context.Context, pane string, args ...string) *exec.Cmd {
	f.t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		f.t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, f.self, "-test.run=^TestIssue937PublicCLIChild$")
	cmd.Env = append(os.Environ(), "HOME="+f.home, "XDG_CONFIG_HOME="+filepath.Join(f.home, ".config"), "XDG_STATE_HOME="+f.stateHome,
		"TMUX="+f.socket+",0,0", "TMUX_PANE="+pane, "POSTMAN_TEST_PUBLIC_ARGS="+string(raw))
	return cmd
}

func (f *publicApprovalFixture) run(pane, body string, args ...string) (publicApprovalOutput, error) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := f.child(ctx, pane, args...)
	cmd.Stdin = strings.NewReader(body)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		f.t.Fatalf("public CLI %v timed out: stdout=%q stderr=%q", args, stdout.String(), stderr.String())
	}
	var result publicApprovalOutput
	if decodeErr := json.Unmarshal(stdout.Bytes(), &result); decodeErr != nil {
		f.t.Fatalf("public CLI %v: err=%v stdout=%q stderr=%q decode=%v", args, err, stdout.String(), stderr.String(), decodeErr)
	}
	return result, err
}

func (f *publicApprovalFixture) runWhenAvailable(pane, body string, args ...string) (publicApprovalOutput, error) {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		result, err := f.run(pane, body, args...)
		if result.Status != "unavailable" || result.Reason != "workspace mutation is busy" || result.Message != nil || time.Now().After(deadline) {
			return result, err
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (f *publicApprovalFixture) waitRequest() workspace.Message {
	return f.waitRequestExcept(nil)
}

func (f *publicApprovalFixture) waitRequestExcept(excluded map[string]bool) workspace.Message {
	f.t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if request, ok := f.requestExcept(excluded); ok {
			return request
		}
		time.Sleep(25 * time.Millisecond)
	}
	f.t.Fatal("public execute-bash did not deliver a reviewer request")
	return workspace.Message{}
}

func (f *publicApprovalFixture) requestExcept(excluded map[string]bool) (workspace.Message, bool) {
	f.t.Helper()
	root, err := workspace.DefaultStateRoot(f.home, f.stateHome)
	if err != nil {
		f.t.Fatal(err)
	}
	inbox := filepath.Join(root, "work", "agents", "peer", "inbox")
	entries, err := os.ReadDir(inbox)
	if errors.Is(err, os.ErrNotExist) {
		return workspace.Message{}, false
	}
	if err != nil {
		f.t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(inbox, entry.Name()))
		if err != nil {
			f.t.Fatal(err)
		}
		prefix := []byte("<!-- postman ")
		separator := []byte(" -->\n\n")
		split := bytes.Index(data, separator)
		if !bytes.HasPrefix(data, prefix) || split < len(prefix) {
			f.t.Fatalf("bad request envelope: %s", entry.Name())
		}
		var request workspace.Message
		if err := json.Unmarshal(data[len(prefix):split], &request); err != nil {
			f.t.Fatal(err)
		}
		if request.From != "worker" || request.To != "peer" || !request.NeedsReply || request.InputRequestID == "" {
			f.t.Fatalf("bad approval request: %+v", request)
		}
		if excluded[request.ID] {
			continue
		}
		return request, true
	}
	return workspace.Message{}, false
}

func (f *publicApprovalFixture) reply(request workspace.Message, command, decision string) workspace.Message {
	f.t.Helper()
	body, err := json.Marshal(workspace.ApprovalDecision{Version: 1, Command: command, Decision: decision})
	if err != nil {
		f.t.Fatal(err)
	}
	result, err := f.runWhenAvailable(f.peer, string(body), "send-heredoc", "--postman-md", f.config,
		"--to", "worker", "--reply-to", request.ID, "--fills-input-request-id", request.InputRequestID, "--no-reply")
	if err != nil || result.Status != "sent" || result.Message == nil {
		f.t.Fatalf("public reviewer reply: %+v %v", result, err)
	}
	return *result.Message
}

func TestWorkspaceExecuteBashPublicApprovalReplies(t *testing.T) {
	for _, scenario := range []string{"approved", "rejected", "wrong_then_approved"} {
		t.Run(scenario, func(t *testing.T) {
			f := newPublicApprovalFixture(t)
			marker := filepath.Join(f.home, "executed")
			command := "printf x >> " + marker
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			child := f.child(ctx, f.worker, "execute-bash", "--postman-md", f.config, "--command", command)
			var stdout, stderr bytes.Buffer
			child.Stdout, child.Stderr = &stdout, &stderr
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			request := f.waitRequest()
			claimed, err := f.runWhenAvailable(f.peer, "", "pop", "--postman-md", f.config)
			if err != nil || claimed.Message == nil || claimed.Message.ID != request.ID {
				t.Fatalf("public reviewer POP: %+v %v", claimed, err)
			}
			var wrong, unrelated workspace.Message
			if scenario == "wrong_then_approved" {
				wrong = f.reply(request, "different command", "approved")
				ordinary, err := f.runWhenAvailable(f.peer, "ordinary mail", "send-heredoc", "--postman-md", f.config,
					"--to", "worker", "--no-reply")
				if err != nil || ordinary.Status != "sent" || ordinary.Message == nil {
					t.Fatalf("unrelated public mail: %+v %v", ordinary, err)
				}
				unrelated = *ordinary.Message
			}
			decision := scenario
			if scenario == "wrong_then_approved" {
				decision = "approved"
			}
			good := f.reply(request, command, decision)
			err = child.Wait()
			if ctx.Err() != nil {
				t.Fatalf("public execution timed out: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			var result publicApprovalOutput
			if decodeErr := json.NewDecoder(bytes.NewReader(stderr.Bytes())).Decode(&result); decodeErr != nil {
				t.Fatalf("public execution JSON: err=%v stderr=%q decode=%v", err, stderr.String(), decodeErr)
			}
			if result.Execution == nil || result.Execution.RequestID != request.ID || result.Execution.ReplyID != good.ID {
				t.Fatalf("public exact reply correlation: %+v err=%v", result, err)
			}
			if scenario == "rejected" {
				if err == nil || result.Status != "rejected" {
					t.Fatalf("public rejection: %+v %v", result, err)
				}
				if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("rejected command executed: %v", err)
				}
			} else {
				if err != nil || result.Status != "executed" || stdout.Len() != 0 {
					t.Fatalf("public approval: %+v err=%v stdout=%q", result, err, stdout.String())
				}
				if data, err := os.ReadFile(marker); err != nil || string(data) != "x" {
					t.Fatalf("approved command did not execute once: %q %v", data, err)
				}
			}
			if scenario == "wrong_then_approved" {
				root, err := workspace.DefaultStateRoot(f.home, f.stateHome)
				if err != nil {
					t.Fatal(err)
				}
				for _, id := range []string{wrong.ID, unrelated.ID} {
					path := filepath.Join(root, "work", "agents", "worker", "inbox", id+".md")
					if _, err := os.Stat(path); err != nil {
						t.Fatalf("public execute-bash consumed unrelated reply %s: %v", id, err)
					}
				}
			}
		})
	}
}

func TestWorkspaceExecuteBashPublicReviewerDisappears(t *testing.T) {
	f := newPublicApprovalFixture(t)
	marker := filepath.Join(f.home, "executed")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := f.child(ctx, f.worker, "execute-bash", "--postman-md", f.config, "--command", "printf never > "+marker)
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	request := f.waitRequest()
	f.tmux("kill-pane", "-t", f.peer)
	err := child.Wait()
	if ctx.Err() != nil {
		t.Fatalf("reviewer disappearance timed out: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	var result publicApprovalOutput
	if decodeErr := json.NewDecoder(bytes.NewReader(stderr.Bytes())).Decode(&result); decodeErr != nil || err == nil || result.Status != "unavailable" ||
		result.Execution == nil || result.Execution.RequestID != request.ID || result.Execution.ReplyID != "" {
		t.Fatalf("reviewer disappearance: %+v err=%v decode=%v stderr=%q", result, err, decodeErr, stderr.String())
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("command executed after reviewer disappeared: %v", err)
	}
}

func TestWorkspaceExecuteBashPublicLateReplyAfterInterrupt(t *testing.T) {
	f := newPublicApprovalFixture(t)
	marker := filepath.Join(f.home, "executed")
	command := "printf x >> " + marker
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	first := f.child(ctx, f.worker, "execute-bash", "--postman-md", f.config, "--command", command)
	var firstErr bytes.Buffer
	first.Stderr = &firstErr
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	old := f.waitRequest()
	claimed, err := f.runWhenAvailable(f.peer, "", "pop", "--postman-md", f.config)
	if err != nil || claimed.Message == nil || claimed.Message.ID != old.ID {
		t.Fatalf("first public reviewer POP: %+v %v", claimed, err)
	}
	if err := first.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err = first.Wait()
	var cancelled publicApprovalOutput
	if decodeErr := json.NewDecoder(&firstErr).Decode(&cancelled); decodeErr != nil || err == nil ||
		(cancelled.Status != "cancelled" && cancelled.Status != "unavailable") ||
		cancelled.Execution == nil || cancelled.Execution.RequestID != old.ID || cancelled.Execution.ReplyID != "" {
		t.Fatalf("first public interruption: %+v err=%v decode=%v stderr=%q", cancelled, err, decodeErr, firstErr.String())
	}
	late := f.reply(old, command, "approved")

	second := f.child(ctx, f.worker, "execute-bash", "--postman-md", f.config, "--command", command)
	var secondErr bytes.Buffer
	second.Stderr = &secondErr
	if err := second.Start(); err != nil {
		t.Fatal(err)
	}
	fresh := f.waitRequestExcept(map[string]bool{old.ID: true})
	if fresh.InputRequestID == old.InputRequestID {
		t.Fatal("fresh request reused cancelled input ID")
	}
	claimed, err = f.runWhenAvailable(f.peer, "", "pop", "--postman-md", f.config)
	if err != nil || claimed.Message == nil || claimed.Message.ID != fresh.ID {
		t.Fatalf("fresh public reviewer POP: %+v %v", claimed, err)
	}
	rejected := f.reply(fresh, command, "rejected")
	err = second.Wait()
	var result publicApprovalOutput
	if decodeErr := json.NewDecoder(&secondErr).Decode(&result); decodeErr != nil || err == nil ||
		result.Status != "rejected" || result.Execution == nil ||
		result.Execution.RequestID != fresh.ID || result.Execution.ReplyID != rejected.ID {
		t.Fatalf("fresh public rejection: %+v err=%v decode=%v stderr=%q", result, err, decodeErr, secondErr.String())
	}
	root, err := workspace.DefaultStateRoot(f.home, f.stateHome)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "work", "agents", "worker", "inbox", late.ID+".md")); err != nil {
		t.Fatalf("fresh call consumed late reply: %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("late reply authorized execution: %v", err)
	}
}

func TestWorkspaceExecuteBashPublicConcurrentIndependentRequests(t *testing.T) {
	f := newPublicApprovalFixture(t)
	marker := filepath.Join(f.home, "executed")
	command := "printf x >> " + marker
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := func() (*exec.Cmd, *bytes.Buffer) {
		t.Helper()
		child := f.child(ctx, f.worker, "execute-bash", "--postman-md", f.config, "--command", command)
		stderr := new(bytes.Buffer)
		child.Stderr = stderr
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		return child, stderr
	}
	first, firstErr := start()
	request1 := f.waitRequest()
	var second *exec.Cmd
	var secondErr *bytes.Buffer
	var secondDone chan error
	var request2 workspace.Message
	deadline := time.Now().Add(6 * time.Second)
	for request2.ID == "" {
		if time.Now().After(deadline) {
			t.Fatal("second public request stayed busy")
		}
		second, secondErr = start()
		secondDone = make(chan error, 1)
		go func() { secondDone <- second.Wait() }()
		for request2.ID == "" {
			if request, ok := f.requestExcept(map[string]bool{request1.ID: true}); ok {
				request2 = request
				break
			}
			select {
			case err := <-secondDone:
				var result publicApprovalOutput
				decodeErr := json.NewDecoder(secondErr).Decode(&result)
				// Recording the failed delivery can encounter the same busy lock.
				busyReason := result.Reason == "workspace mutation is busy" ||
					result.Reason == "workspace mutation is busy audit terminal unavailable: workspace mutation is busy"
				if err == nil || decodeErr != nil || result.Status != "delivery_failed" ||
					!busyReason || result.Execution == nil ||
					result.Execution.RequestID != "" || result.Message != nil {
					t.Fatalf("second public request failed before delivery: %+v err=%v decode=%v", result, err, decodeErr)
				}
				goto retrySecond
			default:
				if time.Now().After(deadline) {
					t.Fatal("second public request did not arrive")
				}
				time.Sleep(25 * time.Millisecond)
			}
		}
	retrySecond:
	}
	if request1.InputRequestID == request2.InputRequestID {
		t.Fatal("concurrent requests reused input ID")
	}
	claimed := map[string]bool{}
	for range 2 {
		result, err := f.runWhenAvailable(f.peer, "", "pop", "--postman-md", f.config)
		if err != nil || result.Message == nil {
			t.Fatalf("concurrent public reviewer POP: %+v %v", result, err)
		}
		claimed[result.Message.ID] = true
	}
	if !claimed[request1.ID] || !claimed[request2.ID] || len(claimed) != 2 {
		t.Fatalf("concurrent reviewer claims: %+v", claimed)
	}
	approved := f.reply(request2, command, "approved")
	rejected := f.reply(request1, command, "rejected")
	for _, check := range []struct {
		wait    func() error
		stderr  *bytes.Buffer
		request workspace.Message
		reply   workspace.Message
		status  string
	}{{
		first.Wait, firstErr, request1, rejected, "rejected",
	}, {
		func() error { return <-secondDone }, secondErr, request2, approved, "executed",
	}} {
		err := check.wait()
		var result publicApprovalOutput
		decodeErr := json.NewDecoder(check.stderr).Decode(&result)
		if decodeErr != nil || result.Status != check.status || result.Execution == nil ||
			result.Execution.RequestID != check.request.ID || result.Execution.ReplyID != check.reply.ID ||
			(check.status == "executed" && err != nil) || (check.status == "rejected" && err == nil) {
			t.Fatalf("concurrent %s: %+v err=%v decode=%v stderr=%q", check.status, result, err, decodeErr, check.stderr.String())
		}
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "x" {
		t.Fatalf("concurrent approvals executed wrong count: %q %v", data, err)
	}
}
