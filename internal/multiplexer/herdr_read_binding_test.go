package multiplexer

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const herdrBindingCanary = "CANARY-must-not-escape"

func herdrBindingPane(config HerdrReadConfig) ResourceID {
	return ResourceID{Backend: BackendKindHerdr, Kind: ResourceKindPane, Native: config.Runtime.PaneID}
}

// herdrMatchingRead is a pane.read response that echoes exactly the configured
// identity and the given source, carrying the canary as its text.
func herdrMatchingRead(config HerdrReadConfig, source string) HerdrPaneReadResult {
	return HerdrPaneReadResult{
		Envelope:    validHerdrEnvelope(),
		Text:        herdrBindingCanary,
		PaneID:      config.Runtime.PaneID,
		WorkspaceID: config.Runtime.WorkspaceID,
		TabID:       config.Runtime.TabID,
		Source:      source,
	}
}

func assertHerdrResponseMismatch(t *testing.T, got string, err error) {
	t.Helper()
	if !errors.Is(err, ErrHerdrResponseTargetMismatch) {
		t.Fatalf("error = %v, want ErrHerdrResponseTargetMismatch", err)
	}
	if errors.Is(err, ErrHerdrBackendUnavailable) {
		t.Fatalf("error = %v, must not be ErrHerdrBackendUnavailable", err)
	}
	if got != "" {
		t.Fatalf("returned %q, want empty on response mismatch", got)
	}
	if strings.Contains(err.Error(), herdrBindingCanary) {
		t.Fatalf("error text leaks mismatched response content: %v", err)
	}
}

func TestHerdrBackendCapturePaneRejectsMismatchedReadIdentity(t *testing.T) {
	config := validHerdrReadConfig()
	tests := []struct {
		name       string
		opts       CaptureOptions
		wantSource string
		mutate     func(*HerdrPaneReadResult)
	}{
		{name: "wrong pane", wantSource: "visible", mutate: func(r *HerdrPaneReadResult) { r.PaneID = "workspace-1:pane-9" }},
		{name: "wrong workspace", wantSource: "visible", mutate: func(r *HerdrPaneReadResult) { r.WorkspaceID = "workspace-2" }},
		{name: "wrong tab", wantSource: "visible", mutate: func(r *HerdrPaneReadResult) { r.TabID = "workspace-1:tab-9" }},
		{name: "wrong source for default visible", wantSource: "visible", mutate: func(r *HerdrPaneReadResult) { r.Source = "recent" }},
		{name: "wrong source for history", opts: CaptureOptions{History: true}, wantSource: "recent", mutate: func(r *HerdrPaneReadResult) { r.Source = "visible" }},
		{name: "wrong source for tail", opts: CaptureOptions{TailLines: 50}, wantSource: "recent", mutate: func(r *HerdrPaneReadResult) { r.Source = "visible" }},
		{name: "empty pane", wantSource: "visible", mutate: func(r *HerdrPaneReadResult) { r.PaneID = "" }},
		{name: "whitespace pane", wantSource: "visible", mutate: func(r *HerdrPaneReadResult) { r.PaneID = "   " }},
		{name: "empty workspace", wantSource: "visible", mutate: func(r *HerdrPaneReadResult) { r.WorkspaceID = "" }},
		{name: "empty tab", wantSource: "visible", mutate: func(r *HerdrPaneReadResult) { r.TabID = "" }},
		{name: "empty source", wantSource: "visible", mutate: func(r *HerdrPaneReadResult) { r.Source = "" }},
		{name: "pane case variant", wantSource: "visible", mutate: func(r *HerdrPaneReadResult) { r.PaneID = "WORKSPACE-1:PANE-1" }},
		{name: "pane trailing whitespace", wantSource: "visible", mutate: func(r *HerdrPaneReadResult) { r.PaneID = config.Runtime.PaneID + " " }},
		{name: "workspace leading whitespace", wantSource: "visible", mutate: func(r *HerdrPaneReadResult) { r.WorkspaceID = " " + config.Runtime.WorkspaceID }},
		{name: "source case variant", wantSource: "visible", mutate: func(r *HerdrPaneReadResult) { r.Source = "Visible" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			read := herdrMatchingRead(config, tt.wantSource)
			tt.mutate(&read)
			client := &fakeHerdrReadClient{snapshot: validHerdrSessionSnapshot(), readPane: read}
			backend := HerdrBackend{Config: config, Client: client}

			got, err := backend.CapturePane(context.Background(), herdrBindingPane(config), tt.opts)

			// The failure must come from the response, not from the pre-read checks.
			if client.readPaneCalls != 1 || client.readPaneID != config.Runtime.PaneID || client.readOptions.Source != tt.wantSource {
				t.Fatalf("client read = calls:%d pane:%q source:%q, want one read of the configured pane with source %q", client.readPaneCalls, client.readPaneID, client.readOptions.Source, tt.wantSource)
			}
			assertHerdrResponseMismatch(t, got, err)
		})
	}
}

