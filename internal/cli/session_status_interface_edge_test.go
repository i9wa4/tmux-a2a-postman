package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspacetree"
)

func ifaceEdgeStatusConfig() *config.Config {
	return &config.Config{
		InterfaceNode: "mouthpiece",
		WorkspaceTree: []config.WorkspaceTreeNodeConfig{
			{SessionName: "top", InterfaceNode: "mouthpiece"},
			{SessionName: "owner", ParentSessionName: "top", InterfaceNode: "mouthpiece"},
			{SessionName: "repo", ParentSessionName: "owner", InterfaceNode: "mouthpiece"},
		},
	}
}

func ifaceEdgeStatusView(owned func(string) bool) workspacetree.LiveView {
	return workspacetree.LiveView{
		OwnSession:     "owner",
		NodeExists:     func(string) bool { return true },
		SessionEnabled: func(string) bool { return true },
		SessionOwned:   owned,
	}
}

// A6: the status schema is v6 and exposes declared and active interface edges
// from the shared evaluator; A-B-C declares A-B and B-C, never A-C; the active
// list is the live subset.
func TestBuildInterfaceEdgeStatus_DeclaredAndActive(t *testing.T) {
	if status.SchemaVersion != 6 {
		t.Fatalf("status.SchemaVersion = %d, want 6 (the single #700 bump)", status.SchemaVersion)
	}
	declared, active := buildInterfaceEdgeStatus(ifaceEdgeStatusConfig(), ifaceEdgeStatusView(func(s string) bool { return s != "repo" }))
	wantDeclared := []status.InterfaceEdgeStatus{
		{A: "owner:mouthpiece", B: "repo:mouthpiece"},
		{A: "top:mouthpiece", B: "owner:mouthpiece"},
	}
	wantActive := []status.InterfaceEdgeStatus{{A: "top:mouthpiece", B: "owner:mouthpiece"}}
	if !reflect.DeepEqual(declared, wantDeclared) {
		t.Fatalf("declared = %+v, want %+v", declared, wantDeclared)
	}
	if !reflect.DeepEqual(active, wantActive) {
		t.Fatalf("active = %+v, want %+v", active, wantActive)
	}
}

func TestBuildInterfaceEdgeStatus_NoInterfaceNodeOrTreeIsEmpty(t *testing.T) {
	cfg := ifaceEdgeStatusConfig()
	cfg.InterfaceNode = ""
	if d, a := buildInterfaceEdgeStatus(cfg, ifaceEdgeStatusView(func(string) bool { return true })); d != nil || a != nil {
		t.Fatalf("no interface_node: declared=%v active=%v, want both nil", d, a)
	}
	cfg = ifaceEdgeStatusConfig()
	cfg.WorkspaceTree = nil
	if d, a := buildInterfaceEdgeStatus(cfg, ifaceEdgeStatusView(func(string) bool { return true })); d != nil || a != nil {
		t.Fatalf("no workspace_tree: declared=%v active=%v, want both nil", d, a)
	}
}

// F-700-R2-4: the v6 interface_edges field is always present: an empty active
// set serializes as [] (never null, never omitted), including a zero-value
// SessionStatus, so consumers can rely on the key.
func TestSessionStatusJSON_EmptyInterfaceEdgesSerializeAsEmptyArray(t *testing.T) {
	view := ifaceEdgeStatusView(func(string) bool { return false })
	declared, active := buildInterfaceEdgeStatus(ifaceEdgeStatusConfig(), view)
	if len(declared) == 0 || len(active) != 0 {
		t.Fatalf("setup: declared=%v active=%v, want declared non-empty and active empty", declared, active)
	}
	for name, st := range map[string]status.SessionStatus{
		"active empty":     {SchemaVersion: status.SchemaVersion, InterfaceEdges: active, DeclaredInterfaceEdges: declared},
		"zero value":       {},
		"nil from builder": {InterfaceEdges: nil},
	} {
		raw, err := json.Marshal(st)
		if err != nil {
			t.Fatalf("%s: Marshal: %v", name, err)
		}
		if !strings.Contains(string(raw), `"interface_edges":[]`) {
			t.Fatalf("%s: status JSON lacks \"interface_edges\":[]: %s", name, raw)
		}
		if strings.Contains(string(raw), `"interface_edges":null`) {
			t.Fatalf("%s: interface_edges serialized as null: %s", name, raw)
		}
	}
}

// F-700-R2-1/R2-3: the status active set honors the same collision and enabled
// inputs as the send path: a collided node or a not-enabled session is declared
// but never active.
func TestBuildInterfaceEdgeStatus_CollisionAndEnabledViews(t *testing.T) {
	cfg := ifaceEdgeStatusConfig()
	base := func() workspacetree.LiveView {
		return workspacetree.LiveView{
			OwnSession:     "owner",
			NodeExists:     func(string) bool { return true },
			SessionEnabled: func(string) bool { return true },
			SessionOwned:   func(string) bool { return true },
		}
	}

	control := base()
	if _, active := buildInterfaceEdgeStatus(cfg, control); len(active) != 2 {
		t.Fatalf("control: active = %+v, want both edges", active)
	}

	collided := base()
	collided.Ambiguous = func(address string) bool { return address == "top:mouthpiece" }
	declared, active := buildInterfaceEdgeStatus(cfg, collided)
	wantActive := []status.InterfaceEdgeStatus{{A: "owner:mouthpiece", B: "repo:mouthpiece"}}
	if !reflect.DeepEqual(active, wantActive) {
		t.Fatalf("collision: active = %+v, want %+v", active, wantActive)
	}
	if len(declared) != 2 {
		t.Fatalf("collision: declared = %+v, want both edges still declared", declared)
	}

	disabled := base()
	disabled.SessionEnabled = func(session string) bool { return session != "repo" }
	if _, active := buildInterfaceEdgeStatus(cfg, disabled); !reflect.DeepEqual(active, []status.InterfaceEdgeStatus{{A: "top:mouthpiece", B: "owner:mouthpiece"}}) {
		t.Fatalf("not enabled: active = %+v, want only the top-owner edge", active)
	}
}

// A6: JSON field names are the contract; there is no static diplomat_edges,
// and no filesystem path appears in the edge output.
func TestSessionStatusJSON_InterfaceEdgeFieldsAndNoDiplomatEdges(t *testing.T) {
	declared, active := buildInterfaceEdgeStatus(ifaceEdgeStatusConfig(), ifaceEdgeStatusView(func(string) bool { return true }))
	raw, err := json.Marshal(status.SessionStatus{SchemaVersion: status.SchemaVersion, InterfaceEdges: active, DeclaredInterfaceEdges: declared})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	text := string(raw)
	for _, want := range []string{`"interface_edges":`, `"declared_interface_edges":`, `"schema_version":6`} {
		if !strings.Contains(text, want) {
			t.Fatalf("status JSON lacks %s: %s", want, text)
		}
	}
	for _, forbidden := range []string{"diplomat_edges", "/Users/", "/tmp/"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("status JSON contains %q: %s", forbidden, text)
		}
	}
}
