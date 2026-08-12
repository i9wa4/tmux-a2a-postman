package herdrruntime_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/controlplane"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/herdrruntime"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/multiplexer"
)

func TestRuntimeDiscoverRegistersProductionHerdrDeliveryAndOwnership(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	client := &fakeRuntimeHerdrClient{snapshot: validRuntimeHerdrSnapshot()}
	cfg := config.DefaultConfig()
	cfg.NotificationTemplate = "notice {node}"
	cfg.Nodes = map[string]config.NodeConfig{"worker": {}}
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)

	nodes, collisions, err := rt.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(collisions) != 0 {
		t.Fatalf("Discover() collisions = %#v, want none", collisions)
	}
	nodeInfo, ok := nodes[sessionName+":worker"]
	if !ok {
		t.Fatalf("Discover() nodes = %#v, want work:worker", nodes)
	}
	if nodeInfo.Backend != string(multiplexer.BackendKindHerdr) || nodeInfo.Runtime != "codex" {
		t.Fatalf("nodeInfo = %#v, want Herdr codex node", nodeInfo)
	}
	rt.ReconcileFinalNodes(nodes)

	ownershipBackend, err := multiplexer.OwnershipBackendForKind(multiplexer.BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	if err := ownershipBackend.SetPaneOwnerMarker(context.Background(), multiplexer.HerdrPaneID("workspace-1:pane-1"), contextID); err != nil {
		t.Fatalf("SetPaneOwnerMarker() error = %v", err)
	}
	if client.setPaneMetadataPane != "workspace-1:pane-1" || client.setPaneMetadataValue != contextID {
		t.Fatalf("pane owner mutation pane=%q value=%q, want production Herdr ownership claim", client.setPaneMetadataPane, client.setPaneMetadataValue)
	}

	result, err := message.DeliverSystemMessageDirectResult(
		"20260414-120000-r1234-from-postman-to-worker.md",
		nodeInfo,
		"worker",
		"postman",
		contextID,
		"body",
		cfg,
		nil,
		map[string]discovery.NodeInfo{sessionName + ":worker": nodeInfo},
		nil,
	)
	if err != nil {
		t.Fatalf("DeliverSystemMessageDirectResult() error = %v", err)
	}
	if !result.Delivered {
		t.Fatal("DeliverSystemMessageDirectResult() delivered = false, want true")
	}
	if client.writeTextCalls != 1 || client.writeTextPane != "workspace-1:pane-1" {
		t.Fatalf("Herdr write calls = %d pane=%q, want bootstrap-registered delivery", client.writeTextCalls, client.writeTextPane)
	}
	if client.sendKeyCalls != 2 || client.sendKeyKey != multiplexer.HerdrKeySubmit {
		t.Fatalf("Herdr submit calls = %d key=%q, want Codex default submit count", client.sendKeyCalls, client.sendKeyKey)
	}
}

func TestRuntimeReconcileKeepsSamePaneIDInDifferentTabsRoutable(t *testing.T) {
	contextID := "ctx-main"
	sessionName := "work"
	paneID := "workspace-1:pane-shared"
	tabOne := "workspace-1:tab-1"
	tabTwo := "workspace-1:tab-2"
	snapshot := runtimeHerdrSnapshotFor(sessionName, "workspace-1", tabOne, paneID, "")
	snapshot.Tabs = append(snapshot.Tabs, multiplexer.HerdrTabSnapshot{
		ID:          tabTwo,
		WorkspaceID: "workspace-1",
		Metadata:    map[string]string{},
	})
	snapshot.Panes[0].Metadata[multiplexer.HerdrPaneContextIDMetadataKey] = "ctx-tab-1"
	snapshot.Panes = append(snapshot.Panes, multiplexer.HerdrPaneSnapshot{
		ID:             paneID,
		WorkspaceID:    "workspace-1",
		TabID:          tabTwo,
		Metadata:       map[string]string{"postman.node": "critic", multiplexer.HerdrPaneContextIDMetadataKey: "ctx-tab-2"},
		Env:            map[string]string{},
		ProcessInfo:    multiplexer.HerdrPaneProcessInfo{ForegroundProcesses: []multiplexer.HerdrProcessInfo{{Name: "codex"}}},
		PostmanNode:    "critic",
		PostmanSession: sessionName,
	})
	client := &fakeRuntimeHerdrClient{snapshot: snapshot}
	cfg := config.DefaultConfig()
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)

	nodes := map[string]discovery.NodeInfo{
		sessionName + ":worker": {
			PaneID:           paneID,
			SessionName:      sessionName,
			Backend:          string(multiplexer.BackendKindHerdr),
			HerdrSocketPath:  cfg.Herdr.SocketPath,
			HerdrWorkspaceID: "workspace-1",
			HerdrTabID:       tabOne,
		},
		sessionName + ":critic": {
			PaneID:           paneID,
			SessionName:      sessionName,
			Backend:          string(multiplexer.BackendKindHerdr),
			HerdrSocketPath:  cfg.Herdr.SocketPath,
			HerdrWorkspaceID: "workspace-1",
			HerdrTabID:       tabTwo,
		},
	}
	rt.ReconcileFinalNodes(nodes)

	workerTarget := controlplane.TargetForNode(sessionName+":worker", nodes[sessionName+":worker"])
	workerAdapter, err := controlplane.DefaultHandAdapter(workerTarget)
	if err != nil {
		t.Fatalf("DefaultHandAdapter(worker) error = %v", err)
	}
	if err := workerAdapter.Deliver(workerTarget, controlplane.PaneDelivery{Content: "worker"}); err != nil {
		t.Fatalf("Deliver(worker) error = %v", err)
	}
	criticTarget := controlplane.TargetForNode(sessionName+":critic", nodes[sessionName+":critic"])
	criticAdapter, err := controlplane.DefaultHandAdapter(criticTarget)
	if err != nil {
		t.Fatalf("DefaultHandAdapter(critic) error = %v", err)
	}
	if err := criticAdapter.Deliver(criticTarget, controlplane.PaneDelivery{Content: "critic"}); err != nil {
		t.Fatalf("Deliver(critic) error = %v", err)
	}
	if client.writeTextCalls != 2 {
		t.Fatalf("writeTextCalls = %d, want both tab-distinct adapters routable", client.writeTextCalls)
	}

	ownershipBackend := rt.OwnershipBackend()
	workerPane := multiplexer.HerdrPaneIDForRuntime(multiplexer.HerdrRuntimeIdentity{
		SocketPath:  cfg.Herdr.SocketPath,
		SessionName: sessionName,
		WorkspaceID: "workspace-1",
		TabID:       tabOne,
		PaneID:      paneID,
	}, paneID)
	if got, err := ownershipBackend.PaneOwnerMarker(context.Background(), workerPane); err != nil || got != "ctx-tab-1" {
		t.Fatalf("PaneOwnerMarker(worker tab) = %q, %v; want ctx-tab-1", got, err)
	}
	criticPane := multiplexer.HerdrPaneIDForRuntime(multiplexer.HerdrRuntimeIdentity{
		SocketPath:  cfg.Herdr.SocketPath,
		SessionName: sessionName,
		WorkspaceID: "workspace-1",
		TabID:       tabTwo,
		PaneID:      paneID,
	}, paneID)
	if got, err := ownershipBackend.PaneOwnerMarker(context.Background(), criticPane); err != nil || got != "ctx-tab-2" {
		t.Fatalf("PaneOwnerMarker(critic tab) = %q, %v; want ctx-tab-2", got, err)
	}
	if err := ownershipBackend.SetPaneOwnerMarker(context.Background(), workerPane, contextID); err != nil {
		t.Fatalf("SetPaneOwnerMarker(worker tab) error = %v", err)
	}
	if err := ownershipBackend.SetPaneOwnerMarker(context.Background(), criticPane, contextID); err != nil {
		t.Fatalf("SetPaneOwnerMarker(critic tab) error = %v", err)
	}
}

func TestRuntimeReconcileKeepsIndependentTabRuntimeAdaptersIsolated(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	paneID := "workspace-1:pane-shared"
	tabOne := "workspace-1:tab-1"
	tabTwo := "workspace-1:tab-2"
	firstClient := &fakeRuntimeHerdrClient{snapshot: runtimeHerdrSnapshotFor(sessionName, "workspace-1", tabOne, paneID, "")}
	firstCfg := config.DefaultConfig()
	firstCfg.Herdr = validRuntimeHerdrConfig()
	firstRuntime, err := herdrruntime.New(firstCfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return firstClient, nil
	})
	if err != nil {
		t.Fatalf("New(first) error = %v", err)
	}
	t.Cleanup(firstRuntime.Close)

	secondClient := &fakeRuntimeHerdrClient{snapshot: runtimeHerdrSnapshotFor(sessionName, "workspace-1", tabTwo, paneID, "")}
	secondCfg := config.DefaultConfig()
	secondCfg.Herdr = validRuntimeHerdrConfig()
	secondRuntime, err := herdrruntime.New(secondCfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return secondClient, nil
	})
	if err != nil {
		t.Fatalf("New(second) error = %v", err)
	}
	t.Cleanup(secondRuntime.Close)

	firstNodes, _, err := firstRuntime.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover(first) error = %v", err)
	}
	firstRuntime.ReconcileFinalNodes(firstNodes)
	firstTarget := controlplane.TargetForNode(sessionName+":worker", firstNodes[sessionName+":worker"])
	firstAdapter, err := controlplane.DefaultHandAdapter(firstTarget)
	if err != nil {
		t.Fatalf("DefaultHandAdapter(first after first reconcile) error = %v", err)
	}
	if err := firstAdapter.Deliver(firstTarget, controlplane.PaneDelivery{Content: "first-before"}); err != nil {
		t.Fatalf("Deliver(first before second reconcile) error = %v", err)
	}

	secondNodes, _, err := secondRuntime.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover(second) error = %v", err)
	}
	secondRuntime.ReconcileFinalNodes(secondNodes)
	secondTarget := controlplane.TargetForNode(sessionName+":worker", secondNodes[sessionName+":worker"])
	secondAdapter, err := controlplane.DefaultHandAdapter(secondTarget)
	if err != nil {
		t.Fatalf("DefaultHandAdapter(second after second reconcile) error = %v", err)
	}
	if err := secondAdapter.Deliver(secondTarget, controlplane.PaneDelivery{Content: "second"}); err != nil {
		t.Fatalf("Deliver(second) error = %v", err)
	}
	firstAdapter, err = controlplane.DefaultHandAdapter(firstTarget)
	if err != nil {
		t.Fatalf("DefaultHandAdapter(first after second reconcile) error = %v", err)
	}
	if err := firstAdapter.Deliver(firstTarget, controlplane.PaneDelivery{Content: "first-after"}); err != nil {
		t.Fatalf("Deliver(first after second reconcile) error = %v", err)
	}

	firstRuntime.ReconcileFinalNodes(firstNodes)
	if _, err := controlplane.DefaultHandAdapter(secondTarget); err != nil {
		t.Fatalf("DefaultHandAdapter(second after first refresh) error = %v", err)
	}

	secondRuntime.Close()
	if _, err := controlplane.DefaultHandAdapter(secondTarget); err == nil {
		t.Fatal("closed second tab runtime adapter remained registered")
	}
	if _, err := controlplane.DefaultHandAdapter(firstTarget); err != nil {
		t.Fatalf("DefaultHandAdapter(first after second close) error = %v", err)
	}
}

