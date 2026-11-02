package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/diagnostic"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

// workspaceCommand is deliberately limited to the file-first commands.
// All identity and routing inputs are rechecked by the workspace operations.
type workspaceCommand struct {
	trace        *diagnostic.Recorder
	topologyPath string
	source       config.Source
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
	caller workspace.Binding
	root   string
}

func (r roleBoundRuntime) Snapshot(ctx context.Context) (workspace.Snapshot, error) {
	s, err := r.Runtime.Snapshot(ctx)
	if err != nil {
		return s, err
	}
	if s.Caller != r.caller || s.Root != r.root {
		return workspace.Snapshot{}, workspace.ErrUnknown
	}
	return s, nil
}

func (c workspaceCommand) boundRuntime() workspace.Runtime {
	return roleBoundRuntime{Runtime: c.runtime, caller: c.caller.Caller, root: c.caller.Root}
}

func (c workspaceCommand) namedCaller(ctx context.Context) (workspace.NamedRuntime, error) {
	caller, err := c.runtime.NamedCaller(ctx)
	if err != nil {
		return workspace.NamedRuntime{}, err
	}
	snapshot, err := caller.Snapshot(ctx)
	if err != nil || snapshot.Root != c.caller.Root || snapshot.Caller != c.caller.Caller {
		return workspace.NamedRuntime{}, workspace.ErrUnknown
	}
	return caller, nil
}

type workspaceFlagSet struct {
	fs   *flag.FlagSet
	path *string
}

func newWorkspaceFlags(name string) workspaceFlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("config", "", "absolute path to postman.md")
	return workspaceFlagSet{fs: fs, path: path}
}

func (f workspaceFlagSet) parse(args []string) (string, error) {
	fs, path := f.fs, f.path
	if err := ValidateFlagDuplicates(fs, args); err != nil {
		return "", err
	}
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	provided := false
	fs.Visit(func(option *flag.Flag) {
		if option.Name == "config" {
			provided = true
		}
	})
	if provided && *path == "" {
		return "", errors.New("--config requires an absolute file path")
	}
	if *path == "" {
		*path = os.Getenv("TMUX_A2A_POSTMAN_CONFIG")
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

func (f workspaceFlagSet) source(args []string) (config.Source, error) {
	path, err := f.parse(args)
	if err != nil {
		return config.Source{}, err
	}
	explicit := os.Getenv("TMUX_A2A_POSTMAN_CONFIG") != ""
	f.fs.Visit(func(option *flag.Flag) {
		if option.Name == "config" {
			explicit = true
		}
	})
	source := config.Source{Path: path}
	if !explicit {
		_, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			source.Builtin = true
		} else if err != nil {
			return source, err
		}
	}
	return source, nil
}

func openWorkspaceCommand(ctx context.Context, source config.Source) (workspaceCommand, error) {
	var c workspaceCommand
	configSpan := diagnostic.From(ctx).Start("configuration")
	topology, err := source.Load(ctx)
	configSpan.Error(err)
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
	server, contextErr := workspace.ParseServerContext(os.Getenv("TMUX"))
	if contextErr != nil {
		return c, errors.New("TMUX must contain an absolute native tmux socket")
	}
	rt := workspace.Tmux{Socket: server.Socket, ConfigPath: source.Path, BuiltinConfig: source.Builtin, ConfigFingerprint: workspace.StatusConfigFingerprint(topology), Pane: os.Getenv("TMUX_PANE"), Home: home, Roles: topology.Nodes}
	identitySpan := diagnostic.From(ctx).Start("identity")
	snapshot, err := rt.Snapshot(ctx)
	identitySpan.Error(err)
	if err != nil {
		return c, err
	}
	if server.PID != 0 && !strings.HasPrefix(snapshot.Caller.Owner.Incarnation, fmt.Sprintf("%d:", server.PID)) {
		return c, errors.New("explicit server PID disagrees with native caller")
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
	return workspaceCommand{topologyPath: source.Path, source: source, topology: topology, runtime: rt, mail: workspace.Mail{Store: workspace.Store{StateRoot: root}}, caller: snapshot}, nil
}

func (c workspaceCommand) permits(to string) bool {
	for _, peer := range c.topology.Edges[c.caller.Caller.Role] {
		if peer == to {
			return true
		}
	}
	return false
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
		trace := diagnostic.From(ctx)
		waitStart := trace.Now()
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			trace.Measure("admission_wait", waitStart)
			return ctx.Err()
		case <-timer.C:
		}
		trace.Measure("admission_wait", waitStart)
	}
}

func ValidateFlagDuplicates(fs *flag.FlagSet, args []string) error {
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "-") {
			break
		}
		name, _, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		option := fs.Lookup(name)
		if option == nil {
			continue
		}
		if seen[name] {
			return errors.New("duplicate option: --" + name)
		}
		seen[name] = true
		boolean, ok := option.Value.(interface{ IsBoolFlag() bool })
		if !hasValue && (!ok || !boolean.IsBoolFlag()) {
			i++
		}
	}
	return nil
}
