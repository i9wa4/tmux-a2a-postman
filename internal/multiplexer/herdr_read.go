package multiplexer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

const LayoutGroupKindTab = "tab"

var (
	ErrHerdrReadDisabled        = errors.New("herdr read backend disabled")
	ErrHerdrBackendUnavailable  = errors.New("herdr backend unavailable")
	ErrHerdrReadClientMissing   = errors.New("herdr read client missing")
	ErrHerdrPaneTargetMismatch  = errors.New("herdr pane target mismatch")
	ErrHerdrSessionNameMismatch = errors.New("herdr session name mismatch")
	ErrHerdrSnapshotInvalid     = errors.New("herdr snapshot invalid")
)

type HerdrReadConfig struct {
	Enabled bool
	Runtime HerdrRuntimeIdentity
	Policy  HerdrGatePolicy
}

type HerdrReadClient interface {
	Ping(ctx context.Context) (HerdrResponseEnvelope, error)
	SessionSnapshot(ctx context.Context) (HerdrSessionSnapshot, error)
	ReadPane(ctx context.Context, paneID string, opts HerdrPaneReadOptions) (HerdrPaneReadResult, error)
	PaneProcessInfo(ctx context.Context, paneID string) (HerdrPaneProcessInfoResult, error)
}

type HerdrPaneReadOptions struct {
	Source    string
	TailLines int
}

type HerdrPaneReadResult struct {
	Envelope HerdrResponseEnvelope
	Text     string
}

type HerdrPaneProcessInfoResult struct {
	Envelope    HerdrResponseEnvelope
	ProcessInfo HerdrPaneProcessInfo
}

type HerdrSessionSnapshot struct {
	Envelope           HerdrResponseEnvelope
	FocusedWorkspaceID string
	FocusedTabID       string
	FocusedPaneID      string
	Workspaces         []HerdrWorkspaceSnapshot
	Tabs               []HerdrTabSnapshot
	Panes              []HerdrPaneSnapshot
}

type HerdrWorkspaceSnapshot struct {
	ID       string
	Label    string
	Metadata map[string]string
}

type HerdrTabSnapshot struct {
	ID          string
	WorkspaceID string
	Label       string
	Order       int
	Metadata    map[string]string
}

type HerdrPaneSnapshot struct {
	ID             string
	WorkspaceID    string
	TabID          string
	Label          string
	Order          int
	Metadata       map[string]string
	Env            map[string]string
	ProcessInfo    HerdrPaneProcessInfo
	AgentStatus    string
	TerminalTitle  string
	ForegroundCWD  string
	Stale          bool
	StaleReason    string
	PostmanNode    string
	PostmanSession string
}

type HerdrPaneProcessInfo struct {
	ShellPID            int
	ForegroundProcessID int
	ForegroundProcesses []HerdrProcessInfo
}

type HerdrProcessInfo struct {
	PID     int
	Name    string
	Argv    []string
	Command string
	CWD     string
}

type HerdrIdentityCollision struct {
	SessionName string
	NodeName    string
	PaneIDs     []string
}

type HerdrDiscoveryResult struct {
	Layout                  SessionLayout
	Collisions              []HerdrIdentityCollision
	StalePanes              []ResourceID
	UnsupportedStatusFields []string
}

type HerdrBackend struct {
	Config         HerdrReadConfig
	Client         HerdrReadClient
	InputSanitizer HerdrInputSanitizer
}

func NewHerdrBackend(config HerdrReadConfig, client HerdrReadClient) (HerdrBackend, error) {
	if !config.Enabled {
		return HerdrBackend{}, ErrHerdrReadDisabled
	}
	if client == nil {
		return HerdrBackend{}, ErrHerdrReadClientMissing
	}
	return HerdrBackend{Config: config, Client: client}, nil
}

func (HerdrBackend) Kind() BackendKind {
	return BackendKindHerdr
}

func (b HerdrBackend) SessionLayout(ctx context.Context, sessionName string) (SessionLayout, error) {
	discovery, err := b.Discover(ctx, sessionName)
	if err != nil {
		return SessionLayout{}, err
	}
	return discovery.Layout, nil
}

func (b HerdrBackend) Discover(ctx context.Context, sessionName string) (HerdrDiscoveryResult, error) {
	if err := b.authorizeReadPath(HerdrReadScopeDiscovery); err != nil {
		return HerdrDiscoveryResult{}, err
	}
	if strings.TrimSpace(sessionName) != strings.TrimSpace(b.Config.Runtime.SessionName) {
		return HerdrDiscoveryResult{}, ErrHerdrSessionNameMismatch
	}
	snapshot, err := b.readValidatedSnapshot(ctx, HerdrReadScopeDiscovery)
	if err != nil {
		return HerdrDiscoveryResult{}, err
	}
	return b.discoveryFromSnapshot(sessionName, snapshot)
}

