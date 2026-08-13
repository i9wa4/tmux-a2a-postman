package multiplexer

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
)

func TestNewHerdrBackendDisabledByDefault(t *testing.T) {
	_, err := NewHerdrBackend(HerdrReadConfig{}, &fakeHerdrReadClient{})
	if !errors.Is(err, ErrHerdrReadDisabled) {
		t.Fatalf("NewHerdrBackend() error = %v, want ErrHerdrReadDisabled", err)
	}
}

func TestHerdrBackendDiscoveryRequiresReadGateBeforeSnapshot(t *testing.T) {
	client := &fakeHerdrReadClient{
		ping: validHerdrEnvelope(),
	}
	config := validHerdrReadConfig()
	config.Policy.ReadEnabled = false
	backend := HerdrBackend{Config: config, Client: client}

	_, err := backend.Discover(context.Background(), config.Runtime.SessionName)
	assertHerdrGateError(t, err, HerdrAccessPhaseRead, "read_enabled", HerdrGateFailureClosed)
	if client.pingCalls != 0 {
		t.Fatalf("pingCalls = %d, want 0 before read gate passes", client.pingCalls)
	}
	if client.snapshotCalls != 0 {
		t.Fatalf("snapshotCalls = %d, want 0 before read gate passes", client.snapshotCalls)
	}
}

func TestHerdrBackendDiscoveryRequiresAllowlistBeforeClientCall(t *testing.T) {
	client := &fakeHerdrReadClient{
		ping: validHerdrEnvelope(),
	}
	config := validHerdrReadConfig()
	config.Policy.AllowedWorkspaceIDs = []string{"workspace-other"}
	backend := HerdrBackend{Config: config, Client: client}

	_, err := backend.Discover(context.Background(), config.Runtime.SessionName)
	assertHerdrGateError(t, err, HerdrAccessPhaseRead, "workspace_id", HerdrGateFailureNotAllowlisted)
	if client.pingCalls != 0 || client.snapshotCalls != 0 {
		t.Fatalf("client calls = ping:%d snapshot:%d, want none", client.pingCalls, client.snapshotCalls)
	}
}

func TestHerdrBackendReadGatePrecedesSessionMismatch(t *testing.T) {
	client := &fakeHerdrReadClient{}
	config := validHerdrReadConfig()
	config.Policy.ReadEnabled = false
	backend := HerdrBackend{Config: config, Client: client}

	_, err := backend.Discover(context.Background(), "other")
	assertHerdrGateError(t, err, HerdrAccessPhaseRead, "read_enabled", HerdrGateFailureClosed)
}

func TestHerdrBackendDiscoveryProjectsReadOnlyLayout(t *testing.T) {
	client := &fakeHerdrReadClient{
		ping:     validHerdrEnvelope(),
		snapshot: validHerdrSessionSnapshot(),
	}
	config := validHerdrReadConfig()
	backend := HerdrBackend{Config: config, Client: client}

	discovery, err := backend.Discover(context.Background(), config.Runtime.SessionName)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if discovery.Layout.Backend != BackendKindHerdr {
		t.Fatalf("Layout.Backend = %q, want herdr", discovery.Layout.Backend)
	}
	if discovery.Layout.NativeIDs["tmux_windows"] != "unsupported" {
		t.Fatalf("tmux_windows marker = %q, want unsupported", discovery.Layout.NativeIDs["tmux_windows"])
	}
	if !reflect.DeepEqual(discovery.UnsupportedStatusFields, []string{"windows"}) {
		t.Fatalf("UnsupportedStatusFields = %#v, want windows", discovery.UnsupportedStatusFields)
	}
	if len(discovery.Layout.Groups) != 1 {
		t.Fatalf("len(Groups) = %d, want 1", len(discovery.Layout.Groups))
	}
	group := discovery.Layout.Groups[0]
	if group.Kind != LayoutGroupKindTab || group.ID != HerdrTabID("workspace-1:tab-1") {
		t.Fatalf("group = %#v, want Herdr tab group", group)
	}
	if len(group.Items) != 1 {
		t.Fatalf("len(group.Items) = %d, want 1 authoritative item", len(group.Items))
	}
	if group.Items[0].LogicalName != "worker" || group.Items[0].ID != HerdrPaneID("workspace-1:pane-1") {
		t.Fatalf("first item = %#v, want worker pane", group.Items[0])
	}
	if group.Items[0].CurrentCommand != "codex" {
		t.Fatalf("CurrentCommand = %q, want codex", group.Items[0].CurrentCommand)
	}
	if len(discovery.Collisions) != 0 {
		t.Fatalf("Collisions = %#v, want no stale-pane collision", discovery.Collisions)
	}
	if len(discovery.StalePanes) != 1 || discovery.StalePanes[0] != HerdrPaneID("workspace-1:pane-2") {
		t.Fatalf("StalePanes = %#v, want pane-2", discovery.StalePanes)
	}
}

