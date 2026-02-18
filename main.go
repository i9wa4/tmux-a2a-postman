package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
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
	"github.com/i9wa4/tmux-a2a-postman/internal/session"
	"github.com/i9wa4/tmux-a2a-postman/internal/sessionidle"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
	"github.com/i9wa4/tmux-a2a-postman/internal/version"
	"github.com/i9wa4/tmux-a2a-postman/internal/watchdog"
)

// safeGo starts a goroutine with panic recovery (Issue #57).
func safeGo(name string, events chan<- tui.DaemonEvent, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				log.Printf("🚨 PANIC in goroutine %q: %v\n%s\n", name, r, string(stack))
				fmt.Fprintf(os.Stderr, "🚨 PANIC in goroutine %q: %v\n", name, r)
				if events != nil {
					events <- tui.DaemonEvent{
						Type:    "error",
						Message: fmt.Sprintf("Internal error in %s (recovered)", name),
					}
				}
			}
		}()
		fn()
	}()
}

func main() {
	// Top-level flags
	fs := flag.NewFlagSet("postman", flag.ContinueOnError)
	showVersion := fs.Bool("version", false, "show version")
	showHelp := fs.Bool("help", false, "show help")
	noTUI := fs.Bool("no-tui", false, "run without TUI")
	contextID := fs.String("context-id", "", "session context ID (auto-generated if not specified)")
	configPath := fs.String("config", "", "path to config file (auto-detect from XDG_CONFIG_HOME if not specified)")
	logFilePath := fs.String("log-file", "", "log file path (defaults to $XDG_STATE_HOME/tmux-a2a-postman/{contextID}/postman.log)")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: tmux-a2a-postman [options] [command]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Options:")
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  start                      Start tmux-a2a-postman daemon (default)")
		fmt.Fprintln(os.Stderr, "  create-draft               Create message draft")
		fmt.Fprintln(os.Stderr, "  get-session-status-oneline Show all sessions' pane status in one line")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman --no-tui                    # Start daemon without TUI")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman --context-id my-session     # Start with specific context")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman create-draft --to worker    # Create draft message")
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			return
		}
		os.Exit(1)
	}

	if *showVersion {
		fmt.Printf("tmux-a2a-postman %s\n", version.Version)
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
		if config.GetTmuxPaneName() == "watchdog" {
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
	case "get-session-status-oneline":
		if err := runGetSessionStatusOneline(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman get-session-status-oneline: %v\n", err)
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

	// Validate configuration
	if cfg.PingMode == "ui_node_only" && cfg.UINode == "" {
		log.Println("⚠️  WARNING: ping_mode=ui_node_only is set but ui_node is empty. No PING will be sent.")
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
		// Issue #57: Ensure logs are flushed before exit
		log.Println("postman: flushing logs and shutting down")
		_ = logFile.Sync()
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

	// Issue #75: Generate RULES.md in session directory
	if err := config.GenerateRulesFile(sessionDir, contextID, cfg); err != nil {
		log.Printf("⚠️  postman: failed to generate RULES.md: %v\n", err)
		// Non-fatal: continue without RULES.md
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

	// Issue #57: Signal handling logging
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	safeGo("signal-handler", nil, func() {
		sig := <-sigCh
		log.Printf("🛑 postman: received signal %v, initiating graceful shutdown\n", sig)
		cancel()
	})

	postDir := filepath.Join(sessionDir, "post")

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	// Discover nodes at startup (before watching, edge-filtered)
	nodes, err := discovery.DiscoverNodes(baseDir, contextID)
	if err != nil {
		// WARNING: log but continue - nodes can be empty
		log.Printf("⚠️  postman: node discovery failed: %v\n", err)
		nodes = make(map[string]discovery.NodeInfo)
	}
	edgeNodes := config.GetEdgeNodeNames(cfg.Edges)
	for nodeName := range nodes {
		parts := strings.SplitN(nodeName, ":", 2)
		rawName := parts[len(parts)-1]
		if !edgeNodes[rawName] {
			delete(nodes, nodeName)
		}
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

	// Issue #50: Watch nodes/ directory if exists
	nodesDir := config.ResolveNodesDir(resolvedConfigPath)
	if nodesDir != "" {
		if err := watcher.Add(nodesDir); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  postman: warning: could not watch nodes dir: %v\n", err)
		}
	}

	log.Printf("📮 postman: daemon started (context=%s, pid=%d, nodes=%d)\n",
		contextID, os.Getpid(), len(nodes))

	// Issue #47: Removed automatic SendPingToAll (too aggressive)
	// PING is now manual via 'p' key in TUI

	// Track known nodes for new node detection
	knownNodes := make(map[string]bool)
	for nodeName := range nodes {
		knownNodes[nodeName] = true
	}

	// Reminder state for per-node message counters
	reminderState := reminder.NewReminderState()

	// Issue #71: Create state management instances
	daemonState := daemon.NewDaemonState()
	idleTracker := idle.NewIdleTracker()
	compactionTracker := compaction.NewCompactionTracker()

	// Start idle check goroutine
	idleTracker.StartIdleCheck(ctx, cfg, adjacency, sessionDir)

	// Start pane capture check goroutine (hybrid idle detection)
	idleTracker.StartPaneCaptureCheck(ctx, cfg, baseDir, contextID)

	// Start session-level idle check goroutine
	sessionidle.StartSessionIdleCheck(baseDir, contextID, sessionDir, cfg, adjacency, 30.0)

	// Start compaction detection goroutine
	compactionTracker.StartCompactionCheck(ctx, cfg, nodes, sessionDir)

	// Start daemon loop in goroutine
	daemonEvents := make(chan tui.DaemonEvent, 100)
	safeGo("daemon-loop", daemonEvents, func() {
		daemon.RunDaemonLoop(ctx, baseDir, sessionDir, contextID, cfg, watcher, adjacency, nodes, knownNodes, reminderState, daemonEvents, resolvedConfigPath, nodesDir, daemonState, idleTracker)
	})

	// Issue #117: Discover all tmux sessions
	allSessions, _ := discovery.DiscoverAllSessions()
	if allSessions == nil {
		allSessions = []string{}
	}

	// Build session info from nodes (all disabled by default)
	sessionList := session.BuildSessionList(nodes, allSessions, daemonState.IsSessionEnabled)

	// Send initial status
	daemonEvents <- tui.DaemonEvent{
		Type:    "status_update",
		Message: "Running",
		Details: map[string]interface{}{
			"node_count": len(nodes),
			"sessions":   sessionList,
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
			"edges":    edgeList,
			"sessions": sessionList,
		},
	}

	// Send initial inbox messages (worker node)
	if nodeName := config.GetTmuxPaneName(); nodeName != "" {
		msgList := message.ScanInboxMessages(filepath.Join(inboxDir, nodeName))
		daemonEvents <- tui.DaemonEvent{
			Type: "inbox_update",
			Details: map[string]interface{}{
				"messages": msgList,
			},
		}
	}

	// Start TUI or wait for shutdown
	if noTUI {
		// No TUI mode: log only, block until ctx.Done()
		<-ctx.Done()
	} else {
		// TUI mode with command channel (Issue #47)
		tuiCommands := make(chan tui.TUICommand, 10)

		// Start TUI command handler goroutine
		safeGo("tui-command-handler", daemonEvents, func() {
			for {
				select {
				case <-ctx.Done():
					log.Println("postman: TUI command handler stopped")
					return
				case cmd := <-tuiCommands:
					// Issue #47: Handle TUI commands
					switch cmd.Type {
					case "send_ping":
						// Send PING to all nodes in the target session (Issue #52)
						targetNodes := make(map[string]discovery.NodeInfo)
						for k, v := range nodes {
							if v.SessionName == cmd.Target {
								targetNodes[k] = v
							}
						}
						if len(targetNodes) > 0 {
							// Build active nodes list (use simple names for display)
							activeNodes := make([]string, 0, len(nodes))
							for nodeName := range nodes {
								simpleName := ping.ExtractSimpleName(nodeName)
								activeNodes = append(activeNodes, simpleName)
							}
							// Send PING to each node in the target session
							successCount := 0
							failCount := 0
							pongActiveNodes := idleTracker.GetPongActiveNodes()
							for nodeName, nodeInfo := range targetNodes {
								if err := ping.SendPingToNode(nodeInfo, contextID, nodeName, cfg.PingTemplate, cfg, activeNodes, pongActiveNodes); err != nil {
									log.Printf("❌ postman: PING to %s failed: %v\n", nodeName, err)
									failCount++
									daemonEvents <- tui.DaemonEvent{
										Type:    "message_received",
										Message: fmt.Sprintf("PING failed for %s: %v", nodeName, err),
									}
								} else {
									log.Printf("📮 postman: PING sent to %s\n", nodeName)
									successCount++
									daemonEvents <- tui.DaemonEvent{
										Type:    "message_received",
										Message: fmt.Sprintf("PING sent to %s", nodeName),
									}
								}
							}
							// Send summary event
							totalCount := successCount + failCount
							daemonEvents <- tui.DaemonEvent{
								Type:    "message_received",
								Message: fmt.Sprintf("PING: %d/%d sent successfully", successCount, totalCount),
							}
						}
					case "session_toggle":
						// Toggle session enable/disable
						currentState := daemonState.IsSessionEnabled(cmd.Target)
						newState := !currentState
						daemonState.SetSessionEnabled(cmd.Target, newState)
						log.Printf("📮 postman: Session %s toggled to %v\n", cmd.Target, newState)

						// Rebuild session list and send status update (all sessions, not just nodes)
						allSessions, _ := discovery.DiscoverAllSessions()
						updatedSessionList := session.BuildSessionList(nodes, allSessions, daemonState.IsSessionEnabled)

						// Send status update
						daemonEvents <- tui.DaemonEvent{
							Type:    "status_update",
							Message: "Running",
							Details: map[string]interface{}{
								"node_count": len(nodes),
								"sessions":   updatedSessionList,
							},
						}

						// Send Events pane feedback
						stateStr := "OFF"
						if newState {
							stateStr = "ON"
						}
						daemonEvents <- tui.DaemonEvent{
							Type:    "message_received",
							Message: fmt.Sprintf("Session %s toggled %s", cmd.Target, stateStr),
						}
					case "clear_edge_history":
						// Clear edge activity history when switching sessions
						daemonState.ClearEdgeHistory()
						log.Println("postman: Edge history cleared (session switch)")
					}
				}
			}
		})

		p := tea.NewProgram(tui.InitialModel(daemonEvents, tuiCommands, cfg))
		log.Println("postman: TUI starting")
		finalModel, err := p.Run()
		if err != nil {
			log.Printf("postman: TUI exited with error: %v\n", err)
			return fmt.Errorf("TUI error: %w", err)
		}
		// Issue #57: Log TUI exit reason
		if model, ok := finalModel.(tui.Model); ok {
			if model.Quitting() {
				log.Println("postman: TUI exited normally (user quit)")
			} else {
				log.Println("postman: TUI exited (unexpected termination)")
			}
		} else {
			log.Println("postman: TUI exited (unknown state)")
		}
	}

	log.Println("postman: daemon exiting normally")
	return nil
}

func runCreateDraft(args []string) error {
	fs := flag.NewFlagSet("create-draft", flag.ContinueOnError)
	to := fs.String("to", "", "recipient node name (required)")
	contextID := fs.String("context-id", "", "session context ID (optional, auto-detect if not specified)")
	from := fs.String("from", "", "sender node name (defaults to current pane title)")
	session := fs.String("session", "", "tmux session name (optional, auto-detect if in tmux)")
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
		sender = config.GetTmuxPaneName()
	}
	if sender == "" {
		return fmt.Errorf("--from is required (or set tmux pane title)")
	}

	sessionName := *session
	if sessionName == "" {
		sessionName = config.GetTmuxSessionName()
	}
	if sessionName == "" {
		return fmt.Errorf("--session is required (or run inside tmux)")
	}

	// Issue #76: Validate session name (path traversal defense)
	sessionName = filepath.Base(sessionName)
	if sessionName == "." || sessionName == ".." || sessionName == "" {
		return fmt.Errorf("invalid session name: %q", *session)
	}

	// Issue #76: Verify session exists in tmux (if we're in tmux)
	if config.GetTmuxSessionName() != "" {
		// We're in tmux, so verify the session exists
		cmd := exec.Command("tmux", "list-sessions", "-F", "#{session_name}")
		output, err := cmd.Output()
		if err == nil {
			found := false
			for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
				if line == sessionName {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("tmux session %q does not exist", sessionName)
			}
		}
	}

	draftDir := filepath.Join(baseDir, resolvedContextID, sessionName, "draft")

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

// runGetSessionStatusOneline shows all tmux sessions' pane status in one line.
// Output format: [S0:window0_panes:window1_panes:...] [S1:window0_panes:...]
// Example: [S0:🟢🟢🔴:🟢🟢] [S1:🟢🟢🟢]
// Pane status: 🟢 = active (idle.go: 2+ changes in activity_window_seconds), 🔴 = inactive
// Issue #120: Refactored to use idle.go activity detection instead of #{pane_active}
func runGetSessionStatusOneline(args []string) error {
	// Load config to get base directory
	cfg, err := config.LoadConfig("")
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	// Resolve context ID (auto-detect if not specified)
	// If no context is active, output nothing (used from status bars)
	contextID, _, err := config.ResolveContextID("", baseDir)
	if err != nil {
		return nil
	}

	// Read pane activity status from daemon's exported state
	stateFile := filepath.Join(baseDir, contextID, "pane-activity.json")
	stateData, err := os.ReadFile(stateFile)
	if err != nil {
		// If file doesn't exist, daemon might not be running or no state yet
		// Fall back to showing all panes as inactive
		if os.IsNotExist(err) {
			return fmt.Errorf("daemon state file not found (is daemon running?): %s", stateFile)
		}
		return fmt.Errorf("reading pane activity state: %w", err)
	}

	var paneActivity map[string]bool
	if err := json.Unmarshal(stateData, &paneActivity); err != nil {
		return fmt.Errorf("parsing pane activity state: %w", err)
	}

	// Get all tmux sessions
	sessionsOutput, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		// Check if no server running
		if strings.Contains(string(sessionsOutput), "no server running") {
			// No tmux sessions - output nothing
			return nil
		}
		return fmt.Errorf("listing sessions: %w", err)
	}

	sessions := strings.Split(strings.TrimSpace(string(sessionsOutput)), "\n")
	if len(sessions) == 0 || sessions[0] == "" {
		// No sessions - output nothing
		return nil
	}

	var output []string

	for sessionIdx, sessionName := range sessions {
		if sessionName == "" {
			continue
		}

		// Get all windows in this session
		windowsOutput, err := exec.Command("tmux", "list-windows", "-t", sessionName, "-F", "#{window_index}").Output()
		if err != nil {
			return fmt.Errorf("listing windows for session %s: %w", sessionName, err)
		}

		windows := strings.Split(strings.TrimSpace(string(windowsOutput)), "\n")
		var windowStatuses []string

		for _, windowIndex := range windows {
			if windowIndex == "" {
				continue
			}

			// Get all panes in this window with their IDs
			target := fmt.Sprintf("%s:%s", sessionName, windowIndex)
			panesOutput, err := exec.Command("tmux", "list-panes", "-t", target, "-F", "#{pane_id}").Output()
			if err != nil {
				return fmt.Errorf("listing panes for %s: %w", target, err)
			}

			panes := strings.Split(strings.TrimSpace(string(panesOutput)), "\n")
			var paneStatuses string

			for _, paneID := range panes {
				if paneID == "" {
					continue
				}
				// Check if pane is active based on idle.go state
				isActive := paneActivity[paneID]
				if isActive {
					paneStatuses += "🟢"
				} else {
					paneStatuses += "🔴"
				}
			}

			windowStatuses = append(windowStatuses, paneStatuses)
		}

		// Build session status: [S<n>:window0:window1:...]
		sessionStatus := fmt.Sprintf("[S%d:%s]", sessionIdx, strings.Join(windowStatuses, ":"))
		output = append(output, sessionStatus)
	}

	fmt.Println(strings.Join(output, " "))
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

// runWatchdog runs the watchdog daemon when the pane title is "watchdog".
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
	lockPath := filepath.Join(sessionDir, "postman-watchdog.lock")
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
					if err := watchdog.SendIdleReminder(cfg, activity.PaneID, sessionDir, contextID, cfg.UINode, activity); err != nil {
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
