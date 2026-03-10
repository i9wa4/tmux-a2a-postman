package diplomat

import (
	"os"
	"testing"
	"time"
)

func TestWriteAndLoadRegistration(t *testing.T) {
	baseDir := t.TempDir()
	reg := ContextRegistration{
		ContextID:    "ctx-1",
		SessionName:  "session-a",
		PID:          os.Getpid(),
		DiplomatNode: "diplomat",
		StartedAt:    time.Now().Truncate(time.Second),
	}
	if err := WriteRegistration(baseDir, reg); err != nil {
		t.Fatalf("WriteRegistration: %v", err)
	}

	regs, err := LoadActiveContexts(baseDir)
	if err != nil {
		t.Fatalf("LoadActiveContexts: %v", err)
	}
	if len(regs) != 1 {
		t.Fatalf("expected 1 registration, got %d", len(regs))
	}
	if regs[0].ContextID != "ctx-1" {
		t.Errorf("ContextID: got %q, want %q", regs[0].ContextID, "ctx-1")
	}
	if regs[0].SessionName != "session-a" {
		t.Errorf("SessionName: got %q, want %q", regs[0].SessionName, "session-a")
	}
}

func TestDeleteRegistration(t *testing.T) {
	baseDir := t.TempDir()
	reg := ContextRegistration{
		ContextID:    "ctx-2",
		SessionName:  "session-b",
		PID:          os.Getpid(),
		DiplomatNode: "diplomat",
		StartedAt:    time.Now(),
	}
	if err := WriteRegistration(baseDir, reg); err != nil {
		t.Fatalf("WriteRegistration: %v", err)
	}

	if err := DeleteRegistration(baseDir, "ctx-2"); err != nil {
		t.Fatalf("DeleteRegistration: %v", err)
	}

	regs, err := LoadActiveContexts(baseDir)
	if err != nil {
		t.Fatalf("LoadActiveContexts: %v", err)
	}
	if len(regs) != 0 {
		t.Errorf("expected 0 registrations after delete, got %d", len(regs))
	}
}

func TestLoadActiveContexts_EmptyDir(t *testing.T) {
	baseDir := t.TempDir()
	regs, err := LoadActiveContexts(baseDir)
	if err != nil {
		t.Fatalf("LoadActiveContexts: %v", err)
	}
	if len(regs) != 0 {
		t.Errorf("expected 0 registrations, got %d", len(regs))
	}
}

func TestIsContextAlive_SelfProcess(t *testing.T) {
	reg := ContextRegistration{PID: os.Getpid()}
	if !IsContextAlive(reg) {
		t.Error("current process should be alive")
	}
}
