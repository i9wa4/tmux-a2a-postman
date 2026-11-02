package cli

import (
	"path/filepath"
	"testing"
)

func TestWorkspaceDefaultTopologyPathIsStableAcrossCWD(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TMUX_A2A_POSTMAN_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	want := filepath.Join(home, ".config", "tmux-a2a-postman", "postman.md")
	for _, command := range []string{"send-heredoc", "pop", "get-status", "get-status-oneline", "execute-heredoc", "approve-execution", "decline-execution"} {
		first, err := newWorkspaceFlags(command).parse(nil)
		if err != nil || first != want {
			t.Fatalf("%s default path: %q %v", command, first, err)
		}
		t.Chdir(t.TempDir())
		second, err := newWorkspaceFlags(command).parse(nil)
		if err != nil || second != first {
			t.Fatalf("%s changed with cwd: %q %v", command, second, err)
		}
	}
}

func TestWorkspaceTopologyPathXDGAndExplicitOverride(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("TMUX_A2A_POSTMAN_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	want := filepath.Join(xdg, "tmux-a2a-postman", "postman.md")
	if got, err := newWorkspaceFlags("pop").parse(nil); err != nil || got != want {
		t.Fatalf("XDG default path: %q %v", got, err)
	}
	explicit := filepath.Join(t.TempDir(), "other.md")
	if got, err := newWorkspaceFlags("pop").parse([]string{"--config", explicit}); err != nil || got != explicit {
		t.Fatalf("explicit path: %q %v", got, err)
	}
	if _, err := newWorkspaceFlags("pop").parse([]string{"--config", explicit, "--postman-md", explicit}); err == nil {
		t.Fatal("removed alias accepted")
	}
	if _, err := newWorkspaceFlags("pop").parse([]string{"--config", explicit, "--config", explicit}); err == nil {
		t.Fatal("duplicate config accepted")
	}
	if _, err := newWorkspaceFlags("pop").parse([]string{"--config", "relative.md"}); err == nil {
		t.Fatal("relative explicit path accepted")
	}
	t.Setenv("XDG_CONFIG_HOME", "relative-xdg")
	if _, err := newWorkspaceFlags("pop").parse(nil); err == nil {
		t.Fatal("relative XDG directory accepted")
	}
	if got, err := newWorkspaceFlags("pop").parse([]string{"--config", explicit}); err != nil || got != explicit {
		t.Fatalf("explicit override depended on invalid XDG: %q %v", got, err)
	}
}

func TestRemovedDaemonCommandsAreUnknown(t *testing.T) {
	for _, command := range []string{"execute-bash", "reply-approval", "approve", "decline", "start", "stop", "send", "inspect-daemon-submit", "inspect-command-approvals", "capture-profile", "reconcile-command-approval-reply-slot", "backfill-verdict-events"} {
		got := Dispatch(command, nil, Config{}, Handlers{})
		if got.Err == nil || !got.ShowUsage {
			t.Fatalf("retired command %s still dispatches: %+v", command, got)
		}
	}
}

func TestStatusOnelineDispatchUsesCommonConfigAndOwnHandler(t *testing.T) {
	var got []string
	result := Dispatch("get-status-oneline", nil, Config{ConfigPath: "/private/postman.md"}, Handlers{
		GetStatusOneline: func(args []string) error { got = args; return nil },
	})
	if result.Err != nil || result.ShowUsage || len(got) != 2 || got[0] != "--config" || got[1] != "/private/postman.md" {
		t.Fatal(result, got)
	}
}

func TestInheritedLaunchConfigurationKeepsExplicitOverridePrecedence(t *testing.T) {
	inherited := filepath.Join(t.TempDir(), "selected.md")
	t.Setenv("TMUX_A2A_POSTMAN_CONFIG", inherited)
	if got, err := newWorkspaceFlags("pop").parse(nil); err != nil || got != inherited {
		t.Fatal(got, err)
	}
	explicit := filepath.Join(t.TempDir(), "other.md")
	if got, err := newWorkspaceFlags("pop").parse([]string{"--config", explicit}); err != nil || got != explicit {
		t.Fatal(got, err)
	}
	t.Setenv("TMUX_A2A_POSTMAN_CONFIG", "relative.md")
	if _, err := newWorkspaceFlags("pop").parse(nil); err == nil {
		t.Fatal("relative inherited config accepted")
	}
}

func TestWorkspaceSourceFallbackAndExplicitErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("TMUX_A2A_POSTMAN_CONFIG", "")
	implicit, err := newWorkspaceFlags("pop").source(nil)
	if err != nil || !implicit.Builtin {
		t.Fatal(implicit, err)
	}
	t.Chdir(t.TempDir())
	again, err := newWorkspaceFlags("pop").source(nil)
	if err != nil || again != implicit {
		t.Fatal("cwd changed selection", again, err)
	}
	for _, args := range [][]string{{"--config="}, {"--config", ""}} {
		if _, err := newWorkspaceFlags("pop").source(args); err == nil {
			t.Fatal("explicit empty accepted", args)
		}
	}
	explicit, err := newWorkspaceFlags("pop").source([]string{"--config", implicit.Path})
	if err != nil || explicit.Builtin {
		t.Fatal("explicit missing path fell back", explicit, err)
	}
	t.Setenv("TMUX_A2A_POSTMAN_CONFIG", implicit.Path)
	inherited, err := newWorkspaceFlags("pop").source(nil)
	if err != nil || inherited.Builtin {
		t.Fatal("inherited missing path fell back", inherited, err)
	}
}
