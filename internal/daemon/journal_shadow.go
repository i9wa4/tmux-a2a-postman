package daemon

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
)

func installShadowJournalManager(sessionDir, contextID, selfSession string, now time.Time) {
	manager := journal.NewManager(contextID, os.Getpid())
	journal.InstallProcessManager(manager)
	if err := manager.Bootstrap(sessionDir, selfSession, now); err != nil {
		log.Printf("postman: WARNING: journal shadow bootstrap failed for %s: %v\n", selfSession, err)
	}
}

func recordShadowMailboxPathEvent(eventPath, eventType string, visibility journal.Visibility, now time.Time) {
	sessionDir, sessionName, ok := shadowSessionFromEventPath(eventPath)
	if !ok {
		return
	}
	info, err := message.ParseMessageFilename(filepath.Base(eventPath))
	if err != nil {
		return
	}
	content, readErr := os.ReadFile(eventPath)
	if eventType == "compatibility_mailbox_posted" {
		if os.IsNotExist(readErr) {
			return
		}
		if readErr == nil && len(content) == 0 {
			return
		}
	}
	if readErr != nil && !os.IsNotExist(readErr) {
		log.Printf("postman: WARNING: failed to read shadow mailbox payload %s: %v\n", filepath.Base(eventPath), readErr)
	}
	payload := compatibilityMailboxPayloadForFile(filepath.Base(eventPath), shadowRelativePath(sessionDir, eventPath), string(content))
	if payload.From == "" {
		payload.From = info.From
	}
	if payload.To == "" {
		payload.To = info.To
	}
	if err := journal.RecordProcessMailboxPayload(
		sessionDir,
		sessionName,
		eventType,
		visibility,
		payload,
		now,
	); err != nil {
		log.Printf("postman: WARNING: journal shadow append failed for %s: %v\n", filepath.Base(eventPath), err)
	}
}

func shadowSessionFromEventPath(eventPath string) (string, string, bool) {
	parentDir := filepath.Dir(filepath.Dir(eventPath))
	sessionName := filepath.Base(parentDir)
	if parentDir == "." || sessionName == "." || sessionName == string(filepath.Separator) {
		return "", "", false
	}
	return parentDir, sessionName, true
}

func shadowRelativePath(sessionDir, fullPath string) string {
	rel, err := filepath.Rel(sessionDir, fullPath)
	if err != nil {
		return filepath.Base(fullPath)
	}
	return rel
}