func TestRuntimeDiscoverPrunesStalePaneRegistrations(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	client := &fakeRuntimeHerdrClient{snapshot: validRuntimeHerdrSnapshot()}
	cfg := config.DefaultConfig()
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)

	nodes, _, err := rt.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover(initial) error = %v", err)
	}
	rt.ReconcileFinalNodes(nodes)
	staleTarget := controlplane.Target{Hand: controlplane.HandAttachment{Kind: controlplane.HandKindHerdr, Address: "workspace-1:pane-1"}}
	if _, err := controlplane.DefaultHandAdapter(staleTarget); err != nil {
		t.Fatalf("DefaultHandAdapter(initial) error = %v", err)
	}

	next := validRuntimeHerdrSnapshot()
	next.Panes[0].ID = "workspace-1:pane-2"
	client.snapshot = next
	nodes, _, err = rt.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover(second) error = %v", err)
	}
	rt.ReconcileFinalNodes(nodes)
	if _, err := controlplane.DefaultHandAdapter(staleTarget); err == nil {
		t.Fatal("stale Herdr pane adapter remained registered after rediscovery")
	}
	freshTarget := controlplane.Target{Hand: controlplane.HandAttachment{Kind: controlplane.HandKindHerdr, Address: "workspace-1:pane-2"}}
	if _, err := controlplane.DefaultHandAdapter(freshTarget); err != nil {
		t.Fatalf("DefaultHandAdapter(fresh) error = %v", err)
	}
	ownershipBackend, err := multiplexer.OwnershipBackendForKind(multiplexer.BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	if err := ownershipBackend.SetPaneOwnerMarker(context.Background(), multiplexer.HerdrPaneID("workspace-1:pane-1"), contextID); err == nil {
		t.Fatal("stale Herdr pane ownership backend remained routable")
	}
}

func TestRuntimeDiscoverDoesNotPublishRoutesBeforeFinalReconcile(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	client := &fakeRuntimeHerdrClient{snapshot: validRuntimeHerdrSnapshot()}
	cfg := config.DefaultConfig()
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)

	nodes, _, err := rt.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	nodeInfo, ok := nodes[sessionName+":worker"]
	if !ok {
		t.Fatalf("Discover() nodes = %#v, want work:worker candidate", nodes)
	}
	target := controlplane.TargetForNode(sessionName+":worker", nodeInfo)
	if _, err := controlplane.DefaultHandAdapter(target); err == nil {
		t.Fatal("Herdr hand adapter was published before final reconcile")
	}
	ownershipBackend, err := multiplexer.OwnershipBackendForKind(multiplexer.BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	if err := ownershipBackend.SetPaneOwnerMarker(context.Background(), multiplexer.HerdrPaneID(nodeInfo.PaneID), contextID); err == nil {
		t.Fatal("Herdr pane ownership route was published before final reconcile")
	}
	if err := ownershipBackend.SetSessionOwnerMarker(context.Background(), contextID, sessionName, 0); err == nil {
		t.Fatal("Herdr session ownership route was published before final reconcile")
	}

	rt.ReconcileFinalNodes(nodes)
	if _, err := controlplane.DefaultHandAdapter(target); err != nil {
		t.Fatalf("DefaultHandAdapter(after reconcile) error = %v", err)
	}
	if err := ownershipBackend.SetPaneOwnerMarker(context.Background(), multiplexer.HerdrPaneID(nodeInfo.PaneID), contextID); err != nil {
		t.Fatalf("SetPaneOwnerMarker(after reconcile) error = %v", err)
	}
}

func TestRuntimeStartupMarkerPublicationWaitsForFinalReconcile(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	client := &fakeRuntimeHerdrClient{snapshot: validRuntimeHerdrSnapshot()}
	cfg := config.DefaultConfig()
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)

	nodes, _, err := rt.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	nodeInfo, ok := nodes[sessionName+":worker"]
	if !ok {
		t.Fatalf("Discover() nodes = %#v, want work:worker candidate", nodes)
	}
	target := controlplane.TargetForNode(sessionName+":worker", nodeInfo)
	if _, err := controlplane.DefaultHandAdapter(target); err == nil {
		t.Fatal("Herdr candidate pane route was published before final reconcile")
	}
	if err := rt.SetSessionEnabledMarker(context.Background(), contextID, sessionName, true); err == nil {
		t.Fatal("SetSessionEnabledMarker() succeeded before final reconcile")
	}

	rt.ReconcileFinalNodes(nodes)
	if err := rt.SetSessionEnabledMarker(context.Background(), contextID, sessionName, true); err != nil {
		t.Fatalf("SetSessionEnabledMarker(after reconcile) error = %v", err)
	}
	if client.setWorkspaceMetadataWorkspace != "workspace-1" || client.setWorkspaceMetadataValue == "" {
		t.Fatalf("session marker workspace=%q value=%q, want Herdr startup marker publication", client.setWorkspaceMetadataWorkspace, client.setWorkspaceMetadataValue)
	}
	if _, err := controlplane.DefaultHandAdapter(target); err != nil {
		t.Fatalf("DefaultHandAdapter(after marker publication) error = %v", err)
	}
}

func TestRuntimeReconcileFinalNodesPrunesCrossBackendCollisionLoser(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	client := &fakeRuntimeHerdrClient{snapshot: validRuntimeHerdrSnapshot()}
	cfg := config.DefaultConfig()
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)

	nodes, _, err := rt.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover(initial) error = %v", err)
	}
	rt.ReconcileFinalNodes(nodes)
	loserTarget := controlplane.Target{Hand: controlplane.HandAttachment{Kind: controlplane.HandKindHerdr, Address: "workspace-1:pane-1"}}
	if _, err := controlplane.DefaultHandAdapter(loserTarget); err != nil {
		t.Fatalf("DefaultHandAdapter(initial) error = %v", err)
	}

	rt.ReconcileFinalNodes(map[string]discovery.NodeInfo{
		sessionName + ":worker": {
			PaneID:      "%tmux-winner",
			SessionName: sessionName,
			Backend:     string(multiplexer.BackendKindTmux),
		},
	})

	if _, err := controlplane.DefaultHandAdapter(loserTarget); err == nil {
		t.Fatal("cross-backend collision loser Herdr adapter remained registered")
	}
	ownershipBackend, err := multiplexer.OwnershipBackendForKind(multiplexer.BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	if err := ownershipBackend.SetPaneOwnerMarker(context.Background(), multiplexer.HerdrPaneID("workspace-1:pane-1"), contextID); err == nil {
		t.Fatal("cross-backend collision loser Herdr ownership route remained available")
	}
	if got, err := ownershipBackend.SessionOwnerMarker(context.Background(), sessionName); err != nil || got != "" {
		t.Fatalf("final-pruned Herdr session owner read = %q, %v; want empty workspace marker route", got, err)
	}
	if err := ownershipBackend.SetSessionOwnerMarker(context.Background(), contextID, sessionName, 0); err != nil {
		t.Fatalf("final-pruned Herdr session owner set error = %v, want workspace marker route", err)
	}
	if err := ownershipBackend.ClearSessionOwnerMarker(context.Background(), sessionName); err != nil {
		t.Fatalf("final-pruned Herdr session owner clear error = %v, want workspace marker route", err)
	}
}

func TestRuntimeReconcileFinalNodesPrunesFilteredHerdrPane(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	client := &fakeRuntimeHerdrClient{snapshot: validRuntimeHerdrSnapshot()}
	cfg := config.DefaultConfig()
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)

	nodes, _, err := rt.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover(initial) error = %v", err)
	}
	rt.ReconcileFinalNodes(nodes)
	filteredTarget := controlplane.Target{Hand: controlplane.HandAttachment{Kind: controlplane.HandKindHerdr, Address: "workspace-1:pane-1"}}
	if _, err := controlplane.DefaultHandAdapter(filteredTarget); err != nil {
		t.Fatalf("DefaultHandAdapter(initial) error = %v", err)
	}

	rt.ReconcileFinalNodes(map[string]discovery.NodeInfo{})

	if _, err := controlplane.DefaultHandAdapter(filteredTarget); err == nil {
		t.Fatal("activation-filtered Herdr hand adapter remained registered")
	}
	ownershipBackend, err := multiplexer.OwnershipBackendForKind(multiplexer.BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	if err := ownershipBackend.SetPaneOwnerMarker(context.Background(), multiplexer.HerdrPaneID("workspace-1:pane-1"), contextID); err == nil {
		t.Fatal("activation-filtered Herdr ownership route remained available")
	}
	if got, err := ownershipBackend.SessionOwnerMarker(context.Background(), sessionName); err != nil || got != "" {
		t.Fatalf("activation-filtered Herdr session owner read = %q, %v; want empty workspace marker route", got, err)
	}
	if err := ownershipBackend.SetSessionOwnerMarker(context.Background(), contextID, sessionName, 0); err != nil {
		t.Fatalf("activation-filtered Herdr session owner set error = %v, want workspace marker route", err)
	}
	if err := ownershipBackend.ClearSessionOwnerMarker(context.Background(), sessionName); err != nil {
		t.Fatalf("activation-filtered Herdr session owner clear error = %v, want workspace marker route", err)
	}
}

