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

// Two public POP processes racing for one exact ID must produce one claim.
// This proves the process boundary, not recovery from an interrupted claim.
func TestWorkspacePopProcessConcurrentExactClaim(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "pm937-pop-concurrent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	home := filepath.Join(dir, "home")
	stateHome := filepath.Join(dir, "state")
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
		out, err := exec.CommandContext(ctx, "tmux", append([]string{"-S", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("owned tmux %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	worker := runTmux("-f", "/dev/null", "new-session", "-d", "-s", "work", "-c", work, "-P", "-F", "#{pane_id}", "exec sleep 120")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "tmux", "-S", socket, "kill-server").Run()
	})
	runTmux("select-pane", "-t", worker, "-T", "worker")
	peer := runTmux("split-window", "-d", "-t", worker, "-P", "-F", "#{pane_id}", "exec sleep 120")
	runTmux("select-pane", "-t", peer, "-T", "peer")
	config := filepath.Join(dir, "postman.md")
	if err := os.WriteFile(config, []byte("## `edges`\n```mermaid\ngraph LR\npeer --- worker\n```\n## `peer`\nPeer\n## `worker`\nWorker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	type output struct {
		Status       string             `json:"status"`
		Message      *workspace.Message `json:"message"`
		MarkdownPath string             `json:"markdown_absolute_path"`
		ReceiptPath  string             `json:"pop_receipt_absolute_path"`
	}
	child := func(ctx context.Context, pane, body string, args ...string) *exec.Cmd {
		t.Helper()
		encoded, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, self, "-test.run=^TestIssue937PublicCLIChild$")
		cmd.Env = append(os.Environ(), "HOME="+home, "XDG_STATE_HOME="+stateHome,
			"TMUX="+socket+",0,0", "TMUX_PANE="+pane, "POSTMAN_TEST_PUBLIC_ARGS="+string(encoded))
		cmd.Stdin = strings.NewReader(body)
		return cmd
	}
	invoke := func(pane, body string, args ...string) output {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, err := child(ctx, pane, body, args...).CombinedOutput()
		if err != nil || ctx.Err() != nil {
			t.Fatalf("public CLI %v: %v, context=%v: %s", args, err, ctx.Err(), out)
		}
		var result output
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("public CLI JSON %v: %v: %s", args, err, out)
		}
		return result
	}

	body := "concurrent exact claim payload 937-6\n"
	sent := invoke(peer, body, "send-heredoc", "--postman-md", config, "--to", "worker", "--no-reply")
	if sent.Status != "sent" || sent.Message == nil || sent.Message.ID == "" || sent.Message.From != "peer" || sent.Message.To != "worker" {
		t.Fatalf("unexpected public send: %+v", sent)
	}
	unrelatedBody := "unrelated inbox payload 937-6\n"
	unrelated := invoke(peer, unrelatedBody, "send-heredoc", "--postman-md", config, "--to", "worker", "--no-reply")
	if unrelated.Status != "sent" || unrelated.Message == nil || unrelated.Message.ID == "" || unrelated.Message.ID == sent.Message.ID {
		t.Fatalf("unexpected unrelated public send: %+v", unrelated)
	}
	stateRoot, err := workspace.DefaultStateRoot(home, stateHome)
	if err != nil {
		t.Fatal(err)
	}
	workerBox := filepath.Join(stateRoot, "work", "agents", "worker")
	inbox := filepath.Join(workerBox, "inbox", sent.Message.ID+".md")
	archived := filepath.Join(workerBox, "read", sent.Message.ID+".md")
	receiptPath := filepath.Join(workerBox, "read", sent.Message.ID+".pop.json")
	intent := filepath.Join(stateRoot, "work", "transactions", "claim-"+sent.Message.ID+".json")
	unrelatedInbox := filepath.Join(workerBox, "inbox", unrelated.Message.ID+".md")
	unrelatedBefore, err := os.ReadFile(unrelatedInbox)
	if err != nil {
		t.Fatalf("unrelated message absent before race: %v", err)
	}
	targetBefore, err := os.ReadFile(inbox)
	if err != nil {
		t.Fatalf("selected message absent before race: %v", err)
	}

	type raceResult struct {
		raw      []byte
		err      error
		timedOut bool
	}
	start := make(chan struct{})
	results := make(chan raceResult, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			raw, err := child(ctx, worker, "", "pop", "--postman-md", config, sent.Message.ID).CombinedOutput()
			results <- raceResult{raw: raw, err: err, timedOut: ctx.Err() != nil}
		}()
	}
	close(start)
	var winner, empty, busy int
	for i := 0; i < 2; i++ {
		result := <-results
		if result.timedOut {
			t.Fatalf("concurrent public POP timed out: err=%v output=%q", result.err, result.raw)
		}
		if result.err != nil {
			if !strings.Contains(string(result.raw), workspace.ErrBusy.Error()) {
				t.Fatalf("concurrent public POP failed unexpectedly: %v: %s", result.err, result.raw)
			}
			busy++
			continue
		}
		var got output
		if err := json.Unmarshal(result.raw, &got); err != nil {
			t.Fatalf("concurrent public POP returned invalid JSON: %v: %s", err, result.raw)
		}
		switch got.Status {
		case "message":
			if got.Message == nil || got.Message.ID != sent.Message.ID || got.Message.From != "peer" || got.Message.To != "worker" ||
				got.MarkdownPath != archived || got.ReceiptPath != receiptPath {
				t.Fatalf("concurrent winner returned wrong claim: %+v", got)
			}
			winner++
		case "empty":
			if got.Message != nil || got.MarkdownPath != "" || got.ReceiptPath != "" {
				t.Fatalf("concurrent loser returned claim fields: %+v", got)
			}
			empty++
		default:
			t.Fatalf("concurrent public POP returned wrong status: %+v", got)
		}
	}
	if winner != 1 || empty+busy != 1 {
		t.Fatalf("expected one winner and one empty or busy loser: winner=%d empty=%d busy=%d", winner, empty, busy)
	}
	retry := invoke(worker, "", "pop", "--postman-md", config, sent.Message.ID)
	if retry.Status != "empty" || retry.Message != nil || retry.MarkdownPath != "" || retry.ReceiptPath != "" {
		t.Fatalf("post-race exact retry delivered a duplicate: %+v", retry)
	}
	archivedBytes, err := os.ReadFile(archived)
	if err != nil || !bytes.Equal(archivedBytes, targetBefore) {
		t.Fatalf("race did not preserve exact selected archive: %v", err)
	}
	const prefix = "<!-- postman "
	if !strings.HasPrefix(string(archivedBytes), prefix) {
		t.Fatalf("archived message lacks envelope: %q", archivedBytes)
	}
	header, archivedBody, ok := strings.Cut(strings.TrimPrefix(string(archivedBytes), prefix), " -->\n\n")
	if !ok {
		t.Fatalf("archived message lacks envelope separator: %q", archivedBytes)
	}
	var archivedMessage workspace.Message
	if err := json.Unmarshal([]byte(header), &archivedMessage); err != nil ||
		archivedMessage.ID != sent.Message.ID || archivedMessage.From != "peer" || archivedMessage.To != "worker" ||
		archivedMessage.CreatedAt != sent.Message.CreatedAt || archivedBody != body {
		t.Fatalf("race changed exact ID/body/header: err=%v header=%+v body=%q", err, archivedMessage, archivedBody)
	}
	receiptBytes, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatalf("race lacks durable receipt: %v", err)
	}
	var receipt workspace.ClaimReceipt
	if err := json.Unmarshal(receiptBytes, &receipt); err != nil ||
		receipt.Version != 1 || receipt.ID != sent.Message.ID || receipt.Role != "worker" || receipt.ClaimedAt == "" {
		t.Fatalf("race has wrong receipt: err=%v bytes=%s", err, receiptBytes)
	}
	if _, err := os.Stat(inbox); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("race retained selected inbox copy: %v", err)
	}
	if _, err := os.Stat(intent); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("race retained selected claim intent: %v", err)
	}
	unrelatedAfter, err := os.ReadFile(unrelatedInbox)
	if err != nil || !bytes.Equal(unrelatedAfter, unrelatedBefore) {
		t.Fatalf("exact race changed unrelated inbox: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workerBox, "read", unrelated.Message.ID+".md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("exact race archived unrelated ID: %v", err)
	}
}
