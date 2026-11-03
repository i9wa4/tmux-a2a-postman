package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/helpdocs"
)

func RunResources(args []string) error {
	if len(args) != 0 {
		return errors.New("resources takes no arguments; use help resources")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root, err := helpdocs.ResourcePath(ctx)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, root)
	return err
}
