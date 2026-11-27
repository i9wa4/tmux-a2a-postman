package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPostOperationGuidanceNativeMailAndExecution(t *testing.T) {
	f := newPublicApprovalFixture(t)
	for _, body := range []string{"first required request", "second required request"} {
		if result, err := f.run(f.peer, body, "send-heredoc", "--config", f.config, "--to", "worker"); err != nil || !result.Success {
			t.Fatal("peer send", result, err)
		}
	}
	sent, err := f.run(f.worker, "own request", "send-heredoc", "--config", f.config, "--to", "peer")
	if err != nil || !sent.Success || sent.Self.UnreadCount == nil || *sent.Self.UnreadCount != 2 ||
		sent.Self.InboundCount == nil || *sent.Self.InboundCount != 2 || sent.Self.OutboundCount == nil || *sent.Self.OutboundCount != 1 ||
		!strings.Contains(sent.Instruction, "tmux-a2a-postman pop") || !strings.Contains(sent.Instruction, "Own required responses remain") ||
		strings.Contains(sent.Instruction, "Sent requests await responses") {
		t.Fatal("mixed post-send guidance", sent, err)
	}
	for remaining := 1; remaining >= 0; remaining-- {
		claimed, err := f.run(f.worker, "", "pop", "--config", f.config)
		if err != nil || !claimed.Success || claimed.MarkdownPath == nil || claimed.Self.UnreadCount == nil || *claimed.Self.UnreadCount != remaining {
			t.Fatal("claimed POP", claimed, err)
		}
		if _, err := os.ReadFile(*claimed.MarkdownPath); err != nil || !strings.HasPrefix(claimed.Instruction, "Read the complete file") {
			t.Fatal("archive-first guidance", claimed, err)
		}
		if remaining > 0 && !strings.Contains(claimed.Instruction, "Remaining unread messages after this POP: 1") {
			t.Fatal("remaining POP guidance", claimed)
		}
		request := readProtocolArchive(t, *claimed.MarkdownPath)
		replied, err := f.run(f.worker, "substantive result", "send-heredoc", "--config", f.config, "--to", request.ID)
		if err != nil || !replied.Success {
			t.Fatal("exact response", replied, err)
		}
		if remaining > 0 && !strings.Contains(replied.Instruction, "tmux-a2a-postman pop") {
			t.Fatal("reply did not surface remaining unread", replied)
		}
	}
	empty, err := f.run(f.worker, "", "pop", "--config", f.config)
	if err != nil || !empty.Success || empty.MarkdownPath != nil || empty.Instruction != "" {
		t.Fatal("empty POP urged another action", empty, err)
	}
	f.writeConfig(false)
	if result, err := f.run(f.peer, "new required request", "send-heredoc", "--config", f.config, "--to", "worker"); err != nil || !result.Success {
		t.Fatal("peer send before execution", result, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cmd := f.child(ctx, f.worker, "execute-heredoc", "--config", f.config, "--reason", "verify output streams")
	cmd.Stdin = strings.NewReader("printf child-stdout; printf child-stderr >&2")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil || stdout.String() != "child-stdout" {
		t.Fatal("execution stream/exit", stdout.String(), stderr.String(), err)
	}
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	var execution protocolOutput
	if len(lines) == 0 || json.Unmarshal([]byte(lines[len(lines)-1]), &execution) != nil ||
		!execution.Success || execution.Executed == nil || !*execution.Executed ||
		!strings.Contains(execution.Instruction, "tmux-a2a-postman pop") ||
		!strings.Contains(stderr.String(), "child-stderr") {
		t.Fatal("execution final JSON or guidance", stderr.String(), execution)
	}
	failed := f.child(ctx, f.worker, "execute-heredoc", "--config", f.config, "--reason", "verify child exit")
	failed.Stdin = strings.NewReader("printf failed-stdout; exit 7")
	stdout.Reset()
	stderr.Reset()
	failed.Stdout, failed.Stderr = &stdout, &stderr
	if err := failed.Run(); err == nil || stdout.String() != "failed-stdout" {
		t.Fatal("child exit propagation", stdout.String(), stderr.String(), err)
	}
	lines = strings.Split(strings.TrimSpace(stderr.String()), "\n")
	if json.Unmarshal([]byte(lines[len(lines)-1]), &execution) != nil || execution.Success || execution.ExitCode == nil || *execution.ExitCode != 7 ||
		!strings.Contains(execution.Instruction, "tmux-a2a-postman pop") {
		t.Fatal("failed execution result", stderr.String(), execution)
	}
}

func TestPostOperationGuidanceVerifiedExternalBinding(t *testing.T) {
	f := newPublicApprovalFixture(t)
	root := filepath.Join(f.home, "external")
	config := filepath.Join(f.home, "external.md")
	content := "---\ninterface_node: worker\nexternal_mailbox_root: " + root + "\nexternal_workspace: ~/work\nexternal_session: work\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `worker`\nWorker\n## `peer`\nPeer\n"
	if err := os.WriteFile(config, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := f.run(f.peer, "", "channel-sync", "--config", config); err == nil || result.Success || result.Self.UnreadCount != nil {
		t.Fatal("unverified caller received self", result, err)
	}
	if result, err := f.run(f.peer, "unverified", "channel-report-heredoc", "--config", config, "--to", "m-00000000000000000000000000000000", "--state", "done"); err == nil || result.Success || result.Self.UnreadCount != nil {
		t.Fatal("unverified reporter received self", result, err)
	}
	if result, err := f.run(f.peer, "required work", "send-heredoc", "--config", config, "--to", "worker"); err != nil || !result.Success {
		t.Fatal("peer send", result, err)
	}
	synced, err := f.run(f.worker, "", "channel-sync", "--config", config)
	if err != nil || !synced.Success || synced.Self.UnreadCount == nil || *synced.Self.UnreadCount != 1 ||
		!strings.Contains(synced.Instruction, "tmux-a2a-postman pop") {
		t.Fatal("verified channel sync guidance", synced, err)
	}
	failedReport, err := f.run(f.worker, "no claimed external request", "channel-report-heredoc", "--config", config,
		"--to", "m-00000000000000000000000000000000", "--state", "done")
	if err == nil || failedReport.Success || failedReport.Self.UnreadCount == nil || *failedReport.Self.UnreadCount != 1 ||
		!strings.Contains(failedReport.Instruction, "tmux-a2a-postman pop") {
		t.Fatal("verified failed report lost self/guidance", failedReport, err)
	}
	nativeClaim, err := f.run(f.worker, "", "pop", "--config", config)
	if err != nil || !nativeClaim.Success || nativeClaim.MarkdownPath == nil {
		t.Fatal("claim native request before external fixture", nativeClaim, err)
	}
	nativeRequest := readProtocolArchive(t, *nativeClaim.MarkdownPath)
	if nativeRequest.External != nil {
		t.Fatal("expected native request", nativeRequest)
	}
	if reply, err := f.run(f.worker, "Native request handled", "send-heredoc", "--config", config, "--to", nativeRequest.ID); err != nil || !reply.Success {
		t.Fatal("close native request", reply, err)
	}
	if err := os.WriteFile(filepath.Join(root, "inbox", "request.md"), []byte("May I run this exact action?"), 0o600); err != nil {
		t.Fatal(err)
	}
	if accepted, err := f.run(f.worker, "", "channel-sync", "--config", config); err != nil || !accepted.Success {
		t.Fatal("external request intake", accepted, err)
	}
	claim, err := f.run(f.worker, "", "pop", "--config", config)
	if err != nil || !claim.Success || claim.MarkdownPath == nil {
		t.Fatal("claim external request", claim, err)
	}
	request := readProtocolArchive(t, *claim.MarkdownPath)
	scope := fmt.Sprintf("%x", sha256.Sum256([]byte("exact action and context")))
	reported, err := f.run(f.worker, "Exact action needs a human decision", "channel-report-heredoc", "--config", config,
		"--to", request.ID, "--state", "pending", "--scope", scope)
	if err != nil || !reported.Success || reported.Self.UnreadCount == nil ||
		!strings.HasPrefix(reported.Instruction, "This pending external report requests an exact external decision") ||
		strings.Contains(reported.Instruction, "Sent requests await responses") {
		t.Fatal("successful pending report guidance", reported, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := f.child(ctx, f.worker, "channel-sync", "--config", config)
	var raw, diagnostic bytes.Buffer
	cmd.Stdout, cmd.Stderr = &raw, &diagnostic
	err = cmd.Run()
	var syncedPending struct {
		protocolOutput
		Channel struct {
			Pending []struct {
				DecisionID      string `json:"decision_id"`
				Scope           string `json:"scope"`
				ApproveFilename string `json:"approve_filename"`
				DeclineFilename string `json:"decline_filename"`
			} `json:"pending"`
			More bool `json:"more"`
		} `json:"channel"`
	}
	if err != nil || json.Unmarshal(raw.Bytes(), &syncedPending) != nil || !syncedPending.Success ||
		len(syncedPending.Channel.Pending) != 1 || syncedPending.Channel.Pending[0].Scope != scope ||
		syncedPending.Channel.Pending[0].DecisionID == "" ||
		syncedPending.Channel.Pending[0].ApproveFilename == "" ||
		syncedPending.Channel.Pending[0].DeclineFilename == "" || syncedPending.Channel.More ||
		syncedPending.Self.UnreadCount == nil ||
		!strings.Contains(syncedPending.Instruction, "External decisions are pending in channel.pending") ||
		strings.Contains(syncedPending.Instruction, "Sent requests await responses") {
		t.Fatal("actual pending sync JSON/guidance", raw.String(), diagnostic.String(), syncedPending, err)
	}
}

func TestPostOperationGuidanceTypedDecisions(t *testing.T) {
	for _, approve := range []bool{true, false} {
		name := "decline"
		if approve {
			name = "approve"
		}
		t.Run(name, func(t *testing.T) {
			f := newPublicApprovalFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := f.execution(ctx, "printf decision-test")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			request := f.waitRequest()
			f.claimRequest(request)
			if result, err := f.run(f.worker, "another ordinary request", "send-heredoc", "--config", f.config, "--to", "peer"); err != nil || !result.Success {
				t.Fatal("send while execution waits", result, err)
			}
			decision, err := f.decide(request, approve)
			if err != nil || !decision.Success || decision.Self.UnreadCount == nil || *decision.Self.UnreadCount != 1 ||
				!strings.Contains(decision.Instruction, "tmux-a2a-postman pop") || strings.Contains(decision.Instruction, "Sent requests await responses") {
				t.Fatal("typed decision guidance", decision, err)
			}
			_ = cmd.Wait()
			if ctx.Err() != nil {
				t.Fatal("execution did not receive exact decision", stderr.String())
			}
		})
	}
}
