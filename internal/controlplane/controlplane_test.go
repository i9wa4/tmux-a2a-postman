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

func TestTargetForNodeUsesExplicitHerdrBackendMetadata(t *testing.T) {
	target := TargetForNode("work:worker", discovery.NodeInfo{
		PaneID:      "workspace-1:pane-1",
		SessionName: "work",
		SessionDir:  "/tmp/work",
		Backend:     string(multiplexer.BackendKindHerdr),
		Runtime:     "codex",
	})

	if target.Hand.Kind != HandKindHerdr {
		t.Fatalf("Hand.Kind = %q, want herdr", target.Hand.Kind)
	}
	if target.Hand.Address != "workspace-1:pane-1" {
		t.Fatalf("Hand.Address = %q, want Herdr pane", target.Hand.Address)
	}
	if target.Brain.Runtime != "codex" {
		t.Fatalf("Brain.Runtime = %q, want codex", target.Brain.Runtime)
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
	if client.sendKeyCalls != 1 || client.sendKeyKey != multiplexer.HerdrKeySubmit {
		t.Fatalf("send key call = calls:%d key:%q, want one Herdr Enter key", client.sendKeyCalls, client.sendKeyKey)
	}
}

func TestHerdrInteractiveDeliveryAdapterUsesCodexDefaultEnterCount(t *testing.T) {
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
		Brain: Brain{Runtime: "codex"},
		Hand:  HandAttachment{Kind: HandKindHerdr, Address: "workspace-1:pane-1"},
	}, PaneDelivery{Content: "notice", EnterCount: 0})
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if client.sendKeyCalls != 2 || client.sendKeyKey != multiplexer.HerdrKeySubmit {
		t.Fatalf("send key call = calls:%d key:%q, want two submit keys for Codex default", client.sendKeyCalls, client.sendKeyKey)
	}
}

func TestDefaultHandAdapterSelectsRegisteredHerdrAdapter(t *testing.T) {
	client := &fakeHerdrControlplaneWriteClient{
		snapshot: validHerdrControlplaneSnapshot(),
	}
	unregister := RegisterHerdrHandAdapter("workspace-1:pane-1", HerdrHandAdapter{
		HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
			Backend: multiplexer.HerdrBackend{
				Config: validHerdrControlplaneConfig(),
				Client: client,
			},
		},
	})
	t.Cleanup(unregister)

	target := TargetForNode("work:worker", discovery.NodeInfo{
		PaneID:      "workspace-1:pane-1",
		SessionName: "work",
		Backend:     string(multiplexer.BackendKindHerdr),
		Runtime:     "codex",
	})
	adapter, err := DefaultHandAdapter(target)
	if err != nil {
		t.Fatalf("DefaultHandAdapter() error = %v", err)
	}
	if adapter.Kind() != HandKindHerdr {
		t.Fatalf("adapter.Kind() = %q, want herdr", adapter.Kind())
	}
	if err := adapter.Deliver(target, PaneDelivery{Content: "notice"}); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if client.writeTextCalls != 1 || client.sendKeyCalls != 2 {
		t.Fatalf("Herdr delivery calls = write:%d key:%d, want reachable registered Herdr delivery", client.writeTextCalls, client.sendKeyCalls)
	}
}

