package workspace

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

func TestBuiltinApprovalCannotSwitchSourceWhileWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "postman.md")
	source := config.Source{Path: path, Builtin: true}
	rt := mailRuntime("worker")
	rt.snapshot.Bindings[1].Role = "approver"
	m := mailFixture(t)
	calls := 0
	executor := Executor{Mail: m, Resolve: SourceApprovalResolver(source)}
	executor.wait = func(context.Context) error { return os.WriteFile(path, []byte("## `worker`\nNo approver.\n"), 0o600) }
	executor.run = func(context.Context, string, io.Writer, io.Writer) (int, error) { calls++; return 0, nil }
	got, err := executor.Execute(context.Background(), rt, "never")
	if !errors.Is(err, ErrUnknown) || calls != 0 || got.Status != "unavailable" {
		t.Fatal("builtin changed into advisory execution", got, err, calls)
	}
}

func TestBuiltinStatusUsesRegisteredSourceAcrossCollectorEnvironment(t *testing.T) {
	ctx := context.Background()
	f := newTmuxFixture(t)
	path := filepath.Join(f.home, "chosen-config", "tmux-a2a-postman", "postman.md")
	topology, err := (config.Source{Path: path, Builtin: true}).Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rt := Tmux{Socket: f.socket, Pane: f.pane, Home: f.home, Roles: topology.Nodes, ConfigPath: path, BuiltinConfig: true, ConfigFingerprint: StatusConfigFingerprint(topology)}
	m := mailFixture(t)
	if _, err := m.Claim(ctx, rt, ""); err != nil {
		t.Fatal(err)
	}
	snapshot, err := rt.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TMUX_A2A_POSTMAN_CONFIG", "/does/not/select/this.md")
	_, scope, nodes, err := m.Store.readStatusAttribution(ctx, snapshot.Root, snapshot.Caller.Owner, snapshot.Bindings)
	if err != nil || scope == nil || !scope.Builtin || scope.Config != path || len(nodes) != 8 {
		t.Fatal("collector substituted own source", scope, nodes, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("## `worker`\nChanged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := m.Store.readStatusAttribution(ctx, snapshot.Root, snapshot.Caller.Owner, snapshot.Bindings); err == nil {
		t.Fatal("cached builtin silently adopted appeared config")
	}
}

func TestBuiltinLaunchOverridesStaleServerConfigWithoutPrompt(t *testing.T) {
	for _, preset := range []string{"preset-p", "preset-w"} {
		t.Run(preset, func(t *testing.T) {
			f := newTmuxFixture(t)
			plan := nativeLaunchPlan(t, f, "builtin-"+preset)
			plan.Config = filepath.Join(f.home, "chosen-config", "tmux-a2a-postman", "postman.md")
			plan.BuiltinConfig = true
			topology, err := (config.Source{Path: plan.Config, Builtin: true}).Load(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			plan.Topology = topology
			presets, err := topology.LaunchPresets()
			if err != nil {
				t.Fatal(err)
			}
			plan.Columns = presets[preset].Columns
			plan.Preset = preset
			bin := filepath.Join(f.home, "fixture-bin")
			if err := os.Mkdir(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\nprintf '%s\\n%s\\n' \"${TMUX_A2A_POSTMAN_CONFIG-unset}\" \"$XDG_CONFIG_HOME\" > " + shellQuote(filepath.Join(plan.CWD, "env-")) + "\"$TMUX_PANE\"\nexec sleep 120\n"
			for _, client := range []string{"codex", "claude"} {
				if err := os.WriteFile(filepath.Join(bin, client), []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			// A fixture shell prevents system login startup from replacing PATH and
			// accidentally resolving an installed real client instead of our stub.
			bash, err := exec.LookPath("bash")
			if err != nil {
				t.Fatal(err)
			}
			fixtureShell := filepath.Join(bin, "bash")
			wrapper := "#!/bin/sh\nexport PATH=" + shellQuote(bin+":"+os.Getenv("PATH")) + "\nexec " + shellQuote(bash) + " --noprofile --norc -c \"$2\"\n"
			if err := os.WriteFile(fixtureShell, []byte(wrapper), 0o700); err != nil {
				t.Fatal(err)
			}
			f.run(t, "set-option", "-g", "default-shell", fixtureShell)
			f.run(t, "set-environment", "-g", "PATH", bin+":"+os.Getenv("PATH"))
			f.run(t, "set-environment", "-g", LaunchConfigEnvironment, "/stale/server/postman.md")
			f.run(t, "set-environment", "-g", "XDG_CONFIG_HOME", "/stale/server/xdg")
			result, err := mailFixture(t).Store.LaunchAgents(context.Background(), plan)
			if err != nil {
				t.Fatal(result, err)
			}
			if len(result.Panes) != 7 {
				t.Fatal("incomplete standard launch", result)
			}
			for _, pane := range result.Panes {
				path := filepath.Join(plan.CWD, "env-"+pane.Pane)
				deadline := time.Now().Add(3 * time.Second)
				var raw []byte
				for {
					raw, err = os.ReadFile(path)
					if err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("fixture client did not start", pane, err, f.run(t, "capture-pane", "-p", "-t", pane.Pane))
					}
					time.Sleep(5 * time.Millisecond)
				}
				if string(raw) != "\n"+filepath.Dir(filepath.Dir(plan.Config))+"\n" {
					t.Fatal("stale server config inherited", pane, string(raw))
				}
				if title := f.run(t, "display-message", "-p", "-t", pane.Pane, "#{pane_title}"); title != pane.Role {
					t.Fatal(title, pane)
				}
				screen := f.run(t, "capture-pane", "-p", "-t", pane.Pane)
				if strings.TrimSpace(screen) != "" {
					t.Fatal("launcher injected startup content", screen)
				}
			}
			if _, err := os.Lstat(plan.Config); !os.IsNotExist(err) {
				t.Fatal("launch copied builtin to user config", err)
			}
		})
	}
}

func TestBuiltinDevStartsEditorShellAndOnlyAgentCommand(t *testing.T) {
	f := newTmuxFixture(t)
	plan := nativeLaunchPlan(t, f, "builtin-dev")
	plan.Config = filepath.Join(f.home, "config", "tmux-a2a-postman", "postman.md")
	plan.BuiltinConfig = true
	topology, err := (config.Source{Path: plan.Config, Builtin: true}).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	presets, err := topology.LaunchPresets()
	if err != nil {
		t.Fatal(err)
	}
	plan.Topology, plan.Preset, plan.Columns = topology, "dev", presets["dev"].Columns
	bin := filepath.Join(f.home, "fixture-bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(plan.CWD, "agent-command")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$*\" > " + shellQuote(marker) + "\nexec sleep 120\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	actualBash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	fixtureShell := filepath.Join(bin, "bash")
	wrapper := "#!/bin/sh\nexport PATH=" + shellQuote(bin+":"+os.Getenv("PATH")) + "\ncase \"$1\" in\n  -il) exec " + shellQuote(actualBash) + " --noprofile --norc -i ;;\n  -lic) exec " + shellQuote(actualBash) + " --noprofile --norc -c \"$2\" ;;\nesac\nexit 70\n"
	if err := os.WriteFile(fixtureShell, []byte(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	f.run(t, "set-option", "-g", "default-shell", fixtureShell)
	f.run(t, "set-environment", "-g", "PATH", bin+":"+os.Getenv("PATH"))
	result, err := mailFixture(t).Store.LaunchAgents(context.Background(), plan)
	if err != nil || len(result.Panes) != 2 {
		t.Fatal(result, err)
	}
	left, right := result.Panes[0], result.Panes[1]
	if left.Title != "editor" || left.Role != "" || left.Started || right.Title != "agent" || right.Role != "agent" || !right.Started || left.Column != 0 || right.Column != 1 {
		t.Fatal("wrong dev pane roles or order", result.Panes)
	}
	for _, pane := range result.Panes {
		if got := f.run(t, "display-message", "-p", "-t", pane.Pane, "#{pane_title}"); got != pane.Title {
			t.Fatal("native title mismatch", pane, got)
		}
	}
	if active := f.run(t, "display-message", "-p", "-t", left.Pane, "#{pane_active}"); active != "1" {
		t.Fatal("editor is not focused", active)
	}
	leftWidth, leftErr := strconv.Atoi(f.run(t, "display-message", "-p", "-t", left.Pane, "#{pane_width}"))
	rightWidth, rightErr := strconv.Atoi(f.run(t, "display-message", "-p", "-t", right.Pane, "#{pane_width}"))
	if leftErr != nil || rightErr != nil || leftWidth-rightWidth > 1 || rightWidth-leftWidth > 1 {
		t.Fatal("dev columns are not equal width", leftWidth, rightWidth)
	}
	deadline := time.Now().Add(3 * time.Second)
	var raw []byte
	for {
		raw, err = os.ReadFile(marker)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agent stub did not start", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if string(raw) != "--yolo --no-alt-screen --no-daemon --model gpt-6-sol --config model_reasoning_effort=medium\n" {
		t.Fatal("agent command changed", string(raw))
	}
	shellMarker := filepath.Join(plan.CWD, "editor-shell")
	f.run(t, "send-keys", "-l", "-t", left.Pane, "printf shell-ok > "+shellQuote(shellMarker))
	f.run(t, "send-keys", "-t", left.Pane, "Enter")
	deadline = time.Now().Add(3 * time.Second)
	for {
		raw, err = os.ReadFile(shellMarker)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("editor shell did not accept input", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if string(raw) != "shell-ok" {
		t.Fatal("editor shell output", string(raw))
	}
}
