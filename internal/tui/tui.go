package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/i9wa4/tmux-a2a-postman/internal/concierge"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
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
)

const (
	minWidth  = 40
	minHeight = 10
)

// ViewType represents the current TUI view.
type ViewType int

const (
	ViewEvents ViewType = iota
	ViewMessages
	ViewRouting
	ViewSessions // Issue #35: Requirement 3
)

// Edge represents a routing edge definition.
type Edge struct {
	Raw             string    // Raw edge string (e.g., "A -- B -- C")
	LastActivityAt  time.Time // Issue #35: Requirement 5 - last message time
	IsActive        bool      // Issue #35: Requirement 5 - was recently used
	Direction      string    // Issue #37: Communication direction ("none", "forward", "backward", "bidirectional")
	SegmentDirections  []string  // Issue #42: Direction for each segment in chain edges
}

// SessionInfo holds information about a tmux session.
// Issue #35: Requirement 3 - multiple session display
type SessionInfo struct {
	Name      string
	NodeCount int
	Enabled   bool // Issue #35: Requirement 4 - enable/disable toggle
}

// DaemonEvent represents an event from the daemon goroutine.
type DaemonEvent struct {
	Type    string // "message_received", "status_update", "error", "inbox_update", "config_update", "edge_update", "concierge_status_update"
	Message string
	Details map[string]interface{}
}

// DaemonEventMsg wraps DaemonEvent for tea.Msg interface.
type DaemonEventMsg DaemonEvent

// Model holds the TUI state.
type Model struct {
	// View state
	currentView ViewType

	// Terminal size (Issue #35)
	width  int
	height int

	// Message list view
	messageList []message.MessageInfo
	selectedMsg int

	// Routing view
	edges        []Edge
	selectedEdge int

	// Session list view (Issue #35: Requirement 3)
	sessions        []SessionInfo
	selectedSession int

	// Concierge status
	conciergeStatus *concierge.PaneInfo

	// Shared state
	daemonEvents <-chan DaemonEvent
	messages     []string
	status       string
	nodeCount    int
	lastEvent    string
	quitting     bool
}

