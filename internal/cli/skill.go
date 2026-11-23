package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/i9wa4/tmux-a2a-postman/internal/helpdocs"
)

// RunSkill reads only embedded content; it needs no workspace or configuration.
func RunSkill(args []string) error {
	return WriteSkill(os.Stdout, args)
}

func WriteSkill(out io.Writer, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("usage: tmux-a2a-postman skill <name> [reference]")
	}
	data, err := helpdocs.ReadSkill(args[0], args[1:]...)
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	return err
}