func TestHerdrBackendDiscoveryRejectsUnsupportedSnapshotEnvelope(t *testing.T) {
	client := &fakeHerdrReadClient{
		ping: validHerdrEnvelope(),
		snapshot: HerdrSessionSnapshot{
			Envelope: HerdrResponseEnvelope{ProtocolVersion: "99", SchemaVersion: 1},
		},
	}
	config := validHerdrReadConfig()
	backend := HerdrBackend{Config: config, Client: client}

	_, err := backend.Discover(context.Background(), config.Runtime.SessionName)
	assertHerdrGateError(t, err, HerdrAccessPhaseRead, "protocol_version", HerdrGateFailureUnsupportedProtocol)
}

func TestHerdrBackendPublicAPIsFailClosedOnUnavailableClient(t *testing.T) {
	config := validHerdrReadConfig()
	client := &fakeHerdrReadClient{
		snapshotErr: net.ErrClosed,
	}
	backend := HerdrBackend{Config: config, Client: client}

	if _, err := backend.Discover(context.Background(), config.Runtime.SessionName); !errors.Is(err, ErrHerdrBackendUnavailable) {
		t.Fatalf("Discover() error = %v, want ErrHerdrBackendUnavailable", err)
	}
	client.snapshotErr = nil
	client.snapshot = validHerdrSessionSnapshot()
	client.readPaneErr = net.ErrClosed
	if _, err := backend.CapturePane(context.Background(), ResourceID{Backend: BackendKindHerdr, Kind: ResourceKindPane, Native: config.Runtime.PaneID}, CaptureOptions{}); !errors.Is(err, ErrHerdrBackendUnavailable) {
		t.Fatalf("CapturePane() error = %v, want ErrHerdrBackendUnavailable", err)
	}
	if client.readPaneCalls != 1 {
		t.Fatalf("readPaneCalls = %d, want 1", client.readPaneCalls)
	}
	client.readPaneErr = nil
	client.processInfoErr = net.ErrClosed
	if _, err := backend.PaneCurrentCommand(context.Background(), ResourceID{Backend: BackendKindHerdr, Kind: ResourceKindPane, Native: config.Runtime.PaneID}); !errors.Is(err, ErrHerdrBackendUnavailable) {
		t.Fatalf("PaneCurrentCommand() error = %v, want ErrHerdrBackendUnavailable", err)
	}
	if client.processInfoCalls != 1 {
		t.Fatalf("processInfoCalls = %d, want 1", client.processInfoCalls)
	}
}

