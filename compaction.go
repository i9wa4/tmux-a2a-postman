package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Compaction detection state
var (
	compactionDetected     = make(map[string]time.Time) // Last detection time per node
	compactionMutex        sync.Mutex
	stderrMutex            sync.Mutex
)

// startCompactionCheck starts a goroutine that periodically checks for compaction events.
func startCompactionCheck(cfg *Config, nodes map[string]NodeInfo, sessionDir string) {
	if !cfg.CompactionDetection.Enabled {
		return
	}

	ticker := time.NewTicker(5 * time.Second) // Check every 5 seconds
	go func() {
		for range ticker.C {
			checkAllNodesForCompaction(cfg, nodes, sessionDir)
		}
	}()
}

// checkAllNodesForCompaction checks all nodes for compaction events.
func checkAllNodesForCompaction(cfg *Config, nodes map[string]NodeInfo, sessionDir string) {
	compactionMutex.Lock()
	defer compactionMutex.Unlock()

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
			if lastDetected, ok := compactionDetected[nodeName]; ok {
				if time.Since(lastDetected) < 30*time.Second {
					continue // Skip duplicate notification within 30 seconds
				}
			}

			// Log detection (without captured content for privacy)
			stderrMutex.Lock()
			fmt.Fprintf(os.Stderr, "postman: compaction detected for node %s\n", nodeName)
			stderrMutex.Unlock()

			// Update detection timestamp
			compactionDetected[nodeName] = time.Now()

			// Notify observers
			notifyObserversOfCompaction(nodeName, cfg, nodes, sessionDir)
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

// notifyObserversOfCompaction notifies observers subscribed to the affected node.
func notifyObserversOfCompaction(nodeName string, cfg *Config, nodes map[string]NodeInfo, sessionDir string) {
	// Find observers subscribed to this node
	for observerName, nodeConfig := range cfg.Nodes {
		if !nodeConfig.SubscribeDigest {
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
			time.AfterFunc(delay, func() {
				if err := sendCompactionNotification(capturedObserver, capturedNode, cfg, sessionDir); err != nil {
					stderrMutex.Lock()
					fmt.Fprintf(os.Stderr, "postman: compaction notification to %s failed: %v\n", capturedObserver, err)
					stderrMutex.Unlock()
				} else {
					stderrMutex.Lock()
					fmt.Fprintf(os.Stderr, "postman: compaction notification sent to %s (node: %s)\n", capturedObserver, capturedNode)
					stderrMutex.Unlock()
				}
			})
		} else {
			// Send immediately
			if err := sendCompactionNotification(observerName, nodeName, cfg, sessionDir); err != nil {
				stderrMutex.Lock()
				fmt.Fprintf(os.Stderr, "postman: compaction notification to %s failed: %v\n", observerName, err)
				stderrMutex.Unlock()
			} else {
				stderrMutex.Lock()
				fmt.Fprintf(os.Stderr, "postman: compaction notification sent to %s (node: %s)\n", observerName, nodeName)
				stderrMutex.Unlock()
			}
		}
	}
}

// sendCompactionNotification sends a compaction notification message to an observer.
func sendCompactionNotification(observerName, affectedNode string, cfg *Config, sessionDir string) error {
	inboxDir := filepath.Join(sessionDir, "inbox", observerName)
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		return fmt.Errorf("creating inbox directory: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("%s-from-postman-to-%s-compaction-notification.md", timestamp, observerName)
	filePath := filepath.Join(inboxDir, filename)

	// Build message body from template
	messageBody := cfg.CompactionDetection.MessageTemplate.Body
	if messageBody == "" {
		messageBody = fmt.Sprintf("Compaction detected for node %s. Please send status update.", affectedNode)
	}
	messageBody = strings.ReplaceAll(messageBody, "{node}", affectedNode)

	content := fmt.Sprintf("---\nmethod: message/send\nparams:\n  from: postman\n  to: %s\n  timestamp: %s\n  type: compaction-recovery\n---\n\n## Compaction Detected\n\n%s\n",
		observerName, time.Now().Format(time.RFC3339), messageBody)

	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing notification file: %w", err)
	}

	return nil
}
