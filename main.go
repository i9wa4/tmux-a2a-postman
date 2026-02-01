package main

import (
	"context"
	"flag"
	"fmt"
	"os"
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
