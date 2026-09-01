package herdrruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/multiplexer"
)

const (
	herdr082SchemaProtocol = "20"
	herdr082SchemaVersion  = 1
)

var herdr082SchemaEnvelope = multiplexer.HerdrResponseEnvelope{
	ProtocolVersion: herdr082SchemaProtocol,
	SchemaVersion:   herdr082SchemaVersion,
}

// Fixtures are redacted from Herdr v0.8.2's generated API schema and source
// contract: commit 9eb521456ac0d19d3ab3d9d7cea3cca10baa8a4c.
const (
	herdr082PongFixture = `{"id":"postman:1","result":{"type":"pong","version":"0.8.2","protocol":20,"capabilities":{"live_handoff":true}}}` + "\n"
	herdr082SchemaJSON  = `{"protocol":20,"schema_version":1,"schemas":{}}`
)

func TestSocketClientPingUsesLivePongAndSchemaCommand(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "herdr.sock")
	requests := serveHerdrSocketSequence(t, socketPath, herdr082PongFixture)
	client := testSocketClient(socketPath)

	envelope, err := client.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if envelope != herdr082SchemaEnvelope {
		t.Fatalf("Ping() envelope = %#v, want live pong protocol plus schema version", envelope)
	}

	request := <-requests
	if _, ok := request["jsonrpc"]; ok {
		t.Fatalf("request includes jsonrpc field: %#v", request)
	}
	if request["id"] != "postman:1" || request["method"] != "ping" {
		t.Fatalf("request = %#v, want string id and ping method", request)
	}
	if params, ok := request["params"].(map[string]any); !ok || len(params) != 0 {
		t.Fatalf("request params = %#v, want empty object", request["params"])
	}
}

func TestSocketClientRejectsUnsupportedCompatibilityEvidence(t *testing.T) {
	tests := []struct {
		name     string
		response string
		schema   multiplexer.HerdrResponseEnvelope
	}{
		{name: "missing schema version", response: herdr082PongFixture, schema: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "20"}},
		{name: "missing schema protocol", response: herdr082PongFixture, schema: multiplexer.HerdrResponseEnvelope{SchemaVersion: 1}},
		{name: "schema protocol mismatch", response: herdr082PongFixture, schema: multiplexer.HerdrResponseEnvelope{ProtocolVersion: "19", SchemaVersion: 1}},
		{name: "pong protocol not numeric", response: `{"id":"postman:1","result":{"type":"pong","version":"0.8.2","protocol":"20"}}` + "\n", schema: herdr082SchemaEnvelope},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			socketPath := filepath.Join(t.TempDir(), "herdr.sock")
			serveHerdrSocketSequence(t, socketPath, tt.response)
			client := &socketClient{
				socketPath: socketPath,
				schema: func(context.Context) (multiplexer.HerdrResponseEnvelope, error) {
					return tt.schema, nil
				},
			}
			if _, err := client.Ping(context.Background()); err == nil {
				t.Fatal("Ping() error = nil, want compatibility rejection")
			}
		})
	}
}

func TestSocketClientRequiresMatchingStringResponseID(t *testing.T) {
	tests := []struct {
		name     string
		response string
	}{
		{name: "missing", response: `{"result":{"type":"pong","version":"0.8.2","protocol":20}}` + "\n"},
		{name: "null", response: `{"id":null,"result":{"type":"pong","version":"0.8.2","protocol":20}}` + "\n"},
		{name: "number", response: `{"id":1,"result":{"type":"pong","version":"0.8.2","protocol":20}}` + "\n"},
		{name: "object", response: `{"id":{},"result":{"type":"pong","version":"0.8.2","protocol":20}}` + "\n"},
		{name: "array", response: `{"id":[],"result":{"type":"pong","version":"0.8.2","protocol":20}}` + "\n"},
		{name: "mismatch", response: `{"id":"postman:2","result":{"type":"pong","version":"0.8.2","protocol":20}}` + "\n"},
		{name: "malformed", response: `{"id":"postman:1","result":` + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			socketPath := filepath.Join(t.TempDir(), "herdr.sock")
			serveHerdrSocketSequence(t, socketPath, tt.response)
			client := testSocketClient(socketPath)
			if _, err := client.Ping(context.Background()); err == nil {
				t.Fatal("Ping() error = nil, want response id rejection")
			}
		})
	}
}

