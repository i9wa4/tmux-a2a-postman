package main

import (
	"flag"
	"os"

	"github.com/i9wa4/tmux-a2a-postman/internal/cli"
)

// runGetContextID prints the live context ID for the current tmux session.
// Issue #249: zero-argument discovery primitive for AI agents.
func runGetContextID(args []string) error {
	fs := flag.NewFlagSet("get-context-id", flag.ContinueOnError)
	// Options struct fields (--params scope): json
	// SYNC: schema get-context-id properties; alwaysExcludedParams map
	jsonOut := fs.Bool("json", false, `output json: {"context_id":"..."}`)
	paramsFlag := fs.String("params", "", "command parameters as JSON or shorthand (k=v,k=v)")
	// NOTE: always-excluded from --params scope (SYNC: alwaysExcludedParams map)
	sessionFlag := fs.String("session", "", "tmux session name (optional, auto-detect if in tmux)")
	configPath := fs.String("config", "", "path to config file (optional)")
	commandName := fs.Name()
	// Step 1: parse flags
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Step 2: record explicitly-set flags (for --params precedence)
	explicitlySet := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		explicitlySet[f.Name] = true
	})
	// Steps 3+4: parse and apply --params to non-explicit flags
	if explicitlySet["params"] {
		resolvedParams, err := parseParams(*paramsFlag)
		if err != nil {
			return err
		}
		if err := applyParams(fs, resolvedParams, explicitlySet, commandName); err != nil {
			return err
		}
	}

	return cli.RunGetContextID(os.Stdout, *sessionFlag, *configPath, *jsonOut)
}
