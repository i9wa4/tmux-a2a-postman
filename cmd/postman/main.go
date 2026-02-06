package main

import (
	"context"
	"flag"
	"fmt"
	"log"
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
	"github.com/i9wa4/tmux-a2a-postman/internal/uipane"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/daemon"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/lock"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/ping"
	"github.com/i9wa4/tmux-a2a-postman/internal/reminder"
	"github.com/i9wa4/tmux-a2a-postman/internal/sessionidle"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
	"github.com/i9wa4/tmux-a2a-postman/internal/version"
	"github.com/i9wa4/tmux-a2a-postman/internal/watchdog"
)

func main() {
	// Top-level flags
	fs := flag.NewFlagSet("postman", flag.ContinueOnError)
	showVersion := fs.Bool("version", false, "show version")
	showHelp := fs.Bool("help", false, "show help")
	noTUI := fs.Bool("no-tui", false, "run without TUI")
	contextID := fs.String("context-id", "", "session context ID (auto-generated if not specified)")
	configPath := fs.String("config", "", "path to config file (auto-detect from XDG_CONFIG_HOME if not specified)")
	logFilePath := fs.String("log-file", "", "log file path (defaults to $XDG_STATE_HOME/postman/postman.log)")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: postman [options] [command]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Options:")
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  start        Start postman daemon (default)")
		fmt.Fprintln(os.Stderr, "  create-draft Create message draft")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  postman --no-tui                    # Start daemon without TUI")
		fmt.Fprintln(os.Stderr, "  postman --context-id my-session     # Start with specific context")
		fmt.Fprintln(os.Stderr, "  postman create-draft --to worker    # Create draft message")
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			return
		}
		os.Exit(1)
	}

	if *showVersion {
		fmt.Printf("postman %s\n", version.Version)
		return
	}

	if *showHelp {
		fs.Usage()
		return
	}

	// Determine command (default: start)
	command := "start"
	args := fs.Args()
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}

	switch command {
	case "start":
		// Check if this should run as watchdog
		if os.Getenv("A2A_NODE") == "watchdog" {
			if err := runWatchdog(*contextID, *configPath, *logFilePath); err != nil {
				fmt.Fprintf(os.Stderr, "❌ postman watchdog: %v\n", err)
				os.Exit(1)
			}
		} else {
			if err := runStartWithFlags(*contextID, *configPath, *logFilePath, *noTUI); err != nil {
				fmt.Fprintf(os.Stderr, "❌ postman start: %v\n", err)
				os.Exit(1)
			}
		}
	case "create-draft":
		if err := runCreateDraft(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman create-draft: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "❌ postman: unknown command %q\n", command)
		fs.Usage()
		os.Exit(1)
	}
}

