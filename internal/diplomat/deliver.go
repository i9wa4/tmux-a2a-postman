package diplomat

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CrossContextDelivered is the event type for successful cross-context delivery.
const CrossContextDelivered = "cross_context_delivered"

// Renamer is an interface for file rename operations (enables testing).
type Renamer interface {
	Rename(src, dst string) error
}

// OSRenamer implements Renamer using os.Rename.
type OSRenamer struct{}

// Rename moves a file from src to dst using os.Rename.
func (OSRenamer) Rename(src, dst string) error { return os.Rename(src, dst) }

// Deliverer handles cross-context message delivery with trace-ID dedup.
// Issue #163 Task 2: core delivery engine.
type Deliverer struct {
	seenTraceIDs map[string]bool
}

// NewDeliverer creates a new Deliverer with an empty trace-ID set.
func NewDeliverer() *Deliverer {
	return &Deliverer{seenTraceIDs: make(map[string]bool)}
}

// DeliverCrossContextMessage delivers a cross-context message from the diplomat
// drop directory to the target node's inbox. Performs hop-limit, trace-ID dedup,
// and target validation checks. Dead-letters invalid messages.
func (d *Deliverer) DeliverCrossContextMessage(
	postPath, baseDir, contextID, sessionName, to, traceID string,
	hopCount int,
	renamer Renamer,
) (string, error) {
	deadLetterDir := filepath.Join(baseDir, "diplomat", contextID, "dead-letter")

	// Check hop limit
	if hopCount >= 1 {
		return "hop_limit", d.deadLetter(postPath, deadLetterDir, renamer)
	}

	// Validate target node
	if to == "" {
		return "missing_target_node", d.deadLetter(postPath, deadLetterDir, renamer)
	}

	// Check trace-ID dedup
	if traceID != "" && d.seenTraceIDs[traceID] {
		return "duplicate_trace_id", d.deadLetter(postPath, deadLetterDir, renamer)
	}
	if traceID != "" {
		d.seenTraceIDs[traceID] = true
	}

	// Resolve inbox path
	inboxDir := filepath.Join(baseDir, contextID, sessionName, "inbox", to)
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		return "inbox_mkdir_failed", fmt.Errorf("creating inbox dir: %w", err)
	}

	dst := filepath.Join(inboxDir, filepath.Base(postPath))
	if err := renamer.Rename(postPath, dst); err != nil {
		_ = d.deadLetter(postPath, deadLetterDir, renamer)
		return "rename_failed", err
	}

	return "", nil // success
}

// deadLetter moves a file to the dead-letter directory.
func (d *Deliverer) deadLetter(postPath, deadLetterDir string, renamer Renamer) error {
	if err := os.MkdirAll(deadLetterDir, 0o700); err != nil {
		return err
	}
	return renamer.Rename(postPath, filepath.Join(deadLetterDir, filepath.Base(postPath)))
}

// GenerateTraceID creates a UUID4-format trace ID using crypto/rand.
func GenerateTraceID() (string, error) {
	var uuid [16]byte
	if _, err := rand.Read(uuid[:]); err != nil {
		return "", err
	}
	// Set version 4 and variant bits
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	uuid[8] = (uuid[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16]), nil
}

// DropDirPath returns the diplomat drop directory for a target context.
func DropDirPath(baseDir, targetContextID string) string {
	return filepath.Join(baseDir, "diplomat", targetContextID, "post")
}

// ParseCrossContextTarget parses a "contextID:node" string.
func ParseCrossContextTarget(target string) (contextID, node string, err error) {
	parts := strings.SplitN(target, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid cross-context target %q: expected <contextID>:<node>", target)
	}
	return parts[0], parts[1], nil
}
