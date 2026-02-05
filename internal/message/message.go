package message

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
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

// DeliverMessage moves a message from post/ to the recipient's inbox/ or dead-letter/.
// Multi-session support: postPath is the full path to the message file in post/ directory.
// The message will be delivered to the recipient's session directory based on NodeInfo.SessionDir.
// Routing rules (DEFAULT DENY):
// - sender="postman" is always allowed
// - otherwise, sender->recipient edge must exist in adjacency map
func DeliverMessage(postPath string, contextID string, knownNodes map[string]discovery.NodeInfo, adjacency map[string][]string, cfg *config.Config) error {
	// Extract filename from postPath
	filename := filepath.Base(postPath)

	// Extract source session directory from postPath
	// postPath format: /path/to/context-id/session-name/post/message.md
	sourceSessionDir := filepath.Dir(filepath.Dir(postPath))

	// Check if file still exists (handles duplicate fsnotify event)
	if _, err := os.Stat(postPath); os.IsNotExist(err) {
		return nil // Already processed
	}

	info, err := ParseMessageFilename(filename)
	if err != nil {
		// Parse error: move to dead-letter/ in source session
		dst := filepath.Join(sourceSessionDir, "dead-letter", filename)
		return os.Rename(postPath, dst)
	}

	// PONG handling: messages to "postman" are PONG responses
	// Move directly to read/ in source session (skip inbox delivery)
	if info.To == "postman" {
		dst := filepath.Join(sourceSessionDir, "read", filename)
		if err := os.Rename(postPath, dst); err != nil {
			return fmt.Errorf("moving PONG to read: %w", err)
		}
		fmt.Printf("postman: PONG received from %s\n", info.From)
		return nil
	}

	// Check if recipient exists
	nodeInfo, found := knownNodes[info.To]
	if !found {
		// Unknown recipient: move to dead-letter/ in source session
		dst := filepath.Join(sourceSessionDir, "dead-letter", filename)
		return os.Rename(postPath, dst)
	}
	paneID := nodeInfo.PaneID

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
			// Routing denied: move to dead-letter/ in source session
			dst := filepath.Join(sourceSessionDir, "dead-letter", filename)
			fmt.Printf("📨 postman: routing denied %s -> %s (moved to dead-letter/)\n", info.From, info.To)
			return os.Rename(postPath, dst)
		}
	}

	// Ensure recipient inbox subdirectory exists (in recipient's session directory)
	recipientSessionDir := nodeInfo.SessionDir
	recipientInbox := filepath.Join(recipientSessionDir, "inbox", info.To)
	if err := os.MkdirAll(recipientInbox, 0o755); err != nil {
		return fmt.Errorf("creating recipient inbox: %w", err)
	}

	dst := filepath.Join(recipientInbox, filename)
	if err := os.Rename(postPath, dst); err != nil {
		return fmt.Errorf("moving to inbox: %w", err)
	}

	// Send tmux notification to the recipient pane
	notificationMsg := notification.BuildNotification(cfg, adjacency, knownNodes, contextID, info.To, info.From, postPath)
	enterDelay := time.Duration(cfg.EnterDelay * float64(time.Second))
	tmuxTimeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
	_ = notification.SendToPane(paneID, notificationMsg, enterDelay, tmuxTimeout)
	// NOTE: Error already logged by SendToPane (WARNING level)
	// Continue with delivery (notification failure does not fail delivery)

	// Update activity timestamps for idle detection
	idle.UpdateActivity(info.From)
	idle.UpdateActivity(info.To)

	fmt.Printf("📬 postman: delivered %s -> %s\n", filename, info.To)
	return nil
}

// ScanInboxMessages scans the inbox directory and returns a list of MessageInfo.
func ScanInboxMessages(inboxPath string) []MessageInfo {
	var messages []MessageInfo

	entries, err := os.ReadDir(inboxPath)
	if err != nil {
		return messages
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		info, err := ParseMessageFilename(entry.Name())
		if err != nil {
			continue
		}
		messages = append(messages, *info)
	}

	return messages
}
