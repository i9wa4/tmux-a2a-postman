package watchdog

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// paneActivityExport mirrors idle.PaneActivityExport for local JSON parsing.
// Defined locally to avoid importing internal/idle (separation of concerns).
type paneActivityExport struct {
	Status       string    `json:"status"`
	LastChangeAt time.Time `json:"lastChangeAt"`
}

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

		// Skip panes with activity timestamp of 0 (never active or just created)
		if timestamp == 0 {
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

// GetIdlePanesFromActivityFile reads pane activity from a pane-activity.json file
// and returns panes with status "idle".
// Issue #123: Supports both legacy map[string]string and new map[string]PaneActivityExport formats.
// Returns empty slice (not error) on: file missing, stale, parse error, schema mismatch.
func GetIdlePanesFromActivityFile(path string) ([]PaneActivity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return []PaneActivity{}, nil // file missing or unreadable
	}

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawMap); err != nil {
		return []PaneActivity{}, nil // parse error
	}

	var result []PaneActivity
	for paneID, raw := range rawMap {
		var status string
		var lastChangeAt time.Time

		// Try legacy format: plain string value
		if err := json.Unmarshal(raw, &status); err != nil {
			// Try new format: paneActivityExport struct
			var export paneActivityExport
			if err := json.Unmarshal(raw, &export); err != nil {
				continue // schema mismatch, skip entry
			}
			status = export.Status
			lastChangeAt = export.LastChangeAt
		}

		if status == "idle" {
			result = append(result, PaneActivity{
				PaneID:           paneID,
				LastActivityTime: lastChangeAt,
			})
		}
	}

	if result == nil {
		return []PaneActivity{}, nil
	}
	return result, nil
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