func TestHerdrBackendPaneAPIsRejectUnsupportedEnvelopesBeforeReturningData(t *testing.T) {
	snapshot := validHerdrSessionSnapshot()
	unsupported := HerdrResponseEnvelope{ProtocolVersion: "99", SchemaVersion: 1}
	client := &fakeHerdrReadClient{
		snapshot:    snapshot,
		readPane:    HerdrPaneReadResult{Envelope: unsupported, Text: "secret\n"},
		processInfo: HerdrPaneProcessInfoResult{Envelope: unsupported, ProcessInfo: HerdrPaneProcessInfo{ForegroundProcesses: []HerdrProcessInfo{{Name: "codex"}}}},
	}
	config := validHerdrReadConfig()
	backend := HerdrBackend{Config: config, Client: client}
	pane := ResourceID{Backend: BackendKindHerdr, Kind: ResourceKindPane, Native: config.Runtime.PaneID}

	if _, err := backend.CapturePane(context.Background(), pane, CaptureOptions{}); err == nil {
		t.Fatal("CapturePane() error = nil, want unsupported envelope rejection")
	} else {
		assertHerdrGateError(t, err, HerdrAccessPhaseRead, "protocol_version", HerdrGateFailureUnsupportedProtocol)
	}
	if _, err := backend.PaneCurrentCommand(context.Background(), pane); err == nil {
		t.Fatal("PaneCurrentCommand() error = nil, want unsupported envelope rejection")
	} else {
		assertHerdrGateError(t, err, HerdrAccessPhaseRead, "protocol_version", HerdrGateFailureUnsupportedProtocol)
	}
}

func TestHerdrBackendPaneAPIsQuarantineStaleConfiguredPane(t *testing.T) {
	config := validHerdrReadConfig()
	config.Runtime.PaneID = "workspace-1:pane-2"
	client := &fakeHerdrReadClient{snapshot: validHerdrSessionSnapshot()}
	backend := HerdrBackend{Config: config, Client: client}
	pane := ResourceID{Backend: BackendKindHerdr, Kind: ResourceKindPane, Native: config.Runtime.PaneID}

	if _, err := backend.CapturePane(context.Background(), pane, CaptureOptions{}); !errors.Is(err, ErrHerdrSnapshotInvalid) {
		t.Fatalf("CapturePane() error = %v, want stale configured-pane rejection", err)
	}
	if _, err := backend.PaneCurrentCommand(context.Background(), pane); !errors.Is(err, ErrHerdrSnapshotInvalid) {
		t.Fatalf("PaneCurrentCommand() error = %v, want stale configured-pane rejection", err)
	}
	if client.readPaneCalls != 0 || client.processInfoCalls != 0 {
		t.Fatalf("downstream calls = read:%d process:%d, want zero", client.readPaneCalls, client.processInfoCalls)
	}
}

func TestHerdrBackendDiscoveryOmitsUnprovenFocusedIDs(t *testing.T) {
	snapshot := validHerdrSessionSnapshot()
	snapshot.FocusedWorkspaceID = "workspace-foreign"
	snapshot.FocusedTabID = "workspace-foreign:tab-1"
	snapshot.FocusedPaneID = "workspace-foreign:pane-1"
	client := &fakeHerdrReadClient{ping: validHerdrEnvelope(), snapshot: snapshot}
	backend := HerdrBackend{Config: validHerdrReadConfig(), Client: client}
	discovery, err := backend.Discover(context.Background(), "work")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if got := discovery.Layout.NativeIDs["focused_workspace_id"]; got != "" {
		t.Fatalf("focused workspace = %q, want omitted", got)
	}
	if got := discovery.Layout.NativeIDs["focused_tab_id"]; got != "" {
		t.Fatalf("focused tab = %q, want omitted", got)
	}
	if got := discovery.Layout.NativeIDs["focused_pane_id"]; got != "" {
		t.Fatalf("focused pane = %q, want omitted", got)
	}
}

func TestHerdrBackendDiscoveryOmitsStaleFocusedPane(t *testing.T) {
	snapshot := validHerdrSessionSnapshot()
	snapshot.FocusedPaneID = "workspace-1:pane-2"
	client := &fakeHerdrReadClient{ping: validHerdrEnvelope(), snapshot: snapshot}
	backend := HerdrBackend{Config: validHerdrReadConfig(), Client: client}
	discovery, err := backend.Discover(context.Background(), "work")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if got := discovery.Layout.NativeIDs["focused_pane_id"]; got != "" {
		t.Fatalf("focused pane = %q, want omitted", got)
	}
}

