package diplomat

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"
)

// StartDiplomatCleanup starts a goroutine that periodically removes stale context
// registrations. onStaleRemoved is called (with the contextID) after each
// successful removal so the caller can emit a TUI event without creating a
// circular import between the diplomat and tui packages.
// Issue #165 Task 4.
func StartDiplomatCleanup(
	ctx context.Context,
	baseDir string,
	intervalSeconds float64,
	onStaleRemoved func(contextID string),
) {
	interval := time.Duration(intervalSeconds * float64(time.Second))
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				regs, err := LoadActiveContexts(baseDir)
				if err != nil {
					log.Printf("diplomat cleanup: LoadActiveContexts error: %v\n", err)
					continue
				}
				for _, reg := range regs {
					if IsContextAlive(reg) {
						continue
					}
					jsonFile := filepath.Join(baseDir, "diplomat", "active-contexts", reg.ContextID+".json")
					if err := os.Remove(jsonFile); err != nil {
						if !os.IsNotExist(err) {
							log.Printf("diplomat cleanup: remove %s: %v\n", jsonFile, err)
						}
					} else {
						if onStaleRemoved != nil {
							onStaleRemoved(reg.ContextID)
						}
					}
				}
			}
		}
	}()
}
