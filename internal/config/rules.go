package config

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/template"
)

// GenerateRulesFile generates RULES.md in the session directory (Issue #75).
// If rulesTemplate is empty, no file is generated.
// Variables: {context_id}, {reply_command}, {session_dir}
func GenerateRulesFile(sessionDir, contextID string, cfg *Config) error {
	if cfg.RulesTemplate == "" {
		return nil // No template configured, skip generation
	}

	// Issue #75: Pre-expand {context_id} in reply_command to prevent nested variable issues
	replyCmd := strings.ReplaceAll(cfg.ReplyCommand, "{context_id}", contextID)

	vars := map[string]string{
		"context_id":    contextID,
		"reply_command": replyCmd,
		"session_dir":   sessionDir,
	}

	timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
	content := template.ExpandTemplate(cfg.RulesTemplate, vars, timeout)

	rulesPath := filepath.Join(sessionDir, "RULES.md")
	return os.WriteFile(rulesPath, []byte(content), 0o644)
}
