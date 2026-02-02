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
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"
)

var revision string

// ReminderState manages per-node message counters for reminder feature.
type ReminderState struct {
	mu       sync.Mutex
	counters map[string]int
}

// NewReminderState creates a new ReminderState.
func NewReminderState() *ReminderState {
	return &ReminderState{
		counters: make(map[string]int),
	}
}

// Increment increments the counter for a node and sends reminder if threshold is reached.
func (r *ReminderState) Increment(nodeName string, nodes map[string]string, cfg *Config) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.counters[nodeName]++
	count := r.counters[nodeName]

	// Check if reminder should be sent
	if cfg.ReminderInterval > 0 && count >= int(cfg.ReminderInterval) {
		// Get node-specific reminder settings
		nodeConfig, hasNodeConfig := cfg.Nodes[nodeName]
		reminderInterval := cfg.ReminderInterval
		reminderMessage := cfg.ReminderMessage

		if hasNodeConfig {
			if nodeConfig.ReminderInterval > 0 {
				reminderInterval = nodeConfig.ReminderInterval
			}
			if nodeConfig.ReminderMessage != "" {
				reminderMessage = nodeConfig.ReminderMessage
			}
		}

		// Send reminder if interval is configured
		if reminderInterval > 0 && count >= int(reminderInterval) {
			paneID, found := nodes[nodeName]
			if found && reminderMessage != "" {
				vars := map[string]string{
					"node":  nodeName,
					"count": strconv.Itoa(count),
				}
				timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
				content := ExpandTemplate(reminderMessage, vars, timeout)

				if err := exec.Command("tmux", "send-keys", "-t", paneID, content, "Enter").Run(); err != nil {
					fmt.Fprintf(os.Stderr, "postman: reminder to %s failed: %v\n", nodeName, err)
				} else {
					fmt.Printf("postman: reminder sent to %s (count=%d)\n", nodeName, count)
				}
			}
			// Reset counter after sending reminder
			r.counters[nodeName] = 0
		}
	}
}

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
	configPath := fs.String("config", "", "path to config file (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *contextID == "" {
		return fmt.Errorf("--context-id is required")
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Parse edge definitions for routing
	adjacency, err := ParseEdges(cfg.Edges)
	if err != nil {
		return fmt.Errorf("parsing edges: %w", err)
	}

	baseDir := resolveBaseDir(cfg.BaseDir)
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
	inboxDir := filepath.Join(sessionDir, "inbox")

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
		resolvedConfigPath = resolveConfigPath()
	}
	if resolvedConfigPath != "" {
		if err := watcher.Add(resolvedConfigPath); err != nil {
			fmt.Fprintf(os.Stderr, "postman: warning: could not watch config: %v\n", err)
		}
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

	// Send PING to all nodes after startup delay
	if cfg.StartupDelay > 0 {
		startupDelay := time.Duration(cfg.StartupDelay * float64(time.Second))
		time.AfterFunc(startupDelay, func() {
			sendPingToAll(sessionDir, *contextID, cfg)
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
	reminderState := NewReminderState()

	// Start daemon loop in goroutine
	daemonEvents := make(chan DaemonEvent, 100)
	go runDaemonLoop(ctx, sessionDir, *contextID, cfg, watcher, adjacency, nodes, knownNodes, digestedFiles, reminderState, daemonEvents, resolvedConfigPath)

	// Send initial status
	daemonEvents <- DaemonEvent{
		Type:    "status_update",
		Message: "Running",
		Details: map[string]interface{}{
			"node_count": len(nodes),
		},
	}

	// Send initial edges
	edgeList := make([]Edge, len(cfg.Edges))
	for i, e := range cfg.Edges {
		edgeList[i] = Edge{Raw: e}
	}
	daemonEvents <- DaemonEvent{
		Type: "config_update",
		Details: map[string]interface{}{
			"edges": edgeList,
		},
	}

	// Send initial inbox messages (worker node)
	if nodeName := os.Getenv("A2A_NODE"); nodeName != "" {
		msgList := scanInboxMessages(filepath.Join(inboxDir, nodeName))
		daemonEvents <- DaemonEvent{
			Type: "inbox_update",
			Details: map[string]interface{}{
				"messages": msgList,
			},
		}
	}

	// Start TUI
	p := tea.NewProgram(InitialModel(daemonEvents))
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}

// runDaemonLoop runs the daemon event loop in a goroutine.
func runDaemonLoop(
	ctx context.Context,
	sessionDir string,
	contextID string,
	cfg *Config,
	watcher *fsnotify.Watcher,
	adjacency map[string][]string,
	nodes map[string]string,
	knownNodes map[string]bool,
	digestedFiles map[string]bool,
	reminderState *ReminderState,
	events chan<- DaemonEvent,
	configPath string,
) {
	defer close(events)

	// Debounce timers for different paths
	var inboxTimer *time.Timer
	var configTimer *time.Timer
	postDir := filepath.Join(sessionDir, "post")
	inboxDir := filepath.Join(sessionDir, "inbox")

	for {
		select {
		case <-ctx.Done():
			events <- DaemonEvent{
				Type:    "status_update",
				Message: "Shutting down",
			}
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			// Path-based routing
			eventPath := event.Name

			// Handle post/ directory events
			if strings.HasPrefix(eventPath, postDir) {
				if event.Op&(fsnotify.Create|fsnotify.Rename) != 0 {
					filename := filepath.Base(eventPath)
					if strings.HasSuffix(filename, ".md") {
						// Re-discover nodes before each delivery
						if freshNodes, err := DiscoverNodes(); err == nil {
							// Detect new nodes and send PING
							for nodeName := range freshNodes {
								if !knownNodes[nodeName] {
									knownNodes[nodeName] = true
									// Send PING after delay
									if cfg.NewNodePingDelay > 0 {
										newNodeDelay := time.Duration(cfg.NewNodePingDelay * float64(time.Second))
										capturedNode := nodeName
										time.AfterFunc(newNodeDelay, func() {
											if err := sendPingToNode(sessionDir, contextID, capturedNode, cfg.PingTemplate, cfg); err != nil {
												events <- DaemonEvent{
													Type:    "error",
													Message: fmt.Sprintf("PING to new node %s failed: %v", capturedNode, err),
												}
											}
										})
									}
								}
							}
							nodes = freshNodes
							// Update node count
							events <- DaemonEvent{
								Type:    "status_update",
								Message: "Running",
								Details: map[string]interface{}{
									"node_count": len(nodes),
								},
							}
						}
						if err := deliverMessage(sessionDir, filename, nodes, adjacency); err != nil {
							events <- DaemonEvent{
								Type:    "error",
								Message: fmt.Sprintf("deliver %s: %v", filename, err),
							}
						} else {
							// Send message received event
							events <- DaemonEvent{
								Type:    "message_received",
								Message: fmt.Sprintf("Delivered: %s", filename),
							}
							// Send observer digest on successful delivery
							if info, err := ParseMessageFilename(filename); err == nil {
								sendObserverDigest(filename, info.From, nodes, cfg, digestedFiles)
								// Increment reminder counter for recipient
								if info.To != "postman" {
									reminderState.Increment(info.To, nodes, cfg)
								}
							}
						}
					}
				}
			}

			// Handle inbox/ directory events (with debounce)
			if strings.HasPrefix(eventPath, inboxDir) {
				// Debounce inbox updates (200ms)
				if inboxTimer != nil {
					inboxTimer.Stop()
				}
				inboxTimer = time.AfterFunc(200*time.Millisecond, func() {
					// Scan inbox for current node
					if nodeName := os.Getenv("A2A_NODE"); nodeName != "" {
						msgList := scanInboxMessages(filepath.Join(inboxDir, nodeName))
						events <- DaemonEvent{
							Type: "inbox_update",
							Details: map[string]interface{}{
								"messages": msgList,
							},
						}
					}
				})
			}

			// Handle config file events (with debounce)
			if configPath != "" && eventPath == configPath {
				if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
					// Debounce config updates (200ms)
					if configTimer != nil {
						configTimer.Stop()
					}
					configTimer = time.AfterFunc(200*time.Millisecond, func() {
						// Reload config
						newCfg, err := LoadConfig(configPath)
						if err != nil {
							events <- DaemonEvent{
								Type:    "error",
								Message: fmt.Sprintf("config reload failed: %v", err),
							}
							return
						}
						// Update adjacency
						newAdjacency, err := ParseEdges(newCfg.Edges)
						if err != nil {
							events <- DaemonEvent{
								Type:    "error",
								Message: fmt.Sprintf("edge parsing failed: %v", err),
							}
							return
						}
						// Update shared state
						cfg = newCfg
						adjacency = newAdjacency
						// Send config update event
						edgeList := make([]Edge, len(newCfg.Edges))
						for i, e := range newCfg.Edges {
							edgeList[i] = Edge{Raw: e}
						}
						events <- DaemonEvent{
							Type: "config_update",
							Details: map[string]interface{}{
								"edges": edgeList,
							},
						}
						events <- DaemonEvent{
							Type:    "message_received",
							Message: "Config reloaded",
						}
					})
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			events <- DaemonEvent{
				Type:    "error",
				Message: fmt.Sprintf("watcher error: %v", err),
			}
		}
	}
}

func runCreateDraft(args []string) error {
	fs := flag.NewFlagSet("create-draft", flag.ContinueOnError)
	to := fs.String("to", "", "recipient node name (required)")
	contextID := fs.String("context-id", "", "session context ID (required)")
	from := fs.String("from", "", "sender node name (defaults to $A2A_NODE)")
	configPath := fs.String("config", "", "path to config file (optional)")
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

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	baseDir := resolveBaseDir(cfg.BaseDir)
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

// buildPingMessage constructs a PING message using the template.
func buildPingMessage(template string, vars map[string]string, timeout time.Duration) string {
	return ExpandTemplate(template, vars, timeout)
}

// sendPingToNode sends a PING message to a specific node.
func sendPingToNode(sessionDir, contextID, nodeName, template string, cfg *Config) error {
	vars := map[string]string{
		"context_id": contextID,
		"node":       nodeName,
	}
	timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
	content := buildPingMessage(template, vars, timeout)

	now := time.Now()
	ts := now.Format("20060102-150405")
	filename := fmt.Sprintf("%s-from-postman-to-%s.md", ts, nodeName)
	postPath := filepath.Join(sessionDir, "post", filename)

	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing PING message: %w", err)
	}

	fmt.Printf("postman: PING sent to %s\n", nodeName)
	return nil
}

// sendPingToAll sends PING messages to all discovered nodes.
func sendPingToAll(sessionDir, contextID string, cfg *Config) {
	nodes, err := DiscoverNodes()
	if err != nil {
		fmt.Fprintf(os.Stderr, "postman: PING: node discovery failed: %v\n", err)
		return
	}

	for nodeName := range nodes {
		if err := sendPingToNode(sessionDir, contextID, nodeName, cfg.PingTemplate, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "postman: PING to %s failed: %v\n", nodeName, err)
		}
	}
}

// sendObserverDigest sends digest notification to observers with subscribe_digest=true.
// Loop prevention: skip if sender starts with "observer".
// Duplicate prevention: track digested files in digestedFiles map.
func sendObserverDigest(filename string, sender string, nodes map[string]string, cfg *Config, digestedFiles map[string]bool) {
	// Loop prevention: skip observer messages
	if strings.HasPrefix(sender, "observer") {
		return
	}

	// Duplicate prevention: skip if already digested
	if digestedFiles[filename] {
		return
	}
	digestedFiles[filename] = true

	// Find nodes with subscribe_digest=true
	for nodeName, nodeConfig := range cfg.Nodes {
		if !nodeConfig.SubscribeDigest {
			continue
		}
		paneID, found := nodes[nodeName]
		if !found {
			continue
		}

		// Build digest message
		vars := map[string]string{
			"sender":   sender,
			"filename": filename,
		}
		timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
		content := ExpandTemplate(cfg.DigestTemplate, vars, timeout)

		// Send directly to pane via tmux send-keys
		if err := exec.Command("tmux", "send-keys", "-t", paneID, content, "Enter").Run(); err != nil {
			fmt.Fprintf(os.Stderr, "postman: digest to %s failed: %v\n", nodeName, err)
		} else {
			fmt.Printf("postman: digest sent to %s (message from %s)\n", nodeName, sender)
		}
	}
}

// scanInboxMessages scans the inbox directory and returns a list of MessageInfo.
func scanInboxMessages(inboxPath string) []MessageInfo {
	var messages []MessageInfo

	entries, err := os.ReadDir(inboxPath)
	if err != nil {
		return messages
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		info, err := ParseMessageFilename(entry.Name())
		if err != nil {
			continue
		}
		messages = append(messages, *info)
	}

	return messages
}
