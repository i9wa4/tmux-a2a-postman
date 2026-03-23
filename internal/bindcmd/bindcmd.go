// Package bindcmd implements the `tmux-a2a-postman bind` subcommands.
// Shell scripts in scripts/ are thin wrappers that delegate all bindings.toml
// mutations here; no TOML parsing occurs in the shell scripts themselves.
//
// Subcommands:
//   - register  §6.1 Phase A — append unassigned binding (Row 1)
//   - assign    §6.1 Phase B — activate binding with session/pane fields (Rows 2-4)
//   - deactivate §6.2        — set active=false (Rows 5-7)
//   - rebind    §6.3         — full field update
//
// TODO(Phase 2): add runtime e2e verification tests for these subcommands.
package bindcmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
)

// Run dispatches the bind subcommand from args.
// args[0] is the subcommand name; remaining args are flags.
func Run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("bind: subcommand required (register|assign|deactivate|rebind)")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "register":
		return runRegister(rest)
	case "assign":
		return runAssign(rest)
	case "deactivate":
		return runDeactivate(rest)
	case "rebind":
		return runRebind(rest)
	default:
		return fmt.Errorf("bind: unknown subcommand %q (register|assign|deactivate|rebind)", sub)
	}
}

// runRegister appends an unassigned binding (Row 1: active=false, no session).
func runRegister(args []string) error {
	fs := flag.NewFlagSet("bind register", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	path := fs.String("file", "", "path to bindings.toml (required)")
	channelID := fs.String("channel-id", "", "channel ID (required)")
	nodeName := fs.String("node-name", "", "node name (required)")
	contextID := fs.String("context-id", "", "context ID (required)")
	senders := fs.String("permitted-senders", "", "comma-separated permitted senders (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" || *channelID == "" || *nodeName == "" || *contextID == "" || *senders == "" {
		fs.Usage()
		return fmt.Errorf("bind register: --file, --channel-id, --node-name, --context-id, --permitted-senders are required")
	}
	reg, err := loadOrEmpty(*path)
	if err != nil {
		return err
	}
	b := binding.Binding{
		ChannelID:        *channelID,
		NodeName:         *nodeName,
		ContextID:        *contextID,
		Active:           false,
		PermittedSenders: splitSenders(*senders),
	}
	reg.Bindings = append(reg.Bindings, b)
	return reg.Save(*path)
}

// runAssign activates a binding and sets session/pane fields (Rows 2-4).
func runAssign(args []string) error {
	fs := flag.NewFlagSet("bind assign", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	path := fs.String("file", "", "path to bindings.toml (required)")
	nodeName := fs.String("node-name", "", "node name to assign (required)")
	sessionName := fs.String("session-name", "", "tmux session name (required)")
	paneTitle := fs.String("pane-title", "", "pane title for matching")
	paneNodeName := fs.String("pane-node-name", "", "pane node name for matching")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" || *nodeName == "" || *sessionName == "" {
		fs.Usage()
		return fmt.Errorf("bind assign: --file, --node-name, --session-name are required")
	}
	if *paneTitle == "" && *paneNodeName == "" {
		return fmt.Errorf("bind assign: at least one of --pane-title or --pane-node-name is required")
	}
	reg, err := loadOrEmpty(*path)
	if err != nil {
		return err
	}
	for i := range reg.Bindings {
		if reg.Bindings[i].NodeName == *nodeName {
			reg.Bindings[i].Active = true
			reg.Bindings[i].SessionName = *sessionName
			reg.Bindings[i].PaneTitle = *paneTitle
			reg.Bindings[i].PaneNodeName = *paneNodeName
			return reg.Save(*path)
		}
	}
	return fmt.Errorf("bind assign: node_name %q not found in %s", *nodeName, *path)
}

// runDeactivate sets active=false for the named node (Rows 5-7).
func runDeactivate(args []string) error {
	fs := flag.NewFlagSet("bind deactivate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	path := fs.String("file", "", "path to bindings.toml (required)")
	nodeName := fs.String("node-name", "", "node name to deactivate (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" || *nodeName == "" {
		fs.Usage()
		return fmt.Errorf("bind deactivate: --file and --node-name are required")
	}
	reg, err := loadOrEmpty(*path)
	if err != nil {
		return err
	}
	for i := range reg.Bindings {
		if reg.Bindings[i].NodeName == *nodeName {
			reg.Bindings[i].Active = false
			return reg.Save(*path)
		}
	}
	return fmt.Errorf("bind deactivate: node_name %q not found in %s", *nodeName, *path)
}

// runRebind performs a full field update on an existing binding (§6.3).
func runRebind(args []string) error {
	fs := flag.NewFlagSet("bind rebind", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	path := fs.String("file", "", "path to bindings.toml (required)")
	nodeName := fs.String("node-name", "", "node name to rebind (required)")
	sessionName := fs.String("session-name", "", "new session name")
	paneTitle := fs.String("pane-title", "", "new pane title")
	paneNodeName := fs.String("pane-node-name", "", "new pane node name")
	active := fs.Bool("active", true, "active state")
	senders := fs.String("permitted-senders", "", "comma-separated permitted senders (replaces existing)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" || *nodeName == "" {
		fs.Usage()
		return fmt.Errorf("bind rebind: --file and --node-name are required")
	}
	reg, err := loadOrEmpty(*path)
	if err != nil {
		return err
	}
	for i := range reg.Bindings {
		if reg.Bindings[i].NodeName == *nodeName {
			reg.Bindings[i].SessionName = *sessionName
			reg.Bindings[i].PaneTitle = *paneTitle
			reg.Bindings[i].PaneNodeName = *paneNodeName
			reg.Bindings[i].Active = *active
			if *senders != "" {
				reg.Bindings[i].PermittedSenders = splitSenders(*senders)
			}
			return reg.Save(*path)
		}
	}
	return fmt.Errorf("bind rebind: node_name %q not found in %s", *nodeName, *path)
}

// loadOrEmpty loads the registry from path, or returns an empty registry if
// the file does not exist (allows register on a fresh bindings.toml).
func loadOrEmpty(path string) (*binding.BindingRegistry, error) {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return &binding.BindingRegistry{}, nil
	}
	return binding.Load(path, binding.AllowEmptySenders())
}

// splitSenders splits a comma-separated sender list, trimming whitespace.
func splitSenders(s string) []string {
	var out []string
	for _, part := range splitComma(s) {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// splitComma splits s by comma and trims spaces from each element.
func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			part := trim(s[start:i])
			out = append(out, part)
			start = i + 1
		}
	}
	return out
}

// trim removes leading and trailing spaces.
func trim(s string) string {
	start, end := 0, len(s)
	for start < end && s[start] == ' ' {
		start++
	}
	for end > start && s[end-1] == ' ' {
		end--
	}
	return s[start:end]
}
