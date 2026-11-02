package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

// --config remains accepted for common invocation compatibility, but is never
// opened or used as a server membership filter by any status command.
func statusContext(command string, args []string) (workspace.Store, workspace.ServerContext, string, error) {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	_ = fs.String("config", "", "accepted for compatibility; status uses saved per-workspace configuration provenance")
	if err := ValidateFlagDuplicates(fs, args); err != nil {
		return workspace.Store{}, workspace.ServerContext{}, "", err
	}
	if err := fs.Parse(args); err != nil {
		return workspace.Store{}, workspace.ServerContext{}, "", err
	}
	if fs.NArg() != 0 {
		return workspace.Store{}, workspace.ServerContext{}, "", errors.New(command + " takes no positional arguments")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return workspace.Store{}, workspace.ServerContext{}, "", err
	}
	root, err := workspace.DefaultStateRoot(home, os.Getenv("XDG_STATE_HOME"))
	if err != nil {
		return workspace.Store{}, workspace.ServerContext{}, "", err
	}
	server, err := workspace.ParseServerContext(os.Getenv("TMUX"))
	return workspace.Store{StateRoot: root}, server, home, err
}

func RunWorkspaceGetStatus(args []string) error {
	store, server, _, err := statusContext("get-status", args)
	var raw []byte
	if err == nil {
		raw, _, err = store.ReadServerStatus(server)
	}
	if err != nil {
		if writeErr := json.NewEncoder(os.Stdout).Encode(map[string]any{"status": "unavailable", "reason": err.Error()}); writeErr != nil {
			return writeErr
		}
		return err
	}
	_, err = os.Stdout.Write(raw)
	return err
}

func RunWorkspaceGetStatusOneline(args []string) error {
	store, server, _, err := statusContext("get-status-oneline", args)
	var snapshot workspace.ServerStatus
	if err == nil {
		_, snapshot, err = store.ReadServerStatus(server)
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stdout, "?")
		return err
	}
	// Session IDs are native, never ordinal positions or digits from names.
	var output []string
	for _, session := range snapshot.Sessions {
		number, _ := strconv.Atoi(strings.TrimPrefix(session.ID, "$"))
		rows := []workspace.OperatorNode{}
		for _, window := range session.Windows {
			for _, pane := range window.Panes {
				if pane.Status != nil {
					rows = append(rows, workspace.OperatorNode{Session: number, Window: window.Index, Top: pane.Top, Left: pane.Left, Pane: pane.ID, Status: *pane.Status})
				}
			}
		}
		line, diagnostics := renderStatusOneline(rows, time.Now().UTC())
		if line == "" {
			line = fmt.Sprintf("[%d]", number)
		}
		output = append(output, line)
		for _, reason := range diagnostics {
			_, _ = fmt.Fprintln(os.Stderr, reason)
		}
	}
	if len(output) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "?")
		return errors.New("saved server has no sessions")
	}
	_, err = fmt.Fprintln(os.Stdout, strings.Join(output, " "))
	if !snapshot.Coverage.Complete {
		_, _ = fmt.Fprintln(os.Stderr, "saved status has partial coverage: "+snapshot.Coverage.Reason)
	}
	return err
}

func RunWorkspaceRefreshStatus(args []string) error {
	store, server, home, err := statusContext("refresh-status", args)
	result := workspace.RefreshResult{Status: "error"}
	if err == nil {
		result, err = (workspace.Mail{Store: store}).RefreshServerStatus(context.Background(), server, home, workspace.RefreshOptions{})
	}
	if err != nil {
		result.Reason = err.Error()
	}
	if writeErr := json.NewEncoder(os.Stdout).Encode(result); writeErr != nil {
		return writeErr
	}
	if err == nil && result.Reason != "" {
		_, _ = fmt.Fprintln(os.Stderr, result.Reason)
	}
	return err
}

func (c workspaceCommand) refreshStatusAfterOperation() (result workspace.RefreshResult) {
	span := c.trace.Start("server_refresh")
	defer func() { span.End(result.Status) }()
	server, err := workspace.ParseServerContext(os.Getenv("TMUX"))
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "postman: supplemental status refresh: "+err.Error())
		return workspace.RefreshResult{Status: "error", Reason: err.Error()}
	}
	result, err = c.mail.RefreshServerStatus(context.Background(), server, c.runtime.Home, workspace.RefreshOptions{})
	if result.Coverage != nil {
		c.trace.Add("panes_discovered", int64(result.Coverage.Enumerated), 0)
		c.trace.Add("panes_captured", int64(result.Coverage.Captured), 0)
		c.trace.Add("panes_reused", int64(result.Coverage.Reused), 0)
		c.trace.Add("panes_failed", int64(result.Coverage.Unavailable), 0)
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "postman: supplemental status refresh: "+err.Error())
	} else if result.Reason != "" {
		_, _ = fmt.Fprintln(os.Stderr, "postman: supplemental status refresh: "+result.Reason)
	}
	return result
}
