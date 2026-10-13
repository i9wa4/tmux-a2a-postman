package herdrruntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/multiplexer"
)

const herdrBindingCanary = "CANARY-must-not-escape"

func paneReadFixture(id, paneID, workspaceID, tabID, source, text string) string {
	return `{"id":"` + id + `","result":{"type":"pane_read","read":{"pane_id":"` + paneID + `","workspace_id":"` + workspaceID + `","tab_id":"` + tabID + `","source":"` + source + `","format":"text","text":"` + text + `","revision":8,"truncated":false}}}` + "\n"
}

func processInfoFixture(id, paneID string) string {
	return `{"id":"` + id + `","result":{"type":"pane_process_info","process_info":{"pane_id":"` + paneID + `","foreground_processes":[{"pid":123,"name":"` + herdrBindingCanary + `"}]}}}` + "\n"
}

// bindingBackend wires the real socket client into a HerdrBackend configured
// for workspace-1 / tab-1 / pane-1, the pane described by herdr082SnapshotFixture.
func bindingBackend(socketPath string) multiplexer.HerdrBackend {
	return multiplexer.HerdrBackend{
		Config: multiplexer.HerdrReadConfig{
			Enabled: true,
			Runtime: multiplexer.HerdrRuntimeIdentity{
				SocketPath:  "/tmp/herdr.sock",
				SessionName: "work",
				WorkspaceID: "workspace-1",
				TabID:       "tab-1",
				PaneID:      "pane-1",
			},
			Policy: multiplexer.HerdrGatePolicy{
				ReadEnabled:             true,
				ReadScope:               multiplexer.HerdrReadScopePane,
				AllowedSocketPaths:      []string{"/tmp/herdr.sock"},
				AllowedSessions:         []string{"work"},
				AllowedWorkspaceIDs:     []string{"workspace-1"},
				AllowedProtocolVersions: []string{"20"},
				AllowedSchemaVersions:   []int{1},
				InputSanitizerReady:     true,
				ComplianceDecision:      multiplexer.HerdrComplianceDecisionRecorded,
			},
		},
		Client: testSocketClient(socketPath),
	}
}

func bindingPane() multiplexer.ResourceID {
	return multiplexer.HerdrPaneID("pane-1")
}

// snapshotFrames are the two frames the pre-read containment snapshot consumes
// (compatibility ping, then session.snapshot), so the next request is postman:3.
func snapshotFrames() []string {
	return []string{
		herdr082PongFixture,
		`{"id":"postman:2","result":{"type":"session_snapshot","snapshot":` + herdr082SnapshotFixture + `}}` + "\n",
		herdr082PongFixtureWithID("postman:3"),
	}
}

func TestSocketClientReadPaneReturnsEchoedIdentityExactly(t *testing.T) {
	socketPath := testSocketPath(t)
	serveHerdrSocketSequence(t, socketPath,
		herdr082PongFixture,
		paneReadFixture("postman:2", "Pane-1 ", " workspace-1", "tab-1\\t", "Recent", "hello"),
	)

	read, err := testSocketClient(socketPath).ReadPane(context.Background(), "pane-1", multiplexer.HerdrPaneReadOptions{Source: "recent"})
	if err != nil {
		t.Fatalf("ReadPane() error = %v", err)
	}
	if read.PaneID != "Pane-1 " || read.WorkspaceID != " workspace-1" || read.TabID != "tab-1\t" || read.Source != "Recent" || read.Text != "hello" {
		t.Fatalf("ReadPane() = %#v, want identity returned byte for byte without trimming or case folding", read)
	}
}

func TestSocketClientReadPanePassesPresentEmptyIdentityToBackend(t *testing.T) {
	socketPath := testSocketPath(t)
	serveHerdrSocketSequence(t, socketPath,
		herdr082PongFixture,
		paneReadFixture("postman:2", "", "", "", "", "hello"),
	)

	read, err := testSocketClient(socketPath).ReadPane(context.Background(), "pane-1", multiplexer.HerdrPaneReadOptions{Source: "visible"})
	if err != nil {
		t.Fatalf("ReadPane() error = %v, want present-empty identity surfaced for the backend to reject as a mismatch", err)
	}
	if read.PaneID != "" || read.WorkspaceID != "" || read.TabID != "" || read.Source != "" {
		t.Fatalf("ReadPane() = %#v, want empty identity preserved", read)
	}
}

