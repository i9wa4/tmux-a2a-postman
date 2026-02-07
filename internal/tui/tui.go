package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/i9wa4/tmux-a2a-postman/internal/uipane"
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

// DaemonEvent represents an event from the daemon goroutine.
type DaemonEvent struct {
	Type    string // "message_received", "status_update", "error", "config_update", "edge_update", "concierge_status_update"
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

	// Target node status (Issue #45: renamed from "Concierge" in UI)
	conciergeStatus *uipane.PaneInfo

	// Shared state
	daemonEvents <-chan DaemonEvent
	tuiCommands  chan<- TUICommand // Issue #47: Command channel to daemon
	messages     []string
	status       string
	nodeCount    int
	lastEvent    string
	quitting     bool
}

// Quitting returns true if the TUI is in quitting state (Issue #57).
func (m Model) Quitting() bool {
	return m.quitting
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
		sessions:        []SessionInfo{}, // Issue #35: Requirement 3
		selectedSession: 0,               // Issue #35: Requirement 3
		conciergeStatus: nil,
		daemonEvents:    daemonEvents,
		tuiCommands:     tuiCommands, // Issue #47: Command channel
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
			if m.selectedSession >= 0 && m.selectedSession < len(m.sessions) {
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
			if m.selectedSession >= 0 && m.selectedSession < len(m.sessions) {
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
			if paneInfo, ok := msg.Details["pane_info"].(*uipane.PaneInfo); ok {
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
	return borderStyle.Width(m.width - 2).Height(m.height - 2).Render(splitView)
}

// renderLeftPane renders the left pane (Sessions list).
// Issue #45: New function for left-right split layout
func (m Model) renderLeftPane(width, height int) string {
	var b strings.Builder

	b.WriteString("Sessions\n")
	b.WriteString(strings.Repeat("─", width-2) + "\n")

	if len(m.sessions) == 0 {
		b.WriteString("(no sessions)\n")
	} else {
		// Calculate scroll window (each session can use 1-2 lines)
		maxLines := height - 5 // Reserve space for header + footer
		if maxLines < 2 {
			maxLines = 2
		}

		startIdx := 0
		endIdx := len(m.sessions)
		if len(m.sessions) > maxLines/2 {
			// Keep selected session visible (approximate)
			if m.selectedSession >= maxLines/2 {
				startIdx = m.selectedSession - maxLines/2 + 1
			}
			endIdx = startIdx + maxLines/2
			if endIdx > len(m.sessions) {
				endIdx = len(m.sessions)
			}
		}

		for i := startIdx; i < endIdx; i++ {
			sess := m.sessions[i]

			// Status indicator (ON/OFF)
			statusIcon := "ON "
			if !sess.Enabled {
				statusIcon = "OFF"
			}

			// Check if session name + status fits in one line
			oneLine := fmt.Sprintf("%s (%d) %s", sess.Name, sess.NodeCount, statusIcon)
			if len(oneLine) <= width-4 {
				// Fits in one line
				if i == m.selectedSession {
					b.WriteString(fmt.Sprintf("> %s\n", oneLine))
				} else {
					b.WriteString(fmt.Sprintf("  %s\n", oneLine))
				}
			} else {
				// Wrap to two lines
				if i == m.selectedSession {
					b.WriteString(fmt.Sprintf("> %s\n", sess.Name))
					b.WriteString(fmt.Sprintf("  (%d) %s\n", sess.NodeCount, statusIcon))
				} else {
					b.WriteString(fmt.Sprintf("  %s\n", sess.Name))
					b.WriteString(fmt.Sprintf("  (%d) %s\n", sess.NodeCount, statusIcon))
				}
			}
		}
	}

	b.WriteString("\n")
	b.WriteString("[space: session on/off] [p: ping]\n") // Issue #47: Added ping help

	return b.String()
}

// renderRightPane renders the right pane (Events/Routing tabs).
// Issue #45: New function for left-right split layout
func (m Model) renderRightPane(width, height int) string {
	var b strings.Builder

	// Issue #45: Target Node status (renamed from "Concierge")
	b.WriteString(m.renderTargetNodeStatusLine())
	b.WriteString("\n")

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
	b.WriteString("]\n\n")

	// Content based on current view
	switch m.currentView {
	case ViewEvents:
		b.WriteString(m.renderEventsView(width, height-6))
	case ViewRouting:
		b.WriteString(m.renderRoutingView(width, height-6))
	}

	return b.String()
}

// renderTargetNodeStatusLine renders the target node status line.
// Issue #45: Renamed from "Concierge" to "Target Node"
func (m Model) renderTargetNodeStatusLine() string {
	var b strings.Builder
	b.WriteString("[Target: ")

	if m.conciergeStatus == nil {
		b.WriteString("UNKNOWN]")
		return b.String()
	}

	// Status display (text only, no emoji for simplicity)
	b.WriteString(fmt.Sprintf("%s]", m.conciergeStatus.Status))

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
	if len(m.messages) == 0 {
		b.WriteString("  (no events yet)\n")
	} else {
		// Truncate list if too long
		maxLines := height - 2 // Issue #45: Adjusted for right pane
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

// renderRoutingView renders the routing view (right pane content).
// Issue #45: Adjusted for right pane layout, removed selection display
func (m Model) renderRoutingView(width, height int) string {
	var b strings.Builder
	b.WriteString("Routing Edges:\n")
	if len(m.edges) == 0 {
		b.WriteString("  (no edges defined)\n")
	} else {
		// Truncate list if too long
		maxLines := height - 2 // Issue #45: Adjusted for right pane
		if maxLines < 1 {
			maxLines = 1
		}
		displayCount := len(m.edges)
		if displayCount > maxLines {
			displayCount = maxLines
		}
		startIdx := 0
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

			// Issue #45: Simplified display (no selection indicator in right pane)
			b.WriteString(fmt.Sprintf("  %s\n", line))
		}
	}
	return b.String()
}
