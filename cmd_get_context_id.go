package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
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

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	sessionName := *sessionFlag
	if sessionName == "" {
		sessionName = config.GetTmuxSessionName()
	}
	if sessionName == "" {
		return fmt.Errorf("--session is required (or run inside tmux)")
	}
	sessionName = filepath.Base(sessionName)

	contextID, err := config.ResolveContextIDFromSession(baseDir, sessionName)
	if err != nil {
		return err
	}
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(struct {
			ContextID string `json:"context_id"`
		}{ContextID: contextID})
	}
	fmt.Println(contextID)
	return nil
}