func TestSocketClientSessionSnapshotUsesHerdrLineProtocol(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "herdr.sock")
	requests := serveHerdrSocketSequence(t, socketPath,
		herdr082PongFixture,
		`{"id":"postman:2","result":{"type":"session_snapshot","snapshot":`+herdr082SnapshotFixture+`}}`+"\n",
	)
	client, err := NewSocketClient(config.HerdrConfig{SocketPath: socketPath})
	if err != nil {
		t.Fatalf("NewSocketClient() error = %v", err)
	}
	client.(*socketClient).schema = testSchemaLoader

	snapshot, err := client.SessionSnapshot(context.Background())
	if err != nil {
		t.Fatalf("SessionSnapshot() error = %v", err)
	}
	if snapshot.Panes[0].ID != "pane-1" || snapshot.Panes[0].TerminalID != "terminal-1" {
		t.Fatalf("pane = %#v, want pane_id and terminal_id from Herdr snapshot", snapshot.Panes[0])
	}
	if snapshot.Envelope != herdr082SchemaEnvelope {
		t.Fatalf("envelope = %#v, want negotiated compatibility envelope", snapshot.Envelope)
	}

	<-requests
	request := <-requests
	if request["id"] != "postman:2" || request["method"] != "session.snapshot" {
		t.Fatalf("request = %#v, want session.snapshot with postman:2 id", request)
	}
	if params, ok := request["params"].(map[string]any); !ok || len(params) != 0 {
		t.Fatalf("request params = %#v, want empty object", request["params"])
	}
}

