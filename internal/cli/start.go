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
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/fswatcher/fswatcher"
	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/daemon"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/lock"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/ping"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
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

func restrictPingTargetsToConfiguredUINode(nodes map[string]discovery.NodeInfo, cfg *config.Config) (map[string]discovery.NodeInfo, bool) {
	if cfg == nil || !cfg.HasExplicitUINodeSetting() || cfg.UINode == "" {
		return cliutil.FilterToUINode(nodes, ""), true
	}

	filtered := cliutil.FilterToUINode(nodes, cfg.UINode)
	return filtered, len(filtered) > 0
}

func pingTargetsForSession(nodes map[string]discovery.NodeInfo, sessionName string) map[string]discovery.NodeInfo {
	target := make(map[string]discovery.NodeInfo)
	for nodeName, nodeInfo := range nodes {
		if nodeInfo.SessionName == sessionName {
			target[nodeName] = nodeInfo
		}
	}
	return target
}

func activePingNodeNames(nodes map[string]discovery.NodeInfo) []string {
	activeNodes := make([]string, 0, len(nodes))
	seen := make(map[string]bool)
	for nodeName := range nodes {
		simpleName := ping.ExtractSimpleName(nodeName)
		if seen[simpleName] {
			continue
		}
		seen[simpleName] = true
		activeNodes = append(activeNodes, simpleName)
	}
	sort.Strings(activeNodes)
	return activeNodes
}

