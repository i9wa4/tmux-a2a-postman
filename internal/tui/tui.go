package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/diplomat"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/version"
)

// Issue #101: Event severity constants (observer review feedback - MINOR)
const (
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
	SeverityDropped  = "dropped"
)

// Cached style objects (Issue #35)
var (
	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63"))

	warningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("208")).
			Bold(true)

	// Issue #42: Edge arrow styles
	grayArrowStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	greenArrowStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("10"))

	// Issue #55: Node state styles
	activeNodeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")) // green

	ballHolderStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("226")) // yellow

	// Issue #56: Dropped ball style
	droppedNodeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("196")) // red

	composingNodeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("33")) // Blue: node actively composing a reply

	// Issue #89: Selected session row highlight
	selectedSessionStyle = lipgloss.NewStyle().
				Reverse(true)

	// Issue #101: Event severity styles
	eventWarningStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("226")) // yellow

	eventCriticalStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("196")) // red

	eventDroppedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("240")) // gray
)

// waitingStateRank defines priority for waiting/ state color override.
// Higher rank = worse state = takes visual priority.
// spinning is rank 3 (active-failure, more urgent than idle/user_input).
var waitingStateRank = map[string]int{
	"active":     0,
	"user_input": 0,
	"composing":  1,
	"idle":       2,
	"spinning":   3,
	"stale":      3,
	"stuck":      4,
}

// ParseEdgeNodes parses an edge string into a list of node names (Issue #74).
// Supports both directed ("-->") and undirected ("--") edges.
// Also supports chain edges with multiple nodes (Issue #42).
func ParseEdgeNodes(edgeString string) []string {
	var nodes []string
	if strings.Contains(edgeString, "-->") {
		parts := strings.Split(edgeString, "-->")
		for _, p := range parts {
			nodes = append(nodes, strings.TrimSpace(p))
		}
	} else if strings.Contains(edgeString, "--") {
		parts := strings.Split(edgeString, "--")
		for _, p := range parts {
			nodes = append(nodes, strings.TrimSpace(p))
		}
	}
	return nodes
}

const (
	minWidth  = 40
	minHeight = 10
)

// ViewType represents the current TUI view (right pane tabs).
// Issue #45: Removed ViewMessages and ViewSessions
type ViewType int

const (
	ViewEvents ViewType = iota
	ViewRouting
	ViewDiplomat // Issue #164: Cross-context diplomat status tab
)

// Edge represents a routing edge definition.
type Edge struct {
	Raw               string    // Raw edge string (e.g., "A -- B -- C")
	LastActivityAt    time.Time // Issue #35: Requirement 5 - last message time
	IsActive          bool      // Issue #35: Requirement 5 - was recently used
	Direction         string    // Issue #37: Communication direction ("none", "forward", "backward", "bidirectional")
	SegmentDirections []string  // Issue #42: Direction for each segment in chain edges
}

// SessionInfo holds information about a tmux session.
// Issue #35: Requirement 3 - multiple session display
type SessionInfo struct {
	Name      string
	NodeCount int
	Enabled   bool // Issue #35: Requirement 4 - enable/disable toggle
}

// EventEntry holds event information with session context (Issue #59).
// Issue #101: Added Severity field for color-coded display.
type EventEntry struct {
	Message     string
	SessionName string
	Timestamp   time.Time
	Severity    string // Issue #101: "warning", "critical", "dropped", or "" (default)
}

// DaemonEvent represents an event from the daemon goroutine.
type DaemonEvent struct {
	Type    string // "message_received", "status_update", "error", "config_update", "edge_update"
	Message string
	Details map[string]interface{}
}

// DaemonEventMsg wraps DaemonEvent for tea.Msg interface.
type DaemonEventMsg DaemonEvent

// TUICommand represents a command from TUI to the daemon.
// Issue #47: Added for manual PING functionality.
type TUICommand struct {
	Type   string // "send_ping", "create_draft", etc.
	Target string // Session name for PING target
	Value  string // Extra data (e.g., node name for create_draft)
}

// Model holds the TUI state.
// Issue #45: Removed messageList and selectedMsg fields
type Model struct {
	// View state (right pane tab selection)
	currentView ViewType

	// Terminal size (Issue #35)
	width  int
	height int

	// Routing view
	edges        []Edge
	selectedEdge int

	// Session list view (Issue #35: Requirement 3, Issue #45: left pane)
	sessions        []SessionInfo
	selectedSession int
	sessionNodes    map[string][]string // Issue #59: session name -> simple node names

	// Node state tracking (Issue #55)
	nodeStates    map[string]string // "active" / "idle" / "stale"
	waitingStates map[string]string // "composing" / "spinning" / "stuck" / "user_input"
	readCounts    map[string]int    // cumulative read/ moves per node (Issue #246)

	// Shared state
	daemonEvents <-chan DaemonEvent
	tuiCommands  chan<- TUICommand // Issue #47: Command channel to daemon
	events       []EventEntry      // Issue #59: Session-tagged events (was messages []string)
	status       string
	nodeCount    int
	lastEvent    string
	lastKey      string
	quitting     bool
	layoutMode   bool // Issue #127: false = horizontal (default), true = vertical stacking

	// Issue #249: Startup guard toggle (initially disabled at code level, not config).
	// Press 'g' to enable. Warns if a duplicate daemon is detected for the current session.
	startupGuardEnabled bool

	// Config reference (for node state thresholds)
	config *config.Config

	// Diplomat state (Issue #164)
	diplomatEnabled bool
	activeContexts  []diplomat.ContextRegistration
	ownContextID    string
}

