package diplomat

import (
	"fmt"
	"os"
	"path/filepath"
)

// AppendAuditLog appends an entry to the diplomat audit log.
// Issue #165 Task 3: all delivery outcomes (success and dead-letter) are logged.
// Path: {baseDir}/diplomat/audit.log
func AppendAuditLog(baseDir, entry string) error {
	dir := filepath.Join(baseDir, "diplomat")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating diplomat dir: %w", err)
	}
	logPath := filepath.Join(dir, "audit.log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening audit log: %w", err)
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, entry)
	return err
}

// CheckAllowlist validates that a source node is permitted by the allowlist.
// Issue #165 Task 2: empty allowlist means allow all; non-empty enforces.
func CheckAllowlist(allowlist []string, sourceNode string) bool {
	if len(allowlist) == 0 {
		return true
	}
	for _, allowed := range allowlist {
		if allowed == sourceNode {
			return true
		}
	}
	return false
}
