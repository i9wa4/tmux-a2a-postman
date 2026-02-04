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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"
	"github.com/i9wa4/tmux-a2a-postman/internal/compaction"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/daemon"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/lock"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/ping"
	"github.com/i9wa4/tmux-a2a-postman/internal/reminder"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
	"github.com/i9wa4/tmux-a2a-postman/internal/version"
)

func main() {
	// Dual-mode: no args or --tui → TUI mode (default interactive)
	if len(os.Args) == 1 {
		// No arguments → TUI mode
		if err := runTUIMain([]string{}); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman TUI: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Check for --version or -v flag
	if os.Args[1] == "--version" || os.Args[1] == "-v" {
		fmt.Printf("postman %s\n", version.Version)
		return
	}

	// Check for --tui flag (explicit TUI launch)
	if os.Args[1] == "--tui" {
		if err := runTUIMain(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman TUI: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Backward compatible CLI mode
	switch os.Args[1] {
	case "version":
		fmt.Printf("postman %s\n", version.Version)
	case "start":
		if err := runStart(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman start: %v\n", err)
			os.Exit(1)
		}
	case "create-draft":
		if err := runCreateDraft(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman create-draft: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "❌ postman: unknown command %q\n", os.Args[1])
		fmt.Fprintln(os.Stderr, "usage: postman [--version] [--tui] [command] [options]")
		fmt.Fprintln(os.Stderr, "commands: start, create-draft, version")
		os.Exit(1)
	}
}

// runTUIMain runs the TUI mode (create-draft TUI).
func runTUIMain(args []string) error {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	contextID := fs.String("context-id", "", "session context ID (optional, fallback to env/file)")
	configPath := fs.String("config", "", "path to config file (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	// Resolve context ID with fallback chain
	resolvedContextID, source, err := config.ResolveContextID(*contextID, baseDir)
	if err != nil {
		return fmt.Errorf("resolving context ID: %w", err)
	}
	fmt.Fprintf(os.Stderr, "postman: using context ID from %s\n", source)

	sessionDir := filepath.Join(baseDir, resolvedContextID)

	// Check if daemon is running (check for postman.pid)
	pidPath := filepath.Join(sessionDir, "postman.pid")
	if _, err := os.Stat(pidPath); os.IsNotExist(err) {
		return fmt.Errorf("daemon not running for context %s (start with: postman start --context-id %s)", resolvedContextID, resolvedContextID)
	}

	// Discover nodes (require daemon to be running)
	nodes, err := discovery.DiscoverNodes(baseDir)
	if err != nil {
		return fmt.Errorf("discovering nodes: %w (is daemon running?)", err)
	}

	// Get sender node from A2A_NODE env
	senderNode := os.Getenv("A2A_NODE")
	if senderNode == "" {
		return fmt.Errorf("A2A_NODE environment variable not set")
	}

	// Extract node names for TUI (map[string]NodeInfo -> map[string]string)
	nodeNames := make(map[string]string)
	for nodeName, nodeInfo := range nodes {
		nodeNames[nodeName] = nodeInfo.PaneID
	}

	// Launch create-draft TUI
	m := tui.InitialDraftModel(sessionDir, resolvedContextID, senderNode, nodeNames)
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}

func runStart(args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	contextID := fs.String("context-id", "", "session context ID (required)")
	configPath := fs.String("config", "", "path to config file (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *contextID == "" {
		return fmt.Errorf("--context-id is required")
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Parse edge definitions for routing
	adjacency, err := config.ParseEdges(cfg.Edges)
	if err != nil {
		return fmt.Errorf("parsing edges: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)
	sessionDir := filepath.Join(baseDir, *contextID)

	if err := config.CreateSessionDirs(sessionDir); err != nil {
		return fmt.Errorf("creating session directories: %w", err)
	}

	lockObj, err := lock.NewSessionLock(filepath.Join(sessionDir, "postman.lock"))
	if err != nil {
		return fmt.Errorf("acquiring lock: %w", err)
	}
	defer func() { _ = lockObj.Release() }()

	pidPath := filepath.Join(sessionDir, "postman.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return fmt.Errorf("writing PID file: %w", err)
	}
	defer func() { _ = os.Remove(pidPath) }()

	// Cleanup stale inbox messages (move to read/)
	inboxDir := filepath.Join(sessionDir, "inbox")
	readDir := filepath.Join(sessionDir, "read")
	if err := cleanupStaleInbox(inboxDir, readDir); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  postman: stale inbox cleanup failed: %v\n", err)
	}

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
	if err := watcher.Add(inboxDir); err != nil {
		return fmt.Errorf("watching inbox directory: %w", err)
	}

	// Watch config file if exists
	resolvedConfigPath := *configPath
	if resolvedConfigPath == "" {
		resolvedConfigPath = config.ResolveConfigPath()
	}
	if resolvedConfigPath != "" {
		if err := watcher.Add(resolvedConfigPath); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  postman: warning: could not watch config: %v\n", err)
		}
	}

	// Discover nodes at startup
	nodes, err := discovery.DiscoverNodes(baseDir)
	if err != nil {
		// WARNING: log but continue - nodes can be empty
		fmt.Fprintf(os.Stderr, "⚠️  postman: node discovery failed: %v\n", err)
		nodes = make(map[string]discovery.NodeInfo)
	}

	fmt.Printf("📮 postman: daemon started (context=%s, pid=%d, nodes=%d)\n",
		*contextID, os.Getpid(), len(nodes))

	// Send PING to all nodes after startup delay
	if cfg.StartupDelay > 0 {
		startupDelay := time.Duration(cfg.StartupDelay * float64(time.Second))
		time.AfterFunc(startupDelay, func() {
			ping.SendPingToAll(baseDir, *contextID, cfg)
		})
	}

	// Track known nodes for new node detection
	knownNodes := make(map[string]bool)
	for nodeName := range nodes {
		knownNodes[nodeName] = true
	}

	// Track digested files for observer digest (duplicate prevention)
	digestedFiles := make(map[string]bool)

	// Reminder state for per-node message counters
	reminderState := reminder.NewReminderState()

	// Start idle check goroutine
	idle.StartIdleCheck(cfg, adjacency, sessionDir)

	// Start compaction detection goroutine
	compaction.StartCompactionCheck(cfg, nodes, sessionDir)

	// Start daemon loop in goroutine
	daemonEvents := make(chan tui.DaemonEvent, 100)
	go daemon.RunDaemonLoop(ctx, baseDir, sessionDir, *contextID, cfg, watcher, adjacency, nodes, knownNodes, digestedFiles, reminderState, daemonEvents, resolvedConfigPath)

	// Send initial status
	daemonEvents <- tui.DaemonEvent{
		Type:    "status_update",
		Message: "Running",
		Details: map[string]interface{}{
			"node_count": len(nodes),
		},
	}

	// Send initial edges
	edgeList := make([]tui.Edge, len(cfg.Edges))
	for i, e := range cfg.Edges {
		edgeList[i] = tui.Edge{Raw: e}
	}
	daemonEvents <- tui.DaemonEvent{
		Type: "config_update",
		Details: map[string]interface{}{
			"edges": edgeList,
		},
	}

	// Send initial inbox messages (worker node)
	if nodeName := os.Getenv("A2A_NODE"); nodeName != "" {
		msgList := message.ScanInboxMessages(filepath.Join(inboxDir, nodeName))
		daemonEvents <- tui.DaemonEvent{
			Type: "inbox_update",
			Details: map[string]interface{}{
				"messages": msgList,
			},
		}
	}

	// Start TUI
	p := tea.NewProgram(tui.InitialModel(daemonEvents))
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}

func runCreateDraft(args []string) error {
	fs := flag.NewFlagSet("create-draft", flag.ContinueOnError)
	to := fs.String("to", "", "recipient node name (required)")
	contextID := fs.String("context-id", "", "session context ID (optional, auto-detect if not specified)")
	from := fs.String("from", "", "sender node name (defaults to $A2A_NODE)")
	configPath := fs.String("config", "", "path to config file (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to == "" {
		return fmt.Errorf("--to is required")
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	// Resolve context ID with fallback chain (auto-detect if not specified)
	resolvedContextID, _, err := config.ResolveContextID(*contextID, baseDir)
	if err != nil {
		return fmt.Errorf("resolving context ID: %w", err)
	}

	sender := *from
	if sender == "" {
		sender = os.Getenv("A2A_NODE")
	}
	if sender == "" {
		return fmt.Errorf("--from is required (or set A2A_NODE)")
	}

	draftDir := filepath.Join(baseDir, resolvedContextID, "draft")

	if err := os.MkdirAll(draftDir, 0o755); err != nil {
		return fmt.Errorf("creating draft directory: %w", err)
	}

	now := time.Now()
	ts := now.Format("20060102-150405")
	filename := fmt.Sprintf("%s-from-%s-to-%s.md", ts, sender, *to)
	draftPath := filepath.Join(draftDir, filename)

	// Use draft_template from config if available
	content := cfg.DraftTemplate
	if content == "" {
		// Fallback to minimal template
		content = "---\nmethod: message/send\nparams:\n  contextId: {{context_id}}\n  from: {{from}}\n  to: {{to}}\n  timestamp: {{timestamp}}\n---\n\n## Content\n"
	}

	// Expand template variables
	content = strings.ReplaceAll(content, "{{context_id}}", resolvedContextID)
	content = strings.ReplaceAll(content, "{{from}}", sender)
	content = strings.ReplaceAll(content, "{{to}}", *to)
	content = strings.ReplaceAll(content, "{{timestamp}}", now.Format(time.RFC3339))

	if err := os.WriteFile(draftPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing draft: %w", err)
	}

	fmt.Println(draftPath)
	return nil
}

// cleanupStaleInbox moves all messages from inbox/ subdirectories to read/.
// This cleans up stale messages from previous sessions.
func cleanupStaleInbox(inboxDir, readDir string) error {
	// Ensure read/ directory exists
	if err := os.MkdirAll(readDir, 0o755); err != nil {
		return fmt.Errorf("creating read directory: %w", err)
	}

	// Read inbox/ directory
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // inbox/ doesn't exist yet
		}
		return fmt.Errorf("reading inbox directory: %w", err)
	}

	// Iterate over node subdirectories
	movedCount := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		nodeName := entry.Name()
		nodeInbox := filepath.Join(inboxDir, nodeName)

		// Read messages in node's inbox
		messages, err := os.ReadDir(nodeInbox)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  postman: failed to read inbox for %s: %v\n", nodeName, err)
			continue
		}

		// Move each message to read/
		for _, msg := range messages {
			if msg.IsDir() || !strings.HasSuffix(msg.Name(), ".md") {
				continue
			}

			src := filepath.Join(nodeInbox, msg.Name())
			dst := filepath.Join(readDir, msg.Name())

			if err := os.Rename(src, dst); err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  postman: failed to move stale message %s: %v\n", msg.Name(), err)
				continue
			}
			movedCount++
		}
	}

	if movedCount > 0 {
		fmt.Printf("🧹 postman: moved %d stale message(s) to read/\n", movedCount)
	}

	return nil
}
