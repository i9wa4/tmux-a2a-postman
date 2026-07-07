package config

import (
	"fmt"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
)

// ValidationError represents a configuration validation issue (Issue #70).
type ValidationError struct {
	Field    string // e.g., "edges[0]", "nodes.postman", "nodes.worker.observes[0]"
	Message  string // Human-readable description
	Severity string // "error" or "warning"
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("[%s] %s: %s", e.Severity, e.Field, e.Message)
}

// ValidateConfig validates configuration and returns validation errors (Issue #70).
func ValidateConfig(cfg *Config) []ValidationError {
	var errors []ValidationError

	// Rule 0a: Empty nodes check (severity: error)
	if len(cfg.Nodes) == 0 {
		errors = append(errors, ValidationError{
			Field:    "nodes",
			Message:  "no nodes defined in configuration",
			Severity: "error",
		})
	}

	// Rule 0b: Empty edges check (severity: warning)
	if len(cfg.Edges) == 0 {
		errors = append(errors, ValidationError{
			Field:    "edges",
			Message:  "no edges defined in configuration (nodes will not be able to communicate)",
			Severity: "warning",
		})
	}

	// Rule 1: edges node reference check (severity: error)
	// IMPORTANT: "postman" is a reserved name and should be skipped (not an error)
	for i, edge := range cfg.Edges {
		nodeNames := splitEdgeNodeNames(edge)
		if strings.TrimSpace(edge) != "" && len(nodeNames) < 2 {
			errors = append(errors, ValidationError{
				Field:    fmt.Sprintf("edges[%d]", i),
				Message:  fmt.Sprintf("invalid edge format %q (use \"node-a --- node-b\")", edge),
				Severity: "error",
			})
			continue
		}
		for _, nodeName := range nodeNames {
			if strings.HasPrefix(nodeName, "-") {
				errors = append(errors, ValidationError{
					Field:    fmt.Sprintf("edges[%d]", i),
					Message:  fmt.Sprintf("invalid edge format %q (use \"node-a --- node-b\")", edge),
					Severity: "error",
				})
				continue
			}
			// Skip "postman" (reserved system name)
			if nodeName == "postman" {
				continue
			}
			if _, exists := cfg.Nodes[nodeName]; !exists {
				errors = append(errors, ValidationError{
					Field:    fmt.Sprintf("edges[%d]", i),
					Message:  fmt.Sprintf("node %q not found in nodes configuration", nodeName),
					Severity: "error",
				})
			}
		}
	}

	// Rule 2: Reserved section name check (severity: error)
	reservedNames := []string{"postman"}
	for nodeName := range cfg.Nodes {
		for _, reserved := range reservedNames {
			if nodeName == reserved {
				errors = append(errors, ValidationError{
					Field:    fmt.Sprintf("nodes.%s", nodeName),
					Message:  fmt.Sprintf("node name %q is reserved and cannot be used", nodeName),
					Severity: "error",
				})
			}
		}
	}

	// Rule 3: Duplicate edges check (severity: warning)
	edgeMap := make(map[string]int)
	for i, edge := range cfg.Edges {
		normalizedEdge := normalizeEdge(edge)
		if firstIdx, exists := edgeMap[normalizedEdge]; exists {
			errors = append(errors, ValidationError{
				Field:    fmt.Sprintf("edges[%d]", i),
				Message:  fmt.Sprintf("duplicate edge (first occurrence at edges[%d])", firstIdx),
				Severity: "warning",
			})
		} else {
			edgeMap[normalizedEdge] = i
		}
	}

	// Rule 4: Deprecated fields (none currently, placeholder for future)
	// Add deprecated field checks here as needed
	for i, node := range cfg.WorkspaceTree {
		field := fmt.Sprintf("workspace_tree[%d]", i)
		if strings.TrimSpace(node.SessionName) == "" {
			errors = append(errors, ValidationError{
				Field:    field + ".session",
				Message:  "session is required",
				Severity: "error",
			})
		} else if _, err := ValidateSessionName(node.SessionName); err != nil {
			errors = append(errors, ValidationError{
				Field:    field + ".session",
				Message:  err.Error(),
				Severity: "error",
			})
		}
		if strings.TrimSpace(node.ParentSessionName) != "" {
			if _, err := ValidateSessionName(node.ParentSessionName); err != nil {
				errors = append(errors, ValidationError{
					Field:    field + ".parent",
					Message:  err.Error(),
					Severity: "error",
				})
			}
		}
		if node.Label != "" && !binding.ValidateNodeName(node.Label) {
			errors = append(errors, ValidationError{
				Field:    field + ".label",
				Message:  fmt.Sprintf("label %q must match %s", node.Label, binding.NodeNamePattern),
				Severity: "error",
			})
		}
		if node.ID != "" && !binding.ValidateNodeName(node.ID) {
			errors = append(errors, ValidationError{
				Field:    field + ".id",
				Message:  fmt.Sprintf("id %q must match %s", node.ID, binding.NodeNamePattern),
				Severity: "error",
			})
		}
		if node.Representative != "" && !binding.ValidateNodeName(node.Representative) {
			errors = append(errors, ValidationError{
				Field:    field + ".representative",
				Message:  fmt.Sprintf("representative %q must match %s", node.Representative, binding.NodeNamePattern),
				Severity: "error",
			})
		}
	}

	// Rule 5: reviewer_node resolvability check (severity: warning, #626).
	// An unresolvable reviewer_node is never a load error — the unified
	// fail-open rule means command approval simply stays permissive in that
	// case — but it MUST be loud (a typo here silently disables blocking
	// mode otherwise) and auditable, per the decided requirements.
	if name := strings.TrimSpace(cfg.ReviewerNode); name != "" {
		if _, exists := cfg.Nodes[name]; !exists {
			errors = append(errors, ValidationError{
				Field:    "reviewer_node",
				Message:  fmt.Sprintf("reviewer_node %q does not match any configured node; command approval will fail open until this is fixed", name),
				Severity: "warning",
			})
		}
	}
	for i, policy := range cfg.CommandApproval {
		name := strings.TrimSpace(policy.ReviewerNode)
		if name == "" {
			continue
		}
		if _, exists := cfg.Nodes[name]; !exists {
			errors = append(errors, ValidationError{
				Field:    fmt.Sprintf("command_approval[%d].reviewer_node", i),
				Message:  fmt.Sprintf("reviewer_node %q does not match any configured node; this policy will fail open until this is fixed", name),
				Severity: "warning",
			})
		}
	}

	return errors
}

// normalizeEdge normalizes an edge string for duplicate detection.
// Converts "A --- B" and "B --- A" to the same representation.
func normalizeEdge(edge string) string {
	edge = strings.TrimSpace(edge)

	// Detect separator
	var nodes []string
	separator := edgeSeparator(edge)
	switch separator {
	case "---":
		nodes = splitEdgeNodeNames(edge)
	default:
		// Invalid separator - return as-is
		return edge
	}

	// For undirected edges, sort nodes to normalize
	if len(nodes) > 1 {
		// Sort to get consistent representation
		sorted := make([]string, len(nodes))
		copy(sorted, nodes)
		for i := 0; i < len(sorted)-1; i++ {
			for j := i + 1; j < len(sorted); j++ {
				if sorted[i] > sorted[j] {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}
		return strings.Join(sorted, " --- ")
	}

	return edge
}
