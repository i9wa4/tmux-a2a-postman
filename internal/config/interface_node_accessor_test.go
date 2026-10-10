package config

import "testing"

// TestConfiguredInterfaceNode_Whitespace_F002 (#764 F-002): the accessor every
// runtime reader uses trims surrounding whitespace, treats a whitespace-only
// value as no node, and is nil-safe. The explicit-setting flag is independent:
// an explicit whitespace-only value stays explicit but names no node.
func TestConfiguredInterfaceNode_Whitespace_F002(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
		want string
	}{
		{name: "nil config", cfg: nil, want: ""},
		{name: "unset", cfg: &Config{}, want: ""},
		{name: "plain", cfg: &Config{InterfaceNode: "messenger"}, want: "messenger"},
		{name: "surrounding spaces", cfg: &Config{InterfaceNode: " messenger "}, want: "messenger"},
		{name: "surrounding tab and newline", cfg: &Config{InterfaceNode: "\tmessenger\n"}, want: "messenger"},
		{name: "whitespace only", cfg: &Config{InterfaceNode: "   "}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.ConfiguredInterfaceNode(); got != tt.want {
				t.Fatalf("ConfiguredInterfaceNode() = %q, want %q", got, tt.want)
			}
			wantDiagnostic := tt.want == ""
			if got := tt.cfg.InterfaceNodeDiagnostic() != ""; got != wantDiagnostic {
				t.Fatalf("InterfaceNodeDiagnostic() non-empty = %v, want %v", got, wantDiagnostic)
			}
		})
	}
}

// TestLoadMarkdownConfig_WhitespaceInterfaceNodeStaysExplicit_F002: a
// whitespace-only frontmatter interface_node stays an explicit setting (it
// still masks a lower-precedence value) but names no node.
func TestLoadMarkdownConfig_WhitespaceInterfaceNodeStaysExplicit_F002(t *testing.T) {
	path := t.TempDir() + "/postman.md"
	writeFile(t, path, "---\ninterface_node:    \n---\n"+legacyUINodeKindsBody)

	cfg, err := loadMarkdownConfig(path)
	if err != nil {
		t.Fatalf("loadMarkdownConfig: %v", err)
	}
	if !cfg.HasExplicitInterfaceNodeSetting() {
		t.Fatal("HasExplicitInterfaceNodeSetting() = false, want true for an explicit empty value")
	}
	if got := cfg.ConfiguredInterfaceNode(); got != "" {
		t.Fatalf("ConfiguredInterfaceNode() = %q, want empty", got)
	}
}
