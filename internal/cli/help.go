package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/i9wa4/tmux-a2a-postman/internal/helpdocs"
)

func RunHelp(args []string) {
	if err := WriteHelp(os.Stdout, os.Stderr, args); err != nil {
		os.Exit(1)
	}
}

func WriteHelp(stdout, stderr io.Writer, args []string) error {
	return runHelp(stdout, stderr, args)
}

func runHelp(stdout, stderr io.Writer, args []string) error {
	if len(args) > 2 {
		_, _ = fmt.Fprintln(stderr, "usage: tmux-a2a-postman help [topic [reference]]")
		return fmt.Errorf("usage: tmux-a2a-postman help [topic [reference]]")
	}
	topic := ""
	if len(args) > 0 {
		topic = args[0]
	}

	data, err := helpdocs.Read(topic, args[min(1, len(args)):]...)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		_, _ = fmt.Fprintln(stderr, "")
		_, _ = fmt.Fprintln(stderr, "Available topics:")
		for _, t := range sortedHelpTopics() {
			_, _ = fmt.Fprintf(stderr, "  %s\n", t)
		}
		return err
	}
	_, err = stdout.Write(data)
	if err == nil && (topic == "" || topic == "overview" || topic == "help") && len(args) < 2 {
		_, err = fmt.Fprint(stdout, "\n## Available HelpDoc topics\n\n")
		for _, name := range sortedHelpTopics() {
			if err != nil {
				break
			}
			_, err = fmt.Fprintf(stdout, "- `tmux-a2a-postman help %s`\n", name)
		}
	}
	return err
}

func sortedHelpTopics() []string {
	return helpdocs.Topics()
}

func isSubcommandHelpRequest(args []string) bool {
	if len(args) != 1 {
		return false
	}
	switch args[0] {
	case "--help", "-h":
		return true
	default:
		return false
	}
}

func hasCommandHelpTopic(command string) bool {
	if command == "" {
		return false
	}
	for _, topic := range helpdocs.Topics() {
		if command == topic {
			return true
		}
	}
	return false
}
