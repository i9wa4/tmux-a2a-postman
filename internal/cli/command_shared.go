package cli

import (
	"fmt"
	"regexp"
)

// commandExitError preserves the executed command's exit status at the CLI
// process boundary. main forwards ExitCode without replacing it with 1.
type commandExitError struct {
	code int
}

func (e commandExitError) Error() string {
	return fmt.Sprintf("bash command exited with status %d", e.code)
}

func (e commandExitError) ExitCode() int {
	return e.code
}

var commandApprovalThreadIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// validateCommandApprovalThreadID protects the retained send-heredoc
// correlation fields from frontmatter injection. Empty means no correlation.
func validateCommandApprovalThreadID(threadID string) error {
	if threadID == "" {
		return nil
	}
	if !commandApprovalThreadIDPattern.MatchString(threadID) {
		return fmt.Errorf("--thread-id %q contains characters outside [A-Za-z0-9._-]", threadID)
	}
	return nil
}
