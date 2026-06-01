package store

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DeadLetterPlan is the pure path plan for writing or moving a message to dead-letter/.
type DeadLetterPlan struct {
	SessionDir      string
	Filename        string
	Suffix          string
	DeadLetterDir   string
	DestinationPath string
}

// DeadLetterPath builds the dead-letter destination path with a reason suffix.
func DeadLetterPath(sessionDir, filename, suffix string) string {
	return PlanDeadLetterMessage(sessionDir, filename, suffix).DestinationPath
}

// PlanDeadLetterMessage builds the dead-letter destination path without touching the filesystem.
func PlanDeadLetterMessage(sessionDir, filename, suffix string) DeadLetterPlan {
	base := strings.TrimSuffix(filename, ".md")
	deadLetterDir := filepath.Join(sessionDir, "dead-letter")
	return DeadLetterPlan{
		SessionDir:      sessionDir,
		Filename:        filename,
		Suffix:          suffix,
		DeadLetterDir:   deadLetterDir,
		DestinationPath: filepath.Join(deadLetterDir, base+suffix+".md"),
	}
}

type deadLetterFileOps struct {
	lstat     func(string) (fs.FileInfo, error)
	rename    func(string, string) error
	writeFile func(string, []byte, fs.FileMode) error
}

var osDeadLetterFileOps = deadLetterFileOps{
	lstat:     os.Lstat,
	rename:    os.Rename,
	writeFile: os.WriteFile,
}

// ValidateDeadLetterTarget rejects symlinked dead-letter destinations.
func ValidateDeadLetterTarget(dstPath string) error {
	return validateDeadLetterTargetWithOps(dstPath, osDeadLetterFileOps)
}

func validateDeadLetterTargetWithOps(dstPath string, ops deadLetterFileOps) error {
	deadLetterDir := filepath.Dir(dstPath)
	dirInfo, err := ops.lstat(deadLetterDir)
	if err != nil {
		return fmt.Errorf("lstat dead-letter dir: %w", err)
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("dead-letter target dir is symlink: %s", deadLetterDir)
	}

	dstInfo, err := ops.lstat(dstPath)
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
	return moveToDeadLetterWithOps(srcPath, dstPath, osDeadLetterFileOps)
}

func moveToDeadLetterWithOps(srcPath, dstPath string, ops deadLetterFileOps) error {
	if err := validateDeadLetterTargetWithOps(dstPath, ops); err != nil {
		return err
	}
	return ops.rename(srcPath, dstPath)
}

// WriteDeadLetterFile writes a dead-letter record to a validated path.
func WriteDeadLetterFile(dstPath string, content []byte) error {
	return writeDeadLetterFileWithOps(dstPath, content, osDeadLetterFileOps)
}

func writeDeadLetterFileWithOps(dstPath string, content []byte, ops deadLetterFileOps) error {
	if err := validateDeadLetterTargetWithOps(dstPath, ops); err != nil {
		return err
	}
	return ops.writeFile(dstPath, content, 0o600)
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

// InboxArchivePlan is the pure path plan for moving an inbox message to read/.
type InboxArchivePlan struct {
	SourcePath string
	Filename   string
	SessionDir string
	ReadDir    string
	ReadPath   string
}

// PlanArchiveInboxMessage derives the read archive path from an absolute inbox message path.
func PlanArchiveInboxMessage(absPath, filename string) (InboxArchivePlan, error) {
	if absPath == "" {
		return InboxArchivePlan{}, fmt.Errorf("archive inbox message: source path is empty")
	}
	if !filepath.IsAbs(absPath) {
		return InboxArchivePlan{}, fmt.Errorf("archive inbox message: source path must be absolute: %s", absPath)
	}
	if filename == "" || filename != filepath.Base(filename) {
		return InboxArchivePlan{}, fmt.Errorf("archive inbox message: filename must be a base name: %s", filename)
	}

	sourcePath := filepath.Clean(absPath)
	if filepath.Base(sourcePath) != filename {
		return InboxArchivePlan{}, fmt.Errorf("archive inbox message: source filename %q does not match %q", filepath.Base(sourcePath), filename)
	}

	nodeInboxDir := filepath.Dir(sourcePath)
	inboxDir := filepath.Dir(nodeInboxDir)
	sessionDir := filepath.Dir(inboxDir)
	if filepath.Base(inboxDir) != "inbox" || sessionDir == "." || sessionDir == string(filepath.Separator) {
		return InboxArchivePlan{}, fmt.Errorf("archive inbox message: source path is not under <session>/inbox/<node>: %s", sourcePath)
	}

	readDir := filepath.Join(sessionDir, "read")
	return InboxArchivePlan{
		SourcePath: sourcePath,
		Filename:   filename,
		SessionDir: sessionDir,
		ReadDir:    readDir,
		ReadPath:   filepath.Join(readDir, filename),
	}, nil
}

type archiveFileOps struct {
	mkdirAll func(string, fs.FileMode) error
	stat     func(string) (fs.FileInfo, error)
	remove   func(string) error
	rename   func(string, string) error
}

var osArchiveFileOps = archiveFileOps{
	mkdirAll: os.MkdirAll,
	stat:     os.Stat,
	remove:   os.Remove,
	rename:   os.Rename,
}

// ArchiveInboxMessage moves an inbox message to read/ or removes duplicates.
func ArchiveInboxMessage(absPath, filename string) (string, error) {
	plan, err := PlanArchiveInboxMessage(absPath, filename)
	if err != nil {
		return "", err
	}
	return archiveInboxMessageWithOps(plan, osArchiveFileOps)
}

func archiveInboxMessageWithOps(plan InboxArchivePlan, ops archiveFileOps) (string, error) {
	if err := ops.mkdirAll(plan.ReadDir, 0o700); err != nil {
		return "", fmt.Errorf("creating read directory: %w", err)
	}
	if _, err := ops.stat(plan.ReadPath); err == nil {
		if err := ops.remove(plan.SourcePath); err != nil {
			return "", fmt.Errorf("archiving message: %w", err)
		}
		return plan.ReadPath, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("archiving message: %w", err)
	}
	if err := ops.rename(plan.SourcePath, plan.ReadPath); err != nil {
		return "", fmt.Errorf("archiving message: %w", err)
	}
	return plan.ReadPath, nil
}
