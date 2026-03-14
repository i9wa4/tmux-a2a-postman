package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/template"
)

// GenerateBoilerplateFiles generates boilerplate response files in {session_dir}/boilerplate/.
// Variables: {context_id}, {reply_command}, {session_dir}
func GenerateBoilerplateFiles(sessionDir, contextID string, cfg *Config) error {
	if cfg.BoilerplateHeartbeatOk == "" && cfg.BoilerplateHowToReply == "" {
		return nil
	}

	replyCmd := strings.ReplaceAll(cfg.ReplyCommand, "{context_id}", contextID)
	vars := map[string]string{
		"context_id":    contextID,
		"reply_command": replyCmd,
		"session_dir":   sessionDir,
	}
	timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))

	boilerplateDir := filepath.Join(sessionDir, "boilerplate")
	if err := os.MkdirAll(boilerplateDir, 0o700); err != nil {
		return fmt.Errorf("creating boilerplate directory: %w", err)
	}

	files := map[string]string{
		"heartbeat_ok.md": cfg.BoilerplateHeartbeatOk,
		"how_to_reply.md": cfg.BoilerplateHowToReply,
	}
	for filename, tmpl := range files {
		if tmpl == "" {
			continue
		}
		content := template.ExpandTemplate(tmpl, vars, timeout)
		path := filepath.Join(boilerplateDir, filename)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", filename, err)
		}
	}
	return nil
}
