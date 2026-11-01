package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspacePopProcessOutputFailureKeepsOneClaim(t *testing.T) {
	f := newPublicApprovalFixture(t)
	body := "lost stdout still claims exactly once\n"
	if got, err := f.run(f.peer, body, "send-heredoc", "--config", f.config, "--to", "worker"); err != nil || !got.Success {
		t.Fatal(got, err)
	}
	sent := f.sent("peer", body)
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = read.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := f.child(ctx, f.worker, "pop", "--config", f.config)
	cmd.Stdout = write
	if err := cmd.Run(); err == nil || ctx.Err() != nil {
		t.Fatal("broken stdout did not fail promptly", err, ctx.Err())
	}
	_ = write.Close()
	archive := filepath.Join(f.box("worker", "read"), sent.ID+".md")
	receipt := filepath.Join(f.box("worker", "read"), sent.ID+".pop.json")
	before, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if readProtocolArchive(t, archive).Body != body {
		t.Fatal("body changed")
	}
	empty, err := f.run(f.worker, "", "pop", "--config", f.config)
	if err != nil || !empty.Success || empty.MarkdownPath != nil {
		t.Fatal("failed stdout resurrected claim", empty, err)
	}
	after, _ := os.ReadFile(archive)
	again, _ := os.ReadFile(receipt)
	if !bytes.Equal(before, after) || !bytes.Equal(saved, again) {
		t.Fatal("retry changed archive/receipt")
	}
	if _, err := os.Stat(filepath.Join(f.box("worker", "inbox"), sent.ID+".md")); !os.IsNotExist(err) {
		t.Fatal("inbox copy resurrected", err)
	}
}
