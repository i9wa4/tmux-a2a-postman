package herdrruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/multiplexer"
)

type socketClient struct {
	socketPath string
	nextID     atomic.Int64
	schema     herdrSchemaLoader
}

const defaultSocketCallTimeout = 5 * time.Second

type herdrSchemaLoader func(context.Context) (multiplexer.HerdrResponseEnvelope, error)

type socketRequest struct {
	ID     string      `json:"id"`
	Method string      `json:"method"`
	Params interface{} `json:"params"`
}

type socketResponse struct {
	ID     *string         `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *socketError    `json:"error,omitempty"`
}

type socketError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func NewSocketClient(cfg config.HerdrConfig) (multiplexer.HerdrReadClient, error) {
	socketPath := strings.TrimSpace(cfg.SocketPath)
	if socketPath == "" {
		return nil, fmt.Errorf("%w: herdr socket_path is empty", multiplexer.ErrHerdrBackendUnavailable)
	}
	return &socketClient{socketPath: socketPath, schema: loadHerdrAPISchema}, nil
}

func (c *socketClient) Ping(ctx context.Context) (multiplexer.HerdrResponseEnvelope, error) {
	return c.compatibilityEnvelope(ctx)
}

func (c *socketClient) SessionSnapshot(ctx context.Context) (multiplexer.HerdrSessionSnapshot, error) {
	envelope, err := c.compatibilityEnvelope(ctx)
	if err != nil {
		return multiplexer.HerdrSessionSnapshot{}, err
	}
	result, err := c.call(ctx, "session.snapshot", nil)
	if err != nil {
		return multiplexer.HerdrSessionSnapshot{}, err
	}
	snapshot, err := decodeHerdrSessionSnapshotResult(result)
	if err != nil {
		return multiplexer.HerdrSessionSnapshot{}, err
	}
	snapshot.Envelope = envelope
	return snapshot, nil
}

func (c *socketClient) ReadPane(ctx context.Context, paneID string, opts multiplexer.HerdrPaneReadOptions) (multiplexer.HerdrPaneReadResult, error) {
	envelope, err := c.compatibilityEnvelope(ctx)
	if err != nil {
		return multiplexer.HerdrPaneReadResult{}, err
	}
	params := map[string]interface{}{
		"pane_id": paneID,
		"source":  opts.Source,
	}
	if opts.TailLines > 0 {
		params["lines"] = opts.TailLines
	}
	result, err := c.call(ctx, "pane.read", params)
	if err != nil {
		return multiplexer.HerdrPaneReadResult{}, err
	}
	text, err := decodePaneReadResult(result)
	if err != nil {
		return multiplexer.HerdrPaneReadResult{}, err
	}
	return multiplexer.HerdrPaneReadResult{Envelope: envelope, Text: text}, nil
}

func (c *socketClient) PaneProcessInfo(ctx context.Context, paneID string) (multiplexer.HerdrPaneProcessInfoResult, error) {
	envelope, err := c.compatibilityEnvelope(ctx)
	if err != nil {
		return multiplexer.HerdrPaneProcessInfoResult{}, err
	}
	result, err := c.call(ctx, "pane.process_info", map[string]interface{}{"pane_id": paneID})
	if err != nil {
		return multiplexer.HerdrPaneProcessInfoResult{}, err
	}
	info, err := decodePaneProcessInfoResult(result)
	if err != nil {
		return multiplexer.HerdrPaneProcessInfoResult{}, err
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
	envelope, err := c.compatibilityEnvelope(ctx)
	if err != nil {
		return multiplexer.HerdrWriteResult{}, err
	}
	result, err := c.call(ctx, method, params)
	if err != nil {
		return multiplexer.HerdrWriteResult{}, err
	}
	if err := decodeOKResult(method, result); err != nil {
		return multiplexer.HerdrWriteResult{}, err
	}
	return multiplexer.HerdrWriteResult{Envelope: envelope}, nil
}

func (c *socketClient) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	callCtx := ctx
	cancel := func() {}
	if _, hasDeadline := callCtx.Deadline(); !hasDeadline {
		callCtx, cancel = context.WithTimeout(callCtx, defaultSocketCallTimeout)
	}
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(callCtx, "unix", c.socketPath)
	if err != nil {
		return nil, normalizeSocketCallError(callCtx, err)
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := callCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-callCtx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	id := fmt.Sprintf("postman:%d", c.nextID.Add(1))
	if params == nil {
		params = map[string]interface{}{}
	}
	request := socketRequest{ID: id, Method: method, Params: params}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	payload = append(payload, '\n')
	if _, err := conn.Write(payload); err != nil {
		return nil, normalizeSocketCallError(callCtx, err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, normalizeSocketCallError(callCtx, err)
	}
	var response socketResponse
	if err := json.Unmarshal(bytes.TrimSpace(line), &response); err != nil {
		return nil, fmt.Errorf("%w: invalid herdr socket response: %v", multiplexer.ErrHerdrBackendUnavailable, err)
	}
	if response.ID == nil {
		return nil, fmt.Errorf("%w: herdr socket method %s returned missing response id", multiplexer.ErrHerdrBackendUnavailable, method)
	}
	if *response.ID != id {
		return nil, fmt.Errorf("%w: herdr socket method %s returned mismatched response id %q", multiplexer.ErrHerdrBackendUnavailable, method, *response.ID)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("herdr socket method %s failed: %s: %s", method, response.Error.Code, response.Error.Message)
	}
	return response.Result, nil
}

func (c *socketClient) compatibilityEnvelope(ctx context.Context) (multiplexer.HerdrResponseEnvelope, error) {
	callCtx := ctx
	if callCtx == nil {
		callCtx = context.Background()
	}
	cancel := func() {}
	if _, hasDeadline := callCtx.Deadline(); !hasDeadline {
		callCtx, cancel = context.WithTimeout(callCtx, defaultSocketCallTimeout)
	}
	defer cancel()

	result, err := c.call(callCtx, "ping", nil)
	if err != nil {
		return multiplexer.HerdrResponseEnvelope{}, err
	}
	pong, err := decodePongResult(result)
	if err != nil {
		return multiplexer.HerdrResponseEnvelope{}, err
	}
	loader := c.schema
	if loader == nil {
		loader = loadHerdrAPISchema
	}
	schema, err := loader(callCtx)
	if err != nil {
		return multiplexer.HerdrResponseEnvelope{}, err
	}
	if schema.SchemaVersion <= 0 {
		return multiplexer.HerdrResponseEnvelope{}, fmt.Errorf("%w: herdr api schema missing supported schema_version", multiplexer.ErrHerdrBackendUnavailable)
	}
	if schema.ProtocolVersion == "" {
		return multiplexer.HerdrResponseEnvelope{}, fmt.Errorf("%w: herdr api schema missing numeric protocol", multiplexer.ErrHerdrBackendUnavailable)
	}
	if schema.ProtocolVersion != pong.ProtocolVersion {
		return multiplexer.HerdrResponseEnvelope{}, fmt.Errorf("%w: herdr socket protocol %s does not match schema protocol %s", multiplexer.ErrHerdrBackendUnavailable, pong.ProtocolVersion, schema.ProtocolVersion)
	}
	return multiplexer.HerdrResponseEnvelope{ProtocolVersion: pong.ProtocolVersion, SchemaVersion: schema.SchemaVersion}, nil
}

func loadHerdrAPISchema(ctx context.Context) (multiplexer.HerdrResponseEnvelope, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, "herdr", "api", "schema", "--json")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return multiplexer.HerdrResponseEnvelope{}, normalizeSocketCallError(ctx, err)
	}
	var decoded struct {
		Protocol      json.RawMessage `json:"protocol"`
		SchemaVersion int             `json:"schema_version"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		return multiplexer.HerdrResponseEnvelope{}, fmt.Errorf("%w: invalid herdr api schema: %v", multiplexer.ErrHerdrBackendUnavailable, err)
	}
	return multiplexer.HerdrResponseEnvelope{
		ProtocolVersion: decodeHerdrProtocolVersion(decoded.Protocol),
		SchemaVersion:   decoded.SchemaVersion,
	}, nil
}

