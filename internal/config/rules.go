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
	if err := os.MkdirAll(templatesDir, 0o700); err != nil {
		log.Printf("postman: WARNING: failed to create templates directory: %v\n", err)
		return
	}

	for nodeName, nodeConfig := range cfg.Nodes {
		if !BoolVal(cfg.GetNodeConfig(nodeName).MaterializeTemplate, true) || nodeConfig.Template == "" {
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
