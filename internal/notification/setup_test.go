package notification

import (
	"os"
	"path/filepath"
	"testing"
)

// #845 (F-2): TestSendToPane_InvalidPane calls the real SendToPane with a
// pane target tmux will reject ("invalid-pane"), but tmux still processes
// the earlier `set-buffer`/`load-buffer` step against the real server's
// paste buffer before failing on the pane target -- confirmed by Guardian's
// recording shim. Rather than emptying PATH entirely (which broke
// internal/cli tests that need other real executables like "sleep"), this
// prepends a poison "tmux" shim (always exits nonzero, never the real
// binary) onto the EXISTING PATH, so "tmux" can never resolve to the real
// server while every other tool keeps working. Tests that need a scripted
// fake tmux still work via their own t.Setenv("PATH", ...) (full
// replacement), scoped per test and winning over this baseline for that
// test's duration.
func lockDownPATHAgainstRealTmux() (restore func()) {
	dir, err := os.MkdirTemp("", "tmux-a2a-postman-notification-test-no-tmux-*")
	if err != nil {
		panic(err)
	}
	shim := "#!/bin/sh\necho 'tmux is blocked during internal/notification tests (#845)' >&2\nexit 127\n"
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
	// Disable per-pane cooldown so unit tests can call SendToPane multiple times.
	InitPaneCooldown(0)
	restorePath := lockDownPATHAgainstRealTmux()
	code := m.Run()
	restorePath()
	os.Exit(code)
}
