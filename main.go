package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
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

	fmt.Printf("postman: daemon started (context=%s, pid=%d)\n", *contextID, os.Getpid())
	fmt.Println("postman: not yet implemented (waiting for fsnotify in Issue #4)")
	return nil
}

func runCreateDraft(args []string) error {
	fs := flag.NewFlagSet("create-draft", flag.ContinueOnError)
	to := fs.String("to", "", "recipient node name (required)")
	contextID := fs.String("context-id", "", "session context ID")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to == "" {
		return fmt.Errorf("--to is required")
	}

	_ = contextID // NOTE: Will be used in Issue #4 for draft file creation
	fmt.Printf("postman: create-draft to=%s (not yet implemented)\n", *to)
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
