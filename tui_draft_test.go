package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDualMode_NoArgs(t *testing.T) {
	t.Skip("Requires process isolation - deferred to E2E testing")
}

func TestDualMode_Start(t *testing.T) {
	t.Skip("Requires process isolation - deferred to E2E testing")
}

func TestDualMode_TUIFlag(t *testing.T) {
	t.Skip("Requires process isolation - deferred to E2E testing")
}

func TestTUI_CreateDraft_Submit(t *testing.T) {
	// Create temporary session directory
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")
	draftDir := filepath.Join(sessionDir, "draft")
	if err := os.MkdirAll(draftDir, 0o755); err != nil {
		t.Fatalf("creating session dir: %v", err)
	}

	nodes := map[string]string{
		"worker":       "worker-pane",
		"orchestrator": "orchestrator-pane",
	}

	m := InitialDraftModel(sessionDir, "test-context", "worker", nodes)

	// Verify initial state
	if m.mode != DraftModeSelectRecipient {
		t.Errorf("initial mode: got %v, want %v", m.mode, DraftModeSelectRecipient)
	}

	// Simulate recipient selection
	m.selectedNode = "orchestrator"
	m.mode = DraftModeInputMessage

	// Simulate message input
	m.messageBody = "Test message"
	m.mode = DraftModePreview

	// Submit draft
	if err := m.submitDraft(); err != nil {
		t.Fatalf("submitDraft failed: %v", err)
	}

	// Verify draft file was created
	entries, err := os.ReadDir(draftDir)
	if err != nil {
		t.Fatalf("reading draft dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("draft files: got %d, want 1", len(entries))
	}

	// Verify draft content
	draftPath := filepath.Join(draftDir, entries[0].Name())
	content, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatalf("reading draft: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "from: worker") {
		t.Error("draft missing from field")
	}
	if !strings.Contains(contentStr, "to: orchestrator") {
		t.Error("draft missing to field")
	}
	if !strings.Contains(contentStr, "Test message") {
		t.Error("draft missing message body")
	}
}

func TestContextID_Fallback(t *testing.T) {
	tmpDir := t.TempDir()

	// Test 1: Explicit ID (highest priority)
	contextID, source, err := resolveContextID("explicit-id", tmpDir)
	if err != nil {
		t.Fatalf("resolveContextID with explicit: %v", err)
	}
	if contextID != "explicit-id" {
		t.Errorf("explicit ID: got %q, want %q", contextID, "explicit-id")
	}
	if source != "flag" {
		t.Errorf("source: got %q, want %q", source, "flag")
	}

	// Test 2: A2A_CONTEXT_ID env
	_ = os.Setenv("A2A_CONTEXT_ID", "env-id")
	defer func() { _ = os.Unsetenv("A2A_CONTEXT_ID") }()

	contextID, source, err = resolveContextID("", tmpDir)
	if err != nil {
		t.Fatalf("resolveContextID with env: %v", err)
	}
	if contextID != "env-id" {
		t.Errorf("env ID: got %q, want %q", contextID, "env-id")
	}
	if source != "env:A2A_CONTEXT_ID" {
		t.Errorf("source: got %q, want %q", source, "env:A2A_CONTEXT_ID")
	}

	_ = os.Unsetenv("A2A_CONTEXT_ID")

	// Test 3: No fallback available
	_, _, err = resolveContextID("", tmpDir)
	if err == nil {
		t.Error("expected error when no context ID available")
	}
}

func TestTUI_Draft_Navigation(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")

	nodes := map[string]string{
		"worker":       "worker-pane",
		"orchestrator": "orchestrator-pane",
	}

	m := InitialDraftModel(sessionDir, "test-context", "worker", nodes)

	// Test recipient selection
	if m.mode != DraftModeSelectRecipient {
		t.Errorf("initial mode: got %v, want %v", m.mode, DraftModeSelectRecipient)
	}

	// Simulate Enter key (select recipient)
	// NOTE: Full key simulation requires list model state setup, skip for unit test

	// Test ESC in message input mode (back to recipient)
	m.mode = DraftModeInputMessage
	msg := tea.KeyMsg{Type: tea.KeyEsc}
	newModel, _ := m.Update(msg)
	m = newModel.(DraftModel)
	if m.mode != DraftModeSelectRecipient {
		t.Errorf("ESC from InputMessage: got mode %v, want %v", m.mode, DraftModeSelectRecipient)
	}

	// Test quit
	msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}
	newModel, _ = m.Update(msg)
	m = newModel.(DraftModel)
	if !m.quitting {
		t.Error("q key did not set quitting flag")
	}
}
