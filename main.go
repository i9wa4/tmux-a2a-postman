package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

var revision string

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: postman <command> [options]")
		fmt.Fprintln(os.Stderr, "commands: start, create-draft, version")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "version":
		fmt.Printf("postman dev (rev: %s)\n", revision)
	case "start":
		if err := runStart(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "postman start: %v\n", err)
			os.Exit(1)
		}
	case "create-draft":
		if err := runCreateDraft(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "postman create-draft: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "postman: unknown command %q\n", os.Args[1])
		os.Exit(1)
	}
}

func runStart(args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	contextID := fs.String("context-id", "", "session context ID (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *contextID == "" {
		return fmt.Errorf("--context-id is required")
	}

	baseDir := resolveBaseDir()
	sessionDir := filepath.Join(baseDir, *contextID)

	if err := createSessionDirs(sessionDir); err != nil {
		return fmt.Errorf("creating session directories: %w", err)
	}

	lock, err := NewSessionLock(filepath.Join(sessionDir, "postman.lock"))
	if err != nil {
		return fmt.Errorf("acquiring lock: %w", err)
	}
	defer func() { _ = lock.Release() }()

	pidPath := filepath.Join(sessionDir, "postman.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return fmt.Errorf("writing PID file: %w", err)
	}
	defer func() { _ = os.Remove(pidPath) }()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	postDir := filepath.Join(sessionDir, "post")

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	if err := watcher.Add(postDir); err != nil {
		return fmt.Errorf("watching post directory: %w", err)
	}

	// Discover nodes at startup
	nodes, err := DiscoverNodes()
	if err != nil {
		// WARNING: log but continue - nodes can be empty
		fmt.Fprintf(os.Stderr, "postman: node discovery failed: %v\n", err)
		nodes = make(map[string]string)
	}

	fmt.Printf("postman: daemon started (context=%s, pid=%d, nodes=%d)\n",
		*contextID, os.Getpid(), len(nodes))

	for {
		select {
		case <-ctx.Done():
			fmt.Println("postman: shutting down")
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			// React to CREATE and RENAME (covers mv into post/)
			if event.Op&(fsnotify.Create|fsnotify.Rename) != 0 {
				filename := filepath.Base(event.Name)
				if strings.HasSuffix(filename, ".md") {
					// Re-discover nodes before each delivery
					if freshNodes, err := DiscoverNodes(); err == nil {
						nodes = freshNodes
					}
					if err := deliverMessage(sessionDir, filename, nodes); err != nil {
						fmt.Fprintf(os.Stderr, "postman: deliver %s: %v\n", filename, err)
					}
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "postman: watcher error: %v\n", err)
		}
	}
}

func runCreateDraft(args []string) error {
	fs := flag.NewFlagSet("create-draft", flag.ContinueOnError)
	to := fs.String("to", "", "recipient node name (required)")
	contextID := fs.String("context-id", "", "session context ID (required)")
	from := fs.String("from", "", "sender node name (defaults to $A2A_NODE)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to == "" {
		return fmt.Errorf("--to is required")
	}
	if *contextID == "" {
		return fmt.Errorf("--context-id is required")
	}

	sender := *from
	if sender == "" {
		sender = os.Getenv("A2A_NODE")
	}
	if sender == "" {
		return fmt.Errorf("--from is required (or set A2A_NODE)")
	}

	baseDir := resolveBaseDir()
	draftDir := filepath.Join(baseDir, *contextID, "draft")

	if err := os.MkdirAll(draftDir, 0o755); err != nil {
		return fmt.Errorf("creating draft directory: %w", err)
	}

	now := time.Now()
	ts := now.Format("20060102-150405")
	filename := fmt.Sprintf("%s-from-%s-to-%s.md", ts, sender, *to)
	draftPath := filepath.Join(draftDir, filename)

	content := fmt.Sprintf("---\nmethod: message/send\nparams:\n  contextId: %s\n  from: %s\n  to: %s\n  timestamp: %s\n---\n\n",
		*contextID, sender, *to, now.Format("2006-01-02T15:04:05.000000"))

	if err := os.WriteFile(draftPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing draft: %w", err)
	}

	fmt.Println(draftPath)
	return nil
}

// resolveBaseDir returns the base directory for postman sessions.
// Uses POSTMAN_HOME env var if set, otherwise defaults to ".postman/".
func resolveBaseDir() string {
	if v := os.Getenv("POSTMAN_HOME"); v != "" {
		return v
	}
	return ".postman"
}

// createSessionDirs creates the session directory structure.
func createSessionDirs(sessionDir string) error {
	dirs := []string{
		filepath.Join(sessionDir, "inbox"),
		filepath.Join(sessionDir, "post"),
		filepath.Join(sessionDir, "draft"),
		filepath.Join(sessionDir, "read"),
		filepath.Join(sessionDir, "dead-letter"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// deliverMessage moves a message from post/ to the recipient's inbox/ or dead-letter/.
func deliverMessage(sessionDir string, filename string, knownNodes map[string]string) error {
	postPath := filepath.Join(sessionDir, "post", filename)

	info, err := ParseMessageFilename(filename)
	if err != nil {
		// Parse error: move to dead-letter/
		dst := filepath.Join(sessionDir, "dead-letter", filename)
		return os.Rename(postPath, dst)
	}

	paneID, found := knownNodes[info.To]
	if !found {
		// Unknown recipient: move to dead-letter/
		dst := filepath.Join(sessionDir, "dead-letter", filename)
		return os.Rename(postPath, dst)
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

// SessionLock provides flock-based exclusive locking for a postman session.
type SessionLock struct {
	file *os.File
}

// NewSessionLock acquires an exclusive non-blocking lock on the given path.
// Returns an error if the lock is already held by another process.
func NewSessionLock(path string) (*SessionLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock already held: %w", err)
	}

	return &SessionLock{file: f}, nil
}

// Release releases the file lock and closes the lock file.
func (l *SessionLock) Release() error {
	if l.file == nil {
		return nil
	}
	if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil {
		return err
	}
	return l.file.Close()
}
