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
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/fsnotify/fsnotify"
	"github.com/i9wa4/tmux-a2a-postman/internal/alert"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/daemon"
	"github.com/i9wa4/tmux-a2a-postman/internal/diplomat"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/lock"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
	"github.com/i9wa4/tmux-a2a-postman/internal/ping"
	"github.com/i9wa4/tmux-a2a-postman/internal/reminder"
	"github.com/i9wa4/tmux-a2a-postman/internal/session"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
	"github.com/i9wa4/tmux-a2a-postman/internal/version"
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
	contextID := fs.String("context-id", "", "context ID (auto-generated if not specified)")
	configPath := fs.String("config", "", "path to config file (auto-detect from XDG_CONFIG_HOME if not specified)")
	logFilePath := fs.String("log-file", "", "log file path (defaults to $XDG_STATE_HOME/tmux-a2a-postman/{contextID}/postman.log)")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: tmux-a2a-postman [options] [command]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Options:")
		printDoubleDashDefaults(fs)
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  start                      Start tmux-a2a-postman daemon (default)")
		fmt.Fprintln(os.Stderr, "  stop                       Stop the running daemon for this tmux session")
		fmt.Fprintln(os.Stderr, "  create-draft               Create message draft")
		fmt.Fprintln(os.Stderr, "  send <filename>            Move draft to post/ to send it")
		fmt.Fprintln(os.Stderr, "  get-context-id             Print live context ID for current tmux session")
		fmt.Fprintln(os.Stderr, "  resend                     Re-send a dead-letter message")
		fmt.Fprintln(os.Stderr, "  get-session-status-oneline Show all sessions' pane status in one line")
		fmt.Fprintln(os.Stderr, "  get-session-health         Print session health per node")
		fmt.Fprintln(os.Stderr, "  count                      Count unread inbox messages")
		fmt.Fprintln(os.Stderr, "  read                       List inbox message paths")
		fmt.Fprintln(os.Stderr, "  archive <filename> [filename...]   Move inbox messages to read/")
		fmt.Fprintln(os.Stderr, "  help [topic]               Show help overview or topic-based help")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman --no-tui                    # Start daemon without TUI")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman --context-id my-session     # Start with specific context")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman create-draft --to worker    # Create draft message")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman archive msg.md              # Archive a message by filename")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman help messaging              # Show messaging topic help")
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
		runHelp([]string{})
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
		if err := runStartWithFlags(*contextID, *configPath, *logFilePath, *noTUI); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman start: %v\n", err)
			os.Exit(1)
		}
	case "stop":
		if err := runStop(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman stop: %v\n", err)
			os.Exit(1)
		}
	case "create-draft":
		if err := runCreateDraft(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman create-draft: %v\n", err)
			os.Exit(1)
		}
	case "send":
		if err := runSend(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman send: %v\n", err)
			os.Exit(1)
		}
	case "get-session-status-oneline":
		if err := runGetSessionStatusOneline(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman get-session-status-oneline: %v\n", err)
			os.Exit(1)
		}
	case "count":
		if err := runCount(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman count: %v\n", err)
			os.Exit(1)
		}
	case "read":
		if err := runRead(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman read: %v\n", err)
			os.Exit(1)
		}
	case "archive":
		if err := runArchive(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman archive: %v\n", err)
			os.Exit(1)
		}
	case "get-session-health":
		if err := runGetSessionHealth(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman get-session-health: %v\n", err)
			os.Exit(1)
		}
	case "get-context-id":
		if err := runGetContextID(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman get-context-id: %v\n", err)
			os.Exit(1)
		}
	case "resend":
		if err := runResend(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman resend: %v\n", err)
			os.Exit(1)
		}
	case "show-inbox-message":
		if err := runShowInboxMessage(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman show-inbox-message: %v\n", err)
			os.Exit(1)
		}
	case "list-archived-messages":
		if err := runListArchivedMessages(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman list-archived-messages: %v\n", err)
			os.Exit(1)
		}
	case "show-archived-message":
		if err := runShowArchivedMessage(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman show-archived-message: %v\n", err)
			os.Exit(1)
		}
	case "help":
		runHelp(args)
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
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return fmt.Errorf("creating log directory: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}
	defer func() {
		// Issue #57: Ensure logs are flushed before exit
		_ = logFile.Sync()
		_ = logFile.Close()
	}()

	log.SetOutput(logFile)
	log.SetFlags(log.LstdFlags)
	log.Printf("postman: daemon starting (context=%s, log=%s)\n", contextID, logPath)

	tmuxSessionName := config.GetTmuxSessionName()
	sessionName := tmuxSessionName
	if sessionName == "" {
		sessionName = "default"
	}
	sessionDir := filepath.Join(contextDir, sessionName)

	if err := config.CreateMultiSessionDirs(contextDir, sessionName); err != nil {
		return fmt.Errorf("creating session directories: %w", err)
	}

	if tmuxSessionName == "" {
		log.Println("warning: postman: could not determine tmux session name; running without session lock")
	} else {
		// Issue #249: Startup guard — detect duplicate daemon for this session.
		// Check postman.pid liveness before acquiring the flock, to emit a clear error.
		if liveCtx, err := config.ResolveContextIDFromSession(baseDir, tmuxSessionName); err == nil {
			return fmt.Errorf(
				"a postman daemon is already running in tmux session %q (context: %s).\n"+
					"Stop it first or use a different tmux session.",
				tmuxSessionName, liveCtx,
			)
		}

		lockDir := filepath.Join(baseDir, "lock")
		if err := os.MkdirAll(lockDir, 0o700); err != nil {
			return fmt.Errorf("creating lock directory: %w", err)
		}
		lockObj, err := lock.NewSessionLock(filepath.Join(lockDir, tmuxSessionName+".lock"))
		if err != nil {
			return fmt.Errorf("acquiring lock: %w", err)
		}
		defer func() { _ = lockObj.Release() }()
	}

	pidPath := filepath.Join(sessionDir, "postman.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		return fmt.Errorf("writing PID file: %w", err)
	}
	defer func() { _ = os.Remove(pidPath) }()

	// Cleanup stale inbox messages (move to read/)
	inboxDir := filepath.Join(sessionDir, "inbox")
	readDir := filepath.Join(sessionDir, "read")
	if err := cleanupStaleInbox(inboxDir, readDir); err != nil {
		log.Printf("⚠️  postman: stale inbox cleanup failed: %v\n", err)
	}

	// Drain stale post/ messages (Issue #207)
	if drained := message.DrainStalePost(sessionDir, cfg.MessageTTLSeconds); drained > 0 {
		log.Printf("postman: drained %d stale post/ messages at startup\n", drained)
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

	// Reclaim panes from dead daemon contexts (#272)
	if out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id} #{session_name} #{pane_title}").CombinedOutput(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, " ", 3)
			if len(parts) != 3 || parts[2] == "" {
				continue
			}
			paneID, paneSessionName := parts[0], parts[1]
			claimedOut, claimedErr := exec.Command("tmux", "show-options", "-p", "-v", "-t", paneID, "@a2a_context_id").Output()
			if claimedErr != nil {
				continue
			}
			claimedContext := strings.TrimSpace(string(claimedOut))
			if claimedContext == "" || claimedContext == contextID {
				continue
			}
			if !config.IsSessionPIDAlive(baseDir, claimedContext, paneSessionName) {
				_ = exec.Command("tmux", "set-option", "-p", "-u", "-t", paneID, "@a2a_context_id").Run()
			}
		}
	}

	// Discover nodes at startup (before watching, edge-filtered)
	nodes, startupCollisions, err := discovery.DiscoverNodesWithCollisions(baseDir, contextID, sessionName)
	if err != nil {
		// WARNING: log but continue - nodes can be empty
		log.Printf("⚠️  postman: node discovery failed: %v\n", err)
		nodes = make(map[string]discovery.NodeInfo)
		startupCollisions = nil
	}
	edgeNodes := config.GetEdgeNodeNames(cfg.Edges)
	for nodeName := range nodes {
		parts := strings.SplitN(nodeName, ":", 2)
		rawName := parts[len(parts)-1]
		if !edgeNodes[rawName] {
			delete(nodes, nodeName)
		}
	}
	// Claim discovered panes with this daemon's context ID.
	for _, nodeInfo := range nodes {
		claimCmd := exec.Command(
			"tmux", "set-option", "-p", "-t", nodeInfo.PaneID,
			"@a2a_context_id", contextID,
		)
		if err := claimCmd.Run(); err != nil {
			log.Printf(
				"postman: WARNING: failed to claim pane %s: %v\n",
				nodeInfo.PaneID, err,
			)
		}
	}
	// Shared node snapshot for background periodic refresh (Issue #139)
	var sharedNodes atomic.Pointer[map[string]discovery.NodeInfo]
	sharedNodes.Store(&nodes)

	// Log collisions for edge nodes after edge filter
	for _, collision := range startupCollisions {
		parts := strings.SplitN(collision.NodeKey, ":", 2)
		rawName := parts[len(parts)-1]
		if !edgeNodes[rawName] {
			continue
		}
		log.Printf("⚠️  postman: pane collision: %s: %s displaced by %s\n", collision.NodeKey, collision.LoserPaneID, collision.WinnerPaneID)
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
		nodeReadDir := filepath.Join(nodeInfo.SessionDir, "read")
		if !watchedDirs[nodeReadDir] {
			if err := watcher.Add(nodeReadDir); err != nil {
				log.Printf("⚠️  postman: warning: could not watch %s read directory: %v\n", nodeName, err)
			} else {
				watchedDirs[nodeReadDir] = true
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
	if !watchedDirs[readDir] {
		if err := watcher.Add(readDir); err != nil {
			log.Printf("⚠️  postman: warning: could not watch read directory: %v\n", err)
		} else {
			watchedDirs[readDir] = true
		}
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

	// Track known nodes for new node detection
	knownNodes := make(map[string]bool)
	for nodeName := range nodes {
		knownNodes[nodeName] = true
	}

	// Reminder state for per-node message counters
	reminderState := reminder.NewReminderState()

	// Issue #71: Create state management instances
	daemonState := daemon.NewDaemonState(cfg.StartupDrainWindowSeconds)
	if cfg.StartupDrainWindowSeconds > 0 {
		log.Printf("postman: startup drain window active (%.0fs) — session-enabled check bypassed (#217)\n", cfg.StartupDrainWindowSeconds)
	}
	idleTracker := idle.NewIdleTracker()
	alertRateLimiter := alert.NewAlertRateLimiter(time.Duration(cfg.AlertCooldownSeconds) * time.Second)
	notification.InitPaneCooldown(time.Duration(cfg.PaneNotifyCooldownSeconds) * time.Second)

	// Start idle check goroutine
	idleTracker.StartIdleCheck(ctx, cfg, adjacency, sessionDir, contextID, &sharedNodes)

	// Start pane capture check goroutine (hybrid idle detection)
	idleTracker.StartPaneCaptureCheck(ctx, cfg, baseDir, contextID, sessionName)

	// Start daemon loop in goroutine
	daemonEvents := make(chan tui.DaemonEvent, 100)
	safeGo("daemon-loop", daemonEvents, func() {
		daemon.RunDaemonLoop(ctx, baseDir, sessionDir, contextID, cfg, watcher, adjacency, nodes, knownNodes, reminderState, daemonEvents, resolvedConfigPath, nodesDir, daemonState, idleTracker, alertRateLimiter, &sharedNodes, sessionName)
	})

	// Issue #165: Start diplomat stale-registration cleanup goroutine
	if cfg.GetDiplomatEnabled() {
		diplomat.StartDiplomatCleanup(ctx, baseDir, 30.0, func(contextID string) {
			daemonEvents <- tui.DaemonEvent{
				Type:    "diplomat_stale_removed",
				Details: map[string]interface{}{"context_id": contextID},
			}
		})
	}

	// Issue #117: Discover all tmux sessions
	allSessions, _ := discovery.DiscoverAllSessions()
	if allSessions == nil {
		allSessions = []string{}
	}

	// Build session info from nodes (all disabled by default)
	sessionList := session.BuildSessionList(nodes, allSessions, daemonState.GetConfiguredSessionEnabled)

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
			var edgeClearTimer *time.Timer
			for {
				select {
				case <-ctx.Done():
					return
				case cmd := <-tuiCommands:
					// Issue #47: Handle TUI commands
					switch cmd.Type {
					case "session_toggle":
						// Toggle session enable/disable
						currentState := daemonState.GetConfiguredSessionEnabled(cmd.Target)
						newState := !currentState

						// Enforce 1-daemon-per-session: block enable if another live daemon already owns it.
						if newState {
							if liveCtx, err := config.ResolveContextIDFromSession(baseDir, cmd.Target); err == nil && liveCtx != contextID {
								log.Printf("session_toggle blocked: %q already owned by %s\n", cmd.Target, liveCtx)
								daemonEvents <- tui.DaemonEvent{
									Type:    "status_update",
									Message: fmt.Sprintf("BLOCKED: session %q already owned by daemon %s", cmd.Target, liveCtx),
								}
								continue
							}
						}

						daemonState.SetSessionEnabled(cmd.Target, newState)
						log.Printf("📮 postman: Session %s toggled to %v\n", cmd.Target, newState)

						// When enabling a session: create its inbox dirs and refresh node
						// discovery so cross-session panes become visible to send_ping.
						if newState {
							if err := config.CreateMultiSessionDirs(contextDir, cmd.Target); err != nil {
								log.Printf("⚠️  postman: warning: could not create dirs for session %s: %v\n", cmd.Target, err)
							} else {
								refreshed, _, _ := discovery.DiscoverNodesWithCollisions(baseDir, contextID, sessionName)
								edgeNodesRefresh := config.GetEdgeNodeNames(cfg.Edges)
								for nodeName := range refreshed {
									parts := strings.SplitN(nodeName, ":", 2)
									if !edgeNodesRefresh[parts[len(parts)-1]] {
										delete(refreshed, nodeName)
									}
								}
								sharedNodes.Store(&refreshed)
								log.Printf("postman: node snapshot refreshed after enabling session %s (%d nodes)\n", cmd.Target, len(refreshed))
							}
						}

						// Rebuild session list and send status update (all sessions, not just nodes)
						allSessions, _ := discovery.DiscoverAllSessions()
						updatedSessionList := session.BuildSessionList(nodes, allSessions, daemonState.GetConfiguredSessionEnabled)

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
					case "send_ping":
						cachedPtr := sharedNodes.Load()
						var freshNodes map[string]discovery.NodeInfo
						if cachedPtr != nil {
							cached := *cachedPtr
							freshNodes = make(map[string]discovery.NodeInfo, len(cached))
							for k, v := range cached {
								freshNodes[k] = v
							}
						}
						// Edge-filter and session-filter nodes (replicate startup logic, main.go:268-274)
						edgeNodesFilter := config.GetEdgeNodeNames(cfg.Edges)
						filterNodes := func(nodes map[string]discovery.NodeInfo) map[string]discovery.NodeInfo {
							for nodeName := range nodes {
								parts := strings.SplitN(nodeName, ":", 2)
								rawName := parts[len(parts)-1]
								if !edgeNodesFilter[rawName] {
									delete(nodes, nodeName)
								}
							}
							target := make(map[string]discovery.NodeInfo)
							for k, v := range nodes {
								if v.SessionName == cmd.Target {
									target[k] = v
								}
							}
							return target
						}
						targetNodes := filterNodes(freshNodes)
						if cachedPtr == nil || len(targetNodes) == 0 {
							if cachedPtr == nil {
								log.Printf("postman: PING skipped for session %s — no nodes discovered yet\n", cmd.Target)
							} else {
								log.Printf("postman: PING skipped for session %s — 0 nodes matched in session (total discovered across all sessions: %d)\n", cmd.Target, len(freshNodes))
							}
							daemonEvents <- tui.DaemonEvent{
								Type:    "status_update",
								Message: fmt.Sprintf("Nodes not yet discovered for session %s \u2014 press 'p' again", cmd.Target),
							}
							break
						}
						// Build active nodes from freshNodes (not stale startup nodes)
						activeNodes := make([]string, 0, len(freshNodes))
						for nodeName := range freshNodes {
							simpleName := ping.ExtractSimpleName(nodeName)
							activeNodes = append(activeNodes, simpleName)
						}
						// Send PING to each node in the target session concurrently.
						livenessMap := idleTracker.GetLivenessMap()
						pingAdjacency, _ := config.ParseEdges(cfg.Edges)
						if pingAdjacency == nil {
							pingAdjacency = map[string][]string{}
						}
						go func() {
							var wg sync.WaitGroup
							var successCount, failCount atomic.Int32
							for nodeName, nodeInfo := range targetNodes {
								wg.Add(1)
								go func(name string, info discovery.NodeInfo) {
									defer wg.Done()
									if err := ping.SendPingToNode(info, contextID, name,
										cfg.MessageTemplate, cfg, activeNodes, livenessMap,
										pingAdjacency, freshNodes); err != nil {
										log.Printf("❌ postman: PING to %s failed: %v\n", name, err)
										failCount.Add(1)
										daemonEvents <- tui.DaemonEvent{
											Type:    "message_received",
											Message: fmt.Sprintf("PING failed for %s: %v", name, err),
										}
									} else {
										log.Printf("📮 postman: PING sent to %s\n", name)
										successCount.Add(1)
										daemonEvents <- tui.DaemonEvent{
											Type:    "message_received",
											Message: fmt.Sprintf("PING sent to %s", name),
										}
									}
								}(nodeName, nodeInfo)
							}
							wg.Wait()
							total := int(successCount.Load()) + int(failCount.Load())
							daemonEvents <- tui.DaemonEvent{
								Type:    "status_update",
								Message: fmt.Sprintf("PING: %d/%d sent successfully", successCount.Load(), total),
							}
						}()
					case "create_draft":
						// Issue #230: TUI shortcut for create-draft
						err := runCreateDraft([]string{
							"--to", cmd.Value,
							"--context-id", contextID,
							"--session", cmd.Target,
							"--config", resolvedConfigPath,
						})
						if err != nil {
							daemonEvents <- tui.DaemonEvent{
								Type:    "message_received",
								Message: fmt.Sprintf("Draft failed: %v", err),
							}
						} else {
							daemonEvents <- tui.DaemonEvent{
								Type:    "message_received",
								Message: fmt.Sprintf("Draft created: to=%s session=%s", cmd.Value, cmd.Target),
							}
						}
					case "clear_edge_history":
						// Debounce 200ms to prevent TUI flicker from rapid session switches (#190)
						if edgeClearTimer != nil {
							edgeClearTimer.Stop()
						}
						edgeClearTimer = time.AfterFunc(200*time.Millisecond, func() {
							daemonState.ClearEdgeHistory()
							edgeList := daemonState.BuildEdgeList(cfg.Edges, cfg)
							daemonEvents <- tui.DaemonEvent{
								Type: "edge_update",
								Details: map[string]interface{}{
									"edges": edgeList,
								},
							}
							log.Println("postman: Edge history cleared (session switch)")
						})
					}
				}
			}
		})

		p := tea.NewProgram(tui.InitialModel(daemonEvents, tuiCommands, cfg, contextID))
		finalModel, err := p.Run()
		if err != nil {
			log.Printf("postman: TUI exited with error: %v\n", err)
			return fmt.Errorf("TUI error: %w", err)
		}
		// Issue #57: Log TUI exit reason
		if model, ok := finalModel.(tui.Model); ok {
			if !model.Quitting() {
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
	to := fs.String("to", "", "recipient node name (required unless --cross-context is set)")
	crossContext := fs.String("cross-context", "", "cross-context target as <contextID>:<node> (mutually exclusive with --to)")
	contextID := fs.String("context-id", "", "context ID (optional, auto-detect if not specified)")
	session := fs.String("session", "", "tmux session name (optional, auto-detect if in tmux)")
	configPath := fs.String("config", "", "path to config file (optional)")
	body := fs.String("body", "", "inline message body (replaces <!-- write here --> placeholder)")
	sendFlag := fs.Bool("send", false, "send immediately after creating draft (atomic)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *crossContext != "" && *to != "" {
		return fmt.Errorf("cannot combine --cross-context with --to")
	}
	if *crossContext == "" && *to == "" {
		return fmt.Errorf("--to is required")
	}
	if *sendFlag && *crossContext != "" {
		return fmt.Errorf("--send cannot be combined with --cross-context")
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	sender := config.GetTmuxPaneName()
	if sender == "" {
		return fmt.Errorf("sender auto-detection failed: set tmux pane title")
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

	// Issue #229: Resolve context-id — explicit flag takes priority, then auto-detect from session
	resolvedContextID, err := config.ResolveContextID(*contextID)
	if err != nil {
		// Auto-resolve: scan baseDir for exactly one context matching this session
		resolvedContextID, err = config.ResolveContextIDFromSession(baseDir, sessionName)
		if err != nil {
			return err
		}
	}

	// Issue #164: Handle --cross-context flag (cross-daemon delivery via diplomat drop dir)
	if *crossContext != "" {
		if cfg.DiplomatNode == "" {
			return fmt.Errorf("diplomat_node is not set in config; cannot use --cross-context")
		}
		targetContextID, targetNode, err := diplomat.ParseCrossContextTarget(*crossContext)
		if err != nil {
			return err
		}
		traceID, err := diplomat.GenerateTraceID()
		if err != nil {
			return fmt.Errorf("generating trace ID: %w", err)
		}
		dropDir := diplomat.DropDirPath(baseDir, targetContextID)
		if err := os.MkdirAll(dropDir, 0o700); err != nil {
			return fmt.Errorf("creating diplomat drop dir: %w", err)
		}
		now := time.Now()
		ts := now.Format("20060102-150405")
		filename := message.GenerateFilename(ts, sender, targetNode, sessionName)
		dropPath := filepath.Join(dropDir, filename)
		content := fmt.Sprintf("---\nmethod: message/send\nparams:\n  contextId: %s\n  sourceContextId: %s\n  sourceNode: %s\n  to: %s\n  crossContext: true\n  hop_count: 0\n  trace_id: %s\n---\n",
			targetContextID, resolvedContextID, cfg.DiplomatNode, targetNode, traceID)
		if err := os.WriteFile(dropPath, []byte(content), 0o600); err != nil {
			return fmt.Errorf("writing diplomat draft: %w", err)
		}
		fmt.Printf("Drop created: diplomat/%s/post/%s\n\nNote:\n  This file has been placed directly in the cross-context drop path.\n  No send step required.\n", targetContextID, filepath.Base(dropPath))
		return nil
	}

	draftDir := filepath.Join(baseDir, resolvedContextID, sessionName, "draft")

	if err := os.MkdirAll(draftDir, 0o700); err != nil {
		return fmt.Errorf("creating draft directory: %w", err)
	}

	now := time.Now()
	ts := now.Format("20060102-150405")
	filename := message.GenerateFilename(ts, sender, *to, sessionName)
	draftPath := filepath.Join(draftDir, filename)

	// Generate unique task ID
	taskID := fmt.Sprintf("%s-%04x", ts, now.UnixNano()%0xFFFF)

	// Use draft_template from config if available
	content := cfg.DraftTemplate
	if content == "" {
		// Fallback to minimal template
		content = "---\nmethod: message/send\nparams:\n  contextId: {context_id}\n  taskId: {task_id}\n  from: {sender}\n  to: {recipient}\n  timestamp: {timestamp}\nprotocol: tmux-a2a-postman --help\n---\n\nYou can only talk to: {can_talk_to}\n\n# Content\n\n"
	}

	// Build can_talk_to from adjacency
	adjacency, err := config.ParseEdges(cfg.Edges)
	if err != nil {
		return fmt.Errorf("parsing edges: %w", err)
	}
	canTalkTo := strings.Join(config.GetTalksTo(adjacency, sender), ", ")

	// Build variables map for template expansion
	vars := map[string]string{
		"context_id":     resolvedContextID,
		"task_id":        taskID,
		"sender":         sender,
		"recipient":      *to,
		"timestamp":      now.Format(time.RFC3339),
		"can_talk_to":    canTalkTo,
		"session_dir":    filepath.Join(baseDir, resolvedContextID, sessionName),
		"reply_command":  expandReplyCommand(cfg.ReplyCommand, resolvedContextID),
		"template":       getNodeTemplate(cfg, *to),
		"session_name":   sessionName,
		"sender_pane_id": config.GetTmuxPaneID(),
		// Backward compatibility
		"from": sender,
		"to":   *to,
	}

	// Expand template with variables and shell commands
	timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
	content = template.ExpandTemplate(content, vars, timeout)

	if *body != "" {
		content = strings.ReplaceAll(content, "<!-- write here -->", *body)
	}

	if err := os.WriteFile(draftPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("writing draft: %w", err)
	}

	if *sendFlag {
		if *body == "" {
			fmt.Fprintf(os.Stderr, "warning: --send without --body: message will contain '<!-- write here -->' placeholder\n")
		}
		postDir := filepath.Clean(filepath.Join(draftDir, "..", "post"))
		if err := os.MkdirAll(postDir, 0o700); err != nil {
			return fmt.Errorf("creating post/ directory: %w", err)
		}
		dst := filepath.Join(postDir, filename)
		if err := os.Rename(draftPath, dst); err != nil {
			return fmt.Errorf("sending draft: %w", err)
		}
		fmt.Printf("Sent: %s\n", filename)
		return nil
	} else if *body != "" {
		fmt.Printf("Draft created: %s\n\nNext steps:\n  1. tmux-a2a-postman send %s\n", filename, filename)
		return nil
	}
	fmt.Printf("Draft created: %s\n\nNext steps:\n  1. Edit ## Content section in the draft file\n  2. tmux-a2a-postman send %s\n", filename, filename)
	return nil
}

// expandReplyCommand substitutes {context_id} in the reply command template
func expandReplyCommand(replyCmd string, contextID string) string {
	return strings.ReplaceAll(replyCmd, "{context_id}", contextID)
}

// getNodeTemplate retrieves the template for a given node from config
// Returns empty string if node or template is not found (nil-safe)
func getNodeTemplate(cfg *config.Config, nodeName string) string {
	if cfg == nil || cfg.Nodes == nil {
		return ""
	}
	nodeConfig, exists := cfg.Nodes[nodeName]
	if !exists {
		return ""
	}
	return nodeConfig.Template
}

// runGetSessionStatusOneline shows all tmux sessions' pane status in one line.
// Output format: [0]window0_panes:window1_panes:... [1]window0_panes:...
// TTY output (interactive terminal): ANSI-colored dots (● green/blue/yellow/red)
// Non-TTY output (tmux #(), pipes): plain emoji (🟢/🔵/🟡/🔴)
// Pane status: active=green, composing=blue, idle/spinning=yellow, stale=red
// Issue #120: Refactored to use idle.go activity detection instead of #{pane_active}
// Issue #275: TTY detection so tmux status-right receives plain emoji, not ANSI codes
func runGetSessionStatusOneline(args []string) error {
	// Load config to get base directory
	cfg, err := config.LoadConfig("")
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	// Find the most recently started live context for the current tmux session.
	// Context directories are named session-YYYYMMDD-HHMMSS-XXXX; lexicographic
	// descending sort gives newest first.
	statusPriority := map[string]int{"active": 2, "idle": 1, "stale": 0}
	paneActivity := make(map[string]string)

	contextDirs, _ := filepath.Glob(filepath.Join(baseDir, "session-*"))
	sort.Sort(sort.Reverse(sort.StringSlice(contextDirs)))

	var liveStateFiles []string
	for _, ctxDir := range contextDirs {
		fi, err := os.Stat(ctxDir)
		if err != nil || !fi.IsDir() {
			continue
		}
		ctxName := filepath.Base(ctxDir)
		// Scan all session subdirs for any live postman.pid.
		sessionEntries, _ := os.ReadDir(ctxDir)
		for _, se := range sessionEntries {
			if !se.IsDir() {
				continue
			}
			if config.IsSessionPIDAlive(baseDir, ctxName, se.Name()) {
				liveStateFiles = append(liveStateFiles, filepath.Join(ctxDir, "pane-activity.json"))
				break
			}
		}
	}

	if len(liveStateFiles) == 0 {
		return nil // no live context found
	}

	for _, liveStateFile := range liveStateFiles {
		stateData, err := os.ReadFile(liveStateFile)
		if err == nil {
			// Issue #123: Dual-format reader — supports both legacy map[string]string and
			// new map[string]PaneActivityExport formats.
			var rawMap map[string]json.RawMessage
			if jsonErr := json.Unmarshal(stateData, &rawMap); jsonErr == nil {
				for paneID, raw := range rawMap {
					var status string
					// Try legacy format: plain string value
					if err := json.Unmarshal(raw, &status); err != nil {
						// Try new format: PaneActivityExport struct
						var export idle.PaneActivityExport
						if err := json.Unmarshal(raw, &export); err != nil {
							continue // skip on schema mismatch
						}
						status = export.Status
					}
					if status == "" {
						continue
					}
					existing, exists := paneActivity[paneID]
					if !exists || statusPriority[status] > statusPriority[existing] {
						paneActivity[paneID] = status // higher priority wins on conflict
					}
				}
			}
		}
	}

	// Build edge node set and pane title map for filtering
	edgeNodes := config.GetEdgeNodeNames(cfg.Edges)
	paneTitleOutput, _ := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id} #{pane_title}").Output()
	paneTitles := make(map[string]string) // paneID -> paneTitle
	for _, line := range strings.Split(strings.TrimSpace(string(paneTitleOutput)), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), " ", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			paneTitles[parts[0]] = parts[1]
		}
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

	// Pane status styles matching tui.go style definitions exactly (Issue #120).
	activeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	composingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("33"))
	idleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("226"))
	staleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

	// Issue #275: Use plain emoji when stdout is not a TTY (e.g. tmux status-right #()).
	isTerminal := term.IsTerminal(os.Stdout.Fd())

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
				// Edge filter: only show panes in edge list
				if !edgeNodes[paneTitles[paneID]] {
					continue
				}
				// Check pane status: ANSI colors for TTY, plain emoji for non-TTY (Issue #275).
				if isTerminal {
					switch paneActivity[paneID] {
					case "active", "user_input":
						paneStatuses += activeStyle.Render("●")
					case "composing":
						paneStatuses += composingStyle.Render("●")
					case "idle", "spinning":
						paneStatuses += idleStyle.Render("●")
					default:
						paneStatuses += staleStyle.Render("●")
					}
				} else {
					switch paneActivity[paneID] {
					case "active", "user_input":
						paneStatuses += "🟢"
					case "composing":
						paneStatuses += "🔵"
					case "idle", "spinning":
						paneStatuses += "🟡"
					default:
						paneStatuses += "🔴"
					}
				}
			}

			if paneStatuses != "" {
				windowStatuses = append(windowStatuses, paneStatuses)
			}
		}

		// Build session status: (n)window0:window1:...
		if len(windowStatuses) > 0 {
			sessionStatus := fmt.Sprintf("[%d]%s", sessionIdx, strings.Join(windowStatuses, ":"))
			output = append(output, sessionStatus)
		}
	}

	if len(output) > 0 {
		fmt.Println(strings.Join(output, " "))
	}
	return nil
}

