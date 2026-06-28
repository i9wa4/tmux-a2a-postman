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

// RunGetSessionStatusOneline formats the compact all-session status view in one line.
// Output format: [0]🔷🟡:🟢 [1]🔴
func RunGetSessionStatusOneline(stdout io.Writer, args []string) error {
	return runGetSessionStatusOnelineWithContext(commandContext{stdout: stdout}, args)
}

func runGetSessionStatusOnelineWithContext(ctx commandContext, args []string) error {
	ctx = ctx.withDefaults()
	fs := flag.NewFlagSet("get-status-oneline", flag.ContinueOnError)
	fs.SetOutput(ctx.stderr)
	cliutil.SetUsageWithoutContextID(fs)
	contextID := fs.String("context-id", "", "Context ID (optional, auto-resolved from session)")
	configPath := fs.String("config", "", "Config file path")
	severity := fs.Bool("severity", false, "Print opt-in compact contextual severity tokens")
	if err := fs.Parse(args); err != nil {
		return err
	}

	statuses, _, ok, err := collectAllSessionStatusWithContext(ctx, *contextID, "", *configPath, ctx.collectSessionStatus)
	if err != nil {
		if strings.Contains(err.Error(), "no active postman found") {
			return nil
		}
		return err
	}
	if !ok {
		return nil
	}

	statusStr := formatAllSessionStatusOneline(statuses)
	if *severity {
		statusStr = formatAllSessionStatusSeverityOneline(statuses)
	}
	if statusStr != "" {
		_, err := fmt.Fprintln(ctx.stdout, statusStr)
		return err
	}
	return nil
}

func formatAllSessionStatusOneline(statuses status.AllSessionStatus) string {
	var sessionStatuses []string
	for i, sessionStatusPayload := range statuses.Sessions {
		sessionStatus := formatSessionStatusOneline(sessionStatusPayload)
		sessionStatuses = append(sessionStatuses, fmt.Sprintf("[%d]%s", i, sessionStatus))
	}
	return strings.Join(sessionStatuses, " ")
}

func formatSessionStatusOneline(sessionStatus status.SessionStatus) string {
	return sessionStatus.Compact
}

func formatAllSessionStatusSeverityOneline(statuses status.AllSessionStatus) string {
	var sessionStatuses []string
	for i, sessionStatusPayload := range statuses.Sessions {
		sessionStatus := formatSessionStatusSeverityOneline(sessionStatusPayload)
		sessionStatuses = append(sessionStatuses, fmt.Sprintf("[%d]%s", i, sessionStatus))
	}
	return strings.Join(sessionStatuses, " ")
}

func formatSessionStatusSeverityOneline(sessionStatus status.SessionStatus) string {
	if sessionStatus.CompactSeverity != "" {
		return sessionStatus.CompactSeverity
	}
	return "ok:session"
}
