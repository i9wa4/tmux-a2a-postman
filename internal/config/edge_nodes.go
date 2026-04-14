package config

import "strings"

// EdgeNodeAllowed returns true when nodeName matches an edge entry exactly
// or by its raw node name after the session prefix.
func EdgeNodeAllowed(edgeNodes map[string]bool, nodeName string) bool {
	parts := strings.SplitN(nodeName, ":", 2)
	rawName := parts[len(parts)-1]
	return edgeNodes[nodeName] || edgeNodes[rawName]
}
