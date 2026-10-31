package main

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

// The internal/cli test harness deliberately poisons tmux on PATH (#845).
// This root-package test uses only its own private tmux socket and sleep panes.
func TestWorkspaceCLILocalSendAndPopKeepVerifiedSessionPeers(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	socketDir, err := os.MkdirTemp("/tmp", "pm937-cli-")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(socketDir, "s")
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	runTmux := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "tmux", append([]string{"-S", socket}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("owned tmux %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	home := t.TempDir()
	for _, name := range []string{"parent", "child", "grandchild"} {
		if err := os.Mkdir(filepath.Join(home, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	parent := runTmux("-f", "/dev/null", "new-session", "-d", "-s", "parent", "-c", filepath.Join(home, "parent"), "-P", "-F", "#{pane_id}", "exec sleep 120")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "tmux", "-S", socket, "kill-server").Run()
	})
	runTmux("select-pane", "-t", parent, "-T", "worker")
	local := runTmux("new-window", "-d", "-t", "parent", "-c", filepath.Join(home, "parent"), "-P", "-F", "#{pane_id}", "exec sleep 120")
	runTmux("select-pane", "-t", local, "-T", "local")
	child := runTmux("new-session", "-d", "-s", "child", "-c", filepath.Join(home, "child"), "-P", "-F", "#{pane_id}", "exec sleep 120")
	runTmux("select-pane", "-t", child, "-T", "worker")
	grandchild := runTmux("new-session", "-d", "-s", "grandchild", "-c", filepath.Join(home, "grandchild"), "-P", "-F", "#{pane_id}", "exec sleep 120")
	runTmux("select-pane", "-t", grandchild, "-T", "worker")

	config := filepath.Join(t.TempDir(), "postman.md")
	content := "---\ninterface_node: worker\nworkspace_tree:\n  - session_name: parent\n    interface_node: worker\n  - session_name: child\n    parent_session_name: parent\n    interface_node: worker\n  - session_name: stale\n    parent_session_name: parent\n    interface_node: worker\n  - session_name: grandchild\n    parent_session_name: child\n    interface_node: worker\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- local\n```\n## `worker`\nWorker\n## `local`\nLocal\n"
	if err := os.WriteFile(config, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	stateHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("TMUX", socket+",0,0")
	t.Setenv("TMUX_PANE", parent)
	decode := func(fn func() error) struct {
		Status  string              `json:"status"`
		Message *workspace.Message  `json:"message"`
		Self    workspace.SelfState `json:"self"`
	} {
		t.Helper()
		read, write, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		previous := os.Stdout
		os.Stdout = write
		runErr := fn()
		os.Stdout = previous
		_ = write.Close()
		out, readErr := io.ReadAll(read)
		_ = read.Close()
		if runErr != nil || readErr != nil {
			t.Fatalf("CLI failed: %v, read: %v, output: %s", runErr, readErr, out)
		}
		var result struct {
			Status  string              `json:"status"`
			Message *workspace.Message  `json:"message"`
			Self    workspace.SelfState `json:"self"`
		}
		if err := json.Unmarshal(out, &result); err != nil {
			t.Fatalf("CLI JSON: %v, output: %s", err, out)
		}
		return result
	}
	readStdin, writeStdin, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeStdin.WriteString("local message\n"); err != nil {
		t.Fatal(err)
	}
	_ = writeStdin.Close()
	previousStdin := os.Stdin
	os.Stdin = readStdin
	t.Cleanup(func() { os.Stdin = previousStdin; _ = readStdin.Close() })
	sent := decode(func() error {
		return cli.RunWorkspaceSendHeredoc([]string{"--postman-md", config, "--to", "local", "--reply-required"})
	})
	if sent.Status != "sent" || !sent.Self.Known || sent.Self.Unread == nil || *sent.Self.Unread != 0 || len(sent.Self.Inbound) != 0 || len(sent.Self.Outbound) != 1 ||
		strings.Join(sent.Self.Peers, ",") != "child:worker,local" {
		t.Fatalf("local send lost verified peers or obligations: %+v", sent)
	}

	root, err := workspace.DefaultStateRoot(home, stateHome)
	if err != nil {
		t.Fatal(err)
	}
	mail := workspace.Mail{Store: workspace.Store{StateRoot: root}}
	localRuntime := workspace.Tmux{Socket: socket, Pane: local, Home: home, Roles: []string{"worker", "local"}}
	if _, err := mail.SendWithState(context.Background(), localRuntime, "worker", "incoming", "", "", false, []string{"worker"}); err != nil {
		t.Fatal(err)
	}
	claimed := decode(func() error { return cli.RunWorkspacePop([]string{"--postman-md", config}) })
	if claimed.Status != "message" || claimed.Message == nil || claimed.Message.From != "local" || !claimed.Self.Known ||
		claimed.Self.Unread == nil || *claimed.Self.Unread != 0 || len(claimed.Self.Inbound) != 0 || len(claimed.Self.Outbound) != 1 ||
		strings.Join(claimed.Self.Peers, ",") != "child:worker,local" {
		t.Fatalf("local POP lost verified peers or obligations: %+v", claimed)
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
			for _, name := range []string{"parent", "child"} {
				if err := os.Mkdir(filepath.Join(home, name), 0o700); err != nil {
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
			child := runTmux("new-session", "-d", "-s", "child", "-c", filepath.Join(home, "child"), "-P", "-F", "#{pane_id}", "exec sleep 120")
			runTmux("select-pane", "-t", child, "-T", "worker")

			config := filepath.Join(t.TempDir(), "postman.md")
			content := "---\ninterface_node: worker\nworkspace_tree:\n  - session_name: parent\n    interface_node: worker\n  - session_name: child\n    parent_session_name: parent\n    interface_node: worker\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- local\nalt --- worker\n```\n## `worker`\nWorker\n## `local`\nLocal\n## `alt`\nAlt\n"
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
			sendErr := cli.RunWorkspaceSendHeredoc([]string{"--postman-md", config, "--to", tc.to})
			os.Stdout = previousOut
			_ = writeOut.Close()
			out, readErr := io.ReadAll(readOut)
			_ = readOut.Close()
			if sendErr == nil || readErr != nil {
				t.Fatalf("retitled send: %v, output read: %v", sendErr, readErr)
			}
			var result struct {
				Status  string              `json:"status"`
				Self    workspace.SelfState `json:"self"`
				Message *workspace.Message  `json:"message"`
			}
			if err := json.Unmarshal(out, &result); err != nil || result.Status != "unavailable" || result.Self.Known || len(result.Self.Peers) != 0 || result.Message != nil {
				t.Fatalf("retitle leaked status or stale peers: %+v, decode: %v, raw: %s", result, err, out)
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

// The named caller may be pinned while the pane temporarily has a different
// role. Peer guidance must still agree with the role in the mail-state view.
func TestWorkspaceCLIAlternatingRetitlesClearSessionPeers(t *testing.T) {
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "pm937-peer-retitle-")
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
	for _, name := range []string{"parent", "child"} {
		if err := os.Mkdir(filepath.Join(home, name), 0o700); err != nil {
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
	child := runTmux("new-session", "-d", "-s", "child", "-c", filepath.Join(home, "child"), "-P", "-F", "#{pane_id}", "exec sleep 120")
	runTmux("select-pane", "-t", child, "-T", "worker")

	config := filepath.Join(t.TempDir(), "postman.md")
	content := "---\ninterface_node: worker\nworkspace_tree:\n  - session_name: parent\n    interface_node: worker\n  - session_name: child\n    parent_session_name: parent\n    interface_node: worker\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- local\nalt --- worker\n```\n## `worker`\nWorker\n## `local`\nLocal\n## `alt`\nAlt\n"
	if err := os.WriteFile(config, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	stateHome := t.TempDir()
	root, err := workspace.DefaultStateRoot(home, stateHome)
	if err != nil {
		t.Fatal(err)
	}
	mail := workspace.Mail{Store: workspace.Store{StateRoot: root}}
	runtime := workspace.Tmux{Socket: socket, Pane: parent, Home: home, Roles: []string{"worker", "local", "alt"}}
	if _, err := mail.SendWithState(context.Background(), runtime, "local", "fixture", "", "", false, []string{"local"}); err != nil {
		t.Fatal(err)
	}
	proxyDir := t.TempDir()
	counter := filepath.Join(proxyDir, "count")
	// Each Snapshot makes two long display-message calls. The flips occur
	// after the helper's first bound Snapshot, its first named Snapshot, and
	// the peer lookup, respectively; all earlier mail-state scans see worker.
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
    case "$count" in
      8|20) "%s" -S "%s" select-pane -t "%s" -T alt || exit 98 ;;
      14) "%s" -S "%s" select-pane -t "%s" -T worker || exit 98 ;;
    esac
    ;;
esac
exit "$status"
`, socket, realTmux, counter, counter, counter, realTmux, socket, parent, realTmux, socket, parent)
	if err := os.WriteFile(filepath.Join(proxyDir, "tmux"), []byte(proxy), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", proxyDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("TMUX", socket+",0,0")
	t.Setenv("TMUX_PANE", parent)
	readOut, writeOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previousOut := os.Stdout
	os.Stdout = writeOut
	statusErr := cli.RunWorkspaceGetStatus([]string{"--postman-md", config})
	os.Stdout = previousOut
	_ = writeOut.Close()
	out, readErr := io.ReadAll(readOut)
	_ = readOut.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	var result struct {
		Status string              `json:"status"`
		Self   workspace.SelfState `json:"self"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("CLI JSON: %v, output: %s", err, out)
	}
	count, _ := os.ReadFile(counter)
	if statusErr == nil || result.Status != "unavailable" || result.Self.Known || result.Self.Unread != nil ||
		len(result.Self.Inbound) != 0 || len(result.Self.Outbound) != 0 || len(result.Self.Peers) != 0 ||
		strings.TrimSpace(string(count)) != "14" || runTmux("display-message", "-p", "-t", parent, "#{pane_title}") != "worker" {
		t.Fatalf("alternating retitle leaked peer guidance: %+v, error: %v, count: %q, raw: %s", result, statusErr, count, out)
	}
}
