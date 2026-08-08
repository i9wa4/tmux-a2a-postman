package store

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

type verifiedArchiveHooks struct {
	beforeStage  func()
	beforeUnlink func()
}

var defaultVerifiedArchiveHooks verifiedArchiveHooks

// ArchiveInboxMessage moves an inbox message to read/ or removes duplicates.
func ArchiveInboxMessage(absPath, filename string) (string, error) {
	plan, err := PlanArchiveInboxMessage(absPath, filename)
	if err != nil {
		return "", err
	}
	return archiveInboxMessageWithOps(plan, osArchiveFileOps)
}

// ArchiveInboxMessageVerified verifies or repairs read/ with the exact source
// content before removing the inbox source. Source preservation is preferred
// over returning a corrupt or missing archived body.
func ArchiveInboxMessageVerified(absPath, filename string, data []byte) (string, error) {
	plan, err := PlanArchiveInboxMessage(absPath, filename)
	if err != nil {
		return "", err
	}
	readPath, cleanup, err := archiveInboxMessageVerifiedWithHooks(plan, data, defaultVerifiedArchiveHooks)
	if err != nil {
		return "", err
	}
	if err := cleanup(); err != nil {
		return "", err
	}
	return readPath, nil
}

// BeginArchiveInboxMessageVerified completes the durable portion of a verified
// inbox archive and returns cleanup for the remaining private stage state.
//
// On success the read archive content and parent read directory are durable, so
// callers may report a successful pop before invoking cleanup. Cleanup remains
// idempotent and recoverable through RecoverArchiveBindings if the process dies
// or cleanup fails.
func BeginArchiveInboxMessageVerified(absPath, filename string, data []byte) (string, func() error, error) {
	plan, err := PlanArchiveInboxMessage(absPath, filename)
	if err != nil {
		return "", nil, err
	}
	return archiveInboxMessageVerifiedWithHooks(plan, data, defaultVerifiedArchiveHooks)
}

// BeginArchiveInboxMessageVerifiedAt completes a verified inbox archive using
// an already-trusted source directory descriptor. The path still defines the
// read/ archive location, but source verification, staging, and cleanup stay
// relative to sourceDir.
func BeginArchiveInboxMessageVerifiedAt(sourceDir *os.File, absPath, filename string, data []byte) (string, func() error, error) {
	plan, err := PlanArchiveInboxMessage(absPath, filename)
	if err != nil {
		return "", nil, err
	}
	if sourceDir == nil {
		return "", nil, fmt.Errorf("archive inbox message: source directory descriptor is nil")
	}
	return archiveInboxMessageVerifiedAtWithHooks(sourceDir, plan, data, defaultVerifiedArchiveHooks)
}

// RecoverArchiveBindings repairs or fails closed on orphaned verified archive
// stages left in inbox directories by an interrupted direct pop.
func RecoverArchiveBindings(sessionDir string) error {
	inboxRoot := filepath.Join(sessionDir, "inbox")
	readDir := filepath.Join(sessionDir, "read")
	entries, err := os.ReadDir(inboxRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading inbox root for archive binding recovery: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := recoverArchiveBindingsInInboxDir(filepath.Join(inboxRoot, entry.Name()), readDir); err != nil {
			return err
		}
	}
	return nil
}

func archiveInboxMessageVerifiedWithHooks(plan InboxArchivePlan, data []byte, hooks verifiedArchiveHooks) (readPath string, cleanup func() error, err error) {
	source, err := openBoundArchiveSource(plan.SourcePath, data)
	if err != nil {
		return "", nil, err
	}
	return archiveInboxMessageVerifiedFromSource(source, plan, data, hooks)
}

func archiveInboxMessageVerifiedAtWithHooks(sourceDir *os.File, plan InboxArchivePlan, data []byte, hooks verifiedArchiveHooks) (readPath string, cleanup func() error, err error) {
	source, err := openBoundArchiveSourceAt(sourceDir, plan.SourcePath, plan.Filename, data)
	if err != nil {
		return "", nil, err
	}
	return archiveInboxMessageVerifiedFromSource(source, plan, data, hooks)
}

