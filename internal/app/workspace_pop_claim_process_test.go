package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

func TestWorkspacePopProcessReportsRemainingBatchAndContinuation(t *testing.T) {
	f := newPublicApprovalFixture(t)
	want := map[string]string{}
	for index := range 3 {
		body := fmt.Sprintf("batch message %d\nread through this final line\n", index)
		if sent, err := f.run(f.peer, body, "send-heredoc", "--config", f.config, "--to", "worker"); err != nil || !sent.Success {
			t.Fatal(sent, err)
		}
		want[f.sent("peer", body).ID] = body
	}
	for remaining := 2; remaining >= 0; remaining-- {
		got, err := f.run(f.worker, "", "pop", "--config", f.config)
		if err != nil || !got.Success || got.MarkdownPath == nil || got.Self.UnreadCount == nil || *got.Self.UnreadCount != remaining {
			t.Fatal("incorrect post-claim unread count", remaining, got, err)
		}
		archived := readProtocolArchive(t, *got.MarkdownPath)
		if body, ok := want[archived.ID]; !ok || body != archived.Body {
			t.Fatal("duplicate or unexpected archive", archived)
		}
		delete(want, archived.ID)
		if remaining > 0 {
			for _, fragment := range []string{
				fmt.Sprintf("Remaining unread messages after this POP: %d", remaining),
				"After fully reading and handling this archive",
				"run `tmux-a2a-postman pop` again without waiting for another notification",
				"stop on zero, unknown, or an empty POP",
			} {
				if !strings.Contains(got.Instruction, fragment) {
					t.Fatal("missing batch guidance", fragment, got.Instruction)
				}
			}
		} else if strings.Contains(got.Instruction, "pop` again") {
			t.Fatal("empty inbox requested another POP", got.Instruction)
		}
		if reply, err := f.run(f.worker, "read complete; batch item handled", "send-heredoc", "--config", f.config, "--to", archived.ID); err != nil || !reply.Success {
			t.Fatal(reply, err)
		}
	}
	empty, err := f.run(f.worker, "", "pop", "--config", f.config)
	if err != nil || !empty.Success || empty.MarkdownPath != nil || empty.Instruction != "" || empty.Self.UnreadCount == nil || *empty.Self.UnreadCount != 0 {
		t.Fatal("empty POP must end the batch", empty, err)
	}
}

func TestWorkspacePopProcessUnknownUnreadDoesNotRequestContinuation(t *testing.T) {
	f := newPublicApprovalFixture(t)
	if sent, err := f.run(f.peer, "claim despite unavailable supplemental counts", "send-heredoc", "--config", f.config, "--to", "worker"); err != nil || !sent.Success {
		t.Fatal(sent, err)
	}
	// Corrupt a new inbox entry only after the receipt exists, during the
	// supplemental native refresh. The claim stays valid while unread becomes
	// unknown; damage in another box can leave unread independently known.
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(f.home, "shim")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	sent := f.sent("peer", "claim despite unavailable supplemental counts")
	receipt := filepath.Join(f.box("worker", "read"), sent.ID+".pop.json")
	invalid := filepath.Join(f.box("worker", "inbox"), "invalid.md")
	shim := fmt.Sprintf("#!/bin/sh\nif [ -f %q ]; then printf 'damaged concurrent entry' > %q; fi\nexec %q \"$@\"\n", receipt, invalid, realTmux)
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err := f.run(f.worker, "", "pop", "--config", f.config)
	if err != nil || !got.Success || got.MarkdownPath == nil || got.Self.UnreadCount != nil || got.Self.Reason == "" {
		t.Fatal("supplemental uncertainty must not undo the claim or become zero", got, err)
	}
	if got.Instruction == "" || strings.Contains(got.Instruction, "pop` again") || strings.Contains(got.Instruction, "Remaining unread messages") {
		t.Fatal("unknown count must retain full-read guidance without requesting continuation", got.Instruction)
	}
}

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