func TestRuntimeDiscoverErrorClearsStalePaneRegistrations(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	client := &fakeRuntimeHerdrClient{snapshot: validRuntimeHerdrSnapshot()}
	cfg := config.DefaultConfig()
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)

	nodes, _, err := rt.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover(initial) error = %v", err)
	}
	rt.ReconcileFinalNodes(nodes)
	staleTarget := controlplane.Target{Hand: controlplane.HandAttachment{Kind: controlplane.HandKindHerdr, Address: "workspace-1:pane-1"}}
	if _, err := controlplane.DefaultHandAdapter(staleTarget); err != nil {
		t.Fatalf("DefaultHandAdapter(initial) error = %v", err)
	}

	client.snapshotErr = errors.New("socket snapshot failed")
	if _, _, err := rt.Discover(context.Background(), baseDir, contextID); err == nil {
		t.Fatal("Discover(error) error = nil, want discovery failure")
	}
	if _, err := controlplane.DefaultHandAdapter(staleTarget); err == nil {
		t.Fatal("stale Herdr adapter remained registered after discovery error")
	}
	ownershipBackend, err := multiplexer.OwnershipBackendForKind(multiplexer.BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	if err := ownershipBackend.SetPaneOwnerMarker(context.Background(), multiplexer.HerdrPaneID("workspace-1:pane-1"), contextID); err == nil {
		t.Fatal("stale Herdr pane ownership route remained available after discovery error")
	}
}

func TestRuntimeReconcileFinalNodesIgnoresOlderSuccessfulDiscoveryAfterNewerSuccess(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	oldSnapshot := runtimeHerdrSnapshotFor("work", "workspace-1", "workspace-1:tab-old", "workspace-1:pane-old", "")
	oldSnapshot.Panes = append(oldSnapshot.Panes, multiplexer.HerdrPaneSnapshot{
		ID:             "workspace-1:pane-old-2",
		WorkspaceID:    "workspace-1",
		TabID:          "workspace-1:tab-old",
		Metadata:       map[string]string{"postman.node": "worker-2"},
		Env:            map[string]string{},
		ProcessInfo:    multiplexer.HerdrPaneProcessInfo{ForegroundProcesses: []multiplexer.HerdrProcessInfo{{Name: "codex"}}},
		PostmanNode:    "worker-2",
		PostmanSession: "work",
	})
	newSnapshot := runtimeHerdrSnapshotFor("work", "workspace-1", "workspace-1:tab-new", "workspace-1:pane-new", "")
	oldCall := newControlledRuntimeSnapshot(oldSnapshot, nil)
	newCall := newControlledRuntimeSnapshot(newSnapshot, nil)
	client := &sequencedRuntimeHerdrClient{calls: make(chan *controlledRuntimeSnapshot, 2)}
	client.calls <- oldCall
	client.calls <- newCall

	cfg := config.DefaultConfig()
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)

	oldResult := make(chan runtimeDiscoverResult, 1)
	go func() {
		nodes, collisions, token, err := rt.DiscoverForReconcile(context.Background(), baseDir, contextID)
		oldResult <- runtimeDiscoverResult{nodes: nodes, collisions: collisions, token: token, err: err}
	}()
	<-oldCall.started

	newResult := make(chan runtimeDiscoverResult, 1)
	go func() {
		nodes, collisions, token, err := rt.DiscoverForReconcile(context.Background(), baseDir, contextID)
		newResult <- runtimeDiscoverResult{nodes: nodes, collisions: collisions, token: token, err: err}
	}()
	<-newCall.started
	close(newCall.release)
	newer := <-newResult
	if newer.err != nil {
		t.Fatalf("newer DiscoverForReconcile() error = %v", newer.err)
	}
	if len(newer.collisions) != 0 {
		t.Fatalf("newer collisions = %#v, want none", newer.collisions)
	}
	if !rt.ReconcileFinalNodesForToken(newer.token, newer.nodes) {
		t.Fatal("newer ReconcileFinalNodesForToken() accepted = false, want true")
	}

	newTarget := controlplane.Target{Hand: controlplane.HandAttachment{Kind: controlplane.HandKindHerdr, Address: "workspace-1:pane-new"}}
	if _, err := controlplane.DefaultHandAdapter(newTarget); err != nil {
		t.Fatalf("newer Herdr adapter missing after final reconcile: %v", err)
	}

	close(oldCall.release)
	older := <-oldResult
	if older.err != nil {
		t.Fatalf("older DiscoverForReconcile() error = %v", older.err)
	}
	if rt.ReconcileFinalNodesForToken(older.token, older.nodes) {
		t.Fatal("older ReconcileFinalNodesForToken() accepted = true, want stale rejection")
	}

	for _, paneID := range []string{"workspace-1:pane-old", "workspace-1:pane-old-2"} {
		oldTarget := controlplane.Target{Hand: controlplane.HandAttachment{Kind: controlplane.HandKindHerdr, Address: paneID}}
		if _, err := controlplane.DefaultHandAdapter(oldTarget); err == nil {
			t.Fatalf("older successful discovery registered stale Herdr adapter for %s", paneID)
		}
	}
	if _, err := controlplane.DefaultHandAdapter(newTarget); err != nil {
		t.Fatalf("newer Herdr adapter was removed by stale older reconcile: %v", err)
	}
}

func TestRuntimeStaleZeroPaneReconcileDoesNotPruneNewerRoutesOrWorkspaceOwner(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	oldSnapshot := runtimeHerdrSnapshotFor("work", "workspace-1", "workspace-1:tab-old", "workspace-1:pane-old", "")
	oldSnapshot.Panes = nil
	newSnapshot := runtimeHerdrSnapshotFor("work", "workspace-1", "workspace-1:tab-new", "workspace-1:pane-new", "ctx-new:1")
	oldCall := newControlledRuntimeSnapshot(oldSnapshot, nil)
	newCall := newControlledRuntimeSnapshot(newSnapshot, nil)
	client := &sequencedRuntimeHerdrClient{calls: make(chan *controlledRuntimeSnapshot, 2)}
	client.calls <- oldCall
	client.calls <- newCall

	cfg := config.DefaultConfig()
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)

	oldResult := make(chan runtimeDiscoverResult, 1)
	go func() {
		nodes, collisions, token, err := rt.DiscoverForReconcile(context.Background(), baseDir, contextID)
		oldResult <- runtimeDiscoverResult{nodes: nodes, collisions: collisions, token: token, err: err}
	}()
	<-oldCall.started

	newResult := make(chan runtimeDiscoverResult, 1)
	go func() {
		nodes, collisions, token, err := rt.DiscoverForReconcile(context.Background(), baseDir, contextID)
		newResult <- runtimeDiscoverResult{nodes: nodes, collisions: collisions, token: token, err: err}
	}()
	<-newCall.started
	close(newCall.release)
	newer := <-newResult
	if newer.err != nil {
		t.Fatalf("newer DiscoverForReconcile() error = %v", newer.err)
	}
	if !rt.ReconcileFinalNodesForToken(newer.token, newer.nodes) {
		t.Fatal("newer ReconcileFinalNodesForToken() accepted = false, want true")
	}

	close(oldCall.release)
	older := <-oldResult
	if older.err != nil {
		t.Fatalf("older DiscoverForReconcile() error = %v", older.err)
	}
	if rt.ReconcileFinalNodesForToken(older.token, older.nodes) {
		t.Fatal("older zero-pane ReconcileFinalNodesForToken() accepted = true, want stale rejection")
	}

	newTarget := controlplane.Target{Hand: controlplane.HandAttachment{Kind: controlplane.HandKindHerdr, Address: "workspace-1:pane-new"}}
	if _, err := controlplane.DefaultHandAdapter(newTarget); err != nil {
		t.Fatalf("newer Herdr adapter was removed by stale zero-pane reconcile: %v", err)
	}
	verifyCall := newControlledRuntimeSnapshot(newSnapshot, nil)
	client.calls <- verifyCall
	close(verifyCall.release)
	owner, err := rt.OwnershipBackend().SessionOwnerMarker(context.Background(), "work")
	if err != nil {
		t.Fatalf("SessionOwnerMarker(work) error = %v", err)
	}
	if owner != "ctx-new:1" {
		t.Fatalf("SessionOwnerMarker(work) = %q, want newer owner", owner)
	}
}

func TestRuntimeDiscoveryErrorDoesNotClearNewerSuccessfulRoutes(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	oldErr := errors.New("old snapshot failed")
	oldCall := newControlledRuntimeSnapshot(multiplexer.HerdrSessionSnapshot{}, oldErr)
	newCall := newControlledRuntimeSnapshot(runtimeHerdrSnapshotFor("work", "workspace-1", "workspace-1:tab-new", "workspace-1:pane-new", ""), nil)
	client := &sequencedRuntimeHerdrClient{calls: make(chan *controlledRuntimeSnapshot, 2)}
	client.calls <- oldCall
	client.calls <- newCall

	cfg := config.DefaultConfig()
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)

	oldResult := make(chan runtimeDiscoverResult, 1)
	go func() {
		nodes, collisions, token, err := rt.DiscoverForReconcile(context.Background(), baseDir, contextID)
		oldResult <- runtimeDiscoverResult{nodes: nodes, collisions: collisions, token: token, err: err}
	}()
	<-oldCall.started

	newResult := make(chan runtimeDiscoverResult, 1)
	go func() {
		nodes, collisions, token, err := rt.DiscoverForReconcile(context.Background(), baseDir, contextID)
		newResult <- runtimeDiscoverResult{nodes: nodes, collisions: collisions, token: token, err: err}
	}()
	<-newCall.started
	close(newCall.release)
	newer := <-newResult
	if newer.err != nil {
		t.Fatalf("newer DiscoverForReconcile() error = %v", newer.err)
	}
	rt.ReconcileFinalNodesForToken(newer.token, newer.nodes)

	newTarget := controlplane.Target{Hand: controlplane.HandAttachment{Kind: controlplane.HandKindHerdr, Address: "workspace-1:pane-new"}}
	if _, err := controlplane.DefaultHandAdapter(newTarget); err != nil {
		t.Fatalf("newer Herdr adapter missing after final reconcile: %v", err)
	}

	close(oldCall.release)
	older := <-oldResult
	if !errors.Is(older.err, oldErr) {
		t.Fatalf("older DiscoverForReconcile() error = %v, want old snapshot failure", older.err)
	}
	if _, err := controlplane.DefaultHandAdapter(newTarget); err != nil {
		t.Fatalf("newer Herdr adapter was cleared by stale older error: %v", err)
	}
}