func archiveInboxMessageVerifiedFromSource(source *boundArchiveSource, plan InboxArchivePlan, data []byte, hooks verifiedArchiveHooks) (readPath string, cleanup func() error, err error) {
	stageName, err := source.Stage(hooks.beforeStage)
	if err != nil {
		_ = source.Close()
		return "", nil, err
	}
	staged := true
	defer func() {
		if staged {
			_ = source.Restore(stageName)
			_ = source.Close()
		}
	}()
	if err := ensureReadArchiveContent(plan.ReadPath, data); err != nil {
		return "", nil, err
	}
	var once sync.Once
	var cleanupErr error
	cleanup = func() error {
		once.Do(func() {
			defer source.Close()
			cleanupErr = source.UnlinkStage(stageName, hooks.beforeUnlink)
			if cleanupErr != nil && source.archiveBindingCleanupComplete(stageName) {
				cleanupErr = nil
			}
		})
		return cleanupErr
	}
	staged = false
	return plan.ReadPath, cleanup, nil
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

func ensureReadArchiveContent(readPath string, data []byte) error {
	info, err := os.Lstat(readPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("checking archive read path: refusing symlink %s", readPath)
		}
		current, readErr := os.ReadFile(readPath)
		if readErr != nil {
			return fmt.Errorf("checking archive read path: %w", readErr)
		}
		if bytes.Equal(current, data) {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking archive read path: %w", err)
	}

	dir := filepath.Dir(readPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating read directory: %w", err)
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("checking read directory: %w", err)
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("checking read directory: refusing symlink %s", dir)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(readPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temporary archive read path: %w", err)
	}
	tmpPath := tmp.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing temporary archive read path: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("syncing temporary archive read path: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temporary archive read path: %w", err)
	}
	if err := os.Rename(tmpPath, readPath); err != nil {
		return fmt.Errorf("repairing archive read path: %w", err)
	}
	cleanupTmp = false
	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("syncing archive read directory: %w", err)
	}
	return nil
}

func validateVerifiedArchiveSource(sourcePath string, data []byte) (fs.FileInfo, error) {
	sourceInfo, err := os.Lstat(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("checking archive source: %w", err)
	}
	if sourceInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("checking archive source: refusing symlink %s", sourcePath)
	}
	if !sourceInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("checking archive source: refusing non-regular file %s", sourcePath)
	}
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("opening archive source for verification: %w", err)
	}
	defer sourceFile.Close()
	descriptorInfo, err := sourceFile.Stat()
	if err != nil {
		return nil, fmt.Errorf("stating archive source descriptor: %w", err)
	}
	if !os.SameFile(sourceInfo, descriptorInfo) {
		return nil, fmt.Errorf("checking archive source: source object changed for %s", sourcePath)
	}
	if !descriptorInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("checking archive source descriptor: refusing non-regular file %s", sourcePath)
	}
	current, err := io.ReadAll(sourceFile)
	if err != nil {
		return nil, fmt.Errorf("reading archive source descriptor for verification: %w", err)
	}
	if !bytes.Equal(current, data) {
		return nil, fmt.Errorf("checking archive source: source bytes changed for %s", sourcePath)
	}
	currentInfo, err := sourceFile.Stat()
	if err != nil {
		return nil, fmt.Errorf("checking archive source descriptor after read: %w", err)
	}
	if !os.SameFile(descriptorInfo, currentInfo) {
		return nil, fmt.Errorf("checking archive source descriptor after read: source object changed for %s", sourcePath)
	}
	return sourceInfo, nil
}

var syncDirectory = func(dir string) error {
	dirHandle, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := dirHandle.Sync(); err != nil {
		_ = dirHandle.Close()
		return err
	}
	if err := dirHandle.Close(); err != nil {
		return err
	}
	return nil
}

// PopReceiptPlan is the pure path plan for writing a pop receipt next to a read/ archive.
type PopReceiptPlan struct {
	MarkdownPath string
	ReadDir      string
	ReceiptPath  string
}

// PlanPopReceipt derives the pop receipt path without touching the filesystem.
func PlanPopReceipt(markdownPath string) PopReceiptPlan {
	if markdownPath == "" {
		return PopReceiptPlan{}
	}
	readDir := filepath.Dir(markdownPath)
	if filepath.Base(readDir) != "read" {
		return PopReceiptPlan{MarkdownPath: markdownPath}
	}
	filename := filepath.Base(markdownPath)
	ext := filepath.Ext(filename)
	stem := strings.TrimSuffix(filename, ext)
	if stem == "" {
		stem = filename
	}
	return PopReceiptPlan{
		MarkdownPath: markdownPath,
		ReadDir:      readDir,
		ReceiptPath:  filepath.Join(readDir, stem+".pop.json"),
	}
}
