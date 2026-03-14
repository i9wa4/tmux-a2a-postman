package config

import (
	"os"
	"strings"
	"testing"
)

func TestMaterializeNodeTemplates(t *testing.T) {
	tmpDir := t.TempDir()
	const nodeName = "worker"
	const nodeTemplate = "# WORKER\n\nYou are the executor."

	cfg := Config{
		TmuxTimeout: 5.0,
		Nodes: map[string]NodeConfig{
			nodeName: {
				Template:            nodeTemplate,
				MaterializeTemplate: boolPtr(true),
			},
			"observer": {
				Template:            "# OBSERVER",
				MaterializeTemplate: boolPtr(false),
			},
		},
	}

	MaterializeNodeTemplates(tmpDir, "test-ctx", &cfg)

	// nodeName must appear in MaterializedPaths
	matPath, ok := cfg.MaterializedPaths[nodeName]
	if !ok {
		t.Fatalf("MaterializedPaths[%q] not set", nodeName)
	}

	// File must exist at that path
	content, err := os.ReadFile(matPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", matPath, err)
	}

	contentStr := string(content)

	// File must start with role template header
	expectedHeader := "<!-- role template: " + nodeName + " -->"
	if !strings.HasPrefix(contentStr, expectedHeader) {
		t.Errorf("file content must start with %q, got: %q", expectedHeader, contentStr)
	}

	// File must contain the original template text
	if !strings.Contains(contentStr, nodeTemplate) {
		t.Errorf("file content must contain original template %q, got: %q", nodeTemplate, contentStr)
	}

	// Node with MaterializeTemplate=false must NOT be in MaterializedPaths
	if _, ok := cfg.MaterializedPaths["observer"]; ok {
		t.Errorf("observer (MaterializeTemplate=false) must not be in MaterializedPaths")
	}
}
