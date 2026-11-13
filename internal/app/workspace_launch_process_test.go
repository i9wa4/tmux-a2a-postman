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
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
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

func TestBuiltinPresetsLaunchFromPaneAndCloseOnlyTheirWindows(t *testing.T) {
	f := newPublicApprovalFixture(t)
	// Keep tmux's automatic process-name updates from changing window names
	// while checking whether the launcher itself renamed an existing window.
	f.tmux("set-option", "-g", "automatic-rename", "off")
	originalSession := f.tmux("display-message", "-p", "-t", f.worker, "#{session_id}")
	originalWindow := f.tmux("display-message", "-p", "-t", f.worker, "#{window_id}")
	// This extra window represents unrelated work and must survive each trial.
	sentinelPane := f.tmux("new-window", "-d", "-t", "work", "-c", filepath.Join(f.home, "work"), "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.tmux("select-pane", "-t", sentinelPane, "-T", "sentinel")
	unrelated := f.tmux("display-message", "-p", "-t", sentinelPane, "#{window_id}")
	f.tmux("select-pane", "-t", f.worker, "-T", "worker")
	snapshot := func(pane string) []string {
		fields := []string{"pane_id", "window_id", "pane_title", "pane_current_path", "pane_pid", "pane_current_command", "pane_left", "pane_top", "pane_width", "pane_height", "pane_active"}
		values := make([]string, 0, len(fields))
		for _, field := range fields {
			values = append(values, f.tmux("display-message", "-p", "-t", pane, "#{"+field+"}"))
		}
		return values
	}
	preserved := map[string][]string{f.worker: snapshot(f.worker), f.peer: snapshot(f.peer), sentinelPane: snapshot(sentinelPane)}
	windowSnapshot := func(window string) []string {
		fields := []string{"window_id", "window_name", "window_layout", "window_active"}
		values := make([]string, 0, len(fields))
		for _, field := range fields {
			values = append(values, f.tmux("display-message", "-p", "-t", window, "#{"+field+"}"))
		}
		return values
	}
	preservedWindows := map[string][]string{originalWindow: windowSnapshot(originalWindow), unrelated: windowSnapshot(unrelated)}
	assertPreserved := func(t *testing.T) {
		t.Helper()
		for pane, before := range preserved {
			if after := snapshot(pane); !slices.Equal(after, before) {
				t.Fatalf("existing pane changed: %s: before=%q after=%q", pane, before, after)
			}
		}
		for window, before := range preservedWindows {
			if after := windowSnapshot(window); !slices.Equal(after, before) {
				t.Fatalf("existing window changed: %s: before=%q after=%q", window, before, after)
			}
		}
	}
	windows := func() []string {
		ids := strings.Split(f.tmux("list-windows", "-t", "work", "-F", "#{window_id}"), "\n")
		slices.Sort(ids)
		return ids
	}
	assertWindows := func(t *testing.T, want ...string) {
		t.Helper()
		slices.Sort(want)
		if got := windows(); !slices.Equal(got, want) {
			t.Fatalf("native window IDs: got=%v want=%v", got, want)
		}
	}
	assertWindows(t, originalWindow, unrelated)
	invalidConfig := filepath.Join(f.home, "invalid-override.md")
	if err := os.WriteFile(invalidConfig, []byte("not a Postman configuration\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_A2A_POSTMAN_CONFIG", invalidConfig)
	f.tmux("set-environment", "-g", "TMUX_A2A_POSTMAN_CONFIG", invalidConfig)
	bin := filepath.Join(f.home, "fixture-bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\nprintf '%s\\000' \"$0\" \"${TMUX_A2A_POSTMAN_CONFIG-unset}\" \"${XDG_CONFIG_HOME-unset}\" \"$PWD\" \"$@\" > \"$PWD/started-$TMUX_PANE\"\nprintf x >> \"$PWD/count-$TMUX_PANE\"\nexec sleep 120\n"
	for _, client := range []string{"codex", "claude"} {
		if err := os.WriteFile(filepath.Join(bin, client), []byte(stub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := "#!/bin/sh\nexport PATH=" + launchShellWord(bin+":"+os.Getenv("PATH")) + "\nexport HISTFILE=/dev/null\ncase \"$1\" in\n  -il) exec " + launchShellWord(bash) + " --noprofile --norc -i ;;\n  -lic) exec " + launchShellWord(bash) + " --noprofile --norc -c \"$2\" ;;\nesac\nexit 70\n"
	if err := os.WriteFile(filepath.Join(bin, "bash"), []byte(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	f.tmux("set-option", "-g", "default-shell", filepath.Join(bin, "bash"))
	f.tmux("set-environment", "-g", "PATH", bin+":"+os.Getenv("PATH"))
	topology, err := (config.Source{Path: filepath.Join(f.home, ".config", "tmux-a2a-postman", "postman.md"), Builtin: true}).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	presets, err := topology.LaunchPresets()
	if err != nil {
		t.Fatal(err)
	}
	secondaryRole := presets["preset-p"].Columns[0].Panes[3].Role
	if secondaryRole == "" || presets["preset-w"].Columns[0].Panes[3].Role != secondaryRole {
		t.Fatal("standard preset role order differs")
	}
	reviewRole := presets["preset-p"].Columns[1].Panes[2].Role
	if reviewRole == "" || presets["preset-w"].Columns[1].Panes[2].Role != reviewRole {
		t.Fatal("standard preset review role differs")
	}

	codex := func(model, effort string) string {
		return "--yolo --no-alt-screen --no-daemon --model " + model + " --config model_reasoning_effort=" + effort
	}
	expected := map[string]map[string]string{
		"dev": {"agent": codex("gpt-6-sol", "medium")},
		"preset-p": {
			"messenger": codex("gpt-6-luna", "high"), "orchestrator": codex("gpt-6-sol", "high"),
			"worker": codex("gpt-6-sol", "medium"), secondaryRole: codex("gpt-6-sol", "medium"),
			"approver": codex("gpt-6-luna", "high"), "guardian": codex("gpt-6-sol", "high"),
			reviewRole: codex("gpt-5.6-sol", "high"),
		},
		"preset-w": {
			"messenger": codex("gpt-6-luna", "high"), "orchestrator": codex("gpt-6-sol", "medium"),
			"worker":      codex("gpt-6-luna", "high"),
			secondaryRole: "--allow-dangerously-skip-permissions --dangerously-skip-permissions --model sonnet --effort medium",
			"approver":    codex("gpt-6-luna", "high"), "guardian": codex("gpt-6-sol", "high"),
			reviewRole: "--allow-dangerously-skip-permissions --dangerously-skip-permissions --model opus[1m] --effort medium",
		},
	}
	for _, preset := range []string{"dev", "preset-p", "preset-w"} {
		t.Run(preset, func(t *testing.T) {
			cwd := filepath.Join(f.home, "trial-"+preset)
			if err := os.Mkdir(cwd, 0o700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			beforeWindows := windows()
			cmd := f.child(ctx, f.worker, "launch-agents", preset, "--cwd", cwd)
			// The parent and tmux server intentionally contain a hostile override.
			// Clear it only for the launched CLI so the missing isolated default
			// selects the embedded presets and no real user config is consulted.
			cmd.Env = append(cmd.Env, "TMUX_A2A_POSTMAN_CONFIG=")
			var out, diagnostics bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &diagnostics
			err := cmd.Run()
			var got launchProtocol
			if json.Unmarshal(out.Bytes(), &got) != nil || err != nil || !got.Success {
				t.Fatalf("launch: %s %s (%v)", out.Bytes(), diagnostics.Bytes(), err)
			}
			if got.Launch.Readiness != "not established; created processes are not authenticated or ready agents" {
				t.Fatalf("launch must report process start without authenticated readiness: %q", got.Launch.Readiness)
			}
			afterWindows := windows()
			if len(afterWindows) != len(beforeWindows)+1 {
				t.Fatalf("expected exactly one new invoking-session window: before=%v after=%v receipt=%+v", beforeWindows, afterWindows, got.Launch)
			}
			for _, existing := range beforeWindows {
				if !slices.Contains(afterWindows, existing) {
					t.Fatalf("existing window disappeared during launch: before=%v after=%v receipt=%+v", beforeWindows, afterWindows, got.Launch)
				}
			}
			window := ""
			for _, candidate := range afterWindows {
				if !slices.Contains(beforeWindows, candidate) {
					if window != "" {
						t.Fatalf("multiple new windows during launch: before=%v after=%v receipt=%+v", beforeWindows, afterWindows, got.Launch)
					}
					window = candidate
				}
			}
			if window == "" || window == originalWindow || window == unrelated || got.Launch.Window != window || got.Launch.SessionID != originalSession {
				t.Fatalf("launch receipt does not name the new invoking-session window: before=%v after=%v receipt=%+v", beforeWindows, afterWindows, got.Launch)
			}
			defer f.tmux("kill-window", "-t", window)
			assertPreserved(t)
			assertWindows(t, originalWindow, unrelated, window)
			if sessions := f.tmux("list-sessions", "-F", "#{session_name}"); sessions != "work" {
				t.Fatalf("created another session: %s", sessions)
			}
			if len(got.Launch.Panes) != len(expected[preset])+(map[bool]int{true: 1, false: 0}[preset == "dev"]) {
				t.Fatalf("incomplete preset: %+v", got.Launch.Panes)
			}
			seen := map[string]bool{}
			lefts, tops, widths := map[int]int{}, map[int]int{}, map[int]int{}
			for index, pane := range got.Launch.Panes {
				wantRole := presets[preset].Columns[pane.Column].Panes[pane.Row].Role
				if pane.Role != wantRole {
					t.Fatalf("built-in role mismatch at index %d: got=%q want=%q", index, pane.Role, wantRole)
				}
				if nativeWindow := f.tmux("display-message", "-p", "-t", pane.Pane, "#{window_id}"); nativeWindow != window {
					t.Fatalf("pane outside trial window: %+v %s", pane, nativeWindow)
				}
				if nativeTitle := f.tmux("display-message", "-p", "-t", pane.Pane, "#{pane_title}"); nativeTitle != pane.Title {
					t.Fatalf("title mismatch: %+v %s", pane, nativeTitle)
				}
				if nativeCWD := f.tmux("display-message", "-p", "-t", pane.Pane, "#{pane_current_path}"); nativeCWD != cwd {
					t.Fatalf("cwd mismatch: %+v %s", pane, nativeCWD)
				}
				left, leftErr := strconv.Atoi(f.tmux("display-message", "-p", "-t", pane.Pane, "#{pane_left}"))
				top, topErr := strconv.Atoi(f.tmux("display-message", "-p", "-t", pane.Pane, "#{pane_top}"))
				if leftErr != nil || topErr != nil {
					t.Fatalf("native pane coordinates: %+v: %v %v", pane, leftErr, topErr)
				}
				if previous, ok := lefts[pane.Column]; ok && previous != left {
					t.Fatalf("column shifted: %+v: %d != %d", pane, left, previous)
				}
				lefts[pane.Column] = left
				if previous, ok := tops[pane.Column]; ok && top <= previous {
					t.Fatalf("pane rows out of order: %+v: %d <= %d", pane, top, previous)
				}
				tops[pane.Column] = top
				if preset == "dev" {
					width, err := strconv.Atoi(f.tmux("display-message", "-p", "-t", pane.Pane, "#{pane_width}"))
					if err != nil {
						t.Fatalf("native pane width for %+v: %v", pane, err)
					}
					widths[pane.Column] = width
				}
				if preset == "dev" && pane.Title == "editor" {
					if pane.Role != "" || pane.Started || pane.Column != 0 || f.tmux("display-message", "-p", "-t", pane.Pane, "#{pane_active}") != "1" {
						t.Fatalf("editor pane: %+v", pane)
					}
					shellMarker := filepath.Join(cwd, "editor-shell")
					f.tmux("send-keys", "-l", "-t", pane.Pane, "printf shell-ok > "+launchShellWord(shellMarker))
					f.tmux("send-keys", "-t", pane.Pane, "Enter")
					deadline := time.Now().Add(3 * time.Second)
					for {
						if raw, err := os.ReadFile(shellMarker); err == nil && string(raw) == "shell-ok" {
							break
						}
						if time.Now().After(deadline) {
							t.Fatal("editor shell did not accept input")
						}
						time.Sleep(10 * time.Millisecond)
					}
					continue
				}
				if (preset == "dev" && pane.Column != 1) || (preset != "dev" && pane.Column != map[bool]int{true: 0, false: 1}[len(seen) < 4]) {
					t.Fatalf("unexpected pane column: %+v", pane)
				}
				want, ok := expected[preset][pane.Role]
				if !ok || seen[pane.Role] || !pane.Started || pane.Title != pane.Role {
					t.Fatalf("unexpected agent pane: %+v", pane)
				}
				seen[pane.Role] = true
				marker := filepath.Join(cwd, "started-"+pane.Pane)
				deadline := time.Now().Add(3 * time.Second)
				var raw []byte
				for {
					raw, err = os.ReadFile(marker)
					if err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("client did not start: %+v: %v; screen: %s", pane, err, f.tmux("capture-pane", "-p", "-t", pane.Pane))
					}
					time.Sleep(10 * time.Millisecond)
				}
				parts := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
				client := "codex"
				if preset == "preset-w" && (pane.Role == secondaryRole || pane.Role == reviewRole) {
					client = "claude"
				}
				wantParts := append([]string{filepath.Join(bin, client), "", filepath.Join(f.home, ".config"), cwd}, strings.Fields(want)...)
				if !slices.Equal(parts, wantParts) {
					t.Fatalf("client/config/argv/cwd mismatch for %s: got=%q want=%q", pane.Role, parts, wantParts)
				}
				countPath := filepath.Join(cwd, "count-"+pane.Pane)
				for {
					count, err := os.ReadFile(countPath)
					if err == nil {
						if string(count) != "x" {
							t.Fatalf("client invocation count for %s: %q", pane.Role, count)
						}
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("client invocation count missing for %s: %v", pane.Role, err)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			if len(seen) != len(expected[preset]) {
				t.Fatalf("missing roles: %v", seen)
			}
			if lefts[0] >= lefts[1] {
				t.Fatalf("columns not left to right: %v", lefts)
			}
			if preset == "dev" {
				// An odd tmux window width gives one column the rounding cell.
				difference := widths[0] - widths[1]
				if len(widths) != 2 || difference < -1 || difference > 1 {
					t.Fatalf("dev columns are not equal width: %v", widths)
				}
			}
			paneIDs := make([]string, 0, len(got.Launch.Panes))
			for _, pane := range got.Launch.Panes {
				paneIDs = append(paneIDs, pane.Pane)
			}
			slices.Sort(paneIDs)
			native := strings.Split(f.tmux("list-panes", "-t", window, "-F", "#{pane_id}"), "\n")
			slices.Sort(native)
			if !slices.Equal(native, paneIDs) {
				t.Fatalf("trial pane IDs: got=%v want=%v", native, paneIDs)
			}
			assertPreserved(t)
			assertWindows(t, originalWindow, unrelated, window)
		})
		assertPreserved(t)
		assertWindows(t, originalWindow, unrelated)
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
