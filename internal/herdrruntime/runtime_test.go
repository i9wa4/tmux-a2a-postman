package herdrruntime_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
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

	setPaneMetadataPane  string
	setPaneMetadataKey   string
	setPaneMetadataValue string

	setWorkspaceMetadataWorkspace   string
	setWorkspaceMetadataValue       string
	clearWorkspaceMetadataWorkspace string
	clearWorkspaceMetadataKey       string
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
	return multiplexer.HerdrWriteResult{Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}}, nil
}

func (f *fakeRuntimeHerdrClient) SetPaneMetadata(_ context.Context, paneID string, key string, value string) (multiplexer.HerdrWriteResult, error) {
	f.setPaneMetadataPane = paneID
	f.setPaneMetadataKey = key
	f.setPaneMetadataValue = value
	return multiplexer.HerdrWriteResult{Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}}, nil
}

func (f *fakeRuntimeHerdrClient) ClearPaneMetadata(context.Context, string, string) (multiplexer.HerdrWriteResult, error) {
	return multiplexer.HerdrWriteResult{Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}}, nil
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
