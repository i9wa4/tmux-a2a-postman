package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

// statusDot returns the status indicator string for a pane.
// When isTerminal is true, returns a lipgloss-styled ANSI dot.
// When isTerminal is false, returns a plain emoji suitable for tmux #() output.
// lipgloss's own color detection is intentionally bypassed here because #() contexts
// require plain text regardless of color capability. (Issue #275)
func statusDot(status string, isTerminal bool) string {
	if isTerminal {
		activeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
		pendingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("51"))
		composingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("33"))
		spinningStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("226"))
		staleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
		userInputStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
		switch status {
		case "ready", "active", "idle":
			return activeStyle.Render("●")
		case "pending":
			return pendingStyle.Render("●")
		case "composing":
			return composingStyle.Render("●")
		case "spinning":
			return spinningStyle.Render("●")
		case "user_input":
			return userInputStyle.Render("●")
		default:
			return staleStyle.Render("●")
		}
	}
	switch status {
	case "ready", "active", "idle":
		return "🟢"
	case "pending":
		return "🔷"
	case "composing":
		return "🔵"
	case "spinning":
		return "🟡"
	case "user_input":
		return "🟣"
	default:
		return "🔴"
	}
}

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
// Output format: [0]window0_panes:window1_panes [1]window0_panes:window1_panes ...
// TTY output (interactive terminal): ANSI-colored dots (● green/blue/yellow/red)
// Non-TTY output (tmux #(), pipes): plain emoji (🟢/🔵/🟡/🔴)
// Pane status: active/idle=green, composing=blue, spinning=yellow, stale=red
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

	isTerminal := false
	if file, ok := stdout.(interface{ Fd() uintptr }); ok {
		isTerminal = term.IsTerminal(file.Fd())
	}

	statusStr := formatAllSessionHealthOneline(healths, isTerminal)
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

func sessionStatusDot(visibleState string, isTerminal bool) string {
	if isTerminal {
		unavailableStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
		switch visibleState {
		case "unavailable", "unowned":
			return unavailableStyle.Render("●")
		default:
			return statusDot(visibleState, true)
		}
	}
	switch visibleState {
	case "unavailable", "unowned":
		return "⚪"
	default:
		return statusDot(visibleState, false)
	}
}

func formatAllSessionHealthOneline(healths []status.SessionHealth, isTerminal bool) string {
	var sessionStatuses []string
	for i, health := range healths {
		sessionStatus := formatSessionHealthOneline(health, isTerminal)
		if sessionStatus == "" {
			visibleState := health.VisibleState
			if visibleState == "" {
				visibleState = status.SessionVisibleState(health.Nodes)
			}
			sessionStatus = sessionStatusDot(visibleState, isTerminal)
		}
		sessionStatuses = append(sessionStatuses, fmt.Sprintf("[%d]%s", i, sessionStatus))
	}
	return strings.Join(sessionStatuses, " ")
}

func formatSessionHealthOneline(health status.SessionHealth, isTerminal bool) string {
	nodeByName := make(map[string]status.NodeHealth, len(health.Nodes))
	for _, node := range health.Nodes {
		nodeByName[node.Name] = node
	}

	var windowStatuses []string
	for _, window := range health.Windows {
		var paneStatuses strings.Builder
		for _, windowNode := range window.Nodes {
			nodeName := windowNode.Name
			node, ok := nodeByName[nodeName]
			if !ok {
				continue
			}
			if isShellCommand(node.CurrentCommand) {
				continue
			}
			paneStatuses.WriteString(statusDot(node.VisibleState, isTerminal))
		}
		if paneStatuses.Len() > 0 {
			windowStatuses = append(windowStatuses, paneStatuses.String())
		}
	}

	if len(windowStatuses) == 0 {
		return ""
	}
	return strings.Join(windowStatuses, ":")
}
