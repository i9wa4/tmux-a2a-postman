package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

func RunTimeline(args []string) error {
	return runTimeline(os.Stdout, args)
}

func runTimeline(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("timeline", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	contextID := fs.String("context-id", "", "context ID (optional, auto-resolve only when a live daemon owns the session)")
	sessionName := fs.String("session", "", "tmux session name (optional, auto-detect if in tmux)")
	configPath := fs.String("config", "", "config file path")
	limit := fs.Int("limit", 50, "maximum number of current-generation events to print (0 = all)")
	includeControlPlane := fs.Bool("include-control-plane", false, "include control-plane-only events in the timeline")
	if err := fs.Parse(args); err != nil {
		return err
	}

	target, ok, err := resolveReadOnlySessionTarget(*contextID, *sessionName, *configPath)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session name required: run inside tmux or pass --session")
	}
	if *limit < 0 {
		return fmt.Errorf("--limit must be >= 0")
	}

	sessionDir := filepath.Join(target.baseDir, target.contextID, target.sessionName)
	timeline, ok, err := projection.ProjectTimeline(sessionDir, projection.TimelineOptions{
		IncludeControlPlane: *includeControlPlane,
	})
	if err != nil {
		return fmt.Errorf("timeline: %w", err)
	}
	if !ok {
		return fmt.Errorf("no replayable timeline found")
	}

	entries := timeline.Entries
	if *limit > 0 && len(entries) > *limit {
		entries = entries[len(entries)-*limit:]
	}

	response := struct {
		ContextID   string                     `json:"context_id"`
		SessionName string                     `json:"session_name"`
		Limit       int                        `json:"limit"`
		Entries     []projection.TimelineEntry `json:"entries"`
	}{
		ContextID:   target.contextID,
		SessionName: target.sessionName,
		Limit:       *limit,
		Entries:     entries,
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(response)
}
