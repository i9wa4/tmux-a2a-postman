package ping

import (
	"os"
	"testing"
)

// #845: this package's tests hardcode fake PaneIDs (e.g. "%100") that are
// not scoped to any real tmux session. Delivery code reachable from
// SendPingToNode (sendDeliveryNotification and friends, via
// internal/message) shells out to the real "tmux" binary if one is on
// PATH. Tmux PaneIDs are unique per tmux SERVER, not per session, so
// running these tests from inside a live tmux session could otherwise
// send real keystrokes to whatever ambient pane on the real server
// happens to hold a fixture's fake ID. TestMain runs once for the whole
// package and points PATH at an empty directory, protecting every test
// in this package without editing each fixture individually. A test that
// needs its own scripted fake tmux (e.g.
// TestSendPingToNode_NotificationAttemptedOnDelivery) still works: its own
// t.Setenv("PATH", ...) prepends its fake binary's directory onto this
// already-empty PATH.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tmux-a2a-postman-ping-test-no-tmux-*")
	if err != nil {
		panic(err)
	}
	original, hadOriginal := os.LookupEnv("PATH")
	if err := os.Setenv("PATH", dir); err != nil {
		panic(err)
	}
	code := m.Run()
	if hadOriginal {
		_ = os.Setenv("PATH", original)
	} else {
		_ = os.Unsetenv("PATH")
	}
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