func TestDefaultHandAdapterKeepsOverlappingHerdrPaneIDsIsolatedByRuntime(t *testing.T) {
	paneID := "shared-native-pane"
	firstClient := &fakeHerdrControlplaneWriteClient{snapshot: validHerdrControlplaneSnapshot()}
	firstConfig := validHerdrControlplaneConfig()
	firstConfig.Runtime.SocketPath = "/tmp/herdr-first.sock"
	firstConfig.Runtime.WorkspaceID = "workspace-first"
	firstConfig.Runtime.PaneID = paneID
	firstConfig.Policy.AllowedSocketPaths = []string{"/tmp/herdr-first.sock"}
	firstConfig.Policy.AllowedWorkspaceIDs = []string{"workspace-first"}
	firstClient.snapshot.Workspaces[0].ID = "workspace-first"
	firstClient.snapshot.Tabs[0].WorkspaceID = "workspace-first"
	firstClient.snapshot.Panes[0].WorkspaceID = "workspace-first"
	firstClient.snapshot.Panes[0].ID = paneID
	firstUnregister := RegisterHerdrHandAdapter(paneID, HerdrHandAdapter{
		HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
			Backend: multiplexer.HerdrBackend{Config: firstConfig, Client: firstClient},
		},
	})
	t.Cleanup(firstUnregister)

	secondClient := &fakeHerdrControlplaneWriteClient{snapshot: validHerdrControlplaneSnapshot()}
	secondConfig := validHerdrControlplaneConfig()
	secondConfig.Runtime.SocketPath = "/tmp/herdr-second.sock"
	secondConfig.Runtime.WorkspaceID = "workspace-second"
	secondConfig.Runtime.PaneID = paneID
	secondConfig.Policy.AllowedSocketPaths = []string{"/tmp/herdr-second.sock"}
	secondConfig.Policy.AllowedWorkspaceIDs = []string{"workspace-second"}
	secondClient.snapshot.Workspaces[0].ID = "workspace-second"
	secondClient.snapshot.Tabs[0].WorkspaceID = "workspace-second"
	secondClient.snapshot.Panes[0].WorkspaceID = "workspace-second"
	secondClient.snapshot.Panes[0].ID = paneID
	secondUnregister := RegisterHerdrHandAdapter(paneID, HerdrHandAdapter{
		HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
			Backend: multiplexer.HerdrBackend{Config: secondConfig, Client: secondClient},
		},
	})
	t.Cleanup(secondUnregister)

	firstTarget := Target{
		Hand: HandAttachment{Kind: HandKindHerdr, Address: paneID, HerdrRuntimeID: firstConfig.Runtime},
	}
	secondTarget := Target{
		Hand: HandAttachment{Kind: HandKindHerdr, Address: paneID, HerdrRuntimeID: secondConfig.Runtime},
	}
	firstAdapter, err := DefaultHandAdapter(firstTarget)
	if err != nil {
		t.Fatalf("DefaultHandAdapter(first) error = %v", err)
	}
	if err := firstAdapter.Deliver(firstTarget, PaneDelivery{Content: "first"}); err != nil {
		t.Fatalf("Deliver(first) error = %v", err)
	}
	secondAdapter, err := DefaultHandAdapter(secondTarget)
	if err != nil {
		t.Fatalf("DefaultHandAdapter(second) error = %v", err)
	}
	if err := secondAdapter.Deliver(secondTarget, PaneDelivery{Content: "second"}); err != nil {
		t.Fatalf("Deliver(second) error = %v", err)
	}
	if firstClient.writeTextCalls != 1 || secondClient.writeTextCalls != 1 {
		t.Fatalf("write calls first=%d second=%d, want isolated deliveries", firstClient.writeTextCalls, secondClient.writeTextCalls)
	}

	firstUnregister()
	if _, err := DefaultHandAdapter(firstTarget); err == nil {
		t.Fatal("first runtime adapter remained registered after cleanup")
	}
	if _, err := DefaultHandAdapter(secondTarget); err != nil {
		t.Fatalf("second runtime adapter was removed by stale first cleanup: %v", err)
	}

	secondUnregister()
	if _, err := DefaultHandAdapter(secondTarget); err == nil {
		t.Fatal("second runtime adapter remained registered after cleanup")
	}
}

