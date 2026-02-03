package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// DraftMode represents the current step in draft creation.
type DraftMode int

const (
	DraftModeSelectRecipient DraftMode = iota
	DraftModeInputMessage
	DraftModePreview
	DraftModeSubmit
)

// DraftModel holds the TUI state for create-draft.
type DraftModel struct {
	mode          DraftMode
	nodes         map[string]string // Available nodes from discovery
	recipientList list.Model
	selectedNode  string
	messageArea   textarea.Model
	messageBody   string
	sessionDir    string
	contextID     string
	senderNode    string
	err           error
	quitting      bool
	submitted     bool
}

// recipientItem implements list.Item for bubbles/list.
type recipientItem struct {
	name string
}

func (i recipientItem) FilterValue() string { return i.name }
func (i recipientItem) Title() string       { return i.name }
func (i recipientItem) Description() string { return "" }

// InitialDraftModel creates the initial model for create-draft TUI.
func InitialDraftModel(sessionDir, contextID, senderNode string, nodes map[string]string) DraftModel {
	// Build recipient list
	items := make([]list.Item, 0, len(nodes))
	for nodeName := range nodes {
		if nodeName != senderNode {
			items = append(items, recipientItem{name: nodeName})
		}
	}

	recipientList := list.New(items, list.NewDefaultDelegate(), 40, 10)
	recipientList.Title = "Select Recipient"

	messageArea := textarea.New()
	messageArea.Placeholder = "Type your message here..."
	messageArea.Focus()
	messageArea.SetWidth(80)
	messageArea.SetHeight(10)

	return DraftModel{
		mode:          DraftModeSelectRecipient,
		nodes:         nodes,
		recipientList: recipientList,
		messageArea:   messageArea,
		sessionDir:    sessionDir,
		contextID:     contextID,
		senderNode:    senderNode,
	}
}

// Init initializes the model.
func (m DraftModel) Init() tea.Cmd {
	return nil
}

// Update handles messages and updates the model.
func (m DraftModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		switch m.mode {
		case DraftModeSelectRecipient:
			switch msg.String() {
			case "q", "esc", "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			case "enter":
				// Confirm recipient selection
				if selected := m.recipientList.SelectedItem(); selected != nil {
					m.selectedNode = selected.(recipientItem).name
					m.mode = DraftModeInputMessage
					return m, textarea.Blink
				}
			default:
				var cmd tea.Cmd
				m.recipientList, cmd = m.recipientList.Update(msg)
				return m, cmd
			}

		case DraftModeInputMessage:
			switch msg.String() {
			case "esc":
				// Go back to recipient selection
				m.mode = DraftModeSelectRecipient
				return m, nil
			case "ctrl+d":
				// Submit message
				m.messageBody = m.messageArea.Value()
				m.mode = DraftModePreview
				return m, nil
			case "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			default:
				var cmd tea.Cmd
				m.messageArea, cmd = m.messageArea.Update(msg)
				return m, cmd
			}

		case DraftModePreview:
			switch msg.String() {
			case "enter", "y":
				// Confirm and submit
				if err := m.submitDraft(); err != nil {
					m.err = err
					return m, nil
				}
				m.submitted = true
				m.quitting = true
				return m, tea.Quit
			case "esc", "n":
				// Go back to message input
				m.mode = DraftModeInputMessage
				return m, textarea.Blink
			case "ctrl+c", "q":
				m.quitting = true
				return m, tea.Quit
			}
		}
	}

	return m, nil
}

// View renders the TUI.
func (m DraftModel) View() string {
	if m.quitting {
		if m.submitted {
			return "Draft created successfully!\n"
		}
		return "Cancelled.\n"
	}

	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress any key to exit.\n", m.err)
	}

	var b strings.Builder

	b.WriteString("=== Create Draft ===\n\n")

	switch m.mode {
	case DraftModeSelectRecipient:
		b.WriteString(m.recipientList.View())
		b.WriteString("\n\nKeys: j/k (nav) | Enter (select) | q/Esc (quit)\n")

	case DraftModeInputMessage:
		b.WriteString(fmt.Sprintf("Recipient: %s\n\n", m.selectedNode))
		b.WriteString(m.messageArea.View())
		b.WriteString("\n\nKeys: Ctrl+D (submit) | Esc (back) | Ctrl+C (quit)\n")

	case DraftModePreview:
		b.WriteString(fmt.Sprintf("From: %s\n", m.senderNode))
		b.WriteString(fmt.Sprintf("To: %s\n", m.selectedNode))
		b.WriteString(fmt.Sprintf("Context: %s\n\n", m.contextID))
		b.WriteString("--- Message ---\n")
		b.WriteString(m.messageBody)
		b.WriteString("\n--- End ---\n\n")
		b.WriteString("Submit this draft? [y/n] (Esc: back | Ctrl+C: quit)\n")
	}

	return b.String()
}

// submitDraft writes the draft file.
func (m *DraftModel) submitDraft() error {
	draftDir := filepath.Join(m.sessionDir, "draft")
	if err := os.MkdirAll(draftDir, 0o755); err != nil {
		return fmt.Errorf("creating draft directory: %w", err)
	}

	now := time.Now()
	ts := now.Format("20060102-150405")
	filename := fmt.Sprintf("%s-from-%s-to-%s.md", ts, m.senderNode, m.selectedNode)
	draftPath := filepath.Join(draftDir, filename)

	content := fmt.Sprintf("---\nmethod: message/send\nparams:\n  contextId: %s\n  from: %s\n  to: %s\n  timestamp: %s\n---\n\n%s\n",
		m.contextID, m.senderNode, m.selectedNode, now.Format("2006-01-02T15:04:05.000000"), m.messageBody)

	if err := os.WriteFile(draftPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing draft: %w", err)
	}

	return nil
}
