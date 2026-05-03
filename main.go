package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/i9wa4/tmux-a2a-postman/internal/cli"
	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
)

func splitCommand(args []string) (string, []string, bool) {
	if len(args) == 0 {
		return "", nil, false
	}
	return args[0], args[1:], true
}

func main() {
	// Top-level flags
	fs := flag.NewFlagSet("postman", flag.ContinueOnError)
	showVersion := fs.Bool("version", false, "show version")
	showHelp := fs.Bool("help", false, "show help")
	contextID := fs.String("context-id", "", "context ID (auto-generated if not specified)")
	configPath := fs.String("config", "", "path to config file (auto-detect from XDG_CONFIG_HOME if not specified)")

	fs.Usage = func() {
		printUsage(os.Stderr, fs)
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			return
		}
		os.Exit(1)
	}

	if *showVersion {
		_ = cli.RunVersion(os.Stdout, nil)
		return
	}

	if *showHelp {
		cli.RunHelp([]string{})
		return
	}

	command, args, ok := splitCommand(fs.Args())
	if !ok {
		fs.Usage()
		return
	}

	result := cli.Dispatch(
		command,
		args,
		cli.Config{
			ContextID:   *contextID,
			ConfigPath:  *configPath,
			LogFilePath: "",
			NoTUI:       false,
		},
		cli.Handlers{
			Start:                   cli.RunStartWithFlags,
			Pop:                     cli.RunPop,
			GetSessionHealth:        cli.RunGetSessionHealth,
			GetSessionStatusOneline: func(args []string) error { return cli.RunGetSessionStatusOneline(os.Stdout, args) },
			SendMessage:             cli.RunSendMessage,
			Stop: func(args []string) error {
				return cli.RunStop(os.Stdout, args)
			},
			Version: func(args []string) error {
				return cli.RunVersion(os.Stdout, args)
			},
			Help: cli.RunHelp,
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
	fmt.Fprintln(w, "Usage: tmux-a2a-postman [options] <command>")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Use an explicit subcommand; bare `tmux-a2a-postman` prints usage.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Options:")
	cliutil.PrintDoubleDashDefaultsExcept(w, fs, map[string]bool{"context-id": true})
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Default operator surface:")
	fmt.Fprintln(w, "  send                       Send a message in one step (--to and --body required)")
	fmt.Fprintln(w, "  pop                        Read and archive the oldest unread inbox message")
	fmt.Fprintln(w, "  get-health                 Print canonical session health JSON")
	fmt.Fprintln(w, "  get-health-oneline         Print compact all-session health")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Lifecycle and recovery:")
	fmt.Fprintln(w, "  start                      Start tmux-a2a-postman daemon")
	fmt.Fprintln(w, "  stop                       Stop the running daemon for this tmux session")
	fmt.Fprintln(w, "  version                    Print the build version JSON")
	fmt.Fprintln(w, "  Legacy and diagnostic helpers are internal, not CLI commands.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintln(w, "  tmux-a2a-postman start                               # Start daemon")
	fmt.Fprintln(w, "  tmux-a2a-postman send --to worker --body \"DONE\"          # Send message")
	fmt.Fprintln(w, "  tmux-a2a-postman pop                                 # Read next message as JSON")
	fmt.Fprintln(w, "  tmux-a2a-postman get-health                          # Inspect runtime health as JSON")
	fmt.Fprintln(w, "  tmux-a2a-postman get-health-oneline                  # Inspect compact health")
	fmt.Fprintln(w, "  tmux-a2a-postman version                             # Print version as JSON")
	fmt.Fprintln(w, "  tmux-a2a-postman help messaging                      # Messaging guide")
}