func TestDefaultHandAdapterKeepsOverlappingHerdrPaneIDsIsolatedByTab(t *testing.T) {
	paneID := "shared-native-pane"
	firstClient := &fakeHerdrControlplaneWriteClient{snapshot: validHerdrControlplaneSnapshot()}
	firstConfig := validHerdrControlplaneConfig()
	firstConfig.Runtime.TabID = "workspace-1:tab-1"
	firstConfig.Runtime.PaneID = paneID
	firstClient.snapshot.Panes[0].ID = paneID
	firstClient.snapshot.Panes[0].TabID = "workspace-1:tab-1"
	firstUnregister := RegisterHerdrHandAdapter(paneID, HerdrHandAdapter{
		HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
			Backend: multiplexer.HerdrBackend{Config: firstConfig, Client: firstClient},
		},
	})
	t.Cleanup(firstUnregister)

	secondClient := &fakeHerdrControlplaneWriteClient{snapshot: validHerdrControlplaneSnapshot()}
	secondConfig := validHerdrControlplaneConfig()
	secondConfig.Runtime.TabID = "workspace-1:tab-2"
	secondConfig.Runtime.PaneID = paneID
	secondClient.snapshot.Tabs = append(secondClient.snapshot.Tabs, multiplexer.HerdrTabSnapshot{ID: "workspace-1:tab-2", WorkspaceID: "workspace-1"})
	secondClient.snapshot.Panes[0].ID = paneID
	secondClient.snapshot.Panes[0].TabID = "workspace-1:tab-2"
	secondUnregister := RegisterHerdrHandAdapter(paneID, HerdrHandAdapter{
		HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
			Backend: multiplexer.HerdrBackend{Config: secondConfig, Client: secondClient},
		},
	})
	t.Cleanup(secondUnregister)

	firstTarget := Target{Hand: HandAttachment{Kind: HandKindHerdr, Address: paneID, HerdrRuntimeID: firstConfig.Runtime}}
	firstAdapter, err := DefaultHandAdapter(firstTarget)
	if err != nil {
		t.Fatalf("DefaultHandAdapter(first tab) error = %v", err)
	}
	if err := firstAdapter.Deliver(firstTarget, PaneDelivery{Content: "first"}); err != nil {
		t.Fatalf("Deliver(first tab) error = %v", err)
	}
	secondTarget := Target{Hand: HandAttachment{Kind: HandKindHerdr, Address: paneID, HerdrRuntimeID: secondConfig.Runtime}}
	secondAdapter, err := DefaultHandAdapter(secondTarget)
	if err != nil {
		t.Fatalf("DefaultHandAdapter(second tab) error = %v", err)
	}
	if err := secondAdapter.Deliver(secondTarget, PaneDelivery{Content: "second"}); err != nil {
		t.Fatalf("Deliver(second tab) error = %v", err)
	}
	if firstClient.writeTextCalls != 1 || secondClient.writeTextCalls != 1 {
		t.Fatalf("write calls first=%d second=%d, want tab-isolated deliveries", firstClient.writeTextCalls, secondClient.writeTextCalls)
	}
}