func normalizeSocketCallError(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return multiplexer.NormalizeHerdrBackendError(err)
}

func decodeHerdrEnvelope(raw json.RawMessage) (multiplexer.HerdrResponseEnvelope, error) {
	var decoded struct {
		Envelope        multiplexer.HerdrResponseEnvelope `json:"envelope"`
		ProtocolVersion string                            `json:"protocol_version"`
		ProtocolCamel   string                            `json:"protocolVersion"`
		Protocol        json.RawMessage                   `json:"protocol"`
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
	if envelope.ProtocolVersion == "" {
		envelope.ProtocolVersion = decodeHerdrProtocolVersion(decoded.Protocol)
	}
	if envelope.SchemaVersion == 0 {
		envelope.SchemaVersion = decoded.SchemaVersion
	}
	if envelope.SchemaVersion == 0 {
		envelope.SchemaVersion = decoded.SchemaCamel
	}
	return envelope, nil
}

func decodeHerdrProtocolVersion(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var number int
	if err := json.Unmarshal(raw, &number); err == nil {
		return strconv.Itoa(number)
	}
	return ""
}

func decodePongResult(raw json.RawMessage) (multiplexer.HerdrResponseEnvelope, error) {
	var decoded struct {
		Type     string `json:"type"`
		Version  string `json:"version"`
		Protocol *int   `json:"protocol"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return multiplexer.HerdrResponseEnvelope{}, fmt.Errorf("%w: invalid ping result: %v", multiplexer.ErrHerdrBackendUnavailable, err)
	}
	if decoded.Type != "pong" {
		return multiplexer.HerdrResponseEnvelope{}, fmt.Errorf("%w: ping result type = %q, want pong", multiplexer.ErrHerdrBackendUnavailable, decoded.Type)
	}
	if strings.TrimSpace(decoded.Version) == "" {
		return multiplexer.HerdrResponseEnvelope{}, fmt.Errorf("%w: ping result missing version", multiplexer.ErrHerdrBackendUnavailable)
	}
	if decoded.Protocol == nil {
		return multiplexer.HerdrResponseEnvelope{}, fmt.Errorf("%w: ping result missing numeric protocol", multiplexer.ErrHerdrBackendUnavailable)
	}
	return multiplexer.HerdrResponseEnvelope{ProtocolVersion: strconv.Itoa(*decoded.Protocol)}, nil
}

func decodeHerdrSessionSnapshotResult(raw json.RawMessage) (multiplexer.HerdrSessionSnapshot, error) {
	var result struct {
		Type     string          `json:"type"`
		Snapshot json.RawMessage `json:"snapshot"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return multiplexer.HerdrSessionSnapshot{}, fmt.Errorf("%w: invalid session.snapshot result: %v", multiplexer.ErrHerdrBackendUnavailable, err)
	}
	if result.Type != "session_snapshot" {
		return multiplexer.HerdrSessionSnapshot{}, fmt.Errorf("%w: session.snapshot result type = %q, want session_snapshot", multiplexer.ErrHerdrBackendUnavailable, result.Type)
	}
	if len(result.Snapshot) == 0 || string(result.Snapshot) == "null" {
		return multiplexer.HerdrSessionSnapshot{}, fmt.Errorf("%w: session.snapshot result missing snapshot", multiplexer.ErrHerdrBackendUnavailable)
	}
	return decodeHerdrSessionSnapshotObject(result.Snapshot)
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
		envelope, err := decodeHerdrEnvelope(source)
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
		Version            string                            `json:"version"`
		Protocol           *int                              `json:"protocol"`
		FocusedWorkspaceID string                            `json:"focused_workspace_id"`
		FocusedWorkspace   string                            `json:"focusedWorkspaceID"`
		FocusedTabID       string                            `json:"focused_tab_id"`
		FocusedTab         string                            `json:"focusedTabID"`
		FocusedPaneID      string                            `json:"focused_pane_id"`
		FocusedPane        string                            `json:"focusedPaneID"`
		Workspaces         []rawHerdrWorkspace               `json:"workspaces"`
		Tabs               []rawHerdrTab                     `json:"tabs"`
		Panes              []rawHerdrPane                    `json:"panes"`
		Layouts            []json.RawMessage                 `json:"layouts"`
		Agents             []json.RawMessage                 `json:"agents"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return multiplexer.HerdrSessionSnapshot{}, err
	}
	if strings.TrimSpace(decoded.Version) == "" || decoded.Protocol == nil || decoded.Workspaces == nil || decoded.Tabs == nil || decoded.Panes == nil || decoded.Layouts == nil || decoded.Agents == nil {
		return multiplexer.HerdrSessionSnapshot{}, fmt.Errorf("%w: session.snapshot result missing mandatory snapshot fields", multiplexer.ErrHerdrBackendUnavailable)
	}
	snapshot := multiplexer.HerdrSessionSnapshot{
		Envelope:           decoded.Envelope,
		FocusedWorkspaceID: firstNonEmpty(decoded.FocusedWorkspaceID, decoded.FocusedWorkspace),
		FocusedTabID:       firstNonEmpty(decoded.FocusedTabID, decoded.FocusedTab),
		FocusedPaneID:      firstNonEmpty(decoded.FocusedPaneID, decoded.FocusedPane),
	}
	for _, workspace := range decoded.Workspaces {
		if err := workspace.validate(); err != nil {
			return multiplexer.HerdrSessionSnapshot{}, err
		}
		snapshot.Workspaces = append(snapshot.Workspaces, workspace.toSnapshot())
	}
	for _, tab := range decoded.Tabs {
		if err := tab.validate(); err != nil {
			return multiplexer.HerdrSessionSnapshot{}, err
		}
		snapshot.Tabs = append(snapshot.Tabs, tab.toSnapshot())
	}
	for _, pane := range decoded.Panes {
		if err := pane.validate(); err != nil {
			return multiplexer.HerdrSessionSnapshot{}, err
		}
		snapshot.Panes = append(snapshot.Panes, pane.toSnapshot())
	}
	return snapshot, nil
}

func decodePaneReadResult(raw json.RawMessage) (string, error) {
	var result struct {
		Type string `json:"type"`
		Read *struct {
			PaneID      string  `json:"pane_id"`
			WorkspaceID string  `json:"workspace_id"`
			TabID       string  `json:"tab_id"`
			Source      string  `json:"source"`
			Format      string  `json:"format"`
			Text        *string `json:"text"`
			Revision    *int    `json:"revision"`
			Truncated   *bool   `json:"truncated"`
		} `json:"read"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("%w: invalid pane.read result: %v", multiplexer.ErrHerdrBackendUnavailable, err)
	}
	if result.Type != "pane_read" {
		return "", fmt.Errorf("%w: pane.read result type = %q, want pane_read", multiplexer.ErrHerdrBackendUnavailable, result.Type)
	}
	if result.Read == nil || result.Read.Text == nil {
		return "", fmt.Errorf("%w: pane.read result missing read.text", multiplexer.ErrHerdrBackendUnavailable)
	}
	if strings.TrimSpace(result.Read.PaneID) == "" || strings.TrimSpace(result.Read.WorkspaceID) == "" || strings.TrimSpace(result.Read.TabID) == "" || strings.TrimSpace(result.Read.Source) == "" || strings.TrimSpace(result.Read.Format) == "" || result.Read.Revision == nil || result.Read.Truncated == nil {
		return "", fmt.Errorf("%w: pane.read result missing mandatory read fields", multiplexer.ErrHerdrBackendUnavailable)
	}
	return *result.Read.Text, nil
}