func TestSocketClientReadProcessAndWritesUseStrictTaggedResults(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "herdr.sock")
	requests := serveHerdrSocketSequence(t, socketPath,
		herdr082PongFixture,
		`{"id":"postman:2","result":{"type":"pane_read","read":{"pane_id":"pane-1","workspace_id":"workspace-1","tab_id":"tab-1","source":"recent","format":"text","text":"hello\n","revision":8,"truncated":false}}}`+"\n",
		`{"id":"postman:3","result":{"type":"pong","version":"0.8.2","protocol":20}}`+"\n",
		`{"id":"postman:4","result":{"type":"pane_process_info","process_info":{"pane_id":"pane-1","shell_pid":456,"foreground_process_group_id":789,"foreground_processes":[{"pid":123,"name":"codex","argv":["/usr/bin/codex","--yolo"],"argv0":"codex","cmdline":"/usr/bin/codex --yolo","cwd":"/tmp"}]}}}`+"\n",
		`{"id":"postman:5","result":{"type":"pong","version":"0.8.2","protocol":20}}`+"\n",
		`{"id":"postman:6","result":{"type":"ok"}}`+"\n",
		`{"id":"postman:7","result":{"type":"pong","version":"0.8.2","protocol":20}}`+"\n",
		`{"id":"postman:8","result":{"type":"ok"}}`+"\n",
		`{"id":"postman:9","result":{"type":"pong","version":"0.8.2","protocol":20}}`+"\n",
		`{"id":"postman:10","result":{"type":"ok"}}`+"\n",
		`{"id":"postman:11","result":{"type":"pong","version":"0.8.2","protocol":20}}`+"\n",
		`{"id":"postman:12","result":{"type":"ok"}}`+"\n",
		`{"id":"postman:13","result":{"type":"pong","version":"0.8.2","protocol":20}}`+"\n",
		`{"id":"postman:14","result":{"type":"ok"}}`+"\n",
		`{"id":"postman:15","result":{"type":"pong","version":"0.8.2","protocol":20}}`+"\n",
		`{"id":"postman:16","result":{"type":"ok"}}`+"\n",
	)
	client := testSocketClient(socketPath)

	read, err := client.ReadPane(context.Background(), "pane-1", multiplexer.HerdrPaneReadOptions{Source: "recent", TailLines: 25})
	if err != nil {
		t.Fatalf("ReadPane() error = %v", err)
	}
	if read.Text != "hello\n" || read.Envelope != herdr082SchemaEnvelope {
		t.Fatalf("ReadPane() = %#v, want text and negotiated envelope", read)
	}
	processInfo, err := client.PaneProcessInfo(context.Background(), "pane-1")
	if err != nil {
		t.Fatalf("PaneProcessInfo() error = %v", err)
	}
	if processInfo.ProcessInfo.PaneID != "pane-1" || processInfo.ProcessInfo.CurrentCommand() != "codex" {
		t.Fatalf("ProcessInfo = %#v, want pane id and codex command", processInfo.ProcessInfo)
	}
	if processInfo.ProcessInfo.ShellPID != 456 || processInfo.ProcessInfo.ForegroundProcessID != 789 {
		t.Fatalf("ProcessInfo scalar IDs = shell:%d foreground:%d, want supported snake_case values", processInfo.ProcessInfo.ShellPID, processInfo.ProcessInfo.ForegroundProcessID)
	}
	writeCalls := []struct {
		name string
		run  func(context.Context) (multiplexer.HerdrWriteResult, error)
	}{
		{name: "write text", run: func(ctx context.Context) (multiplexer.HerdrWriteResult, error) {
			return client.WritePaneText(ctx, "pane-1", "hello")
		}},
		{name: "send key", run: func(ctx context.Context) (multiplexer.HerdrWriteResult, error) {
			return client.SendPaneKey(ctx, "pane-1", multiplexer.HerdrKeySubmit)
		}},
		{name: "set pane metadata", run: func(ctx context.Context) (multiplexer.HerdrWriteResult, error) {
			return client.SetPaneMetadata(ctx, "pane-1", "postman.node", "worker")
		}},
		{name: "clear pane metadata", run: func(ctx context.Context) (multiplexer.HerdrWriteResult, error) {
			return client.ClearPaneMetadata(ctx, "pane-1", "postman.node")
		}},
		{name: "set workspace metadata", run: func(ctx context.Context) (multiplexer.HerdrWriteResult, error) {
			return client.SetWorkspaceMetadata(ctx, "workspace-1", "postman.session", "work")
		}},
		{name: "clear workspace metadata", run: func(ctx context.Context) (multiplexer.HerdrWriteResult, error) {
			return client.ClearWorkspaceMetadata(ctx, "workspace-1", "postman.session")
		}},
	}
	for _, call := range writeCalls {
		result, err := call.run(context.Background())
		if err != nil {
			t.Fatalf("%s error = %v", call.name, err)
		}
		if result.Envelope != herdr082SchemaEnvelope {
			t.Fatalf("%s envelope = %#v, want negotiated envelope", call.name, result.Envelope)
		}
	}

	assertRequestParams(t, <-requests, "ping", map[string]any{})
	readParams := assertRequestParams(t, <-requests, "pane.read", map[string]any{"pane_id": "pane-1", "source": "recent", "lines": float64(25)})
	if len(readParams) != 3 {
		t.Fatalf("pane.read params = %#v, want exactly pane_id/source/lines", readParams)
	}
	assertRequestParams(t, <-requests, "ping", map[string]any{})
	assertRequestParams(t, <-requests, "pane.process_info", map[string]any{"pane_id": "pane-1"})
	assertRequestParams(t, <-requests, "ping", map[string]any{})
	assertRequestParams(t, <-requests, "pane.send_text", map[string]any{"pane_id": "pane-1", "text": "hello"})
	assertRequestParams(t, <-requests, "ping", map[string]any{})
	keyParams := assertRequestParams(t, <-requests, "pane.send_keys", map[string]any{"pane_id": "pane-1"})
	keys, ok := keyParams["keys"].([]any)
	if !ok || len(keys) != 1 || keys[0] != multiplexer.HerdrKeySubmit {
		t.Fatalf("pane.send_keys keys = %#v, want submit key", keyParams["keys"])
	}
	assertRequestParams(t, <-requests, "ping", map[string]any{})
	setPaneParams := assertRequestParams(t, <-requests, "pane.report_metadata", map[string]any{"pane_id": "pane-1", "source": "tmux-a2a-postman"})
	assertTokenValue(t, setPaneParams, "postman.node", "worker")
	assertRequestParams(t, <-requests, "ping", map[string]any{})
	clearPaneParams := assertRequestParams(t, <-requests, "pane.report_metadata", map[string]any{"pane_id": "pane-1", "source": "tmux-a2a-postman"})
	assertTokenValue(t, clearPaneParams, "postman.node", nil)
	assertRequestParams(t, <-requests, "ping", map[string]any{})
	setWorkspaceParams := assertRequestParams(t, <-requests, "workspace.report_metadata", map[string]any{"workspace_id": "workspace-1", "source": "tmux-a2a-postman"})
	assertTokenValue(t, setWorkspaceParams, "postman.session", "work")
	assertRequestParams(t, <-requests, "ping", map[string]any{})
	clearWorkspaceParams := assertRequestParams(t, <-requests, "workspace.report_metadata", map[string]any{"workspace_id": "workspace-1", "source": "tmux-a2a-postman"})
	assertTokenValue(t, clearWorkspaceParams, "postman.session", nil)
}

