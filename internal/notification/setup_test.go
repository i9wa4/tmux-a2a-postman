package notification

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Disable per-pane cooldown so unit tests can call SendToPane multiple times.
	InitPaneCooldown(0)
	os.Exit(m.Run())
}
