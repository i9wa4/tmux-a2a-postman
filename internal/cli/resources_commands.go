package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/helpdocs"
	"github.com/i9wa4/tmux-a2a-postman/internal/version"
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
	if args[0] != "install" && args[0] != "export" {
		return errors.New("skills supports list, install or export; use help skills")
	}
	f := flag.NewFlagSet("skills "+args[0], flag.ContinueOnError)
	var opts helpdocs.InstallOptions
	f.StringVar(&opts.Client, "client", "", "claude or codex")
	f.StringVar(&opts.Scope, "scope", "", "user or project")
	f.StringVar(&opts.Project, "project", "", "absolute project directory")
	f.StringVar(&opts.ExportDir, "to", "", "absolute export directory")
	if err := ValidateFlagDuplicates(f, args[1:]); err != nil {
		return err
	}
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return WriteHelp(os.Stdout, os.Stderr, []string{"skills"})
		}
		return err
	}
	if (args[0] == "export") != (opts.ExportDir != "") {
		return errors.New("skills export requires --to; skills install requires client and scope")
	}
	opts.Names, opts.Version = f.Args(), version.Version
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := helpdocs.Install(ctx, opts)
	output := map[string]any{"success": err == nil, "target": result.Target, "actions": result.Actions}
	if err != nil {
		output["reason"] = err.Error()
	}
	if writeErr := json.NewEncoder(os.Stdout).Encode(output); writeErr != nil {
		return writeErr
	}
	if err != nil {
		return reportedError{err}
	}
	return nil
}
