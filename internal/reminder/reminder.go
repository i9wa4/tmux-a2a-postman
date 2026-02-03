package reminder

import (
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
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
func (r *ReminderState) Increment(nodeName string, nodes map[string]discovery.NodeInfo, cfg *config.Config) {
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
			nodeInfo, found := nodes[nodeName]
			if found && reminderMessage != "" {
				vars := map[string]string{
					"node":  nodeName,
					"count": strconv.Itoa(count),
				}
				timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
				content := template.ExpandTemplate(reminderMessage, vars, timeout)

				if err := exec.Command("tmux", "send-keys", "-t", nodeInfo.PaneID, content, "Enter").Run(); err != nil {
					_ = err // Suppress unused variable warning
				}
			}
			// Reset counter after sending reminder
			r.counters[nodeName] = 0
		}
	}
}
