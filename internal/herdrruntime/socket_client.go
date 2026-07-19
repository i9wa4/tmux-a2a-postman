package herdrruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync/atomic"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/multiplexer"
)

type socketClient struct {
	socketPath string
	nextID     atomic.Int64
}

type socketRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type socketResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *socketError    `json:"error,omitempty"`
}

type socketError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func NewSocketClient(cfg config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
	socketPath := strings.TrimSpace(cfg.SocketPath)
	if socketPath == "" {
		return nil, fmt.Errorf("%w: herdr socket_path is empty", multiplexer.ErrHerdrBackendUnavailable)
	}
	return &socketClient{socketPath: socketPath}, nil
}

func (c *socketClient) Ping(ctx context.Context) (multiplexer.HerdrResponseEnvelope, error) {
	result, err := c.call(ctx, "ping", nil)
	if err != nil {
		return multiplexer.HerdrResponseEnvelope{}, err
	}
	return decodeHerdrEnvelope(result)
}

func (c *socketClient) SessionSnapshot(ctx context.Context) (multiplexer.HerdrSessionSnapshot, error) {
	result, err := c.call(ctx, "session.snapshot", nil)
	if err != nil {
		return multiplexer.HerdrSessionSnapshot{}, err
	}
	return decodeHerdrSessionSnapshot(result)
}

func (c *socketClient) ReadPane(ctx context.Context, paneID string, opts multiplexer.HerdrPaneReadOptions) (multiplexer.HerdrPaneReadResult, error) {
	result, err := c.call(ctx, "pane.read", map[string]interface{}{
		"pane_id":    paneID,
		"paneId":     paneID,
		"source":     opts.Source,
		"tail_lines": opts.TailLines,
		"tailLines":  opts.TailLines,
	})
	if err != nil {
		return multiplexer.HerdrPaneReadResult{}, err
	}
	var raw struct {
		Envelope multiplexer.HerdrResponseEnvelope `json:"envelope"`
		Text     string                            `json:"text"`
		Content  string                            `json:"content"`
		Output   string                            `json:"output"`
	}
	_ = json.Unmarshal(result, &raw)
	envelope, err := decodeHerdrEnvelope(result)
	if err != nil {
		return multiplexer.HerdrPaneReadResult{}, err
	}
	text := raw.Text
	if text == "" {
		text = raw.Content
	}
	if text == "" {
		text = raw.Output
	}
	return multiplexer.HerdrPaneReadResult{Envelope: envelope, Text: text}, nil
}

func (c *socketClient) PaneProcessInfo(ctx context.Context, paneID string) (multiplexer.HerdrPaneProcessInfoResult, error) {
	result, err := c.call(ctx, "pane.process_info", map[string]interface{}{"pane_id": paneID, "paneId": paneID})
	if err != nil {
		return multiplexer.HerdrPaneProcessInfoResult{}, err
	}
	var raw struct {
		ProcessInfo      rawHerdrProcessInfo              `json:"process_info"`
		ProcessInfoCamel multiplexer.HerdrPaneProcessInfo `json:"processInfo"`
	}
	_ = json.Unmarshal(result, &raw)
	envelope, err := decodeHerdrEnvelope(result)
	if err != nil {
		return multiplexer.HerdrPaneProcessInfoResult{}, err
	}
	info := raw.ProcessInfo.toSnapshot()
	if len(info.ForegroundProcesses) == 0 {
		info = raw.ProcessInfoCamel
	}
	return multiplexer.HerdrPaneProcessInfoResult{Envelope: envelope, ProcessInfo: info}, nil
}

func (c *socketClient) WritePaneText(ctx context.Context, paneID string, text string) (multiplexer.HerdrWriteResult, error) {
	return c.write(ctx, "pane.send_text", map[string]interface{}{"pane_id": paneID, "text": text})
}

func (c *socketClient) SendPaneKey(ctx context.Context, paneID string, key string) (multiplexer.HerdrWriteResult, error) {
	return c.write(ctx, "pane.send_keys", map[string]interface{}{"pane_id": paneID, "keys": []string{key}})
}