func TestSocketClientRejectsMalformedTaggedResults(t *testing.T) {
	tests := []struct {
		name      string
		responses []string
		run       func(*socketClient) error
	}{
		{name: "ping wrong type", responses: []string{`{"id":"postman:1","result":{"type":"pane_read","version":"0.8.2","protocol":20}}` + "\n"}, run: func(c *socketClient) error { _, err := c.Ping(context.Background()); return err }},
		{name: "snapshot missing wrapper", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"session_snapshot"}}` + "\n"}, run: func(c *socketClient) error { _, err := c.SessionSnapshot(context.Background()); return err }},
		{name: "snapshot missing nested fields", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"session_snapshot","snapshot":{"version":"0.8.2","protocol":20,"workspaces":[],"tabs":[],"panes":[]}}}` + "\n"}, run: func(c *socketClient) error { _, err := c.SessionSnapshot(context.Background()); return err }},
		{name: "snapshot null layouts", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"session_snapshot","snapshot":{"version":"0.8.2","protocol":20,"workspaces":[],"tabs":[],"panes":[],"layouts":null,"agents":[]}}}` + "\n"}, run: func(c *socketClient) error { _, err := c.SessionSnapshot(context.Background()); return err }},
		{name: "snapshot null agents", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"session_snapshot","snapshot":{"version":"0.8.2","protocol":20,"workspaces":[],"tabs":[],"panes":[],"layouts":[],"agents":null}}}` + "\n"}, run: func(c *socketClient) error { _, err := c.SessionSnapshot(context.Background()); return err }},
		{name: "snapshot object layouts", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"session_snapshot","snapshot":{"version":"0.8.2","protocol":20,"workspaces":[],"tabs":[],"panes":[],"layouts":{},"agents":[]}}}` + "\n"}, run: func(c *socketClient) error { _, err := c.SessionSnapshot(context.Background()); return err }},
		{name: "snapshot string agents", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"session_snapshot","snapshot":{"version":"0.8.2","protocol":20,"workspaces":[],"tabs":[],"panes":[],"layouts":[],"agents":"bad"}}}` + "\n"}, run: func(c *socketClient) error { _, err := c.SessionSnapshot(context.Background()); return err }},
		{name: "snapshot incomplete workspace", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"session_snapshot","snapshot":{"version":"0.8.2","protocol":20,"workspaces":[{"workspace_id":"workspace-1"}],"tabs":[],"panes":[],"layouts":[],"agents":[]}}}` + "\n"}, run: func(c *socketClient) error { _, err := c.SessionSnapshot(context.Background()); return err }},
		{name: "snapshot incomplete tab", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"session_snapshot","snapshot":{"version":"0.8.2","protocol":20,"workspaces":[],"tabs":[{"tab_id":"tab-1"}],"panes":[],"layouts":[],"agents":[]}}}` + "\n"}, run: func(c *socketClient) error { _, err := c.SessionSnapshot(context.Background()); return err }},
		{name: "snapshot incomplete pane", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"session_snapshot","snapshot":{"version":"0.8.2","protocol":20,"workspaces":[],"tabs":[],"panes":[{"pane_id":"pane-1"}],"layouts":[],"agents":[]}}}` + "\n"}, run: func(c *socketClient) error { _, err := c.SessionSnapshot(context.Background()); return err }},
		{name: "snapshot alternate process shape", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"session_snapshot","snapshot":{"version":"0.8.2","protocol":20,"workspaces":[],"tabs":[],"panes":[{"pane_id":"pane-1","terminal_id":"terminal-1","workspace_id":"workspace-1","tab_id":"tab-1","focused":true,"agent_status":"working","revision":7,"processInfo":{"foregroundProcesses":[{"name":"codex"}]}}],"layouts":[],"agents":[]}}}` + "\n"}, run: func(c *socketClient) error { _, err := c.SessionSnapshot(context.Background()); return err }},
		{name: "snapshot nested shellPID alias", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"session_snapshot","snapshot":{"version":"0.8.2","protocol":20,"workspaces":[],"tabs":[],"panes":[{"pane_id":"pane-1","terminal_id":"terminal-1","workspace_id":"workspace-1","tab_id":"tab-1","focused":true,"agent_status":"working","revision":7,"process_info":{"pane_id":"pane-1","shellPID":456}}],"layouts":[],"agents":[]}}}` + "\n"}, run: func(c *socketClient) error { _, err := c.SessionSnapshot(context.Background()); return err }},
		{name: "snapshot nested foregroundProcessID alias", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"session_snapshot","snapshot":{"version":"0.8.2","protocol":20,"workspaces":[],"tabs":[],"panes":[{"pane_id":"pane-1","terminal_id":"terminal-1","workspace_id":"workspace-1","tab_id":"tab-1","focused":true,"agent_status":"working","revision":7,"process_info":{"pane_id":"pane-1","foregroundProcessID":789}}],"layouts":[],"agents":[]}}}` + "\n"}, run: func(c *socketClient) error { _, err := c.SessionSnapshot(context.Background()); return err }},
		{name: "read wrong type", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"pong","read":{"text":"x"}}}` + "\n"}, run: func(c *socketClient) error {
			_, err := c.ReadPane(context.Background(), "pane-1", multiplexer.HerdrPaneReadOptions{})
			return err
		}},
		{name: "read missing text", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"pane_read","read":{}}}` + "\n"}, run: func(c *socketClient) error {
			_, err := c.ReadPane(context.Background(), "pane-1", multiplexer.HerdrPaneReadOptions{})
			return err
		}},
		{name: "read null revision", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"pane_read","read":{"pane_id":"pane-1","workspace_id":"workspace-1","tab_id":"tab-1","source":"recent","format":"text","text":"x","revision":null,"truncated":false}}}` + "\n"}, run: func(c *socketClient) error {
			_, err := c.ReadPane(context.Background(), "pane-1", multiplexer.HerdrPaneReadOptions{})
			return err
		}},
		{name: "process wrong type", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"process_info","process_info":{"pane_id":"pane-1"}}}` + "\n"}, run: func(c *socketClient) error { _, err := c.PaneProcessInfo(context.Background(), "pane-1"); return err }},
		{name: "process missing pane id", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"pane_process_info","process_info":{}}}` + "\n"}, run: func(c *socketClient) error { _, err := c.PaneProcessInfo(context.Background(), "pane-1"); return err }},
		{name: "process entry missing pid", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"pane_process_info","process_info":{"pane_id":"pane-1","foreground_processes":[{"name":"codex"}]}}}` + "\n"}, run: func(c *socketClient) error { _, err := c.PaneProcessInfo(context.Background(), "pane-1"); return err }},
		{name: "process entry missing name", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"pane_process_info","process_info":{"pane_id":"pane-1","foreground_processes":[{"pid":123}]}}}` + "\n"}, run: func(c *socketClient) error { _, err := c.PaneProcessInfo(context.Background(), "pane-1"); return err }},
		{name: "process alternate process shape", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"pane_process_info","process_info":{"pane_id":"pane-1","foregroundProcesses":[{"name":"codex"}]}}}` + "\n"}, run: func(c *socketClient) error { _, err := c.PaneProcessInfo(context.Background(), "pane-1"); return err }},
		{name: "process shellPID alias", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"pane_process_info","process_info":{"pane_id":"pane-1","shellPID":456}}}` + "\n"}, run: func(c *socketClient) error { _, err := c.PaneProcessInfo(context.Background(), "pane-1"); return err }},
		{name: "process foregroundProcessID alias", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"pane_process_info","process_info":{"pane_id":"pane-1","foregroundProcessID":789}}}` + "\n"}, run: func(c *socketClient) error { _, err := c.PaneProcessInfo(context.Background(), "pane-1"); return err }},
		{name: "write wrong type", responses: []string{herdr082PongFixture, `{"id":"postman:2","result":{"type":"pane_read"}}` + "\n"}, run: func(c *socketClient) error {
			_, err := c.WritePaneText(context.Background(), "pane-1", "x")
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			socketPath := filepath.Join(t.TempDir(), "herdr.sock")
			serveHerdrSocketSequence(t, socketPath, tt.responses...)
			if err := tt.run(testSocketClient(socketPath)); err == nil {
				t.Fatal("operation error = nil, want strict decoder rejection")
			}
		})
	}
}