// Quitting returns true if the TUI is in quitting state (Issue #57).
func (m Model) Quitting() bool {
	return m.quitting
}

// getSelectedSessionName returns the selected session name.
func (m Model) getSelectedSessionName() string {
	if m.selectedSession < 0 || m.selectedSession >= len(m.sessions) {
		return ""
	}
	return m.sessions[m.selectedSession].Name
}

// getSelectedBorderColor returns border color based on selection (Issue #59).
func (m Model) getSelectedBorderColor() string {
	if m.getSelectedSessionName() == "" {
		return "63" // default (gray)
	}
	return "10" // highlight color (green, matches session selection)
}

// getSessionWorstState returns the worst node state for a session (Issue #97).
// Priority: stuck/stale > spinning/idle > composing > active
func (m Model) getSessionWorstState(sessionName string) string {
	nodes, ok := m.sessionNodes[sessionName]
	if !ok {
		return "active"
	}
	worstState := "active"
	worstRank := 0
	for _, nodeName := range nodes {
		es := m.effectiveNodeState(sessionName + ":" + nodeName)
		if r := waitingStateRank[es]; r > worstRank {
			worstRank = r
			worstState = es
		}
	}
	// Normalize waiting-only states to renderable session states
	switch worstState {
	case "stuck":
		return "stale"
	case "spinning":
		return "idle"
	case "user_input":
		return "active"
	default:
		return worstState // "active", "composing", "idle", "stale" pass through
	}
}

// effectiveNodeState returns the display state for a session-prefixed node key.
// waiting/ state overrides nodeStates when it represents an equal or worse condition.
func (m Model) effectiveNodeState(key string) string {
	state := ""
	if ns, ok := m.nodeStates[key]; ok {
		state = ns
	}
	if ws, ok := m.waitingStates[key]; ok {
		if waitingStateRank[ws] >= waitingStateRank[state] {
			state = ws
		}
	}
	return state
}

// updateNodeStatesFromActivity updates node states from idle.NodeActivity map (Issue #55).
// Issue #56: Added droppedNodes parameter for dropped-ball detection.
// Issue #77: Use session-prefixed keys to avoid collision across sessions.
// Issue #79: Simplified - nodeActivity keys are already session-prefixed, no reverse index needed.
func (m *Model) updateNodeStatesFromActivity(nodeStatesRaw interface{}, droppedNodes map[string]bool) {
	// Type assertion: nodeStatesRaw should be map[string]idle.NodeActivity
	nodeActivities, ok := nodeStatesRaw.(map[string]idle.NodeActivity)
	if !ok {
		return
	}

	// Build edge filter set (simple names)
	edgeNodes := make(map[string]bool)
	for _, edge := range m.edges {
		nodes := ParseEdgeNodes(edge.Raw)
		for _, node := range nodes {
			edgeNodes[node] = true
		}
	}

	for nodeKey, activity := range nodeActivities {
		// Extract simple name for edge filter
		simpleName := nodeKey
		if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
			simpleName = parts[1]
		}
		if !edgeNodes[simpleName] {
			continue
		}

		// Determine state
		var state string
		switch {
		case droppedNodes != nil && droppedNodes[nodeKey]:
			state = "stale"
		case !activity.LivenessConfirmed:
			state = "stale"
		case activity.LastReceived.After(activity.LastSent) && !activity.LastReceived.IsZero():
			// BLOCKING FIX: Preserve existing ball possession logic
			// LastReceived > LastSent = recipient has received but not replied
			state = "idle"
		default:
			// Issue #95: Time-based color changes (only for active nodes)
			// Calculate idle duration from max(LastSent, LastReceived)
			now := time.Now()
			lastActivity := activity.LastSent
			if activity.LastReceived.After(lastActivity) {
				lastActivity = activity.LastReceived
			}

			idleDuration := time.Duration(0)
			if !lastActivity.IsZero() {
				idleDuration = now.Sub(lastActivity)
			}

			// Time-based state determination (3-stage transition)
			// Default: 0-5min active, 5-15min idle, 15min+ stale
			activeThreshold := time.Duration(m.config.NodeActiveSeconds * float64(time.Second))
			idleThreshold := time.Duration(m.config.NodeIdleSeconds * float64(time.Second))

			switch {
			case idleDuration >= idleThreshold:
				state = "stale"
			case idleDuration >= activeThreshold:
				state = "idle"
			default:
				state = "active"
			}
		}

		// Direct assignment with session-prefixed key
		m.nodeStates[nodeKey] = state
	}
}

