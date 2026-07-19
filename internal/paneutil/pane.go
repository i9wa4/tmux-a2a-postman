package paneutil

import (
	"context"
	"fmt"

	"github.com/i9wa4/tmux-a2a-postman/internal/multiplexer"
	"github.com/i9wa4/tmux-a2a-postman/internal/tmuxrunner"
)

type tmuxCombinedOutputFunc func(args ...string) ([]byte, error)

func runTmuxCombinedOutput(args ...string) ([]byte, error) {
	return tmuxrunner.CombinedOutput(args...)
}

// CaptureContent captures the visible content of a tmux pane.
// Returns the content as a string, or empty string on error.
func CaptureContent(paneID string) (string, error) {
	return captureContentWithBackend(multiplexer.TmuxBackend{}, paneID, multiplexer.CaptureOptions{})
}

// CaptureRecentContent captures visible content plus recent scrollback lines.
func CaptureRecentContent(paneID string, tailLines int) (string, error) {
	return captureContentWithBackend(multiplexer.TmuxBackend{}, paneID, multiplexer.CaptureOptions{TailLines: tailLines})
}

// CaptureHistoryContent captures visible content plus all retained scrollback.
func CaptureHistoryContent(paneID string) (string, error) {
	return captureContentWithBackend(multiplexer.TmuxBackend{}, paneID, multiplexer.CaptureOptions{History: true})
}

func captureContentWithBackend(backend multiplexer.PaneBackend, paneID string, opts multiplexer.CaptureOptions) (string, error) {
	content, err := backend.CapturePane(context.Background(), multiplexer.TmuxPaneID(paneID), opts)
	if err != nil {
		if opts.History {
			return "", fmt.Errorf("capturing pane %s history: %w", paneID, err)
		}
		return "", fmt.Errorf("capturing pane %s: %w", paneID, err)
	}
	return content, nil
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
