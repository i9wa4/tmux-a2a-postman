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

	if _, _, err := rt.Discover(context.Background(), baseDir, contextID); err != nil {
		t.Fatalf("Discover(initial) error = %v", err)
	}
	staleTarget := controlplane.Target{Hand: controlplane.HandAttachment{Kind: controlplane.HandKindHerdr, Address: "workspace-1:pane-1"}}
	if _, err := controlplane.DefaultHandAdapter(staleTarget); err != nil {
		t.Fatalf("DefaultHandAdapter(initial) error = %v", err)
	}

	next := validRuntimeHerdrSnapshot()
	next.Panes[0].ID = "workspace-1:pane-2"
	client.snapshot = next
	if _, _, err := rt.Discover(context.Background(), baseDir, contextID); err != nil {
		t.Fatalf("Discover(second) error = %v", err)
	}
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

	if _, _, err := rt.Discover(context.Background(), baseDir, contextID); err != nil {
		t.Fatalf("Discover(initial) error = %v", err)
	}
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
	if _, err := ownershipBackend.SessionOwnerMarker(context.Background(), sessionName); err == nil {
		t.Fatal("final-pruned Herdr session owner read remained available")
	}
	if err := ownershipBackend.SetSessionOwnerMarker(context.Background(), contextID, sessionName, 0); err == nil {
		t.Fatal("final-pruned Herdr session owner set remained available")
	}
	if err := ownershipBackend.ClearSessionOwnerMarker(context.Background(), sessionName); err != nil {
		t.Fatalf("final-pruned Herdr session owner clear error = %v, want teardown-only clear to remain available", err)
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

	if _, _, err := rt.Discover(context.Background(), baseDir, contextID); err != nil {
		t.Fatalf("Discover(initial) error = %v", err)
	}
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

	if _, _, err := rt.Discover(context.Background(), baseDir, contextID); err != nil {
		t.Fatalf("Discover(initial) error = %v", err)
	}
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
	if _, err := ownershipBackend.SessionOwnerMarker(context.Background(), sessionName); err == nil {
		t.Fatal("zero-pane Herdr session owner read remained available")
	}
	if err := ownershipBackend.SetSessionOwnerMarker(context.Background(), contextID, sessionName, 0); err == nil {
		t.Fatal("zero-pane Herdr session owner set remained available")
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
	ownershipBackend, err := multiplexer.OwnershipBackendForKind(multiplexer.BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	if _, err := ownershipBackend.SessionOwnerMarker(context.Background(), sessionName); err == nil {
		t.Fatal("duplicate-only Herdr session owner read remained available")
	}
	if err := rt.SetSessionEnabledMarker(context.Background(), contextID, sessionName, true); err == nil {
		t.Fatal("duplicate-only Herdr session owner set remained available")
	}
	if err := ownershipBackend.ClearSessionOwnerMarker(context.Background(), sessionName); err != nil {
		t.Fatalf("duplicate-only Herdr session owner clear error = %v, want teardown-only clear to remain available", err)
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

	if _, _, err := workRuntime.Discover(context.Background(), baseDir, contextID); err != nil {
		t.Fatalf("Discover(work) error = %v", err)
	}
	otherNodes, _, err := otherRuntime.Discover(context.Background(), baseDir, contextID)
	if err != nil {
		t.Fatalf("Discover(other) error = %v", err)
	}
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
	snapshot    multiplexer.HerdrSessionSnapshot
	snapshotErr error

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