func TestHerdrBackendCapturePaneAcceptsMatchingReadIdentity(t *testing.T) {
	config := validHerdrReadConfig()
	tests := []struct {
		name       string
		opts       CaptureOptions
		wantSource string
	}{
		{name: "visible", wantSource: "visible"},
		{name: "history", opts: CaptureOptions{History: true}, wantSource: "recent"},
		{name: "tail", opts: CaptureOptions{TailLines: 25}, wantSource: "recent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeHerdrReadClient{snapshot: validHerdrSessionSnapshot(), readPane: herdrMatchingRead(config, tt.wantSource)}
			backend := HerdrBackend{Config: config, Client: client}

			got, err := backend.CapturePane(context.Background(), herdrBindingPane(config), tt.opts)
			if err != nil {
				t.Fatalf("CapturePane() error = %v, want matching response accepted", err)
			}
			if got != herdrBindingCanary {
				t.Fatalf("CapturePane() = %q, want response text", got)
			}
		})
	}
}

func TestHerdrBackendPaneCurrentCommandRejectsMismatchedProcessIdentity(t *testing.T) {
	config := validHerdrReadConfig()
	for _, tt := range []struct {
		name   string
		paneID string
	}{
		{name: "wrong pane", paneID: "workspace-1:pane-9"},
		{name: "empty pane", paneID: ""},
		{name: "whitespace pane", paneID: " "},
		{name: "case variant", paneID: "WORKSPACE-1:PANE-1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeHerdrReadClient{
				snapshot: validHerdrSessionSnapshot(),
				processInfoByPane: map[string]HerdrPaneProcessInfoResult{
					config.Runtime.PaneID: {
						Envelope: validHerdrEnvelope(),
						ProcessInfo: HerdrPaneProcessInfo{
							PaneID:              tt.paneID,
							ForegroundProcesses: []HerdrProcessInfo{{Name: herdrBindingCanary}},
						},
					},
				},
			}
			backend := HerdrBackend{Config: config, Client: client}

			got, err := backend.PaneCurrentCommand(context.Background(), herdrBindingPane(config))

			if client.processInfoCalls != 1 || client.processInfoPane != config.Runtime.PaneID {
				t.Fatalf("process info calls = %d pane %q, want one request for the configured pane", client.processInfoCalls, client.processInfoPane)
			}
			assertHerdrResponseMismatch(t, got, err)
		})
	}
}

func TestHerdrBackendDiscoveryPropagatesMismatchedProcessProbe(t *testing.T) {
	for _, tt := range []struct {
		name   string
		paneID string
	}{
		{name: "wrong pane", paneID: "other-pane"},
		{name: "empty pane", paneID: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeHerdrReadClient{
				ping:     validHerdrEnvelope(),
				snapshot: validHerdrSessionSnapshot(),
				processInfoByPane: map[string]HerdrPaneProcessInfoResult{
					"workspace-1:pane-1": {
						Envelope:    validHerdrEnvelope(),
						ProcessInfo: HerdrPaneProcessInfo{PaneID: tt.paneID, ForegroundProcesses: []HerdrProcessInfo{{Name: herdrBindingCanary}}},
					},
				},
			}
			backend := HerdrBackend{Config: validHerdrReadConfig(), Client: client}

			discovery, err := backend.Discover(context.Background(), "work")

			if !errors.Is(err, ErrHerdrResponseTargetMismatch) {
				t.Fatalf("Discover() error = %v, want ErrHerdrResponseTargetMismatch", err)
			}
			if len(discovery.Layout.Groups) != 0 || len(discovery.StalePanes) != 0 {
				t.Fatalf("Discover() = %#v, want no layout and no stale classification on a mismatched probe", discovery)
			}
			if strings.Contains(err.Error(), herdrBindingCanary) {
				t.Fatalf("error text leaks mismatched response content: %v", err)
			}
		})
	}
}

func TestHerdrResponseTargetMismatchErrorBoundsAndEscapesIdentities(t *testing.T) {
	hostile := "line1\nline2\x00\x1b[31m" + strings.Repeat("x", 500)
	err := herdrResponseTargetMismatch("pane.read", "pane_id", "workspace-1:pane-1", hostile)
	if !errors.Is(err, ErrHerdrResponseTargetMismatch) {
		t.Fatalf("error = %v, want ErrHerdrResponseTargetMismatch", err)
	}
	text := err.Error()
	if strings.ContainsAny(text, "\n\x00\x1b") {
		t.Fatalf("error text contains raw control bytes: %q", text)
	}
	if len(text) > 400 {
		t.Fatalf("error text length = %d, want bounded", len(text))
	}
	if !strings.Contains(text, `"workspace-1:pane-1"`) {
		t.Fatalf("error text = %q, want the requested identity quoted", text)
	}
}

func TestNormalizeHerdrBackendErrorPreservesResponseTargetMismatch(t *testing.T) {
	err := herdrResponseTargetMismatch("pane.read", "pane_id", "a", "b")
	if got := NormalizeHerdrBackendError(err); !errors.Is(got, ErrHerdrResponseTargetMismatch) || errors.Is(got, ErrHerdrBackendUnavailable) {
		t.Fatalf("NormalizeHerdrBackendError() = %v, want mismatch preserved and not unavailable", got)
	}
}
