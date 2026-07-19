package controlplane

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/multiplexer"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
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
		SessionDir:  t.TempDir(),
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

func TestTmuxInteractiveDeliveryAdapterUsesPaneSender(t *testing.T) {
	var (
		gotDelivery notification.PaneDelivery
		probeCalls  int
	)

	adapter := TmuxInteractiveDeliveryAdapter{
		ProbeRuntime: func(paneID string) (string, error) {
			probeCalls++
			if paneID != "%99" {
				t.Fatalf("ProbeRuntime paneID = %q, want %q", paneID, "%99")
			}
			return "codex", nil
		},
		PaneSender: notification.PaneSenderFunc(func(delivery notification.PaneDelivery) error {
			gotDelivery = delivery
			return nil
		}),
	}

	err := adapter.Deliver(Target{
		ActorID:     "worker",
		RunID:       "notify-session:worker",
		SessionName: "notify-session",
		SessionDir:  t.TempDir(),
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

	if gotDelivery.PaneID != "%99" {
		t.Fatalf("PaneDelivery.PaneID = %q, want %q", gotDelivery.PaneID, "%99")
	}
	if gotDelivery.Message != "notice worker" {
		t.Fatalf("PaneDelivery.Message = %q, want %q", gotDelivery.Message, "notice worker")
	}
	if gotDelivery.EnterCount != 2 {
		t.Fatalf("PaneDelivery.EnterCount = %d, want %d", gotDelivery.EnterCount, 2)
	}
	if gotDelivery.EnterDelay != 5*time.Millisecond {
		t.Fatalf("PaneDelivery.EnterDelay = %s, want %s", gotDelivery.EnterDelay, 5*time.Millisecond)
	}
	if gotDelivery.TmuxTimeout != 1*time.Second {
		t.Fatalf("PaneDelivery.TmuxTimeout = %s, want %s", gotDelivery.TmuxTimeout, 1*time.Second)
	}
	if !gotDelivery.BypassCooldown {
		t.Fatal("PaneDelivery.BypassCooldown = false, want true")
	}
	if gotDelivery.VerifyDelay != 7*time.Millisecond {
		t.Fatalf("PaneDelivery.VerifyDelay = %s, want %s", gotDelivery.VerifyDelay, 7*time.Millisecond)
	}
	if gotDelivery.MaxRetries != 3 {
		t.Fatalf("PaneDelivery.MaxRetries = %d, want %d", gotDelivery.MaxRetries, 3)
	}
	if probeCalls != 1 {
		t.Fatalf("ProbeRuntime calls = %d, want %d", probeCalls, 1)
	}
}

func TestHerdrInteractiveDeliveryAdapterUsesBackendAndSanitizes(t *testing.T) {
	client := &fakeHerdrControlplaneWriteClient{
		snapshot: validHerdrControlplaneSnapshot(),
	}
	adapter := HerdrInteractiveDeliveryAdapter{
		Backend: multiplexer.HerdrBackend{
			Config: validHerdrControlplaneConfig(),
			Client: client,
		},
	}

	err := adapter.Deliver(Target{
		ActorID:     "worker",
		RunID:       "work:worker",
		SessionName: "work",
		SessionDir:  t.TempDir(),
		Hand:        HandAttachment{Kind: HandKindHerdr, Address: "workspace-1:pane-1"},
	}, PaneDelivery{
		Content: "\x1b[31mnotice worker\x1b[0m",
	})
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	if client.writeTextCalls != 1 || client.writeTextPane != "workspace-1:pane-1" {
		t.Fatalf("write text call = calls:%d pane:%q, want Herdr pane write", client.writeTextCalls, client.writeTextPane)
	}
	if !strings.Contains(client.writeTextText, "<!-- message start -->") ||
		!strings.Contains(client.writeTextText, "notice worker") ||
		strings.Contains(client.writeTextText, "\x1b") {
		t.Fatalf("write text = %q, want wrapped sanitized content", client.writeTextText)
	}
	if client.sendKeyCalls != 1 || client.sendKeyKey != multiplexer.HerdrKeyEnter {
		t.Fatalf("send key call = calls:%d key:%q, want one Herdr Enter key", client.sendKeyCalls, client.sendKeyKey)
	}
}

func TestHerdrInteractiveDeliveryAdapterRejectsWrongHandKind(t *testing.T) {
	client := &fakeHerdrControlplaneWriteClient{
		snapshot: validHerdrControlplaneSnapshot(),
	}
	adapter := HerdrInteractiveDeliveryAdapter{
		Backend: multiplexer.HerdrBackend{
			Config:         validHerdrControlplaneConfig(),
			Client:         client,
			InputSanitizer: func(text string) (string, error) { return text, nil },
		},
	}

	err := adapter.Deliver(Target{
		Hand: HandAttachment{Kind: HandKindTmux, Address: "%1"},
	}, PaneDelivery{Content: "notice"})
	if err == nil {
		t.Fatal("Deliver() error = nil, want wrong hand kind error")
	}
	if client.writeTextCalls != 0 || client.sendKeyCalls != 0 {
		t.Fatalf("mutation calls = write:%d key:%d, want none", client.writeTextCalls, client.sendKeyCalls)
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

type fakeHerdrControlplaneWriteClient struct {
	snapshot multiplexer.HerdrSessionSnapshot

	writeTextCalls int
	writeTextPane  string
	writeTextText  string
	sendKeyCalls   int
	sendKeyPane    string
	sendKeyKey     string
}

func (f *fakeHerdrControlplaneWriteClient) Ping(context.Context) (multiplexer.HerdrResponseEnvelope, error) {
	return validHerdrControlplaneEnvelope(), nil
}

func (f *fakeHerdrControlplaneWriteClient) SessionSnapshot(context.Context) (multiplexer.HerdrSessionSnapshot, error) {
	return f.snapshot, nil
}

func (f *fakeHerdrControlplaneWriteClient) ReadPane(context.Context, string, multiplexer.HerdrPaneReadOptions) (multiplexer.HerdrPaneReadResult, error) {
	return multiplexer.HerdrPaneReadResult{Envelope: validHerdrControlplaneEnvelope()}, nil
}

func (f *fakeHerdrControlplaneWriteClient) PaneProcessInfo(context.Context, string) (multiplexer.HerdrPaneProcessInfoResult, error) {
	return multiplexer.HerdrPaneProcessInfoResult{Envelope: validHerdrControlplaneEnvelope()}, nil
}

func (f *fakeHerdrControlplaneWriteClient) WritePaneText(_ context.Context, paneID string, text string) (multiplexer.HerdrWriteResult, error) {
	f.writeTextCalls++
	f.writeTextPane = paneID
	f.writeTextText = text
	return multiplexer.HerdrWriteResult{Envelope: validHerdrControlplaneEnvelope()}, nil
}

func (f *fakeHerdrControlplaneWriteClient) SendPaneKey(_ context.Context, paneID string, key string) (multiplexer.HerdrWriteResult, error) {
	f.sendKeyCalls++
	f.sendKeyPane = paneID
	f.sendKeyKey = key
	return multiplexer.HerdrWriteResult{Envelope: validHerdrControlplaneEnvelope()}, nil
}

func (f *fakeHerdrControlplaneWriteClient) SetWorkspaceMetadata(context.Context, string, string, string) (multiplexer.HerdrWriteResult, error) {
	return multiplexer.HerdrWriteResult{Envelope: validHerdrControlplaneEnvelope()}, nil
}

func (f *fakeHerdrControlplaneWriteClient) ClearWorkspaceMetadata(context.Context, string, string) (multiplexer.HerdrWriteResult, error) {
	return multiplexer.HerdrWriteResult{Envelope: validHerdrControlplaneEnvelope()}, nil
}

func (f *fakeHerdrControlplaneWriteClient) SetPaneMetadata(context.Context, string, string, string) (multiplexer.HerdrWriteResult, error) {
	return multiplexer.HerdrWriteResult{Envelope: validHerdrControlplaneEnvelope()}, nil
}

func (f *fakeHerdrControlplaneWriteClient) ClearPaneMetadata(context.Context, string, string) (multiplexer.HerdrWriteResult, error) {
	return multiplexer.HerdrWriteResult{Envelope: validHerdrControlplaneEnvelope()}, nil
}

func validHerdrControlplaneConfig() multiplexer.HerdrReadConfig {
	return multiplexer.HerdrReadConfig{
		Enabled: true,
		Runtime: multiplexer.HerdrRuntimeIdentity{
			SocketPath:  "/tmp/herdr.sock",
			SessionName: "work",
			WorkspaceID: "workspace-1",
			TabID:       "workspace-1:tab-1",
			PaneID:      "workspace-1:pane-1",
		},
		Policy: multiplexer.HerdrGatePolicy{
			ReadEnabled:             true,
			WriteEnabled:            true,
			AllowedSocketPaths:      []string{"/tmp/herdr.sock"},
			AllowedSessions:         []string{"work"},
			AllowedWorkspaceIDs:     []string{"workspace-1"},
			AllowedProtocolVersions: []string{"1"},
			AllowedSchemaVersions:   []int{1},
			InputSanitizerReady:     true,
			ComplianceDecision:      multiplexer.HerdrComplianceDecisionCommercial,
		},
	}
}

func validHerdrControlplaneSnapshot() multiplexer.HerdrSessionSnapshot {
	return multiplexer.HerdrSessionSnapshot{
		Envelope: validHerdrControlplaneEnvelope(),
		Workspaces: []multiplexer.HerdrWorkspaceSnapshot{{
			ID: "workspace-1",
		}},
		Tabs: []multiplexer.HerdrTabSnapshot{{
			ID:          "workspace-1:tab-1",
			WorkspaceID: "workspace-1",
		}},
		Panes: []multiplexer.HerdrPaneSnapshot{{
			ID:          "workspace-1:pane-1",
			WorkspaceID: "workspace-1",
			TabID:       "workspace-1:tab-1",
		}},
	}
}

func validHerdrControlplaneEnvelope() multiplexer.HerdrResponseEnvelope {
	return multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}
}

func TestFilesystemSystemMessageAdapterWritesInbox(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	target := Target{
		ActorID:     "worker",
		RunID:       "review-session:worker",
		SessionName: "review-session",
		SessionDir:  sessionDir,
	}

	result, err := (FilesystemSystemMessageAdapter{}).DeliverSystemMessage(target, SystemMessageDelivery{
		Filename: "20260414-120000-r1234-from-postman-to-worker.md",
		Sender:   "postman",
		Content:  "system delivery",
		QueueCap: 20,
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