func sendCompactionPings(contextID string, cfg *config.Config, idleTracker *idle.IdleTracker, nodes map[string]discovery.NodeInfo, targets []idle.CompactionPingTarget) {
	if len(targets) == 0 {
		return
	}

	activeNodes := activePingNodeNames(nodes)
	livenessMap := idleTracker.GetLivenessMap()
	pingAdjacency, err := config.ParseEdges(cfg.Edges)
	if err != nil || pingAdjacency == nil {
		pingAdjacency = map[string][]string{}
	}

	for _, target := range targets {
		nodeInfo, ok := nodes[target.NodeKey]
		if !ok {
			continue
		}
		options := ping.SendOptions{CompactionTriggered: true, Runtime: target.Runtime}
		if _, err := ping.SendPingToNodeWithOptions(nodeInfo, contextID, target.NodeKey, cfg.DaemonMessageTemplate, cfg, activeNodes, livenessMap, pingAdjacency, nodes, options); err != nil {
			log.Printf("postman: compaction-triggered PING failed for %s: %v\n", target.NodeKey, err)
			continue
		}
		log.Printf("postman: compaction-triggered PING sent to %s trigger=%s runtime=%s\n", target.NodeKey, target.Trigger, target.Runtime)
	}
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

	startPreflight := planStartPreflight(startPreflightInput{
		BaseDir:         baseDir,
		ContextID:       contextID,
		SessionName:     sessionName,
		TmuxSessionName: tmuxSessionName,
	})
	if startPreflight.Err != nil {
		return startPreflight.Err
	}

	lockDir := filepath.Join(baseDir, "lock")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		return fmt.Errorf("creating lock directory: %w", err)
	}
	userLockObj, err := lock.NewSessionLock(config.CurrentUserDaemonLockPath(baseDir))
	if err != nil {
		return fmt.Errorf("a postman daemon is already starting or running for this user: %w", err)
	}
	defer func() { _ = userLockObj.Release() }()

	if err := config.CreateMultiSessionDirs(contextDir, sessionName); err != nil {
		return fmt.Errorf("creating session directories: %w", err)
	}
	journalManager := journal.NewManager(contextID, os.Getpid())
	journal.InstallProcessManager(journalManager)
	defer journal.ClearProcessManager()
	if err := journalManager.Bootstrap(sessionDir, sessionName, time.Now()); err != nil {
		log.Printf("postman: WARNING: journal shadow bootstrap failed for %s: %v\n", sessionName, err)
	}

	if tmuxSessionName == "" {
		log.Println("warning: postman: could not determine tmux session name; running without session lock")
	} else {
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
	startupActivatedSessions := activateStartupSessions(baseDir, contextDir, contextID, sessionName, cfg)

	inboxDir := filepath.Join(sessionDir, "inbox")
	readDir := filepath.Join(sessionDir, "read")

	// Drain stale post/ messages (Issue #207)
	if drained := message.DrainStalePost(sessionDir, cfg.MessageTTLSeconds); drained > 0 {
		log.Printf("postman: drained %d stale post/ messages at startup\n", drained)
	}
	if removed, err := cleanupExpiredRuntimeState(baseDir, contextID, cfg.RetentionPeriodDays, time.Now()); err != nil {
		log.Printf("postman: WARNING: retention cleanup skipped: %v\n", err)
	} else if removed > 0 {
		log.Printf("postman: pruned %d expired runtime path(s) at startup\n", removed)
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

	watcher, err := fswatcher.NewWatcher()
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
	activationNodes := activationNodeNames(cfg)
	nodes = filterDiscoveredActivationNodes(nodes, activationNodes)
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
		activationNodesLocal := activationNodeNames(cfg)
		fresh = filterDiscoveredActivationNodes(fresh, activationNodesLocal)
		sharedNodes.Store(&fresh)
		log.Printf("postman: startup re-discovery complete (%d nodes)\n", len(fresh))
	})

	// Log collisions for edge nodes after edge filter
	for _, collision := range startupCollisions {
		if !config.EdgeNodeAllowed(activationNodes, collision.NodeKey) {
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
			if err := watcher.Add(nodePostDir, fswatcher.All); err != nil {
				log.Printf("⚠️  postman: warning: could not watch %s post directory: %v\n", nodeName, err)
			} else {
				watchedDirs[nodePostDir] = true
			}
		}
		if !watchedDirs[nodeInboxDir] {
			if err := watcher.Add(nodeInboxDir, fswatcher.All); err != nil {
				log.Printf("⚠️  postman: warning: could not watch %s inbox directory: %v\n", nodeName, err)
			} else {
				watchedDirs[nodeInboxDir] = true
			}
		}
		nodeReadDir := filepath.Join(nodeInfo.SessionDir, "read")
		if !watchedDirs[nodeReadDir] {
			if err := watcher.Add(nodeReadDir, fswatcher.All); err != nil {
				log.Printf("⚠️  postman: warning: could not watch %s read directory: %v\n", nodeName, err)
			} else {
				watchedDirs[nodeReadDir] = true
			}
		}
		submitRequestsDir := projection.DaemonSubmitRequestsDir(nodeInfo.SessionDir)
		if err := projection.EnsureDaemonSubmitDirs(nodeInfo.SessionDir); err != nil {
			log.Printf("postman: WARNING: component=%s event=dirs_create_failed submit_path=%s node=%s err=%v\n", projection.SubmitPathDaemon, projection.SubmitPathDaemon, nodeName, err)
		} else if !watchedDirs[submitRequestsDir] {
			if err := watcher.Add(submitRequestsDir, fswatcher.All); err != nil {
				log.Printf("postman: WARNING: component=%s event=watch_failed submit_path=%s node=%s err=%v\n", projection.SubmitPathDaemon, projection.SubmitPathDaemon, nodeName, err)
			} else {
				watchedDirs[submitRequestsDir] = true
			}
		}
	}

	// Also watch default session directories (for postman's own messages)
	if !watchedDirs[postDir] {
		if err := watcher.Add(postDir, fswatcher.All); err != nil {
			return fmt.Errorf("watching post directory: %w", err)
		}
		watchedDirs[postDir] = true
	}
	if !watchedDirs[inboxDir] {
		if err := watcher.Add(inboxDir, fswatcher.All); err != nil {
			return fmt.Errorf("watching inbox directory: %w", err)
		}
		watchedDirs[inboxDir] = true
	}
	if !watchedDirs[readDir] {
		if err := watcher.Add(readDir, fswatcher.All); err != nil {
			log.Printf("⚠️  postman: warning: could not watch read directory: %v\n", err)
		} else {
			watchedDirs[readDir] = true
		}
	}
	submitRequestsDir := projection.DaemonSubmitRequestsDir(sessionDir)
	if err := projection.EnsureDaemonSubmitDirs(sessionDir); err != nil {
		log.Printf("postman: WARNING: component=%s event=dirs_create_failed submit_path=%s session=%s err=%v\n", projection.SubmitPathDaemon, projection.SubmitPathDaemon, sessionName, err)
	} else if !watchedDirs[submitRequestsDir] {
		if err := watcher.Add(submitRequestsDir, fswatcher.All); err != nil {
			log.Printf("postman: WARNING: component=%s event=watch_failed submit_path=%s session=%s err=%v\n", projection.SubmitPathDaemon, projection.SubmitPathDaemon, sessionName, err)
		} else {
			watchedDirs[submitRequestsDir] = true
		}
	}

	// Snapshot global/explicit config at startup. Config, postman.md, and nodes/*
	// changes require a daemon restart so in-progress edits cannot mutate runtime
	// routing or role templates.
	resolvedConfigPath := configPath
	if resolvedConfigPath == "" {
		resolvedConfigPath = config.ResolveConfigPath()
	}

	log.Printf("📮 postman: daemon started (context=%s, pid=%d, nodes=%d)\n",
		contextID, os.Getpid(), len(nodes))

	// Track known nodes for new node detection
	knownNodes := make(map[string]bool)
	for nodeName := range nodes {
		knownNodes[nodeName] = true
	}

	// Issue #71: Create state management instances
	daemonState := daemon.NewDaemonState(cfg.StartupDrainWindowSeconds, contextID)
	daemonState.AutoEnableSessionIfNew(sessionName)
	for _, activatedSession := range startupActivatedSessions {
		daemonState.AutoEnableSessionIfNew(activatedSession)
	}
	if cfg.StartupDrainWindowSeconds > 0 {
		log.Printf("postman: startup drain window active (%.0fs) — session-enabled check bypassed (#217)\n", cfg.StartupDrainWindowSeconds)
	}
	idleTracker := idle.NewIdleTracker()

	// Start pane capture check goroutine (hybrid idle detection)
	idleTracker.StartPaneCaptureCheck(ctx, cfg, baseDir, contextID, sessionName, func(nodes map[string]discovery.NodeInfo, targets []idle.CompactionPingTarget) {
		sendCompactionPings(contextID, cfg, idleTracker, nodes, targets)
	})

	// Start daemon loop in goroutine
	daemonEvents := make(chan tui.DaemonEvent, 100)
	var tuiEvents chan tui.DaemonEvent
	if !noTUI {
		tuiEvents = make(chan tui.DaemonEvent, 200)
	}
	safeGo("tui-status-relay", nil, func() {
		relayDaemonEventsToTUI(ctx, daemonEvents, tuiEvents, baseDir, contextID, cfg)
	})
	safeGo("daemon-loop", daemonEvents, func() {
		daemon.RunDaemonLoop(ctx, baseDir, sessionDir, contextID, cfg, watcher, adjacency, nodes, knownNodes, daemonEvents, resolvedConfigPath, nil, nil, daemonState, idleTracker, &sharedNodes, sessionName)
	})

	// Issue #117: Discover all tmux sessions
	allSessions, _ := discovery.DiscoverAllSessions()
	if allSessions == nil {
		allSessions = []string{}
	}

	// Build session info from nodes (all disabled by default)
	sessionList := session.BuildSessionList(nodes, allSessions, daemonState.GetConfiguredSessionEnabled)
	for _, sessionInfo := range sessionList {
		if _, err := refreshProjectedSessionStatus(baseDir, contextID, sessionInfo.Name, cfg); err != nil {
			log.Printf("postman: WARNING: initial session status snapshot skipped %s: %v\n", sessionInfo.Name, err)
		}
	}

	// Send initial status
	daemonEvents <- tui.DaemonEvent{
		Type:    "status_update",
		Message: "Running",
		Details: map[string]interface{}{
			"node_count": len(nodes),
			"sessions":   sessionList,
		},
	}

	// Send initial session list.
	daemonEvents <- tui.DaemonEvent{
		Type: "config_update",
		Details: map[string]interface{}{
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
					return
				case cmd := <-tuiCommands:
					// Issue #47: Handle TUI commands
					switch cmd.Type {
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
						activationNodesFilter := activationNodeNames(cfg)
						targetNodes := pingTargetsForSession(freshNodes, cmd.Target)
						if cachedPtr == nil || len(targetNodes) == 0 {
							activationBlocked := false
							// Attempt a fresh discovery before giving up (catches panes
							// that set titles after startup or after the last scan).
							freshDiscovered, _, discErr := discovery.DiscoverNodesWithCollisions(baseDir, contextID, sessionName)
							if discErr == nil && len(freshDiscovered) > 0 {
								freshNodes = filterDiscoveredActivationNodes(freshDiscovered, activationNodesFilter)
								sharedNodes.Store(&freshNodes)
								targetNodes = pingTargetsForSession(freshNodes, cmd.Target)
							}
							if len(targetNodes) == 0 {
								activatedNodes, activationErr := activateSessionForPing(baseDir, contextDir, contextID, sessionName, cmd.Target, cfg, watcher, watchedDirs)
								switch {
								case activationErr == nil:
									daemonState.SetSessionEnabled(cmd.Target, true)
									freshNodes = activatedNodes
									sharedNodes.Store(&freshNodes)
									targetNodes = pingTargetsForSession(freshNodes, cmd.Target)
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
						// Build active nodes from freshNodes (not stale startup nodes)
						activeNodes := activePingNodeNames(freshNodes)
						// Send PING to all discovered nodes in the target session.
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

func cleanupExpiredRuntimeState(baseDir, activeContextID string, retentionDays int, now time.Time) (int, error) {
	if retentionDays <= 0 || baseDir == "" {
		return 0, nil
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("reading base dir: %w", err)
	}

	cutoff := now.AddDate(0, 0, -retentionDays)
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		contextID := entry.Name()
		if contextID == "lock" || contextID == activeContextID {
			continue
		}
		if config.ContextHasLiveDaemon(baseDir, contextID) {
			continue
		}

		contextDir := filepath.Join(baseDir, contextID)
		contextRemoved, err := cleanupExpiredContextRuntime(contextDir, cutoff)
		if err != nil {
			return removed, fmt.Errorf("cleaning context %s: %w", contextID, err)
		}
		removed += contextRemoved

		empty, err := isDirectoryEmpty(contextDir)
		if err != nil {
			return removed, fmt.Errorf("checking context %s emptiness: %w", contextID, err)
		}
		if empty {
			if err := os.Remove(contextDir); err != nil && !os.IsNotExist(err) {
				return removed, fmt.Errorf("removing empty context %s: %w", contextID, err)
			}
		}
	}

	return removed, nil
}

func cleanupExpiredContextRuntime(contextDir string, cutoff time.Time) (int, error) {
	entries, err := os.ReadDir(contextDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("reading context dir: %w", err)
	}

	removed := 0
	for _, entry := range entries {
		if !isRetentionEligibleContextEntry(contextDir, entry) {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			return removed, fmt.Errorf("stat %s: %w", entry.Name(), err)
		}
		if info.ModTime().After(cutoff) {
			continue
		}

		path := filepath.Join(contextDir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return removed, fmt.Errorf("removing %s: %w", entry.Name(), err)
		}
		removed++
	}

	return removed, nil
}

func isRetentionEligibleContextEntry(contextDir string, entry os.DirEntry) bool {
	name := entry.Name()
	if entry.IsDir() {
		return isSessionRuntimeDir(filepath.Join(contextDir, name))
	}
	return name == "postman.log" || name == "pane-activity.json"
}

func isSessionRuntimeDir(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}

	recognized := false
	for _, entry := range entries {
		if entry.IsDir() {
			if !isKnownSessionRuntimeSubdir(entry.Name()) {
				return false
			}
			recognized = true
			continue
		}
		if entry.Name() == "postman.pid" {
			recognized = true
			continue
		}
		return false
	}

	return recognized
}

func isKnownSessionRuntimeSubdir(name string) bool {
	switch name {
	case "inbox", "post", "draft", "read", "dead-letter":
		return true
	default:
		return false
	}
}

func isDirectoryEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	return len(entries) == 0, nil
}
