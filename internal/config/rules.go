package config

import (
	"fmt"
	"log"
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

// GenerateBoilerplateFiles generates boilerplate response files in {session_dir}/boilerplate/.
// Variables: {context_id}, {reply_command}, {session_dir}
func GenerateBoilerplateFiles(sessionDir, contextID string, cfg *Config) error {
	if cfg.BoilerplatePong == "" && cfg.BoilerplateHeartbeatOk == "" && cfg.BoilerplateHowToReply == "" {
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
	if err := os.MkdirAll(boilerplateDir, 0o755); err != nil {
		return fmt.Errorf("creating boilerplate directory: %w", err)
	}

	files := map[string]string{
		"pong.md":         cfg.BoilerplatePong,
		"heartbeat_ok.md": cfg.BoilerplateHeartbeatOk,
		"how_to_reply.md": cfg.BoilerplateHowToReply,
	}
	for filename, tmpl := range files {
		if tmpl == "" {
			continue
		}
		content := template.ExpandTemplate(tmpl, vars, timeout)
		path := filepath.Join(boilerplateDir, filename)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", filename, err)
		}
	}
	return nil
}

// sanitizeNodeName replaces characters outside [a-zA-Z0-9_-] with '_' (Issue #134).
func sanitizeNodeName(name string) string {
	b := make([]byte, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			b[i] = c
		} else {
			b[i] = '_'
		}
	}
	return string(b)
}

// MaterializeNodeTemplates writes per-node template content as state files (Issue #134).
// Files are written atomically to contextDir/templates/<sanitized-node>.md.
// cfg.MaterializedPaths is populated with nodeName -> filePath for successful writes.
// Failures are logged but non-fatal; message delivery falls back to inline template.
func MaterializeNodeTemplates(baseDir, contextID string, cfg *Config) {
	cfg.MaterializedPaths = make(map[string]string)

	contextDir := filepath.Join(baseDir, contextID)
	templatesDir := filepath.Join(contextDir, "templates")
	if err := os.MkdirAll(templatesDir, 0o755); err != nil {
		log.Printf("postman: WARNING: failed to create templates directory: %v\n", err)
		return
	}

	for nodeName, nodeConfig := range cfg.Nodes {
		if !cfg.GetNodeConfig(nodeName).MaterializeTemplate || nodeConfig.Template == "" {
			continue
		}
		sanitized := sanitizeNodeName(nodeName)
		filePath := filepath.Join(templatesDir, sanitized+".md")
		tmpPath := filePath + ".tmp"
		fileContent := "<!-- role template: " + nodeName + " -->\n\n" + nodeConfig.Template
		if err := os.WriteFile(tmpPath, []byte(fileContent), 0o600); err != nil {
			log.Printf("postman: WARNING: failed to write template tmp for node %s: %v\n", nodeName, err)
			continue
		}
		if err := os.Rename(tmpPath, filePath); err != nil {
			log.Printf("postman: WARNING: failed to rename template file for node %s: %v\n", nodeName, err)
			continue
		}
		cfg.MaterializedPaths[nodeName] = filePath
	}
}
