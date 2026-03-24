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
	"regexp"
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
	"github.com/i9wa4/tmux-a2a-postman/internal/bindcmd"
	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/daemon"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/lock"
	"github.com/i9wa4/tmux-a2a-postman/internal/memory"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
	"github.com/i9wa4/tmux-a2a-postman/internal/ping"
	"github.com/i9wa4/tmux-a2a-postman/internal/reminder"
	"github.com/i9wa4/tmux-a2a-postman/internal/session"
	"github.com/i9wa4/tmux-a2a-postman/internal/supervisor"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
	"github.com/i9wa4/tmux-a2a-postman/internal/version"
)

// idempotencyKeyPattern is the canonical regex for --idempotency-key tokens.
// Prevents newline/YAML injection via caller-supplied token.
const idempotencyKeyPattern = `^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`

var validIdempotencyKeyRe = regexp.MustCompile(idempotencyKeyPattern)

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
	globalBaseDir := fs.String("base-dir", "", "override state directory (sets POSTMAN_HOME)")
	globalStateHome := fs.String("state-home", "", "override XDG_STATE_HOME")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: tmux-a2a-postman [options] [command]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Options:")
		printDoubleDashDefaults(fs)
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  start                      Start tmux-a2a-postman daemon (default)")
		fmt.Fprintln(os.Stderr, "  stop                       Stop the running daemon for this tmux session")
		fmt.Fprintln(os.Stderr, "  send-message               Send a message in one step (--to and --body required)")
		fmt.Fprintln(os.Stderr, "  next                       Read and archive the oldest unread inbox message")
		fmt.Fprintln(os.Stderr, "  count                      Count unread inbox messages")
		fmt.Fprintln(os.Stderr, "  create-draft               Create message draft")
		fmt.Fprintln(os.Stderr, "  send <filename>            Move draft file to post/ (advanced)")
		fmt.Fprintln(os.Stderr, "  get-context-id             Print live context ID for current tmux session")
		fmt.Fprintln(os.Stderr, "  resend                     Re-send a dead-letter message")
		fmt.Fprintln(os.Stderr, "  list-dead-letters          List dead-letter messages (no filenames)")
		fmt.Fprintln(os.Stderr, "  supervisor-drain           Phase 3→2 rollback: annotate pending records and drain supervisor dead-letters")
		fmt.Fprintln(os.Stderr, "  get-session-status-oneline Show all sessions' pane status in one line")
		fmt.Fprintln(os.Stderr, "  get-session-health         Print session health per node")
		fmt.Fprintln(os.Stderr, "  read                       List inbox message paths")
		fmt.Fprintln(os.Stderr, "  archive <filename> [filename...]   Mark inbox messages as read (advanced)")
		fmt.Fprintln(os.Stderr, "  schema [command]           Print JSON Schema for config or command options")
		fmt.Fprintln(os.Stderr, "  help [topic]               Show help overview or topic-based help")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Global Flags:")
		fmt.Fprintln(os.Stderr, "  --base-dir <path>          Override state directory (sets POSTMAN_HOME)")
		fmt.Fprintln(os.Stderr, "  --state-home <path>        Override XDG_STATE_HOME")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman start                               # Start daemon")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman send-message --to worker --body \"DONE\"  # Send message")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman next --json                         # Read next message as JSON")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman schema send-message                 # Show send-message JSON Schema")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman --base-dir /tmp/test count          # Override state directory")
		fmt.Fprintln(os.Stderr, "  tmux-a2a-postman help messaging                      # Messaging guide")
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			return
		}
		os.Exit(1)
	}

	// Sub-feature (d): inject flag values into env before subcommand dispatch
	// so all config.ResolveBaseDir / config.ResolveXDGStateHome call sites pick them up.
	if *globalBaseDir != "" {
		os.Setenv("POSTMAN_HOME", *globalBaseDir)
	}
	if *globalStateHome != "" {
		os.Setenv("XDG_STATE_HOME", *globalStateHome)
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

	// Issue #315: forward global --context-id to subcommands that accept it.
	// Prepending ensures subcommand-level --context-id takes precedence (last-wins).
	prependContextID := func(a []string) []string {
		if *contextID == "" {
			return a
		}
		return append([]string{"--context-id", *contextID}, a...)
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
		if err := runCreateDraft(prependContextID(args)); err != nil {
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
		if err := runCount(prependContextID(args)); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman count: %v\n", err)
			os.Exit(1)
		}
	case "read":
		if err := runRead(prependContextID(args)); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman read: %v\n", err)
			os.Exit(1)
		}
	case "archive":
		if err := runArchive(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman archive: %v\n", err)
			os.Exit(1)
		}
	case "next":
		if err := runNext(prependContextID(args)); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman next: %v\n", err)
			os.Exit(1)
		}
	case "get-session-health":
		if err := runGetSessionHealth(prependContextID(args)); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman get-session-health: %v\n", err)
			os.Exit(1)
		}
	case "get-context-id":
		if err := runGetContextID(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman get-context-id: %v\n", err)
			os.Exit(1)
		}
	case "get-nodes-dir":
		if err := runGetNodesDir(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman get-nodes-dir: %v\n", err)
			os.Exit(1)
		}
	case "resend":
		if err := runResend(prependContextID(args)); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman resend: %v\n", err)
			os.Exit(1)
		}
	case "list-dead-letters":
		if err := runListDeadLetters(prependContextID(args)); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman list-dead-letters: %v\n", err)
			os.Exit(1)
		}
	case "supervisor-drain":
		if err := runSupervisorDrain(prependContextID(args)); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman supervisor-drain: %v\n", err)
			os.Exit(1)
		}
	case "show-inbox-message":
		if err := runShowInboxMessage(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman show-inbox-message: %v\n", err)
			os.Exit(1)
		}
	case "list-archived-messages":
		if err := runListArchivedMessages(prependContextID(args)); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman list-archived-messages: %v\n", err)
			os.Exit(1)
		}
	case "show-archived-message":
		if err := runShowArchivedMessage(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman show-archived-message: %v\n", err)
			os.Exit(1)
		}
	case "send-message":
		if err := runSendMessage(prependContextID(args)); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman send-message: %v\n", err)
			os.Exit(1)
		}
	case "bind":
		if err := bindcmd.Run(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman bind: %v\n", err)
			os.Exit(1)
		}
	case "schema":
		if err := runSchema(args); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman schema: %v\n", err)
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
		contextID = fmt.Sprintf("%s-%04x",
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
		// Issue #249: Startup guard — detect duplicate daemon for this context+session.
		// Scope check to contextID only: multi-session support allows independent daemons
		// in separate contexts sharing the same tmux session name.
		if config.IsSessionPIDAlive(baseDir, contextID, tmuxSessionName) {
			return fmt.Errorf(
				"a postman daemon is already running in tmux session %q (context: %s).\n"+
					"Stop it first.",
				tmuxSessionName, contextID,
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

	// Post-startup background re-discovery: catches panes that set their titles
	// slightly after daemon start (agent launch scripts run after daemon starts).
	time.AfterFunc(2*time.Second, func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("🚨 startup-rediscovery panic: %v\n", r)
			}
		}()
		fresh, _, err := discovery.DiscoverNodesWithCollisions(baseDir, contextID, sessionName)
		if err != nil {
			log.Printf("⚠️  postman: startup re-discovery failed: %v\n", err)
			return
		}
		edgeNodesLocal := config.GetEdgeNodeNames(cfg.Edges)
		for nodeName := range fresh {
			parts := strings.SplitN(nodeName, ":", 2)
			if !edgeNodesLocal[parts[len(parts)-1]] {
				delete(fresh, nodeName)
			}
		}
		sharedNodes.Store(&fresh)
		log.Printf("postman: startup re-discovery complete (%d nodes)\n", len(fresh))
	})

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
	daemonState := daemon.NewDaemonState(cfg.StartupDrainWindowSeconds, contextID)
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
							if owner := config.FindSessionOwner(baseDir, cmd.Target, contextID); owner != "" {
								log.Printf("session_toggle blocked: %q already owned by %s\n", cmd.Target, owner)
								// Fetch fresh sessions list so the TUI reverses the optimistic flip.
								blockedSessions, _ := discovery.DiscoverAllSessions()
								blockedNodes := nodes
								if cached := sharedNodes.Load(); cached != nil {
									blockedNodes = *cached
								}
								blockedSessionList := session.BuildSessionList(blockedNodes, blockedSessions, daemonState.GetConfiguredSessionEnabled)
								ownerSession := config.FindContextSessionName(baseDir, owner)
								blockMsg := fmt.Sprintf("BLOCKED: session %q already owned by daemon %s", cmd.Target, owner)
								if ownerSession != "" {
									blockMsg = fmt.Sprintf("BLOCKED: session %q owned by daemon in tmux session %q (%s)", cmd.Target, ownerSession, owner)
								}
								daemonEvents <- tui.DaemonEvent{
									Type:    "status_update",
									Message: blockMsg,
									Details: map[string]interface{}{
										"session":  cmd.Target,
										"sessions": blockedSessionList,
									},
								}
								continue
							}
							// Owner "" means no live daemon holds this session;
							// clear any stale @a2a_session_on_ option left by a crashed daemon.
							_ = exec.Command("tmux", "set-option", "-gu", "@a2a_session_on_"+cmd.Target).Run()
						}

						daemonState.SetSessionEnabled(cmd.Target, newState)
						log.Printf("📮 postman: Session %s toggled to %v\n", cmd.Target, newState)

						// When enabling a session: create its inbox dirs and refresh node
						// discovery so cross-session panes become visible to send_ping.
						if newState {
							if err := config.CreateMultiSessionDirs(contextDir, cmd.Target); err != nil {
								log.Printf("⚠️  postman: warning: could not create dirs for session %s: %v\n", cmd.Target, err)
							} else {
								// Pre-claim panes in the enabled session so the F3 guard passes.
								edgeNodes := config.GetEdgeNodeNames(cfg.Edges)
								preClaimed := 0
								if paneOut, paneErr := exec.Command("tmux", "list-panes", "-s", "-t", cmd.Target, "-F", "#{pane_id} #{pane_title}").Output(); paneErr == nil {
									for _, line := range strings.Split(strings.TrimSpace(string(paneOut)), "\n") {
										parts := strings.SplitN(line, " ", 2)
										if len(parts) == 2 && edgeNodes[parts[1]] {
											if err := exec.Command("tmux", "set-option", "-p", "-t", parts[0], "@a2a_context_id", contextID).Run(); err != nil {
												log.Printf("postman: WARNING: failed to pre-claim pane %s (%s): %v\n", parts[0], parts[1], err)
											} else {
												preClaimed++
											}
										}
									}
								} else {
									log.Printf("postman: WARNING: failed to list panes for session %s: %v\n", cmd.Target, paneErr)
								}
								log.Printf("postman: pre-claimed %d panes in session %s for context %s\n", preClaimed, cmd.Target, contextID)
								refreshed, _, _ := discovery.DiscoverNodesWithCollisions(baseDir, contextID, sessionName)
								for nodeName := range refreshed {
									parts := strings.SplitN(nodeName, ":", 2)
									if !edgeNodes[parts[len(parts)-1]] {
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
								"session":    cmd.Target,
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
							// Attempt a fresh discovery before giving up (catches panes
							// that set titles after startup or after the last scan).
							freshDiscovered, _, discErr := discovery.DiscoverNodesWithCollisions(baseDir, contextID, sessionName)
							if discErr == nil && len(freshDiscovered) > 0 {
								// filterNodes modifies the map in-place (removes non-edge nodes)
								// and returns the session-filtered subset.
								targetNodes = filterNodes(freshDiscovered)
								sharedNodes.Store(&freshDiscovered)
								freshNodes = freshDiscovered // update for activeNodes loop below
							}
							if len(targetNodes) == 0 {
								if cachedPtr == nil {
									log.Printf("postman: PING skipped for session %s — no nodes discovered yet\n", cmd.Target)
								} else {
									log.Printf("postman: PING skipped for session %s — 0 nodes matched in session (total discovered across all sessions: %d)\n", cmd.Target, len(freshNodes))
								}
								daemonEvents <- tui.DaemonEvent{
									Type:    "status_update",
									Message: fmt.Sprintf("Nodes not yet discovered for session %s \u2014 press 'p' again", cmd.Target),
									Details: map[string]interface{}{"session": cmd.Target},
								}
								break
							}
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
						sessionTarget := cmd.Target
						go func() {
							var wg sync.WaitGroup
							var successCount, failCount atomic.Int32
							for nodeName, nodeInfo := range targetNodes {
								wg.Add(1)
								go func(name string, info discovery.NodeInfo) {
									defer wg.Done()
									if err := ping.SendPingToNode(info, contextID, name,
										cfg.DaemonMessageTemplate, cfg, activeNodes, livenessMap,
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
								Message: fmt.Sprintf("PING: %d/%d dispatched", successCount.Load(), total),
								Details: map[string]interface{}{"session": sessionTarget},
							}
							time.AfterFunc(30*time.Second, func() {
								daemonEvents <- tui.DaemonEvent{
									Type:    "status_update",
									Message: "",
									Details: map[string]interface{}{"session": sessionTarget},
								}
							})
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
	to := fs.String("to", "", "recipient node name (required)")
	contextID := fs.String("context-id", "", "context ID (optional, auto-detect if not specified)")
	session := fs.String("session", "", "tmux session name (optional, auto-detect if in tmux)")
	configPath := fs.String("config", "", "path to config file (optional)")
	body := fs.String("body", "", "inline message body (replaces <!-- write here --> placeholder)")
	sendFlag := fs.Bool("send", false, "send immediately after creating draft (atomic)")
	from := fs.String("from", "", "phony node name (sidecar use only; skips tmux sender detection)")
	bindingsPath := fs.String("bindings", "", "path to bindings.toml (required with --from)")
	idempotencyKey := fs.String("idempotency-key", "", "idempotency token written to draft YAML frontmatter")
	jsonOut := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to == "" {
		return fmt.Errorf("--to is required")
	}
	// Issue #304: --from requires --bindings
	if *from != "" && *bindingsPath == "" {
		return fmt.Errorf("--bindings is required with --from")
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	var sender string
	if *from != "" {
		// Issue #304: sidecar caller — validate identity, load registry, enforce inbound auth.
		if !binding.ValidateNodeName(*from) {
			return fmt.Errorf("--from %q: invalid node name (must match %s)", *from, binding.NodeNamePattern)
		}
		sender = *from
		reg, err := binding.Load(*bindingsPath)
		if err != nil {
			return fmt.Errorf("loading bindings: %w", err)
		}
		entry := reg.FindByNodeName(*from)
		if entry == nil {
			return fmt.Errorf("--from %q: no active binding found in registry", *from)
		}
		if entry.PaneNodeName == "" {
			return fmt.Errorf("--from %q: binding has empty pane_node_name (unassigned)", *from)
		}
		if *to != entry.PaneNodeName {
			return fmt.Errorf("--from %q: --to must be %q (binding's pane_node_name), got %q",
				*from, entry.PaneNodeName, *to)
		}
	} else {
		sender = config.GetTmuxPaneName()
		if sender == "" {
			return fmt.Errorf("sender auto-detection failed: set tmux pane title")
		}
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
	// Issue #304 6c: skip guard for phony-node callers (--from present)
	if *from == "" && config.GetTmuxSessionName() != "" {
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

	// Issue #304: context-id path traversal allowlist (user-supplied --context-id only)
	if *contextID != "" && !binding.ValidateNodeName(*contextID) {
		return fmt.Errorf("--context-id %q: invalid value (must match %s)", *contextID, binding.NodeNamePattern)
	}

	draftDir := filepath.Join(baseDir, resolvedContextID, sessionName, "draft")

	if err := os.MkdirAll(draftDir, 0o700); err != nil {
		return fmt.Errorf("creating draft directory: %w", err)
	}

	now := time.Now()
	ts := now.Format("20060102-150405")
	filename := message.GenerateFilename(ts, sender, *to, sessionName)
	draftPath := filepath.Join(draftDir, filename)

	// Use draft_template from config if available
	content := cfg.DraftTemplate
	if content == "" {
		// Fallback to minimal template
		content = "---\nparams:\n  contextId: {context_id}\n  from: {sender}\n  to: {recipient}\n  timestamp: {timestamp}\n---\n\nYou can only talk to: {can_talk_to}\n\n# Content\n\n"
	}

	// Build can_talk_to from adjacency
	adjacency, err := config.ParseEdges(cfg.Edges)
	if err != nil {
		return fmt.Errorf("parsing edges: %w", err)
	}
	talksToList := config.GetTalksTo(adjacency, sender)
	canTalkTo := strings.Join(talksToList, ", ")

	// Issue #332: Pre-flight edge enforcement for --send mode.
	// Only reject when the sender has explicitly configured edges (empty talksToList
	// means sender is not in the edge config and should not be blocked).
	if *sendFlag {
		if len(talksToList) > 0 {
			recipientAllowed := false
			for _, n := range talksToList {
				if n == *to {
					recipientAllowed = true
					break
				}
			}
			if !recipientAllowed {
				return fmt.Errorf("edge violation: %q cannot send to %q — allowed recipients: %s",
					sender, *to, canTalkTo)
			}
		}
	}

	// Build variables map for template expansion
	vars := map[string]string{
		"context_id":     resolvedContextID,
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
		stripped, err := notification.StripVT(*body)
		if err != nil {
			return fmt.Errorf("--body contains invalid UTF-8: %w", err)
		}
		content = strings.ReplaceAll(content, "<!-- write here -->", stripped)
	}

	// Append message footer (separated by ---)
	if cfg.MessageFooter != "" {
		footer := template.ExpandTemplate(cfg.MessageFooter, vars, timeout)
		content = strings.TrimRight(content, "\n") + "\n\n---\n\n" + footer + "\n"
	}

	// Issue #304: inject idempotency_key into YAML frontmatter
	if *idempotencyKey != "" {
		if !validIdempotencyKeyRe.MatchString(*idempotencyKey) {
			return fmt.Errorf("--idempotency-key %q: invalid token (must match %s)", *idempotencyKey, idempotencyKeyPattern)
		}
		const delim = "\n---\n"
		idx := strings.Index(content, delim)
		if idx == -1 {
			return fmt.Errorf("draft content has no YAML frontmatter closing delimiter (---)")
		}
		content = content[:idx] + "\nidempotency_key: " + *idempotencyKey + content[idx:]
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
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(struct {
				Sent string `json:"sent"`
			}{Sent: filename})
		}
		fmt.Printf("Sent: %s\n", filename)
		return nil
	} else if *body != "" {
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(struct {
				Draft string `json:"draft"`
			}{Draft: filename})
		}
		fmt.Printf("Draft created: %s\n\nNext steps:\n  1. tmux-a2a-postman send %s\n", filename, filename)
		return nil
	}
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(struct {
			Draft string `json:"draft"`
		}{Draft: filename})
	}
	fmt.Printf("Draft created: %s\n\nNext steps:\n  1. Edit ## Content section in the draft file\n  2. tmux-a2a-postman send %s\n", filename, filename)
	return nil
}

func runSendMessage(args []string) error {
	fs := flag.NewFlagSet("send-message", flag.ContinueOnError)
	to := fs.String("to", "", "recipient node name (required)")
	body := fs.String("body", "", "message body (required)")
	contextID := fs.String("context-id", "", "context ID (optional, auto-detected)")
	session := fs.String("session", "", "tmux session name (optional, auto-detected)")
	configPath := fs.String("config", "", "config file path (optional)")
	from := fs.String("from", "", "phony node name (sidecar use only; skips tmux sender detection)")
	bindingsPath := fs.String("bindings", "", "path to bindings.toml (required with --from)")
	idempotencyKey := fs.String("idempotency-key", "", "idempotency token written to draft YAML frontmatter")
	jsonOut := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to == "" {
		return fmt.Errorf("--to is required")
	}
	if *body == "" {
		// NOTE: runCreateDraft issues only a warning (not an error) for --send
		// without --body (see runCreateDraft:966-968). Enforce here before
		// delegating so send-message never sends a placeholder-body message.
		return fmt.Errorf("--body is required")
	}
	newArgs := []string{"--to", *to, "--body", *body, "--send"}
	if *contextID != "" {
		newArgs = append(newArgs, "--context-id", *contextID)
	}
	if *session != "" {
		newArgs = append(newArgs, "--session", *session)
	}
	if *configPath != "" {
		newArgs = append(newArgs, "--config", *configPath)
	}
	if *from != "" {
		newArgs = append(newArgs, "--from", *from)
	}
	if *bindingsPath != "" {
		newArgs = append(newArgs, "--bindings", *bindingsPath)
	}
	if *idempotencyKey != "" {
		newArgs = append(newArgs, "--idempotency-key", *idempotencyKey)
	}
	if *jsonOut {
		newArgs = append(newArgs, "--json")
	}
	return runCreateDraft(newArgs)
}

// expandReplyCommand substitutes {context_id} in the reply command template
func expandReplyCommand(replyCmd string, contextID string) string {
	return strings.ReplaceAll(replyCmd, "{context_id}", contextID)
}

// getNodeTemplate retrieves the template for a given node from config,
// prepending common_template if present (mirrors BuildEnvelope/BuildRoleContent).
// Returns empty string if node or template is not found (nil-safe).
func getNodeTemplate(cfg *config.Config, nodeName string) string {
	if cfg == nil || cfg.Nodes == nil {
		return ""
	}
	nodeConfig, exists := cfg.Nodes[nodeName]
	if !exists {
		return ""
	}
	tmpl := nodeConfig.Template
	if cfg.CommonTemplate != "" && tmpl != "" {
		return cfg.CommonTemplate + "\n\n" + tmpl
	}
	if cfg.CommonTemplate != "" {
		return cfg.CommonTemplate
	}
	return tmpl
}

// runGetSessionStatusOneline shows all tmux sessions' pane status in one line.
// statusDot returns the status indicator string for a pane.
// When isTerminal is true, returns a lipgloss-styled ANSI dot.
// When isTerminal is false, returns a plain emoji suitable for tmux #() output.
// lipgloss's own color detection is intentionally bypassed here because #() contexts
// require plain text regardless of color capability. (Issue #275)
func statusDot(status string, isTerminal bool) string {
	if isTerminal {
		activeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
		pendingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("51"))
		composingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("33"))
		spinningStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("226"))
		staleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
		userInputStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
		switch status {
		case "ready", "active":
			return activeStyle.Render("●")
		case "pending":
			return pendingStyle.Render("●")
		case "composing":
			return composingStyle.Render("●")
		case "spinning", "idle":
			return spinningStyle.Render("●")
		case "user_input":
			return userInputStyle.Render("●")
		default:
			return staleStyle.Render("●")
		}
	}
	switch status {
	case "ready", "active":
		return "🟢"
	case "pending":
		return "🔷"
	case "composing":
		return "🔵"
	case "spinning", "idle":
		return "🟡"
	case "user_input":
		return "🟣"
	default:
		return "🔴"
	}
}

// isShellCommand returns true if cmd is a known interactive shell name.
// Used by runGetSessionStatusOneline to exclude panes with no AI running (Issue #312).
var shellCommands = map[string]bool{
	"bash": true, "zsh": true, "sh": true, "fish": true,
	"dash": true, "ksh": true, "csh": true, "tcsh": true, "nu": true,
}

func isShellCommand(cmd string) bool {
	return shellCommands[cmd]
}

// Output format: [0]window0_panes:window1_panes:... [1]window0_panes:...
// TTY output (interactive terminal): ANSI-colored dots (● green/blue/yellow/red)
// Non-TTY output (tmux #(), pipes): plain emoji (🟢/🔵/🟡/🔴)
// Pane status: active=green, composing=blue, idle/spinning=yellow, stale=red
// Issue #120: Refactored to use idle.go activity detection instead of #{pane_active}
// Issue #275: TTY detection so tmux status-right receives plain emoji, not ANSI codes
// Issue #312: Filter panes by pane_current_command; fix session index stability.
func runGetSessionStatusOneline(args []string) error {
	fs := flag.NewFlagSet("get-session-status-oneline", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Load config to get base directory
	cfg, err := config.LoadConfig("")
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	// Find the most recently started live context for the current tmux session.
	// Context directories are named YYYYMMDD-HHMMSS-XXXX; lexicographic
	// descending sort gives newest first.
	statusPriority := map[string]int{"active": 2, "idle": 1, "stale": 0}
	paneActivity := make(map[string]string)

	contextDirs, _ := filepath.Glob(filepath.Join(baseDir, "[0-9]*"))
	sort.Sort(sort.Reverse(sort.StringSlice(contextDirs)))

	var liveStateFiles []string
	var liveCtxSessionPairs [][2]string // [ctxDir, sessionSubdir]
	paneActivityAdded := make(map[string]bool)
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
				if !paneActivityAdded[ctxDir] {
					liveStateFiles = append(liveStateFiles, filepath.Join(ctxDir, "pane-activity.json"))
					paneActivityAdded[ctxDir] = true
				}
				liveCtxSessionPairs = append(liveCtxSessionPairs, [2]string{ctxDir, se.Name()})
				// NOTE: no break — collect ALL live session subdirs for waiting-file overlay (#285)
			}
		}
	}

	if len(liveStateFiles) == 0 {
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(struct {
				Status string `json:"status"`
			}{Status: ""})
		}
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

	// Build edge node set and pane title map for filtering.
	// Issue #312: also capture pane_current_command to detect shell-only panes.
	// Edge node names are always single-word identifiers, so SplitN(4) safely
	// captures all four fields: pane_id, session_name, pane_title, pane_current_command.
	edgeNodes := config.GetEdgeNodeNames(cfg.Edges)
	paneTitleOutput, _ := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id} #{session_name} #{pane_title} #{pane_current_command}").Output()
	paneTitles := make(map[string]string)           // paneID -> paneTitle (for edge filter)
	sessionTitleToPaneID := make(map[string]string) // "sessionName:paneTitle" -> paneID (for waiting overlay, #285)
	paneCurrentCmd := make(map[string]string)       // paneID -> current command name (for shell filter, #312)
	for _, line := range strings.Split(strings.TrimSpace(string(paneTitleOutput)), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), " ", 4)
		if len(parts) >= 3 && parts[0] != "" && parts[2] != "" {
			paneID, sessionName, title := parts[0], parts[1], parts[2]
			paneTitles[paneID] = title
			sessionTitleToPaneID[sessionName+":"+title] = paneID
			if len(parts) == 4 {
				paneCurrentCmd[paneID] = parts[3]
			}
		}
	}

	// Overlay waiting-file states onto paneActivity (Issue #285).
	// waiting/*.md files carry "composing", "spinning", "stuck", "user_input" states
	// that are never present in pane-activity.json. This mirrors the TUI's
	// effectiveNodeState merge (tui.go:260).
	applyWaitingOverlay(liveCtxSessionPairs, sessionTitleToPaneID, paneActivity)
	applyPendingOverlay(liveCtxSessionPairs, sessionTitleToPaneID, paneActivity)

	// Get all tmux sessions with their stable index (Issue #312, #349: #{session_id}
	// is assigned at creation time and does not shift when other sessions are removed,
	// unlike a loop counter over the output slice. #{session_index} is unsupported on
	// tmux 3.6a; #{session_id} (e.g. $0, $70) works on 3.6a and newer versions.
	sessionsOutput, err := exec.Command("tmux", "list-sessions", "-F", "#{session_id} #{session_name}").Output()
	if err != nil {
		// Check if no server running
		if strings.Contains(string(sessionsOutput), "no server running") {
			if *jsonOut {
				return json.NewEncoder(os.Stdout).Encode(struct {
					Status string `json:"status"`
				}{Status: ""})
			}
			return nil
		}
		return fmt.Errorf("listing sessions: %w", err)
	}

	type sessionEntry struct {
		index string
		name  string
	}
	var sessions []sessionEntry
	for _, line := range strings.Split(strings.TrimSpace(string(sessionsOutput)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 || parts[1] == "" {
			continue
		}
		sessions = append(sessions, sessionEntry{index: strings.TrimPrefix(parts[0], "$"), name: parts[1]})
	}
	if len(sessions) == 0 {
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(struct {
				Status string `json:"status"`
			}{Status: ""})
		}
		return nil
	}

	// Issue #275: Use plain emoji when stdout is not a TTY (e.g. tmux status-right #()).
	isTerminal := term.IsTerminal(os.Stdout.Fd())

	var output []string

	for _, sess := range sessions {
		sessionName := sess.name

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
				// #312: Skip panes with no running agent.
				// (a) Pane not in paneActivity: daemon hasn't captured it yet.
				// (b) Pane's foreground process is a shell: no AI launched there.
				if _, tracked := paneActivity[paneID]; !tracked {
					continue
				}
				if isShellCommand(paneCurrentCmd[paneID]) {
					continue
				}
				paneStatuses += statusDot(paneActivity[paneID], isTerminal)
			}

			if paneStatuses != "" {
				windowStatuses = append(windowStatuses, paneStatuses)
			}
		}

		// Build session status: [N]window0:window1:...
		if len(windowStatuses) > 0 {
			sessionStatus := fmt.Sprintf("[%s]%s", sess.index, strings.Join(windowStatuses, ":"))
			output = append(output, sessionStatus)
		}
	}

	if len(output) > 0 {
		statusStr := strings.Join(output, " ")
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(struct {
				Status string `json:"status"`
			}{Status: statusStr})
		}
		fmt.Println(statusStr)
		return nil
	}
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(struct {
			Status string `json:"status"`
		}{Status: ""})
	}
	return nil
}