func TestSocketClientHandlesStringErrorResponses(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "herdr.sock")
	serveHerdrSocketSequence(t, socketPath, `{"id":"postman:1","error":{"code":"unsupported_protocol","message":"unsupported"}}`+"\n")
	if _, err := testSocketClient(socketPath).Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "unsupported_protocol") {
		t.Fatalf("Ping() error = %v, want string error code", err)
	}
}

func TestSocketClientReturnsCompatibilityForBackendGateToReject(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "herdr.sock")
	serveHerdrSocketSequence(t, socketPath, `{"id":"postman:1","result":{"type":"pong","version":"0.8.2","protocol":19}}`+"\n")
	client := &socketClient{
		socketPath: socketPath,
		schema: func(context.Context) (multiplexer.HerdrResponseEnvelope, error) {
			return multiplexer.HerdrResponseEnvelope{ProtocolVersion: "19", SchemaVersion: 2}, nil
		},
	}
	envelope, err := client.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	policy := multiplexer.HerdrGatePolicy{
		ReadEnabled:             true,
		ReadScope:               multiplexer.HerdrReadScopeDiscovery,
		AllowedSocketPaths:      []string{"/tmp/herdr.sock"},
		AllowedSessions:         []string{"work"},
		AllowedWorkspaceIDs:     []string{"workspace-1"},
		AllowedProtocolVersions: []string{"20"},
		AllowedSchemaVersions:   []int{1},
		InputSanitizerReady:     true,
		ComplianceDecision:      multiplexer.HerdrComplianceDecisionRecorded,
	}
	runtime := multiplexer.HerdrRuntimeIdentity{SocketPath: "/tmp/herdr.sock", SessionName: "work", WorkspaceID: "workspace-1"}
	if err := multiplexer.ValidateHerdrReadGate(policy, runtime, envelope); err == nil {
		t.Fatal("ValidateHerdrReadGate() error = nil, want unsupported compatibility rejection")
	}
}

