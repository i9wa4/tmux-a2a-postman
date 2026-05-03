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
	"":                   "helptext/overview.txt",
	"commands":           "helptext/commands.txt",
	"config":             "helptext/config.txt",
	"directories":        "helptext/directories.txt",
	"get-health":         "helptext/get-health.txt",
	"get-health-oneline": "helptext/get-health-oneline.txt",
	"help":               "helptext/help.txt",
	"messaging":          "helptext/messaging.txt",
	"pop":                "helptext/pop.txt",
	"send":               "helptext/send.txt",
	"start":              "helptext/start.txt",
	"stop":               "helptext/stop.txt",
	"version":            "helptext/version.txt",
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
		fmt.Fprintf(stderr, "unknown help topic: %q\n", topic)
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Available topics:")
		for _, t := range sortedHelpTopics() {
			fmt.Fprintf(stderr, "  %s\n", t)
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
