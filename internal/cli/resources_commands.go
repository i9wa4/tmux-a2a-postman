package cli

import (
	"context"
	"encoding/json"
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

func RunSkills(args []string) error {
	if len(args) == 0 {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"skills": helpdocs.EntryNames(), "bundle_id": helpdocs.BundleID()})
	}
	if len(args) == 1 || len(args) == 2 {
		var data []byte
		var err error
		if len(args) == 1 {
			data, err = helpdocs.Entry(args[0])
		} else {
			data, err = helpdocs.Reference(args[0], args[1])
		}
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(data)
		return err
	}
	return errors.New("usage: tmux-a2a-postman skills [name [ref]]; use skills --help")
}
