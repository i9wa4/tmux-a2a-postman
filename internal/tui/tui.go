package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
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
			Foreground(lipgloss.Color("208")) // orange

	// Issue #56: Dropped ball style
	droppedNodeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("196")) // red

	// Issue #89: Selected session row highlight
	selectedSessionStyle = lipgloss.NewStyle().
				Reverse(true)

	// Issue #93: Legend styles
	waitingNodeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("240")) // gray
)

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
type EventEntry struct {
	Message     string
	SessionName string
	Timestamp   time.Time
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
	Type   string // "send_ping"
	Target string // Session name for PING target
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
	nodeStates map[string]string // "waiting" / "active" / "holding" / "dropped"

	// Shared state
	daemonEvents <-chan DaemonEvent
	tuiCommands  chan<- TUICommand // Issue #47: Command channel to daemon
	events       []EventEntry      // Issue #59: Session-tagged events (was messages []string)
	status       string
	nodeCount    int
	lastEvent    string
	quitting     bool
}

// Quitting returns true if the TUI is in quitting state (Issue #57).
func (m Model) Quitting() bool {
	return m.quitting
}

// getSelectedSessionName returns the selected session name, or "" for "(All)" (Issue #59).
func (m Model) getSelectedSessionName() string {
	if m.selectedSession == 0 || m.selectedSession >= len(m.sessions) {
		return "" // "" means "All"
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

// sessionHasIdleNodes returns true if any node in the session has "holding" or "dropped" state (Issue #64).
// Issue #77: Use session-prefixed keys (sessionName:nodeName) to avoid collision across sessions.
func (m Model) sessionHasIdleNodes(sessionName string) bool {
	nodes, ok := m.sessionNodes[sessionName]
	if !ok {
		return false
	}
	for _, nodeName := range nodes {
		// Issue #77: Construct session-prefixed key
		prefixedKey := sessionName + ":" + nodeName
		if state, exists := m.nodeStates[prefixedKey]; exists {
			if state == "holding" || state == "dropped" {
				return true
			}
		}
	}
	return false
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
			state = "dropped"
		case !activity.PongReceived:
			state = "waiting"
		case activity.LastReceived.After(activity.LastSent) && !activity.LastReceived.IsZero():
			// BLOCKING FIX: Preserve existing "holding" logic (ball possession)
			// LastReceived > LastSent = recipient has received but not replied
			state = "holding"
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

			// Time-based state determination:
			// 0-5min: active (green)
			// 5-15min: holding (orange)
			// 15-30min: dropped (red)
			// 30min+: waiting (gray)
			switch {
			case idleDuration >= 30*time.Minute:
				state = "waiting"
			case idleDuration >= 15*time.Minute:
				state = "dropped"
			case idleDuration >= 5*time.Minute:
				state = "holding"
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
func InitialModel(daemonEvents <-chan DaemonEvent, tuiCommands chan<- TUICommand) Model {
	return Model{
		currentView:     ViewEvents,
		width:           80, // Default width (Issue #35)
		height:          24, // Default height (Issue #35)
		edges:           []Edge{},
		selectedEdge:    0,
		sessions:        []SessionInfo{},           // Issue #35: Requirement 3
		selectedSession: 0,                         // Issue #35: Requirement 3
		sessionNodes:    make(map[string][]string), // Issue #59: Session-node mapping
		nodeStates:      make(map[string]string),   // Issue #55: Node state tracking
		daemonEvents:    daemonEvents,
		tuiCommands:     tuiCommands,    // Issue #47: Command channel
		events:          []EventEntry{}, // Issue #59: Session-tagged events
		status:          "Starting...",
		nodeCount:       0,
		lastEvent:       "",
		quitting:        false,
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

	case tea.KeyMsg:
		// Issue #45: Redesigned key bindings
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit

		// Right pane tab switching (Issue #45)
		case "tab":
			if m.currentView == ViewEvents {
				m.currentView = ViewRouting
			} else {
				m.currentView = ViewEvents
			}
			return m, nil
		case "1":
			m.currentView = ViewEvents
			return m, nil
		case "2":
			m.currentView = ViewRouting
			return m, nil

		// Left pane (Sessions) navigation (Issue #45)
		case "j", "down":
			if m.selectedSession < len(m.sessions)-1 {
				m.selectedSession++
			}
			return m, nil
		case "k", "up":
			if m.selectedSession > 0 {
				m.selectedSession--
			}
			return m, nil
		case " ", "enter":
			// Session toggle via TUICommand
			// Issue #59: Guard against "(All)" selection
			if m.selectedSession > 0 && m.selectedSession < len(m.sessions) {
				sess := m.sessions[m.selectedSession]
				if m.tuiCommands != nil {
					m.tuiCommands <- TUICommand{
						Type:   "session_toggle",
						Target: sess.Name,
					}
				}
			}
			return m, nil
		case "p":
			// Issue #47: Send PING to selected session
			// Issue #59: Guard against "(All)" selection
			if m.selectedSession > 0 && m.selectedSession < len(m.sessions) {
				sess := m.sessions[m.selectedSession]
				if m.tuiCommands != nil {
					m.tuiCommands <- TUICommand{
						Type:   "send_ping",
						Target: sess.Name,
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
				// Issue #59: Prepend "(All)" virtual entry
				allEntry := SessionInfo{Name: "(All)", Enabled: true}
				m.sessions = append([]SessionInfo{allEntry}, sessionList...)
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
				// Issue #59: Prepend "(All)" virtual entry
				allEntry := SessionInfo{Name: "(All)", Enabled: true}
				m.sessions = append([]SessionInfo{allEntry}, sessionList...)
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
		case "pong_received":
			// Issue #55: Mark node as active when PONG received
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
		case "error":
			m.events = append(m.events, EventEntry{
				Message:     fmt.Sprintf("ERROR: %s", msg.Message),
				SessionName: "", // Error events have no specific session
				Timestamp:   time.Now(),
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

// View renders the TUI with left-right split layout (Issue #45).
func (m Model) View() string {
	if m.quitting {
		return "Shutting down...\n"
	}

	// Minimum size check (Issue #35)
	if m.width < minWidth || m.height < minHeight {
		warning := warningStyle.Render(fmt.Sprintf("⚠️  Terminal too small (min: %dx%d, current: %dx%d)", minWidth, minHeight, m.width, m.height))
		return borderStyle.Width(m.width - 2).Render(warning)
	}

	// Issue #45: Calculate pane widths for split layout
	totalWidth := m.width - 4 // Account for border + padding
	leftPaneWidth := 25       // Fixed width for sessions list
	rightPaneWidth := totalWidth - leftPaneWidth - 1
	contentHeight := m.height - 4 // Account for border + padding

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
	splitView := lipgloss.JoinHorizontal(lipgloss.Top, leftPaneStyled, separator, rightPaneStyled)

	// Apply border (Issue #35)
	// Issue #59: Dynamic border color based on selection
	localBorderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(m.getSelectedBorderColor()))
	return localBorderStyle.Width(m.width - 2).Height(m.height - 2).Render(splitView)
}

// renderLeftPane renders the left pane (Sessions list).
// Issue #45: New function for left-right split layout
// Issue #64: Simplified with emoji status indicators
func (m Model) renderLeftPane(width, height int) string {
	var b strings.Builder

	b.WriteString("Sessions\n")
	b.WriteString(strings.Repeat("─", width-2) + "\n")

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

			var line string
			if sess.Name == "(All)" {
				// "(All)" has no emoji prefix
				line = fmt.Sprintf("%s%s", cursor, sess.Name)
			} else {
				// Status emoji
				statusEmoji := "⚫"
				if sess.Enabled {
					statusEmoji = "🟢"
				}

				// Mail emoji for sessions with idle/waiting nodes
				// NOTE: Skip idle check for disabled sessions
				mailEmoji := "  "
				if sess.Enabled && m.sessionHasIdleNodes(sess.Name) {
					mailEmoji = "📧"
				}

				// Add space after emojis before session name
				prefix := statusEmoji + mailEmoji + " "

				line = fmt.Sprintf("%s%s%s", cursor, prefix, sess.Name)
			}

			// Issue #89: Apply reverse style with fixed width for full-line highlight
			if i == m.selectedSession {
				line = selectedSessionStyle.Width(width - 2).Render(line)
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n")
	b.WriteString("[space: session on/off] [p: ping]\n")

	return b.String()
}

// renderRightPane renders the right pane (Events/Routing tabs).
// Issue #45: New function for left-right split layout
func (m Model) renderRightPane(width, height int) string {
	var b strings.Builder

	// Tab display
	b.WriteString("[")
	if m.currentView == ViewEvents {
		b.WriteString("1:Events*")
	} else {
		b.WriteString("1:Events")
	}
	b.WriteString(" | ")
	if m.currentView == ViewRouting {
		b.WriteString("2:Routing*")
	} else {
		b.WriteString("2:Routing")
	}
	b.WriteString("]\n")

	// Issue #93: Legend display
	legend := "Legend: " +
		activeNodeStyle.Render("Active") + " | " +
		ballHolderStyle.Render("Holding") + " | " +
		droppedNodeStyle.Render("Dropped") + " | " +
		waitingNodeStyle.Render("Waiting")
	b.WriteString(legend + "\n\n")

	// Content based on current view
	switch m.currentView {
	case ViewEvents:
		b.WriteString(m.renderEventsView(width, height-7))
	case ViewRouting:
		b.WriteString(m.renderRoutingView(width, height-7))
	}

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
		if selectedName == "" || event.SessionName == "" || event.SessionName == selectedName {
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
			b.WriteString(fmt.Sprintf("  - %s\n", msg))
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
			line := edge.Raw

			// Issue #42: Replace each segment with colored directional arrow
			if len(edge.SegmentDirections) > 0 {
				// Parse nodes from edge
				nodes := ParseEdgeNodes(line)

				// Rebuild line with styled arrows and colored node names (Issue #55)
				if len(nodes) == len(edge.SegmentDirections)+1 {
					var builder strings.Builder
					for j, node := range nodes {
						// Issue #55: Color node name based on state
						// Issue #56: Added "dropped" state
						// Issue #77: Use session-prefixed keys to avoid collision across sessions
						nodeStyle := lipgloss.NewStyle() // default (gray)

						// Construct session-prefixed key
						var stateKey string
						if selectedName != "" {
							// Specific session selected: use that session's prefix
							stateKey = selectedName + ":" + node
						} else {
							// "(All)" selected: find any session containing this node
							for sessionName, nodesInSession := range m.sessionNodes {
								for _, nodeName := range nodesInSession {
									if nodeName == node {
										stateKey = sessionName + ":" + node
										break
									}
								}
								if stateKey != "" {
									break
								}
							}
							// Fallback: if node not found in any session, try simple name (shouldn't happen)
							if stateKey == "" {
								stateKey = node
							}
						}

						if state, exists := m.nodeStates[stateKey]; exists {
							switch state {
							case "active":
								nodeStyle = activeNodeStyle
							case "holding":
								nodeStyle = ballHolderStyle
							case "dropped":
								nodeStyle = droppedNodeStyle
								// case "waiting" or default: use default style
							}
						}
						builder.WriteString(nodeStyle.Render(node))
						if j < len(edge.SegmentDirections) {
							// Get arrow and style for this segment
							// Issue #44: Align all arrows to 6 characters width
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
							default: // "none"
								arrow = "  --  "
								arrowStyle = grayArrowStyle
							}
							builder.WriteString(arrowStyle.Render(arrow))
						}
					}
					line = builder.String()
				}
			}

			// Issue #45: Simplified display (no selection indicator in right pane)
			b.WriteString(fmt.Sprintf("  %s\n", line))
		}
	}
	return b.String()
}
