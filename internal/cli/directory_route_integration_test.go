package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

func TestDirectoryRouteCLIWithoutTree(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	socketDir, err := os.MkdirTemp("/tmp", "pm-cli-route-")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(socketDir, "s")
	home := t.TempDir()
	for key, value := range map[string]string{
		"HOME": home, "ZDOTDIR": home, "XDG_CONFIG_HOME": filepath.Join(home, "config"),
		"XDG_STATE_HOME": filepath.Join(home, "state"), "XDG_DATA_HOME": filepath.Join(home, "data"),
		"XDG_CACHE_HOME": filepath.Join(home, "cache"), "HISTFILE": filepath.Join(home, "history"),
		"TMUX_A2A_POSTMAN_CONFIG": "",
	} {
		t.Setenv(key, value)
	}
	runTmux := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("tmux", append([]string{"-S", socket}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("owned tmux %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", socket, "kill-server").Run()
		if err := os.RemoveAll(socketDir); err != nil {
			t.Error(err)
		}
	})
	parentRoot := filepath.Join(home, "parent")
	childRoot := filepath.Join(parentRoot, "branch", "leaf")
	siblingRoot := filepath.Join(home, "sibling")
	for _, root := range []string{childRoot, siblingRoot} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	parentPane := runTmux("-f", "/dev/null", "new-session", "-d", "-s", "parent", "-c", parentRoot,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	runTmux("select-pane", "-t", parentPane, "-T", "messenger")
	childPane := runTmux("new-session", "-d", "-s", "child", "-c", childRoot,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	runTmux("select-pane", "-t", childPane, "-T", "messenger")
	serverPID := runTmux("display-message", "-p", "-t", parentPane, "#{pid}")
	t.Setenv("TMUX", socket+","+serverPID+",0")

	invoke := func(pane, body string, fn func([]string) error, args ...string) (mailOutput, error) {
		t.Helper()
		t.Setenv("TMUX_PANE", pane)
		input, err := os.CreateTemp(t.TempDir(), "stdin-")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = input.Close() }()
		if _, err := input.WriteString(body); err != nil {
			t.Fatal(err)
		}
		if _, err := input.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		output, err := os.CreateTemp(t.TempDir(), "stdout-")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = output.Close() }()
		oldInput, oldOutput := os.Stdin, os.Stdout
		os.Stdin, os.Stdout = input, output
		defer func() { os.Stdin, os.Stdout = oldInput, oldOutput }()
		callErr := fn(args)
		if _, err := output.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		var result mailOutput
		if err := json.NewDecoder(output).Decode(&result); err != nil {
			t.Fatalf("decode CLI result: %v", err)
		}
		return result, callErr
	}
	claim := func(pane, body string) workspace.Message {
		t.Helper()
		result, err := invoke(pane, "", RunWorkspacePop)
		if err != nil || result.MarkdownPath == nil || *result.MarkdownPath == nil {
			t.Fatalf("CLI POP: %+v %v", result, err)
		}
		archive, err := os.ReadFile(**result.MarkdownPath)
		if err != nil || !strings.Contains(string(archive), body) {
			t.Fatalf("complete claimed archive: %v", err)
		}
		firstLine, _, ok := strings.Cut(string(archive), "\n")
		if !ok || !strings.HasPrefix(firstLine, "<!-- postman ") || !strings.HasSuffix(firstLine, " -->") {
			t.Fatal("claimed archive lacks metadata")
		}
		var msg workspace.Message
		if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(firstLine, "<!-- postman "), " -->")), &msg); err != nil {
			t.Fatal(err)
		}
		return msg
	}
	// A local interface send must not wait for advisory whole-server discovery.
	// Slow any pre-stage enumeration in an owned server with many sessions and
	// record whether the CLI attempted it before a durable draft existed.
	orchestratorPane := runTmux("split-window", "-d", "-t", parentPane, "-c", parentRoot,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	runTmux("select-pane", "-t", orchestratorPane, "-T", "orchestrator")
	for i := 0; i < 16; i++ {
		runTmux("new-session", "-d", "-s", "unrelated-"+strconv.Itoa(i), "-c", home,
			"-P", "-F", "#{pane_id}", "exec sleep 120")
	}
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	shimDir := t.TempDir()
	shim := filepath.Join(shimDir, "tmux")
	logPath := filepath.Join(shimDir, "pre-stage-enumerations")
	script := "#!/bin/sh\nfor arg do\n  if [ \"$arg\" = list-sessions ] && [ -z \"$(find \"$POSTMAN_TEST_STATE\" -type f \\( -path '*/draft/*.md' -o -path '*/sent/*.md' \\) -print -quit)\" ]; then\n    printf 'pre-stage list-sessions\\n' >> \"$POSTMAN_TEST_LOG\"\n    sleep 3\n  fi\ndone\nexec \"$POSTMAN_REAL_TMUX\" \"$@\"\n"
	if err := os.WriteFile(shim, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("POSTMAN_TEST_STATE", filepath.Join(home, "state"))
	t.Setenv("POSTMAN_TEST_LOG", logPath)
	t.Setenv("POSTMAN_REAL_TMUX", realTmux)
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+oldPath)
	local, localErr := invoke(orchestratorPane, "local interface task\n", RunWorkspaceSendHeredoc, "--to", "messenger")
	if err := os.Setenv("PATH", oldPath); err != nil {
		t.Fatal(err)
	}
	if localErr != nil || !local.Success {
		t.Fatalf("local interface send: %+v %v", local, localErr)
	}
	if data, err := os.ReadFile(logPath); err == nil {
		t.Fatalf("local interface send enumerated sessions before staging: %s", data)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	t.Log("local interface send with 18 native sessions: pre-stage full-server enumerations: 0")
	_ = claim(parentPane, "local interface task\n")
	if result, err := invoke(childPane, "", RunWorkspacePop); err != nil || !result.Success {
		t.Fatalf("register child configuration: %+v %v", result, err)
	}
	parentName, err := workspace.Name("~/parent")
	if err != nil {
		t.Fatal(err)
	}
	parentConfig := filepath.Join(home, "state", "tmux-a2a-postman", parentName, "runtime",
		"status-config-p"+strings.TrimPrefix(parentPane, "%")+".json")
	registered, err := os.ReadFile(parentConfig)
	if err != nil {
		t.Fatal(err)
	}
	type statusConfig struct {
		Version     int               `json:"version"`
		Binding     workspace.Binding `json:"binding"`
		Path        string            `json:"path"`
		Fingerprint string            `json:"fingerprint"`
		Builtin     bool              `json:"builtin"`
	}
	var prior statusConfig
	if err := json.Unmarshal(registered, &prior); err != nil {
		t.Fatal(err)
	}
	currentFingerprint := prior.Fingerprint
	if prior.Version != 3 || !prior.Builtin {
		t.Fatal("normal built-in operation did not register v3", prior)
	}
	prior.Version = 2
	prior.Fingerprint = "sha256:819dd379f45c6df17bc9a2129648c769e68449804a73ff16a51f3f59052aa3e4"
	writePrior := func() {
		t.Helper()
		data, err := json.Marshal(prior)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(parentConfig, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writePrior()
	entries, err := os.ReadDir(filepath.Dir(parentConfig))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "status-config-p") || entry.Name() == filepath.Base(parentConfig) {
			continue
		}
		path := filepath.Join(filepath.Dir(parentConfig), entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var other statusConfig
		if err := json.Unmarshal(data, &other); err != nil {
			t.Fatal(err)
		}
		other.Version = 2
		other.Fingerprint = prior.Fingerprint
		data, err = json.Marshal(other)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if result, err := invoke(childPane, "prior builtin parent must reject\n", RunWorkspaceSendHeredoc, "--to", "parent:messenger"); err == nil || result.Success || result.RecoveryMessageID != "" {
		t.Fatalf("CLI prior-built-in parent admitted mail: %+v %v", result, err)
	}
	if result, err := invoke(parentPane, "", RunWorkspacePop); err != nil || !result.Success {
		t.Fatalf("normal parent operation failed to re-register: %+v %v", result, err)
	}
	if result, err := invoke(orchestratorPane, "", RunWorkspacePop); err != nil || !result.Success {
		t.Fatalf("normal orchestrator operation failed to re-register: %+v %v", result, err)
	}
	if result, err := invoke(childPane, "re-registered builtin parent\n", RunWorkspaceSendHeredoc, "--to", "parent:messenger"); err != nil || !result.Success {
		t.Fatalf("CLI re-registered parent send: %+v %v", result, err)
	}
	archiveResult, err := invoke(parentPane, "", RunWorkspacePop)
	if err != nil || archiveResult.MarkdownPath == nil || *archiveResult.MarkdownPath == nil {
		t.Fatalf("CLI prior-built-in parent POP: %+v %v", archiveResult, err)
	}
	archive, err := os.ReadFile(**archiveResult.MarkdownPath)
	if err != nil || !strings.Contains(string(archive), "re-registered builtin parent\n") ||
		!strings.Contains(string(archive), "<!-- postman recipient context v1 -->") ||
		!strings.Contains(string(archive), "### messenger instructions") {
		t.Fatalf("CLI prior-built-in parent archive context: %v", err)
	}
	var refreshed statusConfig
	data, err := os.ReadFile(parentConfig)
	if err != nil || json.Unmarshal(data, &refreshed) != nil || refreshed.Version != 3 || refreshed.Fingerprint != currentFingerprint {
		t.Fatalf("normal parent operation did not write structural v3 registration: %v %+v", err, refreshed)
	}
	orchestratorConfig := filepath.Join(filepath.Dir(parentConfig),
		"status-config-p"+strings.TrimPrefix(orchestratorPane, "%")+".json")
	var reRegistered statusConfig
	data, err = os.ReadFile(orchestratorConfig)
	if err != nil || json.Unmarshal(data, &reRegistered) != nil || reRegistered.Version != 3 || reRegistered.Fingerprint != currentFingerprint {
		t.Fatalf("other parent registration did not refresh through normal operation: %v %+v", err, reRegistered)
	}
	if result, err := invoke(childPane, "mixed builtin parent second\n", RunWorkspaceSendHeredoc, "--to", "parent:messenger"); err != nil || !result.Success {
		t.Fatalf("CLI mixed-built-in parent second send: %+v %v", result, err)
	}
	secondResult, err := invoke(parentPane, "", RunWorkspacePop)
	if err != nil || secondResult.MarkdownPath == nil || *secondResult.MarkdownPath == nil {
		t.Fatalf("CLI mixed-built-in parent second POP: %+v %v", secondResult, err)
	}
	secondArchive, err := os.ReadFile(**secondResult.MarkdownPath)
	if err != nil || !strings.Contains(string(secondArchive), "mixed builtin parent second\n") ||
		!strings.Contains(string(secondArchive), "<!-- postman recipient context v1 -->") ||
		!strings.Contains(string(secondArchive), "### messenger instructions") {
		t.Fatalf("CLI mixed-built-in parent second archive context: %v", err)
	}
	bindingPath := filepath.Join(filepath.Dir(parentConfig), "binding.json")
	bindingData, err := os.ReadFile(bindingPath)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Version  int                 `json:"version"`
		Owner    workspace.Owner     `json:"owner"`
		Bindings []workspace.Binding `json:"bindings"`
	}
	if err := json.Unmarshal(bindingData, &saved); err != nil {
		t.Fatal(err)
	}
	changed := false
	for i := range saved.Bindings {
		if saved.Bindings[i].Owner.Pane == parentPane {
			saved.Bindings[i].PanePID = "999999"
			changed = true
		}
	}
	if !changed {
		t.Fatal("parent messenger binding absent")
	}
	bindingData, err = json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bindingPath, bindingData, 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := invoke(childPane, "stale parent must reject\n", RunWorkspaceSendHeredoc, "--to", "parent:messenger"); err == nil || result.Success || result.RecoveryMessageID != "" {
		t.Fatalf("CLI stale parent pane admitted mail: %+v %v", result, err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "status-config-p") {
			continue
		}
		path := filepath.Join(filepath.Dir(parentConfig), entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var current statusConfig
		if err := json.Unmarshal(data, &current); err != nil {
			t.Fatal(err)
		}
		current.Fingerprint = currentFingerprint
		data, err = json.Marshal(current)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, direction := range []struct {
		name, target, sourcePane, targetPane string
	}{
		{"parent to child", "child:messenger", parentPane, childPane},
		{"child to parent", "parent:messenger", childPane, parentPane},
	} {
		t.Run(direction.name, func(t *testing.T) {
			body := "directory task: " + direction.name + "\n"
			if result, err := invoke(direction.sourcePane, body, RunWorkspaceSendHeredoc, "--to", direction.target); err != nil || !result.Success {
				t.Fatalf("CLI task send: %+v %v", result, err)
			}
			request := claim(direction.targetPane, body)
			if !request.NeedsReply || request.FromSession+":messenger" == direction.target ||
				request.ToSession+":messenger" != direction.target {
				t.Fatalf("task owner/route: %+v", request)
			}
			answerBody := "directory answer: " + direction.name + "\n"
			if result, err := invoke(direction.targetPane, answerBody, RunWorkspaceSendHeredoc, "--to", request.ID); err != nil || !result.Success {
				t.Fatalf("CLI exact reply send: %+v %v", result, err)
			}
			answer := claim(direction.sourcePane, answerBody)
			if len(answer.Replies) != 1 || answer.Replies[0].MessageID != request.ID ||
				answer.Replies[0].InputRequestID != request.InputRequestID || answer.NeedsReply ||
				answer.ToSession != request.FromSession || answer.FromSession != request.ToSession {
				t.Fatalf("claimed exact reply correlation: request=%+v answer=%+v", request, answer)
			}
		})
	}
	removeProvenance := func(root, pane string) {
		t.Helper()
		canonical, err := workspace.CanonicalRoot(root, home)
		if err != nil {
			t.Fatal(err)
		}
		name, err := workspace.Name(canonical)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(home, "state", "tmux-a2a-postman", name, "runtime",
			"status-config-p"+strings.TrimPrefix(pane, "%")+".json")
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	betaRoot := filepath.Join(parentRoot, "beta")
	if err := os.MkdirAll(betaRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	betaPane := runTmux("new-session", "-d", "-s", "beta", "-c", betaRoot,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	runTmux("select-pane", "-t", betaPane, "-T", "messenger")
	if result, err := invoke(betaPane, "", RunWorkspacePop); err != nil || !result.Success {
		t.Fatalf("register beta: %+v %v", result, err)
	}
	removeProvenance(betaRoot, betaPane)
	if result, err := invoke(parentPane, "off-route beta must not veto alpha\n", RunWorkspaceSendHeredoc, "--to", "child:messenger"); err != nil || !result.Success {
		t.Fatalf("valid child send blocked by beta: %+v %v", result, err)
	}
	request := claim(childPane, "off-route beta must not veto alpha\n")
	if result, err := invoke(childPane, "exact alpha reply with beta unknown\n", RunWorkspaceSendHeredoc, "--to", request.ID); err != nil || !result.Success {
		t.Fatalf("valid exact reply blocked by beta: %+v %v", result, err)
	}
	answer := claim(parentPane, "exact alpha reply with beta unknown\n")
	if len(answer.Replies) != 1 || answer.Replies[0].MessageID != request.ID {
		t.Fatalf("off-route beta changed exact reply correlation: %+v", answer)
	}
	if result, err := invoke(parentPane, "unknown beta reject\n", RunWorkspaceSendHeredoc, "--to", "beta:messenger"); err == nil || result.Success || result.RecoveryMessageID != "" {
		t.Fatalf("unknown beta became a route or durable draft: %+v %v", result, err)
	}

	siblingPane := runTmux("new-session", "-d", "-s", "sibling", "-c", siblingRoot,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	runTmux("select-pane", "-t", siblingPane, "-T", "messenger")
	if result, err := invoke(parentPane, "sibling reject\n", RunWorkspaceSendHeredoc, "--to", "sibling:messenger"); err == nil || result.Success {
		t.Fatalf("CLI accepted sibling route: %+v %v", result, err)
	}
	middlePane := runTmux("new-session", "-d", "-s", "middle", "-c", filepath.Join(parentRoot, "branch"),
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	runTmux("select-pane", "-t", middlePane, "-T", "messenger")
	if result, err := invoke(middlePane, "", RunWorkspacePop); err != nil || !result.Success {
		t.Fatalf("register intermediate configuration: %+v %v", result, err)
	}
	if result, err := invoke(parentPane, "skipped-level reject\n", RunWorkspaceSendHeredoc, "--to", "child:messenger"); err == nil || result.Success {
		t.Fatalf("CLI accepted intermediate shortcut: %+v %v", result, err)
	}
	removeProvenance(filepath.Join(parentRoot, "branch"), middlePane)
	if result, err := invoke(parentPane, "unknown intermediate reject\n", RunWorkspaceSendHeredoc, "--to", "child:messenger"); err == nil || result.Success || result.RecoveryMessageID != "" {
		t.Fatalf("unknown intermediate admitted mail before durable stage: %+v %v", result, err)
	}
}