func TestLoadHerdrAPISchemaRejectsCommandFailureAndMalformedJSON(t *testing.T) {
	tests := []struct {
		name   string
		script string
	}{
		{name: "command failure", script: "#!/bin/sh\nexit 42\n"},
		{name: "malformed json", script: "#!/bin/sh\nprintf '{not-json}\\n'\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installFakeHerdrCommand(t, tt.script)
			if _, err := loadHerdrAPISchema(context.Background()); err == nil {
				t.Fatal("loadHerdrAPISchema() error = nil, want failure")
			}
		})
	}
}

func TestSocketClientCompatibilityBoundsHangingSchemaCommand(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "herdr.sock")
	serveHerdrSocketSequence(t, socketPath, herdr082PongFixture)
	installFakeHerdrCommand(t, "#!/bin/sh\nsleep 30\n")
	client, err := NewSocketClient(config.HerdrConfig{SocketPath: socketPath})
	if err != nil {
		t.Fatalf("NewSocketClient() error = %v", err)
	}
	started := time.Now()
	_, err = client.Ping(context.Background())
	if err == nil {
		t.Fatal("Ping() error = nil, want schema command timeout")
	}
	if elapsed := time.Since(started); elapsed > defaultSocketCallTimeout+2*time.Second {
		t.Fatalf("Ping() elapsed = %v, want bounded by default timeout", elapsed)
	}
}

