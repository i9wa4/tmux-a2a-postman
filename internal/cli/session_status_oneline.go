package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

// isShellCommand returns true if cmd is a known interactive shell name.
// Used by RunGetSessionStatusOneline to exclude panes with no AI running (Issue #312).
var shellCommands = map[string]bool{
	"bash": true, "zsh": true, "sh": true, "fish": true,
	"dash": true, "ksh": true, "csh": true, "tcsh": true, "nu": true,
}

func isShellCommand(cmd string) bool {
	return shellCommands[cmd]
}

// RunGetSessionStatusOneline formats the compact all-session health view in one line.
// Output format: [0](window0,window1,)🔷🔵:🟢 [1]🔴
func RunGetSessionStatusOneline(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("get-health-oneline", flag.ContinueOnError)
	// Options struct fields (--params scope): json
	// SYNC: schema get-health-oneline properties; alwaysExcludedParams map
	jsonOut := fs.Bool("json", false, `output json: {"status":"..."}`)
	contextID := fs.String("context-id", "", "Context ID (optional, auto-resolved from session)")
	sessionFlag := fs.String("session", "", "tmux session name (optional, auto-detected)")
	configPath := fs.String("config", "", "Config file path")
	paramsFlag := fs.String("params", "", "command parameters as JSON or shorthand (k=v,k=v)")
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
			if *jsonOut {
				return json.NewEncoder(stdout).Encode(struct {
					Status string `json:"status"`
				}{Status: ""})
			}
			return nil
		}
		return err
	}
	if !ok {
		if *jsonOut {
			return json.NewEncoder(stdout).Encode(struct {
				Status string `json:"status"`
			}{Status: ""})
		}
		return nil
	}

	statusStr := formatAllSessionHealthOneline(healths)
	if statusStr != "" {
		if *jsonOut {
			return json.NewEncoder(stdout).Encode(struct {
				Status string `json:"status"`
			}{Status: statusStr})
		}
		_, err := fmt.Fprintln(stdout, statusStr)
		return err
	}
	if *jsonOut {
		return json.NewEncoder(stdout).Encode(struct {
			Status string `json:"status"`
		}{Status: ""})
	}
	return nil
}

func formatAllSessionHealthOneline(healths status.AllSessionHealth) string {
	var sessionStatuses []string
	for i, health := range healths.Sessions {
		sessionStatus := formatSessionHealthOneline(health)
		sessionStatuses = append(sessionStatuses, fmt.Sprintf("[%d]%s", i, sessionStatus))
	}
	return strings.Join(sessionStatuses, " ")
}

func formatSessionHealthOneline(health status.SessionHealth) string {
	return health.Compact
}
