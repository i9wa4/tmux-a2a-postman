package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

func nativeLaunchPlan(t *testing.T, f tmuxFixture, name string) LaunchPlan {
	t.Helper()
	cwd := filepath.Join(f.home, name)
	if err := os.Mkdir(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	command := func(role string) string {
		value := fmt.Sprintf("printf once\\n >> %s; exec sleep 120", shellQuote(filepath.Join(cwd, role+".started")))
		raw, _ := json.Marshal(value)
		return string(raw)
	}
	source := "---\npresets:\n  dev:\n    columns:\n      - panes:\n          - role: first\n            command: " + command("first") + "\n      - panes:\n          - role: second\n            command: " + command("second") + "\n---\n## `edges`\n```mermaid\ngraph LR\nfirst --- second\n```\n"
	path := filepath.Join(cwd, "postman.md")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	topology, err := config.LoadWorkspaceTopology(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	presets, err := topology.LaunchPresets()
	if err != nil {
		t.Fatal(err)
	}
	return LaunchPlan{Preset: "dev", Config: path, CWD: cwd, Workspace: "~/" + name, Socket: f.socket, Session: name, Columns: presets["dev"].Columns, Home: f.home, Topology: topology}
}

func TestConcurrentLaunchAgentsStartCommandsOnce(t *testing.T) {
	f := newTmuxFixture(t)
	plan := nativeLaunchPlan(t, f, "parallel-team")
	store := mailFixture(t).Store
	type outcome struct {
		result LaunchResult
		err    error
	}
	start, results := make(chan struct{}), make(chan outcome, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { <-start; r, err := store.LaunchAgents(context.Background(), plan); results <- outcome{r, err} })
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for result := range results {
		if result.err == nil {
			successes++
			if len(result.result.Panes) != 2 {
				t.Fatal(result)
			}
		}
	}
	if successes != 1 {
		t.Fatal("launch duplicated or failed", successes)
	}
	for _, role := range []string{"first", "second"} {
		deadline := time.Now().Add(3 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(plan.CWD, role+".started")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("created command never reached fixture readiness", role)
			}
			time.Sleep(5 * time.Millisecond)
		}
		raw, err := os.ReadFile(filepath.Join(plan.CWD, role+".started"))
		if err != nil || strings.Count(string(raw), "once") != 1 {
			t.Fatal(role, string(raw), err)
		}
	}
	if f.run(t, "display-message", "-p", "-t", f.pane, "#{pane_title}") != "worker" {
		t.Fatal("borrowed sentinel changed")
	}
}

func TestLaunchAgentsFailureAndCancellationCleanOnlyOwnedSession(t *testing.T) {
	for _, mode := range []string{"failure", "cancel", "renamed"} {
		t.Run(mode, func(t *testing.T) {
			f := newTmuxFixture(t)
			plan := nativeLaunchPlan(t, f, "failed-team")
			store := mailFixture(t).Store
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store.beforePublish = func() error {
				if mode == "cancel" {
					cancel()
					return ctx.Err()
				}
				if mode == "renamed" {
					f.run(t, "rename-session", "-t", plan.Session, "renamed-owned-team")
				}
				return errors.New("injected registration failure")
			}
			result, err := store.LaunchAgents(ctx, plan)
			if err == nil || result.Cleanup != "owned session removed" {
				t.Fatal("unverified cleanup", result, err)
			}
			sessions := f.run(t, "list-sessions", "-F", "#{session_name}")
			if sessions != "one" {
				t.Fatal("owned launch leaked or sentinel changed", sessions)
			}
			for _, role := range []string{"first", "second"} {
				if _, err := os.Stat(filepath.Join(plan.CWD, role+".started")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("command ran before complete admission", role, err)
				}
			}
			// The recorded clean, pre-command failure permits a new explicit attempt;
			// no started/uncertain attempt is ever replayed automatically.
			if mode == "failure" {
				store.beforePublish = nil
				again, err := store.LaunchAgents(context.Background(), plan)
				if err != nil || len(again.Panes) != 2 {
					t.Fatal(again, err)
				}
			}
		})
	}
}

func TestLaunchAgentsWindowFailureCleansOnlyOwnedWindow(t *testing.T) {
	f := newTmuxFixture(t)
	plan := nativeLaunchPlan(t, f, "failed-window-team")
	plan.Session = "one"
	plan.WindowSession = f.run(t, "display-message", "-p", "-t", f.pane, "#{session_id}")
	store := mailFixture(t).Store
	store.beforePublish = func() error { return errors.New("injected registration failure") }
	result, err := store.LaunchAgents(context.Background(), plan)
	if err == nil || result.Cleanup != "owned window removed" {
		t.Fatal("unverified window cleanup", result, err)
	}
	if sessions := f.run(t, "list-sessions", "-F", "#{session_name}"); sessions != "one" {
		t.Fatal("borrowed session changed", sessions)
	}
	if panes := f.run(t, "list-panes", "-a", "-F", "#{pane_id}"); panes != f.pane {
		t.Fatal("borrowed pane changed or window leaked", panes)
	}
	for _, role := range []string{"first", "second"} {
		if _, err := os.Stat(filepath.Join(plan.CWD, role+".started")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("command ran before admission", role, err)
		}
	}
}

func TestLaunchAgentsRejectCollisionBeforeCreatingPanes(t *testing.T) {
	f := newTmuxFixture(t)
	plan := nativeLaunchPlan(t, f, "collision-team")
	plan.Session = "one"
	store := mailFixture(t).Store
	result, err := store.LaunchAgents(context.Background(), plan)
	if err == nil || len(result.Panes) != 0 {
		t.Fatal(result, err)
	}
	if panes := f.run(t, "list-panes", "-a", "-F", "#{pane_id}"); panes != f.pane {
		t.Fatal("collision modified existing panes", panes)
	}
}

func TestLaunchAgentsLostCreationResponseCleansByNativeOwnershipToken(t *testing.T) {
	f := newTmuxFixture(t)
	plan := nativeLaunchPlan(t, f, "lost-response-team")
	store := mailFixture(t).Store
	real, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	proxy := t.TempDir()
	source := "#!/bin/sh\ncase \"$*\" in *new-session*) " + shellQuote(real) + " \"$@\" >/dev/null; exit 74;; esac\nexec " + shellQuote(real) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(proxy, "tmux"), []byte(source), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", proxy+string(os.PathListSeparator)+os.Getenv("PATH"))
	result, err := store.LaunchAgents(context.Background(), plan)
	if err == nil || result.SessionID == "" || result.Cleanup != "owned session removed" {
		t.Fatal("lost creation response left unverified resources", result, err)
	}
	if sessions := f.run(t, "list-sessions", "-F", "#{session_name}"); sessions != "one" {
		t.Fatal("cleanup changed sentinel or leaked created session", sessions)
	}
}

func TestLaunchAgentsLostWindowResponseCleansByNativeOwnershipToken(t *testing.T) {
	f := newTmuxFixture(t)
	plan := nativeLaunchPlan(t, f, "lost-window-response-team")
	plan.Session = "one"
	plan.WindowSession = f.run(t, "display-message", "-p", "-t", f.pane, "#{session_id}")
	store := mailFixture(t).Store
	real, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	proxy := t.TempDir()
	source := "#!/bin/sh\ncase \"$*\" in *new-window*) " + shellQuote(real) + " \"$@\" >/dev/null; exit 74;; esac\nexec " + shellQuote(real) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(proxy, "tmux"), []byte(source), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", proxy+string(os.PathListSeparator)+os.Getenv("PATH"))
	result, err := store.LaunchAgents(context.Background(), plan)
	if err == nil || result.Window == "" || result.Cleanup != "owned window removed" {
		t.Fatal("lost window creation response left unverified resources", result, err)
	}
	if sessions := f.run(t, "list-sessions", "-F", "#{session_name}"); sessions != "one" {
		t.Fatal("borrowed session changed", sessions)
	}
	if panes := f.run(t, "list-panes", "-a", "-F", "#{pane_id}"); panes != f.pane {
		t.Fatal("borrowed pane changed or window leaked", panes)
	}
}

func TestLaunchAgentsPartialCommandFailureIsNotReplayed(t *testing.T) {
	f := newTmuxFixture(t)
	plan := nativeLaunchPlan(t, f, "partial-team")
	store := mailFixture(t).Store
	real, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	proxy := t.TempDir()
	counter := filepath.Join(proxy, "count")
	marker := filepath.Join(plan.CWD, "first.started")
	source := fmt.Sprintf(`#!/bin/sh
case "$*" in
 *respawn-pane*)
  n=0
  if [ -f %s ]; then n=$(cat %s); fi
  n=$((n+1)); printf '%%s' "$n" > %s
  if [ "$n" -eq 2 ]; then
   i=0
   while [ ! -f %s ] && [ "$i" -lt 200 ]; do i=$((i+1)); sleep 0.01; done
   exit 75
  fi ;;
esac
exec %s "$@"
`, shellQuote(counter), shellQuote(counter), shellQuote(counter), shellQuote(marker), shellQuote(real))
	if err := os.WriteFile(filepath.Join(proxy, "tmux"), []byte(source), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", proxy+string(os.PathListSeparator)+os.Getenv("PATH"))
	result, err := store.LaunchAgents(context.Background(), plan)
	if err == nil || result.Cleanup != "owned session removed" || len(result.Panes) != 2 || !result.Panes[0].Started || result.Panes[1].Started {
		t.Fatal(result, err)
	}
	raw, err := os.ReadFile(marker)
	if err != nil || strings.Count(string(raw), "once") != 1 {
		t.Fatal(string(raw), err)
	}
	if _, err := store.LaunchAgents(context.Background(), plan); err == nil {
		t.Fatal("partial command effects were replayed")
	}
	again, _ := os.ReadFile(marker)
	if string(again) != string(raw) {
		t.Fatal("repeated launch duplicated command effects")
	}
	if sessions := f.run(t, "list-sessions", "-F", "#{session_name}"); sessions != "one" {
		t.Fatal(sessions)
	}
}

func TestLaunchAgentsIndependentWorkspacesAndLiveOwnerOnOtherSocket(t *testing.T) {
	f := newTmuxFixture(t)
	store := mailFixture(t).Store
	first := nativeLaunchPlan(t, f, "first-worktree")
	if result, err := store.LaunchAgents(context.Background(), first); err != nil {
		t.Fatal(result, err)
	}
	second := nativeLaunchPlan(t, f, "second-worktree")
	if result, err := store.LaunchAgents(context.Background(), second); err != nil {
		t.Fatal(result, err)
	}
	other := newTmuxFixture(t)
	conflicting := first
	conflicting.Socket, conflicting.Session = other.socket, "other-server-team"
	if result, err := store.LaunchAgents(context.Background(), conflicting); err == nil || len(result.Panes) != 0 {
		t.Fatal(result, err)
	}
	if sessions := other.run(t, "list-sessions", "-F", "#{session_name}"); sessions != "one" {
		t.Fatal("other server changed", sessions)
	}
	if count := strings.Count(f.run(t, "list-panes", "-a", "-F", "#{pane_id}"), "\n") + 1; count != 5 {
		t.Fatal("worktrees shared or duplicated panes", count)
	}
}

func TestLaunchAgentsBuildsEightColumnsWithoutHalvingExhaustion(t *testing.T) {
	f := newTmuxFixture(t)
	plan := nativeLaunchPlan(t, f, "wide-team")
	source := "## `edges`\n```mermaid\ngraph LR\n"
	plan.Columns = nil
	for i := range 8 {
		role := fmt.Sprintf("role-%d", i)
		source += role + "\n"
		plan.Columns = append(plan.Columns, config.LaunchColumn{Panes: []config.LaunchPane{{Role: role, Command: "exec sleep 120"}}})
	}
	source += "```\n"
	if err := os.WriteFile(plan.Config, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	topology, err := config.LoadWorkspaceTopology(context.Background(), plan.Config)
	if err != nil {
		t.Fatal(err)
	}
	plan.Topology = topology
	result, err := mailFixture(t).Store.LaunchAgents(context.Background(), plan)
	if err != nil || len(result.Panes) != 8 {
		t.Fatal(result, err)
	}
	lastLeft := -1
	for _, pane := range result.Panes {
		raw := f.run(t, "display-message", "-p", "-t", pane.Pane, "#{pane_left}")
		var left int
		if _, err := fmt.Sscanf(raw, "%d", &left); err != nil || left <= lastLeft {
			t.Fatal("column order", raw, err)
		}
		lastLeft = left
	}
}
