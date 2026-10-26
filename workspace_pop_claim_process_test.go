package main

import (
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

// The public CLI must finish the durable first claim before its POP process
// returns a path to the caller. All state and tmux panes belong to this test.
func TestWorkspacePopProcessFirstClaimIsDurable(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "pm937-pop-process-")
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
	invoke := func(pane string, body string, args ...string) output {
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
		out, err := child.CombinedOutput()
		if err != nil {
			t.Fatalf("public CLI %v: %v: %s", args, err, out)
		}
		var result output
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("public CLI JSON %v: %v: %s", args, err, out)
		}
		return result
	}

	body := "first claim boundary: distinct payload 937-6\n"
	sent := invoke(peer, body, "send-heredoc", "--postman-md", config, "--to", "worker", "--no-reply")
	if sent.Status != "sent" || sent.Message == nil || sent.Message.ID == "" || sent.Message.From != "peer" || sent.Message.To != "worker" {
		t.Fatalf("unexpected public send: %+v", sent)
	}

	claimed := invoke(worker, "", "pop", "--postman-md", config)
	if claimed.Status != "message" || claimed.Message == nil || claimed.Message.ID != sent.Message.ID || claimed.Message.From != "peer" || claimed.Message.To != "worker" {
		t.Fatalf("first public POP claimed wrong mail: sent=%+v claimed=%+v", sent, claimed)
	}
	stateRoot, err := workspace.DefaultStateRoot(home, stateHome)
	if err != nil {
		t.Fatal(err)
	}
	workerBox := filepath.Join(stateRoot, "work", "agents", "worker")
	expectedRead := filepath.Join(workerBox, "read", sent.Message.ID+".md")
	expectedReceipt := filepath.Join(workerBox, "read", sent.Message.ID+".pop.json")
	expectedInbox := filepath.Join(workerBox, "inbox", sent.Message.ID+".md")
	if claimed.MarkdownPath != expectedRead || claimed.ReceiptPath != expectedReceipt {
		t.Fatalf("first public POP returned paths outside expected private worker box: %+v", claimed)
	}
	if _, err := os.Stat(expectedInbox); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("claimed ID still in expected inbox or inbox inaccessible: %s: %v", expectedInbox, err)
	}
	markdown, err := os.ReadFile(expectedRead)
	if err != nil {
		t.Fatalf("archived message missing at POP return: %v", err)
	}
	const prefix = "<!-- postman "
	if !strings.HasPrefix(string(markdown), prefix) {
		t.Fatalf("archived message lacks envelope: %q", markdown)
	}
	header, archivedBody, ok := strings.Cut(strings.TrimPrefix(string(markdown), prefix), " -->\n\n")
	if !ok {
		t.Fatalf("archived message lacks envelope separator: %q", markdown)
	}
	var archived workspace.Message
	if err := json.Unmarshal([]byte(header), &archived); err != nil {
		t.Fatalf("archived message header: %v", err)
	}
	if archived.ID != sent.Message.ID || archived.From != "peer" || archived.To != "worker" || archived.CreatedAt != claimed.Message.CreatedAt || archivedBody != body {
		t.Fatalf("archived message differs from public send/POP or exact body: header=%+v body=%q", archived, archivedBody)
	}
	var receipt workspace.ClaimReceipt
	receiptBytes, err := os.ReadFile(expectedReceipt)
	if err != nil || json.Unmarshal(receiptBytes, &receipt) != nil || receipt.Version != 1 || receipt.ID != sent.Message.ID || receipt.Role != "worker" {
		t.Fatalf("durable POP receipt missing or wrong at return: %v: %s", err, receiptBytes)
	}

	second := invoke(worker, "", "pop", "--postman-md", config)
	if second.Status != "empty" || second.Message != nil || second.MarkdownPath != "" || second.ReceiptPath != "" {
		t.Fatalf("second public POP duplicated first claim: %+v", second)
	}
}