func TestDefaultHandAdapterRejectsAmbiguousReducedHerdrTabFallback(t *testing.T) {
	owner := "tab-ambiguous-owner"
	paneID := "shared-native-pane-reduced"
	tabOne := "workspace-1:tab-1"
	tabTwo := "workspace-1:tab-2"

	firstClient := &fakeHerdrControlplaneWriteClient{snapshot: validHerdrControlplaneSnapshot()}
	firstConfig := validHerdrControlplaneConfig()
	firstConfig.Runtime.TabID = tabOne
	firstConfig.Runtime.PaneID = paneID
	firstClient.snapshot.Panes[0].ID = paneID
	firstClient.snapshot.Panes[0].TabID = tabOne

	secondClient := &fakeHerdrControlplaneWriteClient{snapshot: validHerdrControlplaneSnapshot()}
	secondConfig := validHerdrControlplaneConfig()
	secondConfig.Runtime.TabID = tabTwo
	secondConfig.Runtime.PaneID = paneID
	secondClient.snapshot.Tabs = append(secondClient.snapshot.Tabs, multiplexer.HerdrTabSnapshot{ID: tabTwo, WorkspaceID: "workspace-1"})
	secondClient.snapshot.Panes[0].ID = paneID
	secondClient.snapshot.Panes[0].TabID = tabTwo

	replacement := ReplaceHerdrHandAdaptersForOwnerRuntimeCollect(owner, map[multiplexer.HerdrRuntimeIdentity]HerdrHandAdapter{
		firstConfig.Runtime: {
			HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
				Backend: multiplexer.HerdrBackend{Config: firstConfig, Client: firstClient},
			},
		},
		secondConfig.Runtime: {
			HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
				Backend: multiplexer.HerdrBackend{Config: secondConfig, Client: secondClient},
			},
		},
	})
	replacement.DisplacedCleanup()
	t.Cleanup(replacement.Cleanup)

	firstTarget := Target{Hand: HandAttachment{Kind: HandKindHerdr, Address: paneID, HerdrRuntimeID: firstConfig.Runtime}}
	firstAdapter, err := DefaultHandAdapter(firstTarget)
	if err != nil {
		t.Fatalf("DefaultHandAdapter(first exact) error = %v", err)
	}
	if err := firstAdapter.Deliver(firstTarget, PaneDelivery{Content: "first"}); err != nil {
		t.Fatalf("Deliver(first exact) error = %v", err)
	}

	secondTarget := Target{Hand: HandAttachment{Kind: HandKindHerdr, Address: paneID, HerdrRuntimeID: secondConfig.Runtime}}
	secondAdapter, err := DefaultHandAdapter(secondTarget)
	if err != nil {
		t.Fatalf("DefaultHandAdapter(second exact) error = %v", err)
	}
	if err := secondAdapter.Deliver(secondTarget, PaneDelivery{Content: "second"}); err != nil {
		t.Fatalf("Deliver(second exact) error = %v", err)
	}
	if firstClient.writeTextCalls != 1 || secondClient.writeTextCalls != 1 {
		t.Fatalf("exact write calls first=%d second=%d, want isolated deliveries", firstClient.writeTextCalls, secondClient.writeTextCalls)
	}

	reducedTarget := Target{
		SessionName: firstConfig.Runtime.SessionName,
		Hand:        HandAttachment{Kind: HandKindHerdr, Address: paneID},
	}
	if _, err := DefaultHandAdapter(reducedTarget); err == nil {
		t.Fatal("DefaultHandAdapter(reduced ambiguous) succeeded, want fail-closed ambiguity")
	}
	if firstClient.writeTextCalls != 1 || secondClient.writeTextCalls != 1 {
		t.Fatalf("ambiguous reduced lookup delivered unexpectedly: first=%d second=%d", firstClient.writeTextCalls, secondClient.writeTextCalls)
	}

	uniqueReplacement := ReplaceHerdrHandAdaptersForOwnerRuntimeCollect(owner, map[multiplexer.HerdrRuntimeIdentity]HerdrHandAdapter{
		firstConfig.Runtime: {
			HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
				Backend: multiplexer.HerdrBackend{Config: firstConfig, Client: firstClient},
			},
		},
	})
	uniqueReplacement.DisplacedCleanup()
	t.Cleanup(uniqueReplacement.Cleanup)

	uniqueAdapter, err := DefaultHandAdapter(reducedTarget)
	if err != nil {
		t.Fatalf("DefaultHandAdapter(reduced unique) error = %v", err)
	}
	if err := uniqueAdapter.Deliver(reducedTarget, PaneDelivery{Content: "unique"}); err != nil {
		t.Fatalf("Deliver(reduced unique) error = %v", err)
	}
	if firstClient.writeTextCalls != 2 || secondClient.writeTextCalls != 1 {
		t.Fatalf("unique fallback write calls first=%d second=%d, want first restored only", firstClient.writeTextCalls, secondClient.writeTextCalls)
	}
}

func TestReplaceHerdrHandAdaptersForOwnerAtomicallySwapsVisibleSet(t *testing.T) {
	owner := "runtime-owner"
	firstClient := &fakeHerdrControlplaneWriteClient{snapshot: validHerdrControlplaneSnapshot()}
	firstConfig := validHerdrControlplaneConfig()
	firstConfig.Runtime.PaneID = "pane-old"
	firstClient.snapshot.Panes[0].ID = "pane-old"
	firstCleanup := ReplaceHerdrHandAdaptersForOwner(owner, map[string]HerdrHandAdapter{
		"pane-old": {
			HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
				Backend: multiplexer.HerdrBackend{Config: firstConfig, Client: firstClient},
			},
		},
	})
	t.Cleanup(firstCleanup)
	oldTarget := Target{Hand: HandAttachment{Kind: HandKindHerdr, Address: "pane-old", HerdrRuntimeID: firstConfig.Runtime}}
	if _, err := DefaultHandAdapter(oldTarget); err != nil {
		t.Fatalf("DefaultHandAdapter(old) error = %v", err)
	}

	secondClient := &fakeHerdrControlplaneWriteClient{snapshot: validHerdrControlplaneSnapshot()}
	secondConfig := validHerdrControlplaneConfig()
	secondConfig.Runtime.PaneID = "pane-new"
	secondClient.snapshot.Panes[0].ID = "pane-new"
	secondCleanup := ReplaceHerdrHandAdaptersForOwner(owner, map[string]HerdrHandAdapter{
		"pane-new": {
			HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
				Backend: multiplexer.HerdrBackend{Config: secondConfig, Client: secondClient},
			},
		},
	})
	t.Cleanup(secondCleanup)

	if _, err := DefaultHandAdapter(oldTarget); err == nil {
		t.Fatal("old owner adapter remained visible after owner-scoped replacement")
	}
	newTarget := Target{Hand: HandAttachment{Kind: HandKindHerdr, Address: "pane-new", HerdrRuntimeID: secondConfig.Runtime}}
	adapter, err := DefaultHandAdapter(newTarget)
	if err != nil {
		t.Fatalf("DefaultHandAdapter(new) error = %v", err)
	}
	if err := adapter.Deliver(newTarget, PaneDelivery{Content: "new"}); err != nil {
		t.Fatalf("Deliver(new) error = %v", err)
	}
	if secondClient.writeTextCalls != 1 {
		t.Fatalf("new adapter write calls = %d, want visible replacement", secondClient.writeTextCalls)
	}
}

