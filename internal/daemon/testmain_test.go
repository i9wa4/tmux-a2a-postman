package daemon

import (
	"os"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

// #845: this package's tests hardcode fake PaneIDs (e.g. "%1"/"%2"/"%11")
// across many fixtures that are not scoped to any real tmux session.
// Delivery code reachable from these tests shells out to the real "tmux"
// binary if one is on PATH. Tmux PaneIDs are unique per tmux SERVER, not
// per session, so running these tests from inside a live tmux session
// could otherwise send real keystrokes to whatever ambient pane on the
// real server happens to hold a fixture's fake ID. TestMain runs once for
// the whole package, so pointing PATH at an empty directory here (TestMain
// receives *testing.M, not testing.TB, so the per-test
// tmuxtest.InstallMissing helper does not apply) protects every test in
// this package without editing each fixture individually.
func lockDownPATHAgainstRealTmux() (restore func()) {
	dir, err := os.MkdirTemp("", "tmux-a2a-postman-daemon-test-no-tmux-*")
	if err != nil {
		panic(err)
	}
	original, hadOriginal := os.LookupEnv("PATH")
	if err := os.Setenv("PATH", dir); err != nil {
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
	restoreDurableWrites := journal.SetDurableWritesForTesting(false)
	restorePath := lockDownPATHAgainstRealTmux()
	code := m.Run()
	restorePath()
	restoreDurableWrites()
	os.Exit(code)
}
