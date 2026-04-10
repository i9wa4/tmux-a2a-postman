package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/fsnotify/fsnotify"
	"github.com/i9wa4/tmux-a2a-postman/internal/alert"
	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/daemon"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/lock"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
	"github.com/i9wa4/tmux-a2a-postman/internal/ping"
	"github.com/i9wa4/tmux-a2a-postman/internal/reminder"
	"github.com/i9wa4/tmux-a2a-postman/internal/session"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
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

func RunStartWithFlags(contextID, configPath, logFilePath string, noTUI bool) error {
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
	} else {
		sessionName, err = config.ValidateSessionName(sessionName)
		if err != nil {
			return fmt.Errorf("start: invalid session name: %w", err)
		}
	}
	sessionDir := filepath.Join(contextDir, sessionName)

	if err := config.CreateMultiSessionDirs(contextDir, sessionName); err != nil {
		return fmt.Errorf("creating session directories: %w", err)
	}

	if tmuxSessionName == "" {
		log.Println("warning: postman: could not determine tmux session name; running without session lock")
	} else {
		// Issue #249: Startup guard — detect duplicate daemon for this context+session.
		// Scope check to contextID only: same-context duplicates are rejected via postman.pid,
		// and a tmux-session-wide lock below blocks cross-context same-session startups.
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
	if err := config.SetSessionEnabledMarker(contextID, sessionName, true); err != nil {
		return fmt.Errorf("publishing enabled-session marker for %s: %w", sessionName, err)
	}

	inboxDir := filepath.Join(sessionDir, "inbox")
	readDir := filepath.Join(sessionDir, "read")

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
			if !config.ContextOwnsSession(baseDir, claimedContext, paneSessionName) {
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

	// Watch the same TOML config files LoadConfig effectively applies.
	resolvedConfigPath := configPath
	if resolvedConfigPath == "" {
		resolvedConfigPath = config.ResolveConfigPath()
	}
	watchedConfigPaths := resolveWatchedConfigPaths(configPath)
	for _, watchedConfigPath := range watchedConfigPaths {
		if err := watcher.Add(watchedConfigPath); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  postman: warning: could not watch config: %v\n", err)
		}
	}

	// Issue #50: Watch nodes/ directory if exists
	watchedNodesDirs := resolveWatchedNodesDirs(watchedConfigPaths)
	for _, nodesDir := range watchedNodesDirs {
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
	tuiEvents := daemonEvents
	if !noTUI {
		tuiEvents = make(chan tui.DaemonEvent, 200)
		safeGo("tui-health-relay", nil, func() {
			relayDaemonEventsToTUI(ctx, daemonEvents, tuiEvents, baseDir, contextID, cfg)
		})
	}
	safeGo("daemon-loop", daemonEvents, func() {
		daemon.RunDaemonLoop(ctx, baseDir, sessionDir, contextID, cfg, watcher, adjacency, nodes, knownNodes, reminderState, daemonEvents, resolvedConfigPath, watchedConfigPaths, watchedNodesDirs, daemonState, idleTracker, alertRateLimiter, &sharedNodes, sessionName)
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
								// Register newly created session dirs with the watcher.
								newSessionDir := filepath.Join(contextDir, cmd.Target)
								for _, subdir := range []string{"post", "inbox", "read"} {
									dirToWatch := filepath.Join(newSessionDir, subdir)
									if !watchedDirs[dirToWatch] {
										if err := watcher.Add(dirToWatch); err != nil {
											log.Printf("postman: watcher.Add %s: %v\n", dirToWatch, err)
										} else {
											watchedDirs[dirToWatch] = true
										}
									}
								}
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
						} else {
							// Unregister disabled session dirs from the watcher.
							disabledSessionDir := filepath.Join(contextDir, cmd.Target)
							for _, subdir := range []string{"post", "inbox", "read"} {
								dirToRemove := filepath.Join(disabledSessionDir, subdir)
								if err := watcher.Remove(dirToRemove); err != nil {
									log.Printf("postman: watcher.Remove %s: %v\n", dirToRemove, err)
								}
								delete(watchedDirs, dirToRemove)
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
						filterTargetNodes := func(nodes map[string]discovery.NodeInfo) map[string]discovery.NodeInfo {
							target := make(map[string]discovery.NodeInfo)
							for k, v := range nodes {
								if v.SessionName == cmd.Target {
									target[k] = v
								}
							}
							return target
						}
						targetNodes := filterTargetNodes(freshNodes)
						if cachedPtr == nil || len(targetNodes) == 0 {
							activationBlocked := false
							// Attempt a fresh discovery before giving up (catches panes
							// that set titles after startup or after the last scan).
							freshDiscovered, _, discErr := discovery.DiscoverNodesWithCollisions(baseDir, contextID, sessionName)
							if discErr == nil && len(freshDiscovered) > 0 {
								freshNodes = filterDiscoveredEdgeNodes(freshDiscovered, edgeNodesFilter)
								sharedNodes.Store(&freshNodes)
								targetNodes = filterTargetNodes(freshNodes)
							}
							if len(targetNodes) == 0 {
								activatedNodes, activationErr := activateSessionForPing(baseDir, contextDir, contextID, sessionName, cmd.Target, cfg, watcher, watchedDirs)
								switch {
								case activationErr == nil:
									freshNodes = activatedNodes
									sharedNodes.Store(&freshNodes)
									targetNodes = filterTargetNodes(freshNodes)
									daemonEvents <- tui.DaemonEvent{
										Type:    "status_update",
										Message: fmt.Sprintf("Activated session %s for ping", cmd.Target),
										Details: map[string]interface{}{"session": cmd.Target},
									}
								case errors.Is(activationErr, errPingSessionOwned):
									activationBlocked = true
									log.Printf("postman: PING blocked for session %s — %v\n", cmd.Target, activationErr)
									daemonEvents <- tui.DaemonEvent{
										Type:    "status_update",
										Message: fmt.Sprintf("Session %s is owned by another daemon", cmd.Target),
										Details: map[string]interface{}{"session": cmd.Target},
									}
								default:
									activationBlocked = true
									log.Printf("postman: session activation for PING failed on %s: %v\n", cmd.Target, activationErr)
									daemonEvents <- tui.DaemonEvent{
										Type:    "status_update",
										Message: fmt.Sprintf("Failed to activate session %s", cmd.Target),
										Details: map[string]interface{}{"session": cmd.Target},
									}
								}
							}
							if len(targetNodes) == 0 {
								if activationBlocked {
									break
								}
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
						// Restrict ping to ui_node only (if configured).
						targetNodes = cliutil.FilterToUINode(targetNodes, cfg.UINode)
						if len(targetNodes) == 0 {
							log.Printf("postman: PING skipped for session %s — ui_node %q not found\n", cmd.Target, cfg.UINode)
							daemonEvents <- tui.DaemonEvent{
								Type:    "status_update",
								Message: fmt.Sprintf("ui_node %q not found in session %s", cfg.UINode, cmd.Target),
								Details: map[string]interface{}{"session": cmd.Target},
							}
							break
						}
						// Build active nodes from freshNodes (not stale startup nodes)
						activeNodes := make([]string, 0, len(freshNodes))
						for nodeName := range freshNodes {
							simpleName := ping.ExtractSimpleName(nodeName)
							activeNodes = append(activeNodes, simpleName)
						}
						// Send PING to ui_node in the target session.
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

		p := tea.NewProgram(tui.InitialModel(tuiEvents, tuiCommands, cfg, contextID))
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

func resolveWatchedConfigPaths(configPath string) []string {
	seen := make(map[string]bool)
	paths := make([]string, 0, 2)
	add := func(path string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}

	xdgPath := config.ResolveConfigPath()
	if configPath != "" {
		add(configPath)
	} else {
		add(xdgPath)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return paths
	}

	localPath, err := config.ResolveLocalConfigPath(cwd, xdgPath)
	if err != nil {
		return paths
	}
	add(localPath)

	return paths
}

func resolveWatchedNodesDirs(configPaths []string) []string {
	seen := make(map[string]bool)
	nodesDirs := make([]string, 0, len(configPaths))
	for _, configPath := range configPaths {
		nodesDir := config.ResolveNodesDir(configPath)
		if nodesDir == "" || seen[nodesDir] {
			continue
		}
		seen[nodesDir] = true
		nodesDirs = append(nodesDirs, nodesDir)
	}
	return nodesDirs
}