func TestRuntimeTokenlessDiscoverFailsClosedWhenGenerationIsLost(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	oldCall := newControlledRuntimeSnapshot(runtimeHerdrSnapshotFor(sessionName, "workspace-1", "workspace-1:tab-1", "workspace-1:pane-old", ""), nil)
	newCall := newControlledRuntimeSnapshot(runtimeHerdrSnapshotFor(sessionName, "workspace-1", "workspace-1:tab-1", "workspace-1:pane-new", ""), nil)
	client := &sequencedRuntimeHerdrClient{calls: make(chan *controlledRuntimeSnapshot, 2)}
	client.calls <- oldCall
	client.calls <- newCall
	cfg := config.DefaultConfig()
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)

	oldDone := make(chan error, 1)
	go func() {
		_, _, err := rt.Discover(context.Background(), baseDir, contextID)
		oldDone <- err
	}()
	<-oldCall.started

	newDone := make(chan error, 1)
	go func() {
		_, _, err := rt.Discover(context.Background(), baseDir, contextID)
		newDone <- err
	}()
	<-newCall.started
	close(newCall.release)
	if err := <-newDone; err != nil {
		t.Fatalf("new Discover() error = %v", err)
	}

	close(oldCall.release)
	if err := <-oldDone; err == nil || !strings.Contains(err.Error(), "stale Herdr discovery token") {
		t.Fatalf("old Discover() error = %v, want stale token", err)
	}
}

func TestRuntimeFinalReconcilePublishesBeforeCompetingDiscoveryCanAdvance(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	client := &fakeRuntimeHerdrClient{snapshot: validRuntimeHerdrSnapshot()}
	cfg := config.DefaultConfig()
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)

	nodes, _, token, err := rt.DiscoverForReconcile(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("DiscoverForReconcile() error = %v", err)
	}
	competingStarted := make(chan struct{})
	releaseCompeting := make(chan struct{})
	published := false
	err = rt.ReconcileFinalNodesForTokenAndPublish(token, nodes, func() error {
		client.snapshotStarted = competingStarted
		client.releaseSnapshot = releaseCompeting
		go func() {
			_, _, _, _ = rt.DiscoverForReconcile(context.Background(), baseDir, contextID)
		}()
		select {
		case <-competingStarted:
			t.Fatal("competing discovery advanced before final snapshot publication")
		case <-time.After(20 * time.Millisecond):
		}
		published = true
		return nil
	})
	if err != nil {
		t.Fatalf("ReconcileFinalNodesForTokenAndPublish() error = %v", err)
	}
	if !published {
		t.Fatal("publish callback was not called")
	}
	select {
	case <-competingStarted:
	case <-time.After(time.Second):
		t.Fatal("competing discovery did not advance after final publication")
	}
	close(releaseCompeting)
}

func TestRuntimeFinalReconcileFailureKeepsPriorGenerationAuthoritative(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	client := &fakeRuntimeHerdrClient{snapshot: validRuntimeHerdrSnapshot()}
	cfg := config.DefaultConfig()
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)

	oldNodes, _, oldToken, err := rt.DiscoverForReconcile(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("DiscoverForReconcile(old) error = %v", err)
	}
	if err := rt.ReconcileFinalNodesForTokenAndCommit(oldToken, oldNodes, nil, nil); err != nil {
		t.Fatalf("ReconcileFinalNodesForTokenAndCommit(old) error = %v", err)
	}
	oldNode := oldNodes[sessionName+":worker"]
	oldTarget := controlplane.TargetForNode(sessionName+":worker", oldNode)
	if _, err := controlplane.DefaultHandAdapter(oldTarget); err != nil {
		t.Fatalf("DefaultHandAdapter(old) error = %v", err)
	}

	next := validRuntimeHerdrSnapshot()
	next.Panes[0].ID = "workspace-1:pane-2"
	client.snapshot = next
	newNodes, _, newToken, err := rt.DiscoverForReconcile(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("DiscoverForReconcile(new) error = %v", err)
	}
	newNode := newNodes[sessionName+":worker"]
	newTarget := controlplane.TargetForNode(sessionName+":worker", newNode)
	publishErr := errors.New("publish failed")
	readerStarted := make(chan struct{})
	readerDone := make(chan error, 1)
	err = rt.ReconcileFinalNodesForTokenAndCommit(newToken, newNodes, func(*herdrruntime.FinalPublication) error {
		go func() {
			close(readerStarted)
			_, adapterErr := controlplane.DefaultHandAdapter(oldTarget)
			if adapterErr != nil {
				readerDone <- fmt.Errorf("old adapter lookup during failed prepare: %w", adapterErr)
				return
			}
			readerDone <- nil
		}()
		<-readerStarted
		select {
		case err := <-readerDone:
			t.Fatalf("reader completed during failed prepare with %v; want blocked until publication gate releases", err)
		case <-time.After(20 * time.Millisecond):
		}
		return publishErr
	}, func() {
		t.Fatal("commit callback ran after prepare failure")
	})
	if !errors.Is(err, publishErr) {
		t.Fatalf("ReconcileFinalNodesForTokenAndCommit(new) error = %v, want %v", err, publishErr)
	}
	select {
	case err := <-readerDone:
		if err != nil {
			t.Fatalf("old reader after failed prepare error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("old reader remained blocked after failed prepare rollback")
	}
	if _, err := controlplane.DefaultHandAdapter(oldTarget); err != nil {
		t.Fatalf("old adapter after failed publication error = %v", err)
	}
	client.snapshot = validRuntimeHerdrSnapshot()
	oldAdapter, err := controlplane.DefaultHandAdapter(oldTarget)
	if err != nil {
		t.Fatalf("old adapter after failed publication lookup error = %v", err)
	}
	if err := oldAdapter.Deliver(oldTarget, controlplane.PaneDelivery{Content: "old generation"}); err != nil {
		t.Fatalf("old adapter after failed publication delivery error = %v", err)
	}
	if _, err := controlplane.DefaultHandAdapter(newTarget); err == nil {
		t.Fatal("new adapter escaped after failed publication")
	}
	ownershipBackend, err := multiplexer.OwnershipBackendForKind(multiplexer.BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	if err := ownershipBackend.SetPaneOwnerMarker(context.Background(), multiplexer.HerdrPaneID(newNode.PaneID), contextID); err == nil {
		t.Fatal("new ownership route escaped after failed publication")
	}
	if err := ownershipBackend.SetPaneOwnerMarker(context.Background(), multiplexer.HerdrPaneID(oldNode.PaneID), contextID); err != nil {
		t.Fatalf("old ownership route after failed publication error = %v", err)
	}
}

func TestRuntimeFinalReconcileRollsBackPreparedExternalMarkersOnFailure(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	publishErr := errors.New("pane claim failed")
	client := &fakeRuntimeHerdrClient{
		snapshot:            validRuntimeHerdrSnapshot(),
		setPaneMetadataErr:  publishErr,
		setPaneMetadataPane: "",
	}
	cfg := config.DefaultConfig()
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)

	nodes, _, token, err := rt.DiscoverForReconcile(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("DiscoverForReconcile() error = %v", err)
	}
	err = rt.ReconcileFinalNodesForTokenAndCommit(token, nodes, func(publication *herdrruntime.FinalPublication) error {
		if err := publication.SetSessionEnabledMarker(context.Background(), contextID, sessionName, true); err != nil {
			return err
		}
		nodeInfo := nodes[sessionName+":worker"]
		return publication.SetPaneOwnerMarker(context.Background(), multiplexer.HerdrPaneIDForRuntime(multiplexer.HerdrRuntimeIdentity{
			SocketPath:  nodeInfo.HerdrSocketPath,
			SessionName: nodeInfo.SessionName,
			WorkspaceID: nodeInfo.HerdrWorkspaceID,
			TabID:       nodeInfo.HerdrTabID,
			PaneID:      nodeInfo.PaneID,
		}, nodeInfo.PaneID), contextID)
	}, func() {
		t.Fatal("commit callback ran after failed pane claim")
	})
	if !errors.Is(err, publishErr) {
		t.Fatalf("ReconcileFinalNodesForTokenAndCommit() error = %v, want %v", err, publishErr)
	}
	if client.clearWorkspaceMetadataWorkspace != "workspace-1" {
		t.Fatalf("rollback clear workspace = %q, want workspace-1", client.clearWorkspaceMetadataWorkspace)
	}
	if _, err := controlplane.DefaultHandAdapter(controlplane.TargetForNode(sessionName+":worker", nodes[sessionName+":worker"])); err == nil {
		t.Fatal("adapter escaped after prepared marker rollback")
	}
	if err := rt.OwnershipBackend().SetPaneOwnerMarker(context.Background(), multiplexer.HerdrPaneID("workspace-1:pane-1"), contextID); err == nil {
		t.Fatal("ownership route escaped after prepared marker rollback")
	}
}

func TestRuntimeFinalReconcileReturnsRollbackFailure(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	publishErr := errors.New("pane claim failed")
	rollbackErr := errors.New("workspace clear failed")
	client := &fakeRuntimeHerdrClient{
		snapshot:                  validRuntimeHerdrSnapshot(),
		setPaneMetadataErr:        publishErr,
		clearWorkspaceMetadataErr: rollbackErr,
	}
	cfg := config.DefaultConfig()
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)

	nodes, _, token, err := rt.DiscoverForReconcile(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("DiscoverForReconcile() error = %v", err)
	}
	err = rt.ReconcileFinalNodesForTokenAndCommit(token, nodes, func(publication *herdrruntime.FinalPublication) error {
		if err := publication.SetSessionEnabledMarker(context.Background(), contextID, sessionName, true); err != nil {
			return err
		}
		nodeInfo := nodes[sessionName+":worker"]
		return publication.SetPaneOwnerMarker(context.Background(), multiplexer.HerdrPaneIDForRuntime(multiplexer.HerdrRuntimeIdentity{
			SocketPath:  nodeInfo.HerdrSocketPath,
			SessionName: nodeInfo.SessionName,
			WorkspaceID: nodeInfo.HerdrWorkspaceID,
			TabID:       nodeInfo.HerdrTabID,
			PaneID:      nodeInfo.PaneID,
		}, nodeInfo.PaneID), contextID)
	}, func() {
		t.Fatal("commit callback ran after failed pane claim")
	})
	if !errors.Is(err, publishErr) {
		t.Fatalf("ReconcileFinalNodesForTokenAndCommit() error = %v, want publish failure", err)
	}
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("ReconcileFinalNodesForTokenAndCommit() error = %v, want rollback failure", err)
	}
}

func TestFinalPublicationRestoresPaneMarkerAfterAmbiguousWriteFailure(t *testing.T) {
	backend := &transactionOwnershipBackend{
		kind:        multiplexer.BackendKindHerdr,
		paneMarkers: map[string]string{"%42": "ctx-old"},
		failSetFor:  "ctx-new",
		failSetErr:  errors.New("remote write returned failure after mutation"),
	}
	unregister := multiplexer.RegisterOwnershipBackend(backend)
	t.Cleanup(unregister)

	publication := herdrruntime.NewFinalPublication()
	err := publication.SetPaneOwnerMarker(context.Background(), multiplexer.HerdrPaneID("%42"), "ctx-new")
	if !errors.Is(err, backend.failSetErr) {
		t.Fatalf("SetPaneOwnerMarker() error = %v, want ambiguous write failure", err)
	}
	if got := backend.paneMarkers["%42"]; got != "ctx-old" {
		t.Fatalf("pane marker after failed write = %q, want restored ctx-old", got)
	}
}