func runStartWithFlags(contextID, configPath, logFilePath string, noTUI bool) error {
	// Auto-generate context ID if not specified
	if contextID == "" {
		contextID = fmt.Sprintf("session-%s-%04x",
			time.Now().Format("20060102-150405"),
			time.Now().UnixNano()&0xffff)
	}

	// LoadConfig handles auto-detection if configPath is empty
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Parse edge definitions for routing
	adjacency, err := config.ParseEdges(cfg.Edges)
	if err != nil {
		return fmt.Errorf("parsing edges: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)
	contextDir := filepath.Join(baseDir, contextID)

	// Setup log output (Issue #36: always log to file)
	logPath := logFilePath
	if logPath == "" {
		// Default to $baseDir/{contextID}/postman.log
		logPath = filepath.Join(contextDir, "postman.log")
	}
	logDir := filepath.Dir(logPath)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("creating log directory: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}
	defer func() {
		_ = logFile.Close()
	}()

	log.SetOutput(logFile)
	log.SetFlags(log.LstdFlags)
	log.Printf("postman: daemon starting (context=%s, log=%s)\n", contextID, logPath)

	// TODO: Multi-session support - for now, use "default" as session name
	// Later phases will discover actual tmux sessions and create dirs for each
	defaultSessionName := "default"
	sessionDir := filepath.Join(contextDir, defaultSessionName)

	if err := config.CreateMultiSessionDirs(contextDir, defaultSessionName); err != nil {
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
		log.Printf("⚠️  postman: stale inbox cleanup failed: %v\n", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	postDir := filepath.Join(sessionDir, "post")

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	// Discover nodes at startup (before watching)
	nodes, err := discovery.DiscoverNodes(baseDir, contextID)
	if err != nil {
		// WARNING: log but continue - nodes can be empty
		log.Printf("⚠️  postman: node discovery failed: %v\n", err)
		nodes = make(map[string]discovery.NodeInfo)
	}

	// Watch all discovered session directories
	watchedDirs := make(map[string]bool)
	for nodeName, nodeInfo := range nodes {
		// Ensure session directories exist for discovered nodes
		if err := config.CreateSessionDirs(nodeInfo.SessionDir); err != nil {
			log.Printf("⚠️  postman: warning: could not create session dirs for %s: %v\n", nodeName, err)
			continue
		}

		nodePostDir := filepath.Join(nodeInfo.SessionDir, "post")
		nodeInboxDir := filepath.Join(nodeInfo.SessionDir, "inbox")

		if !watchedDirs[nodePostDir] {
			if err := watcher.Add(nodePostDir); err != nil {
				log.Printf("⚠️  postman: warning: could not watch %s post directory: %v\n", nodeName, err)
			} else {
				watchedDirs[nodePostDir] = true
			}
		}
		if !watchedDirs[nodeInboxDir] {
			if err := watcher.Add(nodeInboxDir); err != nil {
				log.Printf("⚠️  postman: warning: could not watch %s inbox directory: %v\n", nodeName, err)
			} else {
				watchedDirs[nodeInboxDir] = true
			}
		}
	}

	// Also watch default session directories (for postman's own messages)
	if !watchedDirs[postDir] {
		if err := watcher.Add(postDir); err != nil {
			return fmt.Errorf("watching post directory: %w", err)
		}
		watchedDirs[postDir] = true
	}
	if !watchedDirs[inboxDir] {
		if err := watcher.Add(inboxDir); err != nil {
			return fmt.Errorf("watching inbox directory: %w", err)
		}
		watchedDirs[inboxDir] = true
	}

	// Watch config file if exists
	resolvedConfigPath := configPath
	if resolvedConfigPath == "" {
		resolvedConfigPath = config.ResolveConfigPath()
	}
	if resolvedConfigPath != "" {
		if err := watcher.Add(resolvedConfigPath); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  postman: warning: could not watch config: %v\n", err)
		}
	}

	log.Printf("📮 postman: daemon started (context=%s, pid=%d, nodes=%d)\n",
		contextID, os.Getpid(), len(nodes))

	// Send PING to all nodes after startup delay
	if cfg.StartupDelay > 0 {
		startupDelay := time.Duration(cfg.StartupDelay * float64(time.Second))
		time.AfterFunc(startupDelay, func() {
			ping.SendPingToAll(baseDir, contextID, cfg)
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

	// Start session-level idle check goroutine
	sessionidle.StartSessionIdleCheck(baseDir, contextID, sessionDir, cfg, adjacency, 30.0)

	// Start compaction detection goroutine
	compaction.StartCompactionCheck(cfg, nodes, sessionDir)

	// Start daemon loop in goroutine
	daemonEvents := make(chan tui.DaemonEvent, 100)
	go daemon.RunDaemonLoop(ctx, baseDir, sessionDir, contextID, cfg, watcher, adjacency, nodes, knownNodes, digestedFiles, reminderState, daemonEvents, resolvedConfigPath)

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

	// Start UI pane status monitoring goroutine (Issue #46)
	go func() {
		// Issue #46: Find target pane ID using configured UI node name
		targetPaneID, _ := uipane.FindTargetPaneID(cfg.UINode)

		ticker := time.NewTicker(5 * time.Second) // Check every 5 seconds
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				paneInfo, err := uipane.GetPaneInfo(targetPaneID)
				if err == nil && paneInfo != nil {
					daemonEvents <- tui.DaemonEvent{
						Type: "concierge_status_update",
						Details: map[string]interface{}{
							"pane_info": paneInfo,
						},
					}
				}
			}
		}
	}()

	// Start TUI or wait for shutdown
	if noTUI {
		// No TUI mode: log only, block until ctx.Done()
		<-ctx.Done()
	} else {
		// TUI mode
		p := tea.NewProgram(tui.InitialModel(daemonEvents))
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("TUI error: %w", err)
		}
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

	// Generate unique task ID
	taskID := fmt.Sprintf("%s-%04x", ts, now.UnixNano()%0xFFFF)

	// Use draft_template from config if available
	content := cfg.DraftTemplate
	if content == "" {
		// Fallback to minimal template
		content = "---\nmethod: message/send\nparams:\n  contextId: {context_id}\n  from: {from}\n  to: {to}\n  timestamp: {timestamp}\n---\n\n## Content\n"
	}

	// Build variables map for template expansion
	vars := map[string]string{
		"context_id": resolvedContextID,
		"task_id":    taskID,
		"sender":     sender,
		"recipient":  *to,
		"timestamp":  now.Format(time.RFC3339),
		// Backward compatibility
		"from": sender,
		"to":   *to,
	}

	// Expand template with variables and shell commands
	timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
	content = template.ExpandTemplate(content, vars, timeout)

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
				log.Printf("⚠️  postman: failed to move stale message %s: %v\n", msg.Name(), err)
				continue
			}
			movedCount++
		}
	}

	if movedCount > 0 {
		log.Printf("🧹 postman: moved %d stale message(s) to read/\n", movedCount)
	}

	return nil
}

// runWatchdog runs the watchdog daemon when A2A_NODE=watchdog.
func runWatchdog(contextID, configPath, logFilePath string) error {
	// Auto-generate context ID if not specified
	if contextID == "" {
		contextID = fmt.Sprintf("session-%s-%04x",
			time.Now().Format("20060102-150405"),
			time.Now().UnixNano()&0xffff)
	}

	// LoadConfig
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Check if watchdog is enabled
	if !cfg.Watchdog.Enabled {
		return fmt.Errorf("watchdog is disabled in config")
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)
	contextDir := filepath.Join(baseDir, contextID)

	// NOTE: Use contextDir directly (not multi-session subdirectory)
	// to match Python postman's directory structure (.postman/session-ID/post/)
	sessionDir := contextDir

	if err := config.CreateSessionDirs(sessionDir); err != nil {
		return fmt.Errorf("creating session directories: %w", err)
	}

	// Acquire watchdog lock
	lockPath := os.ExpandEnv(cfg.Watchdog.Lock.Path)
	if lockPath == "" {
		lockPath = filepath.Join(baseDir, "postman-watchdog.lock")
	}
	watchdogLock, err := watchdog.AcquireLock(lockPath)
	if err != nil {
		return fmt.Errorf("acquiring watchdog lock: %w", err)
	}
	defer func() { _ = watchdogLock.ReleaseLock() }()

	// Setup log file
	logPath := logFilePath
	if logPath == "" {
		logPath = filepath.Join(contextDir, "watchdog.log")
	}
	if logPath != "" {
		logDir := filepath.Dir(logPath)
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return fmt.Errorf("creating log directory: %w", err)
		}
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("opening log file: %w", err)
		}
		defer func() {
			_ = logFile.Close()
		}()

		log.SetOutput(logFile)
		log.SetFlags(log.LstdFlags)

		log.Printf("watchdog: starting (context=%s, log=%s)\n", contextID, logPath)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Start heartbeat (Issue #46: added cfg.UINode parameter)
	var heartbeatStop chan<- struct{}
	if cfg.Watchdog.HeartbeatIntervalSeconds > 0 {
		heartbeatStop = watchdog.StartHeartbeat(sessionDir, contextID, cfg.UINode, cfg.Watchdog.HeartbeatIntervalSeconds)
		defer func() {
			if heartbeatStop != nil {
				close(heartbeatStop)
			}
		}()
	}

	// Initialize reminder state
	reminderState := watchdog.NewReminderState()

	log.Printf("watchdog: started (pid=%d)\n", os.Getpid())

	// Main watchdog loop
	ticker := time.NewTicker(time.Duration(cfg.Watchdog.IdleThresholdSeconds * float64(time.Second)))
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("watchdog: shutting down")
			return nil
		case <-ticker.C:
			// Check for idle panes
			idlePanes, err := watchdog.GetIdlePanes(cfg.Watchdog.IdleThresholdSeconds)
			if err != nil {
				log.Printf("watchdog: idle check failed: %v\n", err)
				continue
			}

			// Send reminders for idle panes (Issue #46: added cfg.UINode parameter)
			for _, activity := range idlePanes {
				if reminderState.ShouldSendReminder(activity.PaneID, cfg.Watchdog.CooldownSeconds) {
					if err := watchdog.SendIdleReminder(activity.PaneID, sessionDir, contextID, cfg.UINode, activity); err != nil {
						log.Printf("watchdog: reminder failed for %s: %v\n", activity.PaneID, err)
					} else {
						reminderState.MarkReminderSent(activity.PaneID)
						log.Printf("watchdog: reminder sent for %s\n", activity.PaneID)
					}
				}

				// Capture pane if enabled
				if cfg.Watchdog.Capture.Enabled {
					captureDir := filepath.Join(sessionDir, "capture")
					capturePath, err := watchdog.CapturePane(activity.PaneID, captureDir, cfg.Watchdog.Capture.TailLines)
					if err != nil {
						log.Printf("watchdog: capture failed for %s: %v\n", activity.PaneID, err)
					} else {
						log.Printf("watchdog: captured %s -> %s\n", activity.PaneID, capturePath)

						// Rotate captures
						if err := watchdog.RotateCaptures(captureDir, cfg.Watchdog.Capture.MaxFiles, int64(cfg.Watchdog.Capture.MaxBytes)); err != nil {
							log.Printf("watchdog: rotation failed: %v\n", err)
						}
					}
				}
			}
		}
	}
}