// InitialModel creates the initial TUI model.
// Issue #45: Removed messageList and selectedMsg initialization
// Issue #47: Added tuiCommands channel parameter
func InitialModel(daemonEvents <-chan DaemonEvent, tuiCommands chan<- TUICommand, cfg *config.Config, ownContextID string) Model {
	return Model{
		currentView:         ViewEvents,
		width:               80, // Default width (Issue #35)
		height:              24, // Default height (Issue #35)
		edges:               []Edge{},
		selectedEdge:        0,
		sessions:            []SessionInfo{},           // Issue #35: Requirement 3
		selectedSession:     0,                         // Issue #35: Requirement 3
		sessionNodes:        make(map[string][]string), // Issue #59: Session-node mapping
		nodeStates:          make(map[string]string),   // Issue #55: Node state tracking
		waitingStates:       make(map[string]string),
		readCounts:          make(map[string]int), // Issue #246: Cumulative read counts
		config:              cfg,
		daemonEvents:        daemonEvents,
		tuiCommands:         tuiCommands,    // Issue #47: Command channel
		events:              []EventEntry{}, // Issue #59: Session-tagged events
		status:              "Starting...",
		nodeCount:           0,
		lastEvent:           "",
		quitting:            false,
		layoutMode:          false, // Issue #127: default horizontal layout
		startupGuardEnabled: false, // Issue #249: hard-disabled at code level; user enables with 'g'
		// Issue #164: Diplomat state
		diplomatEnabled: cfg.GetDiplomatEnabled(),
		activeContexts:  nil,
		ownContextID:    ownContextID,
	}
}

// Init initializes the TUI and subscribes to daemon events.
func (m Model) Init() tea.Cmd {
	return waitForDaemonEvent(m.daemonEvents)
}

// waitForDaemonEvent waits for the next daemon event from the channel.
func waitForDaemonEvent(ch <-chan DaemonEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			// Channel closed
			return DaemonEventMsg{Type: "channel_closed"}
		}
		return DaemonEventMsg(event)
	}
}