func TestReplaceHerdrHandAdaptersForOwnerRunsDisplacedCleanupOutsideLockOnce(t *testing.T) {
	owner := "runtime-owner-cleanup"
	paneID := "pane-cleanup-old"
	firstConfig := validHerdrControlplaneConfig()
	firstConfig.Runtime.PaneID = paneID
	firstCleanup := ReplaceHerdrHandAdaptersForOwner(owner, map[string]HerdrHandAdapter{
		paneID: {
			HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
				Backend: multiplexer.HerdrBackend{Config: firstConfig, Client: &fakeHerdrControlplaneWriteClient{snapshot: validHerdrControlplaneSnapshot()}},
			},
		},
	})
	t.Cleanup(firstCleanup)

	expected := make(map[herdrHandAdapterKey]bool)
	for _, key := range herdrHandAdapterKeysForRegistration(firstConfig.Runtime, paneID) {
		expected[key] = true
	}
	seen := make(map[herdrHandAdapterKey]int)
	herdrHandAdapterCleanupHook = func(key herdrHandAdapterKey, _ *registeredHerdrHandAdapter) {
		if !registeredHerdrHandAdaptersMu.TryLock() {
			t.Fatalf("cleanup hook for %#v ran while registry lock was held", key)
		}
		registeredHerdrHandAdaptersMu.Unlock()
		seen[key]++
	}
	t.Cleanup(func() {
		herdrHandAdapterCleanupHook = nil
	})

	nextConfig := firstConfig
	nextConfig.Runtime.PaneID = "pane-cleanup-new"
	nextCleanup := ReplaceHerdrHandAdaptersForOwner(owner, map[string]HerdrHandAdapter{
		nextConfig.Runtime.PaneID: {
			HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
				Backend: multiplexer.HerdrBackend{Config: nextConfig, Client: &fakeHerdrControlplaneWriteClient{snapshot: validHerdrControlplaneSnapshot()}},
			},
		},
	})
	t.Cleanup(nextCleanup)

	if len(seen) != len(expected) {
		t.Fatalf("cleanup hook count = %d, want %d (%#v)", len(seen), len(expected), seen)
	}
	for key := range expected {
		if seen[key] != 1 {
			t.Fatalf("cleanup hook for %#v ran %d times, want exactly once", key, seen[key])
		}
	}
}

func TestDefaultHandAdapterRejectsGenerationStaleHandleBeforeWrite(t *testing.T) {
	owner := "runtime-owner-stale-handle"
	paneID := "pane-stale-handle"
	client := &fakeHerdrControlplaneWriteClient{snapshot: validHerdrControlplaneSnapshot()}
	cfg := validHerdrControlplaneConfig()
	cfg.Runtime.PaneID = paneID
	client.snapshot.Panes[0].ID = paneID
	cleanup := ReplaceHerdrHandAdaptersForOwner(owner, map[string]HerdrHandAdapter{
		paneID: {
			HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
				Backend: multiplexer.HerdrBackend{Config: cfg, Client: client},
			},
		},
	})
	t.Cleanup(cleanup)
	target := Target{Hand: HandAttachment{Kind: HandKindHerdr, Address: paneID, HerdrRuntimeID: cfg.Runtime}}
	adapter, err := DefaultHandAdapter(target)
	if err != nil {
		t.Fatalf("DefaultHandAdapter() error = %v", err)
	}

	multiplexer.LockHerdrPublicationWrite()
	multiplexer.AdvanceHerdrPublicationGenerationLocked()
	multiplexer.UnlockHerdrPublicationWrite()

	err = adapter.Deliver(target, PaneDelivery{Content: "stale"})
	if err == nil || !strings.Contains(err.Error(), "stale herdr hand adapter generation") {
		t.Fatalf("Deliver(stale handle) error = %v, want stale generation error", err)
	}
	if client.writeTextCalls != 0 || client.sendKeyCalls != 0 {
		t.Fatalf("stale handle wrote to Herdr: write=%d key=%d", client.writeTextCalls, client.sendKeyCalls)
	}
}

