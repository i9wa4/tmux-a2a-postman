package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func operatorLine(t *testing.T, f *publicApprovalFixture) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := f.child(ctx, "", "get-status-oneline", "--config", f.config)
	var out, diagnostic bytes.Buffer
	command.Stdout, command.Stderr = &out, &diagnostic
	err := command.Run()
	return out.String(), diagnostic.String(), err
}

func statusTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		key, _ := filepath.Rel(root, path)
		value := fmt.Sprintf("%s/%d/%d", info.Mode(), info.Size(), info.ModTime().UnixNano())
		if !entry.IsDir() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += fmt.Sprintf("/%x", sha256.Sum256(raw))
		}
		result[key] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestStatusOnelineColdPaneCandidatesDoNotInitializeState(t *testing.T) {
	f := newPublicApprovalFixture(t)
	raw, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.config, append([]byte("---\nskill_path: /nonexistent/status-must-not-open-catalogs\n---\n"), raw...), 0o600); err != nil {
		t.Fatal(err)
	}
	line, diagnostic, err := operatorLine(t, f)
	if err != nil || line != "[0]??\n" || diagnostic == "" {
		t.Fatal(line, diagnostic, err)
	}
	if _, err := os.Stat(f.stateHome); !os.IsNotExist(err) {
		t.Fatal("operator initialized application state", err)
	}
}

func TestStatusOnelineReadsAllSessionsAndWindowsWithoutMutatingMail(t *testing.T) {
	f := newPublicApprovalFixture(t)
	childRoot := filepath.Join(f.home, "child")
	if err := os.Mkdir(childRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	child := f.tmux("new-session", "-d", "-s", "child", "-c", childRoot, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.tmux("select-pane", "-t", child, "-T", "worker")
	other := f.tmux("new-window", "-d", "-t", "work:", "-c", filepath.Join(f.home, "work"), "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.tmux("select-pane", "-t", other, "-T", "other")
	configuration := "## `edges`\n```mermaid\ngraph LR\nworker --- peer\nworker --- other\n```\n"
	if err := os.WriteFile(f.config, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := f.run(f.peer, "unread request\n", "send-heredoc", "--config", f.config, "--to", "worker"); err != nil || !output.Success {
		t.Fatal(output, err)
	}
	for _, pane := range []string{other, child} {
		if output, err := f.run(pane, "", "pop", "--config", f.config); err != nil || !output.Success {
			t.Fatal(output, err)
		}
	}
	before := statusTree(t, f.stateRoot())
	for range 2 {
		line, diagnostic, err := operatorLine(t, f)
		if err != nil || line != "[0]⬛🖤:⚫ [1]⚫\n" {
			t.Fatal(line, diagnostic, err)
		}
	}
	if after := statusTree(t, f.stateRoot()); !reflect.DeepEqual(before, after) {
		t.Fatal("status refresh changed saved mail, ownership or capture state")
	}
	if err := os.WriteFile(filepath.Join(f.box("worker", "inbox"), "invalid-entry"), []byte("private malformed fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	line, diagnostic, err := operatorLine(t, f)
	if err != nil || line != "[0]?🖤:⚫ [1]⚫\n" || !strings.Contains(diagnostic, "unexpected mailbox entry") {
		t.Fatal("partial read corrupted another node/session", line, diagnostic, err)
	}
}

func TestStatusOnelineRenamedOrReplacedPaneCannotKeepHealthyCounts(t *testing.T) {
	f := newPublicApprovalFixture(t)
	if output, err := f.run(f.worker, "", "pop", "--config", f.config); err != nil || !output.Success {
		t.Fatal(output, err)
	}
	f.tmux("select-pane", "-t", f.worker, "-T", "editor")
	line, diagnostic, err := operatorLine(t, f)
	if err != nil || line != "[0]?⚫\n" || !strings.Contains(diagnostic, "renamed") {
		t.Fatal(line, diagnostic, err)
	}
	f.tmux("select-pane", "-t", f.worker, "-T", "worker")
	f.tmux("respawn-pane", "-k", "-t", f.worker, "exec sleep 120")
	line, diagnostic, err = operatorLine(t, f)
	if err != nil || line != "[0]?⚫\n" {
		t.Fatal("pane replacement reused an old role identity", line, diagnostic, err)
	}
	if output, err := f.run(f.worker, "", "pop", "--config", f.config); err != nil || !output.Success {
		t.Fatal(output, err)
	}
	line, diagnostic, err = operatorLine(t, f)
	if err != nil || line != "[0]⚫⚫\n" {
		t.Fatal("verified replacement did not retain known mailbox facts with unknown activity", line, diagnostic, err)
	}
}

func TestStatusOnelineActualTmuxStatusJobAndEmojiPresentation(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	f := newPublicApprovalFixture(t)
	wrong := newPublicApprovalFixture(t)
	rebuilt := newRebuiltCLIFixture(t)
	if output, err := f.run(f.peer, "unread request\n", "send-heredoc", "--config", f.config, "--to", "worker"); err != nil || !output.Success {
		t.Fatal(output, err)
	}
	before := statusTree(t, f.stateRoot())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "scripts/validation/status-oneline-smoke.py",
		"--postman", rebuilt.bin, "--socket", f.socket, "--wrong-socket", wrong.socket,
		"--config", f.config, "--home", f.home, "--state", f.stateHome,
		"--output", filepath.Join(rebuilt.dir, "tmux-status-terminal.log"))
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("real status job: %v\n%s", err, out)
	}
	if after := statusTree(t, f.stateRoot()); !reflect.DeepEqual(before, after) {
		t.Fatal("actual status job mutated mail/capture/binding files")
	}
}

func TestStatusOnelineEmptyAndFatalResultsKeepOneLine(t *testing.T) {
	f := newPublicApprovalFixture(t)
	for _, pane := range []string{f.worker, f.peer} {
		f.tmux("select-pane", "-t", pane, "-T", "unrelated")
	}
	line, diagnostic, err := operatorLine(t, f)
	if err != nil || line != "\n" || diagnostic != "" {
		t.Fatal(line, diagnostic, err)
	}
	if err := os.Remove(f.config); err != nil {
		t.Fatal(err)
	}
	line, diagnostic, err = operatorLine(t, f)
	if err == nil || line != "\n" || diagnostic == "" {
		t.Fatal("fatal operator failure lost its line/exit boundary", line, diagnostic, err)
	}
	if _, err := os.Stat(f.stateHome); !os.IsNotExist(err) {
		t.Fatal("operator error initialized state", err)
	}
}