// cleanupStaleInbox moves all messages from inbox/ subdirectories to read/.
// This cleans up stale messages from previous sessions.
func cleanupStaleInbox(inboxDir, readDir string) error {
	// Ensure read/ directory exists
	if err := os.MkdirAll(readDir, 0o700); err != nil {
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

// resolveInboxPath resolves the inbox path for the current node (#196).
func resolveInboxPath(args []string) (string, error) {
	fs := flag.NewFlagSet("inbox-resolve", flag.ContinueOnError)
	contextID := fs.String("context-id", "", "context ID")
	configPath := fs.String("config", "", "path to config file")
	if err := fs.Parse(args); err != nil {
		return "", err
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return "", fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	resolvedContextID, err := config.ResolveContextID(*contextID)
	if err != nil {
		return "", fmt.Errorf("resolving context ID: %w", err)
	}

	nodeName := config.GetTmuxPaneName()
	if nodeName == "" {
		return "", fmt.Errorf("node name auto-detection failed: set tmux pane title")
	}

	sessionName := config.GetTmuxSessionName()
	if sessionName == "" {
		return "", fmt.Errorf("tmux session name required (run inside tmux)")
	}
	sessionName = filepath.Base(sessionName)

	inboxPath := filepath.Join(baseDir, resolvedContextID, sessionName, "inbox", nodeName)
	return inboxPath, nil
}

// runCount prints the number of unread inbox messages for the current node (#196).
func runCount(args []string) error {
	inboxPath, err := resolveInboxPath(args)
	if err != nil {
		return err
	}
	msgs := message.ScanInboxMessages(inboxPath)
	fmt.Println(len(msgs))
	return nil
}

// runRead lists inbox message file paths for the current node (#196).
func runRead(args []string) error {
	inboxPath, err := resolveInboxPath(args)
	if err != nil {
		return err
	}
	msgs := message.ScanInboxMessages(inboxPath)
	if len(msgs) == 0 {
		return nil
	}
	for _, msg := range msgs {
		fmt.Println(msg.Filename)
	}
	return nil
}

// runShowInboxMessage prints the content of a named inbox message to stdout.
// The filename is resolved by globbing all inbox/ directories under baseDir.
func runShowInboxMessage(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: show-inbox-message <filename>")
	}
	filename := args[0]
	if strings.ContainsAny(filename, "/\\") {
		return fmt.Errorf("show-inbox-message: filename must not contain path separators")
	}

	cfg, err := config.LoadConfig("")
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	sessionName := config.GetTmuxSessionName()
	if sessionName == "" {
		return fmt.Errorf("tmux session name required (run inside tmux)")
	}
	sessionName = filepath.Base(sessionName)

	pattern := filepath.Join(baseDir, "*", sessionName, "inbox", "*", filename)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("globbing for %s: %w", filename, err)
	}
	switch len(matches) {
	case 0:
		return fmt.Errorf("error: %s not found in any inbox/ directory", filename)
	case 1:
		data, err := os.ReadFile(matches[0])
		if err != nil {
			return fmt.Errorf("reading %s: %w", filename, err)
		}
		fmt.Print(string(data))
		return nil
	default:
		return fmt.Errorf("error: %s found in multiple inbox/ directories: %v", filename, matches)
	}
}

// runListArchivedMessages prints filenames of all messages in read/, one per line.
func runListArchivedMessages(args []string) error {
	// Derive read/ path from resolveInboxPath result
	// inboxPath = {base}/{contextID}/{session}/inbox/{node}
	// readPath  = {base}/{contextID}/{session}/read/
	inboxPath, err := resolveInboxPath(args)
	if err != nil {
		return err
	}
	readPath := filepath.Join(filepath.Dir(filepath.Dir(inboxPath)), "read")

	entries, err := os.ReadDir(readPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no read/ dir yet — empty output is valid
		}
		return fmt.Errorf("reading archived messages: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Println(name)
	}
	return nil
}

// runShowArchivedMessage prints the content of a named archived (read/) message.
func runShowArchivedMessage(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: show-archived-message <filename>")
	}
	filename := args[0]
	if strings.ContainsAny(filename, "/\\") {
		return fmt.Errorf("show-archived-message: filename must not contain path separators")
	}

	cfg, err := config.LoadConfig("")
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	sessionName := config.GetTmuxSessionName()
	if sessionName == "" {
		return fmt.Errorf("tmux session name required (run inside tmux)")
	}
	sessionName = filepath.Base(sessionName)

	pattern := filepath.Join(baseDir, "*", sessionName, "read", filename)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("globbing for %s: %w", filename, err)
	}
	switch len(matches) {
	case 0:
		return fmt.Errorf("error: %s not found in any read/ directory", filename)
	case 1:
		data, err := os.ReadFile(matches[0])
		if err != nil {
			return fmt.Errorf("reading %s: %w", filename, err)
		}
		fmt.Print(string(data))
		return nil
	default:
		return fmt.Errorf("error: %s found in multiple read/ directories: %v", filename, matches)
	}
}