func TestRuntimeFinalReconcileBlocksExternalReadersDuringPublicCommit(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	client := &fakeRuntimeHerdrClient{snapshot: validRuntimeHerdrSnapshot()}
	cfg := config.DefaultConfig()
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)

	oldNodes, _, oldToken, err := rt.DiscoverForReconcile(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("DiscoverForReconcile(old) error = %v", err)
	}
	if err := rt.ReconcileFinalNodesForTokenAndCommit(oldToken, oldNodes, nil, nil); err != nil {
		t.Fatalf("ReconcileFinalNodesForTokenAndCommit(old) error = %v", err)
	}

	next := validRuntimeHerdrSnapshot()
	next.Panes[0].ID = "workspace-1:pane-2"
	client.snapshot = next
	newNodes, _, newToken, err := rt.DiscoverForReconcile(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("DiscoverForReconcile(new) error = %v", err)
	}
	newNode := newNodes[sessionName+":worker"]
	newTarget := controlplane.TargetForNode(sessionName+":worker", newNode)
	readerDone := make(chan error, 1)
	err = rt.ReconcileFinalNodesForTokenAndCommit(newToken, newNodes, nil, func() {
		go func() {
			if _, adapterErr := controlplane.DefaultHandAdapter(newTarget); adapterErr != nil {
				readerDone <- adapterErr
				return
			}
			ownershipBackend, backendErr := multiplexer.OwnershipBackendForKind(multiplexer.BackendKindHerdr)
			if backendErr != nil {
				readerDone <- backendErr
				return
			}
			readerDone <- ownershipBackend.SetPaneOwnerMarker(context.Background(), multiplexer.HerdrPaneID(newNode.PaneID), contextID)
		}()
		select {
		case err := <-readerDone:
			t.Fatalf("external Herdr reader completed inside public commit with %v; want blocked until generation commit releases", err)
		case <-time.After(20 * time.Millisecond):
		}
	})
	if err != nil {
		t.Fatalf("ReconcileFinalNodesForTokenAndCommit(new) error = %v", err)
	}
	select {
	case err := <-readerDone:
		if err != nil {
			t.Fatalf("external Herdr reader after commit error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("external Herdr reader remained blocked after public commit")
	}
}

func TestRuntimeCloseWhileDiscoverBlockedDoesNotRegisterRoutes(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	snapshotStarted := make(chan struct{})
	releaseSnapshot := make(chan struct{})
	client := &fakeRuntimeHerdrClient{
		snapshot:        validRuntimeHerdrSnapshot(),
		snapshotStarted: snapshotStarted,
		releaseSnapshot: releaseSnapshot,
	}
	cfg := config.DefaultConfig()
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	discoverDone := make(chan error, 1)
	go func() {
		_, _, err := rt.Discover(context.Background(), baseDir, contextID)
		discoverDone <- err
	}()
	<-snapshotStarted
	rt.Close()
	close(releaseSnapshot)
	if err := <-discoverDone; err == nil {
		t.Fatal("Discover() error = nil after Close, want terminal closed error")
	}

	target := controlplane.TargetForNode("work:worker", discovery.NodeInfo{
		PaneID:           "workspace-1:pane-1",
		SessionName:      "work",
		Backend:          string(multiplexer.BackendKindHerdr),
		HerdrSocketPath:  "/tmp/herdr.sock",
		HerdrWorkspaceID: "workspace-1",
		HerdrTabID:       "workspace-1:tab-1",
	})
	if _, err := controlplane.DefaultHandAdapter(target); err == nil {
		t.Fatal("Herdr adapter registered after runtime Close")
	}
	if err := rt.OwnershipBackend().SetPaneOwnerMarker(context.Background(), multiplexer.HerdrPaneID("workspace-1:pane-1"), contextID); err == nil {
		t.Fatal("Herdr ownership route registered after runtime Close")
	}
}

func TestRuntimeClearSessionOwnerMarkerSurvivesZeroPaneRediscovery(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}

	client := &fakeRuntimeHerdrClient{snapshot: validRuntimeHerdrSnapshot()}
	cfg := config.DefaultConfig()
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)

	if _, _, err := rt.Discover(context.Background(), baseDir, contextID); err != nil {
		t.Fatalf("Discover(initial) error = %v", err)
	}
	next := validRuntimeHerdrSnapshot()
	next.Panes = nil
	client.snapshot = next
	if nodes, _, err := rt.Discover(context.Background(), baseDir, contextID); err != nil {
		t.Fatalf("Discover(empty) error = %v", err)
	} else if len(nodes) != 0 {
		t.Fatalf("Discover(empty) nodes = %#v, want none", nodes)
	} else {
		rt.ReconcileFinalNodes(nodes)
	}

	ownershipBackend, err := multiplexer.OwnershipBackendForKind(multiplexer.BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	if err := ownershipBackend.ClearSessionOwnerMarker(context.Background(), sessionName); err != nil {
		t.Fatalf("ClearSessionOwnerMarker() after zero-pane rediscovery error = %v", err)
	}
	if client.clearWorkspaceMetadataWorkspace != "workspace-1" || client.clearWorkspaceMetadataKey == "" {
		t.Fatalf("clear workspace metadata workspace=%q key=%q, want retained session ownership backend", client.clearWorkspaceMetadataWorkspace, client.clearWorkspaceMetadataKey)
	}
	if got, err := ownershipBackend.SessionOwnerMarker(context.Background(), sessionName); err != nil || got != "" {
		t.Fatalf("zero-pane Herdr session owner read = %q, %v; want empty workspace marker route", got, err)
	}
	if err := ownershipBackend.SetSessionOwnerMarker(context.Background(), contextID, sessionName, 0); err != nil {
		t.Fatalf("zero-pane Herdr session owner set error = %v, want workspace marker route", err)
	}
	if err := ownershipBackend.SetPaneOwnerMarker(context.Background(), multiplexer.HerdrPaneID("workspace-1:pane-1"), contextID); err == nil {
		t.Fatal("stale Herdr pane ownership backend remained routable after zero-pane rediscovery")
	}
}

func TestRuntimeDiscoverDoesNotRegisterDuplicateHerdrClaims(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	client := &fakeRuntimeHerdrClient{snapshot: validRuntimeHerdrSnapshot()}
	client.snapshot.Panes = append(client.snapshot.Panes, multiplexer.HerdrPaneSnapshot{
		ID:          "workspace-1:pane-2",
		WorkspaceID: "workspace-1",
		TabID:       "workspace-1:tab-1",
		PostmanNode: "worker",
		ProcessInfo: multiplexer.HerdrPaneProcessInfo{ForegroundProcesses: []multiplexer.HerdrProcessInfo{{Name: "codex"}}},
	})
	cfg := config.DefaultConfig()
	cfg.Herdr = validRuntimeHerdrConfig()
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)

	nodes, collisions, err := rt.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if _, ok := nodes[sessionName+":worker"]; ok {
		t.Fatalf("duplicate Herdr claim registered in nodes: %#v", nodes)
	}
	if len(collisions) != 1 || collisions[0].NodeKey != sessionName+":worker" {
		t.Fatalf("collisions = %#v, want duplicate Herdr collision", collisions)
	}
	rt.ReconcileFinalNodes(nodes)
	ownershipBackend, err := multiplexer.OwnershipBackendForKind(multiplexer.BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	if got, err := ownershipBackend.SessionOwnerMarker(context.Background(), sessionName); err != nil || got != "" {
		t.Fatalf("duplicate-only Herdr session owner read = %q, %v; want empty workspace marker route", got, err)
	}
	if err := rt.SetSessionEnabledMarker(context.Background(), contextID, sessionName, true); err != nil {
		t.Fatalf("duplicate-only Herdr session owner set error = %v, want workspace marker route", err)
	}
	if err := ownershipBackend.ClearSessionOwnerMarker(context.Background(), sessionName); err != nil {
		t.Fatalf("duplicate-only Herdr session owner clear error = %v, want workspace marker route", err)
	}
	for _, paneID := range []string{"workspace-1:pane-1", "workspace-1:pane-2"} {
		target := controlplane.Target{Hand: controlplane.HandAttachment{Kind: controlplane.HandKindHerdr, Address: paneID}}
		if _, err := controlplane.DefaultHandAdapter(target); err == nil {
			t.Fatalf("duplicate Herdr pane %q remained registered", paneID)
		}
	}
}

func TestRuntimeHerdrOwnershipRegistryIsolatesMultipleSessions(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	for _, sessionName := range []string{"work", "other"} {
		if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
			t.Fatalf("CreateSessionDirs(%q) error = %v", sessionName, err)
		}
	}

	workClient := &fakeRuntimeHerdrClient{snapshot: runtimeHerdrSnapshotFor("work", "workspace-1", "workspace-1:tab-1", "workspace-1:pane-1", "ctx-work:1")}
	workCfg := config.DefaultConfig()
	workCfg.Herdr = runtimeHerdrConfigFor("work", "workspace-1")
	workRuntime, err := herdrruntime.New(workCfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return workClient, nil
	})
	if err != nil {
		t.Fatalf("New(work) error = %v", err)
	}
	t.Cleanup(workRuntime.Close)

	otherClient := &fakeRuntimeHerdrClient{snapshot: runtimeHerdrSnapshotFor("other", "workspace-2", "workspace-2:tab-1", "workspace-2:pane-1", "ctx-other:1")}
	otherCfg := config.DefaultConfig()
	otherCfg.Herdr = runtimeHerdrConfigFor("other", "workspace-2")
	otherRuntime, err := herdrruntime.New(otherCfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return otherClient, nil
	})
	if err != nil {
		t.Fatalf("New(other) error = %v", err)
	}

	workNodes, _, err := workRuntime.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover(work) error = %v", err)
	}
	workRuntime.ReconcileFinalNodes(workNodes)
	otherNodes, _, err := otherRuntime.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover(other) error = %v", err)
	}
	otherRuntime.ReconcileFinalNodes(otherNodes)
	otherNode := otherNodes["other:worker"]
	if otherNode.PaneID == "" {
		t.Fatalf("Discover(other) nodes = %#v, want other:worker", otherNodes)
	}

	ownershipBackend, err := multiplexer.OwnershipBackendForKind(multiplexer.BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	if got, err := ownershipBackend.SessionOwnerMarker(context.Background(), "work"); err != nil || got != "ctx-work:1" {
		t.Fatalf("SessionOwnerMarker(work) = %q, %v; want ctx-work:1", got, err)
	}
	if got, err := ownershipBackend.SessionOwnerMarker(context.Background(), "other"); err != nil || got != "ctx-other:1" {
		t.Fatalf("SessionOwnerMarker(other) = %q, %v; want ctx-other:1", got, err)
	}

	otherRuntime.Close()
	otherTarget := controlplane.Target{Hand: controlplane.HandAttachment{Kind: controlplane.HandKindHerdr, Address: "workspace-2:pane-1"}}
	if _, err := controlplane.DefaultHandAdapter(otherTarget); err == nil {
		t.Fatal("closed Herdr runtime pane adapter remained registered")
	}
	if result, err := message.DeliverSystemMessageDirectResult(
		"20260414-120000-r5678-from-postman-to-worker.md",
		otherNode,
		"worker",
		"postman",
		contextID,
		"body",
		otherCfg,
		nil,
		map[string]discovery.NodeInfo{"other:worker": otherNode},
		nil,
	); err == nil || result.Delivered {
		t.Fatalf("DeliverSystemMessageDirectResult(closed other) = %#v, %v; want closed runtime delivery unavailable", result, err)
	}
	ownershipBackend, err = multiplexer.OwnershipBackendForKind(multiplexer.BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr after other close) error = %v", err)
	}
	if _, err := ownershipBackend.SessionOwnerMarker(context.Background(), "other"); err == nil {
		t.Fatal("closed Herdr runtime session owner route remained available")
	}
	if got, err := ownershipBackend.SessionOwnerMarker(context.Background(), "work"); err != nil || got != "ctx-work:1" {
		t.Fatalf("SessionOwnerMarker(work after other close) = %q, %v; want ctx-work:1", got, err)
	}
}

