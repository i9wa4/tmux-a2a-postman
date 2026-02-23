package reminder

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
)

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
func (r *ReminderState) Increment(nodeName string, sessionName string, nodes map[string]discovery.NodeInfo, cfg *config.Config) {
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
			// Phase 1: exact match (supports tests and non-prefixed maps)
			var nodeInfo discovery.NodeInfo
			found := false
			if info, ok := nodes[nodeName]; ok {
				nodeInfo = info
				found = true
			}
			// Phase 2: deterministic session-prefixed lookup (current session first)
			if !found && sessionName != "" {
				if info, ok := nodes[sessionName+":"+nodeName]; ok {
					nodeInfo = info
					found = true
				}
			}
			// Phase 3: generic suffix scan (fallback for other sessions)
			if !found {
				for key, info := range nodes {
					parts := strings.SplitN(key, ":", 2)
					if len(parts) == 2 && parts[1] == nodeName {
						nodeInfo = info
						found = true
						break
					}
				}
			}
			if found && reminderMessage != "" {
				vars := map[string]string{
					"node":  nodeName,
					"count": strconv.Itoa(count),
				}
				timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
				content := template.ExpandTemplate(reminderMessage, vars, timeout)

				paneIDForProbe := nodeInfo.PaneID
				enterCount := notification.ResolveEnterCount(
					cfg.Nodes[nodeName].EnterCount,
					func() (string, error) {
						out, err := exec.Command("tmux", "display-message", "-t",
							paneIDForProbe, "-p", "#{pane_current_command}").Output()
						return strings.TrimSpace(string(out)), err
					},
				)
				enterDelay := time.Duration(cfg.EnterDelay * float64(time.Second))
				if nodeEnterDelay := cfg.Nodes[nodeName].EnterDelay; nodeEnterDelay != 0 {
					enterDelay = time.Duration(nodeEnterDelay * float64(time.Second))
				}
				_ = notification.SendToPane(nodeInfo.PaneID, content, enterDelay, timeout, enterCount)
			}
			// Reset counter after sending reminder
			r.counters[nodeName] = 0
		}
	}
}