// runArchive moves inbox message files to read/ to mark them as read.
// If a plain filename is given (no path separators), the file is located by
// globbing inbox/ directories under baseDir. Full paths are accepted for
// backward compatibility.
func runArchive(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: archive <filename> [filename...]")
	}

	cfg, err := config.LoadConfig("")
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	baseDir := config.ResolveBaseDir(cfg.BaseDir)
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return fmt.Errorf("resolving baseDir: %w", err)
	}

	for _, file := range args {
		// Wildcard check applies to base filename in all cases (mirrors runSend behavior).
		if strings.ContainsAny(filepath.Base(file), "*?[]") {
			return fmt.Errorf("archive: %q must not contain wildcards", file)
		}

		resolvedPath := file
		if !strings.ContainsAny(file, "/\\") {
			// Plain filename: locate by globbing all inbox/ directories.
			// path: {baseDir}/{contextID}/{sessionName}/inbox/{nodeName}/{filename}
			pattern := filepath.Join(baseDir, "*", "*", "inbox", "*", file)
			matches, err := filepath.Glob(pattern)
			if err != nil {
				return fmt.Errorf("globbing for %s: %w", file, err)
			}
			switch len(matches) {
			case 0:
				return fmt.Errorf("archive: %q not found in any inbox/ directory", file)
			case 1:
				resolvedPath = matches[0]
			default:
				return fmt.Errorf("archive: %q found in multiple inbox/ directories: %v", file, matches)
			}
		}
		abs, err := filepath.Abs(resolvedPath)
		if err != nil {
			return fmt.Errorf("resolving path %s: %w", resolvedPath, err)
		}
		// Security: reject paths outside postman base directory (prevent path traversal).
		if !strings.HasPrefix(abs+string(filepath.Separator), absBase+string(filepath.Separator)) {
			return fmt.Errorf("archive: %q is outside postman base directory", resolvedPath)
		}
		// Validate inbox structure: path must end with .../inbox/{nodeName}/{filename}.
		parts := strings.Split(filepath.ToSlash(abs), "/")
		if len(parts) < 5 || parts[len(parts)-3] != "inbox" {
			return fmt.Errorf("archive: %q is not an inbox/ path", resolvedPath)
		}
		// inbox path: {base}/{contextID}/{sessionName}/inbox/{nodeName}/{msg}.md
		// read/  dir: {base}/{contextID}/{sessionName}/read/
		readDir := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(abs))), "read")
		if err := os.MkdirAll(readDir, 0o700); err != nil {
			return fmt.Errorf("creating read directory: %w", err)
		}
		dst := filepath.Join(readDir, filepath.Base(abs))
		if err := os.Rename(abs, dst); err != nil {
			return fmt.Errorf("archiving %s: %w", resolvedPath, err)
		}
		sender := extractSenderFromFile(dst)
		if sender != "" {
			fmt.Printf("Next steps: Reply with tmux-a2a-postman create-draft --to %s\n", sender)
		}
	}
	return nil
}

