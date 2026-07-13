package paneutil

import (
	"fmt"
	"os/exec"
)

type tmuxCombinedOutputFunc func(args ...string) ([]byte, error)

func runTmuxCombinedOutput(args ...string) ([]byte, error) {
	return exec.Command("tmux", args...).CombinedOutput()
}

// CaptureContent captures the visible content of a tmux pane.
// Returns the content as a string, or empty string on error.
func CaptureContent(paneID string) (string, error) {
	return captureContent(paneID, 0, runTmuxCombinedOutput)
}

// CaptureRecentContent captures visible content plus recent scrollback lines.
func CaptureRecentContent(paneID string, tailLines int) (string, error) {
	return captureContent(paneID, tailLines, runTmuxCombinedOutput)
}

// CaptureHistoryContent captures visible content plus all retained scrollback.
func CaptureHistoryContent(paneID string) (string, error) {
	return captureHistoryContent(paneID, runTmuxCombinedOutput)
}

func captureContent(paneID string, tailLines int, run tmuxCombinedOutputFunc) (string, error) {
	args := []string{"capture-pane", "-p", "-t", paneID}
	if tailLines > 0 {
		args = append(args, "-S", fmt.Sprintf("-%d", tailLines))
	}
	output, err := run(args...)
	if err != nil {
		return "", fmt.Errorf("capturing pane %s: %w", paneID, err)
	}
	return string(output), nil
}

func captureHistoryContent(paneID string, run tmuxCombinedOutputFunc) (string, error) {
	output, err := run("capture-pane", "-p", "-t", paneID, "-S", "-")
	if err != nil {
		return "", fmt.Errorf("capturing pane %s history: %w", paneID, err)
	}
	return string(output), nil
}