// Update handles messages and updates the model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Handle terminal resize (Issue #35)
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyPressMsg:
		// Issue #45: Redesigned key bindings
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit

		// Right pane tab switching (Issue #45, Issue #164)
		case "tab":
			switch m.currentView {
			case ViewEvents:
				m.currentView = ViewRouting
			case ViewRouting:
				m.currentView = ViewDiplomat
			default:
				m.currentView = ViewEvents
			}
			return m, nil
		case "1":
			m.currentView = ViewEvents
			return m, nil
		case "2":
			m.currentView = ViewRouting
			return m, nil
		case "3":
			m.currentView = ViewDiplomat
			return m, nil

		// Left pane (Sessions) navigation (Issue #45)
		case "j", "down":
			if m.selectedSession < len(m.sessions)-1 {
				m.selectedSession++
				// Clear edge history when switching sessions
				if m.tuiCommands != nil {
					m.tuiCommands <- TUICommand{Type: "clear_edge_history"}
				}
			}
			m.lastKey = ""
			return m, nil
		case "k", "up":
			if m.selectedSession > 0 {
				m.selectedSession--
				// Clear edge history when switching sessions
				if m.tuiCommands != nil {
					m.tuiCommands <- TUICommand{Type: "clear_edge_history"}
				}
			}
			m.lastKey = ""
			return m, nil
		case "g":
			// Issue #249: Toggle startup guard (initially disabled at code level).
			// 'g' key for startup guard toggle.
			m.startupGuardEnabled = !m.startupGuardEnabled
			m.lastKey = ""
			return m, nil
		case "G":
			if len(m.sessions) > 0 {
				m.selectedSession = len(m.sessions) - 1
				if m.tuiCommands != nil {
					m.tuiCommands <- TUICommand{Type: "clear_edge_history"}
				}
			}
			m.lastKey = ""
			return m, nil
		case "space":
			// Session toggle via TUICommand
			if m.selectedSession >= 0 && m.selectedSession < len(m.sessions) {
				sess := m.sessions[m.selectedSession]
				// Issue #249: TUI-level guard — block toggle-ON if another daemon owns this session.
				// Only active when m.startupGuardEnabled (press 'g' to enable guard; default: off).
				// Early return here skips both the TUICommand send AND the optimistic update at line 482.
				// TODO: make async via tea.Cmd for network filesystem support
				if !sess.Enabled && m.startupGuardEnabled && m.config != nil && m.ownContextID != "" {
					baseDir := config.ResolveBaseDir(m.config.BaseDir)
					if liveCtx := config.FindSessionOwner(baseDir, sess.Name, m.ownContextID); liveCtx != "" {
						m.status = fmt.Sprintf("session %q already active in %s (guard=ON blocks enable)", sess.Name, liveCtx)
						m.lastKey = ""
						return m, nil
					}
				}
				if m.tuiCommands != nil {
					m.tuiCommands <- TUICommand{
						Type:   "session_toggle",
						Target: sess.Name,
					}
				}
				// Optimistic immediate update to avoid race with file watcher status_update
				m.sessions[m.selectedSession].Enabled = !sess.Enabled
			}
			m.lastKey = ""
			return m, nil
		case "p":
			// Issue #47: Send PING to selected session
			if m.selectedSession >= 0 && m.selectedSession < len(m.sessions) {
				sess := m.sessions[m.selectedSession]
				if !sess.Enabled {
					m.status = "Session is disabled"
					return m, nil
				}
				m.status = "Sending ping..."
				if m.tuiCommands != nil {
					m.tuiCommands <- TUICommand{
						Type:   "send_ping",
						Target: sess.Name,
					}
				} else {
					m.status = "Ping: daemon unavailable"
				}
			}
			return m, nil
		case "l":
			// Issue #127: Toggle layout mode
			m.layoutMode = !m.layoutMode
			return m, nil
		case "d":
			// Issue #230: Create draft shortcut
			if m.selectedSession >= 0 && m.selectedSession < len(m.sessions) {
				sess := m.sessions[m.selectedSession]
				nodes := m.sessionNodes[sess.Name]
				if len(nodes) > 0 && m.tuiCommands != nil {
					m.tuiCommands <- TUICommand{
						Type:   "create_draft",
						Target: sess.Name,
						Value:  nodes[0],
					}
				}
			}
			return m, nil
		}

	case DaemonEventMsg:
		// Handle daemon event
		switch msg.Type {
		case "message_received":
			m.lastEvent = msg.Message
			// Issue #59: Extract session from Details
			sessionName := ""
			if session, ok := msg.Details["session"].(string); ok {
				sessionName = session
			}
			m.events = append(m.events, EventEntry{
				Message:     msg.Message,
				SessionName: sessionName,
				Timestamp:   time.Now(),
				Severity:    "", // Issue #101: Default severity
			})
			// Keep only last 10 events
			if len(m.events) > 10 {
				m.events = m.events[len(m.events)-10:]
			}
		case "dropped_ball":
			// Issue #56: Dropped ball event
			m.lastEvent = msg.Message
			// Extract session from node name (Details["node"])
			sessionName := ""
			if node, ok := msg.Details["node"].(string); ok {
				// NOTE: node is simple name, need to find session from sessionNodes
				for session, nodes := range m.sessionNodes {
					for _, n := range nodes {
						if n == node {
							sessionName = session
							break
						}
					}
					if sessionName != "" {
						break
					}
				}
			}
			m.events = append(m.events, EventEntry{
				Message:     msg.Message,
				SessionName: sessionName,
				Timestamp:   time.Now(),
				Severity:    "", // Issue #101: Default severity
			})
			// Keep only last 10 events
			if len(m.events) > 10 {
				m.events = m.events[len(m.events)-10:]
			}
		case "status_update":
			m.status = msg.Message
			if count, ok := msg.Details["node_count"].(int); ok {
				m.nodeCount = count
			}
			// Issue #36: Bug 2 - update sessions from Details
			if sessionList, ok := msg.Details["sessions"].([]SessionInfo); ok {
				m.sessions = sessionList
				// Clamp selection
				if m.selectedSession >= len(m.sessions) {
					m.selectedSession = len(m.sessions) - 1
				}
				if m.selectedSession < 0 {
					m.selectedSession = 0
				}
			}
			// Issue #59: Update session-node mapping
			if sessionNodesRaw, ok := msg.Details["session_nodes"].(map[string][]string); ok {
				m.sessionNodes = sessionNodesRaw
			}
		// Issue #45: Removed "inbox_update" handler
		case "config_update":
			// Update edges from Details
			if edgeList, ok := msg.Details["edges"].([]Edge); ok {
				m.edges = edgeList
				// Clamp selection
				if m.selectedEdge >= len(m.edges) {
					m.selectedEdge = len(m.edges) - 1
				}
				if m.selectedEdge < 0 {
					m.selectedEdge = 0
				}
			}
			// Issue #35: Requirement 3 - update sessions from Details
			if sessionList, ok := msg.Details["sessions"].([]SessionInfo); ok {
				m.sessions = sessionList
				// Clamp selection
				if m.selectedSession >= len(m.sessions) {
					m.selectedSession = len(m.sessions) - 1
				}
				if m.selectedSession < 0 {
					m.selectedSession = 0
				}
			}
			// Issue #59: Update session-node mapping
			if sessionNodesRaw, ok := msg.Details["session_nodes"].(map[string][]string); ok {
				m.sessionNodes = sessionNodesRaw
			}
		case "edge_update":
			// Issue #40: Update edges from edge_update event
			if edgeList, ok := msg.Details["edges"].([]Edge); ok {
				m.edges = edgeList
				// Clamp selection
				if m.selectedEdge >= len(m.edges) {
					m.selectedEdge = len(m.edges) - 1
				}
				if m.selectedEdge < 0 {
					m.selectedEdge = 0
				}
			}
		case "node_alive":
			// Issue #55: Mark node as active when liveness confirmed
			// Issue #79: Simplified - node key is already session-prefixed
			if node, ok := msg.Details["node"].(string); ok {
				m.nodeStates[node] = "active"
			}
		case "ball_state_update":
			// Issue #55: Update node states from idle tracking
			// Issue #56: Extract dropped_nodes for dropped-ball detection
			var droppedNodes map[string]bool
			if droppedNodesRaw, ok := msg.Details["dropped_nodes"].(map[string]bool); ok {
				droppedNodes = droppedNodesRaw
			}
			if nodeStatesRaw, ok := msg.Details["node_states"]; ok {
				// Type assertion is tricky with maps - need to handle interface{} carefully
				// The map comes from idle.GetNodeStates() which returns map[string]NodeActivity
				// We need to extract state from each NodeActivity
				m.updateNodeStatesFromActivity(nodeStatesRaw, droppedNodes)
			}
		case "waiting_state_update":
			if wsRaw, ok := msg.Details["waiting_states"].(map[string]string); ok {
				m.waitingStates = wsRaw
			}
		case "read_count_update":
			// Issue #246: Update per-node cumulative read counts for edge display.
			if counts, ok := msg.Details["counts"].(map[string]int); ok {
				m.readCounts = counts
			}
		case diplomat.CrossContextDelivered, "diplomat_stale_removed":
			// Issue #164: Refresh active context registrations on diplomat events.
			m.activeContexts, _ = diplomat.LoadActiveContexts(config.ResolveBaseDir(m.config.BaseDir))
		case "error":
			m.events = append(m.events, EventEntry{
				Message:     fmt.Sprintf("ERROR: %s", msg.Message),
				SessionName: "", // Error events have no specific session
				Timestamp:   time.Now(),
				Severity:    SeverityCritical, // Issue #101: Errors are critical
			})
			if len(m.events) > 10 {
				m.events = m.events[len(m.events)-10:]
			}
		// Issue #101: Handle abnormal event types
		case "inbox_stagnation_warning":
			m.events = append(m.events, EventEntry{
				Message:     msg.Message,
				SessionName: extractSessionFromDetails(msg.Details),
				Timestamp:   time.Now(),
				Severity:    SeverityWarning,
			})
			if len(m.events) > 10 {
				m.events = m.events[len(m.events)-10:]
			}
		case "inbox_stagnation_critical":
			m.events = append(m.events, EventEntry{
				Message:     msg.Message,
				SessionName: extractSessionFromDetails(msg.Details),
				Timestamp:   time.Now(),
				Severity:    SeverityCritical,
			})
			if len(m.events) > 10 {
				m.events = m.events[len(m.events)-10:]
			}
		case "node_inactivity_warning":
			m.events = append(m.events, EventEntry{
				Message:     msg.Message,
				SessionName: extractSessionFromDetails(msg.Details),
				Timestamp:   time.Now(),
				Severity:    SeverityWarning,
			})
			if len(m.events) > 10 {
				m.events = m.events[len(m.events)-10:]
			}
		case "node_inactivity_critical":
			m.events = append(m.events, EventEntry{
				Message:     msg.Message,
				SessionName: extractSessionFromDetails(msg.Details),
				Timestamp:   time.Now(),
				Severity:    SeverityCritical,
			})
			if len(m.events) > 10 {
				m.events = m.events[len(m.events)-10:]
			}
		case "node_inactivity_dropped":
			m.events = append(m.events, EventEntry{
				Message:     msg.Message,
				SessionName: extractSessionFromDetails(msg.Details),
				Timestamp:   time.Now(),
				Severity:    SeverityDropped,
			})
			if len(m.events) > 10 {
				m.events = m.events[len(m.events)-10:]
			}
		case "unreplied_message":
			m.events = append(m.events, EventEntry{
				Message:     msg.Message,
				SessionName: extractSessionFromDetails(msg.Details),
				Timestamp:   time.Now(),
				Severity:    SeverityWarning,
			})
			if len(m.events) > 10 {
				m.events = m.events[len(m.events)-10:]
			}
		case "swallowed_redelivery": // Issue #282
			m.events = append(m.events, EventEntry{
				Message:     msg.Message,
				SessionName: extractSessionFromDetails(msg.Details),
				Timestamp:   time.Now(),
				Severity:    SeverityWarning,
			})
			if len(m.events) > 10 {
				m.events = m.events[len(m.events)-10:]
			}
		case "pane_disappeared":
			// Mark node as inactive when pane disappears (killed)
			if node, ok := msg.Details["node"].(string); ok {
				m.nodeStates[node] = "stale" // Use "stale" for disappeared panes
			}
			// Add event entry
			m.events = append(m.events, EventEntry{
				Message:     msg.Message,
				SessionName: extractSessionFromDetails(msg.Details),
				Timestamp:   time.Now(),
				Severity:    SeverityDropped,
			})
			if len(m.events) > 10 {
				m.events = m.events[len(m.events)-10:]
			}
		case "channel_closed":
			m.quitting = true
			return m, tea.Quit
		}
		// Wait for next event
		return m, waitForDaemonEvent(m.daemonEvents)
	}

	return m, nil
}

