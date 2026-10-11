package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func validationFields(errs []ValidationError, severity string) []string {
	var fields []string
	for _, e := range errs {
		if e.Severity == severity {
			fields = append(fields, e.Field)
		}
	}
	return fields
}

func containsField(fields []string, want string) bool {
	for _, f := range fields {
		if f == want {
			return true
		}
	}
	return false
}

// A1: a peer declaration is a bare node name. A session-qualified value is a
// load error, and the same bare name across sessions is valid.
func TestValidateConfig_WorkspaceTreeEntryInterfaceNode(t *testing.T) {
	base := func(entries ...WorkspaceTreeNodeConfig) *Config {
		return &Config{
			Nodes:         map[string]NodeConfig{"mouthpiece": {}},
			Edges:         []string{"mouthpiece --- mouthpiece"},
			InterfaceNode: "mouthpiece",
			WorkspaceTree: entries,
		}
	}
	t.Run("same bare name across sessions is valid", func(t *testing.T) {
		errs := ValidateConfig(base(
			WorkspaceTreeNodeConfig{SessionName: "top", InterfaceNode: "mouthpiece"},
			WorkspaceTreeNodeConfig{SessionName: "owner", ParentSessionName: "top", InterfaceNode: "mouthpiece"},
		))
		if got := validationFields(errs, "error"); containsField(got, "workspace_tree[0].interface_node") || containsField(got, "workspace_tree[1].interface_node") {
			t.Fatalf("valid bare declarations reported errors: %v", errs)
		}
	})
	t.Run("qualified peer declaration is an error", func(t *testing.T) {
		errs := ValidateConfig(base(
			WorkspaceTreeNodeConfig{SessionName: "top", InterfaceNode: "mouthpiece"},
			WorkspaceTreeNodeConfig{SessionName: "owner", ParentSessionName: "top", InterfaceNode: "top:mouthpiece"},
		))
		if !containsField(validationFields(errs, "error"), "workspace_tree[1].interface_node") {
			t.Fatalf("qualified interface_node not rejected: %v", errs)
		}
	})
	t.Run("invalid node name is an error", func(t *testing.T) {
		errs := ValidateConfig(base(WorkspaceTreeNodeConfig{SessionName: "top", InterfaceNode: "Bad Name"}))
		if !containsField(validationFields(errs, "error"), "workspace_tree[0].interface_node") {
			t.Fatalf("invalid interface_node not rejected: %v", errs)
		}
	})
}

