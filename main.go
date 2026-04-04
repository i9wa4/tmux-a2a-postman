package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/i9wa4/tmux-a2a-postman/internal/bindcmd"
	"github.com/i9wa4/tmux-a2a-postman/internal/cli"
	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/version"
)

// runGetContextID prints the live context ID for the current tmux session.
// Issue #249: zero-argument discovery primitive for AI agents.
func runGetContextID(args []string) error {
	fs := flag.NewFlagSet("get-context-id", flag.ContinueOnError)
	// Options struct fields (--params scope): json
	// SYNC: schema get-context-id properties; alwaysExcludedParams map
	jsonOut := fs.Bool("json", false, `output json: {"context_id":"..."}`)
	paramsFlag := fs.String("params", "", "command parameters as JSON or shorthand (k=v,k=v)")
	// NOTE: always-excluded from --params scope (SYNC: alwaysExcludedParams map)
	sessionFlag := fs.String("session", "", "tmux session name (optional, auto-detect if in tmux)")
	configPath := fs.String("config", "", "path to config file (optional)")
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

	return cli.RunGetContextID(os.Stdout, *sessionFlag, *configPath, *jsonOut)
}

func main() {
	// Top-level flags
	fs := flag.NewFlagSet("postman", flag.ContinueOnError)
	showVersion := fs.Bool("version", false, "show version")
	showHelp := fs.Bool("help", false, "show help")
	noTUI := fs.Bool("no-tui", false, "run without TUI")
	contextID := fs.String("context-id", "", "context ID (auto-generated if not specified)")
	configPath := fs.String("config", "", "path to config file (auto-detect from XDG_CONFIG_HOME if not specified)")
	logFilePath := fs.String("log-file", "", "log file path (defaults to $XDG_STATE_HOME/tmux-a2a-postman/{contextID}/postman.log)")
	globalBaseDir := fs.String("base-dir", "", "override state directory (sets POSTMAN_HOME)")
	globalStateHome := fs.String("state-home", "", "override XDG_STATE_HOME")

	fs.Usage = func() {
		printUsage(os.Stderr, fs)
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			return
		}
		os.Exit(1)
	}

	// Sub-feature (d): inject flag values into env before subcommand dispatch
	// so all config.ResolveBaseDir / config.ResolveXDGStateHome call sites pick them up.
	if *globalBaseDir != "" {
		os.Setenv("POSTMAN_HOME", *globalBaseDir)
	}
	if *globalStateHome != "" {
		os.Setenv("XDG_STATE_HOME", *globalStateHome)
	}

	if *showVersion {
		fmt.Printf("tmux-a2a-postman %s\n", version.Version)
		return
	}

	if *showHelp {
		cli.RunHelp([]string{})
		return
	}

	// Determine command (default: start)
	command := "start"
	args := fs.Args()
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}

	result := cli.Dispatch(
		command,
		args,
		cli.Config{
			ContextID:   *contextID,
			ConfigPath:  *configPath,
			LogFilePath: *logFilePath,
			NoTUI:       *noTUI,
		},
		cli.Handlers{
			Start:                   cli.RunStartWithFlags,
			GetSessionStatusOneline: func(args []string) error { return cli.RunGetSessionStatusOneline(os.Stdout, args) },
			Read:                    cli.RunRead,
			Pop:                     cli.RunPop,
			GetSessionHealth:        cli.RunGetSessionHealth,
			GetContextID:            runGetContextID,
			SupervisorDrain:         cli.RunSupervisorDrain,
			SendMessage:             cli.RunSendMessage,
			Stop: func(args []string) error {
				return cli.RunStop(os.Stdout, args)
			},
			Bind:   bindcmd.Run,
			Schema: cli.RunSchema,
			Help:   cli.RunHelp,
		},
	)
	if result.Err != nil {
		fmt.Fprintf(os.Stderr, "❌ %s: %v\n", result.Label, result.Err)
		if result.ShowUsage {
			fs.Usage()
		}
		os.Exit(1)
	}
	if result.ShowUsage {
		fs.Usage()
	}
}

func printUsage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprintln(w, "Usage: tmux-a2a-postman [options] [command]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Options:")
	cliutil.PrintDoubleDashDefaults(fs)
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Default operator surface:")
	fmt.Fprintln(w, "  send                       Send a message in one step (--to and --body required)")
	fmt.Fprintln(w, "  pop                        Read and archive the oldest unread inbox message")
	fmt.Fprintln(w, "  bind                       Manage sidecar bindings")
	fmt.Fprintln(w, "  get-health                 Print the canonical JSON session-health payload")
	fmt.Fprintln(w, "  get-health-oneline         Print a one-line formatter over get-health")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Lifecycle and recovery:")
	fmt.Fprintln(w, "  start                      Start tmux-a2a-postman daemon (default)")
	fmt.Fprintln(w, "  stop                       Stop the running daemon for this tmux session")
	fmt.Fprintln(w, "  get-context-id             Print live context ID for current tmux session")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Additional tools:")
	fmt.Fprintln(w, "  read                       List inbox messages or access archived/dead-letter messages")
	fmt.Fprintln(w, "  supervisor-drain           Phase 3→2 rollback: annotate pending records and drain supervisor dead-letters")
	fmt.Fprintln(w, "  schema [command]           Print JSON Schema for config or supported command surfaces")
	fmt.Fprintln(w, "  help [topic]               Show help overview or topic-based help")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Global Flags:")
	fmt.Fprintln(w, "  --base-dir <path>          Override state directory (sets POSTMAN_HOME)")
	fmt.Fprintln(w, "  --state-home <path>        Override XDG_STATE_HOME")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintln(w, "  tmux-a2a-postman start                               # Start daemon")
	fmt.Fprintln(w, "  tmux-a2a-postman send --to worker --body \"DONE\"          # Send message")
	fmt.Fprintln(w, "  tmux-a2a-postman pop --json                          # Read next message as JSON")
	fmt.Fprintln(w, "  tmux-a2a-postman schema send                         # Show send JSON Schema")
	fmt.Fprintln(w, "  tmux-a2a-postman --base-dir /tmp/test read           # Override state directory")
	fmt.Fprintln(w, "  tmux-a2a-postman help messaging                      # Messaging guide")
}
