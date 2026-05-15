package tui

import (
	"fmt"
	"log"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
	"github.com/i9wa4/tmux-a2a-postman/internal/version"
)

// Issue #101: Event severity constants (observer review feedback - MINOR)
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
	SeverityDropped  = "dropped"
)

// Cached style objects (Issue #35)
var (
	warningStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("208")).
		Bold(true)
)

const (
	minWidth  = 40
	minHeight = 10
)

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
	Type    string // "message_received", "status_update", "error", "config_update"
	Message string
	Details map[string]interface{}
}

// DaemonEventMsg wraps DaemonEvent for tea.Msg interface.
type DaemonEventMsg DaemonEvent

// TUICommand represents a command from TUI to the daemon.
// Issue #47: Added for manual PING functionality.
type TUICommand struct {
	Type   string // "send_ping", etc.
	Target string // Session name for PING target
	Value  string // Extra data
}

// Model holds the TUI state.
// Issue #45: Removed messageList and selectedMsg fields
type Model struct {
	// Terminal size (Issue #35)
	width  int
	height int

	// Session list view (Issue #35: Requirement 3, Issue #45: left pane)
	sessions         []SessionInfo
	knownSessions    []SessionInfo
	selectedSession  int
	sessionNodes     map[string][]string // Issue #59: session name -> simple node names
	sessionSnapshots map[string]status.SessionStatus

	// Node state tracking (Issue #55)
	nodeStates        map[string]string // "active" / "idle" / "stale"
	unreadInboxCounts map[string]int    // live unread inbox depth per node

	// Shared state
	daemonEvents  <-chan DaemonEvent
	tuiCommands   chan<- TUICommand // Issue #47: Command channel to daemon
	events        []EventEntry      // Issue #59: Session-tagged events (was messages []string)
	sessionStatus map[string]string // per-session status keyed by session name
	generalStatus string            // fallback for non-session-scoped events
	nodeCount     int
	lastEvent     string
	lastKey       string
	quitting      bool

	// Config reference (for node state thresholds)
	config *config.Config

	ownContextID string
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

func clampSelectedSession(sessions []SessionInfo, selected int) int {
	if len(sessions) == 0 {
		return 0
	}
	if selected >= 0 && selected < len(sessions) {
		return selected
	}
	if selected >= len(sessions) {
		return len(sessions) - 1
	}
	if selected < 0 {
		return 0
	}
	return selected
}

func moveSelectedSession(sessions []SessionInfo, selected, delta int) int {
	if len(sessions) == 0 || delta == 0 {
		return 0
	}

	candidate := clampSelectedSession(sessions, selected)
	next := candidate + delta
	if next >= 0 && next < len(sessions) {
		return next
	}

	return candidate
}

func (m *Model) refreshVisibleSessions() {
	// Default TUI session rows mirror tmux list-sessions order exactly.
	m.sessions = append([]SessionInfo(nil), m.knownSessions...)
	m.selectedSession = clampSelectedSession(m.sessions, m.selectedSession)
	m.pruneSessionSnapshots()
}

func (m *Model) pruneSessionSnapshots() {
	if len(m.sessionSnapshots) == 0 || len(m.knownSessions) == 0 {
		return
	}
	liveSessions := make(map[string]struct{}, len(m.knownSessions))
	for _, session := range m.knownSessions {
		liveSessions[session.Name] = struct{}{}
	}
	for sessionName := range m.sessionSnapshots {
		if _, ok := liveSessions[sessionName]; !ok {
			delete(m.sessionSnapshots, sessionName)
		}
	}
}

func (m Model) sessionStatusFor(sessionName string) (status.SessionStatus, bool) {
	if sessionName == "" {
		return status.SessionStatus{}, false
	}
	snapshot, ok := m.sessionSnapshots[sessionName]
	return snapshot, ok
}

// updateNodeStatesFromActivity updates node states from idle.NodeActivity map (Issue #55).
// Issue #77: Use session-prefixed keys to avoid collision across sessions.
// Issue #79: Simplified - nodeActivity keys are already session-prefixed, no reverse index needed.
func (m *Model) updateNodeStatesFromActivity(nodeStatesRaw interface{}) {
	// Type assertion: nodeStatesRaw should be map[string]idle.NodeActivity
	nodeActivities, ok := nodeStatesRaw.(map[string]idle.NodeActivity)
	if !ok {
		return
	}

	// Build a simple-name filter from the known session map. Canonical
	// session status snapshots remain the primary TUI source; this path only
	// updates the legacy in-memory fallback while status is still loading.
	knownNodes := make(map[string]bool)
	for _, nodes := range m.sessionNodes {
		for _, node := range nodes {
			knownNodes[node] = true
		}
	}

	for nodeKey, activity := range nodeActivities {
		// Extract simple name for the session-node filter.
		simpleName := nodeKey
		if parts := strings.SplitN(nodeKey, ":", 2); len(parts) == 2 {
			simpleName = parts[1]
		}
		if len(knownNodes) > 0 && !knownNodes[simpleName] {
			continue
		}

		// Determine state
		var state string
		switch {
		case !activity.LivenessConfirmed:
			state = "stale"
		case activity.LastReceived.After(activity.LastSent) && !activity.LastReceived.IsZero():
			// LastReceived > LastSent means the node recently received mail.
			// Unread inbox counts overlay pending state separately.
			state = "ready"
		default:
			// A live node with confirmed liveness is ready. Unread inbox counts
			// overlay pending separately; stale is reserved for unavailable panes.
			state = "ready"
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
		width:             80,              // Default width (Issue #35)
		height:            24,              // Default height (Issue #35)
		sessions:          []SessionInfo{}, // Issue #35: Requirement 3
		knownSessions:     []SessionInfo{},
		selectedSession:   0,                         // Issue #35: Requirement 3
		sessionNodes:      make(map[string][]string), // Issue #59: Session-node mapping
		sessionSnapshots:  make(map[string]status.SessionStatus),
		nodeStates:        make(map[string]string), // Issue #55: Node state tracking
		unreadInboxCounts: make(map[string]int),
		config:            cfg,
		daemonEvents:      daemonEvents,
		tuiCommands:       tuiCommands,    // Issue #47: Command channel
		events:            []EventEntry{}, // Issue #59: Session-tagged events
		sessionStatus:     map[string]string{},
		generalStatus:     "Starting...",
		nodeCount:         0,
		lastEvent:         "",
		quitting:          false,
		ownContextID:      ownContextID,
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
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "j", "down":
			m.selectedSession = moveSelectedSession(m.sessions, m.selectedSession, 1)
			return m, nil
		case "k", "up":
			m.selectedSession = moveSelectedSession(m.sessions, m.selectedSession, -1)
			return m, nil
		case "p":
			if m.selectedSession >= 0 && m.selectedSession < len(m.sessions) {
				sess := m.sessions[m.selectedSession]
				m.sessionStatus[sess.Name] = "Sending ping..."
				log.Printf("[PING] keypress received for session %q\n", sess.Name)
				if m.tuiCommands != nil {
					m.tuiCommands <- TUICommand{
						Type:   "send_ping",
						Target: sess.Name,
					}
				} else {
					m.sessionStatus[sess.Name] = "Ping: daemon unavailable"
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
		case "status_update":
			if session, ok := msg.Details["session"].(string); ok {
				m.sessionStatus[session] = msg.Message
			} else {
				m.generalStatus = msg.Message
			}
			if count, ok := msg.Details["node_count"].(int); ok {
				m.nodeCount = count
			}
			// Default TUI session rows mirror the full tmux session list.
			if sessionNodesRaw, ok := msg.Details["session_nodes"].(map[string][]string); ok {
				m.sessionNodes = sessionNodesRaw
				m.refreshVisibleSessions()
			}
			// Issue #36: Bug 2 - update sessions from Details
			if sessionList, ok := msg.Details["sessions"].([]SessionInfo); ok {
				m.knownSessions = sessionList
				m.refreshVisibleSessions()
			}
		// Issue #45: Removed "inbox_update" handler
		case "config_update":
			// Default TUI session rows mirror the full tmux session list.
			if sessionNodesRaw, ok := msg.Details["session_nodes"].(map[string][]string); ok {
				m.sessionNodes = sessionNodesRaw
				m.refreshVisibleSessions()
			}
			// Issue #35: Requirement 3 - update sessions from Details
			if sessionList, ok := msg.Details["sessions"].([]SessionInfo); ok {
				m.knownSessions = sessionList
				m.refreshVisibleSessions()
			}
		case "session_status_update":
			if snapshot, ok := msg.Details["status"].(status.SessionStatus); ok && snapshot.SessionName != "" {
				m.sessionSnapshots[snapshot.SessionName] = snapshot
				m.refreshVisibleSessions()
			}
		case "node_alive":
			// Issue #55: Mark node as active when liveness confirmed
			// Issue #79: Simplified - node key is already session-prefixed
			if node, ok := msg.Details["node"].(string); ok {
				m.nodeStates[node] = "active"
			}
		case "node_activity_update":
			// Issue #55: Update node states from idle tracking
			if nodeStatesRaw, ok := msg.Details["node_states"]; ok {
				// Type assertion is tricky with maps - need to handle interface{} carefully
				// The map comes from idle.GetNodeStates() which returns map[string]NodeActivity
				// We need to extract state from each NodeActivity
				m.updateNodeStatesFromActivity(nodeStatesRaw)
			}
		case "inbox_unread_count_update":
			if counts, ok := msg.Details["unread_counts"].(map[string]int); ok {
				m.unreadInboxCounts = counts
			}
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
			sessionName := m.resolveSessionFromDetails(msg.Details)
			if sessionName != "" {
				m.sessionStatus[sessionName] = msg.Message
			}
			if node, ok := msg.Details["node"].(string); ok {
				m.nodeStates[node] = "stale" // Use "stale" for disappeared panes
			}
			// Add event entry
			m.events = append(m.events, EventEntry{
				Message:     msg.Message,
				SessionName: sessionName,
				Timestamp:   time.Now(),
				Severity:    SeverityDropped,
			})
			if len(m.events) > 10 {
				m.events = m.events[len(m.events)-10:]
			}
		case "pane_collision":
			sessionName := m.resolveSessionFromDetails(msg.Details)
			if sessionName != "" {
				m.sessionStatus[sessionName] = msg.Message
			}
			m.events = append(m.events, EventEntry{
				Message:     msg.Message,
				SessionName: sessionName,
				Timestamp:   time.Now(),
				Severity:    SeverityWarning,
			})
			if len(m.events) > 10 {
				m.events = m.events[len(m.events)-10:]
			}
		case "pane_restart":
			sessionName := m.resolveSessionFromDetails(msg.Details)
			if sessionName != "" {
				m.sessionStatus[sessionName] = msg.Message
			}
			m.events = append(m.events, EventEntry{
				Message:     msg.Message,
				SessionName: sessionName,
				Timestamp:   time.Now(),
				Severity:    SeverityWarning,
			})
			if len(m.events) > 10 {
				m.events = m.events[len(m.events)-10:]
			}
		case "session_collapsed":
			sessionName := m.resolveSessionFromDetails(msg.Details)
			if sessionName != "" {
				m.sessionStatus[sessionName] = msg.Message
			}
			m.events = append(m.events, EventEntry{
				Message:     msg.Message,
				SessionName: sessionName,
				Timestamp:   time.Now(),
				Severity:    SeverityCritical,
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

// resolveSessionFromDetails resolves session context from event details and
// falls back to a unique bare-node lookup through the current session map.
func (m Model) resolveSessionFromDetails(details map[string]interface{}) string {
	sessionName := extractSessionFromDetails(details)
	if sessionName != "" || details == nil {
		return sessionName
	}

	node, ok := details["node"].(string)
	if !ok || node == "" || strings.Contains(node, ":") {
		return ""
	}

	match := ""
	for sessionName, nodes := range m.sessionNodes {
		for _, sessionNode := range nodes {
			if sessionNode != node {
				continue
			}
			if match != "" && match != sessionName {
				return ""
			}
			match = sessionName
			break
		}
	}

	return match
}

func sessionIndicator(state string, enabled bool) string {
	if !enabled {
		return "⚫"
	}
	switch state {
	case "", "initial", "unavailable", "unowned":
		return "⚫"
	case "waiting":
		return "🟡"
	case "pending":
		return "🔷"
	case "stale":
		return "🔴"
	default:
		return "🟢"
	}
}

func sessionStatusUnavailable(snapshot status.SessionStatus) bool {
	return snapshot.VisibleState == "unavailable" || snapshot.VisibleState == "unowned"
}

func (m Model) defaultSessionIndicator(session SessionInfo) string {
	snapshot, ok := m.sessionStatusFor(session.Name)
	if !ok {
		// Session exists in tmux, but canonical status has not arrived yet.
		return "⚫"
	}
	if sessionStatusUnavailable(snapshot) {
		return "⚫"
	}
	state := snapshot.VisibleState
	if state == "" {
		state = status.SessionVisibleState(snapshot.Nodes)
	}
	if state == "" {
		// Session exists, but there are no canonical panes to classify yet.
		return "⚫"
	}
	return sessionIndicator(state, true)
}

func nodeStateLabel(state string) string {
	switch state {
	case "", "initial", "unavailable", "unowned":
		return "initial"
	case "ready", "active", "idle":
		return "ready"
	case "stale":
		return "stale"
	default:
		return state
	}
}

func (m Model) renderSessionsSection() string {
	var b strings.Builder

	b.WriteString("[sessions]\n")
	if len(m.sessions) == 0 {
		b.WriteString("(no sessions)\n")
		return b.String()
	}

	for i, session := range m.sessions {
		cursor := "  "
		if i == m.selectedSession {
			cursor = "> "
		}
		indicator := m.defaultSessionIndicator(session)
		b.WriteString(fmt.Sprintf("%s%s [%d] %s\n", cursor, indicator, i, session.Name))
	}

	return b.String()
}

func (m Model) renderNodesSection() string {
	var b strings.Builder

	b.WriteString("[nodes]\n")
	selectedSession := m.getSelectedSessionName()
	if selectedSession == "" {
		b.WriteString("(no nodes)\n")
		return b.String()
	}
	if snapshot, ok := m.sessionStatusFor(selectedSession); ok {
		if sessionStatusUnavailable(snapshot) {
			return b.String() + "(session unavailable)\n"
		}
		return b.String() + m.renderNodesSectionFromStatus(snapshot)
	}
	if len(m.sessionNodes[selectedSession]) == 0 {
		b.WriteString("(non-AI or unknown session)\n")
		return b.String()
	}
	b.WriteString("(loading canonical status)\n")
	return b.String()
}

func visibleStateLabel(node status.NodeStatus) string {
	if node.VisibleState != "" {
		return node.VisibleState
	}
	return status.VisibleState(node.PaneState, node.InboxCount)
}

func orderedStatusNodeNames(snapshot status.SessionStatus) []string {
	seen := make(map[string]struct{}, len(snapshot.Nodes))
	var ordered []string
	for _, window := range snapshot.Windows {
		for _, windowNode := range window.Nodes {
			if _, ok := seen[windowNode.Name]; ok {
				continue
			}
			seen[windowNode.Name] = struct{}{}
			ordered = append(ordered, windowNode.Name)
		}
	}
	for _, node := range snapshot.Nodes {
		if _, ok := seen[node.Name]; ok {
			continue
		}
		seen[node.Name] = struct{}{}
		ordered = append(ordered, node.Name)
	}
	return ordered
}

func (m Model) renderNodesSectionFromStatus(snapshot status.SessionStatus) string {
	nodeByName := make(map[string]status.NodeStatus, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodeByName[node.Name] = node
	}

	nodeNames := orderedStatusNodeNames(snapshot)
	if len(nodeNames) == 0 {
		return "(non-AI or unknown session)\n"
	}

	nameWidth := 0
	for _, nodeName := range nodeNames {
		if len(nodeName) > nameWidth {
			nameWidth = len(nodeName)
		}
	}

	var b strings.Builder
	for _, nodeName := range nodeNames {
		node := nodeByName[nodeName]
		visibleState := visibleStateLabel(node)
		indicator := sessionIndicator(visibleState, true)
		label := nodeStateLabel(visibleState)
		b.WriteString(fmt.Sprintf("%-*s  %s  %s\n", nameWidth, nodeName, indicator, label))
	}

	return b.String()
}

// View renders the simplified default operator surface for #363.
func (m Model) View() tea.View {
	view := tea.View{AltScreen: true}

	if m.quitting {
		view.Content = "Shutting down...\n"
		return view
	}

	if m.width < minWidth || m.height < minHeight {
		warning := warningStyle.Render(fmt.Sprintf("⚠️  Terminal too small (min: %dx%d, current: %dx%d)", minWidth, minHeight, m.width, m.height))
		view.Content = warning + "\n"
		return view
	}

	m.selectedSession = clampSelectedSession(m.sessions, m.selectedSession)

	var b strings.Builder
	b.WriteString("tmux-a2a-postman " + version.Version + "   [up/down:move] [p:ping] [q:quit]\n\n")
	b.WriteString(m.renderSessionsSection())
	b.WriteString("\n")
	b.WriteString(m.renderNodesSection())
	view.Content = b.String()
	return view
}
