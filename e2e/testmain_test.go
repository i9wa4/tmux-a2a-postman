package e2e_test

import (
	"os"
	"path/filepath"
	"testing"
)

// #845 (F-3): this package's harness fixtures use synthetic PaneID strings
// (e.g. "pane-sender", "worker-pane-id") that never match real tmux's
// "%N" target syntax, but delivery code still invokes the real "tmux"
// binary with these as a target before tmux rejects them -- confirmed by
// Guardian's recording shim (42 real-tmux calls, the largest single source
// in the module-wide audit). No file in this package previously installed
// any PATH/tmux isolation. Rather than emptying PATH entirely (which broke
// internal/cli tests needing other real executables like "sleep"), this
// prepends a poison "tmux" shim (always exits nonzero, never the real
// binary) onto the EXISTING PATH, so "tmux" can never resolve to the real
// server while every other tool this harness may need keeps working.
func lockDownPATHAgainstRealTmux() (restore func()) {
	dir, err := os.MkdirTemp("", "tmux-a2a-postman-e2e-test-no-tmux-*")
	if err != nil {
		panic(err)
	}
	shim := "#!/bin/sh\necho 'tmux is blocked during e2e tests (#845)' >&2\nexit 127\n"
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
