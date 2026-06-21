package cli

import (
	"embed"
	"fmt"
	"io"
	"os"
	"sort"
)

//go:embed helptext/*.txt
var helpTextFS embed.FS

var helpTopicFiles = map[string]string{
	"":                      "helptext/overview.txt",
	"capture-profile":       "helptext/capture-profile.txt",
	"commands":              "helptext/commands.txt",
	"config":                "helptext/config.txt",
	"directories":           "helptext/directories.txt",
	"get-status":            "helptext/get-status.txt",
	"get-status-oneline":    "helptext/get-status-oneline.txt",
	"help":                  "helptext/help.txt",
	"inspect-input":         "helptext/inspect-input.txt",
	"inspect-daemon-submit": "helptext/inspect-daemon-submit.txt",
	"inspect-message":       "helptext/inspect-message.txt",
	"messaging":             "helptext/messaging.txt",
	"pop":                   "helptext/pop.txt",
	"send":                  "helptext/send.txt",
	"send-heredoc":          "helptext/send-heredoc.txt",
	"start":                 "helptext/start.txt",
	"stop":                  "helptext/stop.txt",
	"version":               "helptext/version.txt",
}

func RunHelp(args []string) {
	if err := WriteHelp(os.Stdout, os.Stderr, args); err != nil {
		os.Exit(1)
	}
}

func WriteHelp(stdout, stderr io.Writer, args []string) error {
	return runHelp(stdout, stderr, args)
}

func runHelp(stdout, stderr io.Writer, args []string) error {
	topic := ""
	if len(args) > 0 {
		topic = args[0]
	}

	path, ok := helpTopicFiles[topic]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "unknown help topic: %q\n", topic)
		_, _ = fmt.Fprintln(stderr, "")
		_, _ = fmt.Fprintln(stderr, "Available topics:")
		for _, t := range sortedHelpTopics() {
			_, _ = fmt.Fprintf(stderr, "  %s\n", t)
		}
		return fmt.Errorf("unknown help topic: %q", topic)
	}

	data, err := helpTextFS.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading help topic %q: %w", topic, err)
	}
	_, err = stdout.Write(data)
	return err
}

func sortedHelpTopics() []string {
	topics := make([]string, 0, len(helpTopicFiles)-1)
	for topic := range helpTopicFiles {
		if topic == "" {
			continue
		}
		topics = append(topics, topic)
	}
	sort.Strings(topics)
	return topics
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
	_, ok := helpTopicFiles[command]
	return ok && command != ""
}