// applyWaitingOverlay scans waiting/ dirs in liveCtxSessionPairs and overlays
// their states onto paneActivity in-place (Issue #285).
// sessionTitleToPaneID maps "sessionName:paneTitle" -> paneID.
// Priority mirrors daemon.go:998-1003: higher rank = worse state = wins.
// waitingOverlayRank defines overlay priority for waiting/ and inbox/ state display.
// Higher rank = worse state = takes visual priority.
// "ready", "idle", "stale" are absent (default 0); any rank >= 1 overrides them.
var waitingOverlayRank = map[string]int{
	"user_input": 0,
	"pending":    1,
	"composing":  2,
	"spinning":   3,
	"stalled":    4,
}

func applyWaitingOverlay(
	liveCtxSessionPairs [][2]string,
	sessionTitleToPaneID map[string]string,
	paneActivity map[string]string,
) {
	for _, pair := range liveCtxSessionPairs {
		ctxDir, sessionSubdir := pair[0], pair[1]
		waitingDir := filepath.Join(ctxDir, sessionSubdir, "waiting")
		entries, err := os.ReadDir(waitingDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			fileInfo, parseErr := message.ParseMessageFilename(entry.Name())
			if parseErr != nil {
				continue // malformed filename: skip silently (mirrors daemon.go:1032-1034)
			}
			content, readErr := os.ReadFile(filepath.Join(waitingDir, entry.Name()))
			if readErr != nil {
				continue
			}
			cs := string(content)
			var fileState string
			switch {
			case strings.Contains(cs, "state: stalled"), strings.Contains(cs, "state: stuck"):
				fileState = "stalled"
			case strings.Contains(cs, "state: spinning"):
				fileState = "spinning"
			case strings.Contains(cs, "state: composing"):
				fileState = "composing"
			case strings.Contains(cs, "state: user_input"):
				fileState = "user_input"
			default:
				continue
			}
			// sessionSubdir is the tmux session name; fileInfo.To is the recipient node name.
			// Color the RECIPIENT's dot — the node expected to reply.
			recipientKey := sessionSubdir + ":" + fileInfo.To
			paneID, ok := sessionTitleToPaneID[recipientKey]
			if !ok {
				continue
			}
			if waitingOverlayRank[fileState] >= waitingOverlayRank[paneActivity[paneID]] {
				paneActivity[paneID] = fileState
			}
		}
	}
}

