package config

import (
	"strings"
	"testing"
)

func TestValidateConfig_ValidConfig(t *testing.T) {
	cfg := &Config{
		Edges: []string{
			"worker --- orchestrator",
			"orchestrator --- observer",
		},
		Nodes: map[string]NodeConfig{
			"worker":       {},
			"orchestrator": {},
			"observer":     {},
		},
	}

	errors := ValidateConfig(cfg)
	if len(errors) != 0 {
		t.Errorf("expected no validation errors, got %d: %v", len(errors), errors)
	}
}

func TestValidateConfig_InvalidEdgeNode(t *testing.T) {
	cfg := &Config{
		Edges: []string{
			"worker --- nonexistent",
		},
		Nodes: map[string]NodeConfig{
			"worker": {},
		},
	}

	errors := ValidateConfig(cfg)
	if len(errors) == 0 {
		t.Fatal("expected validation error for nonexistent edge node")
	}

	foundError := false
	for _, err := range errors {
		if err.Severity == "error" && err.Field == "edges[0]" {
			foundError = true
			break
		}
	}
	if !foundError {
		t.Errorf("expected error for edges[0], got: %v", errors)
	}
}

func TestValidateConfig_PostmanInEdges(t *testing.T) {
	cfg := &Config{
		Edges: []string{
			"worker --- postman",
		},
		Nodes: map[string]NodeConfig{
			"worker": {},
		},
	}

	errors := ValidateConfig(cfg)
	// "postman" in edges should be skipped (not an error)
	if len(errors) != 0 {
		t.Errorf("expected no validation errors for postman in edges, got: %v", errors)
	}
}

func TestValidateConfig_ReservedNodeName(t *testing.T) {
	cfg := &Config{
		Nodes: map[string]NodeConfig{
			"postman": {},
			"worker":  {},
		},
	}

	errors := ValidateConfig(cfg)
	if len(errors) == 0 {
		t.Fatal("expected validation error for reserved node name 'postman'")
	}

	foundError := false
	for _, err := range errors {
		if err.Severity == "error" && err.Field == "nodes.postman" {
			foundError = true
			break
		}
	}
	if !foundError {
		t.Errorf("expected error for nodes.postman, got: %v", errors)
	}
}

func TestValidateConfig_ChainEdge(t *testing.T) {
	cfg := &Config{
		Edges: []string{
			"A --- B --- C",
		},
		Nodes: map[string]NodeConfig{
			"A": {},
			"B": {},
			"C": {},
		},
	}

	errors := ValidateConfig(cfg)
	if len(errors) != 0 {
		t.Errorf("expected no validation errors for chain edge, got: %v", errors)
	}
}

func TestValidateConfig_ArrowEdgeRejected(t *testing.T) {
	cfg := &Config{
		Edges: []string{
			"A --> B",
		},
		Nodes: map[string]NodeConfig{
			"A": {},
			"B": {},
		},
	}

	errors := ValidateConfig(cfg)
	if len(errors) == 0 {
		t.Fatal("expected validation error for arrow edge")
	}
	if errors[0].Severity != "error" || errors[0].Field != "edges[0]" {
		t.Fatalf("expected edges[0] error, got: %v", errors)
	}
}

func TestValidateConfig_DoubleDashEdgeRejected(t *testing.T) {
	cfg := &Config{
		Edges: []string{
			"A -- B",
		},
		Nodes: map[string]NodeConfig{
			"A": {},
			"B": {},
		},
	}

	errors := ValidateConfig(cfg)
	if len(errors) == 0 {
		t.Fatal("expected validation error for double-dash edge")
	}
	if errors[0].Severity != "error" || errors[0].Field != "edges[0]" {
		t.Fatalf("expected edges[0] error, got: %v", errors)
	}
}

func TestValidateConfig_EmptyNodes(t *testing.T) {
	cfg := &Config{
		Edges: []string{"A --- B"},
		Nodes: map[string]NodeConfig{},
	}

	errors := ValidateConfig(cfg)
	if len(errors) == 0 {
		t.Fatal("expected validation error for empty nodes")
	}

	foundError := false
	for _, err := range errors {
		if err.Severity == "error" && err.Field == "nodes" {
			foundError = true
			break
		}
	}
	if !foundError {
		t.Errorf("expected error for empty nodes, got: %v", errors)
	}
}

func TestValidateConfig_EmptyEdges(t *testing.T) {
	cfg := &Config{
		Edges: []string{},
		Nodes: map[string]NodeConfig{
			"worker": {},
		},
	}

	errors := ValidateConfig(cfg)
	if len(errors) == 0 {
		t.Fatal("expected validation warning for empty edges")
	}

	foundWarning := false
	for _, err := range errors {
		if err.Severity == "warning" && err.Field == "edges" {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Errorf("expected warning for empty edges, got: %v", errors)
	}
}

func TestValidateConfig_DuplicateEdges(t *testing.T) {
	cfg := &Config{
		Edges: []string{
			"worker --- orchestrator",
			"orchestrator --- worker", // Duplicate (same as above for undirected)
		},
		Nodes: map[string]NodeConfig{
			"worker":       {},
			"orchestrator": {},
		},
	}

	errors := ValidateConfig(cfg)
	if len(errors) == 0 {
		t.Fatal("expected validation warning for duplicate edges")
	}

	foundWarning := false
	for _, err := range errors {
		if err.Severity == "warning" && err.Field == "edges[1]" {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Errorf("expected warning for duplicate edge at edges[1], got: %v", errors)
	}
}

func TestValidateConfig_DuplicateEdges_ReverseUndirectedDuplicate(t *testing.T) {
	cfg := &Config{
		Edges: []string{
			"worker --- orchestrator",
			"orchestrator --- worker",
		},
		Nodes: map[string]NodeConfig{
			"worker":       {},
			"orchestrator": {},
		},
	}

	errors := ValidateConfig(cfg)
	foundWarning := false
	for _, err := range errors {
		if err.Severity == "warning" && strings.Contains(err.Message, "duplicate") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Fatalf("expected duplicate warning for reversed edge, got: %v", errors)
	}
}

func TestValidateConfig_WorkspaceRoots(t *testing.T) {
	cfg := &Config{
		Edges: []string{"worker --- orchestrator"},
		Nodes: map[string]NodeConfig{
			"worker":       {},
			"orchestrator": {},
		},
		WorkspaceRoots: []WorkspaceRootConfig{
			{SessionName: "repo", Label: "repo", Root: "/workspace/repo", RootID: "repo-root"},
			{SessionName: "", Root: "/workspace/repo/apps/api"},
			{SessionName: "project", Label: "bad_label", Root: "/workspace/repo/apps/api"},
			{SessionName: "docs", RootID: "bad_id", Root: ""},
		},
	}

	errors := ValidateConfig(cfg)
	wantFields := map[string]bool{
		"workspace_roots[1].session": false,
		"workspace_roots[2].label":   false,
		"workspace_roots[3].root":    false,
		"workspace_roots[3].id":      false,
	}
	for _, err := range errors {
		if _, ok := wantFields[err.Field]; ok && err.Severity == "error" {
			wantFields[err.Field] = true
		}
	}
	for field, found := range wantFields {
		if !found {
			t.Fatalf("missing workspace root validation error for %s in %#v", field, errors)
		}
	}
}
