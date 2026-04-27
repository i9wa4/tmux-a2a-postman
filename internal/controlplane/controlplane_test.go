package controlplane

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
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

func TestTmuxHandAdapterDeliverSystemMessageWritesInbox(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	target := Target{
		ActorID:     "worker",
		RunID:       "review-session:worker",
		SessionName: "review-session",
		SessionDir:  sessionDir,
		Hand:        HandAttachment{Kind: HandKindTmux, Address: "%9"},
	}

	result, err := (TmuxHandAdapter{}).DeliverSystemMessage(target, SystemMessageDelivery{
		Filename:        "20260414-120000-r1234-from-postman-to-worker.md",
		Sender:          "postman",
		Content:         "system delivery",
		QueueCap:        20,
		QueueFullSuffix: "-dl-queue-full",
	})
	if err != nil {
		t.Fatalf("DeliverSystemMessage() error = %v", err)
	}
	if !result.Delivered {
		t.Fatal("DeliverSystemMessage() delivered = false, want true")
	}

	body, err := os.ReadFile(filepath.Join(sessionDir, "inbox", "worker", "20260414-120000-r1234-from-postman-to-worker.md"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(body) != "system delivery" {
		t.Fatalf("inbox body = %q, want %q", string(body), "system delivery")
	}
}

func TestTmuxHandAdapterDeliverSystemMessageQueueFullStaysUndeliveredWithoutDeadLetter(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	target := Target{
		ActorID:     "worker",
		RunID:       "review-session:worker",
		SessionName: "review-session",
		SessionDir:  sessionDir,
		Hand:        HandAttachment{Kind: HandKindTmux, Address: "%9"},
	}

	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(inboxDir) error = %v", err)
	}
	for i := range 20 {
		name := filepath.Join(inboxDir, fmt.Sprintf("20260414-1155%02d-r1111-from-postman-to-worker.md", i))
		if err := os.WriteFile(name, []byte("queued"), 0o600); err != nil {
			t.Fatalf("WriteFile(queued %d) error = %v", i, err)
		}
	}

	result, err := (TmuxHandAdapter{}).DeliverSystemMessage(target, SystemMessageDelivery{
		Filename:        "20260414-120000-r1234-from-postman-to-worker.md",
		Sender:          "postman",
		Content:         "system delivery",
		QueueCap:        20,
		QueueFullSuffix: "-dl-queue-full",
	})
	if err != nil {
		t.Fatalf("DeliverSystemMessage() error = %v", err)
	}
	if result.Delivered {
		t.Fatal("DeliverSystemMessage() delivered = true, want false when inbox is full")
	}

	deadEntries, err := os.ReadDir(filepath.Join(sessionDir, "dead-letter"))
	if err != nil {
		t.Fatalf("ReadDir(dead-letter) error = %v", err)
	}
	if len(deadEntries) != 0 {
		t.Fatalf("dead-letter entries = %d, want 0 for retryable queue-full system delivery", len(deadEntries))
	}
}

func TestTmuxHandAdapterWriteHeartbeatTriggerRecyclesStaleAndWritesPost(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	target := Target{
		ActorID:     "worker",
		RunID:       "review-session:worker",
		SessionName: "review-session",
		SessionDir:  sessionDir,
		Hand:        HandAttachment{Kind: HandKindTmux, Address: "%9"},
	}

	if err := os.MkdirAll(filepath.Join(sessionDir, "inbox", "worker"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	stalePath := filepath.Join(sessionDir, "inbox", "worker", "20260414-115500-r1111-from-postman-to-worker.md")
	if err := os.WriteFile(stalePath, []byte("stale trigger"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	staleTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stalePath, staleTime, staleTime); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}

	result, err := (TmuxHandAdapter{}).WriteHeartbeatTrigger(target, HeartbeatTrigger{
		Filename: "20260414-120000-r2222-from-postman-to-worker.md",
		Content:  "heartbeat trigger",
		TTL:      2 * time.Second,
	})
	if err != nil {
		t.Fatalf("WriteHeartbeatTrigger() error = %v", err)
	}
	if !result.Written {
		t.Fatal("WriteHeartbeatTrigger() written = false, want true")
	}

	if _, err := os.Stat(filepath.Join(sessionDir, "dead-letter", "20260414-115500-r1111-from-postman-to-worker.md")); err != nil {
		t.Fatalf("dead-letter stale trigger missing: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(sessionDir, "post", "20260414-120000-r2222-from-postman-to-worker.md"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(body) != "heartbeat trigger" {
		t.Fatalf("post body = %q, want %q", string(body), "heartbeat trigger")
	}
}