func TestRuntimeHerdrOwnershipCompositeResolvesReducedUniquePaneIdentity(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	for _, sessionName := range []string{"work", "other"} {
		if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
			t.Fatalf("CreateSessionDirs(%q) error = %v", sessionName, err)
		}
	}

	const (
		workPaneID = "workspace-1:pane-reduced"
		workTabID  = "workspace-1:tab-1"
	)
	workSnapshot := runtimeHerdrSnapshotFor("work", "workspace-1", workTabID, workPaneID, "ctx-work:1")
	workSnapshot.Panes[0].Metadata[multiplexer.HerdrPaneContextIDMetadataKey] = "ctx-pane-old"
	workClient := &fakeRuntimeHerdrClient{snapshot: workSnapshot}
	workCfg := config.DefaultConfig()
	workCfg.Herdr = runtimeHerdrConfigFor("work", "workspace-1")
	workCfg.Herdr.SocketPath = "/tmp/herdr-reduced-work.sock"
	workCfg.Herdr.AllowedSocketPaths = []string{workCfg.Herdr.SocketPath}
	workRuntime, err := herdrruntime.New(workCfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return workClient, nil
	})
	if err != nil {
		t.Fatalf("New(work) error = %v", err)
	}
	t.Cleanup(workRuntime.Close)

	otherClient := &fakeRuntimeHerdrClient{snapshot: runtimeHerdrSnapshotFor("other", "workspace-2", "workspace-2:tab-1", "workspace-2:pane-1", "ctx-other:1")}
	otherCfg := config.DefaultConfig()
	otherCfg.Herdr = runtimeHerdrConfigFor("other", "workspace-2")
	otherCfg.Herdr.SocketPath = "/tmp/herdr-reduced-other.sock"
	otherCfg.Herdr.AllowedSocketPaths = []string{otherCfg.Herdr.SocketPath}
	otherRuntime, err := herdrruntime.New(otherCfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return otherClient, nil
	})
	if err != nil {
		t.Fatalf("New(other) error = %v", err)
	}
	t.Cleanup(otherRuntime.Close)

	workNodes, _, err := workRuntime.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover(work) error = %v", err)
	}
	workRuntime.ReconcileFinalNodes(workNodes)
	otherNodes, _, err := otherRuntime.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover(other) error = %v", err)
	}
	otherRuntime.ReconcileFinalNodes(otherNodes)

	ownershipBackend, err := multiplexer.OwnershipBackendForKind(multiplexer.BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	reducedPane := multiplexer.HerdrPaneIDForRuntime(multiplexer.HerdrRuntimeIdentity{
		SocketPath:  workCfg.Herdr.SocketPath,
		SessionName: "work",
		WorkspaceID: "workspace-1",
		PaneID:      workPaneID,
	}, workPaneID)
	if got, err := ownershipBackend.PaneOwnerMarker(context.Background(), reducedPane); err != nil || got != "ctx-pane-old" {
		t.Fatalf("PaneOwnerMarker(reduced unique) = %q, %v; want ctx-pane-old", got, err)
	}
	if err := ownershipBackend.SetPaneOwnerMarker(context.Background(), reducedPane, "ctx-pane-new"); err != nil {
		t.Fatalf("SetPaneOwnerMarker(reduced unique) error = %v", err)
	}
	if workClient.setPaneMetadataPane != workPaneID || workClient.setPaneMetadataValue != "ctx-pane-new" {
		t.Fatalf("work SetPaneMetadata pane/value = %q/%q, want %q/ctx-pane-new", workClient.setPaneMetadataPane, workClient.setPaneMetadataValue, workPaneID)
	}
	if otherClient.setPaneMetadataPane != "" {
		t.Fatalf("other SetPaneMetadata pane = %q, want no reduced unique mutation", otherClient.setPaneMetadataPane)
	}
	if err := ownershipBackend.ClearPaneOwnerMarker(context.Background(), reducedPane); err != nil {
		t.Fatalf("ClearPaneOwnerMarker(reduced unique) error = %v", err)
	}
	if workClient.clearPaneMetadataPane != workPaneID || workClient.clearPaneMetadataKey != multiplexer.HerdrPaneContextIDMetadataKey {
		t.Fatalf("work ClearPaneMetadata pane/key = %q/%q, want %q/%q", workClient.clearPaneMetadataPane, workClient.clearPaneMetadataKey, workPaneID, multiplexer.HerdrPaneContextIDMetadataKey)
	}
	if otherClient.clearPaneMetadataPane != "" {
		t.Fatalf("other ClearPaneMetadata pane = %q, want no reduced unique clear", otherClient.clearPaneMetadataPane)
	}
}

func TestRuntimeHerdrOwnershipSingleRuntimeResolvesReducedPaneIdentity(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs(%q) error = %v", sessionName, err)
	}

	const (
		paneID      = "workspace-1:pane-single"
		socketPath  = "/tmp/herdr-reduced-single.sock"
		workspaceID = "workspace-1"
		tabID       = "workspace-1:tab-1"
	)
	snapshot := runtimeHerdrSnapshotFor(sessionName, workspaceID, tabID, paneID, "")
	snapshot.Panes[0].Metadata[multiplexer.HerdrPaneContextIDMetadataKey] = "ctx-pane-old"
	client := &fakeRuntimeHerdrClient{snapshot: snapshot}
	cfg := config.DefaultConfig()
	cfg.Herdr = runtimeHerdrConfigFor(sessionName, workspaceID)
	cfg.Herdr.SocketPath = socketPath
	cfg.Herdr.AllowedSocketPaths = []string{socketPath}
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)
	nodes, _, err := rt.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	rt.ReconcileFinalNodes(nodes)

	ownershipBackend, err := multiplexer.OwnershipBackendForKind(multiplexer.BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	reducedPane := multiplexer.HerdrPaneIDForRuntime(multiplexer.HerdrRuntimeIdentity{
		SocketPath:  socketPath,
		SessionName: sessionName,
		WorkspaceID: workspaceID,
		PaneID:      paneID,
	}, paneID)
	if got, err := ownershipBackend.PaneOwnerMarker(context.Background(), reducedPane); err != nil || got != "ctx-pane-old" {
		t.Fatalf("PaneOwnerMarker(reduced single runtime) = %q, %v; want ctx-pane-old", got, err)
	}
	if err := ownershipBackend.SetPaneOwnerMarker(context.Background(), reducedPane, "ctx-pane-new"); err != nil {
		t.Fatalf("SetPaneOwnerMarker(reduced single runtime) error = %v", err)
	}
	if client.setPaneMetadataPane != paneID || client.setPaneMetadataValue != "ctx-pane-new" {
		t.Fatalf("SetPaneMetadata pane/value = %q/%q, want %q/ctx-pane-new", client.setPaneMetadataPane, client.setPaneMetadataValue, paneID)
	}
	if err := ownershipBackend.ClearPaneOwnerMarker(context.Background(), reducedPane); err != nil {
		t.Fatalf("ClearPaneOwnerMarker(reduced single runtime) error = %v", err)
	}
	if client.clearPaneMetadataPane != paneID || client.clearPaneMetadataKey != multiplexer.HerdrPaneContextIDMetadataKey {
		t.Fatalf("ClearPaneMetadata pane/key = %q/%q, want %q/%q", client.clearPaneMetadataPane, client.clearPaneMetadataKey, paneID, multiplexer.HerdrPaneContextIDMetadataKey)
	}
}