func TestHerdrBackendDiscoveryRejectsPaneWithMissingTab(t *testing.T) {
	snapshot := validHerdrSessionSnapshot()
	snapshot.Panes[0].TabID = "workspace-1:missing-tab"
	client := &fakeHerdrReadClient{
		ping:     validHerdrEnvelope(),
		snapshot: snapshot,
	}
	config := validHerdrReadConfig()
	backend := HerdrBackend{Config: config, Client: client}

	_, err := backend.Discover(context.Background(), config.Runtime.SessionName)
	if !errors.Is(err, ErrHerdrSnapshotInvalid) {
		t.Fatalf("Discover() error = %v, want ErrHerdrSnapshotInvalid", err)
	}
}

func TestHerdrBackendDiscoveryQuarantinesStalePaneWithMissingTab(t *testing.T) {
	snapshot := validHerdrSessionSnapshot()
	snapshot.Panes[1].TabID = "workspace-1:missing-tab"
	client := &fakeHerdrReadClient{ping: validHerdrEnvelope(), snapshot: snapshot}
	backend := HerdrBackend{Config: validHerdrReadConfig(), Client: client}
	discovery, err := backend.Discover(context.Background(), "work")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(discovery.StalePanes) != 1 || discovery.StalePanes[0] != HerdrPaneID("workspace-1:pane-2") {
		t.Fatalf("StalePanes = %#v, want quarantined stale pane", discovery.StalePanes)
	}
}

func TestHerdrBackendDiscoveryRejectsMissingWorkspaceRoot(t *testing.T) {
	snapshot := validHerdrSessionSnapshot()
	snapshot.Workspaces = nil
	client := &fakeHerdrReadClient{
		snapshot: snapshot,
	}
	config := validHerdrReadConfig()
	backend := HerdrBackend{Config: config, Client: client}

	_, err := backend.Discover(context.Background(), config.Runtime.SessionName)
	if !errors.Is(err, ErrHerdrSnapshotInvalid) {
		t.Fatalf("Discover() error = %v, want ErrHerdrSnapshotInvalid", err)
	}
}

func TestHerdrBackendCapturePaneRequiresPaneReadGate(t *testing.T) {
	client := &fakeHerdrReadClient{
		ping:     validHerdrEnvelope(),
		snapshot: validHerdrSessionSnapshot(),
		readPane: HerdrPaneReadResult{
			Envelope: validHerdrEnvelope(),
			Text:     "hello\n",
		},
	}
	config := validHerdrReadConfig()
	backend := HerdrBackend{Config: config, Client: client}

	got, err := backend.CapturePane(context.Background(), HerdrPaneID("workspace-1:pane-1"), CaptureOptions{TailLines: 50})
	if err != nil {
		t.Fatalf("CapturePane() error = %v", err)
	}
	if got != "hello\n" {
		t.Fatalf("CapturePane() = %q, want hello", got)
	}
	if client.readPaneID != "workspace-1:pane-1" {
		t.Fatalf("readPaneID = %q, want pane", client.readPaneID)
	}
	if client.readOptions.Source != "recent" || client.readOptions.TailLines != 50 {
		t.Fatalf("readOptions = %#v, want recent tail 50", client.readOptions)
	}
}

func TestHerdrBackendCapturePaneRequiresConfiguredPaneTarget(t *testing.T) {
	client := &fakeHerdrReadClient{ping: validHerdrEnvelope()}
	config := validHerdrReadConfig()
	backend := HerdrBackend{Config: config, Client: client}

	_, err := backend.CapturePane(context.Background(), HerdrPaneID("workspace-1:pane-2"), CaptureOptions{})
	if !errors.Is(err, ErrHerdrPaneTargetMismatch) {
		t.Fatalf("CapturePane() error = %v, want ErrHerdrPaneTargetMismatch", err)
	}
	if client.readPaneCalls != 0 {
		t.Fatalf("readPaneCalls = %d, want 0", client.readPaneCalls)
	}
}

