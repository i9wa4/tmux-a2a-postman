package cli

import (
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
// Output format: [0]🔷🟡:🟢 [1]🔴
func RunGetSessionStatusOneline(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("get-health-oneline", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	contextID := fs.String("context-id", "", "Context ID (optional, auto-resolved from session)")
	configPath := fs.String("config", "", "Config file path")
	severity := fs.Bool("severity", false, "Print opt-in compact contextual severity tokens")
	if err := fs.Parse(args); err != nil {
		return err
	}

	healths, _, ok, err := collectAllSessionHealth(*contextID, "", *configPath)
	if err != nil {
		if strings.Contains(err.Error(), "no active postman found") {
			return nil
		}
		return err
	}
	if !ok {
		return nil
	}

	statusStr := formatAllSessionHealthOneline(healths)
	if *severity {
		statusStr = formatAllSessionHealthSeverityOneline(healths)
	}
	if statusStr != "" {
		_, err := fmt.Fprintln(stdout, statusStr)
		return err
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

func formatAllSessionHealthSeverityOneline(healths status.AllSessionHealth) string {
	var sessionStatuses []string
	for i, health := range healths.Sessions {
		sessionStatus := formatSessionHealthSeverityOneline(health)
		sessionStatuses = append(sessionStatuses, fmt.Sprintf("[%d]%s", i, sessionStatus))
	}
	return strings.Join(sessionStatuses, " ")
}

func formatSessionHealthSeverityOneline(health status.SessionHealth) string {
	if health.CompactSeverity != "" {
		return health.CompactSeverity
	}
	return "ok:session"
}
