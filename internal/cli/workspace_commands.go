package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

// workspaceCommand is deliberately limited to the four file-first commands.
// All identity and routing inputs are rechecked by the workspace operations.
type workspaceCommand struct {
	topologyPath string
	topology     config.WorkspaceTopology
	runtime      workspace.Tmux
	mail         workspace.Mail
	caller       workspace.Snapshot
}

type workspaceFlagSet struct {
	fs               *flag.FlagSet
	path, md, legacy *string
}

func newWorkspaceFlags(name string) workspaceFlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	path := fs.String("config", "", "absolute path to postman.md")
	md := fs.String("postman-md", "", "absolute path to postman.md")
	legacy := fs.String("context-id", "", "removed daemon context ID")
	return workspaceFlagSet{fs, path, md, legacy}
}

func (f workspaceFlagSet) parse(args []string) (string, error) {
	fs, path, md, legacy := f.fs, f.path, f.md, f.legacy
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if *legacy != "" {
		return "", errors.New("--context-id is not supported by file-first commands")
	}
	if *path != "" && *md != "" {
		return "", errors.New("use only one of --config and --postman-md")
	}
	if *md != "" {
		*path = *md
	}
	if *path == "" {
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil || !filepath.IsAbs(home) {
				return "", errors.New("cannot resolve absolute HOME for default postman.md")
			}
			base = filepath.Join(home, ".config")
		} else if !filepath.IsAbs(base) {
			return "", errors.New("XDG_CONFIG_HOME must be absolute")
		}
		*path = filepath.Join(base, "tmux-a2a-postman", "postman.md")
	}
	if !filepath.IsAbs(*path) {
		return "", errors.New("postman.md path must be absolute")
	}
	return *path, nil
}

func openWorkspaceCommand(ctx context.Context, path string) (workspaceCommand, error) {
	var c workspaceCommand
	topology, err := config.LoadWorkspaceTopology(ctx, path)
	if err != nil {
		return c, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return c, err
	}
	root, err := workspace.DefaultStateRoot(home, os.Getenv("XDG_STATE_HOME"))
	if err != nil {
		return c, err
	}
	socket, _, ok := strings.Cut(os.Getenv("TMUX"), ",")
	if !ok || !filepath.IsAbs(socket) {
		return c, errors.New("TMUX must contain an absolute native tmux socket")
	}
	rt := workspace.Tmux{Socket: socket, Pane: os.Getenv("TMUX_PANE"), Home: home, Roles: topology.Nodes}
	snapshot, err := rt.Snapshot(ctx)
	if err != nil {
		return c, err
	}
	configured := false
	for _, node := range topology.Nodes {
		if node == snapshot.Caller.Role {
			configured = true
			break
		}
	}
	if !configured {
		return c, errors.New("caller is not a configured topology node")
	}
	return workspaceCommand{path, topology, rt, workspace.Mail{Store: workspace.Store{StateRoot: root}}, snapshot}, nil
}

func (c workspaceCommand) livePeers() []string {
	var peers []string
	for _, peer := range c.topology.Edges[c.caller.Caller.Role] {
		for _, binding := range c.caller.Bindings {
			if binding.Role == peer {
				peers = append(peers, peer)
				break
			}
		}
	}
	return peers
}

func (c workspaceCommand) permits(to string) bool {
	for _, peer := range c.topology.Edges[c.caller.Caller.Role] {
		if peer == to {
			return true
		}
	}
	return false
}

type workspaceOutput struct {
	Status       string                     `json:"status"`
	Message      *workspace.Message         `json:"message,omitempty"`
	MarkdownPath string                     `json:"markdown_absolute_path,omitempty"`
	ReceiptPath  string                     `json:"pop_receipt_absolute_path,omitempty"`
	Self         workspace.SelfState        `json:"self"`
	Execution    *workspace.ExecutionResult `json:"execution,omitempty"`
	Capture      *workspace.CaptureResult   `json:"capture,omitempty"`
	Notice       *workspace.NoticeResult    `json:"notice,omitempty"`
	Reason       string                     `json:"reason,omitempty"`
}

func writeWorkspaceOutput(w io.Writer, result workspaceOutput) error {
	return json.NewEncoder(w).Encode(result)
}

func unavailableWorkspaceOutput(err error) workspaceOutput {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return workspaceOutput{Status: "unavailable", Self: workspace.SelfState{AsOf: now, NextAction: "inspect state error", UnavailableReason: err.Error()}, Reason: err.Error()}
}