func TestHerdrBackendCapturePaneReadGatePrecedesTargetMismatch(t *testing.T) {
	client := &fakeHerdrReadClient{}
	config := validHerdrReadConfig()
	config.Policy.ReadEnabled = false
	backend := HerdrBackend{Config: config, Client: client}

	_, err := backend.CapturePane(context.Background(), HerdrPaneID("workspace-1:pane-2"), CaptureOptions{})
	assertHerdrGateError(t, err, HerdrAccessPhaseRead, "read_enabled", HerdrGateFailureClosed)
	if client.snapshotCalls != 0 || client.readPaneCalls != 0 {
		t.Fatalf("client calls = snapshot:%d read:%d, want none", client.snapshotCalls, client.readPaneCalls)
	}
}

func TestHerdrBackendCapturePaneRequiresSnapshotContainment(t *testing.T) {
	snapshot := validHerdrSessionSnapshot()
	snapshot.Panes[0].TabID = "workspace-1:other-tab"
	client := &fakeHerdrReadClient{
		snapshot: snapshot,
	}
	config := validHerdrReadConfig()
	backend := HerdrBackend{Config: config, Client: client}

	_, err := backend.CapturePane(context.Background(), HerdrPaneID("workspace-1:pane-1"), CaptureOptions{})
	if !errors.Is(err, ErrHerdrSnapshotInvalid) {
		t.Fatalf("CapturePane() error = %v, want ErrHerdrSnapshotInvalid", err)
	}
	if client.readPaneCalls != 0 {
		t.Fatalf("readPaneCalls = %d, want 0 before snapshot containment passes", client.readPaneCalls)
	}
}

func TestHerdrBackendCapturePaneRequiresWorkspaceRoot(t *testing.T) {
	snapshot := validHerdrSessionSnapshot()
	snapshot.Workspaces = []HerdrWorkspaceSnapshot{{
		ID:    "workspace-other",
		Label: "other",
	}}
	client := &fakeHerdrReadClient{
		snapshot: snapshot,
	}
	config := validHerdrReadConfig()
	backend := HerdrBackend{Config: config, Client: client}

	_, err := backend.CapturePane(context.Background(), HerdrPaneID("workspace-1:pane-1"), CaptureOptions{})
	if !errors.Is(err, ErrHerdrSnapshotInvalid) {
		t.Fatalf("CapturePane() error = %v, want ErrHerdrSnapshotInvalid", err)
	}
	if client.readPaneCalls != 0 {
		t.Fatalf("readPaneCalls = %d, want 0 before workspace root passes", client.readPaneCalls)
	}
}

func TestHerdrBackendPaneCurrentCommandReadGatePrecedesTargetMismatch(t *testing.T) {
	client := &fakeHerdrReadClient{}
	config := validHerdrReadConfig()
	config.Policy.ReadEnabled = false
	backend := HerdrBackend{Config: config, Client: client}

	_, err := backend.PaneCurrentCommand(context.Background(), HerdrPaneID("workspace-1:pane-2"))
	assertHerdrGateError(t, err, HerdrAccessPhaseRead, "read_enabled", HerdrGateFailureClosed)
	if client.snapshotCalls != 0 || client.processInfoCalls != 0 {
		t.Fatalf("client calls = snapshot:%d process_info:%d, want none", client.snapshotCalls, client.processInfoCalls)
	}
}

