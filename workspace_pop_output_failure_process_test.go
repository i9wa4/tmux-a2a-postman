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

// Losing stdout after a public POP must not turn its durable claim into a
// second delivery. This test owns the tmux server and every state path it reads.
func TestWorkspacePopProcessOutputFailureKeepsOneClaim(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "pm937-pop-output-")
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
	type output struct {
		Status       string             `json:"status"`
		Message      *workspace.Message `json:"message"`
		MarkdownPath string             `json:"markdown_absolute_path"`
		ReceiptPath  string             `json:"pop_receipt_absolute_path"`
	}
	invoke := func(pane, body string, args ...string) output {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, err := child(ctx, pane, body, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("public CLI %v: %v: %s", args, err, out)
		}
		var result output
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("public CLI JSON %v: %v: %s", args, err, out)
		}
		return result
	}

	body := "stdout failure must preserve one durable claim 937-6\n"
	sent := invoke(peer, body, "send-heredoc", "--postman-md", config, "--to", "worker", "--no-reply")
	if sent.Status != "sent" || sent.Message == nil || sent.Message.ID == "" || sent.Message.From != "peer" || sent.Message.To != "worker" {
		t.Fatalf("unexpected public send: %+v", sent)
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
	if _, err := os.Stat(inbox); err != nil {
		t.Fatalf("send did not place exact ID in private inbox: %v", err)
	}

	// The child's fd 1 has no reader. The claim commits before the CLI writes
	// its JSON result, so this breaks only the path-delivery/output boundary.
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := readEnd.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	broken := child(ctx, worker, "", "pop", "--postman-md", config, sent.Message.ID)
	broken.Stdout = writeEnd
	var stderr bytes.Buffer
	broken.Stderr = &stderr
	runErr := broken.Run()
	if err := writeEnd.Close(); err != nil {
		t.Fatal(err)
	}
	if runErr == nil || ctx.Err() != nil {
		t.Fatalf("broken stdout POP unexpectedly succeeded or timed out: %v, context=%v, stderr=%q", runErr, ctx.Err(), stderr.String())
	}

	// Read independent private paths immediately after the failed public call.
	if _, err := os.Stat(inbox); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed claim retained an inbox copy: %v", err)
	}
	if _, err := os.Stat(intent); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed claim left a pending intent: %v", err)
	}
	archivedBytes, err := os.ReadFile(archived)
	if err != nil {
		t.Fatalf("failed-output POP did not archive exact ID: %v", err)
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
	if err := json.Unmarshal([]byte(header), &archivedMessage); err != nil {
		t.Fatalf("archived message header: %v", err)
	}
	if archivedMessage.ID != sent.Message.ID || archivedMessage.From != "peer" || archivedMessage.To != "worker" || archivedMessage.CreatedAt != sent.Message.CreatedAt || archivedBody != body {
		t.Fatalf("failed-output POP changed exact message/body: header=%+v body=%q", archivedMessage, archivedBody)
	}
	receiptBytes, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatalf("failed-output POP lacks durable receipt: %v", err)
	}
	var receipt workspace.ClaimReceipt
	if err := json.Unmarshal(receiptBytes, &receipt); err != nil || receipt.Version != 1 || receipt.ID != sent.Message.ID || receipt.Role != "worker" || receipt.ClaimedAt == "" {
		t.Fatalf("failed-output POP has wrong receipt: %v: %s", err, receiptBytes)
	}

	retry := invoke(worker, "", "pop", "--postman-md", config, sent.Message.ID)
	if retry.Status != "empty" || retry.Message != nil || retry.MarkdownPath != "" || retry.ReceiptPath != "" {
		t.Fatalf("retry fabricated a second claim after lost output: %+v", retry)
	}
	archivedAfter, err := os.ReadFile(archived)
	if err != nil || !bytes.Equal(archivedAfter, archivedBytes) {
		t.Fatalf("retry changed archived message: %v", err)
	}
	receiptAfter, err := os.ReadFile(receiptPath)
	if err != nil || !bytes.Equal(receiptAfter, receiptBytes) {
		t.Fatalf("retry changed durable receipt: %v", err)
	}
	if _, err := os.Stat(inbox); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retry resurrected inbox copy: %v", err)
	}
	if _, err := os.Stat(intent); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retry left or recreated claim intent: %v", err)
	}
}