func (c *socketClient) SetWorkspaceMetadata(ctx context.Context, workspaceID string, key string, value string) (multiplexer.HerdrWriteResult, error) {
	return c.write(ctx, "workspace.report_metadata", map[string]interface{}{"workspace_id": workspaceID, "source": "tmux-a2a-postman", "tokens": map[string]interface{}{key: value}})
}

func (c *socketClient) ClearWorkspaceMetadata(ctx context.Context, workspaceID string, key string) (multiplexer.HerdrWriteResult, error) {
	return c.write(ctx, "workspace.report_metadata", map[string]interface{}{"workspace_id": workspaceID, "source": "tmux-a2a-postman", "tokens": map[string]interface{}{key: nil}})
}

func (c *socketClient) SetPaneMetadata(ctx context.Context, paneID string, key string, value string) (multiplexer.HerdrWriteResult, error) {
	return c.write(ctx, "pane.report_metadata", map[string]interface{}{"pane_id": paneID, "source": "tmux-a2a-postman", "tokens": map[string]interface{}{key: value}})
}

func (c *socketClient) ClearPaneMetadata(ctx context.Context, paneID string, key string) (multiplexer.HerdrWriteResult, error) {
	return c.write(ctx, "pane.report_metadata", map[string]interface{}{"pane_id": paneID, "source": "tmux-a2a-postman", "tokens": map[string]interface{}{key: nil}})
}

func (c *socketClient) write(ctx context.Context, method string, params interface{}) (multiplexer.HerdrWriteResult, error) {
	result, err := c.call(ctx, method, params)
	if err != nil {
		return multiplexer.HerdrWriteResult{}, err
	}
	envelope, err := decodeHerdrEnvelope(result)
	if err != nil {
		return multiplexer.HerdrWriteResult{}, err
	}
	return multiplexer.HerdrWriteResult{Envelope: envelope}, nil
}

func (c *socketClient) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return nil, multiplexer.NormalizeHerdrBackendError(err)
	}
	defer func() { _ = conn.Close() }()

	id := c.nextID.Add(1)
	request := socketRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	payload = append(payload, '\n')
	if _, err := conn.Write(payload); err != nil {
		return nil, multiplexer.NormalizeHerdrBackendError(err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, multiplexer.NormalizeHerdrBackendError(err)
	}
	var response socketResponse
	if err := json.Unmarshal(bytes.TrimSpace(line), &response); err != nil {
		return nil, fmt.Errorf("%w: invalid herdr socket response: %v", multiplexer.ErrHerdrBackendUnavailable, err)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("herdr socket method %s failed: %s", method, response.Error.Message)
	}
	return response.Result, nil
}

func decodeHerdrEnvelope(raw json.RawMessage) (multiplexer.HerdrResponseEnvelope, error) {
	var decoded struct {
		Envelope        multiplexer.HerdrResponseEnvelope `json:"envelope"`
		ProtocolVersion string                            `json:"protocol_version"`
		ProtocolCamel   string                            `json:"protocolVersion"`
		SchemaVersion   int                               `json:"schema_version"`
		SchemaCamel     int                               `json:"schemaVersion"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return multiplexer.HerdrResponseEnvelope{}, err
	}
	envelope := decoded.Envelope
	if envelope.ProtocolVersion == "" {
		envelope.ProtocolVersion = decoded.ProtocolVersion
	}
	if envelope.ProtocolVersion == "" {
		envelope.ProtocolVersion = decoded.ProtocolCamel
	}
	if envelope.SchemaVersion == 0 {
		envelope.SchemaVersion = decoded.SchemaVersion
	}
	if envelope.SchemaVersion == 0 {
		envelope.SchemaVersion = decoded.SchemaCamel
	}
	return envelope, nil
}

func decodeHerdrSessionSnapshot(raw json.RawMessage) (multiplexer.HerdrSessionSnapshot, error) {
	var wrapper struct {
		Snapshot json.RawMessage `json:"snapshot"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return multiplexer.HerdrSessionSnapshot{}, err
	}
	source := raw
	if len(wrapper.Snapshot) > 0 && string(wrapper.Snapshot) != "null" {
		source = wrapper.Snapshot
	}
	snapshot, err := decodeHerdrSessionSnapshotObject(source)
	if err != nil {
		return multiplexer.HerdrSessionSnapshot{}, err
	}
	if snapshot.Envelope.ProtocolVersion == "" && snapshot.Envelope.SchemaVersion == 0 {
		envelope, err := decodeHerdrEnvelope(raw)
		if err != nil {
			return multiplexer.HerdrSessionSnapshot{}, err
		}
		snapshot.Envelope = envelope
	}
	return snapshot, nil
}

