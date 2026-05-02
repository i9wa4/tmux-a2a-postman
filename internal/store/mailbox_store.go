package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DeadLetterPath builds the dead-letter destination path with a reason suffix.
func DeadLetterPath(sessionDir, filename, suffix string) string {
	base := strings.TrimSuffix(filename, ".md")
	return filepath.Join(sessionDir, "dead-letter", base+suffix+".md")
}

// ValidateDeadLetterTarget rejects symlinked dead-letter destinations.
func ValidateDeadLetterTarget(dstPath string) error {
	deadLetterDir := filepath.Dir(dstPath)
	dirInfo, err := os.Lstat(deadLetterDir)
	if err != nil {
		return fmt.Errorf("lstat dead-letter dir: %w", err)
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("dead-letter target dir is symlink: %s", deadLetterDir)
	}

	dstInfo, err := os.Lstat(dstPath)
	if err == nil {
		if dstInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("dead-letter target is symlink: %s", dstPath)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("lstat dead-letter target: %w", err)
	}
	return nil
}

// MoveToDeadLetter moves a live mailbox file to a validated dead-letter path.
func MoveToDeadLetter(srcPath, dstPath string) error {
	if err := ValidateDeadLetterTarget(dstPath); err != nil {
		return err
	}
	return os.Rename(srcPath, dstPath)
}

// WriteDeadLetterFile writes a dead-letter record to a validated path.
func WriteDeadLetterFile(dstPath string, content []byte) error {
	if err := ValidateDeadLetterTarget(dstPath); err != nil {
		return err
	}
	return os.WriteFile(dstPath, content, 0o600)
}

// DeliverPostToInbox moves a live post file into a recipient inbox.
func DeliverPostToInbox(postPath, recipientInbox, filename string) (string, error) {
	if err := os.MkdirAll(recipientInbox, 0o700); err != nil {
		return "", fmt.Errorf("creating recipient inbox: %w", err)
	}
	dst := filepath.Join(recipientInbox, filename)
	if err := os.Rename(postPath, dst); err != nil {
		return "", fmt.Errorf("moving to inbox: %w", err)
	}
	return dst, nil
}

// ConsumePost removes a post file after another delivery backend consumed it.
func ConsumePost(postPath string) error {
	return os.Remove(postPath)
}

// CountInboxMessages returns the number of .md files in an inbox directory.
func CountInboxMessages(inboxDir string) (int, error) {
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	n := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			n++
		}
	}
	return n, nil
}

// ShadowRelativePath returns the mailbox path relative to the session dir.
func ShadowRelativePath(sessionDir, fullPath string) string {
	rel, err := filepath.Rel(sessionDir, fullPath)
	if err != nil {
		return filepath.Base(fullPath)
	}
	return rel
}

// ArchiveInboxMessage moves an inbox message to read/ or removes duplicates.
func ArchiveInboxMessage(absPath, filename string) (string, error) {
	readDir := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(absPath))), "read")
	if err := os.MkdirAll(readDir, 0o700); err != nil {
		return "", fmt.Errorf("creating read directory: %w", err)
	}
	dst := filepath.Join(readDir, filename)
	if _, err := os.Stat(dst); err == nil {
		if err := os.Remove(absPath); err != nil {
			return "", fmt.Errorf("archiving message: %w", err)
		}
		return dst, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("archiving message: %w", err)
	}
	if err := os.Rename(absPath, dst); err != nil {
		return "", fmt.Errorf("archiving message: %w", err)
	}
	return dst, nil
}
