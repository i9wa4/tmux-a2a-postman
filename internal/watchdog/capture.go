package watchdog

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

// CaptureConfig holds configuration for pane capture.
type CaptureConfig struct {
	Enabled   bool
	MaxFiles  int
	MaxBytes  int
	TailLines int
}

// CapturePane captures the content of a tmux pane and saves it to a file.
// Returns the path to the created capture file.
func CapturePane(paneID, captureDir string, tailLines int) (string, error) {
	// Ensure capture directory exists
	if err := os.MkdirAll(captureDir, 0o755); err != nil {
		return "", fmt.Errorf("creating capture directory: %w", err)
	}

	// Generate filename with timestamp
	now := time.Now()
	filename := fmt.Sprintf("%s.log", now.Format("20060102-150405"))
	capturePath := filepath.Join(captureDir, filename)

	// Capture pane content using tmux
	var cmd *exec.Cmd
	if tailLines > 0 {
		// Capture only last N lines
		cmd = exec.Command("tmux", "capture-pane", "-t", paneID, "-p", "-S", fmt.Sprintf("-%d", tailLines))
	} else {
		// Capture entire pane history
		cmd = exec.Command("tmux", "capture-pane", "-t", paneID, "-p")
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("capturing pane: %w: %s", err, output)
	}

	// Write captured content to file
	if err := os.WriteFile(capturePath, output, 0o644); err != nil {
		return "", fmt.Errorf("writing capture file: %w", err)
	}

	return capturePath, nil
}

// RotateCaptures removes old capture files based on max_files and max_bytes retention policy.
func RotateCaptures(captureDir string, maxFiles int, maxBytes int64) error {
	// List all capture files
	entries, err := os.ReadDir(captureDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Directory doesn't exist yet
		}
		return fmt.Errorf("reading capture directory: %w", err)
	}

	// Collect file info
	type fileInfo struct {
		name    string
		modTime time.Time
		size    int64
	}
	var files []fileInfo
	var totalSize int64

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{
			name:    entry.Name(),
			modTime: info.ModTime(),
			size:    info.Size(),
		})
		totalSize += info.Size()
	}

	// Sort by modification time (oldest first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})

	// Remove files if exceeding max_files
	if maxFiles > 0 && len(files) > maxFiles {
		for i := 0; i < len(files)-maxFiles; i++ {
			path := filepath.Join(captureDir, files[i].name)
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("removing old capture file: %w", err)
			}
			totalSize -= files[i].size
		}
		// Update files slice
		files = files[len(files)-maxFiles:]
	}

	// Remove files if exceeding max_bytes
	if maxBytes > 0 && totalSize > maxBytes {
		for i := 0; i < len(files) && totalSize > maxBytes; i++ {
			path := filepath.Join(captureDir, files[i].name)
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("removing old capture file: %w", err)
			}
			totalSize -= files[i].size
		}
	}

	return nil
}