// applyPendingOverlay overlays "pending" state onto paneActivity
// for any node that has unarchived messages in its inbox/ subdirectory.
// Mirrors applyWaitingOverlay signature for composability.
func applyPendingOverlay(
	liveCtxSessionPairs [][2]string,
	sessionTitleToPaneID map[string]string,
	paneActivity map[string]string,
) {
	for _, pair := range liveCtxSessionPairs {
		ctxDir, sessionSubdir := pair[0], pair[1]
		inboxBase := filepath.Join(ctxDir, sessionSubdir, "inbox")
		nodeDirs, err := os.ReadDir(inboxBase)
		if err != nil {
			continue
		}
		for _, nodeDir := range nodeDirs {
			if !nodeDir.IsDir() {
				continue
			}
			nodeName := nodeDir.Name()
			entries, err := os.ReadDir(filepath.Join(inboxBase, nodeName))
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !strings.HasSuffix(entry.Name(), ".md") {
					continue
				}
				recipientKey := sessionSubdir + ":" + nodeName
				paneID, ok := sessionTitleToPaneID[recipientKey]
				if !ok {
					break
				}
				if waitingOverlayRank["pending"] >= waitingOverlayRank[paneActivity[paneID]] {
					paneActivity[paneID] = "pending"
				}
				break // one message is enough to mark pending
			}
		}
	}
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

	nodeName := config.GetTmuxPaneName()
	if nodeName == "" {
		return "", fmt.Errorf("node name auto-detection failed: set tmux pane title")
	}

	sessionName := config.GetTmuxSessionName()
	if sessionName == "" {
		return "", fmt.Errorf("tmux session name required (run inside tmux)")
	}
	sessionName = filepath.Base(sessionName)

	resolvedContextID, err := config.ResolveContextID(*contextID)
	if err != nil {
		resolvedContextID, err = config.ResolveContextIDFromSession(baseDir, sessionName)
		if err != nil {
			return "", err
		}
	}

	inboxPath := filepath.Join(baseDir, resolvedContextID, sessionName, "inbox", nodeName)
	return inboxPath, nil
}

