package cli

import (
	"path/filepath"
	"testing"
)

func TestWorkspaceDefaultTopologyPathIsStableAcrossCWD(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	want := filepath.Join(home, ".config", "tmux-a2a-postman", "postman.md")
	for _, command := range []string{"send-heredoc", "pop", "get-status", "execute-bash"} {
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
	t.Setenv("XDG_CONFIG_HOME", xdg)
	want := filepath.Join(xdg, "tmux-a2a-postman", "postman.md")
	if got, err := newWorkspaceFlags("pop").parse(nil); err != nil || got != want {
		t.Fatalf("XDG default path: %q %v", got, err)
	}
	explicit := filepath.Join(t.TempDir(), "other.md")
	if got, err := newWorkspaceFlags("pop").parse([]string{"--postman-md", explicit}); err != nil || got != explicit {
		t.Fatalf("explicit path: %q %v", got, err)
	}
	if _, err := newWorkspaceFlags("pop").parse([]string{"--config", explicit, "--postman-md", explicit}); err == nil {
		t.Fatal("ambiguous explicit paths accepted")
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

func TestSupportedWorkspaceRoot(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".local", "state", "tmux-a2a-postman")
	if got, err := supportedWorkspaceRoot(home, "", root); err != nil || got != root {
		t.Fatalf("same root: %q %v", got, err)
	}
	for _, legacy := range []string{filepath.Join(home, "other"), "relative", ""} {
		if _, err := supportedWorkspaceRoot(home, "", legacy); err == nil {
			t.Fatalf("accepted divergent legacy root %q", legacy)
		}
	}
	if _, err := supportedWorkspaceRoot(home, "relative", root); err == nil {
		t.Fatal("accepted relative XDG state root")
	}
	xdg := t.TempDir()
	custom := filepath.Join(xdg, "tmux-a2a-postman")
	if got, err := supportedWorkspaceRoot(home, xdg, custom); err != nil || got != custom {
		t.Fatalf("same XDG root: %q %v", got, err)
	}
}