func RunWorkspaceGetStatus(args []string) error {
	f := newWorkspaceFlags("get-status")
	path, err := f.parse(args)
	if err != nil {
		return err
	}
	if f.fs.NArg() != 0 {
		return errors.New("get-status takes no positional arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := openWorkspaceCommand(ctx, path)
	if err != nil {
		_ = writeWorkspaceOutput(os.Stdout, unavailableWorkspaceOutput(err))
		return err
	}
	self := c.mail.State(ctx, c.runtime, c.livePeers())
	result := workspaceOutput{Status: "ok", Self: self}
	if !self.Known {
		result.Status, result.Reason = "unavailable", self.UnavailableReason
	}
	if err := writeWorkspaceOutput(os.Stdout, result); err != nil {
		return err
	}
	if !self.Known {
		return errors.New(self.UnavailableReason)
	}
	return nil
}

func RunWorkspacePop(args []string) error {
	f := newWorkspaceFlags("pop")
	capture := f.fs.Bool("capture-related-panes", false, "save bounded evidence from this mail's verified sender and receiver panes")
	path, err := f.parse(args)
	if err != nil {
		return err
	}
	if f.fs.NArg() > 1 {
		return errors.New("pop accepts at most one exact message ID")
	}
	selected := ""
	if f.fs.NArg() == 1 {
		selected = f.fs.Arg(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := openWorkspaceCommand(ctx, path)
	if err != nil {
		_ = writeWorkspaceOutput(os.Stdout, unavailableWorkspaceOutput(err))
		return err
	}
	claimed, claimErr := c.mail.PopWithState(ctx, c.runtime, selected, c.livePeers())
	result := workspaceOutput{Status: "empty", Self: claimed.Self}
	if claimed.Claim != nil {
		result.Status = "message"
		result.Message = &claimed.Claim.Message
		result.MarkdownPath = claimed.Claim.Path
		result.ReceiptPath = strings.TrimSuffix(claimed.Claim.Path, ".md") + ".pop.json"
		if *capture {
			evidence := c.mail.CaptureRelated(ctx, c.runtime, claimed.Claim.Message)
			result.Capture = &evidence
		}
	}
	if claimErr != nil {
		result.Status, result.Reason = "unavailable", claimErr.Error()
	}
	if err := writeWorkspaceOutput(os.Stdout, result); err != nil {
		return err
	}
	return claimErr
}

func RunWorkspaceSendHeredoc(args []string) error {
	f := newWorkspaceFlags("send-heredoc")
	capture := f.fs.Bool("capture-related-panes", false, "save bounded evidence from this mail's verified sender and receiver panes")
	to := f.fs.String("to", "", "recipient node")
	replyTo := f.fs.String("reply-to", "", "claimed message ID")
	fills := f.fs.String("fills-input-request-id", "", "exact input request ID")
	required := f.fs.Bool("reply-required", false, "request an exact reply")
	noReply := f.fs.Bool("no-reply", false, "do not request a reply")
	path, err := f.parse(args)
	if err != nil {
		return err
	}
	if f.fs.NArg() != 0 || *to == "" || (*required && *noReply) {
		return errors.New("send-heredoc requires --to, no body argv, and one reply policy")
	}
	if defaultStdinIsTerminal(os.Stdin) {
		return sendHeredocBodySourceError()
	}
	body, err := io.ReadAll(io.LimitReader(os.Stdin, 60*1024+1))
	if err != nil || len(body) == 0 || len(body) > 60*1024 {
		return errors.New("quoted heredoc body must contain 1 to 61440 bytes")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := openWorkspaceCommand(ctx, path)
	if err != nil {
		_ = writeWorkspaceOutput(os.Stdout, unavailableWorkspaceOutput(err))
		return err
	}
	if !c.permits(*to) {
		err = errors.New("recipient is not a configured outgoing edge")
		_ = writeWorkspaceOutput(os.Stdout, unavailableWorkspaceOutput(err))
		return err
	}
	sent, sendErr := c.mail.SendWithState(ctx, c.runtime, *to, string(body), *replyTo, *fills, *required, c.livePeers())
	result := workspaceOutput{Status: "sent", Message: &sent.Message, Self: sent.Self}
	if sendErr == nil {
		notice := workspace.NotifyCommitted(ctx, c.runtime, sent.Message)
		result.Notice = &notice
	}
	if sendErr == nil && *capture {
		evidence := c.mail.CaptureRelated(ctx, c.runtime, sent.Message)
		result.Capture = &evidence
	}
	if sendErr != nil {
		result.Status, result.Reason = "unavailable", sendErr.Error()
		// The send may have committed before a later filesystem step failed.
		// Retain its candidate ID so the caller can inspect that exact operation
		// instead of blindly creating another send.
		if sent.Message.ID == "" {
			result.Message = nil
		}
	}
	if err := writeWorkspaceOutput(os.Stdout, result); err != nil {
		return err
	}
	return sendErr
}

func RunWorkspaceExecuteBash(args []string) error {
	f := newWorkspaceFlags("execute-bash")
	command := f.fs.String("command", "", "exact Bash command")
	path, err := f.parse(args)
	if err != nil {
		return err
	}
	if f.fs.NArg() != 0 || strings.TrimSpace(*command) == "" {
		return errors.New("execute-bash requires --command and no positional arguments")
	}
	ctx, cancel := defaultInterruptContext()
	defer cancel()
	c, err := openWorkspaceCommand(ctx, path)
	if err != nil {
		_ = writeWorkspaceOutput(os.Stdout, unavailableWorkspaceOutput(err))
		return err
	}
	executor := workspace.Executor{Mail: c.mail, Resolve: workspace.ConfiguredApprovalResolver(path), Stdout: os.Stdout, Stderr: os.Stderr}
	execution, runErr := executor.Execute(ctx, c.runtime, *command)
	stateCtx, stateCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stateCancel()
	result := workspaceOutput{Status: execution.Status, Execution: &execution, Self: c.mail.State(stateCtx, c.runtime, c.livePeers()), Reason: execution.Reason}
	if err := writeWorkspaceOutput(os.Stderr, result); err != nil {
		return err
	}
	if runErr != nil {
		return runErr
	}
	if execution.ExitStatus != nil && *execution.ExitStatus != 0 {
		return commandExitError{code: *execution.ExitStatus}
	}
	if execution.Status != "executed" {
		return errors.New(execution.Status + ": " + execution.Reason)
	}
	return nil
}
