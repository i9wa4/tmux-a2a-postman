package watchdog

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// PaneActivity holds pane activity information.
type PaneActivity struct {
	PaneID           string
	LastActivityTime time.Time
}

// GetPaneActivities retrieves activity information for all tmux panes.
// Parses output of: tmux list-panes -a -F "#{pane_activity} #{pane_id}"
func GetPaneActivities() ([]PaneActivity, error) {
	cmd := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_activity} #{pane_id}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("listing panes: %w: %s", err, output)
	}

	var activities []PaneActivity
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}

		// Parse "#{pane_activity} #{pane_id}"
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}

		// Parse Unix timestamp (seconds since epoch)
		timestamp, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}

		activities = append(activities, PaneActivity{
			PaneID:           parts[1],
			LastActivityTime: time.Unix(timestamp, 0),
		})
	}

	return activities, nil
}

// CheckIdle checks if a pane has been idle for longer than the threshold.
// Returns true if idle (no activity for idle_threshold_seconds).
func CheckIdle(activity PaneActivity, idleThresholdSeconds float64) bool {
	if idleThresholdSeconds <= 0 {
		return false // Idle detection disabled
	}

	threshold := time.Duration(idleThresholdSeconds * float64(time.Second))
	timeSinceActivity := time.Since(activity.LastActivityTime)
	return timeSinceActivity > threshold
}

// GetIdlePanes returns a list of panes that have been idle for longer than the threshold.
func GetIdlePanes(idleThresholdSeconds float64) ([]PaneActivity, error) {
	activities, err := GetPaneActivities()
	if err != nil {
		return nil, err
	}

	var idlePanes []PaneActivity
	for _, activity := range activities {
		if CheckIdle(activity, idleThresholdSeconds) {
			idlePanes = append(idlePanes, activity)
		}
	}

	return idlePanes, nil
}
