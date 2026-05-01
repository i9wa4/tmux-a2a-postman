package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
)

// RunStatus is the public operator status command.
//
// Human output intentionally reuses the compact all-session status line. JSON
// output exposes the canonical all-session health payload for scripts and
// external adapters.
func RunStatus(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	jsonOut := fs.Bool("json", false, "output canonical all-session status JSON")
	contextID := fs.String("context-id", "", "Context ID (optional, auto-resolved from session)")
	sessionFlag := fs.String("session", "", "tmux session name (optional, auto-detected)")
	configPath := fs.String("config", "", "Config file path")
	paramsFlag := fs.String("params", "", "command parameters as JSON or shorthand (k=v,k=v)")
	commandName := fs.Name()
	if err := fs.Parse(args); err != nil {
		return err
	}

	explicitlySet := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		explicitlySet[f.Name] = true
	})
	if explicitlySet["params"] {
		resolvedParams, err := cliutil.ParseParams(*paramsFlag)
		if err != nil {
			return err
		}
		if err := cliutil.ApplyParams(fs, resolvedParams, explicitlySet, commandName); err != nil {
			return err
		}
	}

	healths, _, ok, err := collectAllSessionHealth(*contextID, *sessionFlag, *configPath)
	if err != nil {
		if strings.Contains(err.Error(), "no active postman found") {
			return writeEmptyStatus(stdout, *jsonOut)
		}
		return err
	}
	if !ok {
		return writeEmptyStatus(stdout, *jsonOut)
	}
	if *jsonOut {
		return json.NewEncoder(stdout).Encode(healths)
	}
	statusStr := formatAllSessionHealthOneline(healths)
	if statusStr == "" {
		return writeEmptyStatus(stdout, false)
	}
	_, err = fmt.Fprintln(stdout, statusStr)
	return err
}

func writeEmptyStatus(stdout io.Writer, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(stdout).Encode(emptyAllSessionHealth())
	}
	_, err := fmt.Fprintln(stdout, "No active sessions.")
	return err
}