func (b HerdrBackend) CapturePane(ctx context.Context, pane ResourceID, opts CaptureOptions) (string, error) {
	if err := b.authorizeReadPath(HerdrReadScopePane); err != nil {
		return "", err
	}
	if pane.Backend != BackendKindHerdr || pane.Kind != ResourceKindPane {
		return "", fmt.Errorf("herdr capture requires herdr pane resource: %#v", pane)
	}
	if pane.Native != b.Config.Runtime.PaneID {
		return "", ErrHerdrPaneTargetMismatch
	}
	if err := b.validateConfiguredPaneInSnapshot(ctx); err != nil {
		return "", err
	}
	result, err := b.Client.ReadPane(ctx, pane.Native, herdrPaneReadOptions(opts))
	if err != nil {
		return "", NormalizeHerdrBackendError(err)
	}
	if err := b.validateEnvelope(HerdrReadScopePane, result.Envelope); err != nil {
		return "", err
	}
	return result.Text, nil
}

func (b HerdrBackend) PaneCurrentCommand(ctx context.Context, pane ResourceID) (string, error) {
	if err := b.authorizeReadPath(HerdrReadScopePane); err != nil {
		return "", err
	}
	if pane.Backend != BackendKindHerdr || pane.Kind != ResourceKindPane {
		return "", fmt.Errorf("herdr process info requires herdr pane resource: %#v", pane)
	}
	if pane.Native != b.Config.Runtime.PaneID {
		return "", ErrHerdrPaneTargetMismatch
	}
	if err := b.validateConfiguredPaneInSnapshot(ctx); err != nil {
		return "", err
	}
	result, err := b.Client.PaneProcessInfo(ctx, pane.Native)
	if err != nil {
		return "", NormalizeHerdrBackendError(err)
	}
	if err := b.validateEnvelope(HerdrReadScopePane, result.Envelope); err != nil {
		return "", err
	}
	return result.ProcessInfo.CurrentCommand(), nil
}

func (b HerdrBackend) authorizeReadPath(scope HerdrReadScope) error {
	if !b.Config.Enabled {
		return ErrHerdrReadDisabled
	}
	if b.Client == nil {
		return ErrHerdrReadClientMissing
	}
	envelope := b.localReadGateEnvelope()
	return b.validateEnvelope(scope, envelope)
}

func (b HerdrBackend) localReadGateEnvelope() HerdrResponseEnvelope {
	envelope := HerdrResponseEnvelope{}
	if len(b.Config.Policy.AllowedProtocolVersions) > 0 {
		envelope.ProtocolVersion = b.Config.Policy.AllowedProtocolVersions[0]
	}
	if len(b.Config.Policy.AllowedSchemaVersions) > 0 {
		envelope.SchemaVersion = b.Config.Policy.AllowedSchemaVersions[0]
	}
	return envelope
}

func (b HerdrBackend) validateEnvelope(scope HerdrReadScope, envelope HerdrResponseEnvelope) error {
	policy := b.Config.Policy
	policy.ReadScope = scope
	return ValidateHerdrReadGate(policy, b.Config.Runtime, envelope)
}

func (b HerdrBackend) readValidatedSnapshot(ctx context.Context, scope HerdrReadScope) (HerdrSessionSnapshot, error) {
	snapshot, err := b.Client.SessionSnapshot(ctx)
	if err != nil {
		return HerdrSessionSnapshot{}, NormalizeHerdrBackendError(err)
	}
	if err := b.validateEnvelope(scope, snapshot.Envelope); err != nil {
		return HerdrSessionSnapshot{}, err
	}
	if err := b.validateSnapshotWorkspaceRoot(snapshot); err != nil {
		return HerdrSessionSnapshot{}, err
	}
	return snapshot, nil
}

func (b HerdrBackend) validateSnapshotWorkspaceRoot(snapshot HerdrSessionSnapshot) error {
	for _, workspace := range snapshot.Workspaces {
		if workspace.ID == b.Config.Runtime.WorkspaceID {
			return nil
		}
	}
	return fmt.Errorf("%w: configured workspace %q is not in latest snapshot", ErrHerdrSnapshotInvalid, b.Config.Runtime.WorkspaceID)
}

