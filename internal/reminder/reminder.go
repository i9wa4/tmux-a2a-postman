package reminder

import (
	"os/exec"
	"path/filepath"
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
	mu            sync.Mutex
	counters      map[string]int
	totalCounters map[string]int // cumulative; never resets (Issue #246)
}

// NewReminderState creates a new ReminderState.
func NewReminderState() *ReminderState {
	return &ReminderState{
		counters:      make(map[string]int),
		totalCounters: make(map[string]int),
	}
}

// GetCounts returns a copy of the cumulative read counts per node (Issue #246).
func (r *ReminderState) GetCounts() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make(map[string]int, len(r.totalCounters))
	for k, v := range r.totalCounters {
		result[k] = v
	}
	return result
}

// Increment increments the counter for a node and sends reminder if threshold is reached.
func (r *ReminderState) Increment(nodeName string, sessionName string, nodes map[string]discovery.NodeInfo, cfg *config.Config) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.counters[nodeName]++
	r.totalCounters[nodeName]++
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
				// Resolve node's role template (mirrors notification.go BuildNotification)
				// NOTE: reuses nodeConfig and hasNodeConfig declared at line 40
				nodeTemplate := ""
				if hasNodeConfig {
					nodeTemplate = nodeConfig.Template
				}
				if cfg.CommonTemplate != "" {
					if nodeTemplate != "" {
						nodeTemplate = cfg.CommonTemplate + "\n\n" + nodeTemplate
					} else {
						nodeTemplate = cfg.CommonTemplate
					}
				}
				vars := map[string]string{
					"node":       nodeName,
					"count":      strconv.Itoa(count),
					"template":   nodeTemplate,
					"inbox_path": filepath.Join(nodeInfo.SessionDir, "inbox", nodeName),
				}
				timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
				content := template.ExpandTemplate(reminderMessage, vars, timeout)

				paneIDForProbe := nodeInfo.PaneID
				enterCount := notification.ResolveEnterCount(
					cfg.GetNodeConfig(nodeName).EnterCount,
					func() (string, error) {
						out, err := exec.Command("tmux", "display-message", "-t",
							paneIDForProbe, "-p", "#{pane_current_command}").Output()
						return strings.TrimSpace(string(out)), err
					},
				)
				enterDelay := time.Duration(cfg.EnterDelay * float64(time.Second))
				if nodeEnterDelay := cfg.GetNodeConfig(nodeName).EnterDelay; nodeEnterDelay != 0 {
					enterDelay = time.Duration(nodeEnterDelay * float64(time.Second))
				}
				_ = notification.SendToPane(nodeInfo.PaneID, content, enterDelay, timeout, enterCount, false)
			}
			// Reset counter after sending reminder
			r.counters[nodeName] = 0
		}
	}
}
