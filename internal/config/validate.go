package config

import (
	"fmt"
	"strings"
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
		// Parse edge using same logic as ParseEdges
		var separator string
		switch {
		case strings.Contains(edge, " --> "):
			separator = " --> "
		case strings.Contains(edge, " -- "):
			separator = " -- "
		default:
			// Invalid separator - skip (ParseEdges will handle)
			continue
		}

		parts := strings.Split(edge, separator)
		for _, part := range parts {
			nodeName := strings.TrimSpace(part)
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

	// Rule 2: Observes target check (severity: error)
	for nodeName, nodeConfig := range cfg.Nodes {
		for i, target := range nodeConfig.Observes {
			if _, exists := cfg.Nodes[target]; !exists {
				errors = append(errors, ValidationError{
					Field:    fmt.Sprintf("nodes.%s.observes[%d]", nodeName, i),
					Message:  fmt.Sprintf("target node %q not found in nodes configuration", target),
					Severity: "error",
				})
			}
		}
	}

	// Rule 3: Reserved section name check (severity: error)
	// Reserved names: "postman", "compaction_detection", "watchdog"
	reservedNames := []string{"postman", "compaction_detection", "watchdog"}
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

	// Rule 4: Duplicate edges check (severity: warning)
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

	// Rule 5: Deprecated fields (none currently, placeholder for future)
	// Add deprecated field checks here as needed

	return errors
}

// normalizeEdge normalizes an edge string for duplicate detection.
// Converts both "A -- B" and "B -- A" to the same representation.
func normalizeEdge(edge string) string {
	edge = strings.TrimSpace(edge)

	// Detect separator
	var separator string
	var nodes []string
	switch {
	case strings.Contains(edge, " --> "):
		// Directed edge: preserve order
		return edge
	case strings.Contains(edge, " -- "):
		separator = " -- "
		parts := strings.Split(edge, separator)
		for _, p := range parts {
			nodes = append(nodes, strings.TrimSpace(p))
		}
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
		return strings.Join(sorted, separator)
	}

	return edge
}
