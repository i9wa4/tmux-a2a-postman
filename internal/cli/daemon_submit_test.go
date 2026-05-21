package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

func TestRoundTripDaemonSubmitTimeoutWarnsRequestMayStillCommit(t *testing.T) {
	sessionDir := t.TempDir()
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	_, err := roundTripDaemonSubmit(sessionDir, projection.DaemonSubmitRequest{
		Command: projection.DaemonSubmitPop,
		Node:    "worker",
	}, time.Millisecond)
	if err == nil {
		t.Fatal("roundTripDaemonSubmit() error = nil, want timeout")
	}

	got := err.Error()
	for _, want := range []string{
		"daemon-submit pop request",
		"timed out",
		"may still commit",
		"do not retry blindly",
		"inspect status, inbox, read, or recipient-side evidence",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("timeout error missing %q: %q", want, got)
		}
	}
}