func TestHerdrBackendPaneCurrentCommandRequiresSnapshotContainment(t *testing.T) {
	snapshot := validHerdrSessionSnapshot()
	snapshot.Panes[0].WorkspaceID = "workspace-2"
	client := &fakeHerdrReadClient{
		snapshot: snapshot,
	}
	config := validHerdrReadConfig()
	backend := HerdrBackend{Config: config, Client: client}

	_, err := backend.PaneCurrentCommand(context.Background(), HerdrPaneID("workspace-1:pane-1"))
	if !errors.Is(err, ErrHerdrSnapshotInvalid) {
		t.Fatalf("PaneCurrentCommand() error = %v, want ErrHerdrSnapshotInvalid", err)
	}
	if client.processInfoCalls != 0 {
		t.Fatalf("processInfoCalls = %d, want 0 before snapshot containment passes", client.processInfoCalls)
	}
}

func TestHerdrBackendPaneCurrentCommandRequiresWorkspaceRoot(t *testing.T) {
	snapshot := validHerdrSessionSnapshot()
	snapshot.Workspaces = nil
	client := &fakeHerdrReadClient{
		snapshot: snapshot,
	}
	config := validHerdrReadConfig()
	backend := HerdrBackend{Config: config, Client: client}

	_, err := backend.PaneCurrentCommand(context.Background(), HerdrPaneID("workspace-1:pane-1"))
	if !errors.Is(err, ErrHerdrSnapshotInvalid) {
		t.Fatalf("PaneCurrentCommand() error = %v, want ErrHerdrSnapshotInvalid", err)
	}
	if client.processInfoCalls != 0 {
		t.Fatalf("processInfoCalls = %d, want 0 before workspace root passes", client.processInfoCalls)
	}
}

func TestHerdrBackendPaneCurrentCommandReadsProcessInfo(t *testing.T) {
	client := &fakeHerdrReadClient{
		ping:     validHerdrEnvelope(),
		snapshot: validHerdrSessionSnapshot(),
		processInfo: HerdrPaneProcessInfoResult{
			Envelope: validHerdrEnvelope(),
			ProcessInfo: HerdrPaneProcessInfo{
				ForegroundProcesses: []HerdrProcessInfo{{
					Argv: []string{"codex", "--yolo"},
					Name: "codex",
				}},
			},
		},
	}
	config := validHerdrReadConfig()
	backend := HerdrBackend{Config: config, Client: client}

	got, err := backend.PaneCurrentCommand(context.Background(), HerdrPaneID("workspace-1:pane-1"))
	if err != nil {
		t.Fatalf("PaneCurrentCommand() error = %v", err)
	}
	if got != "codex" {
		t.Fatalf("PaneCurrentCommand() = %q, want codex", got)
	}
}

