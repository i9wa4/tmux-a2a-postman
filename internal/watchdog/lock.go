package watchdog

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// WatchdogLock provides flock-based exclusive locking for watchdog with PID validation.
type WatchdogLock struct {
	file *os.File
	path string
}

// AcquireLock acquires an exclusive non-blocking lock on the given path.
// Returns an error if the lock is already held by another process.
// Writes current PID to the lock file for validation.
func AcquireLock(path string) (*WatchdogLock, error) {
	// Expand environment variables in path
	path = os.ExpandEnv(path)

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating lock directory: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}

	// Try to acquire exclusive lock (non-blocking)
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		// Lock failed - check if PID is stale
		pidBytes, readErr := os.ReadFile(path)
		if readErr == nil {
			pidStr := strings.TrimSpace(string(pidBytes))
			if pid, parseErr := strconv.Atoi(pidStr); parseErr == nil {
				// Check if process exists
				if err := syscall.Kill(pid, 0); err != nil {
					// Process doesn't exist - stale lock, force acquire
					_ = f.Close()
					_ = os.Remove(path)
					// Retry lock acquisition
					return AcquireLock(path)
				}
			}
		}
		_ = f.Close()
		return nil, fmt.Errorf("lock already held: %w", err)
	}

	// Write current PID to lock file
	pid := os.Getpid()
	if err := f.Truncate(0); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, fmt.Errorf("truncating lock file: %w", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, fmt.Errorf("seeking lock file: %w", err)
	}
	if _, err := fmt.Fprintf(f, "%d\n", pid); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, fmt.Errorf("writing PID to lock file: %w", err)
	}

	return &WatchdogLock{file: f, path: path}, nil
}

// ReleaseLock releases the file lock and closes the lock file.
func (l *WatchdogLock) ReleaseLock() error {
	if l.file == nil {
		return nil
	}
	if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil {
		return err
	}
	if err := l.file.Close(); err != nil {
		return err
	}
	// Remove lock file after release
	return os.Remove(l.path)
}
