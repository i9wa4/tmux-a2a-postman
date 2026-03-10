package diplomat

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ContextRegistration represents a diplomat context's registration info.
// Issue #163 Task 4: extends Phase 1 types with session-aware registration.
type ContextRegistration struct {
	ContextID    string    `json:"context_id"`
	SessionName  string    `json:"session_name"`
	PID          int       `json:"pid"`
	DiplomatNode string    `json:"diplomat_node"`
	StartedAt    time.Time `json:"started_at"`
}

// WriteRegistration writes a context registration to the active-contexts directory.
// Path: {baseDir}/diplomat/active-contexts/{contextID}.json
func WriteRegistration(baseDir string, reg ContextRegistration) error {
	dir := filepath.Join(baseDir, "diplomat", "active-contexts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating active-contexts dir: %w", err)
	}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling registration: %w", err)
	}
	path := filepath.Join(dir, reg.ContextID+".json")
	return os.WriteFile(path, data, 0o600)
}

// DeleteRegistration removes a context registration file.
func DeleteRegistration(baseDir, contextID string) error {
	path := filepath.Join(baseDir, "diplomat", "active-contexts", contextID+".json")
	return os.Remove(path)
}

// LoadActiveContexts reads all context registrations from the active-contexts directory.
func LoadActiveContexts(baseDir string) ([]ContextRegistration, error) {
	dir := filepath.Join(baseDir, "diplomat", "active-contexts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var regs []ContextRegistration
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var reg ContextRegistration
		if err := json.Unmarshal(data, &reg); err != nil {
			continue
		}
		regs = append(regs, reg)
	}
	return regs, nil
}

// IsContextAlive checks if a registered context's process is still running.
func IsContextAlive(reg ContextRegistration) bool {
	if reg.PID <= 0 {
		return false
	}
	proc, err := os.FindProcess(reg.PID)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
