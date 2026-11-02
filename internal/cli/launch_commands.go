package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

func RunLaunchAgents(args []string) (retErr error) {
	output := map[string]any{"success": false}
	defer func() {
		output["success"] = retErr == nil
		if retErr != nil {
			output["reason"] = retErr.Error()
		}
		if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
			retErr = err
		}
	}()
	f := newWorkspaceFlags("launch-agents")
	socket := f.fs.String("socket", "", "explicit absolute tmux socket; otherwise use TMUX")
	cwd := f.fs.String("cwd", "", "absolute team directory; otherwise the invocation cwd")
	session := f.fs.String("session", "", "dedicated session name; otherwise preset/workspace-derived")
	dryRun := f.fs.Bool("dry-run", false, "validate and preview only; no tmux or state changes")
	list := f.fs.Bool("list", false, "list validated preset names without starting commands")
	selected, flags, err := launchArguments(f.fs, args)
	if err != nil {
		return err
	}
	path, err := f.parse(flags)
	if err != nil {
		return err
	}
	if *list && (*dryRun || selected != "") {
		return errors.New("--list cannot be combined with a preset or --dry-run")
	}
	ctx, cancel := defaultInterruptContext()
	defer cancel()
	topology, err := config.LoadWorkspaceTopology(ctx, path)
	if err != nil {
		return err
	}
	presets, err := topology.LaunchPresets()
	if err != nil {
		return err
	}
	names := []string{}
	for name := range presets {
		names = append(names, name)
	}
	sort.Strings(names)
	if *list {
		output["presets"] = names
		return nil
	}
	preset, ok := presets[selected]
	if !ok {
		return fmt.Errorf("unknown preset %q; available: %s", selected, strings.Join(names, ", "))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if *cwd == "" {
		*cwd, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	if !filepath.IsAbs(*cwd) {
		return errors.New("--cwd must be absolute")
	}
	*cwd, err = filepath.EvalSymlinks(*cwd)
	if err != nil {
		return err
	}
	root, err := workspace.CanonicalRoot(*cwd, home)
	if err != nil {
		return err
	}
	server := workspace.ServerContext{}
	if *socket == "" {
		server, err = workspace.ParseServerContext(os.Getenv("TMUX"))
		if err != nil {
			return errors.New("launch-agents requires an explicit TMUX context or --socket /absolute/path")
		}
		*socket = server.Socket
	} else {
		if !filepath.IsAbs(*socket) || filepath.Clean(*socket) != *socket || strings.ContainsAny(*socket, "\x00\r\n") {
			return errors.New("--socket must be an absolute clean path")
		}
		server.Socket = *socket
	}
	if *session == "" {
		*session = workspace.DefaultLaunchSession(root, selected)
	}
	// Session names use the same routing selector grammar as workspace_tree.
	for i, char := range *session {
		allowed := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || i > 0 && (char == '_' || char == '-')
		if !allowed {
			return errors.New("invalid dedicated session name")
		}
	}
	if len(*session) == 0 || len(*session) > 128 {
		return errors.New("session name must contain 1 to 128 characters")
	}
	plan := workspace.LaunchPlan{Preset: selected, Config: path, CWD: *cwd, Workspace: root, Socket: *socket, ExpectedPID: server.PID, Session: *session, Shell: os.Getenv("SHELL"), Columns: preset.Columns, Home: home, Topology: topology}
	if *dryRun {
		output["dry_run"] = true
		output["plan"] = plan
		return nil
	}
	state, err := workspace.DefaultStateRoot(home, os.Getenv("XDG_STATE_HOME"))
	if err != nil {
		return err
	}
	result, err := (workspace.Store{StateRoot: state}).LaunchAgents(ctx, plan)
	output["launch"] = result
	return err
}

// Permit the one preset name before or after flags, including --config passed
// by top-level dispatch. Values remain exact argv elements, never shell text.
func launchArguments(fs *flag.FlagSet, args []string) (string, []string, error) {
	selected := ""
	flags := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if i+2 != len(args) || selected != "" {
				return "", nil, errors.New("launch-agents takes one preset name")
			}
			selected = args[i+1]
			break
		}
		if !strings.HasPrefix(arg, "-") {
			if selected != "" {
				return "", nil, errors.New("launch-agents takes one preset name")
			}
			selected = arg
			continue
		}
		flags = append(flags, arg)
		name, _, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		option := fs.Lookup(name)
		if option == nil || hasValue {
			continue
		}
		boolean, ok := option.Value.(interface{ IsBoolFlag() bool })
		if !ok || !boolean.IsBoolFlag() {
			if i+1 >= len(args) {
				return "", nil, errors.New("missing value for --" + name)
			}
			i++
			flags = append(flags, args[i])
		}
	}
	return selected, flags, nil
}
