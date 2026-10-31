package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

// roleBoundRuntime makes the role used for routing the same role verified by
// the mail operation under its workspace lock. A pane retitle cannot silently
// turn an authorized route into a send from a different role.
type roleBoundRuntime struct {
	workspace.Runtime
	role string
}

func (r roleBoundRuntime) Snapshot(ctx context.Context) (workspace.Snapshot, error) {
	s, err := r.Runtime.Snapshot(ctx)
	if err != nil {
		return s, err
	}
	if s.Caller.Role != r.role {
		return workspace.Snapshot{}, workspace.ErrUnknown
	}
	return s, nil
}

func (c workspaceCommand) boundRuntime() workspace.Runtime {
	return roleBoundRuntime{Runtime: c.runtime, role: c.caller.Caller.Role}
}

type workspaceFlagSet struct {
	fs       *flag.FlagSet
	path, md *string
}

func newWorkspaceFlags(name string) workspaceFlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	path := fs.String("config", "", "absolute path to postman.md")
	md := fs.String("postman-md", "", "absolute path to postman.md")
	return workspaceFlagSet{fs, path, md}
}

func (f workspaceFlagSet) parse(args []string) (string, error) {
	fs, path, md := f.fs, f.path, f.md
	if err := fs.Parse(args); err != nil {
		return "", err
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

func (c workspaceCommand) state(ctx context.Context) workspace.SelfState {
	return c.withVerifiedSessionPeers(ctx, c.mail.State(ctx, c.boundRuntime(), c.livePeers()))
}

// withVerifiedSessionPeers adds live, declared session peers to an existing
// mail-state snapshot. Send and POP already return that snapshot, so their
// responses must not perform a second mail-state scan just for peer guidance.
func (c workspaceCommand) withVerifiedSessionPeers(ctx context.Context, self workspace.SelfState) workspace.SelfState {
	if !self.Known {
		return self
	}
	verifiedSelf := func(snapshot workspace.Snapshot) bool {
		return snapshot.Root == c.caller.Root && snapshot.Caller == c.caller.Caller &&
			snapshot.Caller.Role == self.Node
	}
	bound, err := c.boundRuntime().Snapshot(ctx)
	if err != nil {
		return unavailableSelf(self, err)
	}
	if !verifiedSelf(bound) {
		return unavailableSelf(self, workspace.ErrUnknown)
	}
	if len(c.topology.Sessions) == 0 {
		return self
	}
	caller, err := c.runtime.NamedCaller(ctx)
	if err != nil {
		return unavailableSelf(self, err)
	}
	named, err := caller.Snapshot(ctx)
	if err != nil {
		return unavailableSelf(self, err)
	}
	if !verifiedSelf(named) {
		return unavailableSelf(self, workspace.ErrUnknown)
	}
	bound, err = c.boundRuntime().Snapshot(ctx)
	if err != nil {
		return unavailableSelf(self, err)
	}
	if !verifiedSelf(bound) {
		return unavailableSelf(self, workspace.ErrUnknown)
	}
	for _, session := range c.topology.Sessions {
		if session.Name == caller.Name() {
			continue
		}
		address := session.Name + ":" + session.InterfaceNode
		if _, _, err := c.topology.InterfaceRoute(caller.Name(), c.caller.Caller.Role, address); err != nil {
			continue
		}
		if _, err := c.runtime.FindNamedPeer(ctx, session.Name, session.InterfaceNode); err == nil {
			self.Peers = append(self.Peers, address)
		}
	}
	named, err = caller.Snapshot(ctx)
	if err != nil {
		return unavailableSelf(self, err)
	}
	if !verifiedSelf(named) {
		return unavailableSelf(self, workspace.ErrUnknown)
	}
	sort.Strings(self.Peers)
	return self
}

func unavailableSelf(self workspace.SelfState, err error) workspace.SelfState {
	self.Known, self.Unread, self.Inbound, self.Outbound, self.Peers = false, nil, nil, nil, nil
	self.Activity = workspace.ActivityState{Status: "unknown", Reason: "caller identity unavailable"}
	self.UnavailableReason, self.NextAction = err.Error(), "inspect state error"
	return self
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
	return workspaceOutput{Status: "unavailable", Self: workspace.SelfState{
		AsOf: now, NextAction: "inspect state error",
		Activity:          workspace.ActivityState{Status: "unknown", Reason: "mail state unavailable"},
		UnavailableReason: err.Error(),
	}, Reason: err.Error()}
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
	self := c.state(ctx)
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
	var claimed workspace.PopResult
	claimErr := retryWorkspaceAdmission(ctx, func() (bool, error) {
		var err error
		claimed, err = c.mail.PopWithState(ctx, c.boundRuntime(), selected, c.livePeers())
		return claimed.Claim == nil, err
	})
	result := workspaceOutput{Status: "empty", Self: c.withVerifiedSessionPeers(ctx, claimed.Self)}
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
	if strings.Contains(*to, ":") {
		caller, routeErr := c.runtime.NamedCaller(ctx)
		var peer workspace.NamedRuntime
		var recipientSession, recipientRole string
		if routeErr == nil {
			var verified workspace.Snapshot
			verified, routeErr = caller.Snapshot(ctx)
			if routeErr == nil && verified.Caller.Role != c.caller.Caller.Role {
				routeErr = workspace.ErrUnknown
			}
			if routeErr == nil {
				recipientSession, recipientRole, routeErr = c.topology.InterfaceRoute(caller.Name(), verified.Caller.Role, *to)
			}
		}
		if routeErr == nil {
			peer, routeErr = c.runtime.FindNamedPeer(ctx, recipientSession, recipientRole)
		}
		if routeErr != nil {
			_ = writeWorkspaceOutput(os.Stdout, unavailableWorkspaceOutput(routeErr))
			return routeErr
		}
		var msg workspace.Message
		sendErr := retryWorkspaceAdmission(ctx, func() (bool, error) {
			var err error
			msg, err = c.mail.SendCrossWithReply(ctx, caller, peer, string(body), *replyTo, *fills, *required)
			return msg.ID == "", err
		})
		result := workspaceOutput{Status: "sent", Message: &msg, Self: c.state(ctx)}
		if sendErr == nil {
			notice := c.mail.Store.NotifyCommittedAfterCommit(ctx, caller, peer, msg)
			result.Notice = &notice
		}
		if sendErr == nil && *capture {
			evidence := c.mail.CaptureRelated(ctx, c.runtime, msg)
			result.Capture = &evidence
		}
		if sendErr != nil {
			result.Status, result.Reason = "unavailable", sendErr.Error()
			if msg.ID == "" {
				result.Message = nil
			}
		}
		if err := writeWorkspaceOutput(os.Stdout, result); err != nil {
			return err
		}
		return sendErr
	}
	if !c.permits(*to) {
		err = errors.New("recipient is not a configured outgoing edge")
		_ = writeWorkspaceOutput(os.Stdout, unavailableWorkspaceOutput(err))
		return err
	}
	var sent workspace.SendResult
	sendErr := retryWorkspaceAdmission(ctx, func() (bool, error) {
		var err error
		sent, err = c.mail.SendWithState(ctx, c.boundRuntime(), *to, string(body), *replyTo, *fills, *required, c.livePeers())
		return sent.Message.ID == "", err
	})
	result := workspaceOutput{Status: "sent", Message: &sent.Message, Self: c.withVerifiedSessionPeers(ctx, sent.Self)}
	if sendErr == nil {
		notice := c.mail.Store.NotifyCommittedAfterCommit(ctx, c.boundRuntime(), c.runtime, sent.Message)
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

// RunWorkspaceRegisterNotifier configures a recipient; it is not a worker.
func RunWorkspaceRegisterNotifier(args []string) error {
	f := newWorkspaceFlags("register-notifier")
	runtime := f.fs.String("runtime", "codex", "recipient runtime: codex or bash")
	path, err := f.parse(args)
	if err != nil {
		return err
	}
	if f.fs.NArg() != 0 {
		return errors.New("register-notifier takes no positional arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := openWorkspaceCommand(ctx, path)
	if err != nil {
		return err
	}
	if *runtime == "bash" {
		return c.mail.Store.RegisterNotifier(ctx, c.boundRuntime())
	}
	if *runtime != "codex" {
		return errors.New("unsupported recipient runtime")
	}
	thread := os.Getenv("CODEX_THREAD_ID")
	if thread == "" {
		return errors.New("run register-notifier from the existing Codex conversation; CODEX_THREAD_ID is required")
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		home = filepath.Join(c.runtime.Home, ".codex")
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return err
	}
	program, err := exec.LookPath("codex")
	if err != nil {
		return err
	}
	program, err = filepath.Abs(program)
	if err != nil {
		return err
	}
	root := c.runtime.Home
	if c.caller.Root != "~" {
		root = filepath.Join(root, strings.TrimPrefix(c.caller.Root, "~/"))
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	n := workspace.CodexNotifier{ThreadID: thread, Home: home, Program: program, Topology: path, Root: root}
	if err := c.mail.Store.RegisterCodexNotifier(ctx, c.boundRuntime(), n); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "registered", "runtime": "codex", "thread_id": thread, "node": c.caller.Caller.Role})
}

// Retry only lock admission proven to precede any mutation. A claimed/partially
// committed operation is never replayed. This is bounded foreground contention
// handling, not an idle POP loop or delivery retry.
func retryWorkspaceAdmission(ctx context.Context, attempt func() (bool, error)) error {
	for {
		untouched, err := attempt()
		if !untouched || !errors.Is(err, workspace.ErrBusy) {
			return err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
