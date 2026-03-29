package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

// RunGetContextID prints the live context ID for the current tmux session.
func RunGetContextID(stdout io.Writer, sessionName, configPath string, jsonOut bool) error {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	if sessionName == "" {
		sessionName = config.GetTmuxSessionName()
	}
	if sessionName == "" {
		return fmt.Errorf("--session is required (or run inside tmux)")
	}
	sessionName = filepath.Base(sessionName)

	contextID, err := config.ResolveContextIDFromSession(baseDir, sessionName)
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(stdout).Encode(struct {
			ContextID string `json:"context_id"`
		}{ContextID: contextID})
	}
	_, err = fmt.Fprintln(stdout, contextID)
	return err
}