func TestOwnerReplacementCleanupHookCanReenterAdapterLookupAfterPublicationUnlock(t *testing.T) {
	owner := "runtime-owner-reentrant-cleanup"
	oldPaneID := "pane-reentrant-old"
	oldConfig := validHerdrControlplaneConfig()
	oldConfig.Runtime.PaneID = oldPaneID
	firstCleanup := ReplaceHerdrHandAdaptersForOwner(owner, map[string]HerdrHandAdapter{
		oldPaneID: {
			HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
				Backend: multiplexer.HerdrBackend{Config: oldConfig, Client: &fakeHerdrControlplaneWriteClient{snapshot: validHerdrControlplaneSnapshot()}},
			},
		},
	})
	t.Cleanup(firstCleanup)

	newPaneID := "pane-reentrant-new"
	newConfig := oldConfig
	newConfig.Runtime.PaneID = newPaneID
	newClient := &fakeHerdrControlplaneWriteClient{snapshot: validHerdrControlplaneSnapshot()}
	newClient.snapshot.Panes[0].ID = newPaneID
	newTarget := Target{Hand: HandAttachment{Kind: HandKindHerdr, Address: newPaneID, HerdrRuntimeID: newConfig.Runtime}}
	hookDone := make(chan error, 8)
	herdrHandAdapterCleanupHook = func(herdrHandAdapterKey, *registeredHerdrHandAdapter) {
		_, err := DefaultHandAdapter(newTarget)
		hookDone <- err
	}
	t.Cleanup(func() {
		herdrHandAdapterCleanupHook = nil
	})

	multiplexer.LockHerdrPublicationWrite()
	replacement := ReplaceHerdrHandAdaptersForOwnerCollect(owner, map[string]HerdrHandAdapter{
		newPaneID: {
			HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
				Backend: multiplexer.HerdrBackend{Config: newConfig, Client: newClient},
			},
		},
	})
	multiplexer.AdvanceHerdrPublicationGenerationLocked()
	multiplexer.UnlockHerdrPublicationWrite()
	t.Cleanup(replacement.Cleanup)
	replacement.DisplacedCleanup()

	select {
	case err := <-hookDone:
		if err != nil {
			t.Fatalf("cleanup hook reentrant DefaultHandAdapter() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cleanup hook reentrant adapter lookup deadlocked")
	}
}