// extractSenderFromFile reads the YAML front matter of a message file and returns
// the value of the params.from field. Returns empty string on any error or if not found.
func extractSenderFromFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	inFrontMatter := false
	for _, line := range lines {
		if line == "---" {
			if !inFrontMatter {
				inFrontMatter = true
				continue
			}
			break // second --- closes front matter
		}
		if !inFrontMatter {
			continue
		}
		// Match "  from: <value>" (2-space indent under params:)
		if strings.HasPrefix(line, "  from: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "  from: "))
		}
	}
	return ""
}

// runSend moves a draft file to post/ to submit it for delivery.
// The argument is a bare filename (no path); the file is located by globbing draft/ directories.
func runSend(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: send <filename> [filename...]")
	}

	cfg, err := config.LoadConfig("")
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	for _, filename := range args {
		if strings.ContainsAny(filename, "/\\*?[]") {
			return fmt.Errorf("send: %q must be a plain filename (no path separators or wildcards)", filename)
		}

		pattern := filepath.Join(baseDir, "*", "*", "draft", filename)
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return fmt.Errorf("globbing for %s: %w", filename, err)
		}
		switch len(matches) {
		case 0:
			return fmt.Errorf("send: %q not found in any draft/ directory", filename)
		case 1:
			// OK
		default:
			return fmt.Errorf("send: %q found in multiple draft/ directories: %v", filename, matches)
		}

		match := matches[0]
		// match = {baseDir}/{contextID}/{sessionName}/draft/{filename}
		// two Dir() calls yield {baseDir}/{contextID}/{sessionName}
		sessionDir := filepath.Dir(filepath.Dir(match))
		postDir := filepath.Join(sessionDir, "post")
		if err := os.MkdirAll(postDir, 0o700); err != nil {
			return fmt.Errorf("creating post/ directory: %w", err)
		}
		dst := filepath.Join(postDir, filename)
		if err := os.Rename(match, dst); err != nil {
			return fmt.Errorf("sending %s: %w", filename, err)
		}
		fmt.Printf("Sent: %s\n", filename)
	}
	return nil
}

