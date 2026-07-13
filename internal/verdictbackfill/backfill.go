package verdictbackfill

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
)

const (
	EventType = "verdict_event"
	Source    = "backfill"

	UnknownIdentity = "unknown"
)

type Row struct {
	EventType          string `json:"event_type"`
	EventID            string `json:"event_id"`
	Source             string `json:"source"`
	MessageID          string `json:"message_id"`
	ArchivePath        string `json:"archive_path"`
	FromNode           string `json:"from_node"`
	ToNode             string `json:"to_node"`
	Timestamp          string `json:"timestamp"`
	ThreadID           string `json:"thread_id,omitempty"`
	ReplyTo            string `json:"reply_to,omitempty"`
	VerdictOf          string `json:"verdict_of,omitempty"`
	Marker             string `json:"marker"`
	Verdict            string `json:"verdict"`
	Evidence           string `json:"evidence"`
	Model              string `json:"model"`
	InstructionVersion string `json:"instruction_version"`
	RuntimeContextID   string `json:"runtime_context_id"`
}

type Options struct {
	SessionDir string
	ArchiveDir string
}

func Collect(opts Options) ([]Row, error) {
	archiveDir, err := resolveArchiveDir(opts)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		return nil, fmt.Errorf("reading archive dir: %w", err)
	}

	rows := make([]Row, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		path := filepath.Join(archiveDir, entry.Name())
		row, ok, err := rowFromArchive(path)
		if err != nil {
			return nil, err
		}
		if ok {
			rows = append(rows, row)
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Timestamp != rows[j].Timestamp {
			return rows[i].Timestamp < rows[j].Timestamp
		}
		return rows[i].MessageID < rows[j].MessageID
	})
	return rows, nil
}

func resolveArchiveDir(opts Options) (string, error) {
	switch {
	case opts.ArchiveDir != "":
		return filepath.Clean(opts.ArchiveDir), nil
	case opts.SessionDir != "":
		return filepath.Join(filepath.Clean(opts.SessionDir), "read"), nil
	default:
		return "", fmt.Errorf("either session dir or archive dir is required")
	}
}

func rowFromArchive(path string) (Row, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Row{}, false, fmt.Errorf("reading archive %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return Row{}, false, fmt.Errorf("stat archive %s: %w", path, err)
	}
	if info.Mode().Type()&fs.ModeType != 0 {
		return Row{}, false, nil
	}

	content := string(data)
	metadata, err := envelope.ParseMetadata(content)
	if err != nil {
		return Row{}, false, fmt.Errorf("parsing archive %s: %w", path, err)
	}
	marker, verdict, ok := markerFromBody(metadata.Body)
	if !ok {
		return Row{}, false, nil
	}

	messageID := metadata.MessageID
	if messageID == "" {
		messageID = filepath.Base(path)
	}
	row := Row{
		EventType:          EventType,
		Source:             Source,
		MessageID:          messageID,
		ArchivePath:        filepath.Clean(path),
		FromNode:           metadata.From,
		ToNode:             metadata.To,
		Timestamp:          metadata.Timestamp,
		ThreadID:           metadata.ThreadID,
		ReplyTo:            metadata.ReplyTo,
		VerdictOf:          metadata.VerdictOf,
		Marker:             marker,
		Verdict:            verdict,
		Evidence:           "",
		Model:              UnknownIdentity,
		InstructionVersion: UnknownIdentity,
		RuntimeContextID:   UnknownIdentity,
	}
	row.EventID = deterministicEventID(row)
	return row, true, nil
}

func markerFromBody(body string) (marker, verdict string, ok bool) {
	firstLine := body
	if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
		firstLine = firstLine[:idx]
	}
	firstLine = strings.TrimSpace(firstLine)

	switch {
	case strings.HasPrefix(firstLine, "APPROVED:"):
		return "PASS", "pass", true
	case strings.HasPrefix(firstLine, "PASS:"):
		return "PASS", "pass", true
	case firstLine == "PASS":
		return "PASS", "pass", true
	case strings.HasPrefix(firstLine, "DONE:"):
		return "DONE", "done", true
	case firstLine == "DONE":
		return "DONE", "done", true
	case strings.HasPrefix(firstLine, "BLOCKED:"):
		return "BLOCKED", "blocked", true
	case firstLine == "BLOCKED":
		return "BLOCKED", "blocked", true
	case strings.HasPrefix(firstLine, "NOT APPROVED:"):
		return "NOT APPROVED", "not_approved", true
	case firstLine == "NOT APPROVED":
		return "NOT APPROVED", "not_approved", true
	default:
		return "", "", false
	}
}

func deterministicEventID(row Row) string {
	material := strings.Join([]string{
		row.Source,
		row.MessageID,
		row.ArchivePath,
		row.FromNode,
		row.ToNode,
		row.Timestamp,
		row.Marker,
	}, "\x00")
	sum := sha256.Sum256([]byte(material))
	return "verdict-backfill-" + hex.EncodeToString(sum[:16])
}