func TestRuntimeHerdrOwnershipQualifiedPaneMissesFailClosed(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs(%q) error = %v", sessionName, err)
	}

	const (
		paneID      = "workspace-1:pane-qualified"
		socketPath  = "/tmp/herdr-qualified-runtime.sock"
		workspaceID = "workspace-1"
		tabID       = "workspace-1:tab-1"
	)
	snapshot := runtimeHerdrSnapshotFor(sessionName, workspaceID, tabID, paneID, "")
	snapshot.Panes[0].Metadata[multiplexer.HerdrPaneContextIDMetadataKey] = "ctx-pane-old"
	client := &fakeRuntimeHerdrClient{snapshot: snapshot}
	cfg := config.DefaultConfig()
	cfg.Herdr = runtimeHerdrConfigFor(sessionName, workspaceID)
	cfg.Herdr.SocketPath = socketPath
	cfg.Herdr.AllowedSocketPaths = []string{socketPath}
	rt, err := herdrruntime.New(cfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return client, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(rt.Close)
	nodes, _, err := rt.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	rt.ReconcileFinalNodes(nodes)
	ownershipBackend := rt.OwnershipBackend()

	for _, tt := range []struct {
		name    string
		runtime multiplexer.HerdrRuntimeIdentity
	}{
		{
			name: "socket",
			runtime: multiplexer.HerdrRuntimeIdentity{
				SocketPath:  "/tmp/herdr-missing.sock",
				SessionName: sessionName,
				WorkspaceID: workspaceID,
				TabID:       tabID,
				PaneID:      paneID,
			},
		},
		{
			name: "session",
			runtime: multiplexer.HerdrRuntimeIdentity{
				SessionName: "missing",
				PaneID:      paneID,
			},
		},
		{
			name: "workspace",
			runtime: multiplexer.HerdrRuntimeIdentity{
				SocketPath:  socketPath,
				SessionName: sessionName,
				WorkspaceID: "missing",
				TabID:       tabID,
				PaneID:      paneID,
			},
		},
		{
			name: "tab",
			runtime: multiplexer.HerdrRuntimeIdentity{
				TabID:  "missing-tab",
				PaneID: paneID,
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client.setPaneMetadataPane = ""
			client.setPaneMetadataValue = ""
			client.clearPaneMetadataPane = ""
			client.clearPaneMetadataKey = ""
			pane := multiplexer.HerdrPaneIDForRuntime(tt.runtime, paneID)
			if _, err := ownershipBackend.PaneOwnerMarker(context.Background(), pane); err == nil {
				t.Fatal("PaneOwnerMarker(qualified miss) error = nil, want fail-closed miss")
			}
			if err := ownershipBackend.SetPaneOwnerMarker(context.Background(), pane, "ctx-new"); err == nil {
				t.Fatal("SetPaneOwnerMarker(qualified miss) error = nil, want fail-closed miss")
			}
			if err := ownershipBackend.ClearPaneOwnerMarker(context.Background(), pane); err == nil {
				t.Fatal("ClearPaneOwnerMarker(qualified miss) error = nil, want fail-closed miss")
			}
			if client.setPaneMetadataPane != "" || client.clearPaneMetadataPane != "" {
				t.Fatalf("qualified miss mutated pane metadata set/clear=%q/%q", client.setPaneMetadataPane, client.clearPaneMetadataPane)
			}
		})
	}
}

func TestRuntimeHerdrOwnershipRegistrySkipsEmptyDuplicateAndTeardown(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-main"
	sessionName := "work"
	if err := config.CreateSessionDirs(filepath.Join(baseDir, contextID, sessionName)); err != nil {
		t.Fatalf("CreateSessionDirs(%q) error = %v", sessionName, err)
	}

	emptyClient := &fakeRuntimeHerdrClient{snapshot: runtimeHerdrSnapshotFor("work", "workspace-1", "workspace-1:tab-1", "workspace-1:pane-empty", "")}
	emptyCfg := config.DefaultConfig()
	emptyCfg.Herdr = runtimeHerdrConfigFor("work", "workspace-1")
	emptyCfg.Herdr.SocketPath = "/tmp/herdr-empty.sock"
	emptyCfg.Herdr.AllowedSocketPaths = []string{"/tmp/herdr-empty.sock"}
	emptyRuntime, err := herdrruntime.New(emptyCfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return emptyClient, nil
	})
	if err != nil {
		t.Fatalf("New(empty) error = %v", err)
	}
	t.Cleanup(emptyRuntime.Close)

	healthyClient := &fakeRuntimeHerdrClient{snapshot: runtimeHerdrSnapshotFor("work", "workspace-1", "workspace-1:tab-2", "workspace-1:pane-healthy", "ctx-work:1")}
	healthyCfg := config.DefaultConfig()
	healthyCfg.Herdr = runtimeHerdrConfigFor("work", "workspace-1")
	healthyCfg.Herdr.SocketPath = "/tmp/herdr-healthy.sock"
	healthyCfg.Herdr.AllowedSocketPaths = []string{"/tmp/herdr-healthy.sock"}
	healthyRuntime, err := herdrruntime.New(healthyCfg, func(config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
		return healthyClient, nil
	})
	if err != nil {
		t.Fatalf("New(healthy) error = %v", err)
	}

	emptyNodes, _, err := emptyRuntime.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover(empty) error = %v", err)
	}
	emptyRuntime.ReconcileFinalNodes(emptyNodes)
	healthyNodes, _, err := healthyRuntime.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover(healthy) error = %v", err)
	}
	healthyRuntime.ReconcileFinalNodes(healthyNodes)

	ownershipBackend, err := multiplexer.OwnershipBackendForKind(multiplexer.BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	if got, err := ownershipBackend.SessionOwnerMarker(context.Background(), "work"); err != nil || got != "ctx-work:1" {
		t.Fatalf("SessionOwnerMarker(work) = %q, %v; want ctx-work:1", got, err)
	}

	healthyRuntime.Close()
	ownershipBackend, err = multiplexer.OwnershipBackendForKind(multiplexer.BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr after healthy close) error = %v", err)
	}
	if got, err := ownershipBackend.SessionOwnerMarker(context.Background(), "work"); err != nil || got != "" {
		t.Fatalf("SessionOwnerMarker(work after healthy close) = %q, %v; want empty surviving duplicate marker", got, err)
	}
}

func TestSocketClientHonorsContextDeadlineAfterConnect(t *testing.T) {
	socketPath := tempHerdrSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen(unix) error = %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() {
			_ = conn.Close()
		}()
		time.Sleep(2 * time.Second)
	}()
	client, err := herdrruntime.NewSocketClient(config.HerdrConfig{SocketPath: socketPath})
	if err != nil {
		t.Fatalf("NewSocketClient() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = client.Ping(ctx)
	if err == nil {
		t.Fatal("Ping() error = nil, want context deadline error")
	}
	if time.Since(started) > time.Second {
		t.Fatalf("Ping() took %v, want bounded by context deadline", time.Since(started))
	}
}

func TestSocketClientRoundTripsSnapshotAndWriteMutations(t *testing.T) {
	socketPath := tempHerdrSocketPath(t)
	methods := serveFakeHerdrSocket(t, socketPath)

	client, err := herdrruntime.NewSocketClient(config.HerdrConfig{SocketPath: socketPath})
	if err != nil {
		t.Fatalf("NewSocketClient() error = %v", err)
	}
	writeClient, ok := client.(multiplexer.HerdrWriteClient)
	if !ok {
		t.Fatalf("NewSocketClient() does not implement HerdrWriteClient")
	}

	snapshot, err := client.SessionSnapshot(context.Background())
	if err != nil {
		t.Fatalf("SessionSnapshot() error = %v", err)
	}
	if snapshot.Envelope.ProtocolVersion != "1" || snapshot.Envelope.SchemaVersion != 1 {
		t.Fatalf("snapshot envelope = %#v, want protocol/schema 1", snapshot.Envelope)
	}
	if len(snapshot.Panes) != 1 || snapshot.Panes[0].WorkspaceID != "workspace-1" || snapshot.Panes[0].TabID != "workspace-1:tab-1" {
		t.Fatalf("snapshot panes = %#v, want snake_case IDs decoded", snapshot.Panes)
	}
	if got := snapshot.Panes[0].ProcessInfo.CurrentCommand(); got != "codex" {
		t.Fatalf("CurrentCommand() = %q, want codex", got)
	}

	if _, err := writeClient.WritePaneText(context.Background(), "workspace-1:pane-1", "body"); err != nil {
		t.Fatalf("WritePaneText() error = %v", err)
	}
	if _, err := writeClient.SetWorkspaceMetadata(context.Background(), "workspace-1", "postman.session_owner.work", "ctx:123"); err != nil {
		t.Fatalf("SetWorkspaceMetadata() error = %v", err)
	}

	gotMethods := []string{<-methods, <-methods, <-methods}
	wantMethods := []string{"session.snapshot", "pane.send_text", "workspace.report_metadata"}
	for i := range wantMethods {
		if gotMethods[i] != wantMethods[i] {
			t.Fatalf("method[%d] = %q, want %q (all=%#v)", i, gotMethods[i], wantMethods[i], gotMethods)
		}
	}
}

func tempHerdrSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "herdr-")
	if err != nil {
		t.Fatalf("MkdirTemp(/tmp) error = %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return filepath.Join(dir, "h.sock")
}

func validRuntimeHerdrConfig() config.HerdrConfig {
	return runtimeHerdrConfigFor("work", "workspace-1")
}

func runtimeHerdrConfigFor(sessionName, workspaceID string) config.HerdrConfig {
	revalidatedAt := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	decidedAt := revalidatedAt.Add(-time.Hour)
	return config.HerdrConfig{
		Enabled:                     true,
		SocketPath:                  "/tmp/herdr.sock",
		SessionName:                 sessionName,
		WorkspaceID:                 workspaceID,
		AllowedSocketPaths:          []string{"/tmp/herdr.sock"},
		AllowedSessions:             []string{sessionName},
		AllowedWorkspaceIDs:         []string{workspaceID},
		AllowedProtocolVersions:     []string{"1"},
		AllowedSchemaVersions:       []int{1},
		ReadEnabled:                 true,
		WriteEnabled:                true,
		InputSanitizerReady:         true,
		ComplianceDecision:          string(multiplexer.HerdrComplianceDecisionRecorded),
		ComplianceAuthorizedBy:      "test-authority",
		ComplianceDecisionID:        "test-decision",
		ComplianceDecidedAt:         decidedAt.Format(time.RFC3339),
		ComplianceRevalidatedAt:     revalidatedAt.Format(time.RFC3339),
		ComplianceCurrentReferences: []string{"https://github.com/ogulcancelik/herdr/blob/master/LICENSE"},
	}
}

func validRuntimeHerdrSnapshot() multiplexer.HerdrSessionSnapshot {
	return runtimeHerdrSnapshotFor("work", "workspace-1", "workspace-1:tab-1", "workspace-1:pane-1", "")
}

func runtimeHerdrSnapshotFor(sessionName, workspaceID, tabID, paneID, sessionOwner string) multiplexer.HerdrSessionSnapshot {
	workspaceMetadata := map[string]string{}
	if sessionOwner != "" {
		workspaceMetadata["postman.session_owner."+sessionName] = sessionOwner
	}
	return multiplexer.HerdrSessionSnapshot{
		Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1},
		Workspaces: []multiplexer.HerdrWorkspaceSnapshot{{
			ID:       workspaceID,
			Metadata: workspaceMetadata,
		}},
		Tabs: []multiplexer.HerdrTabSnapshot{{
			ID:          tabID,
			WorkspaceID: workspaceID,
			Metadata:    map[string]string{},
		}},
		Panes: []multiplexer.HerdrPaneSnapshot{{
			ID:             paneID,
			WorkspaceID:    workspaceID,
			TabID:          tabID,
			Metadata:       map[string]string{"postman.node": "worker"},
			Env:            map[string]string{},
			ProcessInfo:    multiplexer.HerdrPaneProcessInfo{ForegroundProcesses: []multiplexer.HerdrProcessInfo{{Name: "codex"}}},
			PostmanNode:    "worker",
			PostmanSession: sessionName,
		}},
	}
}

