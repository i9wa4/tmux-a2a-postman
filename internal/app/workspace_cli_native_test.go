package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/cli"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

func TestWorkspaceCLIStatusShowsOnlyDirectInterfaceRelatives(t *testing.T) {
	f := newPublicApprovalFixture(t)
	childRoot := filepath.Join(f.home, "work", "child")
	grandRoot := filepath.Join(childRoot, "grandchild")
	for _, dir := range []string{childRoot, grandRoot} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	child := f.tmux("new-session", "-d", "-s", "child", "-c", childRoot, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.tmux("select-pane", "-t", child, "-T", "worker")
	grand := f.tmux("new-session", "-d", "-s", "grandchild", "-c", grandRoot, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.tmux("select-pane", "-t", grand, "-T", "worker")
	content := "---\ninterface_node: worker\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `worker`\nWorker\n## `peer`\nPeer\n"
	if err := os.WriteFile(f.config, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, pane := range []string{f.worker, child, grand} {
		if got, err := f.run(pane, "", "pop", "--config", f.config); err != nil || !got.Success {
			t.Fatal(got, err)
		}
	}
	status, err := f.run(f.worker, "", "get-status", "--config", f.config)
	if err != nil || len(status.Sessions) != 3 {
		t.Fatal(status, err)
	}
	own := findStatusPane(t, status.Sessions, f.worker)
	if own.Scope == nil || own.Scope.Interface != "worker" {
		t.Fatal(own)
	}
	childStatus := findStatusPane(t, status.Sessions, child)
	if childStatus.Workspace != "~/work/child" || childStatus.Status == nil || childStatus.Status.UnreadCount == nil {
		t.Fatal(childStatus)
	}
	failed, sendErr := f.run(f.worker, "must not publish", "send-heredoc", "--config", f.config, "--to", "missing:worker")
	if sendErr == nil || failed.Success {
		t.Fatal("unavailable directory peer was accepted", failed, sendErr)
	}
	nested, err := f.run(child, "", "get-status", "--config", f.config)
	if err != nil || len(nested.Sessions) != 3 || nested.AsOf != status.AsOf { // Failed send may publish another snapshot.
		if err != nil || len(nested.Sessions) != 3 {
			t.Fatal(nested, err)
		}
	}
	normal, err := f.run(f.peer, "", "get-status", "--config", f.config)
	if err != nil || normal.AsOf != nested.AsOf || len(normal.Sessions) != 3 {
		t.Fatal(normal, err)
	}
}

func TestDirectoryRouteUsesEachPeersRegisteredInterface(t *testing.T) {
	f := newPublicApprovalFixture(t)
	childRoot := filepath.Join(f.home, "work", "child")
	if err := os.MkdirAll(childRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	child := f.tmux("new-session", "-d", "-s", "child", "-c", childRoot, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.tmux("select-pane", "-t", child, "-T", "messenger")
	parentConfig := f.config
	childConfig := filepath.Join(f.home, "child-postman.md")
	for path, content := range map[string]string{
		parentConfig: "---\ninterface_node: worker\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `worker`\nWorker\n## `peer`\nPeer\n",
		childConfig:  "---\ninterface_node: messenger\n---\n## `edges`\n```mermaid\ngraph LR\nmessenger --- helper\n```\n## `messenger`\nMessenger\n## `helper`\nHelper\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if result, err := f.run(f.worker, "", "pop", "--config", parentConfig); err != nil || !result.Success {
		t.Fatal("parent registration", result, err)
	}
	if result, err := f.run(f.worker, "unregistered peer", "send-heredoc", "--config", parentConfig, "--to", "child:messenger"); err == nil || result.Success {
		t.Fatal("unregistered title became a route", result, err)
	}
	if result, err := f.run(child, "", "pop", "--config", childConfig); err != nil || !result.Success {
		t.Fatal("child registration", result, err)
	}
	if result, err := f.run(f.worker, "wrong role", "send-heredoc", "--config", parentConfig, "--to", "child:worker"); err == nil || result.Success {
		t.Fatal("caller-selected peer role became a route", result, err)
	}
	requestBody := "registered cross-config task\n"
	if result, err := f.run(f.worker, requestBody, "send-heredoc", "--config", parentConfig, "--to", "child:messenger"); err != nil || !result.Success {
		t.Fatal("cross-config task", result, err)
	}
	pop, err := f.run(child, "", "pop", "--config", childConfig)
	if err != nil || !pop.Success || pop.MarkdownPath == nil {
		t.Fatal("child POP", pop, err)
	}
	complete, err := os.ReadFile(*pop.MarkdownPath)
	if err != nil || !strings.Contains(string(complete), requestBody) {
		t.Fatal("complete child archive", err)
	}
	request := readProtocolArchive(t, *pop.MarkdownPath)
	if request.From != "worker" || request.To != "messenger" || request.FromSession != "work" || request.ToSession != "child" || request.Context == nil || !strings.Contains(request.Context.Markdown, "work:worker") || !strings.Contains(request.Context.Markdown, "Messenger") {
		t.Fatal("registered route or contact", request)
	}
	answerBody := "registered cross-config answer\n"
	if result, err := f.run(child, answerBody, "send-heredoc", "--config", childConfig, "--to", request.ID); err != nil || !result.Success {
		t.Fatal("exact child reply", result, err)
	}
	pop, err = f.run(f.worker, "", "pop", "--config", parentConfig)
	if err != nil || !pop.Success || pop.MarkdownPath == nil {
		t.Fatal("parent POP", pop, err)
	}
	complete, err = os.ReadFile(*pop.MarkdownPath)
	if err != nil || !strings.Contains(string(complete), answerBody) {
		t.Fatal("complete parent archive", err)
	}
	answer := readProtocolArchive(t, *pop.MarkdownPath)
	if len(answer.Replies) != 1 || answer.Replies[0].MessageID != request.ID || answer.From != "messenger" || answer.To != "worker" {
		t.Fatal("exact reply correlation", answer)
	}
}

func TestExternalChannelIsBoundToExactChildInterface(t *testing.T) {
	f := newPublicApprovalFixture(t)
	childRoot := filepath.Join(f.home, "work", "child")
	if err := os.MkdirAll(childRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	child := f.tmux("new-session", "-d", "-s", "child", "-c", childRoot, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.tmux("select-pane", "-t", child, "-T", "messenger")
	root := filepath.Join(f.home, "child-external")
	configPath := filepath.Join(f.home, "child-external.md")
	content := "---\ninterface_node: messenger\nexternal_mailbox_root: " + root + "\nexternal_workspace: ~/work/child\nexternal_session: child\n---\n## `edges`\n```mermaid\ngraph LR\nmessenger --- helper\n```\n## `messenger`\nMessenger\n## `helper`\nHelper\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := f.run(f.worker, "", "channel-sync", "--config", configPath); err == nil || result.Success {
		t.Fatal("parent accessed child binding", result, err)
	}
	if result, err := f.run(child, "", "channel-sync", "--config", configPath); err != nil || !result.Success {
		t.Fatal("configured child binding", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "binding.json")); err != nil {
		t.Fatal("child binding missing", err)
	}
}

func TestWorkspaceCLILocalArchiveListsNearestDirectoryContacts(t *testing.T) {
	f := newPublicApprovalFixture(t)
	childRoot := filepath.Join(f.home, "work", "child")
	var childPane string
	directoryPanes := []string{f.worker}
	for _, item := range []struct{ name, root string }{
		{"child", childRoot},
		{"grandchild", filepath.Join(childRoot, "grandchild")},
		{"sibling", filepath.Join(f.home, "work", "sibling")},
		{"prefix", filepath.Join(f.home, "work", "childish")},
	} {
		if err := os.MkdirAll(item.root, 0o700); err != nil {
			t.Fatal(err)
		}
		pane := f.tmux("new-session", "-d", "-s", item.name, "-c", item.root, "-P", "-F", "#{pane_id}", "exec sleep 120")
		f.tmux("select-pane", "-t", pane, "-T", "worker")
		directoryPanes = append(directoryPanes, pane)
		if item.name == "child" {
			childPane = pane
		}
	}
	childPeer := f.tmux("new-window", "-d", "-t", "child", "-c", childRoot, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.tmux("select-pane", "-t", childPeer, "-T", "peer")
	content := "---\ninterface_node: worker\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `worker`\nWorker\n## `peer`\nPeer\n"
	if err := os.WriteFile(f.config, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, pane := range directoryPanes {
		if result, err := f.run(pane, "", "pop", "--config", f.config); err != nil || !result.Success {
			t.Fatal("register directory interface", result, err)
		}
	}
	if sent, err := f.run(childPeer, "local directory contacts", "send-heredoc", "--config", f.config, "--to", "worker"); err != nil || !sent.Success {
		t.Fatal(sent, err)
	}
	claimed, err := f.run(childPane, "", "pop", "--config", f.config)
	if err != nil || !claimed.Success || claimed.MarkdownPath == nil {
		t.Fatal(claimed, err)
	}
	archive := readProtocolArchive(t, *claimed.MarkdownPath)
	if archive.Body != "local directory contacts" || archive.Context == nil {
		t.Fatal("local message lost body or generated context", archive)
	}
	_, after, ok := strings.Cut(archive.Context.Markdown, "You can talk to:\n\n")
	if !ok {
		t.Fatal("missing contact section", archive.Context.Markdown)
	}
	contacts, _, ok := strings.Cut(after, "\n\nThese are configured routes")
	if !ok || !strings.Contains(contacts, "work:worker") || !strings.Contains(contacts, "grandchild:worker") || !strings.Contains(contacts, "- peer") || strings.Contains(contacts, "sibling:worker") || strings.Contains(contacts, "prefix:worker") {
		t.Fatal("local archive contacts did not match nearest live routes", contacts)
	}
}

func TestWorkspaceCLIRespawnBeforeSendCannotAdoptReplacement(t *testing.T) {
	f := newPublicApprovalFixture(t)
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	proxyDir := t.TempDir()
	counter := filepath.Join(proxyDir, "counter")
	proxy := fmt.Sprintf(`#!/bin/sh
"%s" "$@"
result=$?
case "$*" in
 *'#{socket_path}'*)
 count=0
 if [ -f "%s" ]; then count=$(cat "%s"); fi
 count=$((count+1)); printf '%%s' "$count" > "%s"
 if [ "$count" -eq 2 ]; then
  "%s" -S "%s" respawn-pane -k -t "%s" 'exec sleep 120' || exit 98
  "%s" -S "%s" select-pane -t "%s" -T worker || exit 98
 fi ;;
esac
exit "$result"
`, realTmux, counter, counter, counter, realTmux, f.socket, f.worker, realTmux, f.socket, f.worker)
	if err := os.WriteFile(filepath.Join(proxyDir, "tmux"), []byte(proxy), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := f.child(ctx, f.worker, "send-heredoc", "--config", f.config, "--to", "peer")
	cmd.Env = append(cmd.Env, "PATH="+proxyDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdin = strings.NewReader("old caller must not publish")
	raw, err := cmd.Output()
	if err == nil {
		t.Fatal("old invocation adopted replacement", string(raw))
	}
	var result protocolOutput
	if json.Unmarshal(raw, &result) != nil || result.Success {
		t.Fatal("invalid failure result", string(raw))
	}
	if _, err := os.Stat(f.stateRoot()); !os.IsNotExist(err) {
		t.Fatal("stale invocation initialized replacement state", err)
	}
}

// The proxy changes only an owned pane, after the final native observation in
// the CLI's initial Snapshot. Thus the command starts with a worker snapshot
// but sees an alt role when it routes or reaches the mail lock.
func TestWorkspaceCLIRetitleBeforeSendFailsClosed(t *testing.T) {
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	for _, tc := range []struct{ name, to string }{{"local", "local"}, {"cross", "child:worker"}} {
		t.Run(tc.name, func(t *testing.T) {
			dir, err := os.MkdirTemp("/tmp", "pm937-retitle-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			socket := filepath.Join(dir, "s")
			runTmux := func(args ...string) string {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				defer cancel()
				out, err := exec.CommandContext(ctx, realTmux, append([]string{"-S", socket}, args...)...).CombinedOutput()
				if err != nil {
					t.Fatalf("owned tmux %v: %v: %s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			home := t.TempDir()
			for _, name := range []string{"parent", "parent/child"} {
				if err := os.MkdirAll(filepath.Join(home, name), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			parent := runTmux("-f", "/dev/null", "new-session", "-d", "-s", "parent", "-c", filepath.Join(home, "parent"), "-P", "-F", "#{pane_id}", "exec sleep 120")
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = exec.CommandContext(ctx, realTmux, "-S", socket, "kill-server").Run()
			})
			runTmux("select-pane", "-t", parent, "-T", "worker")
			local := runTmux("new-window", "-d", "-t", "parent", "-c", filepath.Join(home, "parent"), "-P", "-F", "#{pane_id}", "exec sleep 120")
			runTmux("select-pane", "-t", local, "-T", "local")
			child := runTmux("new-session", "-d", "-s", "child", "-c", filepath.Join(home, "parent", "child"), "-P", "-F", "#{pane_id}", "exec sleep 120")
			runTmux("select-pane", "-t", child, "-T", "worker")

			config := filepath.Join(t.TempDir(), "postman.md")
			content := "---\ninterface_node: worker\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- local\nalt --- worker\n```\n## `worker`\nWorker\n## `local`\nLocal\n## `alt`\nAlt\n"
			if err := os.WriteFile(config, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			// The proxy forwards exclusively to this fixture's private socket.
			// Its second long display is the initial Snapshot's final observation.
			proxyDir := t.TempDir()
			counter := filepath.Join(proxyDir, "count")
			proxy := fmt.Sprintf(`#!/bin/sh
case "$1:$2:$3" in
  -u:-S:%s) ;;
  *) exit 97 ;;
esac
"%s" "$@"
status=$?
case "$*" in
  *'#{socket_path}'*)
    count=0
    if [ -f "%s" ]; then count=$(cat "%s"); fi
    count=$((count + 1))
    printf '%%s' "$count" > "%s"
    if [ "$count" -eq 2 ]; then
      "%s" -S "%s" select-pane -t "%s" -T alt || exit 98
    fi
    ;;
esac
exit "$status"
`, socket, realTmux, counter, counter, counter, realTmux, socket, parent)
			if err := os.WriteFile(filepath.Join(proxyDir, "tmux"), []byte(proxy), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", proxyDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			stateHome := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_STATE_HOME", stateHome)
			t.Setenv("TMUX", socket+",0,0")
			t.Setenv("TMUX_PANE", parent)
			readStdin, writeStdin, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writeStdin.WriteString("must not publish\n"); err != nil {
				t.Fatal(err)
			}
			_ = writeStdin.Close()
			previousStdin := os.Stdin
			os.Stdin = readStdin
			t.Cleanup(func() { os.Stdin = previousStdin; _ = readStdin.Close() })
			readOut, writeOut, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			previousOut := os.Stdout
			os.Stdout = writeOut
			sendErr := cli.RunWorkspaceSendHeredoc([]string{"--config", config, "--to", tc.to})
			os.Stdout = previousOut
			_ = writeOut.Close()
			out, readErr := io.ReadAll(readOut)
			_ = readOut.Close()
			if sendErr == nil || readErr != nil {
				t.Fatalf("retitled send: %v, output read: %v", sendErr, readErr)
			}
			var result protocolOutput
			if err := json.Unmarshal(out, &result); err != nil || result.Success || result.Self.UnreadCount != nil {
				t.Fatalf("retitle leaked successful send: %+v decode=%v raw=%s", result, err, out)
			}

			if title := runTmux("display-message", "-p", "-t", parent, "#{pane_title}"); title != "alt" {
				count, _ := os.ReadFile(counter)
				t.Fatalf("controlled retitle did not occur: title=%q, count=%q, output=%s, error=%v", title, count, out, sendErr)
			}
			root, err := workspace.DefaultStateRoot(home, stateHome)
			if err != nil {
				t.Fatal(err)
			}
			err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.IsDir() && strings.HasSuffix(path, ".md") &&
					(strings.Contains(path, string(filepath.Separator)+"inbox"+string(filepath.Separator)) || strings.Contains(path, string(filepath.Separator)+"sent"+string(filepath.Separator))) {
					t.Errorf("retitled sender wrote mail: %s", path)
				}
				return nil
			})
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		})
	}
}
