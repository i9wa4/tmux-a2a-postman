package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/i9wa4/tmux-a2a-postman/internal/verdictbackfill"
)

func RunBackfillVerdictEvents(args []string) error {
	return runBackfillVerdictEvents(os.Stdout, args)
}

func runBackfillVerdictEvents(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("backfill-verdict-events", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sessionDir := fs.String("session-dir", "", "session directory containing read/")
	archiveDir := fs.String("archive-dir", "", "archive read directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}

	rows, err := verdictbackfill.Collect(verdictbackfill.Options{
		SessionDir: *sessionDir,
		ArchiveDir: *archiveDir,
	})
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(stdout)
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return fmt.Errorf("encoding verdict event: %w", err)
		}
	}
	return nil
}
