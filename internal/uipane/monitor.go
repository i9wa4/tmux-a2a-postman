package uipane

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Status represents concierge pane visibility status.
type Status string

const (
	StatusVisible       Status = "VISIBLE"        // pane_active=1
	StatusWindowVisible Status = "WINDOW_VISIBLE" // pane_active=0 && window_active=1
	StatusNotVisible    Status = "NOT_VISIBLE"    // pane_active=0 && window_active=0
	StatusUnknown       Status = "UNKNOWN"        // pane disappeared or data unavailable
	StatusInactive      Status = "INACTIVE"       // tmux detached
)

// PaneInfo holds concierge pane monitoring data.
type PaneInfo struct {
	PaneID       string
	PaneActive   bool
	WindowActive bool
	PaneActivity int64 // Unix timestamp
	Status       Status
	LastChecked  time.Time
}

// NotificationLog tracks notification timestamps per context.
type NotificationLog struct {
	mu            sync.RWMutex
	notifications map[string][]NotificationEntry // context_id -> list of entries
}

// NotificationEntry represents a single notification event.
type NotificationEntry struct {
	Timestamp time.Time
	Node      string
}

// NewNotificationLog creates a new notification log.
func NewNotificationLog() *NotificationLog {
	return &NotificationLog{
		notifications: make(map[string][]NotificationEntry),
	}
}

// AddNotification logs a notification event.
func (nl *NotificationLog) AddNotification(contextID, node string, timestamp time.Time) {
	nl.mu.Lock()
	defer nl.mu.Unlock()

	entry := NotificationEntry{
		Timestamp: timestamp,
		Node:      node,
	}
	nl.notifications[contextID] = append(nl.notifications[contextID], entry)
}

// GetNotifications returns all notifications for a context.
func (nl *NotificationLog) GetNotifications(contextID string) []NotificationEntry {
	nl.mu.RLock()
	defer nl.mu.RUnlock()

	entries := nl.notifications[contextID]
	result := make([]NotificationEntry, len(entries))
	copy(result, entries)
	return result
}

// GetPaneInfo retrieves concierge pane information using tmux commands.
// Returns PaneInfo with status based on pane and window active states.
func GetPaneInfo(conciergePaneID string) (*PaneInfo, error) {
	if conciergePaneID == "" {
		return &PaneInfo{Status: StatusUnknown, LastChecked: time.Now()}, nil
	}

	// Get pane info: pane_id, pane_active, pane_activity
	cmd := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id}:#{pane_active}:#{pane_activity}")
	output, err := cmd.Output()
	if err != nil {
		return &PaneInfo{Status: StatusUnknown, LastChecked: time.Now()}, fmt.Errorf("tmux list-panes failed: %w", err)
	}

	var paneActive bool
	var paneActivity int64
	found := false

	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) != 3 {
			continue
		}
		if parts[0] == conciergePaneID {
			found = true
			paneActive = parts[1] == "1"
			paneActivity, _ = strconv.ParseInt(parts[2], 10, 64)
			break
		}
	}

	if !found {
		return &PaneInfo{Status: StatusUnknown, LastChecked: time.Now()}, nil
	}

	// If pane is active, it's visible
	if paneActive {
		return &PaneInfo{
			PaneID:       conciergePaneID,
			PaneActive:   true,
			PaneActivity: paneActivity,
			Status:       StatusVisible,
			LastChecked:  time.Now(),
		}, nil
	}

	// Pane not active, check window activity
	cmd = exec.Command("tmux", "list-windows", "-a", "-F", "#{window_id}:#{window_active}:#{window_panes}")
	_, err = cmd.Output()
	if err != nil {
		return &PaneInfo{
			PaneID:       conciergePaneID,
			PaneActive:   false,
			PaneActivity: paneActivity,
			Status:       StatusUnknown,
			LastChecked:  time.Now(),
		}, nil
	}

	// Find which window contains our pane
	cmd = exec.Command("tmux", "display-message", "-p", "-t", conciergePaneID, "#{window_active}")
	windowActiveOutput, err := cmd.Output()
	if err != nil {
		return &PaneInfo{
			PaneID:       conciergePaneID,
			PaneActive:   false,
			PaneActivity: paneActivity,
			Status:       StatusUnknown,
			LastChecked:  time.Now(),
		}, nil
	}

	windowActive := strings.TrimSpace(string(windowActiveOutput)) == "1"

	status := StatusNotVisible
	if windowActive {
		status = StatusWindowVisible
	}

	return &PaneInfo{
		PaneID:       conciergePaneID,
		PaneActive:   false,
		WindowActive: windowActive,
		PaneActivity: paneActivity,
		Status:       status,
		LastChecked:  time.Now(),
	}, nil
}

// FindTargetPaneID finds the pane_id for A2A_NODE=nodeName.
// Issue #46: Generalized from FindConciergePaneID to accept any node name.
// Returns empty string if not found.
func FindTargetPaneID(nodeName string) (string, error) {
	// Get all panes with their environment
	cmd := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id}")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tmux list-panes failed: %w", err)
	}

	// Issue #46: Construct the search string from nodeName parameter
	searchStr := fmt.Sprintf("A2A_NODE=%s", nodeName)

	for _, line := range strings.Split(string(output), "\n") {
		paneID := strings.TrimSpace(line)
		if paneID == "" {
			continue
		}

		// Check A2A_NODE environment variable for this pane
		cmd := exec.Command("tmux", "display-message", "-p", "-t", paneID, "#{pane_pid}")
		pidOutput, err := cmd.Output()
		if err != nil {
			continue
		}
		pid := strings.TrimSpace(string(pidOutput))
		if pid == "" {
			continue
		}

		// Check process environment (platform-specific, simplified check)
		// NOTE: This is best-effort - may not work on all platforms
		cmd = exec.Command("ps", "eww", pid)
		envOutput, err := cmd.Output()
		if err != nil {
			continue
		}

		if strings.Contains(string(envOutput), searchStr) {
			return paneID, nil
		}
	}

	return "", nil
}
