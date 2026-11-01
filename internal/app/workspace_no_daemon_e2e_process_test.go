package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceRebuiltCLINoDaemonEndToEnd(t *testing.T) {
	f := newRebuiltCLIFixture(t)
	initial := f.run(f.worker, "", "get-status", "--config", f.config)
	if initial.exit == 0 {
		t.Fatal("missing state looked available")
	}
	if _, err := os.Stat(f.stateRoot); !os.IsNotExist(err) {
		t.Fatal("status initialized state", err)
	}
	body := "literal $HOME `code` 日本語\n\nlast line\n"
	sent := f.run(f.peer, body, "send-heredoc", "--config", f.config, "--to", "worker")
	if result := f.jsonResult(t, sent); sent.exit != 0 || !result.Success || result.Self.OutboundCount == nil || *result.Self.OutboundCount != 1 {
		t.Fatal(result, sent)
	}
	if bytes.Contains(sent.stdout, []byte(body)) {
		t.Fatal("send leaked body")
	}
	claim := f.run(f.worker, "", "pop", "--config", f.config)
	got := f.jsonResult(t, claim)
	if claim.exit != 0 || got.MarkdownPath == nil || !got.Success {
		t.Fatal(got, claim)
	}
	msg := readProtocolArchive(t, *got.MarkdownPath)
	if msg.Body != body || !msg.NeedsReply || got.Self.InboundCount == nil || *got.Self.InboundCount != 1 {
		t.Fatal(msg, got)
	}
	if raw, err := os.ReadFile(*got.MarkdownPath); err != nil || !strings.Contains(string(raw), "--to "+msg.ID) {
		t.Fatal("missing actual reply guidance", err)
	}
	reply := f.run(f.worker, "answer", "send-heredoc", "--config", f.config, "--to", msg.ID)
	if result := f.jsonResult(t, reply); reply.exit != 0 || !result.Success || result.Self.InboundCount == nil || *result.Self.InboundCount != 0 {
		t.Fatal(result, reply)
	}
	status := f.jsonResult(t, f.run(f.peer, "", "get-status", "--config", f.config))
	peer := findProtocolNode(t, status.Nodes, "peer")
	if peer.OutboundCount == nil || *peer.OutboundCount != 0 || peer.UnreadCount == nil || *peer.UnreadCount != 1 {
		t.Fatal("reply arrival did not close wait before POP", peer)
	}
	received := f.jsonResult(t, f.run(f.peer, "", "pop", "--config", f.config))
	if received.MarkdownPath == nil {
		t.Fatal(received)
	}
	answer := readProtocolArchive(t, *received.MarkdownPath)
	if answer.NeedsReply || len(answer.Replies) != 1 || answer.Replies[0].MessageID != msg.ID || answer.Body != "answer" {
		t.Fatal(answer)
	}
	if _, err := os.Stat(filepath.Join(f.stateRoot, "work", "agents", "worker", "inbox", msg.ID+".md")); !os.IsNotExist(err) {
		t.Fatal("claim left inbox", err)
	}
	empty := f.jsonResult(t, f.run(f.worker, "", "pop", "--config", f.config))
	if !empty.Success || empty.MarkdownPath != nil {
		t.Fatal(empty)
	}
}
