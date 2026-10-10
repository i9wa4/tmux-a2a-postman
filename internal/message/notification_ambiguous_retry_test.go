package message

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/controlplane"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

// TestDeliverNotificationWithRetryNeverRepastesAfterAmbiguousTimeout covers N2
// of the #871 review: once the notification may already be in the pane, a tmux
// deadline at ANY later stage (the chained paste/submit itself, the extra C-m
// for enterCount > 1, or a verify-retry C-m) must not make
// deliverNotificationWithRetry run the whole delivery again, which would paste a
// second copy. The fake tmux records every invocation and hangs at the chosen
// stage; exactly one invocation may contain paste-buffer.
func TestDeliverNotificationWithRetryNeverRepastesAfterAmbiguousTimeout(t *testing.T) {
	previousLog := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(previousLog) })

	cases := []struct {
		name        string
		hangAt      string // "chain": the paste chain hangs; "later": the chain succeeds, standalone send-keys hangs
		enterCount  int
		verifyDelay time.Duration
		maxRetries  int
	}{
		{name: "chained paste and submit", hangAt: "chain", enterCount: 1},
		{name: "second C-m (enterCount 2)", hangAt: "later", enterCount: 2},
		{name: "verify-retry C-m", hangAt: "later", enterCount: 1, verifyDelay: 10 * time.Millisecond, maxRetries: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			binDir := t.TempDir()
			invocations := filepath.Join(t.TempDir(), "invocations")
			script := `#!/bin/sh
echo "$*" >> "$FAKE_TMUX_LOG"
case "$*" in
  *paste-buffer*)
    if [ "$FAKE_TMUX_HANG" = chain ]; then while :; do :; done; fi
    exit 0 ;;
  *capture-pane*)
    printf '❯ You got mail: x.md\n'
    exit 0 ;;
  *send-keys*)
    while :; do :; done ;;
  *) exit 0 ;;
esac
`
			if err := os.WriteFile(filepath.Join(binDir, "tmux"), []byte(script), 0o755); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("FAKE_TMUX_LOG", invocations)
			t.Setenv("FAKE_TMUX_HANG", tc.hangAt)

			nodeInfo := discovery.NodeInfo{PaneID: "%7", SessionName: "sess", SessionDir: t.TempDir()}
			target := controlplane.TargetForNode("worker", nodeInfo)
			adapter, err := controlplane.DefaultHandAdapter(target)
			if err != nil {
				t.Fatalf("DefaultHandAdapter: %v", err)
			}
			delivery := controlplane.PaneDelivery{
				Content:        "You've got mail: x.md",
				TmuxTimeout:    150 * time.Millisecond,
				EnterCount:     tc.enterCount,
				BypassCooldown: true,
				VerifyDelay:    tc.verifyDelay,
				MaxRetries:     tc.maxRetries,
			}

			start := time.Now()
			deliverNotificationWithRetry(adapter, target, delivery, "worker", map[string]discovery.NodeInfo{}, "x.md")
			if elapsed := time.Since(start); elapsed > 10*time.Second {
				t.Fatalf("delivery took %s, want bounded by the tmux timeouts", elapsed)
			}

			raw, err := os.ReadFile(invocations)
			if err != nil {
				t.Fatalf("ReadFile invocations: %v", err)
			}
			pastes := 0
			for _, line := range strings.Split(string(raw), "\n") {
				if strings.Contains(line, "paste-buffer") {
					pastes++
				}
			}
			if pastes != 1 {
				t.Fatalf("paste-buffer invoked %d times after an ambiguous timeout at %q, want exactly 1 (no re-paste); invocations:\n%s", pastes, tc.name, raw)
			}
		})
	}
}
