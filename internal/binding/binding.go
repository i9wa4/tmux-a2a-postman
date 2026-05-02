// Package binding owns the canonical node-name validation regex.
package binding

import "regexp"

// NodeNamePattern is the canonical regex for node names and IDs.
// Used both for validation and for error messages.
const NodeNamePattern = `^[a-zA-Z0-9][a-zA-Z0-9-]{0,63}$`

var validNodeNameRe = regexp.MustCompile(NodeNamePattern)

// ValidateNodeName reports whether s is a valid node name.
func ValidateNodeName(s string) bool {
	return validNodeNameRe.MatchString(s)
}