// InitialModel creates the initial TUI model.
func InitialModel(daemonEvents <-chan DaemonEvent) Model {
	return Model{
		currentView:     ViewEvents,
		width:           80, // Default width (Issue #35)
		height:          24, // Default height (Issue #35)
		messageList:     []message.MessageInfo{},
		selectedMsg:     0,
		edges:           []Edge{},
		selectedEdge:    0,
		sessions:        []SessionInfo{}, // Issue #35: Requirement 3
		selectedSession: 0,               // Issue #35: Requirement 3
		conciergeStatus: nil,
		daemonEvents:    daemonEvents,
		messages:        []string{},
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
		// Global keys
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "tab":
			// Cycle through views (Issue #35: now 4 views)
			m.currentView = (m.currentView + 1) % 4
			return m, nil
		case "1":
			m.currentView = ViewEvents
			return m, nil
		case "2":
			m.currentView = ViewMessages
			return m, nil
		case "3":
			m.currentView = ViewRouting
			return m, nil
		case "4":
			m.currentView = ViewSessions // Issue #35: Requirement 3
			return m, nil
		}

		// View-specific keys
		switch m.currentView {
		case ViewMessages:
			switch msg.String() {
			case "j", "down":
				if m.selectedMsg < len(m.messageList)-1 {
					m.selectedMsg++
				}
			case "k", "up":
				if m.selectedMsg > 0 {
					m.selectedMsg--
				}
			}
		case ViewRouting:
			switch msg.String() {
			case "j", "down":
				if m.selectedEdge < len(m.edges)-1 {
					m.selectedEdge++
				}
			case "k", "up":
				if m.selectedEdge > 0 {
					m.selectedEdge--
				}
			}
		case ViewSessions:
			// Issue #35: Requirement 3 & 4
			switch msg.String() {
			case "j", "down":
				if m.selectedSession < len(m.sessions)-1 {
					m.selectedSession++
				}
			case "k", "up":
				if m.selectedSession > 0 {
					m.selectedSession--
				}
			case " ", "enter":
				// Issue #35: Requirement 4 - toggle session enable/disable
				if m.selectedSession >= 0 && m.selectedSession < len(m.sessions) {
					m.sessions[m.selectedSession].Enabled = !m.sessions[m.selectedSession].Enabled
				}
			}
		}

	case DaemonEventMsg:
		// Handle daemon event
		switch msg.Type {
		case "message_received":
			m.lastEvent = msg.Message
			m.messages = append(m.messages, msg.Message)
			// Keep only last 10 messages
			if len(m.messages) > 10 {
				m.messages = m.messages[len(m.messages)-10:]
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
		case "inbox_update":
			// Update message list from Details
			if msgList, ok := msg.Details["messages"].([]message.MessageInfo); ok {
				m.messageList = msgList
				// Clamp selection
				if m.selectedMsg >= len(m.messageList) {
					m.selectedMsg = len(m.messageList) - 1
				}
				if m.selectedMsg < 0 {
					m.selectedMsg = 0
				}
			}
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
		case "concierge_status_update":
			// Update concierge status from Details
			if paneInfo, ok := msg.Details["pane_info"].(*concierge.PaneInfo); ok {
				m.conciergeStatus = paneInfo
			}
		case "error":
			m.messages = append(m.messages, fmt.Sprintf("ERROR: %s", msg.Message))
			if len(m.messages) > 10 {
				m.messages = m.messages[len(m.messages)-10:]
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

// View renders the TUI.
func (m Model) View() string {
	if m.quitting {
		return "Shutting down...\n"
	}

	// Minimum size check (Issue #35)
	if m.width < minWidth || m.height < minHeight {
		warning := warningStyle.Render(fmt.Sprintf("⚠️  Terminal too small (min: %dx%d, current: %dx%d)", minWidth, minHeight, m.width, m.height))
		return borderStyle.Width(m.width - 2).Render(warning)
	}

	// Calculate content dimensions (Issue #35)
	contentWidth := m.width - 4   // Account for border + padding
	contentHeight := m.height - 4 // Account for border + padding

	var b strings.Builder

	// Header
	b.WriteString("=== Postman Daemon ===\n")
	b.WriteString(fmt.Sprintf("Status: %s | Nodes: %d\n", m.status, m.nodeCount))
	b.WriteString("\n")

	// Concierge Status Panel
	b.WriteString(m.renderConciergePanel())
	b.WriteString("\n")

	// View tabs (Issue #35: added 4th tab)
	b.WriteString("[")
	if m.currentView == ViewEvents {
		b.WriteString("1:Events*")
	} else {
		b.WriteString("1:Events")
	}
	b.WriteString(" | ")
	if m.currentView == ViewMessages {
		b.WriteString("2:Messages*")
	} else {
		b.WriteString("2:Messages")
	}
	b.WriteString(" | ")
	if m.currentView == ViewRouting {
		b.WriteString("3:Routing*")
	} else {
		b.WriteString("3:Routing")
	}
	b.WriteString(" | ")
	if m.currentView == ViewSessions {
		b.WriteString("4:Sessions*")
	} else {
		b.WriteString("4:Sessions")
	}
	b.WriteString("]\n\n")

	// View-specific content
	switch m.currentView {
	case ViewEvents:
		b.WriteString(m.renderEventsView(contentWidth, contentHeight))
	case ViewMessages:
		b.WriteString(m.renderMessagesView(contentWidth, contentHeight))
	case ViewRouting:
		b.WriteString(m.renderRoutingView(contentWidth, contentHeight))
	case ViewSessions:
		// Issue #35: Requirement 3 - session list view
		b.WriteString(m.renderSessionsView(contentWidth, contentHeight))
	}

	b.WriteString("\n")

	// Status bar
	b.WriteString("Keys: tab/1-4 (switch view) | j/k (nav) | q (quit)\n")

	content := b.String()

	// Apply border (Issue #35)
	return borderStyle.Width(m.width - 2).Height(m.height - 2).Render(content)
}

func (m Model) renderEventsView(contentWidth, contentHeight int) string {
	var b strings.Builder
	b.WriteString("Recent Events:\n")
	if len(m.messages) == 0 {
		b.WriteString("  (no events yet)\n")
	} else {
		// Truncate list if too long (Issue #35)
		maxLines := contentHeight - 8 // Reserve space for header/footer
		// Issue #36: Bug 3 - Prevent negative maxLines
		if maxLines < 1 {
			maxLines = 1
		}
		displayCount := len(m.messages)
		if displayCount > maxLines {
			displayCount = maxLines
		}
		startIdx := len(m.messages) - displayCount
		for i := startIdx; i < len(m.messages); i++ {
			msg := m.messages[i]
			// Truncate long lines (Issue #35)
			if len(msg) > contentWidth-4 {
				msg = msg[:contentWidth-7] + "..."
			}
			b.WriteString(fmt.Sprintf("  - %s\n", msg))
		}
	}
	return b.String()
}

func (m Model) renderMessagesView(contentWidth, contentHeight int) string {
	var b strings.Builder
	b.WriteString("Inbox Messages:\n")
	if len(m.messageList) == 0 {
		b.WriteString("  (no messages)\n")
	} else {
		// Truncate list if too long (Issue #35)
		maxLines := contentHeight - 8
		// Issue #36: Bug 3 - Prevent negative maxLines
		if maxLines < 1 {
			maxLines = 1
		}
		displayCount := len(m.messageList)
		if displayCount > maxLines {
			displayCount = maxLines
		}
		startIdx := 0
		if len(m.messageList) > maxLines {
			// Keep selected message visible
			if m.selectedMsg >= maxLines {
				startIdx = m.selectedMsg - maxLines + 1
			}
		}
		endIdx := startIdx + displayCount
		if endIdx > len(m.messageList) {
			endIdx = len(m.messageList)
		}

		for i := startIdx; i < endIdx; i++ {
			msg := m.messageList[i]
			line := fmt.Sprintf("%s | from: %s | to: %s", msg.Timestamp, msg.From, msg.To)
			// Truncate long lines (Issue #35)
			if len(line) > contentWidth-6 {
				line = line[:contentWidth-9] + "..."
			}
			if i == m.selectedMsg {
				b.WriteString(fmt.Sprintf("  > %s\n", line))
			} else {
				b.WriteString(fmt.Sprintf("    %s\n", line))
			}
		}
	}
	return b.String()
}

func (m Model) renderRoutingView(contentWidth, contentHeight int) string {
	var b strings.Builder
	b.WriteString("Routing Edges:\n")
	if len(m.edges) == 0 {
		b.WriteString("  (no edges defined)\n")
	} else {
		// Truncate list if too long (Issue #35)
		maxLines := contentHeight - 8
		// Issue #36: Bug 3 - Prevent negative maxLines
		if maxLines < 1 {
			maxLines = 1
		}
		displayCount := len(m.edges)
		if displayCount > maxLines {
			displayCount = maxLines
		}
		startIdx := 0
		if len(m.edges) > maxLines {
			if m.selectedEdge >= maxLines {
				startIdx = m.selectedEdge - maxLines + 1
			}
		}
		endIdx := startIdx + displayCount
		if endIdx > len(m.edges) {
			endIdx = len(m.edges)
		}

		for i := startIdx; i < endIdx; i++ {
			edge := m.edges[i]
			line := edge.Raw

			// Issue #42: Replace each segment with colored directional arrow
			if len(edge.SegmentDirections) > 0 {
				// Parse nodes from edge
				var nodes []string
				if strings.Contains(line, "-->") {
					parts := strings.Split(line, "-->")
					for _, p := range parts {
						nodes = append(nodes, strings.TrimSpace(p))
					}
				} else if strings.Contains(line, "--") {
					parts := strings.Split(line, "--")
					for _, p := range parts {
						nodes = append(nodes, strings.TrimSpace(p))
					}
				}

				// Rebuild line with styled arrows
				if len(nodes) == len(edge.SegmentDirections)+1 {
					var builder strings.Builder
					for j, node := range nodes {
						builder.WriteString(node)
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

			// Issue #42: Remove emoji prefix (simplified display)
			prefix := "    "
			suffix := ""

			// Issue #43: Remove truncation to show all node names (allow wrapping)

			if i == m.selectedEdge {
				b.WriteString(fmt.Sprintf("  > %s%s\n", line, suffix))
			} else {
				b.WriteString(fmt.Sprintf("%s%s%s\n", prefix, line, suffix))
			}
		}
	}
	return b.String()
}

func (m Model) renderConciergePanel() string {
	var b strings.Builder
	b.WriteString("[Concierge Status] ")

	if m.conciergeStatus == nil {
		b.WriteString("UNKNOWN (no data)")
		return b.String()
	}

	// Status indicator with color emoji
	statusEmoji := ""
	switch m.conciergeStatus.Status {
	case concierge.StatusVisible:
		statusEmoji = "🟢"
	case concierge.StatusWindowVisible:
		statusEmoji = "🟡"
	case concierge.StatusNotVisible:
		statusEmoji = "🔴"
	case concierge.StatusUnknown:
		statusEmoji = "⚪"
	case concierge.StatusInactive:
		statusEmoji = "⚫"
	}

	b.WriteString(fmt.Sprintf("%s %s", statusEmoji, m.conciergeStatus.Status))

	// Additional info
	if m.conciergeStatus.PaneID != "" {
		b.WriteString(fmt.Sprintf(" | Pane: %s", m.conciergeStatus.PaneID))
	}

	return b.String()
}

func (m Model) renderSessionsView(contentWidth, contentHeight int) string {
	var b strings.Builder
	b.WriteString("Sessions:\n")
	if len(m.sessions) == 0 {
		b.WriteString("  (no sessions)\n")
	} else {
		// Truncate list if too long (Issue #35)
		maxLines := contentHeight - 8
		// Issue #36: Bug 3 - Prevent negative maxLines
		if maxLines < 1 {
			maxLines = 1
		}
		displayCount := len(m.sessions)
		if displayCount > maxLines {
			displayCount = maxLines
		}
		startIdx := 0
		if len(m.sessions) > maxLines {
			// Keep selected session visible
			if m.selectedSession >= maxLines {
				startIdx = m.selectedSession - maxLines + 1
			}
		}
		endIdx := startIdx + displayCount
		if endIdx > len(m.sessions) {
			endIdx = len(m.sessions)
		}

		for i := startIdx; i < endIdx; i++ {
			sess := m.sessions[i]

			// Issue #35: Requirement 4 - status indicator
			statusIcon := "🔴" // Disabled
			if sess.Enabled {
				statusIcon = "🟢" // Enabled
			}

			line := fmt.Sprintf("%s %s (nodes: %d)", statusIcon, sess.Name, sess.NodeCount)

			// Truncate long lines (Issue #35)
			if len(line) > contentWidth-6 {
				line = line[:contentWidth-9] + "..."
			}

			if i == m.selectedSession {
				b.WriteString(fmt.Sprintf("  > %s\n", line))
			} else {
				b.WriteString(fmt.Sprintf("    %s\n", line))
			}
		}
		b.WriteString("\n")
		b.WriteString("Press space/enter to toggle enable/disable\n")
	}
	return b.String()
}
