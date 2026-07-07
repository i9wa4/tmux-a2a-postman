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

func TestValidateConfig_WorkspaceTree(t *testing.T) {
	cfg := &Config{
		Edges: []string{"worker --- orchestrator"},
		Nodes: map[string]NodeConfig{
			"worker":       {},
			"orchestrator": {},
		},
		WorkspaceTree: []WorkspaceTreeNodeConfig{
			{SessionName: "repo", Label: "repo", ID: "repo-root"},
			{SessionName: "", ParentSessionName: "repo"},
			{SessionName: "project", Label: "bad_label", ParentSessionName: "repo"},
			{SessionName: "docs", ID: "bad_id", ParentSessionName: "bad/parent", Representative: "bad/representative"},
		},
	}

	errors := ValidateConfig(cfg)
	wantFields := map[string]bool{
		"workspace_tree[1].session":        false,
		"workspace_tree[2].label":          false,
		"workspace_tree[3].parent":         false,
		"workspace_tree[3].id":             false,
		"workspace_tree[3].representative": false,
	}
	for _, err := range errors {
		if _, ok := wantFields[err.Field]; ok && err.Severity == "error" {
			wantFields[err.Field] = true
		}
	}
	for field, found := range wantFields {
		if !found {
			t.Fatalf("missing workspace tree validation error for %s in %#v", field, errors)
		}
	}
}

// TestValidateConfig_ReviewerNodeUnresolvable guards #626's decided
// requirement 2 (foot-gun mitigation): a configured-but-unresolvable
// reviewer_node must produce a load-time WARNING, never an error — the
// unified fail-open rule means command approval still runs, but the
// misconfiguration must be loud and auditable.
func TestValidateConfig_ReviewerNodeUnresolvable(t *testing.T) {
	cfg := &Config{
		Edges: []string{"worker --- orchestrator"},
		Nodes: map[string]NodeConfig{
			"worker":       {},
			"orchestrator": {},
		},
		ReviewerNode: "typo-reviewer",
		CommandApproval: []CommandApprovalPolicy{
			{Requester: "worker", Label: "deploy", ReviewerNode: "another-typo"},
		},
	}

	errors := ValidateConfig(cfg)
	wantFields := map[string]bool{
		"reviewer_node":                     false,
		"command_approval[0].reviewer_node": false,
	}
	for _, err := range errors {
		if _, ok := wantFields[err.Field]; ok {
			if err.Severity != "warning" {
				t.Fatalf("field %s severity = %q, want warning (fail-open, never error)", err.Field, err.Severity)
			}
			wantFields[err.Field] = true
		}
	}
	for field, found := range wantFields {
		if !found {
			t.Fatalf("missing reviewer_node validation warning for %s in %#v", field, errors)
		}
	}
}

// TestValidateConfig_ReviewerNodeResolvable guards against a false-positive
// warning when reviewer_node correctly names a configured node.
func TestValidateConfig_ReviewerNodeResolvable(t *testing.T) {
	cfg := &Config{
		Edges: []string{"worker --- orchestrator"},
		Nodes: map[string]NodeConfig{
			"worker":       {},
			"orchestrator": {},
		},
		ReviewerNode: "orchestrator",
	}

	for _, err := range ValidateConfig(cfg) {
		if err.Field == "reviewer_node" {
			t.Fatalf("unexpected reviewer_node validation error for a resolvable name: %#v", err)
		}
	}
}

func TestResolveReviewerNode(t *testing.T) {
	cfg := &Config{
		ReviewerNode: "orchestrator",
		Nodes: map[string]NodeConfig{
			"orchestrator": {},
		},
	}

	if name, valid := cfg.ResolveReviewerNode(""); name != "orchestrator" || !valid {
		t.Fatalf("ResolveReviewerNode(\"\") = (%q, %v), want (orchestrator, true)", name, valid)
	}
	if name, valid := cfg.ResolveReviewerNode("worker"); name != "worker" || valid {
		t.Fatalf("ResolveReviewerNode(\"worker\") = (%q, %v), want (worker, false) — override names an unconfigured node", name, valid)
	}
	if name, valid := (&Config{}).ResolveReviewerNode(""); name != "" || valid {
		t.Fatalf("ResolveReviewerNode on an unconfigured Config = (%q, %v), want (\"\", false)", name, valid)
	}
	if name, valid := (*Config)(nil).ResolveReviewerNode("orchestrator"); name != "" || valid {
		t.Fatalf("ResolveReviewerNode on a nil Config = (%q, %v), want (\"\", false)", name, valid)
	}
}
