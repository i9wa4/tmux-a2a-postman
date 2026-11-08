package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

type launchProtocol struct {
	Success bool                   `json:"success"`
	Reason  string                 `json:"reason"`
	DryRun  bool                   `json:"dry_run"`
	Presets []string               `json:"presets"`
	Launch  workspace.LaunchResult `json:"launch"`
}

func launchCLI(t *testing.T, f *publicApprovalFixture, args ...string) (launchProtocol, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := f.child(ctx, "", append([]string{"launch-agents"}, args...)...)
	var out, diagnostics bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &diagnostics
	err := cmd.Run()
	var result launchProtocol
	if json.Unmarshal(out.Bytes(), &result) != nil {
		t.Fatalf("launch JSON: %s; %s (%v)", out.Bytes(), diagnostics.Bytes(), err)
	}
	return result, err
}

func launchConfig(t *testing.T, f *publicApprovalFixture, commands map[string]string) string {
	t.Helper()
	command := func(role string) string { raw, _ := json.Marshal(commands[role]); return string(raw) }
	source := "---\npresets:\n  dev:\n    columns:\n      - panes:\n          - role: worker\n            command: " + command("worker") + "\n          - role: peer\n            command: " + command("peer") + "\n      - panes:\n          - role: reviewer\n            command: " + command("reviewer") + "\n  single:\n    columns:\n      - panes:\n          - role: worker\n            command: " + command("worker") + "\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- peer\nworker --- reviewer\n```\n## `common_template`\nRead the complete archive through EOF.\n"
	path := filepath.Join(f.home, "selected config's.md")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLaunchAgentsPreviewIsInertAndConfigurationFollowsRealConsoles(t *testing.T) {
	f := newPublicApprovalFixture(t)
	directory := filepath.Join(f.home, "launched team '$()")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	commands := map[string]string{}
	for _, role := range []string{"worker", "peer", "reviewer"} {
		marker := filepath.Join(directory, role+".started")
		commands[role] = fmt.Sprintf("printf '%%s\\n' \"$TMUX_A2A_POSTMAN_CONFIG\" > %s; pwd > %s; printf once\\n >> %s; exec sleep 120", launchShellWord(filepath.Join(directory, role+".config")), launchShellWord(filepath.Join(directory, role+".cwd")), launchShellWord(marker))
	}
	path := launchConfig(t, f, commands)
	listed, err := launchCLI(t, f, "--config", path, "--list")
	if err != nil || !listed.Success || len(listed.Presets) != 2 {
		t.Fatal(listed, err)
	}
	preview, err := launchCLI(t, f, "dev", "--config", path, "--cwd", directory, "--session", "launch-team", "--dry-run")
	if err != nil || !preview.Success || !preview.DryRun {
		t.Fatal(preview, err)
	}
	if _, err := os.Stat(f.stateRoot()); !os.IsNotExist(err) {
		t.Fatal("inert preview initialized state", err)
	}
	if raw := f.tmux("list-sessions", "-F", "#{session_name}"); raw != "work" {
		t.Fatal("preview created sessions", raw)
	}
	got, err := launchCLI(t, f, "dev", "--config", path, "--cwd", directory, "--session", "launch-team")
	if err != nil || !got.Success || len(got.Launch.Panes) != 3 {
		t.Fatal(got, err)
	}
	if got.Launch.Readiness == "" || got.Launch.Server == nil || got.Launch.SessionID == "" {
		t.Fatal(got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for _, role := range []string{"worker", "peer", "reviewer"} {
		for {
			if _, err := os.Stat(filepath.Join(directory, role+".config")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("command not started", role)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	lefts, tops := map[string]string{}, map[string]string{}
	for _, pane := range got.Launch.Panes {
		if !pane.Started {
			t.Fatal(pane)
		}
		// tmux 3.8 display-message sanitizes literal control characters in
		// the format. Query native fields individually rather than splitting
		// a version-dependent human display separator.
		title := f.tmux("display-message", "-p", "-t", pane.Pane, "#{pane_title}")
		cwd := f.tmux("display-message", "-p", "-t", pane.Pane, "#{pane_current_path}")
		if title != pane.Role || cwd != directory {
			t.Fatal(title, cwd, pane)
		}
		tops[pane.Role] = f.tmux("display-message", "-p", "-t", pane.Pane, "#{pane_top}")
		lefts[pane.Role] = f.tmux("display-message", "-p", "-t", pane.Pane, "#{pane_left}")
		content, _ := os.ReadFile(filepath.Join(directory, pane.Role+".config"))
		if strings.TrimSpace(string(content)) != path {
			t.Fatal("selected config lost in new console", string(content))
		}
	}
	if lefts["worker"] != lefts["peer"] || lefts["worker"] == lefts["reviewer"] || tops["worker"] == tops["peer"] {
		t.Fatal("two-column physical ordering", lefts, tops)
	}
	repeated, err := launchCLI(t, f, "dev", "--config", path, "--cwd", directory, "--session", "launch-team")
	if err == nil || repeated.Success {
		t.Fatal("duplicate launch accepted", repeated, err)
	}
	for _, role := range []string{"worker", "peer", "reviewer"} {
		raw, _ := os.ReadFile(filepath.Join(directory, role+".started"))
		if strings.Count(string(raw), "once") != 1 {
			t.Fatal("command started twice", role, string(raw))
		}
	}
	if f.tmux("display-message", "-p", "-t", f.worker, "#{pane_title}") != "worker" {
		t.Fatal("borrowed existing pane changed")
	}
	// Registration has real native bindings but creates no synthetic startup mail.
	err = filepath.WalkDir(f.stateRoot(), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "m-") {
			t.Error("synthetic startup mail", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func launchShellWord(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func TestPresetChildDirectoryFirstTaskAndExactReply(t *testing.T) {
	f := newPublicApprovalFixture(t)
	parentConfig := f.config
	if err := os.WriteFile(parentConfig, []byte("---\ninterface_node: worker\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `worker`\nWorker\n## `peer`\nPeer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	childRoot := filepath.Join(f.home, "work", "child")
	if err := os.MkdirAll(childRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	childConfig := filepath.Join(f.home, "preset-child.md")
	childSource := "---\ninterface_node: messenger\npresets:\n  child:\n    columns:\n      - panes:\n          - role: messenger\n            command: 'exec sleep 120'\n---\n## `edges`\n```mermaid\ngraph LR\nmessenger --- helper\n```\n## `messenger`\nMessenger\n## `helper`\nHelper\n"
	if err := os.WriteFile(childConfig, []byte(childSource), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := f.run(f.worker, "", "pop", "--config", parentConfig); err != nil || !result.Success {
		t.Fatal("parent registration", result, err)
	}
	preview, err := launchCLI(t, f, "child", "--config", childConfig, "--cwd", childRoot, "--session", "child", "--dry-run")
	if err != nil || !preview.Success || !preview.DryRun {
		t.Fatal("child preview", preview, err)
	}
	launched, err := launchCLI(t, f, "child", "--config", childConfig, "--cwd", childRoot, "--session", "child")
	if err != nil || !launched.Success || len(launched.Launch.Panes) != 1 {
		t.Fatal("child launch", launched, err)
	}
	child := launched.Launch.Panes[0].Pane
	requestBody := "preset child task\n"
	if result, err := f.run(f.worker, requestBody, "send-heredoc", "--config", parentConfig, "--to", "child:messenger"); err != nil || !result.Success {
		t.Fatal("first parent task", result, err)
	}
	pop, err := f.run(child, "", "pop", "--config", childConfig)
	if err != nil || !pop.Success || pop.MarkdownPath == nil {
		t.Fatal("child POP", pop, err)
	}
	complete, err := os.ReadFile(*pop.MarkdownPath)
	if err != nil || !bytes.Contains(complete, []byte(requestBody)) {
		t.Fatal("complete child archive", err)
	}
	request := readProtocolArchive(t, *pop.MarkdownPath)
	if request.From != "worker" || request.To != "messenger" || request.FromSession != "work" || request.ToSession != "child" {
		t.Fatal(request)
	}
	answerBody := "preset child answer\n"
	if result, err := f.run(child, answerBody, "send-heredoc", "--config", childConfig, "--to", request.ID); err != nil || !result.Success {
		t.Fatal("child exact reply", result, err)
	}
	pop, err = f.run(f.worker, "", "pop", "--config", parentConfig)
	if err != nil || !pop.Success || pop.MarkdownPath == nil {
		t.Fatal("parent POP", pop, err)
	}
	complete, err = os.ReadFile(*pop.MarkdownPath)
	if err != nil || !bytes.Contains(complete, []byte(answerBody)) {
		t.Fatal("complete parent archive", err)
	}
	answer := readProtocolArchive(t, *pop.MarkdownPath)
	if len(answer.Replies) != 1 || answer.Replies[0].MessageID != request.ID || answer.To != "worker" {
		t.Fatal("reply correlation", answer)
	}
}

func TestLaunchAgentsFromPaneCreatesWindowInInvokingSession(t *testing.T) {
	f := newPublicApprovalFixture(t)
	f.tmux("select-pane", "-t", f.worker, "-T", "shell")
	f.tmux("select-pane", "-t", f.peer, "-T", "other")
	marker := filepath.Join(f.home, "window-started")
	path := launchConfig(t, f, map[string]string{
		"worker": "printf started > " + launchShellWord(marker) + "; exec sleep 120",
		"peer":   "exec sleep 120", "reviewer": "exec sleep 120",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := f.child(ctx, f.worker, "launch-agents", "single", "--config", path)
	var out, diagnostics bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &diagnostics
	err := cmd.Run()
	var got launchProtocol
	if json.Unmarshal(out.Bytes(), &got) != nil || err != nil || !got.Success {
		t.Fatalf("launch: %s %s (%v)", out.Bytes(), diagnostics.Bytes(), err)
	}
	originalSession := f.tmux("display-message", "-p", "-t", f.worker, "#{session_id}")
	if got.Launch.SessionID != originalSession || got.Launch.Window == "" {
		t.Fatalf("launched outside invoking session: %+v; original=%s", got.Launch, originalSession)
	}
	if got.Launch.Window == f.tmux("display-message", "-p", "-t", f.worker, "#{window_id}") {
		t.Fatal("reused invoking window")
	}
	if got := f.tmux("list-sessions", "-F", "#{session_name}"); got != "work" {
		t.Fatal("created another session", got)
	}
	if got := f.tmux("display-message", "-p", "-t", f.worker, "#{pane_title}"); got != "shell" {
		t.Fatal("changed invoking pane", got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if raw, err := os.ReadFile(marker); err == nil && string(raw) == "started" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("new window command did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLaunchAgentsExplicitTargetsRemainDedicatedFromPane(t *testing.T) {
	for _, mode := range []string{"session", "socket"} {
		t.Run(mode, func(t *testing.T) {
			f := newPublicApprovalFixture(t)
			f.tmux("select-pane", "-t", f.worker, "-T", "shell")
			f.tmux("select-pane", "-t", f.peer, "-T", "other")
			path := launchConfig(t, f, map[string]string{
				"worker": "exec sleep 120", "peer": "exec sleep 120", "reviewer": "exec sleep 120",
			})
			args := []string{"launch-agents", "single", "--config", path}
			if mode == "session" {
				args = append(args, "--session", "explicit-team")
			} else {
				args = append(args, "--socket", f.socket)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			cmd := f.child(ctx, f.worker, args...)
			var out, diagnostics bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &diagnostics
			err := cmd.Run()
			var got launchProtocol
			if json.Unmarshal(out.Bytes(), &got) != nil || err != nil || !got.Success {
				t.Fatalf("launch: %s %s (%v)", out.Bytes(), diagnostics.Bytes(), err)
			}
			if got.Launch.SessionID == f.tmux("display-message", "-p", "-t", f.worker, "#{session_id}") {
				t.Fatal("explicit dedicated target reused invoking session")
			}
			if names := strings.Split(f.tmux("list-sessions", "-F", "#{session_name}"), "\n"); len(names) != 2 {
				t.Fatal("dedicated target not created", names)
			}
		})
	}
}

func TestLaunchedTerminalAgentsOwnFullReadRepliesAndExecutionApproval(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	f := newRebuiltCLIFixture(t)
	t.Logf("owned launched protocol fixture: %s", f.dir)
	directory := filepath.Join(f.home, "launched")
	evidence := filepath.Join(f.dir, "launch-evidence")
	for _, path := range []string{directory, evidence, filepath.Join(f.home, ".config", "tmux-a2a-postman")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Falling back to the ordinary default would reject all chosen roles.
	if err := os.WriteFile(filepath.Join(f.home, ".config", "tmux-a2a-postman", "postman.md"), []byte("## `unrelated`\nWrong default policy.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(directory, "executed-marker")
	commands := map[string]string{}
	for _, role := range []string{"worker", "peer", "reviewer"} {
		parts := []string{"env", "PATH=" + os.Getenv("PATH"), "POSTMAN_SMOKE_ROLE=" + role, "POSTMAN_SMOKE_EVIDENCE=" + evidence, "POSTMAN_BIN=" + f.bin, "POSTMAN_SMOKE_CASE=launch", "POSTMAN_SMOKE_CONVERSATION=launched-" + role, "POSTMAN_LAUNCH_QUOTED_MARKER=" + launchShellWord(marker), python, "-u", filepath.Join(repositorySource(t), "scripts/validation/terminal_agent.py"), "nonce-" + role, "bracketed", filepath.Join(repositorySource(t), "scripts/validation/launch_recipient.py")}
		quoted := []string{}
		for _, part := range parts {
			quoted = append(quoted, launchShellWord(part))
		}
		commands[role] = "exec " + strings.Join(quoted, " ")
	}
	public := &publicApprovalFixture{home: f.home}
	path := launchConfig(t, public, commands)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "worker --- reviewer", "worker --- reviewer:::command_approver_node", 1))
	raw = []byte(strings.Replace(string(raw), "Read the complete archive through EOF.", "LAUNCH_SELECTED_COMMON_POLICY: Read the complete archive through EOF.", 1))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	launched := f.run("", "", "launch-agents", "dev", "--config", path, "--cwd", directory, "--session", "launched-terminals")
	var result launchProtocol
	if json.Unmarshal(launched.stdout, &result) != nil || launched.exit != 0 || !result.Success {
		t.Fatalf("launch: %s %s", launched.stdout, launched.stderr)
	}
	panes := map[string]string{}
	for _, pane := range result.Launch.Panes {
		panes[pane.Role] = pane.Pane
	}
	wait := func(label string, ready func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !ready() {
			if time.Now().After(deadline) {
				for _, role := range []string{"worker", "peer", "reviewer"} {
					if raw, err := os.ReadFile(filepath.Join(evidence, role+".error.txt")); err == nil {
						t.Logf("%s actor error: %s", role, raw)
					}
				}
				t.Fatalf("%s timed out; launched panes may have exited; evidence=%s", label, evidence)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	for _, role := range []string{"worker", "peer", "reviewer"} {
		wait(role+" startup", func() bool { _, err := os.Stat(filepath.Join(evidence, role+".ready.json")); return err == nil })
	}
	env := []string{workspace.LaunchConfigEnvironment + "=" + path}
	sent := f.runWithEnv(panes["worker"], "literal launch body $HOME `code`\nLAST_FULL_BODY_LINE\n", env, "send-heredoc", "--to", "peer")
	if sent.exit != 0 {
		t.Fatalf("send: %s %s", sent.stdout, sent.stderr)
	}
	claims := func(role string) []string {
		paths, _ := filepath.Glob(filepath.Join(evidence, role+".claim.*.json"))
		return paths
	}
	wait("recipient-owned exact reply", func() bool { return len(claims("peer")) == 1 && len(claims("worker")) == 1 })
	var requestID string
	for _, role := range []string{"peer", "worker"} {
		raw, err := os.ReadFile(claims(role)[0])
		if err != nil {
			t.Fatal(err)
		}
		var proof struct {
			Archive string            `json:"archive"`
			Hash    string            `json:"sha256"`
			Config  string            `json:"selected_config"`
			Message workspace.Message `json:"message"`
		}
		if json.Unmarshal(raw, &proof) != nil {
			t.Fatal(string(raw))
		}
		archive, err := os.ReadFile(proof.Archive)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(archive)
		if proof.Hash != fmt.Sprintf("%x", sum) || proof.Config != path || !bytes.Contains(archive, []byte("LAUNCH_SELECTED_COMMON_POLICY")) {
			t.Fatal("full read or config continuity failed", proof)
		}
		if role == "peer" {
			requestID = proof.Message.ID
			if !bytes.HasSuffix(archive, []byte("LAST_FULL_BODY_LINE\n")) {
				t.Fatal("literal body truncated")
			}
		} else if len(proof.Message.Replies) != 1 || proof.Message.Replies[0].MessageID != requestID {
			t.Fatal("reply correlation failed", proof.Message)
		}
	}
	execution := f.runWithEnv(panes["worker"], "printf executed > "+launchShellWord(marker), env, "execute-heredoc", "--reason", "launch protocol check")
	outcome := decodeExecutionOutput(t, execution.stderr)
	if execution.exit != 0 || !outcome.Success || outcome.Executed == nil || !*outcome.Executed {
		t.Fatalf("execution: %+v %s", outcome, execution.stderr)
	}
	wait("recipient-owned approval", func() bool { return len(claims("reviewer")) == 1 })
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "executed" {
		t.Fatal(string(raw), err)
	}
}
