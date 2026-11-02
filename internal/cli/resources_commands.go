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
		return WriteHelp(os.Stdout, os.Stderr, []string{"skills"})
	}
	if args[0] == "list" && len(args) == 1 {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"skills": helpdocs.EntryNames(), "bundle_id": helpdocs.BundleID()})
	}
	if len(args) == 2 && args[0] == "show" {
		data, err := helpdocs.Entry(args[1])
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(data)
		return err
	}
	return errors.New("skills supports list or show <name>; manage installation with your Skill manager")
}