func (b HerdrBackend) validateConfiguredPaneInSnapshot(ctx context.Context) error {
	snapshot, err := b.readValidatedSnapshot(ctx, HerdrReadScopePane)
	if err != nil {
		return err
	}
	return b.validatePaneContainment(snapshot, b.Config.Runtime.TabID, b.Config.Runtime.PaneID)
}

func (b HerdrBackend) validatePaneContainment(snapshot HerdrSessionSnapshot, tabID, paneID string) error {
	tabs := herdrTabsByID(snapshot.Tabs, b.Config.Runtime.WorkspaceID)
	if _, ok := tabs[tabID]; !ok {
		return fmt.Errorf("%w: configured tab %q is not in workspace %q", ErrHerdrSnapshotInvalid, tabID, b.Config.Runtime.WorkspaceID)
	}
	for _, pane := range snapshot.Panes {
		if pane.ID != paneID {
			continue
		}
		if pane.WorkspaceID != b.Config.Runtime.WorkspaceID {
			return fmt.Errorf("%w: pane %q is in workspace %q, want %q", ErrHerdrSnapshotInvalid, pane.ID, pane.WorkspaceID, b.Config.Runtime.WorkspaceID)
		}
		if pane.TabID != tabID {
			return fmt.Errorf("%w: pane %q is in tab %q, want %q", ErrHerdrSnapshotInvalid, pane.ID, pane.TabID, tabID)
		}
		if pane.Stale {
			return fmt.Errorf("%w: pane %q is stale: %s", ErrHerdrSnapshotInvalid, pane.ID, pane.StaleReason)
		}
		return nil
	}
	return fmt.Errorf("%w: pane %q is not in latest snapshot", ErrHerdrSnapshotInvalid, paneID)
}

func (b HerdrBackend) discoveryFromSnapshot(sessionName string, snapshot HerdrSessionSnapshot) (HerdrDiscoveryResult, error) {
	layout := SessionLayout{
		Backend:     BackendKindHerdr,
		SessionName: sessionName,
		NativeIDs: map[string]string{
			"session_name":          sessionName,
			"herdr_session_name":    b.Config.Runtime.SessionName,
			"herdr_workspace_id":    b.Config.Runtime.WorkspaceID,
			"focused_workspace_id":  snapshot.FocusedWorkspaceID,
			"focused_tab_id":        snapshot.FocusedTabID,
			"focused_pane_id":       snapshot.FocusedPaneID,
			"tmux_windows":          "unsupported",
			"postman_address_shape": "session:node",
		},
	}

	tabByID := herdrTabsByID(snapshot.Tabs, b.Config.Runtime.WorkspaceID)
	for _, tab := range tabByID {
		layout.Groups = append(layout.Groups, herdrLayoutGroup(tab))
	}

	groupByTabID := make(map[string]int, len(layout.Groups))
	for i, group := range layout.Groups {
		groupByTabID[group.ID.Native] = i
	}

	nodeClaims := make(map[string][]string)
	var stalePanes []ResourceID
	for _, pane := range snapshot.Panes {
		if pane.WorkspaceID != b.Config.Runtime.WorkspaceID {
			continue
		}
		groupIndex, ok := groupByTabID[pane.TabID]
		if !ok {
			return HerdrDiscoveryResult{}, fmt.Errorf("%w: pane %q references missing tab %q", ErrHerdrSnapshotInvalid, pane.ID, pane.TabID)
		}
		nodeName := herdrPostmanNodeName(pane)
		if nodeName != "" {
			nodeClaims[sessionName+":"+nodeName] = append(nodeClaims[sessionName+":"+nodeName], pane.ID)
		}
		if pane.Stale {
			stalePanes = append(stalePanes, HerdrPaneID(pane.ID))
		}
		layout.Groups[groupIndex].Items = append(layout.Groups[groupIndex].Items, herdrLayoutItem(pane, nodeName))
	}

	for i := range layout.Groups {
		sort.Slice(layout.Groups[i].Items, func(left, right int) bool {
			if layout.Groups[i].Items[left].Order != layout.Groups[i].Items[right].Order {
				return layout.Groups[i].Items[left].Order < layout.Groups[i].Items[right].Order
			}
			return layout.Groups[i].Items[left].ID.Native < layout.Groups[i].Items[right].ID.Native
		})
	}
	sort.Slice(layout.Groups, func(left, right int) bool {
		if layout.Groups[left].Order != layout.Groups[right].Order {
			return layout.Groups[left].Order < layout.Groups[right].Order
		}
		return layout.Groups[left].ID.Native < layout.Groups[right].ID.Native
	})

	return HerdrDiscoveryResult{
		Layout:                  layout,
		Collisions:              herdrIdentityCollisions(nodeClaims),
		StalePanes:              stalePanes,
		UnsupportedStatusFields: []string{"windows"},
	}, nil
}

