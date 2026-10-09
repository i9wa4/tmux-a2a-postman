package config

import (
	"os"
	"path/filepath"
	"testing"
)

// #845 (F-3): several TestResolveContextIDFromSession subtests call
// ResolveContextIDFromSession without installSessionOwnerTmux, so they reach
// whatever real "tmux" is on PATH (confirmed by Guardian's recording shim:
// 4 real-tmux calls traced to this package). Rather than emptying PATH
// entirely (which broke internal/cli tests needing other real executables
// like "sleep"), this prepends a poison "tmux" shim (always exits nonzero,
// never the real binary) onto the EXISTING PATH, so "tmux" can never
// resolve to the real server while every other tool keeps working.
// installSessionOwnerTmux still works: it prepends its own fake tmux
// directory onto PATH via t.Setenv (full replacement), scoped per test and
// winning over this baseline for that test's duration.
func lockDownPATHAgainstRealTmux() (restore func()) {
	dir, err := os.MkdirTemp("", "tmux-a2a-postman-config-test-no-tmux-*")
	if err != nil {
		panic(err)
	}
	shim := "#!/bin/sh\necho 'tmux is blocked during internal/config tests (#845)' >&2\nexit 127\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(shim), 0o755); err != nil {
		panic(err)
	}
	original, hadOriginal := os.LookupEnv("PATH")
	newPath := dir
	if hadOriginal {
		newPath = dir + string(os.PathListSeparator) + original
	}
	if err := os.Setenv("PATH", newPath); err != nil {
		panic(err)
	}
	return func() {
		if hadOriginal {
			_ = os.Setenv("PATH", original)
		} else {
			_ = os.Unsetenv("PATH")
		}
		_ = os.RemoveAll(dir)
	}
}

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "postman-config-tests-*")
	if err != nil {
		panic(err)
	}
	origWd, err := os.Getwd()
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		panic(err)
	}

	_ = os.Setenv("HOME", filepath.Join(tmpDir, "home"))
	_ = os.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpDir, "xdg"))
	_ = os.Setenv("XDG_STATE_HOME", filepath.Join(tmpDir, "state"))
	_ = os.Setenv("POSTMAN_HOME", "")
	if err := os.Chdir(tmpDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		panic(err)
	}
	restorePath := lockDownPATHAgainstRealTmux()

	code := m.Run()

	restorePath()
	_ = os.Chdir(origWd)
	_ = os.RemoveAll(tmpDir)
	os.Exit(code)
}
