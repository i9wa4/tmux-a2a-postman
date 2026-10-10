package notification

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type timeoutTestDiscardWriter struct{}

func (timeoutTestDiscardWriter) Write(p []byte) (int, error) { return len(p), nil }

func installTimeoutTestTmux(t *testing.T, script string) {
	t.Helper()
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestSendToPaneHonorsTmuxTimeout pins the #871 mitigation: PaneNotifier.run
// used to ignore the tmuxTimeout passed to SendToPane (bare exec.Command), so
// a hung tmux server could block the caller forever. A timeout must surface as
// an error, never as delivery success.
func TestSendToPaneHonorsTmuxTimeout(t *testing.T) {
	installTimeoutTestTmux(t, "#!/bin/sh\nwhile :; do :; done\n")

	n := NewPaneNotifier(0)
	n.stderr = timeoutTestDiscardWriter{}

	start := time.Now()
	err := n.SendToPane("%1", "You've got mail: x.md", 0, 50*time.Millisecond, 1, true, 0, 0)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SendToPane error = %v, want context deadline exceeded", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("SendToPane took %s, want bounded by the tmux timeout", elapsed)
	}
}

// TestSendToPaneChainDeadlineCoversEnterDelay: the chained invocation includes
// the enter delay, so a tmuxTimeout shorter than that delay must not kill the
// client between paste and C-m (R1 in the #871 review). The fake tmux takes
// ~1s to finish, longer than tmuxTimeout (300ms) but well inside
// tmuxTimeout + enterDelay + margin.
func TestSendToPaneChainDeadlineCoversEnterDelay(t *testing.T) {
	installTimeoutTestTmux(t, "#!/bin/sh\nsleep 1\nexit 0\n")

	n := NewPaneNotifier(0)
	n.stderr = timeoutTestDiscardWriter{}

	err := n.SendToPane("%1", "You've got mail: x.md", 800*time.Millisecond, 300*time.Millisecond, 1, true, 0, 0)
	if err != nil {
		t.Fatalf("SendToPane error = %v, want success (deadline must cover the in-chain enter delay)", err)
	}
}

// TestSendToPaneChainTimeoutIsNonRetryableAndNotRepasted covers G871-3: a
// timeout after the chained paste may have delivered (or even submitted) the
// notification, so it must surface as ErrPaneUnresponsive and SendToPane must
// not issue any second tmux invocation (no re-paste, no extra C-m).
func TestSendToPaneChainTimeoutIsNonRetryableAndNotRepasted(t *testing.T) {
	invocations := filepath.Join(t.TempDir(), "invocations")
	installTimeoutTestTmux(t, "#!/bin/sh\necho call >> '"+invocations+"'\nwhile :; do :; done\n")

	n := NewPaneNotifier(0)
	n.stderr = timeoutTestDiscardWriter{}

	// enterCount 2 and verify retries enabled: all would trigger further tmux
	// invocations if the timeout were treated as a plain retryable failure.
	err := n.SendToPane("%1", "You've got mail: x.md", 0, 50*time.Millisecond, 2, true, 10*time.Millisecond, 2)
	if !errors.Is(err, ErrPaneUnresponsive) {
		t.Fatalf("SendToPane error = %v, want ErrPaneUnresponsive", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SendToPane error = %v, want it to keep the deadline cause", err)
	}
	raw, readErr := os.ReadFile(invocations)
	if readErr != nil {
		t.Fatalf("ReadFile invocations: %v", readErr)
	}
	if calls := strings.Count(string(raw), "call"); calls != 1 {
		t.Fatalf("tmux invoked %d times after the chained timeout, want exactly 1 (no re-paste)", calls)
	}
}

func TestAtomicChainTimeout(t *testing.T) {
	cases := []struct {
		name        string
		tmuxTimeout time.Duration
		enterDelay  time.Duration
		min         time.Duration
	}{
		{"enter delay longer than tmux timeout", 2 * time.Second, 3 * time.Second, 5 * time.Second},
		{"no enter delay", time.Second, 0, time.Second},
		{"unset tmux timeout uses default", 0, time.Second, 11 * time.Second},
		{"negative delay ignored", time.Second, -time.Second, time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := atomicChainTimeout(tc.tmuxTimeout, tc.enterDelay)
			if got < tc.min+atomicChainMargin {
				t.Fatalf("atomicChainTimeout(%s, %s) = %s, want >= %s", tc.tmuxTimeout, tc.enterDelay, got, tc.min+atomicChainMargin)
			}
		})
	}
}
