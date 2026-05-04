package paneutil

import (
	"fmt"
	"os/exec"
)

// CaptureContent captures the visible content of a tmux pane.
// Returns the content as a string, or empty string on error.
func CaptureContent(paneID string) (string, error) {
	cmd := exec.Command("tmux", "capture-pane", "-p", "-t", paneID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("capturing pane %s: %w", paneID, err)
	}
	return string(output), nil
}

// CaptureRecentContent captures visible content plus recent scrollback lines.
func CaptureRecentContent(paneID string, tailLines int) (string, error) {
	if tailLines <= 0 {
		return CaptureContent(paneID)
	}
	cmd := exec.Command("tmux", "capture-pane", "-p", "-t", paneID, "-S", fmt.Sprintf("-%d", tailLines))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("capturing recent pane %s: %w", paneID, err)
	}
	return string(output), nil
}
