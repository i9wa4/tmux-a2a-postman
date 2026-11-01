package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

func TestWorkspacePopProcessFirstClaimIsDurable(t *testing.T) {
	f := newPublicApprovalFixture(t)
	body := "literal $HOME `code` 日本語\n\nthrough EOF\n"
	send, err := f.run(f.peer, body, "send-heredoc", "--config", f.config, "--to", "worker")
	if err != nil || !send.Success {
		t.Fatal(send, err)
	}
	sent := f.sent("peer", body)
	got, err := f.run(f.worker, "", "pop", "--config", f.config)
	if err != nil || !got.Success || got.MarkdownPath == nil {
		t.Fatal(got, err)
	}
	expected := filepath.Join(f.box("worker", "read"), sent.ID+".md")
	if *got.MarkdownPath != expected || !filepath.IsAbs(expected) {
		t.Fatal(got.MarkdownPath, expected)
	}
	if !strings.Contains(got.Instruction, "markdown_absolute_path through EOF") ||
		!strings.Contains(got.Instruction, "deciding no action") || !strings.Contains(got.Instruction, "truncated view") {
		t.Fatal("POP must require a complete archive read before any handling decision", got.Instruction)
	}
	archived := readProtocolArchive(t, expected)
	if archived.ID != sent.ID || archived.Body != body || archived.CreatedAt != sent.CreatedAt {
		t.Fatal(archived)
	}
	raw, err := os.ReadFile(strings.TrimSuffix(expected, ".md") + ".pop.json")
	var receipt workspace.ClaimReceipt
	if err != nil || json.Unmarshal(raw, &receipt) != nil || receipt.Version != 1 || receipt.ID != sent.ID || receipt.Role != "worker" || receipt.ClaimedAt == "" {
		t.Fatal("missing durable receipt", string(raw), err)
	}
	if _, err := os.Stat(filepath.Join(f.box("worker", "inbox"), sent.ID+".md")); !os.IsNotExist(err) {
		t.Fatal("claimed inbox remains", err)
	}
	second, err := f.run(f.worker, "", "pop", "--config", f.config)
	if err != nil || !second.Success || second.MarkdownPath != nil || second.Instruction != "" || second.Self.LastPopAt == nil || *second.Self.LastPopAt != receipt.ClaimedAt {
		t.Fatal("empty POP changed receipt/time", second, err)
	}
}
