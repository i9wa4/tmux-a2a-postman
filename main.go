package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/i9wa4/tmux-a2a-postman/internal/bindcmd"
	"github.com/i9wa4/tmux-a2a-postman/internal/version"
)

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
		fmt.Fprintln(os.Stderr, "Usage: tmux-a2a-postman [options] [command]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Options:")
		printDoubleDashDefaults(fs)
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  start                      Start tmux-a2a-postman daemon (default)")
		fmt.Fprintln(os.Stderr, "  stop                       Stop the running daemon for this tmux session")
		fmt.Fprintln(os.Stderr, "  send-message               Send a message in one step (--to and --body required)")
		fmt.Fprintln(os.Stderr, "  pop                        Read and archive the oldest unread inbox message")
		fmt.Fprintln(os.Stderr, "  read                       List inbox messages or access archived/dead-letter messages")
		fmt.Fprintln(os.Stderr, "  get-context-id             Print live context ID for current tmux session")
		fmt.Fprintln(os.Stderr, "  supervisor-drain           Phase 3→2 rollback: annotate pending records and drain supervisor dead-letters")
		fmt.Fprintln(os.Stderr, "  get-session-status-oneline Show all sessions' pane status in one line")
		fmt.Fprintln(os.Stderr, "  get-session-health         Print session health per node")
		fmt.Fprintln(os.Stderr, "  schema [command]           Print JSON Schema for config or command options")
		fmt.Fprintln(os.Stderr, "  help [topic]               Show help overview or topic-based help")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Global Flags:")
		fmt.Fprintln(os.Stderr, "  --base-dir <path>          Override state directory (sets POSTMAN_HOME)")
		fmt.Fprintln(os.Stderr, "  --state-home <path>        Override XDG_STATE_HOME")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman start                               # Start daemon")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman send-message --to worker --body \"DONE\"  # Send message")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman pop --json                          # Read next message as JSON")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman schema send-message                 # Show send-message JSON Schema")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman --base-dir /tmp/test read           # Override state directory")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman help messaging                      # Messaging guide")
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
		runHelp([]string{})
		return
	}

	// Determine command (default: start)
	command := "start"
	args := fs.Args()
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}

	// Issue #315: forward global --context-id to subcommands that accept it.
	// Prepending ensures subcommand-level --context-id takes precedence (last-wins).
	prependContextID := func(a []string) []string {
		if *contextID == "" {
			return a
		}
		return append([]string{"--context-id", *contextID}, a...)
	}
	// Forward global --config to subcommands that accept it (BLOCKING #5).
	prependConfig := func(a []string) []string {
		if *configPath == "" {
			return a
		}
		return append([]string{"--config", *configPath}, a...)
	}

	switch command {
	case "start":
		if err := runStartWithFlags(*contextID, *configPath, *logFilePath, *noTUI); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman start: %v\n", err)
			os.Exit(1)
		}
	case "get-session-status-oneline":
		if err := runGetSessionStatusOneline(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman get-session-status-oneline: %v\n", err)
			os.Exit(1)
		}
	case "read":
		if err := runRead(prependConfig(prependContextID(args))); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman read: %v\n", err)
			os.Exit(1)
		}
	case "pop":
		if err := runPop(prependConfig(prependContextID(args))); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman pop: %v\n", err)
			os.Exit(1)
		}
	case "get-session-health":
		if err := runGetSessionHealth(prependConfig(prependContextID(args))); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman get-session-health: %v\n", err)
			os.Exit(1)
		}
	case "get-context-id":
		if err := runGetContextID(prependConfig(args)); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman get-context-id: %v\n", err)
			os.Exit(1)
		}
	case "supervisor-drain":
		if err := runSupervisorDrain(prependConfig(prependContextID(args))); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman supervisor-drain: %v\n", err)
			os.Exit(1)
		}
	case "send-message":
		if err := runSendMessage(prependConfig(prependContextID(args))); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman send-message: %v\n", err)
			os.Exit(1)
		}
	case "stop":
		if err := runStop(prependConfig(args)); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman stop: %v\n", err)
			os.Exit(1)
		}
	case "bind":
		if err := bindcmd.Run(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman bind: %v\n", err)
			os.Exit(1)
		}
	case "schema":
		if err := runSchema(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman schema: %v\n", err)
			os.Exit(1)
		}
	case "help":
		runHelp(args)
	default:
		fmt.Fprintf(os.Stderr, "❌ postman: unknown command %q\n", command)
		fs.Usage()
		os.Exit(1)
	}
}
