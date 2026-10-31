package cli

import (
	_ "embed"
	"errors"
	"io"
)

//go:embed postman.bash
var notifierScript string

// RunNotifierScript prints setup for sourcing in the recipient's Bash.
func RunNotifierScript(w io.Writer, args []string) error {
	if len(args) != 0 {
		return errors.New("notifier-script takes no arguments")
	}
	_, err := io.WriteString(w, notifierScript)
	return err
}