func TestDefaultHandAdapterReverseCleanupKeepsOlderOverlappingHerdrRuntime(t *testing.T) {
	paneID := "shared-native-pane-reverse"
	firstClient := &fakeHerdrControlplaneWriteClient{snapshot: validHerdrControlplaneSnapshot()}
	firstConfig := validHerdrControlplaneConfig()
	firstConfig.Runtime.SocketPath = "/tmp/herdr-reverse-first.sock"
	firstConfig.Runtime.WorkspaceID = "workspace-reverse-first"
	firstConfig.Runtime.PaneID = paneID
	firstConfig.Policy.AllowedSocketPaths = []string{"/tmp/herdr-reverse-first.sock"}
	firstConfig.Policy.AllowedWorkspaceIDs = []string{"workspace-reverse-first"}
	firstClient.snapshot.Workspaces[0].ID = "workspace-reverse-first"
	firstClient.snapshot.Tabs[0].WorkspaceID = "workspace-reverse-first"
	firstClient.snapshot.Panes[0].WorkspaceID = "workspace-reverse-first"
	firstClient.snapshot.Panes[0].ID = paneID
	firstUnregister := RegisterHerdrHandAdapter(paneID, HerdrHandAdapter{
		HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
			Backend: multiplexer.HerdrBackend{Config: firstConfig, Client: firstClient},
		},
	})
	t.Cleanup(firstUnregister)

	secondConfig := firstConfig
	secondConfig.Runtime.SocketPath = "/tmp/herdr-reverse-second.sock"
	secondConfig.Runtime.WorkspaceID = "workspace-reverse-second"
	secondConfig.Policy.AllowedSocketPaths = []string{"/tmp/herdr-reverse-second.sock"}
	secondConfig.Policy.AllowedWorkspaceIDs = []string{"workspace-reverse-second"}
	secondUnregister := RegisterHerdrHandAdapter(paneID, HerdrHandAdapter{
		HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
			Backend: multiplexer.HerdrBackend{Config: secondConfig, Client: &fakeHerdrControlplaneWriteClient{snapshot: validHerdrControlplaneSnapshot()}},
		},
	})
	t.Cleanup(secondUnregister)

	secondUnregister()
	firstTarget := Target{
		Hand: HandAttachment{Kind: HandKindHerdr, Address: paneID, HerdrRuntimeID: firstConfig.Runtime},
	}
	if _, err := DefaultHandAdapter(firstTarget); err != nil {
		t.Fatalf("first runtime adapter was removed by newer cleanup: %v", err)
	}
}

func TestHerdrHandAdapterCleanupDoesNotDeleteExactReplacementAfterObservation(t *testing.T) {
	paneID := "replacement-exact-pane"
	firstConfig := validHerdrControlplaneConfig()
	firstConfig.Runtime.SocketPath = "/tmp/herdr-exact-first.sock"
	firstConfig.Runtime.WorkspaceID = "workspace-exact"
	firstConfig.Runtime.PaneID = paneID
	firstConfig.Policy.AllowedSocketPaths = []string{firstConfig.Runtime.SocketPath}
	firstConfig.Policy.AllowedWorkspaceIDs = []string{firstConfig.Runtime.WorkspaceID}
	firstCleanup := RegisterHerdrHandAdapter(paneID, HerdrHandAdapter{
		HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
			Backend: multiplexer.HerdrBackend{Config: firstConfig, Client: &fakeHerdrControlplaneWriteClient{snapshot: validHerdrControlplaneSnapshot()}},
		},
	})
	t.Cleanup(firstCleanup)

	replacementClient := &fakeHerdrControlplaneWriteClient{snapshot: validHerdrControlplaneSnapshot()}
	replacementClient.snapshot.Workspaces[0].ID = firstConfig.Runtime.WorkspaceID
	replacementClient.snapshot.Tabs[0].WorkspaceID = firstConfig.Runtime.WorkspaceID
	replacementClient.snapshot.Panes[0].WorkspaceID = firstConfig.Runtime.WorkspaceID
	replacementClient.snapshot.Panes[0].ID = paneID
	replacementCleanup := func() {}
	exactKey := herdrHandAdapterKeyForRuntime(firstConfig.Runtime, paneID)
	installedReplacement := false
	herdrHandAdapterCleanupHook = func(key herdrHandAdapterKey, _ *registeredHerdrHandAdapter) {
		if installedReplacement || key != exactKey {
			return
		}
		installedReplacement = true
		replacementCleanup = RegisterHerdrHandAdapter(paneID, HerdrHandAdapter{
			HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
				Backend: multiplexer.HerdrBackend{Config: firstConfig, Client: replacementClient},
			},
		})
	}
	t.Cleanup(func() {
		herdrHandAdapterCleanupHook = nil
		replacementCleanup()
	})

	firstCleanup()
	if !installedReplacement {
		t.Fatal("cleanup hook did not install replacement between observation and removal")
	}

	target := Target{Hand: HandAttachment{Kind: HandKindHerdr, Address: paneID, HerdrRuntimeID: firstConfig.Runtime}}
	adapter, err := DefaultHandAdapter(target)
	if err != nil {
		t.Fatalf("DefaultHandAdapter(exact replacement) error = %v", err)
	}
	if err := adapter.Deliver(target, PaneDelivery{Content: "replacement"}); err != nil {
		t.Fatalf("Deliver(exact replacement) error = %v", err)
	}
	if replacementClient.writeTextCalls != 1 {
		t.Fatalf("replacement write calls = %d, want 1", replacementClient.writeTextCalls)
	}
}

