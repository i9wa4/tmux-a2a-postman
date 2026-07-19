package herdrruntime_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
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

func validRuntimeHerdrConfig() config.HerdrConfig {
	return config.HerdrConfig{
		Enabled:                 true,
		SocketPath:              "/tmp/herdr.sock",
		SessionName:             "work",
		WorkspaceID:             "workspace-1",
		AllowedSocketPaths:      []string{"/tmp/herdr.sock"},
		AllowedSessions:         []string{"work"},
		AllowedWorkspaceIDs:     []string{"workspace-1"},
		AllowedProtocolVersions: []string{"1"},
		AllowedSchemaVersions:   []int{1},
		ReadEnabled:             true,
		WriteEnabled:            true,
		InputSanitizerReady:     true,
		ComplianceDecision:      string(multiplexer.HerdrComplianceDecisionAGPL),
	}
}

func validRuntimeHerdrSnapshot() multiplexer.HerdrSessionSnapshot {
	return multiplexer.HerdrSessionSnapshot{
		Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1},
		Workspaces: []multiplexer.HerdrWorkspaceSnapshot{{
			ID:       "workspace-1",
			Metadata: map[string]string{},
		}},
		Tabs: []multiplexer.HerdrTabSnapshot{{
			ID:          "workspace-1:tab-1",
			WorkspaceID: "workspace-1",
			Metadata:    map[string]string{},
		}},
		Panes: []multiplexer.HerdrPaneSnapshot{{
			ID:             "workspace-1:pane-1",
			WorkspaceID:    "workspace-1",
			TabID:          "workspace-1:tab-1",
			Metadata:       map[string]string{"postman.node": "worker"},
			Env:            map[string]string{},
			ProcessInfo:    multiplexer.HerdrPaneProcessInfo{ForegroundProcesses: []multiplexer.HerdrProcessInfo{{Name: "codex"}}},
			PostmanNode:    "worker",
			PostmanSession: "work",
		}},
	}
}

type fakeRuntimeHerdrClient struct {
	snapshot multiplexer.HerdrSessionSnapshot

	writeTextCalls int
	writeTextPane  string
	sendKeyCalls   int
	sendKeyKey     string

	setPaneMetadataPane  string
	setPaneMetadataKey   string
	setPaneMetadataValue string
}

func (f *fakeRuntimeHerdrClient) Ping(context.Context) (multiplexer.HerdrResponseEnvelope, error) {
	return multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}, nil
}

func (f *fakeRuntimeHerdrClient) SessionSnapshot(context.Context) (multiplexer.HerdrSessionSnapshot, error) {
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

func (f *fakeRuntimeHerdrClient) SetWorkspaceMetadata(context.Context, string, string, string) (multiplexer.HerdrWriteResult, error) {
	return multiplexer.HerdrWriteResult{Envelope: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "1", SchemaVersion: 1}}, nil
}

func (f *fakeRuntimeHerdrClient) ClearWorkspaceMetadata(context.Context, string, string) (multiplexer.HerdrWriteResult, error) {
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
