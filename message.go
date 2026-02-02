package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MessageInfo holds parsed information from a message filename.
type MessageInfo struct {
	Timestamp string
	From      string
	To        string
}

// ParseMessageFilename parses a message filename in the format:
// {timestamp}-from-{sender}-to-{recipient}.md
// Example: 20260201-022121-from-orchestrator-to-worker.md
func ParseMessageFilename(filename string) (*MessageInfo, error) {
	// Remove .md extension
	if !strings.HasSuffix(filename, ".md") {
		return nil, fmt.Errorf("invalid filename: missing .md extension: %q", filename)
	}
	base := strings.TrimSuffix(filename, ".md")

	// Find "-from-" and "-to-" markers
	fromIdx := strings.Index(base, "-from-")
	if fromIdx < 0 {
		return nil, fmt.Errorf("invalid filename: missing '-from-' marker: %q", filename)
	}

	rest := base[fromIdx+len("-from-"):]
	toIdx := strings.Index(rest, "-to-")
	if toIdx < 0 {
		return nil, fmt.Errorf("invalid filename: missing '-to-' marker: %q", filename)
	}

	timestamp := base[:fromIdx]
	from := rest[:toIdx]
	to := rest[toIdx+len("-to-"):]

	if timestamp == "" || from == "" || to == "" {
		return nil, fmt.Errorf("invalid filename: empty field in %q", filename)
	}

	return &MessageInfo{
		Timestamp: timestamp,
		From:      from,
		To:        to,
	}, nil
}

// deliverMessage moves a message from post/ to the recipient's inbox/ or dead-letter/.
// Routing rules (DEFAULT DENY):
// - sender="postman" is always allowed
// - otherwise, sender->recipient edge must exist in adjacency map
func deliverMessage(sessionDir string, filename string, knownNodes map[string]string, adjacency map[string][]string) error {
	postPath := filepath.Join(sessionDir, "post", filename)

	info, err := ParseMessageFilename(filename)
	if err != nil {
		// Parse error: move to dead-letter/
		dst := filepath.Join(sessionDir, "dead-letter", filename)
		return os.Rename(postPath, dst)
	}

	// PONG handling: messages to "postman" are PONG responses
	// Move directly to read/ (skip inbox delivery)
	if info.To == "postman" {
		dst := filepath.Join(sessionDir, "read", filename)
		if err := os.Rename(postPath, dst); err != nil {
			return fmt.Errorf("moving PONG to read: %w", err)
		}
		fmt.Printf("postman: PONG received from %s\n", info.From)
		return nil
	}

	// Check if recipient exists
	paneID, found := knownNodes[info.To]
	if !found {
		// Unknown recipient: move to dead-letter/
		dst := filepath.Join(sessionDir, "dead-letter", filename)
		return os.Rename(postPath, dst)
	}

	// Check routing permissions (DEFAULT DENY)
	// IMPORTANT: sender="postman" is always allowed
	if info.From != "postman" {
		allowed := false
		if neighbors, ok := adjacency[info.From]; ok {
			for _, neighbor := range neighbors {
				if neighbor == info.To {
					allowed = true
					break
				}
			}
		}
		if !allowed {
			// Routing denied: move to dead-letter/
			dst := filepath.Join(sessionDir, "dead-letter", filename)
			fmt.Printf("postman: routing denied %s -> %s (moved to dead-letter/)\n", info.From, info.To)
			return os.Rename(postPath, dst)
		}
	}

	// Ensure recipient inbox subdirectory exists
	recipientInbox := filepath.Join(sessionDir, "inbox", info.To)
	if err := os.MkdirAll(recipientInbox, 0o755); err != nil {
		return fmt.Errorf("creating recipient inbox: %w", err)
	}

	dst := filepath.Join(recipientInbox, filename)
	if err := os.Rename(postPath, dst); err != nil {
		return fmt.Errorf("moving to inbox: %w", err)
	}

	// Send tmux notification to the recipient pane
	if err := notifyNode(paneID, info.From); err != nil {
		fmt.Fprintf(os.Stderr, "postman: notify %s: %v\n", info.To, err)
	}

	fmt.Printf("postman: delivered %s -> %s\n", filename, info.To)
	return nil
}

// notifyNode sends a non-intrusive tmux display-message to the target pane.
func notifyNode(paneID string, sender string) error {
	msg := fmt.Sprintf("Message from %s", sender)
	return exec.Command("tmux", "display-message", "-t", paneID, msg).Run()
}
