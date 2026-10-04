package multiplexer

import (
	"os"
	"path/filepath"
	"testing"
)

// #845 (F-3): TestTmuxBackendCapturePanePropagatesTimeout calls TmuxBackend's
// real CapturePane directly, without tmuxtest.Install, reaching whatever
// real "tmux" is on PATH against pane "%11" -- confirmed by Guardian's
// recording shim. Rather than emptying PATH entirely (which broke
// internal/cli tests needing other real executables like "sleep"), this
// prepends a poison "tmux" shim (always exits nonzero, never the real
// binary) onto the EXISTING PATH, so "tmux" can never resolve to the real
// server while every other tool keeps working. Tests that need a scripted
// fake tmux still work via tmuxtest.Install, which uses t.Setenv("PATH",
// dir) (full replacement), scoped per test and winning over this baseline
// for that test's duration.
func lockDownPATHAgainstRealTmux() (restore func()) {
	dir, err := os.MkdirTemp("", "tmux-a2a-postman-multiplexer-test-no-tmux-*")
	if err != nil {
		panic(err)
	}
	shim := "#!/bin/sh\necho 'tmux is blocked during internal/multiplexer tests (#845)' >&2\nexit 127\n"
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
	restorePath := lockDownPATHAgainstRealTmux()
	code := m.Run()
	restorePath()
	os.Exit(code)
}