// A1: the own entry may omit interface_node; if present it must equal the
// global; global empty with an own declaration is an error; a duplicate own
// entry fails closed.
func TestValidateOwnInterfaceDeclaration(t *testing.T) {
	entry := func(node string) []WorkspaceTreeNodeConfig {
		return []WorkspaceTreeNodeConfig{{SessionName: "owner", InterfaceNode: node}}
	}
	cases := []struct {
		name    string
		global  string
		entries []WorkspaceTreeNodeConfig
		wantErr string
	}{
		{name: "omitted own declaration is valid", global: "mouthpiece", entries: entry("")},
		{name: "equal own declaration is valid", global: "mouthpiece", entries: entry("mouthpiece")},
		{name: "own entry absent is valid", global: "mouthpiece", entries: []WorkspaceTreeNodeConfig{{SessionName: "other"}}},
		{name: "different own declaration is an error", global: "mouthpiece", entries: entry("desk"), wantErr: "must be equal"},
		{name: "own declaration without a global is an error", global: "", entries: entry("mouthpiece"), wantErr: "is empty"},
		{
			name: "duplicate own entry is an error", global: "mouthpiece",
			entries: []WorkspaceTreeNodeConfig{{SessionName: "owner"}, {SessionName: "owner"}}, wantErr: "more than one entry",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{InterfaceNode: tc.global, WorkspaceTree: tc.entries}
			err := ValidateOwnInterfaceDeclaration(cfg, "owner")
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateOwnInterfaceDeclaration = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateOwnInterfaceDeclaration = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestDeclaredInterfaceNode(t *testing.T) {
	cfg := &Config{
		InterfaceNode: "mouthpiece",
		WorkspaceTree: []WorkspaceTreeNodeConfig{
			{SessionName: "owner"},
			{SessionName: "top", InterfaceNode: "desk"},
			{SessionName: "bare"},
			{SessionName: "dup", InterfaceNode: "a"},
			{SessionName: "dup", InterfaceNode: "a"},
		},
	}
	if got, ok := cfg.DeclaredInterfaceNode("owner", "owner"); !ok || got != "mouthpiece" {
		t.Fatalf("own session = %q, %v; want the global mouthpiece", got, ok)
	}
	if got, ok := cfg.DeclaredInterfaceNode("top", "owner"); !ok || got != "desk" {
		t.Fatalf("peer = %q, %v; want its own declaration desk", got, ok)
	}
	if got, ok := cfg.DeclaredInterfaceNode("bare", "owner"); ok {
		t.Fatalf("peer without a declaration = %q, true; want none (never inferred)", got)
	}
	if _, ok := cfg.DeclaredInterfaceNode("dup", "owner"); ok {
		t.Fatal("duplicate entry declared; want none")
	}
	if _, ok := cfg.DeclaredInterfaceNode("missing", "owner"); ok {
		t.Fatal("session outside the tree declared; want none")
	}
	// The global designates ONLY the own session, never a peer.
	if got, ok := (&Config{InterfaceNode: "mouthpiece", WorkspaceTree: []WorkspaceTreeNodeConfig{{SessionName: "peer"}}}).DeclaredInterfaceNode("peer", "owner"); ok {
		t.Fatalf("global leaked to a peer: %q", got)
	}
}

// A7: entry-level diplomat_node, ui_node and relay_node warn once each and are
// ignored; no declaration is revived from them, and the per-entry
// interface_node loads.
func TestLoadConfig_LegacyWorkspaceTreeEntryKeysWarnAndGrantNothing(t *testing.T) {
	logs := captureLegacyUINodeLog(t)
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	path := filepath.Join(root, "postman.toml")
	writeFile(t, path, `[postman]
interface_node = "mouthpiece"
edges = ["mouthpiece --- worker"]

[mouthpiece]
role = "interface"

[worker]
role = "worker"

[[postman.workspace_tree]]
session = "top"
interface_node = "mouthpiece"
diplomat_node = "legacy-a"

[[postman.workspace_tree]]
session = "owner"
parent = "top"
ui_node = "legacy-b"
relay_node = "legacy-c"
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.WorkspaceTree) != 2 || cfg.WorkspaceTree[0].InterfaceNode != "mouthpiece" {
		t.Fatalf("WorkspaceTree = %+v, want 2 entries with top declaring mouthpiece", cfg.WorkspaceTree)
	}
	for _, key := range []string{"diplomat_node", "ui_node", "relay_node"} {
		if n := strings.Count(logs.String(), "legacy [[postman.workspace_tree]] key "+key+" is ignored"); n != 1 {
			t.Fatalf("warning for %s logged %d times, want exactly once; logs:\n%s", key, n, logs.String())
		}
	}
	if got, ok := cfg.DeclaredInterfaceNode("owner", "top"); ok {
		t.Fatalf("legacy keys revived a declaration for owner: %q", got)
	}
}

func TestLoadConfig_WorkspaceTreeWithoutLegacyKeysDoesNotWarn(t *testing.T) {
	logs := captureLegacyUINodeLog(t)
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	path := filepath.Join(root, "postman.toml")
	writeFile(t, path, "[postman]\ninterface_node = \"mouthpiece\"\nedges = [\"mouthpiece --- worker\"]\n\n[mouthpiece]\nrole = \"interface\"\n\n[worker]\nrole = \"worker\"\n\n[[postman.workspace_tree]]\nsession = \"top\"\nrepresentative = \"orchestrator\"\ninterface_node = \"mouthpiece\"\n")
	if _, err := LoadConfig(path); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if strings.Contains(logs.String(), "legacy [[postman.workspace_tree]] key") {
		t.Fatalf("unexpected legacy warning:\n%s", logs.String())
	}
}
