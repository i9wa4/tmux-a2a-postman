package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateRulesFile_EmptyTemplate(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	cfg := &Config{
		RulesTemplate: "", // Empty template
		TmuxTimeout:   5.0,
	}

	err := GenerateRulesFile(sessionDir, "test-ctx", cfg)
	if err != nil {
		t.Fatalf("GenerateRulesFile() error = %v, want nil", err)
	}

	rulesPath := filepath.Join(sessionDir, "RULES.md")
	if _, err := os.Stat(rulesPath); !os.IsNotExist(err) {
		t.Errorf("RULES.md should not be generated when template is empty")
	}
}

func TestGenerateRulesFile_WithVariables(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	cfg := &Config{
		RulesTemplate: "Context: {context_id}\nSession: {session_dir}\nReply: {reply_command}",
		ReplyCommand:  "postman create-draft --to <recipient>",
		TmuxTimeout:   5.0,
	}

	err := GenerateRulesFile(sessionDir, "test-ctx-123", cfg)
	if err != nil {
		t.Fatalf("GenerateRulesFile() error = %v", err)
	}

	rulesPath := filepath.Join(sessionDir, "RULES.md")
	content, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("failed to read RULES.md: %v", err)
	}

	contentStr := string(content)

	// Verify variables are expanded
	if !strings.Contains(contentStr, "Context: test-ctx-123") {
		t.Errorf("RULES.md should contain expanded context_id, got: %s", contentStr)
	}
	if !strings.Contains(contentStr, "Session: "+sessionDir) {
		t.Errorf("RULES.md should contain expanded session_dir, got: %s", contentStr)
	}
	if !strings.Contains(contentStr, "Reply: postman create-draft --to <recipient>") {
		t.Errorf("RULES.md should contain expanded reply_command, got: %s", contentStr)
	}
}

func TestGenerateRulesFile_ReplyCommandWithContextID(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	cfg := &Config{
		RulesTemplate: "Run: {reply_command}",
		ReplyCommand:  "postman create-draft --context-id {context_id} --to <recipient>",
		TmuxTimeout:   5.0,
	}

	err := GenerateRulesFile(sessionDir, "test-ctx-456", cfg)
	if err != nil {
		t.Fatalf("GenerateRulesFile() error = %v", err)
	}

	rulesPath := filepath.Join(sessionDir, "RULES.md")
	content, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("failed to read RULES.md: %v", err)
	}

	contentStr := string(content)

	// Verify {context_id} in reply_command is expanded
	expected := "Run: postman create-draft --context-id test-ctx-456 --to <recipient>"
	if !strings.Contains(contentStr, expected) {
		t.Errorf("RULES.md should contain fully expanded reply_command with context_id\ngot: %s\nwant to contain: %s", contentStr, expected)
	}

	// Ensure no leftover {context_id} placeholders
	if strings.Contains(contentStr, "{context_id}") {
		t.Errorf("RULES.md should not contain unexpanded {context_id}, got: %s", contentStr)
	}
}
