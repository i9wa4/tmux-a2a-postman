package discovery

import (
	"testing"
)

func TestDiscoverNodes_WithChildProcess(t *testing.T) {
	// NOTE: This test requires actual tmux panes with child processes
	// Deferred to integration testing
	t.Skip("Requires tmux environment - deferred to integration testing")
}

func TestGetNodeFromProcessOS_ChildProcess(t *testing.T) {
	// NOTE: This test requires spawning child processes with A2A_NODE env
	// Deferred to integration testing
	t.Skip("Requires process spawning - deferred to integration testing")
}