func decodePaneProcessInfoResult(raw json.RawMessage) (multiplexer.HerdrPaneProcessInfo, error) {
	var result struct {
		Type        string               `json:"type"`
		ProcessInfo *rawHerdrProcessInfo `json:"process_info"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return multiplexer.HerdrPaneProcessInfo{}, fmt.Errorf("%w: invalid pane.process_info result: %v", multiplexer.ErrHerdrBackendUnavailable, err)
	}
	if result.Type != "pane_process_info" {
		return multiplexer.HerdrPaneProcessInfo{}, fmt.Errorf("%w: pane.process_info result type = %q, want pane_process_info", multiplexer.ErrHerdrBackendUnavailable, result.Type)
	}
	if result.ProcessInfo == nil {
		return multiplexer.HerdrPaneProcessInfo{}, fmt.Errorf("%w: pane.process_info result missing process_info", multiplexer.ErrHerdrBackendUnavailable)
	}
	if err := result.ProcessInfo.validate(); err != nil {
		return multiplexer.HerdrPaneProcessInfo{}, err
	}
	info := result.ProcessInfo.toSnapshot()
	if strings.TrimSpace(info.PaneID) == "" {
		return multiplexer.HerdrPaneProcessInfo{}, fmt.Errorf("%w: pane.process_info result missing pane_id", multiplexer.ErrHerdrBackendUnavailable)
	}
	return info, nil
}

func decodeOKResult(method string, raw json.RawMessage) error {
	var result struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("%w: invalid %s result: %v", multiplexer.ErrHerdrBackendUnavailable, method, err)
	}
	if result.Type != "ok" {
		return fmt.Errorf("%w: %s result type = %q, want ok", multiplexer.ErrHerdrBackendUnavailable, method, result.Type)
	}
	return nil
}

type rawHerdrWorkspace struct {
	ID          string            `json:"id"`
	HerdrID     string            `json:"workspace_id"`
	Number      *int              `json:"number"`
	Label       string            `json:"label"`
	Focused     *bool             `json:"focused"`
	PaneCount   *int              `json:"pane_count"`
	TabCount    *int              `json:"tab_count"`
	ActiveTabID string            `json:"active_tab_id"`
	AgentStatus string            `json:"agent_status"`
	Metadata    map[string]string `json:"metadata"`
	Tokens      map[string]string `json:"tokens"`
}

func (w rawHerdrWorkspace) validate() error {
	if strings.TrimSpace(firstNonEmpty(w.ID, w.HerdrID)) == "" || w.Number == nil || strings.TrimSpace(w.Label) == "" || w.Focused == nil || w.PaneCount == nil || w.TabCount == nil || strings.TrimSpace(w.ActiveTabID) == "" || strings.TrimSpace(w.AgentStatus) == "" {
		return fmt.Errorf("%w: session.snapshot workspace missing mandatory fields", multiplexer.ErrHerdrBackendUnavailable)
	}
	return nil
}

func (w rawHerdrWorkspace) toSnapshot() multiplexer.HerdrWorkspaceSnapshot {
	return multiplexer.HerdrWorkspaceSnapshot{ID: firstNonEmpty(w.ID, w.HerdrID), Label: w.Label, Metadata: mergeMetadataTokens(w.Metadata, w.Tokens)}
}

type rawHerdrTab struct {
	ID           string            `json:"id"`
	HerdrID      string            `json:"tab_id"`
	WorkspaceID  string            `json:"workspace_id"`
	Workspace    string            `json:"workspaceId"`
	Label        string            `json:"label"`
	Order        int               `json:"order"`
	Number       *int              `json:"number"`
	Focused      *bool             `json:"focused"`
	PaneCount    *int              `json:"pane_count"`
	ActivePaneID string            `json:"active_pane_id"`
	AgentStatus  string            `json:"agent_status"`
	Metadata     map[string]string `json:"metadata"`
	Tokens       map[string]string `json:"tokens"`
}

func (t rawHerdrTab) validate() error {
	if strings.TrimSpace(firstNonEmpty(t.ID, t.HerdrID)) == "" || strings.TrimSpace(firstNonEmpty(t.WorkspaceID, t.Workspace)) == "" || t.Number == nil || strings.TrimSpace(t.Label) == "" || t.Focused == nil || t.PaneCount == nil || strings.TrimSpace(t.AgentStatus) == "" {
		return fmt.Errorf("%w: session.snapshot tab missing mandatory fields", multiplexer.ErrHerdrBackendUnavailable)
	}
	return nil
}

func (t rawHerdrTab) toSnapshot() multiplexer.HerdrTabSnapshot {
	number := 0
	if t.Number != nil {
		number = *t.Number
	}
	return multiplexer.HerdrTabSnapshot{
		ID:          firstNonEmpty(t.ID, t.HerdrID),
		WorkspaceID: firstNonEmpty(t.WorkspaceID, t.Workspace),
		Label:       t.Label,
		Order:       firstNonZero(t.Order, number),
		Metadata:    mergeMetadataTokens(t.Metadata, t.Tokens),
	}
}

type rawHerdrPane struct {
	ID             string              `json:"id"`
	HerdrID        string              `json:"pane_id"`
	TerminalID     string              `json:"terminal_id"`
	WorkspaceID    string              `json:"workspace_id"`
	Workspace      string              `json:"workspaceId"`
	TabID          string              `json:"tab_id"`
	Tab            string              `json:"tabId"`
	Label          string              `json:"label"`
	Order          int                 `json:"order"`
	Number         int                 `json:"number"`
	Focused        *bool               `json:"focused"`
	Revision       *int                `json:"revision"`
	Metadata       map[string]string   `json:"metadata"`
	Tokens         map[string]string   `json:"tokens"`
	Env            map[string]string   `json:"env"`
	ProcessInfo    rawHerdrProcessInfo `json:"process_info"`
	ProcessInfoAlt json.RawMessage     `json:"processInfo"`
	AgentStatus    string              `json:"agent_status"`
	AgentStatusAlt string              `json:"agentStatus"`
	TerminalTitle  string              `json:"terminal_title"`
	TerminalAlt    string              `json:"terminalTitle"`
	ForegroundCWD  string              `json:"foreground_cwd"`
	ForegroundAlt  string              `json:"foregroundCWD"`
	Stale          bool                `json:"stale"`
	StaleReason    string              `json:"stale_reason"`
	StaleAlt       string              `json:"staleReason"`
	PostmanSession string              `json:"postman_session"`
	PostmanSessAlt string              `json:"postmanSession"`
}

func (p rawHerdrPane) validate() error {
	if strings.TrimSpace(firstNonEmpty(p.ID, p.HerdrID)) == "" || strings.TrimSpace(p.TerminalID) == "" || strings.TrimSpace(firstNonEmpty(p.WorkspaceID, p.Workspace)) == "" || strings.TrimSpace(firstNonEmpty(p.TabID, p.Tab)) == "" || p.Focused == nil || strings.TrimSpace(firstNonEmpty(p.AgentStatus, p.AgentStatusAlt)) == "" || p.Revision == nil {
		return fmt.Errorf("%w: session.snapshot pane missing mandatory fields", multiplexer.ErrHerdrBackendUnavailable)
	}
	if len(p.ProcessInfoAlt) > 0 && string(p.ProcessInfoAlt) != "null" {
		return fmt.Errorf("%w: session.snapshot pane contains unsupported processInfo field", multiplexer.ErrHerdrBackendUnavailable)
	}
	if err := p.ProcessInfo.validate(); err != nil {
		return err
	}
	return nil
}

func (p rawHerdrPane) toSnapshot() multiplexer.HerdrPaneSnapshot {
	processInfo := p.ProcessInfo.toSnapshot()
	return multiplexer.HerdrPaneSnapshot{
		ID:             firstNonEmpty(p.ID, p.HerdrID),
		TerminalID:     p.TerminalID,
		WorkspaceID:    firstNonEmpty(p.WorkspaceID, p.Workspace),
		TabID:          firstNonEmpty(p.TabID, p.Tab),
		Label:          p.Label,
		Order:          firstNonZero(p.Order, p.Number),
		Metadata:       mergeMetadataTokens(p.Metadata, p.Tokens),
		Env:            p.Env,
		ProcessInfo:    processInfo,
		AgentStatus:    firstNonEmpty(p.AgentStatus, p.AgentStatusAlt),
		TerminalTitle:  firstNonEmpty(p.TerminalTitle, p.TerminalAlt),
		ForegroundCWD:  firstNonEmpty(p.ForegroundCWD, p.ForegroundAlt),
		Stale:          p.Stale,
		StaleReason:    firstNonEmpty(p.StaleReason, p.StaleAlt),
		PostmanSession: firstNonEmpty(p.PostmanSession, p.PostmanSessAlt),
	}
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
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
	PaneID                 string                       `json:"pane_id"`
	ShellPID               int                          `json:"shell_pid"`
	ShellPIDAlt            json.RawMessage              `json:"shellPID"`
	ForegroundProcessID    int                          `json:"foreground_process_group_id"`
	ForegroundProcessIDAlt json.RawMessage              `json:"foregroundProcessID"`
	ForegroundProcesses    []rawHerdrProcessInfoProcess `json:"foreground_processes"`
	ForegroundProcessesAlt json.RawMessage              `json:"foregroundProcesses"`
}

func (p rawHerdrProcessInfo) validate() error {
	if len(p.ShellPIDAlt) > 0 && string(p.ShellPIDAlt) != "null" {
		return fmt.Errorf("%w: pane.process_info contains unsupported shellPID field", multiplexer.ErrHerdrBackendUnavailable)
	}
	if len(p.ForegroundProcessIDAlt) > 0 && string(p.ForegroundProcessIDAlt) != "null" {
		return fmt.Errorf("%w: pane.process_info contains unsupported foregroundProcessID field", multiplexer.ErrHerdrBackendUnavailable)
	}
	if len(p.ForegroundProcessesAlt) > 0 && string(p.ForegroundProcessesAlt) != "null" {
		return fmt.Errorf("%w: pane.process_info contains unsupported foregroundProcesses field", multiplexer.ErrHerdrBackendUnavailable)
	}
	for _, process := range p.ForegroundProcesses {
		if err := process.validate(); err != nil {
			return err
		}
	}
	return nil
}

type rawHerdrProcessInfoProcess struct {
	PID     *int     `json:"pid"`
	Name    string   `json:"name"`
	Argv    []string `json:"argv"`
	Argv0   string   `json:"argv0"`
	Command string   `json:"cmdline"`
	CWD     string   `json:"cwd"`
}

func (p rawHerdrProcessInfoProcess) validate() error {
	if p.PID == nil || strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%w: pane.process_info foreground process missing mandatory fields", multiplexer.ErrHerdrBackendUnavailable)
	}
	return nil
}

func (p rawHerdrProcessInfoProcess) toSnapshot() multiplexer.HerdrProcessInfo {
	pid := 0
	if p.PID != nil {
		pid = *p.PID
	}
	return multiplexer.HerdrProcessInfo{
		PID:     pid,
		Name:    p.Name,
		Argv:    p.Argv,
		Command: p.Command,
		CWD:     p.CWD,
	}
}

func (p rawHerdrProcessInfo) toSnapshot() multiplexer.HerdrPaneProcessInfo {
	processes := make([]multiplexer.HerdrProcessInfo, 0, len(p.ForegroundProcesses))
	for _, process := range p.ForegroundProcesses {
		processes = append(processes, process.toSnapshot())
	}
	info := multiplexer.HerdrPaneProcessInfo{
		PaneID:              p.PaneID,
		ShellPID:            p.ShellPID,
		ForegroundProcessID: p.ForegroundProcessID,
		ForegroundProcesses: processes,
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