const herdr082SnapshotFixture = `{"version":"0.8.2","protocol":20,"focused_workspace_id":"workspace-1","focused_tab_id":"tab-1","focused_pane_id":"pane-1","workspaces":[{"workspace_id":"workspace-1","number":1,"label":"work","focused":true,"pane_count":1,"tab_count":1,"active_tab_id":"tab-1","agent_status":"working","metadata":{"postman.session":"work"}}],"tabs":[{"tab_id":"tab-1","workspace_id":"workspace-1","number":1,"label":"main","focused":true,"pane_count":1,"agent_status":"working"}],"panes":[{"pane_id":"pane-1","terminal_id":"terminal-1","workspace_id":"workspace-1","tab_id":"tab-1","focused":true,"agent_status":"working","revision":7,"metadata":{"postman.node":"worker"}}],"layouts":[],"agents":[]}`

func testSocketClient(socketPath string) *socketClient {
	return &socketClient{socketPath: socketPath, schema: testSchemaLoader}
}

func testSchemaLoader(context.Context) (multiplexer.HerdrResponseEnvelope, error) {
	var decoded struct {
		Protocol      json.RawMessage `json:"protocol"`
		SchemaVersion int             `json:"schema_version"`
	}
	_ = json.Unmarshal([]byte(herdr082SchemaJSON), &decoded)
	return multiplexer.HerdrResponseEnvelope{
		ProtocolVersion: decodeHerdrProtocolVersion(decoded.Protocol),
		SchemaVersion:   decoded.SchemaVersion,
	}, nil
}

func assertRequestParams(t *testing.T, request map[string]any, method string, want map[string]any) map[string]any {
	t.Helper()
	if request["method"] != method {
		t.Fatalf("request method = %#v, want %s in request %#v", request["method"], method, request)
	}
	params, ok := request["params"].(map[string]any)
	if !ok {
		t.Fatalf("%s params = %#v, want object", method, request["params"])
	}
	for key, value := range want {
		if params[key] != value {
			t.Fatalf("%s params[%s] = %#v, want %#v in %#v", method, key, params[key], value, params)
		}
	}
	if _, ok := params["paneId"]; ok {
		t.Fatalf("%s params include paneId: %#v", method, params)
	}
	if _, ok := params["tail_lines"]; ok {
		t.Fatalf("%s params include tail_lines: %#v", method, params)
	}
	return params
}

func assertTokenValue(t *testing.T, params map[string]any, key string, want any) {
	t.Helper()
	tokens, ok := params["tokens"].(map[string]any)
	if !ok {
		t.Fatalf("tokens = %#v, want object", params["tokens"])
	}
	got, ok := tokens[key]
	if !ok {
		t.Fatalf("tokens = %#v, want key %q", tokens, key)
	}
	if got != want {
		t.Fatalf("tokens[%q] = %#v, want %#v", key, got, want)
	}
	if len(tokens) != 1 {
		t.Fatalf("tokens = %#v, want exactly one token", tokens)
	}
}

func installFakeHerdrCommand(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "herdr")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake herdr) error = %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func serveHerdrSocketSequence(t *testing.T, socketPath string, responses ...string) <-chan map[string]any {
	t.Helper()
	requests := make(chan map[string]any, len(responses))
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen(unix) error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for _, response := range responses {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			line, err := bufio.NewReader(conn).ReadString('\n')
			if err == nil {
				var request map[string]any
				if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &request); err == nil {
					requests <- request
				}
				_, _ = conn.Write([]byte(response))
			}
			_ = conn.Close()
		}
	}()
	return requests
}
