package discovery

import (
	"testing"
)

func TestDiscoverNodes_WithChildProcess(t *testing.T) {
	// NOTE: This test requires actual tmux panes with child processes
	// Deferred to integration testing
	t.Skip("Requires tmux environment - deferred to integration testing")
}

func TestDiscoverNodes_WithPaneTitle(t *testing.T) {
	// NOTE: This test requires spawning tmux panes with named titles
	// Deferred to integration testing
	t.Skip("Requires tmux environment with named panes - deferred to integration testing")
}
