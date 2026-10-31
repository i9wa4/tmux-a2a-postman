package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

// This is the full no-daemon round trip using only an owned tmux server,
// disposable state, and a freshly rebuilt public CLI process per step.
func TestWorkspaceRebuiltCLINoDaemonEndToEnd(t *testing.T) {
	f := newRebuiltCLIFixture(t)
	const requestBody = "private-request-937-17-6f8c2a"
	const replyBody = "private-reply-937-17-d39b4e"
	assertPrivate := func(step string, result rebuiltCLIResult) {
		t.Helper()
		for _, output := range [][]byte{result.stdout, result.stderr} {
			if bytes.Contains(output, []byte(requestBody)) || bytes.Contains(output, []byte(replyBody)) {
				t.Fatalf("%s status disclosed private message body: stdout=%q stderr=%q", step, result.stdout, result.stderr)
			}
		}
	}
	initial := f.run(f.worker, "", "get-status", "--postman-md", f.config)
	missing := f.jsonResult(t, initial)
	if initial.exit == 0 || missing.Status != "unavailable" {
		t.Fatalf("missing workspace: exit=%d result=%+v stderr=%q", initial.exit, missing, initial.stderr)
	}
	assertPrivate("initial", initial)
	if _, err := os.Stat(f.stateRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only initial status created workspace state: %v", err)
	}

	request := f.run(f.peer, requestBody, "send-heredoc", "--postman-md", f.config,
		"--to", "worker", "--reply-required")
	sent := f.jsonResult(t, request)
	if request.exit != 0 || sent.Status != "sent" || sent.Message == nil || sent.Message.ID == "" ||
		sent.Message.From != "peer" || sent.Message.To != "worker" || !sent.Message.NeedsReply || sent.Message.InputRequestID == "" {
		t.Fatalf("request send: exit=%d result=%+v stderr=%q", request.exit, sent, request.stderr)
	}
	workerInbox := filepath.Join(f.stateRoot, "work", "agents", "worker", "inbox", sent.Message.ID+".md")
	if raw, err := os.ReadFile(workerInbox); err != nil || !bytes.Contains(raw, []byte(requestBody)) {
		t.Fatalf("request not durable in worker inbox: %v", err)
	}
	before := f.run(f.worker, "", "get-status", "--postman-md", f.config)
	ready := f.jsonResult(t, before)
	if before.exit != 0 || ready.Status != "ok" || ready.Self.Unread == nil || *ready.Self.Unread != 1 {
		t.Fatalf("ready status: exit=%d result=%+v stderr=%q", before.exit, ready, before.stderr)
	}
	assertPrivate("ready", before)
	if len(ready.Self.Inbound) != 1 || ready.Self.Inbound[0].MessageID != sent.Message.ID ||
		ready.Self.Inbound[0].InputRequestID != sent.Message.InputRequestID {
		t.Fatalf("ready status lost exact request obligation: %+v", ready.Self.Inbound)
	}
	claim := f.run(f.worker, "", "pop", "--postman-md", f.config, sent.Message.ID)
	got := f.jsonResult(t, claim)
	if claim.exit != 0 || got.Status != "message" || got.Message == nil || got.Message.ID != sent.Message.ID {
		t.Fatalf("worker claim: exit=%d result=%+v stderr=%q", claim.exit, got, claim.stderr)
	}
	if _, err := os.Stat(workerInbox); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("claim retained worker inbox copy: %v", err)
	}
	archive, err := os.ReadFile(got.MarkdownPath)
	if err != nil || !bytes.Contains(archive, []byte(requestBody)) {
		t.Fatalf("claim archive missing request: %v", err)
	}
	receipt, err := os.ReadFile(got.ReceiptPath)
	if err != nil {
		t.Fatalf("claim receipt missing: %v", err)
	}
	var recorded workspace.ClaimReceipt
	if err := json.Unmarshal(receipt, &recorded); err != nil || recorded.ID != sent.Message.ID || recorded.Role != "worker" {
		t.Fatalf("wrong durable claim receipt: %+v, err=%v", recorded, err)
	}
	second := f.run(f.worker, "", "pop", "--postman-md", f.config, sent.Message.ID)
	duplicate := f.jsonResult(t, second)
	if second.exit != 0 || duplicate.Status != "empty" || duplicate.Message != nil {
		t.Fatalf("duplicate exact claim: exit=%d result=%+v stderr=%q", second.exit, duplicate, second.stderr)
	}

	reply := f.run(f.worker, replyBody, "send-heredoc", "--postman-md", f.config,
		"--to", "peer", "--reply-to", sent.Message.ID,
		"--fills-input-request-id", sent.Message.InputRequestID, "--no-reply")
	replied := f.jsonResult(t, reply)
	if reply.exit != 0 || replied.Status != "sent" || replied.Message == nil || replied.Message.ID == sent.Message.ID ||
		replied.Message.From != "worker" || replied.Message.To != "peer" ||
		replied.Message.ReplyTo != sent.Message.ID || replied.Message.FillsInputRequestID != sent.Message.InputRequestID {
		t.Fatalf("reply send: exit=%d result=%+v stderr=%q", reply.exit, replied, reply.stderr)
	}
	peerClaim := f.run(f.peer, "", "pop", "--postman-md", f.config, replied.Message.ID)
	peerRead := f.jsonResult(t, peerClaim)
	if peerClaim.exit != 0 || peerRead.Status != "message" || peerRead.Message == nil || peerRead.Message.ID != replied.Message.ID {
		t.Fatalf("peer claim: exit=%d result=%+v stderr=%q", peerClaim.exit, peerRead, peerClaim.stderr)
	}
	if peerRead.Message.FillsInputRequestID != sent.Message.InputRequestID {
		t.Fatalf("peer claim lost exact reply fill: %+v", peerRead.Message)
	}
	if raw, err := os.ReadFile(peerRead.MarkdownPath); err != nil || !strings.Contains(string(raw), replyBody) {
		t.Fatalf("reply archive missing body: %v", err)
	}
	for _, agent := range []struct {
		name string
		pane string
	}{{"worker", f.worker}, {"peer", f.peer}} {
		closed := f.run(agent.pane, "", "get-status", "--postman-md", f.config)
		state := f.jsonResult(t, closed)
		if closed.exit != 0 || state.Status != "ok" || len(state.Self.Inbound) != 0 || len(state.Self.Outbound) != 0 {
			t.Fatalf("%s exact reply obligation remained open: exit=%d state=%+v stderr=%q", agent.name, closed.exit, state.Self, closed.stderr)
		}
		assertPrivate(agent.name, closed)
	}

	marker := filepath.Join(f.dir, "end-to-end-command")
	execution := f.run(f.worker, "", "execute-bash", "--postman-md", f.config,
		"--command", "printf complete > "+marker)
	if execution.exit != 0 || !bytes.Contains(execution.stderr, []byte(`"status":"executed"`)) {
		t.Fatalf("execution: exit=%d stdout=%q stderr=%q", execution.exit, execution.stdout, execution.stderr)
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "complete" {
		t.Fatalf("execution marker missing: %q, %v", raw, err)
	}
	final := f.run(f.worker, "", "get-status", "--postman-md", f.config)
	ended := f.jsonResult(t, final)
	if final.exit != 0 || ended.Status != "ok" || ended.Self.Unread == nil || *ended.Self.Unread != 0 {
		t.Fatalf("final status: exit=%d result=%+v stderr=%q", final.exit, ended, final.stderr)
	}
	assertPrivate("final", final)
}