func serveFakeHerdrSocket(t *testing.T, socketPath string) <-chan string {
	t.Helper()
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen(unix): %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	})
	methods := make(chan string, 8)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				continue
			}
			go handleFakeHerdrSocketConn(conn, methods)
		}
	}()
	return methods
}

func handleFakeHerdrSocketConn(conn net.Conn, methods chan<- string) {
	defer func() { _ = conn.Close() }()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var request struct {
		ID     int64           `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line, &request); err != nil {
		return
	}
	methods <- request.Method
	result := map[string]interface{}{"protocol_version": "1", "schema_version": 1}
	if request.Method == "session.snapshot" {
		result["workspaces"] = []map[string]interface{}{{"id": "workspace-1", "metadata": map[string]string{}}}
		result["tabs"] = []map[string]interface{}{{"id": "workspace-1:tab-1", "workspace_id": "workspace-1"}}
		result["panes"] = []map[string]interface{}{{
			"id":           "workspace-1:pane-1",
			"workspace_id": "workspace-1",
			"tab_id":       "workspace-1:tab-1",
			"metadata":     map[string]string{"postman.node": "worker"},
			"process_info": map[string]interface{}{"foreground_processes": []map[string]string{{"name": "codex"}}},
		}}
	}
	response := map[string]interface{}{"jsonrpc": "2.0", "id": request.ID, "result": result}
	payload, _ := json.Marshal(response)
	payload = append(payload, '\n')
	_, _ = conn.Write(payload)
}

type fakeRuntimeHerdrClient struct {
	snapshot        multiplexer.HerdrSessionSnapshot
	snapshotErr     error
	snapshotStarted chan struct{}
	releaseSnapshot chan struct{}

	writeTextCalls int
	writeTextPane  string
	sendKeyCalls   int
	sendKeyKey     string

	setPaneMetadataPane   string
	setPaneMetadataKey    string
	setPaneMetadataValue  string
	setPaneMetadataErr    error
	clearPaneMetadataPane string
	clearPaneMetadataKey  string

	setWorkspaceMetadataWorkspace   string
	setWorkspaceMetadataValue       string
	clearWorkspaceMetadataWorkspace string
	clearWorkspaceMetadataKey       string
	clearWorkspaceMetadataErr       error
}

func (f *fakeRuntimeHerdrClient) Ping(context.Context) (multiplexer.HerdrResponseEnvelope, error) {
	return multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}, nil
}

func (f *fakeRuntimeHerdrClient) SessionSnapshot(context.Context) (multiplexer.HerdrSessionSnapshot, error) {
	if f.snapshotStarted != nil {
		close(f.snapshotStarted)
		f.snapshotStarted = nil
	}
	if f.releaseSnapshot != nil {
		<-f.releaseSnapshot
	}
	if f.snapshotErr != nil {
		return multiplexer.HerdrSessionSnapshot{}, f.snapshotErr
	}
	return f.snapshot, nil
}

func (f *fakeRuntimeHerdrClient) ReadPane(context.Context, string, multiplexer.HerdrPaneReadOptions) (multiplexer.HerdrPaneReadResult, error) {
	return multiplexer.HerdrPaneReadResult{Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}}, nil
}

func (f *fakeRuntimeHerdrClient) PaneProcessInfo(context.Context, string) (multiplexer.HerdrPaneProcessInfoResult, error) {
	return multiplexer.HerdrPaneProcessInfoResult{
		Envelope:    multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1},
		ProcessInfo: multiplexer.HerdrPaneProcessInfo{ForegroundProcesses: []multiplexer.HerdrProcessInfo{{Name: "codex"}}},
	}, nil
}

func (f *fakeRuntimeHerdrClient) WritePaneText(_ context.Context, paneID string, _ string) (multiplexer.HerdrWriteResult, error) {
	f.writeTextCalls++
	f.writeTextPane = paneID
	return multiplexer.HerdrWriteResult{Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}}, nil
}

func (f *fakeRuntimeHerdrClient) SendPaneKey(_ context.Context, _ string, key string) (multiplexer.HerdrWriteResult, error) {
	f.sendKeyCalls++
	f.sendKeyKey = key
	return multiplexer.HerdrWriteResult{Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}}, nil
}

func (f *fakeRuntimeHerdrClient) SetWorkspaceMetadata(_ context.Context, workspaceID string, _ string, value string) (multiplexer.HerdrWriteResult, error) {
	f.setWorkspaceMetadataWorkspace = workspaceID
	f.setWorkspaceMetadataValue = value
	return multiplexer.HerdrWriteResult{Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}}, nil
}

func (f *fakeRuntimeHerdrClient) ClearWorkspaceMetadata(_ context.Context, workspaceID string, key string) (multiplexer.HerdrWriteResult, error) {
	f.clearWorkspaceMetadataWorkspace = workspaceID
	f.clearWorkspaceMetadataKey = key
	if f.clearWorkspaceMetadataErr != nil {
		return multiplexer.HerdrWriteResult{}, f.clearWorkspaceMetadataErr
	}
	return multiplexer.HerdrWriteResult{Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}}, nil
}

func (f *fakeRuntimeHerdrClient) SetPaneMetadata(_ context.Context, paneID string, key string, value string) (multiplexer.HerdrWriteResult, error) {
	f.setPaneMetadataPane = paneID
	f.setPaneMetadataKey = key
	f.setPaneMetadataValue = value
	if f.setPaneMetadataErr != nil {
		return multiplexer.HerdrWriteResult{}, f.setPaneMetadataErr
	}
	return multiplexer.HerdrWriteResult{Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}}, nil
}

func (f *fakeRuntimeHerdrClient) ClearPaneMetadata(_ context.Context, paneID string, key string) (multiplexer.HerdrWriteResult, error) {
	f.clearPaneMetadataPane = paneID
	f.clearPaneMetadataKey = key
	return multiplexer.HerdrWriteResult{Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}}, nil
}

type transactionOwnershipBackend struct {
	kind        multiplexer.BackendKind
	paneMarkers map[string]string
	failSetFor  string
	failSetErr  error
}

func (b *transactionOwnershipBackend) Kind() multiplexer.BackendKind {
	return b.kind
}

func (b *transactionOwnershipBackend) SessionOwnerMarker(context.Context, string) (string, error) {
	return "", nil
}

func (b *transactionOwnershipBackend) SetSessionOwnerMarker(context.Context, string, string, int) error {
	return nil
}

func (b *transactionOwnershipBackend) ClearSessionOwnerMarker(context.Context, string) error {
	return nil
}

func (b *transactionOwnershipBackend) PaneOwnerMarker(_ context.Context, pane multiplexer.ResourceID) (string, error) {
	return b.paneMarkers[pane.Native], nil
}

func (b *transactionOwnershipBackend) SetPaneOwnerMarker(_ context.Context, pane multiplexer.ResourceID, contextID string) error {
	if b.paneMarkers == nil {
		b.paneMarkers = make(map[string]string)
	}
	b.paneMarkers[pane.Native] = contextID
	if contextID == b.failSetFor {
		return b.failSetErr
	}
	return nil
}

func (b *transactionOwnershipBackend) ClearPaneOwnerMarker(_ context.Context, pane multiplexer.ResourceID) error {
	delete(b.paneMarkers, pane.Native)
	return nil
}

type runtimeDiscoverResult struct {
	nodes      map[string]discovery.NodeInfo
	collisions []discovery.CollisionReport
	token      uint64
	err        error
}

type controlledRuntimeSnapshot struct {
	started  chan struct{}
	release  chan struct{}
	snapshot multiplexer.HerdrSessionSnapshot
	err      error
}

func newControlledRuntimeSnapshot(snapshot multiplexer.HerdrSessionSnapshot, err error) *controlledRuntimeSnapshot {
	return &controlledRuntimeSnapshot{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		snapshot: snapshot,
		err:      err,
	}
}

type sequencedRuntimeHerdrClient struct {
	calls chan *controlledRuntimeSnapshot
}

func (s *sequencedRuntimeHerdrClient) Ping(context.Context) (multiplexer.HerdrResponseEnvelope, error) {
	return multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}, nil
}

func (s *sequencedRuntimeHerdrClient) SessionSnapshot(context.Context) (multiplexer.HerdrSessionSnapshot, error) {
	call := <-s.calls
	close(call.started)
	<-call.release
	if call.err != nil {
		return multiplexer.HerdrSessionSnapshot{}, call.err
	}
	return call.snapshot, nil
}

func (s *sequencedRuntimeHerdrClient) ReadPane(context.Context, string, multiplexer.HerdrPaneReadOptions) (multiplexer.HerdrPaneReadResult, error) {
	return multiplexer.HerdrPaneReadResult{Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}}, nil
}

func (s *sequencedRuntimeHerdrClient) PaneProcessInfo(context.Context, string) (multiplexer.HerdrPaneProcessInfoResult, error) {
	return multiplexer.HerdrPaneProcessInfoResult{
		Envelope:    multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1},
		ProcessInfo: multiplexer.HerdrPaneProcessInfo{ForegroundProcesses: []multiplexer.HerdrProcessInfo{{Name: "codex"}}},
	}, nil
}

func (s *sequencedRuntimeHerdrClient) WritePaneText(context.Context, string, string) (multiplexer.HerdrWriteResult, error) {
	return multiplexer.HerdrWriteResult{Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}}, nil
}

func (s *sequencedRuntimeHerdrClient) SendPaneKey(context.Context, string, string) (multiplexer.HerdrWriteResult, error) {
	return multiplexer.HerdrWriteResult{Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}}, nil
}

func (s *sequencedRuntimeHerdrClient) SetWorkspaceMetadata(context.Context, string, string, string) (multiplexer.HerdrWriteResult, error) {
	return multiplexer.HerdrWriteResult{Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}}, nil
}

func (s *sequencedRuntimeHerdrClient) ClearWorkspaceMetadata(context.Context, string, string) (multiplexer.HerdrWriteResult, error) {
	return multiplexer.HerdrWriteResult{Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}}, nil
}

func (s *sequencedRuntimeHerdrClient) SetPaneMetadata(context.Context, string, string, string) (multiplexer.HerdrWriteResult, error) {
	return multiplexer.HerdrWriteResult{Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}}, nil
}

func (s *sequencedRuntimeHerdrClient) ClearPaneMetadata(context.Context, string, string) (multiplexer.HerdrWriteResult, error) {
	return multiplexer.HerdrWriteResult{Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}}, nil
}