func decodeHerdrSessionSnapshotObject(raw json.RawMessage) (multiplexer.HerdrSessionSnapshot, error) {
	var decoded struct {
		Envelope           multiplexer.HerdrResponseEnvelope `json:"envelope"`
		FocusedWorkspaceID string                            `json:"focused_workspace_id"`
		FocusedWorkspace   string                            `json:"focusedWorkspaceID"`
		FocusedTabID       string                            `json:"focused_tab_id"`
		FocusedTab         string                            `json:"focusedTabID"`
		FocusedPaneID      string                            `json:"focused_pane_id"`
		FocusedPane        string                            `json:"focusedPaneID"`
		Workspaces         []rawHerdrWorkspace               `json:"workspaces"`
		Tabs               []rawHerdrTab                     `json:"tabs"`
		Panes              []rawHerdrPane                    `json:"panes"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return multiplexer.HerdrSessionSnapshot{}, err
	}
	snapshot := multiplexer.HerdrSessionSnapshot{
		Envelope:           decoded.Envelope,
		FocusedWorkspaceID: firstNonEmpty(decoded.FocusedWorkspaceID, decoded.FocusedWorkspace),
		FocusedTabID:       firstNonEmpty(decoded.FocusedTabID, decoded.FocusedTab),
		FocusedPaneID:      firstNonEmpty(decoded.FocusedPaneID, decoded.FocusedPane),
	}
	for _, workspace := range decoded.Workspaces {
		snapshot.Workspaces = append(snapshot.Workspaces, workspace.toSnapshot())
	}
	for _, tab := range decoded.Tabs {
		snapshot.Tabs = append(snapshot.Tabs, tab.toSnapshot())
	}
	for _, pane := range decoded.Panes {
		snapshot.Panes = append(snapshot.Panes, pane.toSnapshot())
	}
	return snapshot, nil
}

type rawHerdrWorkspace struct {
	ID       string            `json:"id"`
	Label    string            `json:"label"`
	Metadata map[string]string `json:"metadata"`
	Tokens   map[string]string `json:"tokens"`
}

func (w rawHerdrWorkspace) toSnapshot() multiplexer.HerdrWorkspaceSnapshot {
	return multiplexer.HerdrWorkspaceSnapshot{ID: w.ID, Label: w.Label, Metadata: mergeMetadataTokens(w.Metadata, w.Tokens)}
}

type rawHerdrTab struct {
	ID          string            `json:"id"`
	WorkspaceID string            `json:"workspace_id"`
	Workspace   string            `json:"workspaceId"`
	Label       string            `json:"label"`
	Order       int               `json:"order"`
	Metadata    map[string]string `json:"metadata"`
	Tokens      map[string]string `json:"tokens"`
}

func (t rawHerdrTab) toSnapshot() multiplexer.HerdrTabSnapshot {
	return multiplexer.HerdrTabSnapshot{
		ID:          t.ID,
		WorkspaceID: firstNonEmpty(t.WorkspaceID, t.Workspace),
		Label:       t.Label,
		Order:       t.Order,
		Metadata:    mergeMetadataTokens(t.Metadata, t.Tokens),
	}
}

type rawHerdrPane struct {
	ID             string                           `json:"id"`
	WorkspaceID    string                           `json:"workspace_id"`
	Workspace      string                           `json:"workspaceId"`
	TabID          string                           `json:"tab_id"`
	Tab            string                           `json:"tabId"`
	Label          string                           `json:"label"`
	Order          int                              `json:"order"`
	Metadata       map[string]string                `json:"metadata"`
	Tokens         map[string]string                `json:"tokens"`
	Env            map[string]string                `json:"env"`
	ProcessInfo    rawHerdrProcessInfo              `json:"process_info"`
	ProcessInfoAlt multiplexer.HerdrPaneProcessInfo `json:"processInfo"`
	AgentStatus    string                           `json:"agent_status"`
	AgentStatusAlt string                           `json:"agentStatus"`
	TerminalTitle  string                           `json:"terminal_title"`
	TerminalAlt    string                           `json:"terminalTitle"`
	ForegroundCWD  string                           `json:"foreground_cwd"`
	ForegroundAlt  string                           `json:"foregroundCWD"`
	Stale          bool                             `json:"stale"`
	StaleReason    string                           `json:"stale_reason"`
	StaleAlt       string                           `json:"staleReason"`
	PostmanNode    string                           `json:"postman_node"`
	PostmanNodeAlt string                           `json:"postmanNode"`
	PostmanSession string                           `json:"postman_session"`
	PostmanSessAlt string                           `json:"postmanSession"`
}

func (p rawHerdrPane) toSnapshot() multiplexer.HerdrPaneSnapshot {
	processInfo := p.ProcessInfo.toSnapshot()
	if len(processInfo.ForegroundProcesses) == 0 {
		processInfo = p.ProcessInfoAlt
	}
	return multiplexer.HerdrPaneSnapshot{
		ID:             p.ID,
		WorkspaceID:    firstNonEmpty(p.WorkspaceID, p.Workspace),
		TabID:          firstNonEmpty(p.TabID, p.Tab),
		Label:          p.Label,
		Order:          p.Order,
		Metadata:       mergeMetadataTokens(p.Metadata, p.Tokens),
		Env:            p.Env,
		ProcessInfo:    processInfo,
		AgentStatus:    firstNonEmpty(p.AgentStatus, p.AgentStatusAlt),
		TerminalTitle:  firstNonEmpty(p.TerminalTitle, p.TerminalAlt),
		ForegroundCWD:  firstNonEmpty(p.ForegroundCWD, p.ForegroundAlt),
		Stale:          p.Stale,
		StaleReason:    firstNonEmpty(p.StaleReason, p.StaleAlt),
		PostmanNode:    firstNonEmpty(p.PostmanNode, p.PostmanNodeAlt),
		PostmanSession: firstNonEmpty(p.PostmanSession, p.PostmanSessAlt),
	}
}

func mergeMetadataTokens(metadata, tokens map[string]string) map[string]string {
	if len(tokens) == 0 {
		return metadata
	}
	merged := make(map[string]string, len(metadata)+len(tokens))
	for key, value := range metadata {
		merged[key] = value
	}
	for key, value := range tokens {
		merged[key] = value
	}
	return merged
}

type rawHerdrProcessInfo struct {
	ShellPID               int                            `json:"shell_pid"`
	ShellPIDAlt            int                            `json:"shellPID"`
	ForegroundProcessID    int                            `json:"foreground_process_id"`
	ForegroundProcessIDAlt int                            `json:"foregroundProcessID"`
	ForegroundProcesses    []multiplexer.HerdrProcessInfo `json:"foreground_processes"`
	ForegroundProcessesAlt []multiplexer.HerdrProcessInfo `json:"foregroundProcesses"`
}

func (p rawHerdrProcessInfo) toSnapshot() multiplexer.HerdrPaneProcessInfo {
	info := multiplexer.HerdrPaneProcessInfo{
		ShellPID:            p.ShellPID,
		ForegroundProcessID: p.ForegroundProcessID,
		ForegroundProcesses: p.ForegroundProcesses,
	}
	if info.ShellPID == 0 {
		info.ShellPID = p.ShellPIDAlt
	}
	if info.ForegroundProcessID == 0 {
		info.ForegroundProcessID = p.ForegroundProcessIDAlt
	}
	if len(info.ForegroundProcesses) == 0 {
		info.ForegroundProcesses = p.ForegroundProcessesAlt
	}
	return info
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
