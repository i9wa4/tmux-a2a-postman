package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQuotedHeredocOpaqueBodyPreservesReviewerRouteAndArchiveBytes(t *testing.T) {
	f := newPublicApprovalFixture(t)
	configuration := "## `edges`\n```mermaid\ngraph LR\nworker --- reviewer\n```\n## `worker`\nWorker\n## `reviewer`\nReviewer\n"
	if err := os.WriteFile(f.config, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	f.tmux("select-pane", "-t", f.peer, "-T", "reviewer")
	body := "$postman-recall\n/review\n"
	const wantSHA = "c9fe9f89510c579fc4126e4203a17269b0b0faca2fcfd7c7f1c97d30c39f21bc"
	if len(body) != 24 || fmt.Sprintf("%x", sha256.Sum256([]byte(body))) != wantSHA {
		t.Fatal("authored ordinary quoted-heredoc body changed")
	}
	sentOutput, err := f.run(f.worker, body, "send-heredoc", "--config", f.config, "--to", "reviewer")
	if err != nil || !sentOutput.Success {
		t.Fatal("production send failed", sentOutput, err)
	}
	sent := f.sent("worker", body)
	if sent.To != "reviewer" || sent.Body != body {
		t.Fatal("reviewer route or exact stdin body changed", sent)
	}
	sentArchive, err := os.ReadFile(filepath.Join(f.box("worker", "sent"), sent.ID+".md"))
	if err != nil || !bytes.HasSuffix(sentArchive, []byte(body)) {
		t.Fatal("sent archive did not end in exact body", err)
	}
	claimed, err := f.run(f.peer, "", "pop", "--config", f.config)
	if err != nil || !claimed.Success || claimed.MarkdownPath == nil {
		t.Fatal("reviewer could not POP body", claimed, err)
	}
	archived := readProtocolArchive(t, *claimed.MarkdownPath)
	if archived.ID != sent.ID || archived.To != "reviewer" || archived.Body != body ||
		fmt.Sprintf("%x", sha256.Sum256([]byte(archived.Body))) != wantSHA {
		t.Fatal("reviewer archive route, body bytes or SHA changed", archived)
	}
	readArchive, err := os.ReadFile(*claimed.MarkdownPath)
	if err != nil || !bytes.HasSuffix(readArchive, []byte(body)) {
		t.Fatal("read archive did not end in exact inert marker lines", err)
	}
}

func TestQuotedHeredocBodyDelimiterDoesNotExecuteSenderText(t *testing.T) {
	f := newPublicApprovalFixture(t)
	configuration := "## `edges`\n```mermaid\ngraph LR\nworker --- reviewer\n```\n## `worker`\nWorker\n## `reviewer`\nReviewer\n"
	if err := os.WriteFile(f.config, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	f.tmux("select-pane", "-t", f.peer, "-T", "reviewer")
	const delimiter = "POSTMAN_BODY_7f4c39b2"
	body := "$postman-recall\nPOSTMAN_BODY\nprintf hijack > \"$POSTMAN_SIDE_EFFECT\"\n/review\n"
	for _, line := range strings.Split(body, "\n") {
		if line == delimiter {
			t.Fatal("chosen delimiter collides with body line")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	base := f.child(ctx, f.worker, "send-heredoc", "--config", f.config, "--to", "reviewer")
	sideEffect := filepath.Join(f.home, "sender-side-effect")
	script := "\"$POSTMAN_TEST_SELF\" -test.run=^TestIssue937PublicCLIChild$ <<'" + delimiter + "'\n" + body + delimiter + "\n"
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Env = append(base.Env, "POSTMAN_TEST_SELF="+f.self, "POSTMAN_SIDE_EFFECT="+sideEffect)
	cmd.Dir = base.Dir
	out, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte(`"success":true`)) {
		t.Fatalf("shell-constructed send failed: %v: %s", err, out)
	}
	if _, err := os.Stat(sideEffect); !os.IsNotExist(err) {
		t.Fatalf("body executed in sender shell: %v", err)
	}
	sent := f.sent("worker", body)
	if sent.To != "reviewer" || sent.Body != body {
		t.Fatal("shell changed recipient or body", sent)
	}
	claimed, err := f.run(f.peer, "", "pop", "--config", f.config)
	if err != nil || !claimed.Success || claimed.MarkdownPath == nil {
		t.Fatal("reviewer could not POP shell-constructed body", claimed, err)
	}
	archived := readProtocolArchive(t, *claimed.MarkdownPath)
	if archived.ID != sent.ID || archived.To != "reviewer" || archived.Body != body {
		t.Fatal("reviewer archive differs from literal body", archived)
	}
}