// extractSessionFromDetails extracts session name from event Details (Issue #101).
// Tries multiple common keys: "session", "node", and extracts session prefix from "node" if needed.
func extractSessionFromDetails(details map[string]interface{}) string {
	// IMPORTANT: Guard against nil Details (observer review feedback)
	if details == nil {
		return ""
	}
	// Try "session" key first
	if session, ok := details["session"].(string); ok {
		return session
	}
	// Try "node" key and extract session prefix (format: "session:node")
	if node, ok := details["node"].(string); ok {
		if parts := strings.SplitN(node, ":", 2); len(parts) == 2 {
			return parts[0]
		}
	}
	return ""
}

// View renders the TUI with left-right split layout (Issue #45).
func (m Model) View() tea.View {
	if m.quitting {
		return tea.View{Content: "Shutting down...\n"}
	}

	// Minimum size check (Issue #35)
	if m.width < minWidth || m.height < minHeight {
		warning := warningStyle.Render(fmt.Sprintf("⚠️  Terminal too small (min: %dx%d, current: %dx%d)", minWidth, minHeight, m.width, m.height))
		return tea.View{Content: borderStyle.Width(m.width - 2).Render(warning)}
	}

	// Issue #45: Calculate pane widths for split layout
	totalWidth := m.width - 4 // Account for border + padding
	leftPaneWidth := 25       // Fixed width for sessions list
	rightPaneWidth := totalWidth - leftPaneWidth - 1
	contentHeight := m.height - 5 // Account for border + padding + header line

	// Header line: application title + version (Issue #149)
	headerLine := lipgloss.NewStyle().Width(m.width - 4).Render("tmux-a2a-postman " + version.Version)

	// Issue #127: Branch on layout mode
	var mainContent string
	if m.layoutMode {
		mainContent = m.renderVerticalLayout(m.width-4, contentHeight)
	} else {
		// Render left and right panes
		leftPane := m.renderLeftPane(leftPaneWidth, contentHeight)
		rightPane := m.renderRightPane(rightPaneWidth, contentHeight)

		// Create vertical separator with exact height
		// NOTE: lipgloss.JoinHorizontal requires all inputs to have the same line count.
		// Use lipgloss.Place to ensure separator matches contentHeight exactly.
		separator := lipgloss.Place(
			1,             // width: 1 character
			contentHeight, // height: match content
			lipgloss.Left, // horizontal alignment
			lipgloss.Top,  // vertical alignment
			strings.Repeat("│\n", contentHeight-1)+"│", // contentHeight lines without trailing newline
		)

		// Ensure leftPane and rightPane are exact height using lipgloss.PlaceVertical
		leftPaneStyled := lipgloss.NewStyle().
			Width(leftPaneWidth).
			Height(contentHeight).
			Render(leftPane)
		rightPaneStyled := lipgloss.NewStyle().
			Width(rightPaneWidth).
			Height(contentHeight).
			Render(rightPane)

		// Horizontal split using lipgloss with separator
		mainContent = lipgloss.JoinHorizontal(lipgloss.Top, leftPaneStyled, separator, rightPaneStyled)
	}

	content := lipgloss.JoinVertical(lipgloss.Top, headerLine, mainContent)

	// Apply border (Issue #35)
	// Issue #59: Dynamic border color based on selection
	localBorderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.getSelectedBorderColor()))
	return tea.View{Content: localBorderStyle.Width(m.width - 2).Height(m.height - 2).Render(content)}
}