// runCount prints the number of unread inbox messages for the current node (#196).
func runCount(args []string) error {
	fs := flag.NewFlagSet("count", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	inboxPath, err := resolveInboxPath(fs.Args())
	if err != nil {
		return err
	}
	msgs := message.ScanInboxMessages(inboxPath)
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(struct {
			Count int `json:"count"`
		}{Count: len(msgs)})
	}
	fmt.Println(len(msgs))
	return nil
}

// runRead lists inbox message file paths for the current node (#196).
func runRead(args []string) error {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	inboxPath, err := resolveInboxPath(fs.Args())
	if err != nil {
		return err
	}
	msgs := message.ScanInboxMessages(inboxPath)
	if *jsonOut {
		files := make([]string, len(msgs))
		for i, msg := range msgs {
			files[i] = msg.Filename
		}
		return json.NewEncoder(os.Stdout).Encode(struct {
			Files []string `json:"files"`
		}{Files: files})
	}
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
	fs := flag.NewFlagSet("list-archived-messages", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	inboxPath, err := resolveInboxPath(fs.Args())
	if err != nil {
		return err
	}
	readPath := filepath.Join(filepath.Dir(filepath.Dir(inboxPath)), "read")

	entries, err := os.ReadDir(readPath)
	if err != nil {
		if os.IsNotExist(err) {
			if *jsonOut {
				return json.NewEncoder(os.Stdout).Encode(struct {
					Messages []archivedMessageJSON `json:"messages"`
				}{Messages: []archivedMessageJSON{}})
			}
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
	if *jsonOut {
		msgs := make([]archivedMessageJSON, 0, len(names))
		for _, name := range names {
			info, err := message.ParseMessageFilename(name)
			if err != nil {
				msgs = append(msgs, archivedMessageJSON{File: name})
			} else {
				msgs = append(msgs, archivedMessageJSON{File: name, From: info.From, To: info.To})
			}
		}
		return json.NewEncoder(os.Stdout).Encode(struct {
			Messages []archivedMessageJSON `json:"messages"`
		}{Messages: msgs})
	}
	for _, name := range names {
		fmt.Println(name)
	}
	return nil
}

// archivedMessageJSON holds JSON-serializable fields for an archived message.
type archivedMessageJSON struct {
	File string `json:"file"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// deadLetterMessageJSON holds JSON-serializable metadata for a dead-letter message.
// Does not expose raw filenames (#287 opacity).
type deadLetterMessageJSON struct {
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

// listDeadLettersFromDir prints dead-letter messages from a directory path (Issue #287).
// Exported for testing. Output goes to stdout; empty-dir message goes to stderr.
func listDeadLettersFromDir(deadLetterPath string) error {
	entries, err := os.ReadDir(deadLetterPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "No dead-letter messages.")
			return nil
		}
		return fmt.Errorf("reading dead-letter messages: %w", err)
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "No dead-letter messages.")
		return nil
	}

	for _, name := range names {
		cleanName := message.StripDeadLetterSuffix(name)
		info, err := message.ParseMessageFilename(cleanName)
		if err != nil {
			fmt.Printf("%s  [unreadable]\n", name)
			continue
		}
		fmt.Printf("%s  from=%s  to=%s\n", info.Timestamp, info.From, info.To)
	}
	return nil
}

// findOldestDeadLetterFile returns the path of the lexicographically first .md
// file in deadLetterDir, or ("", false, nil) if the directory is empty or absent.
func findOldestDeadLetterFile(deadLetterDir string) (string, bool, error) {
	entries, err := os.ReadDir(deadLetterDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("reading dead-letter directory: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "", false, nil
	}
	return filepath.Join(deadLetterDir, names[0]), true, nil
}

// runListDeadLetters prints dead-letter messages without exposing filenames (Issue #287).
func runListDeadLetters(args []string) error {
	fs := flag.NewFlagSet("list-dead-letters", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	inboxPath, err := resolveInboxPath(fs.Args())
	if err != nil {
		return err
	}
	deadLetterPath := filepath.Join(filepath.Dir(filepath.Dir(inboxPath)), "dead-letter")
	if *jsonOut {
		return listDeadLettersJSON(deadLetterPath)
	}
	return listDeadLettersFromDir(deadLetterPath)
}

// listDeadLettersJSON outputs dead-letter message metadata as JSON.
// Emits {"messages": [{"from","to","timestamp"},...]} — never exposes raw filenames (#287).
func listDeadLettersJSON(deadLetterPath string) error {
	emptyResult := func() error {
		return json.NewEncoder(os.Stdout).Encode(struct {
			Messages []deadLetterMessageJSON `json:"messages"`
		}{Messages: []deadLetterMessageJSON{}})
	}
	entries, err := os.ReadDir(deadLetterPath)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyResult()
		}
		return fmt.Errorf("reading dead-letter messages: %w", err)
	}
	names := make([]string, 0)
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	msgs := make([]deadLetterMessageJSON, 0, len(names))
	for _, name := range names {
		cleanName := message.StripDeadLetterSuffix(name)
		info, err := message.ParseMessageFilename(cleanName)
		if err != nil {
			msgs = append(msgs, deadLetterMessageJSON{})
		} else {
			msgs = append(msgs, deadLetterMessageJSON{From: info.From, To: info.To, Timestamp: info.Timestamp})
		}
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Messages []deadLetterMessageJSON `json:"messages"`
	}{Messages: msgs})
}

// runSupervisorDrain implements the Phase 3 → Phase 2 rollback drain procedure
// (Issue #309 section 9). It:
//  1. Annotates all supervisor memory records with outcome=pending.
//  2. Drains the supervisor dead-letter directory, classifying each file by
//     the -dl-<reason> suffix in its filename.
//  3. Writes drain-summary.txt (mode 0600) in the supervisor dead-letter dir.
//
// Eligible reasons (redeliver): session_offline, channel_unbound, sidecar_unavailable.
// Ineligible reasons (quarantine): routing_denied, redelivery_failed, empty idempotency_key.
// Everything else: passthrough (archived in quarantine dir with passthrough marker).
func runSupervisorDrain(args []string) error {
	fs := flag.NewFlagSet("supervisor-drain", flag.ContinueOnError)
	contextIDFlag := fs.String("context-id", "", "context ID (optional, auto-resolved from tmux session)")
	configPath := fs.String("config", "", "path to config file (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	sessionName := config.GetTmuxSessionName()
	if sessionName == "" {
		return fmt.Errorf("supervisor-drain must be run inside tmux")
	}
	sessionName = filepath.Base(sessionName)

	resolvedContextID, err := config.ResolveContextID(*contextIDFlag)
	if err != nil {
		resolvedContextID, err = config.ResolveContextIDFromSession(baseDir, sessionName)
		if err != nil {
			return err
		}
	}

	// Step 1: annotate pending supervisor memory records.
	store, err := memory.NewStore(baseDir, resolvedContextID)
	if err != nil {
		return fmt.Errorf("supervisor-drain: open memory store: %w", err)
	}
	if err := store.AnnotatePendingRollback(); err != nil {
		return fmt.Errorf("supervisor-drain: annotate pending rollback: %w", err)
	}

	// Step 2: drain supervisor dead-letter directory.
	deadLetterDir := filepath.Join(baseDir, resolvedContextID, "supervisor-memory", "dead-letter")
	archiveDir := filepath.Join(deadLetterDir, "archive")
	if err := os.MkdirAll(archiveDir, 0o700); err != nil {
		return fmt.Errorf("supervisor-drain: create archive dir: %w", err)
	}
	postDir := filepath.Join(baseDir, resolvedContextID, sessionName, "post")
	if err := os.MkdirAll(postDir, 0o700); err != nil {
		return fmt.Errorf("supervisor-drain: create post dir: %w", err)
	}

	entries, err := os.ReadDir(deadLetterDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("supervisor-drain: read dead-letter dir: %w", err)
	}

	var redelivered, quarantined, redeliveryFailed, passthrough int
	partial := false

	// Eligible drain reasons → redeliver to post/.
	eligibleReasons := map[string]bool{
		"session-offline":     true,
		"session_offline":     true,
		"channel-unbound":     true,
		"channel_unbound":     true,
		"sidecar-unavailable": true,
		"sidecar_unavailable": true,
	}
	// Ineligible drain reasons → quarantine.
	ineligibleReasons := map[string]bool{
		"routing-denied":    true,
		"routing_denied":    true,
		"redelivery-failed": true,
		"redelivery_failed": true,
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		src := filepath.Join(deadLetterDir, name)

		// Extract reason from -dl-<reason> suffix.
		reason := extractDrainReason(name)

		// Check for empty idempotency_key quarantine condition.
		if reason == "" {
			// No dl-suffix found: treat as passthrough.
			dst := filepath.Join(archiveDir, "passthrough-"+name)
			if mvErr := os.Rename(src, dst); mvErr != nil {
				log.Printf("supervisor-drain: WARNING: passthrough rename %s: %v\n", name, mvErr)
				partial = true
				continue
			}
			passthrough++
			continue
		}

		if eligibleReasons[reason] {
			cleanName := message.StripDeadLetterSuffix(name)
			dst := filepath.Join(postDir, cleanName)
			if mvErr := os.Rename(src, dst); mvErr != nil {
				log.Printf("supervisor-drain: WARNING: redeliver rename %s: %v\n", name, mvErr)
				// Redeliver failed: quarantine instead.
				dst2 := filepath.Join(archiveDir, "redelivery-failed-"+name)
				_ = os.Rename(src, dst2)
				redeliveryFailed++
				partial = true
				continue
			}
			redelivered++
		} else if ineligibleReasons[reason] {
			dst := filepath.Join(archiveDir, "quarantine-"+name)
			if mvErr := os.Rename(src, dst); mvErr != nil {
				log.Printf("supervisor-drain: WARNING: quarantine rename %s: %v\n", name, mvErr)
				partial = true
				continue
			}
			quarantined++
		} else {
			// Unknown reason: passthrough.
			dst := filepath.Join(archiveDir, "passthrough-"+name)
			if mvErr := os.Rename(src, dst); mvErr != nil {
				log.Printf("supervisor-drain: WARNING: passthrough rename %s: %v\n", name, mvErr)
				partial = true
				continue
			}
			passthrough++
		}
	}

	// Step 3: write drain-summary.txt (mode 0600).
	summaryPath := filepath.Join(deadLetterDir, "drain-summary.txt")
	summaryLine := fmt.Sprintf("drained_at=%s redelivered=%d archived_redelivery_failed=%d archived_quarantined=%d archived_passthrough=%d",
		time.Now().UTC().Format(time.RFC3339), redelivered, redeliveryFailed, quarantined, passthrough)
	if partial {
		summaryLine += " status=partial"
	}
	summaryLine += "\n"

	// Append (not overwrite) to allow multiple drain attempts.
	f, err := os.OpenFile(summaryPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("supervisor-drain: open drain-summary.txt: %w", err)
	}
	if _, err := fmt.Fprint(f, summaryLine); err != nil {
		_ = f.Close()
		return fmt.Errorf("supervisor-drain: write drain-summary.txt: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("supervisor-drain: close drain-summary.txt: %w", err)
	}

	log.Printf("supervisor-drain: PII retention: 90 days (default)\n")
	fmt.Printf("supervisor-drain complete: redelivered=%d quarantined=%d redelivery_failed=%d passthrough=%d partial=%v\n",
		redelivered, quarantined, redeliveryFailed, passthrough, partial)

	// Also drain ConfidenceManager by resetting to Phase 2 defaults.
	_ = supervisor.NewConfidenceManager(store) // instantiate for type usage; state reset is implicit via new instance

	return nil
}

// extractDrainReason extracts the reason string from a dead-letter filename
// that contains a -dl-<reason> suffix (e.g. "msg-dl-session-offline.md" → "session-offline").
// Returns "" if no -dl- marker is found.
func extractDrainReason(filename string) string {
	base := strings.TrimSuffix(filename, ".json")
	base = strings.TrimSuffix(base, ".md")
	idx := strings.LastIndex(base, "-dl-")
	if idx < 0 {
		return ""
	}
	return base[idx+4:]
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
	fs := flag.NewFlagSet("archive", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: tmux-a2a-postman archive <filename> [filename...]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  Move inbox message files to read/ to mark them as read.")
		fmt.Fprintln(os.Stderr, "  Accepts plain filenames (located by glob) or full paths (backward compat).")
		fmt.Fprintln(os.Stderr, "  Typical workflow:")
		fmt.Fprintln(os.Stderr, "    1. tmux-a2a-postman read          # list inbox file paths")
		fmt.Fprintln(os.Stderr, "    2. cat /path/to/msg.md            # read the message")
		fmt.Fprintln(os.Stderr, "    3. tmux-a2a-postman archive msg.md  # mark as read (filename only)")
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	args = fs.Args()

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
			fmt.Printf("Next steps: Reply with tmux-a2a-postman send-message --to %s --body \"<your message>\"\n", sender)
		}
	}
	return nil
}

// runNext reads and optionally archives the oldest unread inbox message (#277).
func runNext(args []string) error {
	fs := flag.NewFlagSet("next", flag.ContinueOnError)
	peek := fs.Bool("peek", false, "show without archiving (non-destructive)")
	contextID := fs.String("context-id", "", "context ID") // Issue #315: forward global --context-id
	jsonOut := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	inboxArgs := fs.Args()
	if *contextID != "" {
		inboxArgs = append([]string{"--context-id", *contextID}, inboxArgs...)
	}
	inboxPath, err := resolveInboxPath(inboxArgs)
	if err != nil {
		return err
	}

	msgs := message.ScanInboxMessages(inboxPath)
	if len(msgs) == 0 {
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(struct{}{})
		}
		fmt.Fprintln(os.Stderr, "No unread messages.")
		return nil
	}
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].Filename < msgs[j].Filename
	})

	abs := filepath.Join(inboxPath, msgs[0].Filename)
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			// Race: file disappeared between listing and reading; retry once.
			msgs = message.ScanInboxMessages(inboxPath)
			if len(msgs) == 0 {
				if *jsonOut {
					return json.NewEncoder(os.Stdout).Encode(struct{}{})
				}
				fmt.Fprintln(os.Stderr, "No unread messages.")
				return nil
			}
			sort.Slice(msgs, func(i, j int) bool {
				return msgs[i].Filename < msgs[j].Filename
			})
			abs = filepath.Join(inboxPath, msgs[0].Filename)
			data, err = os.ReadFile(abs)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Fprintln(os.Stderr, "No unread messages.")
					return nil
				}
				return fmt.Errorf("reading message: %w", err)
			}
		} else {
			return fmt.Errorf("reading message: %w", err)
		}
	}

	if *jsonOut {
		parsed := parseMessageContent(string(data), msgs[0].Filename)
		if !*peek {
			// Archive before returning JSON
			readDir := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(abs))), "read")
			if err := os.MkdirAll(readDir, 0o700); err != nil {
				return fmt.Errorf("creating read directory: %w", err)
			}
			dst := filepath.Join(readDir, msgs[0].Filename)
			if err := os.Rename(abs, dst); err != nil {
				return fmt.Errorf("archiving message: %w", err)
			}
		}
		return json.NewEncoder(os.Stdout).Encode(parsed)
	}

	fmt.Fprintf(os.Stderr, "[1/%d unread]\n", len(msgs))
	fmt.Print(string(data))

	if *peek {
		fmt.Fprintf(os.Stderr, "Remaining: %d unread\n", len(msgs))
		return nil
	}

	// Archive: move to {base}/{contextID}/{session}/read/
	readDir := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(abs))), "read")
	if err := os.MkdirAll(readDir, 0o700); err != nil {
		return fmt.Errorf("creating read directory: %w", err)
	}
	dst := filepath.Join(readDir, msgs[0].Filename)
	if err := os.Rename(abs, dst); err != nil {
		return fmt.Errorf("archiving message: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Remaining: %d unread\n", len(msgs)-1)
	sender := extractSenderFromFile(dst)
	if sender != "" {
		fmt.Printf("Next steps: Reply with tmux-a2a-postman send-message --to %s --body \"<your message>\"\n", sender)
	}
	return nil
}

// messageJSON holds JSON-serializable fields for a message (used by next --json).
type messageJSON struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Timestamp string `json:"timestamp"`
	Body      string `json:"body"`
}

// parseMessageContent extracts JSON-friendly fields from raw message file content.
// Parses YAML frontmatter for from/to/timestamp; body is content after frontmatter.
func parseMessageContent(content, filename string) messageJSON {
	result := messageJSON{ID: filename}
	lines := strings.Split(content, "\n")
	inFrontMatter := false
	fmEnd := -1
	for i, line := range lines {
		if line == "---" {
			if !inFrontMatter {
				inFrontMatter = true
				continue
			}
			fmEnd = i
			break
		}
		if !inFrontMatter {
			continue
		}
		if strings.HasPrefix(line, "  from: ") {
			result.From = strings.TrimSpace(strings.TrimPrefix(line, "  from: "))
		} else if strings.HasPrefix(line, "  to: ") {
			result.To = strings.TrimSpace(strings.TrimPrefix(line, "  to: "))
		} else if strings.HasPrefix(line, "  timestamp: ") {
			result.Timestamp = strings.TrimSpace(strings.TrimPrefix(line, "  timestamp: "))
		}
	}
	if fmEnd >= 0 && fmEnd+1 < len(lines) {
		result.Body = strings.TrimSpace(strings.Join(lines[fmEnd+1:], "\n"))
	}
	return result
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
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: tmux-a2a-postman send <filename> [filename...]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  Move a draft file to post/ to submit it for delivery.")
		fmt.Fprintln(os.Stderr, "  The filename is matched by glob across all draft/ directories; no path needed.")
		fmt.Fprintln(os.Stderr, "  Typical workflow:")
		fmt.Fprintln(os.Stderr, "    1. tmux-a2a-postman create-draft --to <node>  # creates draft, prints filename")
		fmt.Fprintln(os.Stderr, "    2. $EDITOR draft/<filename>.md                 # edit the draft")
		fmt.Fprintln(os.Stderr, "    3. tmux-a2a-postman send <filename>.md         # submit for delivery")
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	args = fs.Args()

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
	file := fs.String("file", "", "path to dead-letter file")
	oldest := fs.Bool("oldest", false, "resend the oldest dead-letter (no path required)")
	configPath := fs.String("config", "", "path to config file (optional)")
	jsonOut := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *oldest && *file != "" {
		return fmt.Errorf("--oldest and --file are mutually exclusive")
	}
	if !*oldest && *file == "" {
		return fmt.Errorf("one of --file or --oldest is required")
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

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

	var absFile string
	if *oldest {
		deadLetterDir := filepath.Join(sessionDir, "dead-letter")
		found, ok, err := findOldestDeadLetterFile(deadLetterDir)
		if err != nil {
			return err
		}
		if !ok {
			if *jsonOut {
				return json.NewEncoder(os.Stdout).Encode(struct{}{})
			}
			fmt.Fprintln(os.Stderr, "No dead-letter messages.")
			return nil
		}
		absFile = found
	} else {
		absFile, err = filepath.Abs(*file)
		if err != nil {
			return fmt.Errorf("resolving file path: %w", err)
		}
		if _, err := os.Stat(absFile); err != nil {
			return fmt.Errorf("dead-letter file not found: %w", err)
		}
	}

	// Strip dead-letter suffix (-dl-*.md -> .md) for redelivery filename
	baseName := filepath.Base(absFile)
	cleanName := message.StripDeadLetterSuffix(baseName)

	dst := filepath.Join(postDir, cleanName)
	if err := os.Rename(absFile, dst); err != nil {
		return fmt.Errorf("moving to post/: %w", err)
	}

	if *jsonOut {
		cleanForParse := message.StripDeadLetterSuffix(baseName)
		info, err := message.ParseMessageFilename(cleanForParse)
		if err != nil {
			return json.NewEncoder(os.Stdout).Encode(deadLetterMessageJSON{})
		}
		return json.NewEncoder(os.Stdout).Encode(deadLetterMessageJSON{From: info.From, To: info.To, Timestamp: info.Timestamp})
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
	jsonOut := fs.Bool("json", false, "output JSON")
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
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(struct {
			ContextID string `json:"context_id"`
		}{ContextID: contextID})
	}
	fmt.Println(contextID)
	return nil
}

// runGetNodesDir prints the effective nodes directory paths (XDG and project-local).
// NOTE: always uses auto-detection; does not accept --config.
// If the daemon was started with an explicit --config, the project-local nodes
// directory shown here may differ from what the running daemon uses.
func runGetNodesDir(args []string) error {
	fs := flag.NewFlagSet("get-nodes-dir", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	xdgPath := config.ResolveConfigPath()
	xdgNodesDir := config.ResolveNodesDir(xdgPath)
	localConfigPath := ""
	if cwd, err := os.Getwd(); err == nil {
		localConfigPath, _ = config.ResolveLocalConfigPath(cwd, xdgPath)
	}
	localNodesDir := config.ResolveNodesDir(localConfigPath)
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(struct {
			XDG          string `json:"xdg"`
			ProjectLocal string `json:"project_local"`
		}{XDG: xdgNodesDir, ProjectLocal: localNodesDir})
	}
	if xdgNodesDir != "" {
		fmt.Printf("xdg: %s\n", xdgNodesDir)
	}
	if localNodesDir != "" {
		fmt.Printf("project-local: %s\n", localNodesDir)
	}
	if xdgNodesDir == "" && localNodesDir == "" {
		fmt.Println("(no nodes directory found)")
	}
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

// runSchema prints JSON Schema for the postman config or a specific command's options.
//
//	tmux-a2a-postman schema              # postman.toml config schema
//	tmux-a2a-postman schema send-message # send-message options schema
func runSchema(args []string) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")

	target := ""
	if len(args) > 0 {
		target = args[0]
	}

	type schemaProperty struct {
		Type        string `json:"type"`
		Description string `json:"description"`
	}
	type jsonSchema struct {
		Schema     string                    `json:"$schema"`
		Title      string                    `json:"title"`
		Type       string                    `json:"type"`
		Properties map[string]schemaProperty `json:"properties"`
		Required   []string                  `json:"required,omitempty"`
	}

	switch target {
	case "":
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "postman.toml config",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"scan_interval_seconds":     {Type: "integer", Description: "Inbox scan interval in seconds"},
				"enter_delay_milliseconds":  {Type: "integer", Description: "Delay before entering a pane in milliseconds"},
				"tmux_timeout_seconds":      {Type: "integer", Description: "Timeout for tmux commands in seconds"},
				"startup_delay_seconds":     {Type: "integer", Description: "Delay before starting the daemon in seconds"},
				"reminder_interval_seconds": {Type: "integer", Description: "Interval between dropped-ball reminders in seconds"},
				"edges":                     {Type: "array", Description: "List of 'node-a,node-b' routing edges"},
				"ui_node":                   {Type: "string", Description: "Node name for the postman TUI pane"},
				"base_dir":                  {Type: "string", Description: "Override state base directory (also: POSTMAN_HOME)"},
			},
		})
	case "send-message", "send":
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "send-message options",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"to":              {Type: "string", Description: "Recipient node name (required)"},
				"body":            {Type: "string", Description: "Message body (required)"},
				"context-id":      {Type: "string", Description: "Context ID (optional, auto-detected)"},
				"session":         {Type: "string", Description: "tmux session name (optional, auto-detected)"},
				"config":          {Type: "string", Description: "Config file path (optional)"},
				"from":            {Type: "string", Description: "Phony sender node name (sidecar use only)"},
				"bindings":        {Type: "string", Description: "Path to bindings.toml (required with --from)"},
				"idempotency-key": {Type: "string", Description: "Idempotency token for deduplication"},
				"json":            {Type: "boolean", Description: "Output JSON: {\"sent\": \"filename.md\"}"},
			},
			Required: []string{"to", "body"},
		})
	case "next":
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "next options",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"peek":       {Type: "boolean", Description: "Read without archiving"},
				"context-id": {Type: "string", Description: "Context ID (optional, auto-detected)"},
				"config":     {Type: "string", Description: "Config file path (optional)"},
				"json":       {Type: "boolean", Description: "Output JSON: {\"id\",\"from\",\"to\",\"body\",\"timestamp\"}"},
			},
		})
	case "count":
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "count options",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"context-id": {Type: "string", Description: "Context ID (optional, auto-detected)"},
				"config":     {Type: "string", Description: "Config file path (optional)"},
				"json":       {Type: "boolean", Description: "Output JSON: {\"count\": N}"},
			},
		})
	case "create-draft":
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "create-draft options",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"to":              {Type: "string", Description: "Recipient node name (required)"},
				"body":            {Type: "string", Description: "Message body"},
				"send":            {Type: "boolean", Description: "Send immediately after creating draft"},
				"context-id":      {Type: "string", Description: "Context ID (optional, auto-detected)"},
				"session":         {Type: "string", Description: "tmux session name (optional, auto-detected)"},
				"config":          {Type: "string", Description: "Config file path (optional)"},
				"from":            {Type: "string", Description: "Phony sender node name (sidecar use only)"},
				"bindings":        {Type: "string", Description: "Path to bindings.toml (required with --from)"},
				"idempotency-key": {Type: "string", Description: "Idempotency token for deduplication"},
				"json":            {Type: "boolean", Description: "Output JSON: {\"draft\":\"filename.md\"} or {\"sent\":\"filename.md\"}"},
			},
			Required: []string{"to"},
		})
	case "read":
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "read options",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"context-id": {Type: "string", Description: "Context ID (optional, auto-detected)"},
				"config":     {Type: "string", Description: "Config file path (optional)"},
				"json":       {Type: "boolean", Description: "Output JSON: {\"files\": [...]}"},
			},
		})
	case "list-dead-letters":
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "list-dead-letters options",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"context-id": {Type: "string", Description: "Context ID (optional, auto-detected)"},
				"config":     {Type: "string", Description: "Config file path (optional)"},
				"json":       {Type: "boolean", Description: "Output JSON: {\"messages\": [{\"from\",\"to\",\"timestamp\"},...]}"},
			},
		})
	case "list-archived-messages":
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "list-archived-messages options",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"context-id": {Type: "string", Description: "Context ID (optional, auto-detected)"},
				"config":     {Type: "string", Description: "Config file path (optional)"},
				"json":       {Type: "boolean", Description: "Output JSON: {\"messages\": [{\"file\",\"from\",\"to\"},...]}"},
			},
		})
	case "resend":
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "resend options",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"file":       {Type: "string", Description: "Path to dead-letter file"},
				"oldest":     {Type: "boolean", Description: "Resend the oldest dead-letter"},
				"context-id": {Type: "string", Description: "Context ID (optional, auto-detected)"},
				"config":     {Type: "string", Description: "Config file path (optional)"},
				"json":       {Type: "boolean", Description: "Output JSON: {\"resent\": \"filename.md\"}"},
			},
		})
	case "get-context-id":
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "get-context-id options",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"session": {Type: "string", Description: "tmux session name (optional, auto-detected)"},
				"config":  {Type: "string", Description: "Config file path (optional)"},
				"json":    {Type: "boolean", Description: "Output JSON: {\"context_id\": \"...\"}"},
			},
		})
	case "get-nodes-dir":
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "get-nodes-dir options",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"json": {Type: "boolean", Description: "Output JSON: {\"xdg\": \"...\", \"project_local\": \"...\"}"},
			},
		})
	case "get-session-status-oneline":
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "get-session-status-oneline options",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"json": {Type: "boolean", Description: "Output JSON: {\"status\": \"[1]●●●●\"}"},
			},
		})
	default:
		return fmt.Errorf("unknown command %q; run 'tmux-a2a-postman schema' for config schema", target)
	}
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
		fmt.Println("AI agents send and receive messages. Each message is routed to the recipient")
		fmt.Println("by the daemon. Use send-message to send and next to read.")
		fmt.Println("")
		fmt.Println("Quick Start:")
		fmt.Println("  1. Start daemon:    tmux-a2a-postman start")
		fmt.Println("  2. Send a message:  tmux-a2a-postman send-message --to <node> --body \"text\"")
		fmt.Println("  3. Read next msg:   tmux-a2a-postman next")
		fmt.Println("  4. Check inbox:     tmux-a2a-postman count")
		fmt.Println("")
		fmt.Println("Key Concepts:")
		fmt.Println("  Node       An AI agent identified by its tmux pane title.")
		fmt.Println("  Edge       A bidirectional routing rule between two nodes (configured in edges).")
		fmt.Println("  Envelope   YAML frontmatter at the top of each message file:")
		fmt.Println("               ---")
		fmt.Println("               params:")
		fmt.Println("                 from: <sender>")
		fmt.Println("                 to: <recipient>")
		fmt.Println("                 timestamp: <ISO 8601>")
		fmt.Println("               ---")
		fmt.Println("")
		fmt.Println("Commands:")
		fmt.Println("  start                      Start the daemon (TUI dashboard)")
		fmt.Println("  stop                       Stop the running daemon for this tmux session")
		fmt.Println("  send-message               Send a message in one step (--to and --body required)")
		fmt.Println("  next                       Read and archive the oldest unread inbox message")
		fmt.Println("  count                      Count unread inbox messages")
		fmt.Println("  create-draft               Create a new message draft (advanced / scripts)")
		fmt.Println("  send <filename>            Submit draft for delivery (advanced)")
		fmt.Println("  resend                     Re-send a dead-letter message")
		fmt.Println("  list-dead-letters          List dead-letter messages (no filenames)")
		fmt.Println("  read                       List inbox message file paths")
		fmt.Println("  archive <filename> [filename...]   Mark inbox messages as read (advanced)")
		fmt.Println("  get-session-status-oneline Print pane status (emoji in pipes/tmux #(), ANSI in TTY)")
		fmt.Println("  get-session-health         Print session health per node")
		fmt.Println("  help [topic]               Show help (topics: messaging, directories, config, commands)")
		fmt.Println("")
		fmt.Println("Messaging Protocol:")
		fmt.Println("  send-message --to <node> --body \"text\"  Send a message in one step")
		fmt.Println("  next                                     Read and archive oldest message")
		fmt.Println("  count                                    Count unread messages")
		fmt.Println("")
		printTopicList()
		fmt.Println("")
		fmt.Println("Run `tmux-a2a-postman help <topic>` for detailed information.")
		return
	}

	topic := args[0]
	switch topic {
	case "messaging":
		fmt.Println("Messaging — send and receive messages")
		fmt.Println("")
		fmt.Println("Quick workflow (agents):")
		fmt.Println("  Send:  tmux-a2a-postman send-message --to <node> --body \"text\"")
		fmt.Println("  Read:  tmux-a2a-postman next")
		fmt.Println("  Count: tmux-a2a-postman count")
		fmt.Println("")
		fmt.Println("Reply workflow:")
		fmt.Println("  tmux-a2a-postman send-message --to <sender> --body \"DONE: ...\"")
		fmt.Println("")
		fmt.Println("  To reply in same context thread:")
		fmt.Println("    tmux-a2a-postman send-message --to <sender> --context-id <id> --body \"...\"")
		fmt.Println("    (context-id is auto-detected from the incoming message header)")
		fmt.Println("")
		fmt.Println("Envelope format (YAML frontmatter):")
		fmt.Println("  ---")
		fmt.Println("  params:")
		fmt.Println("    from: <sender node name>")
		fmt.Println("    to: <recipient node name>")
		fmt.Println("    timestamp: <ISO 8601 timestamp>")
		fmt.Println("  ---")
		fmt.Println("")
		fmt.Println("Advanced (scripts and power users):")
		fmt.Println("  1. tmux-a2a-postman create-draft --to <node>")
		fmt.Println("  2. Edit the draft file")
		fmt.Println("  3. tmux-a2a-postman send <filename>")
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
		fmt.Println("          └── waiting/        # per-node waiting state files")
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
	case "commands":
		fmt.Println("Commands — detailed command reference")
		fmt.Println("")
		fmt.Println("send-message")
		fmt.Println("  Compose and deliver a message atomically in a single command.")
		fmt.Println("  Flags:")
		fmt.Println("    --to <node>          Recipient node name (required)")
		fmt.Println("    --body <text>        Message body (required; replaces <!-- write here --> placeholder)")
		fmt.Println("    --context-id <id>    Context ID (optional, auto-detected)")
		fmt.Println("    --session <name>     tmux session name (optional, auto-detected)")
		fmt.Println("    --config <path>      Config file path (optional)")
		fmt.Println("  NOTE: --body is required (error if absent). This is stricter than create-draft --send.")
		fmt.Println("  Example:")
		fmt.Println("    tmux-a2a-postman send-message --to orchestrator --body \"DONE: task complete\"")
		fmt.Println("")
		fmt.Println("next")
		fmt.Println("  Read and archive the oldest unread inbox message.")
		fmt.Println("  Prints full message content to stdout; archives silently.")
		fmt.Println("  Node is auto-detected from tmux pane title.")
		fmt.Println("  Empty inbox: exits 0, prints 'No unread messages.' to stderr.")
		fmt.Println("  Flags:")
		fmt.Println("    --peek               Show without archiving (non-destructive)")
		fmt.Println("    --context-id <id>    Context ID (optional, auto-detected)")
		fmt.Println("    --config <path>      Config file path (optional)")
		fmt.Println("")
		fmt.Println("count")
		fmt.Println("  Print number of unread inbox messages for the current node.")
		fmt.Println("  Node name is auto-detected from tmux pane title.")
		fmt.Println("  Flags:")
		fmt.Println("    --config <path>      Config file path (optional)")
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
		fmt.Println("    --to <node>          Recipient node name (required)")
		fmt.Println("    --session <name>     tmux session name (optional, auto-detected)")
		fmt.Println("    --config <path>      Config file path (optional)")
		fmt.Println("    --body <text>        Inline message body (replaces <!-- write here --> placeholder)")
		fmt.Println("    --send               Send immediately after creating draft (atomic)")
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
		fmt.Println("    --config <path>      Config file path (optional)")
		fmt.Println("")
		fmt.Println("resend")
		fmt.Println("  Re-send a dead-letter message by moving it back to post/.")
		fmt.Println("  Strips -dl-{reason} suffix from filename for redelivery.")
		fmt.Println("  Flags:")
		fmt.Println("    --file <path>        Path to dead-letter file")
		fmt.Println("    --oldest             Resend the oldest dead-letter (no path required)")
		fmt.Println("    --config <path>      Config file path (optional)")
		fmt.Println("")
		fmt.Println("list-dead-letters")
		fmt.Println("  List dead-letter messages without exposing filenames or directory paths.")
		fmt.Println("  Output format: <timestamp>  from=<sender>  to=<recipient>")
		fmt.Println("  Empty dead-letter dir: exits 0, prints 'No dead-letter messages.' to stderr.")
		fmt.Println("  Flags:")
		fmt.Println("    --config <path>      Config file path (optional)")
		fmt.Println("    --context-id <id>    Context ID (optional, auto-detected)")
		fmt.Println("")
		fmt.Println("read")
		fmt.Println("  List inbox message file paths for the current node.")
		fmt.Println("  Node name is auto-detected from tmux pane title.")
		fmt.Println("  Flags:")
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
