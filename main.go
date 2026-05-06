package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/i9wa4/tmux-a2a-postman/internal/cli"
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
		printUsage(os.Stderr)
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
		printUsage(os.Stdout)
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
			InspectInput:            cli.RunInspectInput,
			SendMessage:             cli.RunSendMessage,
			SendHeredoc:             cli.RunSendHeredoc,
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

func printUsage(w io.Writer) {
	if err := cli.WriteHelp(w, w, nil); err != nil {
		fmt.Fprintf(w, "failed to render help: %v\n", err)
	}
}
