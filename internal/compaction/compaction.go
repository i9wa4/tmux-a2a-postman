package compaction

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
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

// safeAfterFunc wraps time.AfterFunc with panic recovery (Issue #57).
func safeAfterFunc(d time.Duration, name string, fn func()) *time.Timer {
	return time.AfterFunc(d, func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				log.Printf("🚨 PANIC in timer callback %q: %v\n%s\n", name, r, string(stack))
			}
		}()
		fn()
	})
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

			// Notify observers
			ct.notifyObserversOfCompaction(nodeName, cfg, nodes, sessionDir)
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

// notifyObserversOfCompaction notifies observers subscribed to the affected node (Issue #71).
func (ct *CompactionTracker) notifyObserversOfCompaction(nodeName string, cfg *config.Config, nodes map[string]discovery.NodeInfo, sessionDir string) {
	// Find observers subscribed to this node
	for observerName, nodeConfig := range cfg.Nodes {
		if len(nodeConfig.Observes) == 0 {
			continue
		}

		// Check if this observer is subscribed to the affected node
		observes := false
		for _, observedNode := range nodeConfig.Observes {
			if observedNode == nodeName {
				observes = true
				break
			}
		}

		if !observes {
			continue
		}

		// Send notification to observer with delay if configured
		if cfg.CompactionDetection.DelaySeconds > 0 {
			delay := time.Duration(cfg.CompactionDetection.DelaySeconds) * time.Second
			capturedObserver := observerName
			capturedNode := nodeName
			safeAfterFunc(delay, "delayed-notification", func() {
				if err := ct.sendCompactionNotification(capturedObserver, capturedNode, cfg, sessionDir); err != nil {
					_ = err // Suppress unused variable warning
				}
			})
		} else {
			// Send immediately
			if err := ct.sendCompactionNotification(observerName, nodeName, cfg, sessionDir); err != nil {
				_ = err // Suppress unused variable warning
			}
		}
	}
}

// sendCompactionNotification sends a compaction notification message to an observer (Issue #71).
func (ct *CompactionTracker) sendCompactionNotification(observerName, affectedNode string, cfg *config.Config, sessionDir string) error {
	inboxDir := filepath.Join(sessionDir, "inbox", observerName)
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		return fmt.Errorf("creating inbox directory: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("%s-from-postman-to-%s-compaction-notification.md", timestamp, observerName)
	filePath := filepath.Join(inboxDir, filename)

	// Build message body from template
	// Issue #82: Use configurable template for compaction body
	messageBody := cfg.CompactionDetection.MessageTemplate.Body
	if messageBody == "" {
		messageBody = cfg.CompactionBodyTemplate
		if messageBody == "" {
			messageBody = "Compaction detected for node {node}. Please send status update."
		}
	}
	messageBody = strings.ReplaceAll(messageBody, "{node}", affectedNode)

	// Issue #82: Use configurable template for compaction header
	headerTemplate := cfg.CompactionHeaderTemplate
	if headerTemplate == "" {
		headerTemplate = "## Compaction Detected"
	}
	timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
	vars := map[string]string{
		"node": affectedNode,
	}
	header := template.ExpandTemplate(headerTemplate, vars, timeout)

	content := fmt.Sprintf("---\nmethod: message/send\nparams:\n  from: postman\n  to: %s\n  timestamp: %s\n  type: compaction-recovery\n---\n\n%s\n\n%s\n",
		observerName, time.Now().Format(time.RFC3339), header, messageBody)

	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing notification file: %w", err)
	}

	return nil
}