func TestHerdrHandAdapterCleanupDoesNotDeleteCompatibilityReplacementAfterObservation(t *testing.T) {
	paneID := "replacement-compat-pane"
	firstConfig := validHerdrControlplaneConfig()
	firstConfig.Runtime.SocketPath = "/tmp/herdr-compat-first.sock"
	firstConfig.Runtime.WorkspaceID = "workspace-compat-first"
	firstConfig.Runtime.PaneID = paneID
	firstConfig.Policy.AllowedSocketPaths = []string{firstConfig.Runtime.SocketPath}
	firstConfig.Policy.AllowedWorkspaceIDs = []string{firstConfig.Runtime.WorkspaceID}
	firstCleanup := RegisterHerdrHandAdapter(paneID, HerdrHandAdapter{
		HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
			Backend: multiplexer.HerdrBackend{Config: firstConfig, Client: &fakeHerdrControlplaneWriteClient{snapshot: validHerdrControlplaneSnapshot()}},
		},
	})
	t.Cleanup(firstCleanup)

	replacementConfig := firstConfig
	replacementConfig.Runtime.SocketPath = "/tmp/herdr-compat-second.sock"
	replacementConfig.Runtime.WorkspaceID = "workspace-compat-second"
	replacementConfig.Policy.AllowedSocketPaths = []string{replacementConfig.Runtime.SocketPath}
	replacementConfig.Policy.AllowedWorkspaceIDs = []string{replacementConfig.Runtime.WorkspaceID}
	replacementClient := &fakeHerdrControlplaneWriteClient{snapshot: validHerdrControlplaneSnapshot()}
	replacementClient.snapshot.Workspaces[0].ID = replacementConfig.Runtime.WorkspaceID
	replacementClient.snapshot.Tabs[0].WorkspaceID = replacementConfig.Runtime.WorkspaceID
	replacementClient.snapshot.Panes[0].WorkspaceID = replacementConfig.Runtime.WorkspaceID
	replacementClient.snapshot.Panes[0].ID = paneID
	replacementCleanup := func() {}
	compatKey := herdrHandAdapterKey{SessionName: firstConfig.Runtime.SessionName, PaneID: paneID}
	installedReplacement := false
	herdrHandAdapterCleanupHook = func(key herdrHandAdapterKey, _ *registeredHerdrHandAdapter) {
		if installedReplacement || key != compatKey {
			return
		}
		installedReplacement = true
		replacementCleanup = RegisterHerdrHandAdapter(paneID, HerdrHandAdapter{
			HerdrInteractiveDeliveryAdapter: HerdrInteractiveDeliveryAdapter{
				Backend: multiplexer.HerdrBackend{Config: replacementConfig, Client: replacementClient},
			},
		})
	}
	t.Cleanup(func() {
		herdrHandAdapterCleanupHook = nil
		replacementCleanup()
	})

	firstCleanup()
	if !installedReplacement {
		t.Fatal("cleanup hook did not install compatibility replacement between observation and removal")
	}

	target := Target{
		SessionName: firstConfig.Runtime.SessionName,
		Hand:        HandAttachment{Kind: HandKindHerdr, Address: paneID},
	}
	adapter, err := DefaultHandAdapter(target)
	if err != nil {
		t.Fatalf("DefaultHandAdapter(compat replacement) error = %v", err)
	}
	if err := adapter.Deliver(target, PaneDelivery{Content: "replacement"}); err != nil {
		t.Fatalf("Deliver(compat replacement) error = %v", err)
	}
	if replacementClient.writeTextCalls != 1 {
		t.Fatalf("replacement write calls = %d, want 1", replacementClient.writeTextCalls)
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
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
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
			ComplianceDecision:      multiplexer.HerdrComplianceDecisionRecorded,
			ComplianceRecord:        multiplexer.HerdrComplianceRecord{Decision: multiplexer.HerdrComplianceDecisionRecorded, AuthorizedBy: "test", DecisionID: "test", DecidedAt: now.Add(-time.Hour), RevalidatedAt: now, CurrentReferences: []string{"test"}},
			ComplianceNow:           func() time.Time { return now },
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