func TestSocketClientReadPaneRejectsMissingOrMalformedIdentityAsUnavailable(t *testing.T) {
	const tail = `"format":"text","text":"x","revision":8,"truncated":false`
	tests := []struct {
		name string
		read string
	}{
		{name: "missing pane", read: `"workspace_id":"workspace-1","tab_id":"tab-1","source":"visible",` + tail},
		{name: "missing workspace", read: `"pane_id":"pane-1","tab_id":"tab-1","source":"visible",` + tail},
		{name: "missing tab", read: `"pane_id":"pane-1","workspace_id":"workspace-1","source":"visible",` + tail},
		{name: "missing source", read: `"pane_id":"pane-1","workspace_id":"workspace-1","tab_id":"tab-1",` + tail},
		{name: "null pane", read: `"pane_id":null,"workspace_id":"workspace-1","tab_id":"tab-1","source":"visible",` + tail},
		{name: "null source", read: `"pane_id":"pane-1","workspace_id":"workspace-1","tab_id":"tab-1","source":null,` + tail},
		{name: "numeric pane", read: `"pane_id":1,"workspace_id":"workspace-1","tab_id":"tab-1","source":"visible",` + tail},
		{name: "object workspace", read: `"pane_id":"pane-1","workspace_id":{},"tab_id":"tab-1","source":"visible",` + tail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			socketPath := testSocketPath(t)
			serveHerdrSocketSequence(t, socketPath,
				herdr082PongFixture,
				`{"id":"postman:2","result":{"type":"pane_read","read":{`+tt.read+`}}}`+"\n",
			)
			_, err := testSocketClient(socketPath).ReadPane(context.Background(), "pane-1", multiplexer.HerdrPaneReadOptions{Source: "visible"})
			if !errors.Is(err, multiplexer.ErrHerdrBackendUnavailable) {
				t.Fatalf("ReadPane() error = %v, want ErrHerdrBackendUnavailable for a malformed response", err)
			}
		})
	}
}

func TestSocketClientPaneProcessInfoReturnsEchoedPaneIDAndRejectsMissing(t *testing.T) {
	t.Run("passes the id through unchanged", func(t *testing.T) {
		socketPath := testSocketPath(t)
		serveHerdrSocketSequence(t, socketPath, herdr082PongFixture, processInfoFixture("postman:2", "Pane-Other "))
		info, err := testSocketClient(socketPath).PaneProcessInfo(context.Background(), "pane-1")
		if err != nil {
			t.Fatalf("PaneProcessInfo() error = %v", err)
		}
		if info.ProcessInfo.PaneID != "Pane-Other " {
			t.Fatalf("PaneID = %q, want the echoed id byte for byte", info.ProcessInfo.PaneID)
		}
	})
	t.Run("present empty id reaches the backend", func(t *testing.T) {
		socketPath := testSocketPath(t)
		serveHerdrSocketSequence(t, socketPath, herdr082PongFixture, processInfoFixture("postman:2", ""))
		info, err := testSocketClient(socketPath).PaneProcessInfo(context.Background(), "pane-1")
		if err != nil {
			t.Fatalf("PaneProcessInfo() error = %v, want present-empty surfaced for the backend", err)
		}
		if info.ProcessInfo.PaneID != "" {
			t.Fatalf("PaneID = %q, want empty preserved", info.ProcessInfo.PaneID)
		}
	})
	for name, body := range map[string]string{
		"missing": `{"foreground_processes":[{"pid":1,"name":"x"}]}`,
		"null":    `{"pane_id":null,"foreground_processes":[{"pid":1,"name":"x"}]}`,
		"numeric": `{"pane_id":7,"foreground_processes":[{"pid":1,"name":"x"}]}`,
	} {
		t.Run(name+" id is malformed", func(t *testing.T) {
			socketPath := testSocketPath(t)
			serveHerdrSocketSequence(t, socketPath, herdr082PongFixture, `{"id":"postman:2","result":{"type":"pane_process_info","process_info":`+body+`}}`+"\n")
			_, err := testSocketClient(socketPath).PaneProcessInfo(context.Background(), "pane-1")
			if !errors.Is(err, multiplexer.ErrHerdrBackendUnavailable) {
				t.Fatalf("PaneProcessInfo() error = %v, want ErrHerdrBackendUnavailable", err)
			}
		})
	}
}

func TestSocketBackendCapturePaneBindsResponseIdentityEndToEnd(t *testing.T) {
	tests := []struct {
		name string
		read string
	}{
		{name: "wrong pane", read: paneReadFixture("postman:4", "pane-9", "workspace-1", "tab-1", "visible", herdrBindingCanary)},
		{name: "wrong workspace", read: paneReadFixture("postman:4", "pane-1", "workspace-9", "tab-1", "visible", herdrBindingCanary)},
		{name: "wrong tab", read: paneReadFixture("postman:4", "pane-1", "workspace-1", "tab-9", "visible", herdrBindingCanary)},
		{name: "wrong source", read: paneReadFixture("postman:4", "pane-1", "workspace-1", "tab-1", "recent", herdrBindingCanary)},
		{name: "empty pane", read: paneReadFixture("postman:4", "", "workspace-1", "tab-1", "visible", herdrBindingCanary)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			socketPath := testSocketPath(t)
			requests := serveHerdrSocketSequence(t, socketPath, append(snapshotFrames(), tt.read)...)

			got, err := bindingBackend(socketPath).CapturePane(context.Background(), bindingPane(), multiplexer.CaptureOptions{})

			if !errors.Is(err, multiplexer.ErrHerdrResponseTargetMismatch) || errors.Is(err, multiplexer.ErrHerdrBackendUnavailable) {
				t.Fatalf("CapturePane() error = %v, want ErrHerdrResponseTargetMismatch", err)
			}
			if got != "" || strings.Contains(err.Error(), herdrBindingCanary) {
				t.Fatalf("CapturePane() = %q, error %v: mismatched content must not escape", got, err)
			}
			// The failure must come from the response: the request went out for the
			// configured pane and the normalized source.
			var readRequest map[string]any
			for i := 0; i < 4; i++ {
				readRequest = <-requests
			}
			assertRequestParams(t, readRequest, "pane.read", map[string]any{"pane_id": "pane-1", "source": "visible"})
		})
	}
}

