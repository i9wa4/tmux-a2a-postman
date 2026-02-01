package main

import (
	"os"
	"path/filepath"
)

// resolveBaseDir returns the base directory for postman sessions.
// Priority:
// 1. POSTMAN_HOME env var (explicit override)
// 2. .postman/ in CWD if it exists (backward compat)
// 3. XDG_STATE_HOME/postman/ (or ~/.local/state/postman/)
// 4. .postman (fallback)
func resolveBaseDir() string {
	// 1. Explicit override
	if v := os.Getenv("POSTMAN_HOME"); v != "" {
		return v
	}
	// 2. Backward compat: .postman/ exists in CWD
	if info, err := os.Stat(".postman"); err == nil && info.IsDir() {
		return ".postman"
	}
	// 3. XDG_STATE_HOME
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			stateHome = filepath.Join(home, ".local", "state")
		}
	}
	if stateHome != "" {
		return filepath.Join(stateHome, "postman")
	}
	// 4. Fallback
	return ".postman"
}

// createSessionDirs creates the session directory structure.
func createSessionDirs(sessionDir string) error {
	dirs := []string{
		filepath.Join(sessionDir, "inbox"),
		filepath.Join(sessionDir, "post"),
		filepath.Join(sessionDir, "draft"),
		filepath.Join(sessionDir, "read"),
		filepath.Join(sessionDir, "dead-letter"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
