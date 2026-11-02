package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceUsesAgentPaneDirectoryWhenNativeSessionStartedAtHome(t *testing.T) {
	f := newPublicApprovalFixtureAtHome(t, true)
	if got := f.tmux("display-message", "-p", "-t", f.worker, "#{session_path}"); got != f.home {
		t.Fatal("fixture did not retain original HOME session directory", got)
	}
	status, err := f.run(f.worker, "", "get-status", "--config", f.config)
	if err == nil || status.Workspace != "~/work" {
		t.Fatal("unregistered status chose old session root", status, err)
	}
	if _, err := os.Stat(f.stateRoot()); !os.IsNotExist(err) {
		t.Fatal("cold status initialized state", err)
	}
	sent, err := f.run(f.worker, "native pane directory delivery", "send-heredoc", "--config", f.config, "--to", "peer")
	if err != nil || !sent.Success || sent.Workspace != "~/work" {
		t.Fatal(sent, err)
	}
	received, err := f.run(f.peer, "", "pop", "--config", f.config)
	if err != nil || !received.Success || received.Workspace != "~/work" || received.MarkdownPath == nil {
		t.Fatal(received, err)
	}
	if readProtocolArchive(t, *received.MarkdownPath).Body != "native pane directory delivery" {
		t.Fatal("wrong native workspace mailbox")
	}
	elsewhere := filepath.Join(f.home, "elsewhere")
	if err := os.Mkdir(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := f.child(ctx, f.worker, "get-status", "--config", f.config)
	cmd.Dir = elsewhere
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var moved protocolOutput
	if err := json.Unmarshal(raw, &moved); err != nil || moved.Workspace != "~/work" {
		t.Fatal("tool cwd redirected mailbox", string(raw), err)
	}
	before, err := snapshotRegularFiles(f.stateRoot())
	if err != nil {
		t.Fatal(err)
	}
	line, _, err := operatorLine(t, f)
	if err != nil || line == "" {
		t.Fatal("operator overview ignored pane-root workspace", line, err)
	}
	after, err := snapshotRegularFiles(f.stateRoot())
	if err != nil || len(before) != len(after) {
		t.Fatal("overview wrote state", err)
	}
	for path, value := range before {
		if after[path] != value {
			t.Fatal("overview changed record", path)
		}
	}
	if _, err := os.Stat(filepath.Join(f.stateRoot(), "%00")); !os.IsNotExist(err) {
		t.Fatal("HOME workspace created", err)
	}
}
