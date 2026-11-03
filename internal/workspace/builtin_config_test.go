package workspace

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	for _, client := range []string{"codex", "claude"} {
		t.Run(client, func(t *testing.T) {
			f := newTmuxFixture(t)
			plan := nativeLaunchPlan(t, f, "builtin-"+client)
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
			plan.Columns = presets[client].Columns
			plan.Preset = client
			bin := filepath.Join(f.home, "fixture-bin")
			if err := os.Mkdir(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\nprintf '%s\\n%s\\n' \"${TMUX_A2A_POSTMAN_CONFIG-unset}\" \"$XDG_CONFIG_HOME\" > " + shellQuote(filepath.Join(plan.CWD, "env-")) + "\"$TMUX_PANE\"\nexec sleep 120\n"
			if err := os.WriteFile(filepath.Join(bin, client), []byte(script), 0o700); err != nil {
				t.Fatal(err)
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
			if len(result.Panes) != 8 {
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