func TestSocketBackendCapturePaneAcceptsMatchingIdentityEndToEnd(t *testing.T) {
	tests := []struct {
		name   string
		opts   multiplexer.CaptureOptions
		source string
	}{
		{name: "visible", source: "visible"},
		{name: "history", opts: multiplexer.CaptureOptions{History: true}, source: "recent"},
		{name: "tail", opts: multiplexer.CaptureOptions{TailLines: 25}, source: "recent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			socketPath := testSocketPath(t)
			serveHerdrSocketSequence(t, socketPath, append(snapshotFrames(), paneReadFixture("postman:4", "pane-1", "workspace-1", "tab-1", tt.source, "hello"))...)

			got, err := bindingBackend(socketPath).CapturePane(context.Background(), bindingPane(), tt.opts)
			if err != nil {
				t.Fatalf("CapturePane() error = %v, want matching response accepted", err)
			}
			if got != "hello" {
				t.Fatalf("CapturePane() = %q, want hello", got)
			}
		})
	}
}

func TestSocketBackendPaneCurrentCommandBindsProcessIdentityEndToEnd(t *testing.T) {
	t.Run("wrong pane", func(t *testing.T) {
		socketPath := testSocketPath(t)
		serveHerdrSocketSequence(t, socketPath, append(snapshotFrames(), processInfoFixture("postman:4", "pane-9"))...)

		got, err := bindingBackend(socketPath).PaneCurrentCommand(context.Background(), bindingPane())
		if !errors.Is(err, multiplexer.ErrHerdrResponseTargetMismatch) || got != "" || strings.Contains(err.Error(), herdrBindingCanary) {
			t.Fatalf("PaneCurrentCommand() = %q, %v, want mismatch without content", got, err)
		}
	})
	t.Run("empty pane", func(t *testing.T) {
		socketPath := testSocketPath(t)
		serveHerdrSocketSequence(t, socketPath, append(snapshotFrames(), processInfoFixture("postman:4", ""))...)

		got, err := bindingBackend(socketPath).PaneCurrentCommand(context.Background(), bindingPane())
		if !errors.Is(err, multiplexer.ErrHerdrResponseTargetMismatch) || got != "" {
			t.Fatalf("PaneCurrentCommand() = %q, %v, want mismatch for present-empty pane_id", got, err)
		}
	})
	t.Run("matching pane", func(t *testing.T) {
		socketPath := testSocketPath(t)
		serveHerdrSocketSequence(t, socketPath, append(snapshotFrames(), processInfoFixture("postman:4", "pane-1"))...)

		got, err := bindingBackend(socketPath).PaneCurrentCommand(context.Background(), bindingPane())
		if err != nil {
			t.Fatalf("PaneCurrentCommand() error = %v", err)
		}
		if got != herdrBindingCanary {
			t.Fatalf("PaneCurrentCommand() = %q, want the foreground process name", got)
		}
	})
}

func TestSocketBackendDiscoveryPropagatesMismatchedProcessProbeEndToEnd(t *testing.T) {
	for _, tt := range []struct {
		name   string
		paneID string
	}{
		{name: "wrong pane", paneID: "pane-other"},
		{name: "empty pane", paneID: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			socketPath := testSocketPath(t)
			serveHerdrSocketSequence(t, socketPath,
				herdr082PongFixture,
				`{"id":"postman:2","result":{"type":"session_snapshot","snapshot":`+herdr082SnapshotFixture+`}}`+"\n",
				herdr082PongFixtureWithID("postman:3"),
				processInfoFixture("postman:4", tt.paneID),
			)
			backend := bindingBackend(socketPath)
			backend.Config.Policy.ReadScope = multiplexer.HerdrReadScopeDiscovery

			discovery, err := backend.Discover(context.Background(), "work")
			if !errors.Is(err, multiplexer.ErrHerdrResponseTargetMismatch) {
				t.Fatalf("Discover() error = %v, want ErrHerdrResponseTargetMismatch", err)
			}
			if len(discovery.Layout.Groups) != 0 || len(discovery.StalePanes) != 0 {
				t.Fatalf("Discover() = %#v, want no layout and no stale entry on a mismatched probe", discovery)
			}
		})
	}
}
