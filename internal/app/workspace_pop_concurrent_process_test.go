package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspacePopProcessConcurrentClaim(t *testing.T) {
	f := newPublicApprovalFixture(t)
	if got, err := f.run(f.peer, "race payload", "send-heredoc", "--config", f.config, "--to", "worker"); err != nil || !got.Success {
		t.Fatal(got, err)
	}
	sent := f.sent("peer", "race payload")
	type result struct {
		out protocolOutput
		err error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			out, err := f.run(f.worker, "", "pop", "--config", f.config)
			results <- result{out, err}
		}()
	}
	close(start)
	claims := 0
	for range 2 {
		got := <-results
		if got.err != nil || !got.out.Success {
			t.Fatal(got)
		}
		if got.out.MarkdownPath != nil {
			claims++
			if readProtocolArchive(t, *got.out.MarkdownPath).ID != sent.ID {
				t.Fatal("wrong claim")
			}
		}
	}
	if claims != 1 {
		t.Fatalf("want one claim, got %d", claims)
	}
	entries, err := os.ReadDir(f.box("worker", "read"))
	if err != nil || len(entries) != 2 {
		t.Fatal("want archive+receipt once", entries, err)
	}
	if _, err := os.Stat(filepath.Join(f.box("worker", "inbox"), sent.ID+".md")); !os.IsNotExist(err) {
		t.Fatal("inbox resurrected", err)
	}
}
