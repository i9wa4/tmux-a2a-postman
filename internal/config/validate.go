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

	// Rule 1: edges node reference check (severity: error)
	// IMPORTANT: "postman" is a reserved name and should be skipped (not an error)
	for i, edge := range cfg.Edges {
		// Parse edge using same logic as ParseEdges
		var separator string
		if strings.Contains(edge, " --> ") {
			separator = " --> "
		} else if strings.Contains(edge, " -- ") {
			separator = " -- "
		} else {
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

	// Rule 4: Deprecated fields (none currently, placeholder for future)
	// Add deprecated field checks here as needed

	return errors
}
