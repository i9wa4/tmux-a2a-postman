package config

import (
	"bytes"
	"log"
	"path/filepath"
	"strings"
	"testing"
)

// legacyUINodeKindsBody is a postman.md body with a topology and a common
// template, so a test can prove the rest of the overlay survives a legacy
// ui_node key.
const legacyUINodeKindsBody = "\n## `edges`\n\n```mermaid\ngraph TD\n    messenger --- worker\n```\n\n## `common_template`\n\nshared template text\n"

// legacyUINodeKindCases lists the legacy ui_node frontmatter value kinds. The
// legacy key is ignored with a warning whatever its YAML kind (#764 F-001).
var legacyUINodeKindCases = []struct {
	name string
	yaml string
}{
	{name: "scalar", yaml: "ui_node: messenger"},
	{name: "flow sequence", yaml: "ui_node: [messenger]"},
	{name: "block sequence", yaml: "ui_node:\n  - messenger\n  - worker"},
	{name: "flow mapping", yaml: "ui_node: {node: messenger}"},
	{name: "block mapping", yaml: "ui_node:\n  node: messenger"},
	{name: "explicit null", yaml: "ui_node: null"},
	{name: "tilde null", yaml: "ui_node: ~"},
	{name: "empty value", yaml: "ui_node:"},
}

func captureLegacyUINodeLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	oldWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldWriter) })
	return &logs
}

// TestLoadMarkdownConfig_LegacyUINodeAnyYAMLKindIsIgnored_F001 (#764 F-001):
// a legacy ui_node frontmatter key of any YAML kind (scalar, sequence, mapping,
// null) warns with the file and form and is ignored. It must not fail the load
// and so must not cost the overlay its interface_node, edges or template.
func TestLoadMarkdownConfig_LegacyUINodeAnyYAMLKindIsIgnored_F001(t *testing.T) {
	for _, tt := range legacyUINodeKindCases {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLegacyUINodeLog(t)
			path := filepath.Join(t.TempDir(), "postman.md")
			writeFile(t, path, "---\n"+tt.yaml+"\ninterface_node: worker\n---\n"+legacyUINodeKindsBody)

			cfg, err := loadMarkdownConfig(path)
			if err != nil {
				t.Fatalf("loadMarkdownConfig with legacy ui_node (%s) returned an error: %v", tt.name, err)
			}
			if cfg.InterfaceNode != "worker" {
				t.Fatalf("InterfaceNode = %q, want worker (legacy ui_node must not mask interface_node)", cfg.InterfaceNode)
			}
			if !cfg.HasExplicitInterfaceNodeSetting() {
				t.Fatal("HasExplicitInterfaceNodeSetting() = false, want true")
			}
			if len(cfg.Edges) != 1 || cfg.Edges[0] != "messenger --- worker" {
				t.Fatalf("Edges = %v, want [messenger --- worker] (overlay edges must be retained)", cfg.Edges)
			}
			assertContains(t, cfg.CommonTemplate, "shared template text")
			assertContains(t, logs.String(), "legacy postman.md frontmatter key ui_node is ignored")
			assertContains(t, logs.String(), path)
		})
	}
}

// TestLoadConfig_LegacyUINodeAnyYAMLKindKeepsOverlay_F001 (#764 F-001): the
// same through LoadConfig. Before the fix a non-scalar legacy ui_node made
// loadMarkdownConfig fail, and LoadConfig dropped the whole postman.md overlay
// with a warning.
func TestLoadConfig_LegacyUINodeAnyYAMLKindKeepsOverlay_F001(t *testing.T) {
	for _, tt := range legacyUINodeKindCases {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLegacyUINodeLog(t)
			root := t.TempDir()
			t.Chdir(root)
			t.Setenv("HOME", filepath.Join(root, "home"))
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
			path := filepath.Join(root, "xdg", "tmux-a2a-postman", "postman.md")
			writeFile(t, path, "---\n"+tt.yaml+"\ninterface_node: worker\n---\n"+legacyUINodeKindsBody)

			cfg, err := LoadConfig("")
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if strings.Contains(logs.String(), "skipping") {
				t.Fatalf("overlay was skipped:\n%s", logs.String())
			}
			if cfg.InterfaceNode != "worker" {
				t.Fatalf("InterfaceNode = %q, want worker", cfg.InterfaceNode)
			}
			foundEdge := false
			for _, edge := range cfg.Edges {
				if edge == "messenger --- worker" {
					foundEdge = true
				}
			}
			if !foundEdge {
				t.Fatalf("Edges = %v, want to contain messenger --- worker", cfg.Edges)
			}
			assertContains(t, cfg.CommonTemplate, "shared template text")
			assertContains(t, logs.String(), "legacy postman.md frontmatter key ui_node is ignored")
		})
	}
}

// TestParsePostmanFrontmatter_LegacyUINodeNeverErrors_F001 pins the parser level
// directly: the legacy key is recorded as present and never errors, while
// interface_node and reply_command keep their scalar-only rule.
func TestParsePostmanFrontmatter_LegacyUINodeNeverErrors_F001(t *testing.T) {
	for _, tt := range legacyUINodeKindCases {
		t.Run(tt.name, func(t *testing.T) {
			scalars, _, _, _, err := parsePostmanFrontmatter("---\n" + tt.yaml + "\n---\nbody")
			if err != nil {
				t.Fatalf("parsePostmanFrontmatter: %v", err)
			}
			if _, ok := scalars["ui_node"]; !ok {
				t.Fatal("ui_node presence was not recorded, so the loader cannot warn")
			}
		})
	}

	if _, _, _, _, err := parsePostmanFrontmatter("---\ninterface_node: [worker]\n---\nbody"); err == nil {
		t.Fatal("non-scalar interface_node must still be rejected")
	}
}
