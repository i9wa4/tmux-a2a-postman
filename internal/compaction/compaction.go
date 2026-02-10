package compaction

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

// safeGo starts a goroutine with panic recovery (Issue #57).
func safeGo(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				log.Printf("🚨 PANIC in goroutine %q: %v\n%s\n", name, r, string(stack))
			}
		}()
		fn()
	}()
}

// CompactionTracker manages compaction detection state (Issue #71).
type CompactionTracker struct {
	compactionDetected map[string]time.Time
	mu                 sync.Mutex
}

// NewCompactionTracker creates a new CompactionTracker instance (Issue #71).
func NewCompactionTracker() *CompactionTracker {
	return &CompactionTracker{
		compactionDetected: make(map[string]time.Time),
	}
}

// StartCompactionCheck starts a goroutine that periodically checks for compaction events (Issue #71).
func (ct *CompactionTracker) StartCompactionCheck(ctx context.Context, cfg *config.Config, nodes map[string]discovery.NodeInfo, sessionDir string) {
	if !cfg.CompactionDetection.Enabled {
		return
	}

	ticker := time.NewTicker(5 * time.Second) // Check every 5 seconds
	safeGo("compaction-monitor", func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ct.checkAllNodesForCompaction(cfg, nodes, sessionDir)
			}
		}
	})
}

// checkAllNodesForCompaction checks all nodes for compaction events (Issue #71).
func (ct *CompactionTracker) checkAllNodesForCompaction(cfg *config.Config, nodes map[string]discovery.NodeInfo, sessionDir string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	for nodeName, nodeInfo := range nodes {
		// Capture pane output (last 10 lines only)
		output, err := capturePaneOutput(nodeInfo.PaneID)
		if err != nil {
			// Silent skip - node may not be available
			continue
		}

		// Check for compaction pattern
		if checkForCompaction(output, cfg.CompactionDetection.Pattern) {
			// Check if already notified recently (avoid duplicate notifications)
			if lastDetected, ok := ct.compactionDetected[nodeName]; ok {
				if time.Since(lastDetected) < 30*time.Second {
					continue // Skip duplicate notification within 30 seconds
				}
			}

			// Update detection timestamp
			ct.compactionDetected[nodeName] = time.Now()
		}
	}
}

// capturePaneOutput captures the last 10 lines from a tmux pane.
// Returns empty string if capture fails (e.g., pane doesn't exist).
func capturePaneOutput(paneID string) (string, error) {
	cmd := exec.Command("tmux", "capture-pane", "-t", paneID, "-p", "-S", "-10")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane failed: %w", err)
	}
	return string(output), nil
}

// checkForCompaction checks if the output contains the compaction pattern.
func checkForCompaction(output, pattern string) bool {
	if pattern == "" {
		return false
	}
	return strings.Contains(output, pattern)
}