// printDoubleDashDefaults prints flag defaults with -- prefix (POSIX style).
func printDoubleDashDefaults(fs *flag.FlagSet) {
	fs.VisitAll(func(f *flag.Flag) {
		typeName, usage := flag.UnquoteUsage(f)
		var line string
		if typeName == "" {
			line = fmt.Sprintf("  --%s", f.Name)
		} else {
			line = fmt.Sprintf("  --%s %s", f.Name, typeName)
		}
		fmt.Fprintf(os.Stderr, "%s\n\t\t%s\n", line, usage)
	})
}

// runGetSessionHealth prints session health: node count, inbox/waiting counts (#220).
func runGetSessionHealth(args []string) error {
	fs := flag.NewFlagSet("get-session-health", flag.ExitOnError)
	contextID := fs.String("context-id", "", "Context ID (optional, auto-resolved from tmux session)")
	configPath := fs.String("config", "", "Config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	// Issue #249: auto-resolve --context-id if not provided
	resolvedContextID, err := config.ResolveContextID(*contextID)
	if err != nil {
		sessionName := config.GetTmuxSessionName()
		if sessionName == "" {
			return fmt.Errorf("--context-id is required (not in tmux)")
		}
		resolvedContextID, err = config.ResolveContextIDFromSession(baseDir, sessionName)
		if err != nil {
			return err
		}
	}

	sessionDir := filepath.Join(baseDir, resolvedContextID)

	// Discover nodes
	nodes, _, err := discovery.DiscoverNodesWithCollisions(baseDir, resolvedContextID, config.GetTmuxSessionName())
	if err != nil {
		return fmt.Errorf("discovering nodes: %w", err)
	}
	edgeNodes := config.GetEdgeNodeNames(cfg.Edges)

	type nodeHealth struct {
		Name         string `json:"name"`
		InboxCount   int    `json:"inbox_count"`
		WaitingCount int    `json:"waiting_count"`
	}

	var healthEntries []nodeHealth
	for nodeName := range nodes {
		parts := strings.SplitN(nodeName, ":", 2)
		rawName := parts[len(parts)-1]
		if !edgeNodes[rawName] {
			continue
		}
		inboxDir := filepath.Join(sessionDir, "inbox", rawName)
		inboxEntries, _ := os.ReadDir(inboxDir)
		inboxCount := 0
		for _, e := range inboxEntries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				inboxCount++
			}
		}
		waitingDir := filepath.Join(sessionDir, "waiting")
		waitingEntries, _ := os.ReadDir(waitingDir)
		waitingCount := 0
		for _, e := range waitingEntries {
			if !e.IsDir() && strings.Contains(e.Name(), "-to-"+rawName) {
				waitingCount++
			}
		}
		healthEntries = append(healthEntries, nodeHealth{
			Name:         rawName,
			InboxCount:   inboxCount,
			WaitingCount: waitingCount,
		})
	}

	sort.Slice(healthEntries, func(i, j int) bool {
		return healthEntries[i].Name < healthEntries[j].Name
	})

	result := struct {
		ContextID string       `json:"context_id"`
		NodeCount int          `json:"node_count"`
		Nodes     []nodeHealth `json:"nodes"`
	}{
		ContextID: resolvedContextID,
		NodeCount: len(healthEntries),
		Nodes:     healthEntries,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

func runResend(args []string) error {
	fs := flag.NewFlagSet("resend", flag.ContinueOnError)
	contextID := fs.String("context-id", "", "context ID (optional, auto-resolved from tmux session)")
	file := fs.String("file", "", "path to dead-letter file (required)")
	configPath := fs.String("config", "", "path to config file (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return fmt.Errorf("--file is required")
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	// Verify dead-letter file exists
	absFile, err := filepath.Abs(*file)
	if err != nil {
		return fmt.Errorf("resolving file path: %w", err)
	}
	if _, err := os.Stat(absFile); err != nil {
		return fmt.Errorf("dead-letter file not found: %w", err)
	}

	// Find session directory
	sessionName := config.GetTmuxSessionName()
	if sessionName == "" {
		return fmt.Errorf("must be run inside tmux")
	}
	sessionName = filepath.Base(sessionName)

	// Issue #249: auto-resolve --context-id if not provided
	resolvedContextID, err := config.ResolveContextID(*contextID)
	if err != nil {
		resolvedContextID, err = config.ResolveContextIDFromSession(baseDir, sessionName)
		if err != nil {
			return err
		}
	}

	sessionDir := filepath.Join(baseDir, resolvedContextID, sessionName)
	postDir := filepath.Join(sessionDir, "post")
	if err := os.MkdirAll(postDir, 0o700); err != nil {
		return fmt.Errorf("creating post/ directory: %w", err)
	}

	// Strip dead-letter suffix (-dl-*.md -> .md) for redelivery filename
	baseName := filepath.Base(absFile)
	cleanName := message.StripDeadLetterSuffix(baseName)

	dst := filepath.Join(postDir, cleanName)
	if err := os.Rename(absFile, dst); err != nil {
		return fmt.Errorf("moving to post/: %w", err)
	}

	fmt.Printf("Resent: %s\n", baseName)
	return nil
}

// runGetContextID prints the live context ID for the current tmux session.
// Issue #249: zero-argument discovery primitive for AI agents.
func runGetContextID(args []string) error {
	fs := flag.NewFlagSet("get-context-id", flag.ContinueOnError)
	sessionFlag := fs.String("session", "", "tmux session name (optional, auto-detect if in tmux)")
	configPath := fs.String("config", "", "path to config file (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	sessionName := *sessionFlag
	if sessionName == "" {
		sessionName = config.GetTmuxSessionName()
	}
	if sessionName == "" {
		return fmt.Errorf("--session is required (or run inside tmux)")
	}
	sessionName = filepath.Base(sessionName)

	contextID, err := config.ResolveContextIDFromSession(baseDir, sessionName)
	if err != nil {
		return err
	}
	fmt.Println(contextID)
	return nil
}

// runStop gracefully stops the running postman daemon for this tmux session.
// Sends SIGTERM and polls until the process exits or --timeout expires.
func runStop(args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	sessionFlag := fs.String("session", "", "tmux session name (auto-detect if in tmux)")
	configPath := fs.String("config", "", "path to config file")
	timeoutSecs := fs.Int("timeout", 10, "seconds to wait for daemon to exit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	sessionName := *sessionFlag
	if sessionName == "" {
		sessionName = config.GetTmuxSessionName()
	}
	if sessionName == "" {
		return fmt.Errorf("--session is required (or run inside tmux)")
	}
	sessionName = filepath.Base(sessionName)

	contextID, err := config.ResolveContextIDFromSession(baseDir, sessionName)
	if err != nil {
		// "no active postman found" is benign — nothing to stop.
		// "constraint violation" (multiple live daemons) is a real error.
		if strings.Contains(err.Error(), "no active postman found") {
			fmt.Println("postman: no daemon running")
			return nil
		}
		return err
	}

	// Verify the resolved context has a live daemon for THIS session specifically.
	// ResolveContextIDFromSession scans all subdirs — the live PID may be for a
	// different session within the same context. Confirm before signalling.
	if !config.IsSessionPIDAlive(baseDir, contextID, sessionName) {
		fmt.Println("postman: no daemon running for this session")
		return nil
	}

	pidPath := filepath.Join(baseDir, contextID, sessionName, "postman.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return fmt.Errorf("reading pid file %s: %w", pidPath, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return fmt.Errorf("invalid pid in %s", pidPath)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("finding process %d: %w", pid, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("sending SIGTERM to pid %d: %w", pid, err)
	}

	deadline := time.Now().Add(time.Duration(*timeoutSecs) * time.Second)
	for time.Now().Before(deadline) {
		if !config.IsSessionPIDAlive(baseDir, contextID, sessionName) {
			fmt.Printf("postman: daemon (pid %d) stopped\n", pid)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf(
		"daemon (pid %d) did not stop within %ds; try: kill -9 %d",
		pid, *timeoutSecs, pid,
	)
}

func runHelp(args []string) {
	topics := []string{"messaging", "directories", "config", "commands"}
	printTopicList := func() {
		fmt.Println("Topics:")
		for _, t := range topics {
			fmt.Printf("  %-14s  tmux-a2a-postman help %s\n", t, t)
		}
	}

	if len(args) == 0 {
		fmt.Println("tmux-a2a-postman — A2A message routing daemon for tmux panes")
		fmt.Println("")
		fmt.Println("AI agents use this tool to exchange structured messages via the filesystem.")
		fmt.Println("Each agent reads its inbox, replies via draft/, and the daemon routes messages.")
		fmt.Println("")
		fmt.Println("Quick Start:")
		fmt.Println("  1. Start daemon:   tmux-a2a-postman start")
		fmt.Println("  2. Create draft:   tmux-a2a-postman create-draft --to <node>")
		fmt.Println("  3. Edit draft:     $EDITOR draft/<filename>.md")
		fmt.Println("  4. Send message:   tmux-a2a-postman send <filename>.md")
		fmt.Println("  5. Daemon routes the file from post/ to recipient's inbox/{sender}/")
		fmt.Println("  6. Recipient reads file and runs: tmux-a2a-postman archive <filename>")
		fmt.Println("")
		fmt.Println("Key Concepts:")
		fmt.Println("  Node       An AI agent identified by its tmux pane title.")
		fmt.Println("  Edge       A bidirectional routing rule between two nodes (configured in edges).")
		fmt.Println("  Envelope   YAML frontmatter at the top of each message file:")
		fmt.Println("               ---")
		fmt.Println("               method: message/send")
		fmt.Println("               params:")
		fmt.Println("                 contextId: <context ID>")
		fmt.Println("                 from: <sender>")
		fmt.Println("                 to: <recipient>")
		fmt.Println("                 timestamp: <ISO 8601>")
		fmt.Println("               ---")
		fmt.Println("  Context    A session namespace. All paths are scoped under {baseDir}/{contextId}/.")
		fmt.Println("")
		fmt.Println("Directory Layout:")
		fmt.Println("  {baseDir}/{contextId}/{sessionName}/")
		fmt.Println("    draft/          Write new messages here")
		fmt.Println("    post/           Move drafts here to send")
		fmt.Println("    inbox/{node}/   Daemon delivers messages here")
		fmt.Println("    read/           Move messages here after reading")
		fmt.Println("    dead-letter/    Unroutable messages (bad recipient or edge violation)")
		fmt.Println("    waiting/        Per-node waiting state files")
		fmt.Println("")
		fmt.Println("Commands:")
		fmt.Println("  start                      Start the daemon (TUI dashboard)")
		fmt.Println("  stop                       Stop the running daemon for this tmux session")
		fmt.Println("  create-draft               Create a new message draft")
		fmt.Println("  send <filename>            Move draft to post/ to send it")
		fmt.Println("  resend                     Re-send a dead-letter message")
		fmt.Println("  count                      Count unread inbox messages")
		fmt.Println("  read                       List inbox message file paths")
		fmt.Println("  archive <filename> [filename...]   Move inbox messages to read/")
		fmt.Println("  get-session-status-oneline Print pane status (emoji in pipes/tmux #(), ANSI in TTY)")
		fmt.Println("  get-session-health         Print session health per node")
		fmt.Println("  help [topic]               Show help (topics: messaging, directories, config, commands)")
		fmt.Println("")
		printTopicList()
		fmt.Println("")
		fmt.Println("Run `tmux-a2a-postman help <topic>` for detailed information.")
		return
	}

	topic := args[0]
	switch topic {
	case "messaging":
		fmt.Println("Messaging — message lifecycle and envelope format")
		fmt.Println("")
		fmt.Println("Lifecycle:")
		fmt.Println("  1. Agent runs: tmux-a2a-postman create-draft --to <node>")
		fmt.Println("  2. Agent runs: tmux-a2a-postman send <filename>")
		fmt.Println("  3. Daemon picks up file from post/, routes to inbox/{node}/ of recipient")
		fmt.Println("  4. Recipient reads from inbox/{node}/, then runs: tmux-a2a-postman archive <filename>")
		fmt.Println("  5. Unknown recipients: file moved to dead-letter/")
		fmt.Println("")
		fmt.Println("Envelope format (YAML frontmatter):")
		fmt.Println("  ---")
		fmt.Println("  method: message/send")
		fmt.Println("  params:")
		fmt.Println("    contextId: <context ID>")
		fmt.Println("    taskId: <optional task ID>")
		fmt.Println("    from: <sender node name>")
		fmt.Println("    to: <recipient node name>")
		fmt.Println("    timestamp: <ISO 8601 timestamp>")
		fmt.Println("  ---")
		fmt.Println("")
		fmt.Println("Reply workflow:")
		fmt.Println("  1. Run: tmux-a2a-postman create-draft --context-id <id> --to <recipient>")
		fmt.Println("  2. Edit the generated file in draft/")
		fmt.Println("  3. Send: tmux-a2a-postman send <filename>")
		fmt.Println("")
		fmt.Println("Sender is auto-detected from the tmux pane title (no --from flag).")
	case "directories":
		fmt.Println("Directories — session directory layout")
		fmt.Println("")
		fmt.Println("Base directory resolution (in priority order):")
		fmt.Println("  1. $POSTMAN_HOME environment variable")
		fmt.Println("  2. base_dir field in config file")
		fmt.Println("  3. $XDG_STATE_HOME/tmux-a2a-postman (default)")
		fmt.Println("     (falls back to ~/.local/state/tmux-a2a-postman)")
		fmt.Println("")
		fmt.Println("Layout:")
		fmt.Println("  {baseDir}/")
		fmt.Println("  └── {contextId}/")
		fmt.Println("      └── {sessionName}/")
		fmt.Println("          ├── draft/          # agent writes drafts here")
		fmt.Println("          ├── post/           # agent moves drafts here to send")
		fmt.Println("          ├── inbox/")
		fmt.Println("          │   └── {node}/     # daemon delivers messages here")
		fmt.Println("          ├── read/           # agent moves messages here after reading")
		fmt.Println("          ├── dead-letter/    # unroutable messages land here")
		fmt.Println("          ├── capture/        # pane capture snapshots")
		fmt.Println("          ├── waiting/        # per-node waiting state files")
		fmt.Println("          └── boilerplate/    # auto-generated response templates")
	case "config":
		fmt.Println("Config — key configuration fields")
		fmt.Println("")
		fmt.Println("Config file: $XDG_CONFIG_HOME/tmux-a2a-postman/postman.toml")
		fmt.Println("            (falls back to ~/.config/tmux-a2a-postman/postman.toml)")
		fmt.Println("")
		fmt.Println("Key fields and defaults:")
		fmt.Println("  scan_interval      float64  1.0      Seconds between post/ scans")
		fmt.Println("  enter_delay        float64  0.5      Delay before sending tmux keys")
		fmt.Println("  tmux_timeout       float64  5.0      Timeout for tmux commands")
		fmt.Println("  startup_delay      float64  2.0      Delay before first scan")
		fmt.Println("  reminder_interval  float64  0.0      Idle reminder interval (0=disabled)")
		fmt.Println("  inbox_unread_threshold  int  3       Alert threshold for unread messages")
		fmt.Println("  edges              []string []       Allowed node-to-node routing pairs")
		fmt.Println("  edge_violation_warning_mode  string  \"compact\"  Warning verbosity")
		fmt.Println("")
		fmt.Println("Edge syntax (both separators create bidirectional routes):")
		fmt.Println("  edges = [")
		fmt.Println("    \"node-a -- node-b\",   # bidirectional: a<->b")
		fmt.Println("    \"node-b --> node-c\",  # also bidirectional: b<->c")
		fmt.Println("  ]")
		fmt.Println("")
		fmt.Println("Per-node configuration (TOML section):")
		fmt.Println("  [node-name]")
		fmt.Println("  role = \"description of node role\"")
		fmt.Println("  template = \"role template content\"")
		fmt.Println("  on_join = \"message sent when node joins\"")
	case "commands":
		fmt.Println("Commands — detailed command reference")
		fmt.Println("")
		fmt.Println("start")
		fmt.Println("  Start the tmux-a2a-postman daemon.")
		fmt.Println("  Flags:")
		fmt.Println("    --context-id <id>    Context ID (auto-generated if omitted)")
		fmt.Println("    --config <path>      Config file path (auto-detected from XDG_CONFIG_HOME)")
		fmt.Println("    --log-file <path>    Log file path (default: {baseDir}/{contextId}/postman.log)")
		fmt.Println("    --no-tui             Run without the TUI dashboard")
		fmt.Println("")
		fmt.Println("stop")
		fmt.Println("  Stop the running daemon for the current tmux session.")
		fmt.Println("  Sends SIGTERM and waits up to --timeout seconds (default 10) for exit.")
		fmt.Println("  Exits 0 if no daemon is running (idempotent).")
		fmt.Println("  Flags:")
		fmt.Println("    --session <name>     tmux session name (optional, auto-detected)")
		fmt.Println("    --config <path>      Config file path (optional)")
		fmt.Println("    --timeout <secs>     Seconds to wait for daemon exit (default 10)")
		fmt.Println("  NOTE: --timeout default (10s) is chosen to exceed the default tmux_timeout")
		fmt.Println("        (5s) plus goroutine drain margin. Increase if your config sets")
		fmt.Println("        tmux_timeout > 8s.")
		fmt.Println("")
		fmt.Println("create-draft")
		fmt.Println("  Create a new message draft file in the session draft/ directory.")
		fmt.Println("  Sender is auto-detected from the tmux pane title.")
		fmt.Println("  Flags:")
		fmt.Println("    --to <node>          Recipient node name (required unless --cross-context is set)")
		fmt.Println("    --cross-context <contextID>:<node>")
		fmt.Println("                         Cross-context target (mutually exclusive with --to;")
		fmt.Println("                         requires diplomat_node set in config)")
		fmt.Println("    --context-id <id>    Context ID (optional, auto-detected)")
		fmt.Println("    --session <name>     tmux session name (optional, auto-detected)")
		fmt.Println("    --config <path>      Config file path (optional)")
		fmt.Println("  NOTE: There is no --from flag. Sender comes from the tmux pane title.")
		fmt.Println("")
		fmt.Println("send <filename> [filename...]")
		fmt.Println("  Move a draft file to post/ to submit it for delivery.")
		fmt.Println("  The filename is matched by glob across all draft/ directories; no path needed.")
		fmt.Println("  Typical workflow:")
		fmt.Println("    1. tmux-a2a-postman create-draft --to <node>  # creates draft, prints filename")
		fmt.Println("    2. $EDITOR draft/<filename>.md                 # edit the draft")
		fmt.Println("    3. tmux-a2a-postman send <filename>.md         # submit for delivery")
		fmt.Println("")
		fmt.Println("get-session-status-oneline")
		fmt.Println("  Print all sessions' pane status on a single line.")
		fmt.Println("  TTY (interactive terminal): ANSI-colored dots matching the TUI.")
		fmt.Println("  Non-TTY (e.g. tmux status-right #()): plain emoji 🟢🔵🟡🔴.")
		fmt.Println("")
		fmt.Println("get-context-id")
		fmt.Println("  Print the live context ID for the current tmux session.")
		fmt.Println("  Useful for AI agents that need to discover context ID without flags.")
		fmt.Println("  Flags:")
		fmt.Println("    --session <name>     tmux session name (optional, auto-detected)")
		fmt.Println("    --config <path>      Config file path (optional)")
		fmt.Println("")
		fmt.Println("get-session-health")
		fmt.Println("  Print session health: node count, inbox/waiting counts per node.")
		fmt.Println("  Flags:")
		fmt.Println("    --context-id <id>    Context ID (optional, auto-detected from tmux session)")
		fmt.Println("    --config <path>      Config file path (optional)")
		fmt.Println("")
		fmt.Println("resend")
		fmt.Println("  Re-send a dead-letter message by moving it back to post/.")
		fmt.Println("  Strips -dl-{reason} suffix from filename for redelivery.")
		fmt.Println("  Flags:")
		fmt.Println("    --context-id <id>    Context ID (optional, auto-detected from tmux session)")
		fmt.Println("    --file <path>        Path to dead-letter file (required)")
		fmt.Println("    --config <path>      Config file path (optional)")
		fmt.Println("")
		fmt.Println("count")
		fmt.Println("  Print number of unread inbox messages for the current node.")
		fmt.Println("  Node name is auto-detected from tmux pane title.")
		fmt.Println("  Flags:")
		fmt.Println("    --context-id <id>    Context ID (required)")
		fmt.Println("    --config <path>      Config file path (optional)")
		fmt.Println("")
		fmt.Println("read")
		fmt.Println("  List inbox message file paths for the current node.")
		fmt.Println("  Node name is auto-detected from tmux pane title.")
		fmt.Println("  Flags:")
		fmt.Println("    --context-id <id>    Context ID (required)")
		fmt.Println("    --config <path>      Config file path (optional)")
		fmt.Println("")
		fmt.Println("archive <filename> [filename...]")
		fmt.Println("  Move inbox message files to read/ to mark them as read.")
		fmt.Println("  Accepts plain filenames (located by glob) or full paths (backward compat).")
		fmt.Println("  Typical workflow:")
		fmt.Println("    1. tmux-a2a-postman read          # list inbox file paths")
		fmt.Println("    2. cat /path/to/msg.md            # read the message")
		fmt.Println("    3. tmux-a2a-postman archive msg.md  # mark as read (filename only)")
		fmt.Println("")
		fmt.Println("help [topic]")
		fmt.Println("  Show help overview or detailed topic page.")
		fmt.Println("  Topics: messaging, directories, config, commands")
	default:
		fmt.Fprintf(os.Stderr, "unknown help topic: %q\n", topic)
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Available topics:")
		for _, t := range topics {
			fmt.Fprintf(os.Stderr, "  %s\n", t)
		}
		os.Exit(1)
	}
}
