package controlplane

import (
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

func TestTargetForNodeSeparatesActorRunBrainAndHand(t *testing.T) {
	target := TargetForNode("review-session:worker", discovery.NodeInfo{
		PaneID:      "%42",
		SessionName: "review-session",
		SessionDir:  "/tmp/review-session",
	})

	if target.ActorID != "worker" {
		t.Fatalf("ActorID = %q, want %q", target.ActorID, "worker")
	}
	if target.RunID != "review-session:worker" {
		t.Fatalf("RunID = %q, want %q", target.RunID, "review-session:worker")
	}
	if target.Brain.Runtime != BrainRuntimeUnknown {
		t.Fatalf("Brain.Runtime = %q, want %q", target.Brain.Runtime, BrainRuntimeUnknown)
	}
	if target.Hand.Kind != HandKindTmux {
		t.Fatalf("Hand.Kind = %q, want %q", target.Hand.Kind, HandKindTmux)
	}
	if target.Hand.Address != "%42" {
		t.Fatalf("Hand.Address = %q, want %q", target.Hand.Address, "%42")
	}
	if got, want := target.InboxDir(), "/tmp/review-session/inbox/worker"; got != want {
		t.Fatalf("InboxDir() = %q, want %q", got, want)
	}
	if got, want := target.PostPath("ping.md"), "/tmp/review-session/post/ping.md"; got != want {
		t.Fatalf("PostPath() = %q, want %q", got, want)
	}
}

func TestTmuxHandAdapterDeliverUsesHandAttachment(t *testing.T) {
	var (
		gotPaneID         string
		gotMessage        string
		gotEnterDelay     time.Duration
		gotTimeout        time.Duration
		gotEnterCount     int
		gotBypassCooldown bool
		gotVerifyDelay    time.Duration
		gotMaxRetries     int
		probeCalls        int
	)

	adapter := TmuxHandAdapter{
		ProbeRuntime: func(paneID string) (string, error) {
			probeCalls++
			if paneID != "%99" {
				t.Fatalf("ProbeRuntime paneID = %q, want %q", paneID, "%99")
			}
			return "codex", nil
		},
		SendToPane: func(paneID string, message string, enterDelay time.Duration, tmuxTimeout time.Duration, enterCount int, bypassCooldown bool, verifyDelay time.Duration, maxRetries int) error {
			gotPaneID = paneID
			gotMessage = message
			gotEnterDelay = enterDelay
			gotTimeout = tmuxTimeout
			gotEnterCount = enterCount
			gotBypassCooldown = bypassCooldown
			gotVerifyDelay = verifyDelay
			gotMaxRetries = maxRetries
			return nil
		},
	}

	err := adapter.Deliver(Target{
		ActorID:     "worker",
		RunID:       "notify-session:worker",
		SessionName: "notify-session",
		SessionDir:  "/tmp/notify-session",
		Brain:       Brain{Runtime: BrainRuntimeUnknown},
		Hand:        HandAttachment{Kind: HandKindTmux, Address: "%99"},
	}, PaneDelivery{
		Content:        "notice worker",
		EnterDelay:     5 * time.Millisecond,
		TmuxTimeout:    1 * time.Second,
		EnterCount:     0,
		BypassCooldown: true,
		VerifyDelay:    7 * time.Millisecond,
		MaxRetries:     3,
	})
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	if gotPaneID != "%99" {
		t.Fatalf("SendToPane paneID = %q, want %q", gotPaneID, "%99")
	}
	if gotMessage != "notice worker" {
		t.Fatalf("SendToPane message = %q, want %q", gotMessage, "notice worker")
	}
	if gotEnterDelay != 5*time.Millisecond {
		t.Fatalf("SendToPane enterDelay = %s, want %s", gotEnterDelay, 5*time.Millisecond)
	}
	if gotTimeout != 1*time.Second {
		t.Fatalf("SendToPane tmuxTimeout = %s, want %s", gotTimeout, 1*time.Second)
	}
	if gotEnterCount != 2 {
		t.Fatalf("SendToPane enterCount = %d, want %d", gotEnterCount, 2)
	}
	if !gotBypassCooldown {
		t.Fatalf("SendToPane bypassCooldown = false, want true")
	}
	if gotVerifyDelay != 7*time.Millisecond {
		t.Fatalf("SendToPane verifyDelay = %s, want %s", gotVerifyDelay, 7*time.Millisecond)
	}
	if gotMaxRetries != 3 {
		t.Fatalf("SendToPane maxRetries = %d, want %d", gotMaxRetries, 3)
	}
	if probeCalls != 1 {
		t.Fatalf("ProbeRuntime calls = %d, want %d", probeCalls, 1)
	}
}