// renderLeftPane renders the left pane (Sessions list).
// Issue #45: New function for left-right split layout
// Issue #64: Simplified with emoji status indicators
func (m Model) renderLeftPane(width, height int) string {
	var b strings.Builder

	b.WriteString("Sessions\n")

	if len(m.sessions) == 0 {
		b.WriteString("(no sessions)\n")
	} else {
		maxLines := height - 5
		if maxLines < 2 {
			maxLines = 2
		}

		startIdx := 0
		endIdx := len(m.sessions)
		if len(m.sessions) > maxLines {
			if m.selectedSession >= maxLines {
				startIdx = m.selectedSession - maxLines + 1
			}
			endIdx = startIdx + maxLines
			if endIdx > len(m.sessions) {
				endIdx = len(m.sessions)
			}
		}

		for i := startIdx; i < endIdx; i++ {
			sess := m.sessions[i]

			// Issue #64: Cursor prefix
			cursor := "  "
			if i == m.selectedSession {
				cursor = "> "
			}

			// Status emoji
			statusEmoji := "⚫"
			if sess.Enabled {
				statusEmoji = "🟢"
			}

			// Add space after emoji before session name
			prefix := statusEmoji + " "

			line := fmt.Sprintf("%s%s%s", cursor, prefix, sess.Name)

			// Issue #97: Apply session color based on worst node state
			// Priority: stale (red) > idle (orange) > active (green)
			if i != m.selectedSession && sess.Enabled {
				worstState := m.getSessionWorstState(sess.Name)
				var sessionStyle lipgloss.Style
				switch worstState {
				case "stale":
					sessionStyle = droppedNodeStyle
				case "idle":
					sessionStyle = ballHolderStyle
				case "composing":
					sessionStyle = composingNodeStyle
				case "active":
					sessionStyle = activeNodeStyle
				default:
					sessionStyle = lipgloss.NewStyle() // No style
				}
				line = sessionStyle.Render(line)
			}

			// Issue #89: Apply reverse style with fixed width for full-line highlight
			if i == m.selectedSession {
				line = selectedSessionStyle.Width(width - 2).Render(line)
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n")
	guardLabel := "off"
	if m.startupGuardEnabled {
		guardLabel = "ON"
	}
	b.WriteString(fmt.Sprintf("[space: session on/off] [p: ping] [l: layout] [g: guard=%s]\n", guardLabel))
	if m.status != "" {
		b.WriteString(m.status + "\n")
	}

	return b.String()
}

// renderRightPane renders the right pane (Events/Routing tabs).
// Issue #45: New function for left-right split layout
func (m Model) renderRightPane(width, height int) string {
	var b strings.Builder

	// Tab display (* always reserved to avoid layout shift on toggle)
	eventsMarker := " "
	if m.currentView == ViewEvents {
		eventsMarker = "*"
	}
	routingMarker := " "
	if m.currentView == ViewRouting {
		routingMarker = "*"
	}
	diplomatMarker := " "
	if m.currentView == ViewDiplomat {
		diplomatMarker = "*"
	}
	b.WriteString("[1:Events" + eventsMarker + " | 2:Routing" + routingMarker + " | 3:Diplomat" + diplomatMarker + "]\n")

	// Content based on current view
	switch m.currentView {
	case ViewEvents:
		b.WriteString("\n")
		b.WriteString(m.renderEventsView(width, height-7))
	case ViewRouting:
		legend := "Legend: " +
			activeNodeStyle.Render("Active") + " | " +
			composingNodeStyle.Render("Composing") + " | " +
			ballHolderStyle.Render("Idle") + " | " +
			droppedNodeStyle.Render("Stale")
		b.WriteString(legend + "\n\n")
		b.WriteString(m.renderRoutingView(width, height-7))
	case ViewDiplomat:
		b.WriteString("\n")
		b.WriteString(m.renderDiplomatView(width, height-7))
	}

	return b.String()
}

// renderEdgeLine renders a single edge line with colored node names and directional arrows.
// sessionName is the session context for state key lookup ("" means All).
func (m Model) renderEdgeLine(edge Edge, sessionName string) string {
	line := edge.Raw
	if len(edge.SegmentDirections) > 0 {
		nodes := ParseEdgeNodes(line)
		if len(nodes) == len(edge.SegmentDirections)+1 {
			var builder strings.Builder
			for j, node := range nodes {
				nodeStyle := lipgloss.NewStyle()
				var stateKey string
				if sessionName != "" {
					stateKey = sessionName + ":" + node
				} else {
					for sName, nodesInSession := range m.sessionNodes {
						for _, nodeName := range nodesInSession {
							if nodeName == node {
								stateKey = sName + ":" + node
								break
							}
						}
						if stateKey != "" {
							break
						}
					}
					if stateKey == "" {
						stateKey = node
					}
				}
				if es := m.effectiveNodeState(stateKey); es != "" {
					switch es {
					case "active", "user_input":
						nodeStyle = activeNodeStyle
					case "composing":
						nodeStyle = composingNodeStyle
					case "idle", "spinning":
						nodeStyle = ballHolderStyle
					case "stale", "stuck":
						nodeStyle = droppedNodeStyle
					}
				}
				builder.WriteString(nodeStyle.Render(node))
				if cnt := m.readCounts[stateKey]; cnt > 0 {
					builder.WriteString(fmt.Sprintf(" (%d)", cnt))
				}
				if j < len(edge.SegmentDirections) {
					var arrow string
					var arrowStyle lipgloss.Style
					switch edge.SegmentDirections[j] {
					case "forward":
						arrow = " -->  "
						arrowStyle = greenArrowStyle
					case "backward":
						arrow = " <--  "
						arrowStyle = greenArrowStyle
					case "bidirectional":
						arrow = " <--> "
						arrowStyle = greenArrowStyle
					default:
						arrow = "  --  "
						arrowStyle = grayArrowStyle
					}
					builder.WriteString(arrowStyle.Render(arrow))
				}
			}
			line = builder.String()
		}
	}
	return line
}

// renderVerticalLayout renders all sessions stacked vertically.
// Issue #127: Vertical layout mode — one panel per session.
func (m Model) renderVerticalLayout(width, height int) string {
	nSessions := len(m.sessions)
	if nSessions < 1 {
		nSessions = 1
	}
	panelHeight := (height - 1) / nSessions // reserves 1 line for footer

	if panelHeight < 3 {
		return warningStyle.Render("⚠️  Terminal too small for vertical layout (need ≥3 lines per session)")
	}

	contentLines := panelHeight - 2
	var b strings.Builder

	for i, sess := range m.sessions {
		// Header: emoji + name + worst state
		statusEmoji := "⚫"
		if sess.Enabled {
			statusEmoji = "🟢"
		}
		worstState := m.getSessionWorstState(sess.Name)
		b.WriteString(fmt.Sprintf("%s %s [%s]\n", statusEmoji, sess.Name, worstState))

		// Content: per-session events or routing (inline, not via shared helpers)
		switch m.currentView {
		case ViewEvents:
			var filtered []EventEntry
			for _, ev := range m.events {
				if ev.SessionName == sess.Name {
					filtered = append(filtered, ev)
				}
			}
			if len(filtered) == 0 {
				b.WriteString("  (no events)\n")
			} else {
				start := len(filtered) - contentLines
				if start < 0 {
					start = 0
				}
				for _, ev := range filtered[start:] {
					b.WriteString(fmt.Sprintf("  - %s\n", truncateString(ev.Message, width-4)))
				}
			}
		case ViewRouting:
			nodesInSession := m.sessionNodes[sess.Name]
			nodeSet := make(map[string]bool)
			for _, n := range nodesInSession {
				nodeSet[n] = true
			}
			var filtered []Edge
			for _, edge := range m.edges {
				nodes := ParseEdgeNodes(edge.Raw)
				for _, n := range nodes {
					if nodeSet[n] {
						filtered = append(filtered, edge)
						break
					}
				}
			}
			if len(filtered) == 0 {
				b.WriteString("  (no edges)\n")
			} else {
				count := contentLines
				if count > len(filtered) {
					count = len(filtered)
				}
				for _, edge := range filtered[:count] {
					b.WriteString(fmt.Sprintf("  %s\n", m.renderEdgeLine(edge, sess.Name)))
				}
			}
		}

		// Separator between panels (omit after last)
		if i < len(m.sessions)-1 {
			b.WriteString(strings.Repeat("─", width) + "\n")
		}
	}

	footer := "[q: quit] [1/2: view] [l: layout] [d: draft]"
	if m.status != "" {
		footer += "  | " + m.status
	}
	b.WriteString(footer)
	return b.String()
}

// truncateString truncates a string to maxLen runes (UTF-8 safe).
// If truncated, appends "..." to the result.
// Issue #60: Fix long line wrapping in Events pane
func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen < 3 {
		// Too short to add "...", just truncate
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

// renderEventsView renders the events view (right pane content).
// Issue #45: Adjusted for right pane layout
// Issue #60: Fix long line wrapping with UTF-8 safe truncation
func (m Model) renderEventsView(width, height int) string {
	var b strings.Builder
	b.WriteString("Recent Events:\n")

	// Issue #59: Filter events by selected session
	selectedName := m.getSelectedSessionName()
	var filteredEvents []EventEntry
	for _, event := range m.events {
		if selectedName == "" || event.SessionName == selectedName {
			filteredEvents = append(filteredEvents, event)
		}
	}

	if len(filteredEvents) == 0 {
		b.WriteString("  (no events yet)\n")
	} else {
		// Truncate list if too long
		maxLines := height - 2 // Issue #45: Adjusted for right pane
		if maxLines < 1 {
			maxLines = 1
		}
		displayCount := len(filteredEvents)
		if displayCount > maxLines {
			displayCount = maxLines
		}
		startIdx := len(filteredEvents) - displayCount
		for i := startIdx; i < len(filteredEvents); i++ {
			event := filteredEvents[i]
			msg := event.Message
			// Truncate long lines (UTF-8 safe)
			// Reserve 4 characters for "  - " prefix
			maxMsgLen := width - 4
			if maxMsgLen > 0 {
				msg = truncateString(msg, maxMsgLen)
			}
			// Issue #101: Apply severity-based styling
			styledMsg := msg
			switch event.Severity {
			case SeverityWarning:
				styledMsg = eventWarningStyle.Render(msg)
			case SeverityCritical:
				styledMsg = eventCriticalStyle.Render(msg)
			case SeverityDropped:
				styledMsg = eventDroppedStyle.Render(msg)
			}
			b.WriteString(fmt.Sprintf("  - %s\n", styledMsg))
		}
	}
	return b.String()
}

// NOTE (Issue #59 Limitations):
// - Per-session edge activity not implemented
// - idle.go maintains global state; per-session idle filtering not supported
//
// renderRoutingView renders the routing view (right pane content).
// Issue #45: Adjusted for right pane layout, removed selection display
func (m Model) renderRoutingView(width, height int) string {
	var b strings.Builder
	b.WriteString("Routing Edges:\n")

	// Issue #59: Filter edges by selected session (ANY method)
	selectedName := m.getSelectedSessionName()
	var filteredEdges []Edge
	if selectedName == "" {
		// "(All)" selected - show all edges
		filteredEdges = m.edges
	} else {
		// Filter edges: show if ANY node belongs to selected session
		nodesInSession := m.sessionNodes[selectedName]
		nodeSet := make(map[string]bool)
		for _, nodeName := range nodesInSession {
			nodeSet[nodeName] = true
		}

		for _, edge := range m.edges {
			// Parse nodes from edge
			nodes := ParseEdgeNodes(edge.Raw)

			// Check if ANY node is in selected session
			anyMatch := false
			for _, node := range nodes {
				if nodeSet[node] {
					anyMatch = true
					break
				}
			}

			if anyMatch {
				filteredEdges = append(filteredEdges, edge)
			}
		}
	}

	if len(filteredEdges) == 0 {
		b.WriteString("  (no edges defined)\n")
	} else {
		// Truncate list if too long
		maxLines := height - 2 // Issue #45: Adjusted for right pane
		if maxLines < 1 {
			maxLines = 1
		}
		displayCount := len(filteredEdges)
		if displayCount > maxLines {
			displayCount = maxLines
		}
		startIdx := 0
		endIdx := startIdx + displayCount
		if endIdx > len(filteredEdges) {
			endIdx = len(filteredEdges)
		}

		for i := startIdx; i < endIdx; i++ {
			edge := filteredEdges[i]
			line := m.renderEdgeLine(edge, selectedName)

			// Issue #45: Simplified display (no selection indicator in right pane)
			b.WriteString(fmt.Sprintf("  %s\n", line))
		}
	}
	return b.String()
}

// renderDiplomatView renders the diplomat cross-context status tab (Issue #164).
func (m Model) renderDiplomatView(_, _ int) string {
	var b strings.Builder

	enabled := "no"
	if m.diplomatEnabled {
		enabled = "yes"
	}
	b.WriteString(fmt.Sprintf("Diplomat enabled: %s\n", enabled))
	b.WriteString(fmt.Sprintf("Own context ID:   %s\n", m.ownContextID))
	b.WriteString(fmt.Sprintf("Own node:         %s\n", m.config.DiplomatNode))

	b.WriteString("\nRemote contexts:\n")
	if len(m.activeContexts) == 0 {
		b.WriteString("  (none registered)\n")
	} else {
		for _, reg := range m.activeContexts {
			alive := "alive"
			if !diplomat.IsContextAlive(reg) {
				alive = "stale"
			}
			b.WriteString(fmt.Sprintf("  %s  node=%s  [%s]\n", reg.ContextID, reg.DiplomatNode, alive))
		}
	}
	return b.String()
}
