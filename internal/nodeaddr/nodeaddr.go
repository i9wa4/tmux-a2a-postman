package nodeaddr

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

// Split separates an optional session-prefixed node address into its parts.
// Returns hasSession=false for bare node names.
func Split(address string) (sessionName, nodeName string, hasSession bool) {
	parts := strings.SplitN(address, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1], true
	}
	return "", address, false
}

// Simple returns the local node segment for a bare or session-prefixed address.
func Simple(address string) string {
	_, nodeName, hasSession := Split(address)
	if hasSession {
		return nodeName
	}
	return address
}

// Full returns a session-prefixed node key, using defaultSession for bare names.
func Full(address, defaultSession string) string {
	if _, _, hasSession := Split(address); hasSession {
		return address
	}
	return defaultSession + ":" + address
}

// Validate accepts either a bare node name or an explicit session:node address.
func Validate(address string) error {
	sessionName, nodeName, hasSession := Split(address)
	if hasSession {
		if strings.Contains(nodeName, ":") {
			return fmt.Errorf("invalid node address %q (must be <session>:<node>)", address)
		}
		if _, err := config.ValidateSessionName(sessionName); err != nil {
			return fmt.Errorf("invalid node address %q: %w", address, err)
		}
	}
	if !binding.ValidateNodeName(nodeName) {
		return fmt.Errorf("invalid node name %q (must match %s)", nodeName, binding.NodeNamePattern)
	}
	return nil
}

// EncodeFilenameSegment escapes an address only when it contains reserved
// filename delimiter substrings used by the message filename grammar.
func EncodeFilenameSegment(address string) string {
	if !strings.Contains(address, "-from-") && !strings.Contains(address, "-to-") {
		return address
	}
	return "~" + hex.EncodeToString([]byte(address))
}

// DecodeFilenameSegment restores an address previously escaped for the message
// filename grammar.
func DecodeFilenameSegment(segment string) (string, error) {
	if !strings.HasPrefix(segment, "~") {
		return segment, nil
	}
	encoded := strings.TrimPrefix(segment, "~")
	if !isLowerHexEven(encoded) {
		return segment, nil
	}
	decoded, err := hex.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("invalid encoded node address %q: %w", segment, err)
	}
	return string(decoded), nil
}

func isLowerHexEven(value string) bool {
	if len(value) == 0 || len(value)%2 != 0 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