func herdrTabsByID(tabs []HerdrTabSnapshot, workspaceID string) map[string]HerdrTabSnapshot {
	tabByID := make(map[string]HerdrTabSnapshot, len(tabs))
	for _, tab := range tabs {
		if tab.WorkspaceID != workspaceID {
			continue
		}
		tabByID[tab.ID] = tab
	}
	return tabByID
}

func herdrLayoutGroup(tab HerdrTabSnapshot) LayoutGroup {
	return LayoutGroup{
		Kind:  LayoutGroupKindTab,
		ID:    HerdrTabID(tab.ID),
		Order: tab.Order,
		NativeIDs: map[string]string{
			"workspace_id": tab.WorkspaceID,
			"tab_id":       tab.ID,
			"tab_label":    tab.Label,
		},
	}
}

func herdrLayoutItem(pane HerdrPaneSnapshot, nodeName string) LayoutItem {
	nativeIDs := map[string]string{
		"workspace_id": pane.WorkspaceID,
		"tab_id":       pane.TabID,
		"pane_id":      pane.ID,
		"pane_label":   pane.Label,
	}
	if pane.AgentStatus != "" {
		nativeIDs["agent_status"] = pane.AgentStatus
	}
	if pane.TerminalTitle != "" {
		nativeIDs["terminal_title"] = pane.TerminalTitle
	}
	if pane.ForegroundCWD != "" {
		nativeIDs["foreground_cwd"] = pane.ForegroundCWD
	}
	if pane.Stale {
		nativeIDs["stale"] = "true"
		nativeIDs["stale_reason"] = pane.StaleReason
	}
	return LayoutItem{
		Kind:           LayoutItemKindPane,
		ID:             HerdrPaneID(pane.ID),
		Order:          pane.Order,
		LogicalName:    nodeName,
		CurrentCommand: pane.ProcessInfo.CurrentCommand(),
		NativeIDs:      nativeIDs,
	}
}

func herdrPostmanNodeName(pane HerdrPaneSnapshot) string {
	if pane.PostmanNode != "" {
		return pane.PostmanNode
	}
	for _, key := range []string{"postman.node", "POSTMAN_NODE", "TMUX_A2A_POSTMAN_NODE"} {
		if value := strings.TrimSpace(pane.Metadata[key]); value != "" {
			return value
		}
		if value := strings.TrimSpace(pane.Env[key]); value != "" {
			return value
		}
	}
	return ""
}

func herdrIdentityCollisions(claims map[string][]string) []HerdrIdentityCollision {
	var collisions []HerdrIdentityCollision
	for logicalAddress, paneIDs := range claims {
		if len(paneIDs) < 2 {
			continue
		}
		sort.Strings(paneIDs)
		sessionName, nodeName, _ := strings.Cut(logicalAddress, ":")
		collisions = append(collisions, HerdrIdentityCollision{
			SessionName: sessionName,
			NodeName:    nodeName,
			PaneIDs:     append([]string(nil), paneIDs...),
		})
	}
	sort.Slice(collisions, func(left, right int) bool {
		if collisions[left].SessionName != collisions[right].SessionName {
			return collisions[left].SessionName < collisions[right].SessionName
		}
		return collisions[left].NodeName < collisions[right].NodeName
	})
	return collisions
}

func herdrPaneReadOptions(opts CaptureOptions) HerdrPaneReadOptions {
	result := HerdrPaneReadOptions{Source: "visible"}
	if opts.History {
		result.Source = "recent"
	} else if opts.TailLines > 0 {
		result.Source = "recent"
		result.TailLines = opts.TailLines
	}
	return result
}

func (info HerdrPaneProcessInfo) CurrentCommand() string {
	if len(info.ForegroundProcesses) == 0 {
		return ""
	}
	process := info.ForegroundProcesses[0]
	if process.Command != "" {
		return herdrCommandToken(process.Command)
	}
	if len(process.Argv) > 0 {
		return herdrCommandToken(process.Argv[0])
	}
	return herdrCommandToken(process.Name)
}

func herdrCommandToken(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

func NormalizeHerdrBackendError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrHerdrBackendUnavailable) ||
		errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ENOENT) {
		return fmt.Errorf("%w: %v", ErrHerdrBackendUnavailable, err)
	}
	return err
}