func TestHerdrPaneProcessInfoCurrentCommandNormalizesExecutableToken(t *testing.T) {
	tests := []struct {
		name string
		info HerdrPaneProcessInfo
		want string
	}{
		{
			name: "command with args",
			info: HerdrPaneProcessInfo{ForegroundProcesses: []HerdrProcessInfo{{
				Command: "/usr/bin/zsh -l",
				Argv:    []string{"ignored"},
				Name:    "ignored",
			}}},
			want: "zsh",
		},
		{
			name: "argv path",
			info: HerdrPaneProcessInfo{ForegroundProcesses: []HerdrProcessInfo{{
				Argv: []string{"/nix/store/hash/bin/codex", "--yolo"},
				Name: "ignored",
			}}},
			want: "codex",
		},
		{
			name: "name path",
			info: HerdrPaneProcessInfo{ForegroundProcesses: []HerdrProcessInfo{{
				Name: "/bin/bash",
			}}},
			want: "bash",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.info.CurrentCommand(); got != tt.want {
				t.Fatalf("CurrentCommand() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeHerdrBackendErrorUnavailable(t *testing.T) {
	err := NormalizeHerdrBackendError(net.ErrClosed)
	if !errors.Is(err, ErrHerdrBackendUnavailable) {
		t.Fatalf("NormalizeHerdrBackendError() = %v, want ErrHerdrBackendUnavailable", err)
	}
}

type fakeHerdrReadClient struct {
	ping             HerdrResponseEnvelope
	pingErr          error
	snapshot         HerdrSessionSnapshot
	snapshotErr      error
	readPane         HerdrPaneReadResult
	readPaneErr      error
	processInfo      HerdrPaneProcessInfoResult
	processInfoErr   error
	pingCalls        int
	snapshotCalls    int
	readPaneCalls    int
	processInfoCalls int
	readPaneID       string
	readOptions      HerdrPaneReadOptions
	processInfoPane  string
}

func (f *fakeHerdrReadClient) Ping(context.Context) (HerdrResponseEnvelope, error) {
	f.pingCalls++
	if f.pingErr != nil {
		return HerdrResponseEnvelope{}, f.pingErr
	}
	return f.ping, nil
}

func (f *fakeHerdrReadClient) SessionSnapshot(context.Context) (HerdrSessionSnapshot, error) {
	f.snapshotCalls++
	if f.snapshotErr != nil {
		return HerdrSessionSnapshot{}, f.snapshotErr
	}
	return f.snapshot, nil
}

func (f *fakeHerdrReadClient) ReadPane(_ context.Context, paneID string, opts HerdrPaneReadOptions) (HerdrPaneReadResult, error) {
	f.readPaneCalls++
	f.readPaneID = paneID
	f.readOptions = opts
	if f.readPaneErr != nil {
		return HerdrPaneReadResult{}, f.readPaneErr
	}
	return f.readPane, nil
}

func (f *fakeHerdrReadClient) PaneProcessInfo(_ context.Context, paneID string) (HerdrPaneProcessInfoResult, error) {
	f.processInfoCalls++
	f.processInfoPane = paneID
	if f.processInfoErr != nil {
		return HerdrPaneProcessInfoResult{}, f.processInfoErr
	}
	return f.processInfo, nil
}

func validHerdrReadConfig() HerdrReadConfig {
	return HerdrReadConfig{
		Enabled: true,
		Runtime: HerdrRuntimeIdentity{
			SocketPath:  "/tmp/herdr.sock",
			SessionName: "work",
			WorkspaceID: "workspace-1",
			TabID:       "workspace-1:tab-1",
			PaneID:      "workspace-1:pane-1",
		},
		Policy: validHerdrGatePolicy(),
	}
}

func validHerdrSessionSnapshot() HerdrSessionSnapshot {
	return HerdrSessionSnapshot{
		Envelope:           validHerdrEnvelope(),
		FocusedWorkspaceID: "workspace-1",
		FocusedTabID:       "workspace-1:tab-1",
		FocusedPaneID:      "workspace-1:pane-1",
		Workspaces: []HerdrWorkspaceSnapshot{{
			ID:    "workspace-1",
			Label: "work",
		}},
		Tabs: []HerdrTabSnapshot{{
			ID:          "workspace-1:tab-1",
			WorkspaceID: "workspace-1",
			Label:       "main",
			Order:       0,
		}},
		Panes: []HerdrPaneSnapshot{
			{
				ID:          "workspace-1:pane-1",
				WorkspaceID: "workspace-1",
				TabID:       "workspace-1:tab-1",
				Label:       "advisory-label",
				Order:       0,
				Metadata: map[string]string{
					"postman.node": "worker",
				},
				ProcessInfo: HerdrPaneProcessInfo{
					ForegroundProcesses: []HerdrProcessInfo{{Name: "codex"}},
				},
			},
			{
				ID:          "workspace-1:pane-2",
				WorkspaceID: "workspace-1",
				TabID:       "workspace-1:tab-1",
				Order:       1,
				Env: map[string]string{
					"POSTMAN_NODE": "worker",
				},
				Stale:       true,
				StaleReason: "pane id no longer appears in latest snapshot",
			},
			{
				ID:          "workspace-2:pane-1",
				WorkspaceID: "workspace-2",
				TabID:       "workspace-2:tab-1",
				Metadata: map[string]string{
					"postman.node": "foreign",
				},
			},
		},
	}
}
