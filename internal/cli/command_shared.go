package cli

import (
	"fmt"
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
