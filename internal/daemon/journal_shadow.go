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
	if readErr != nil && !os.IsNotExist(readErr) {
		log.Printf("postman: WARNING: failed to read shadow mailbox payload %s: %v\n", filepath.Base(eventPath), readErr)
	}
	if err := journal.RecordProcessMailboxPayload(
		sessionDir,
		sessionName,
		eventType,
		visibility,
		journal.MailboxEventPayload{
			MessageID: filepath.Base(eventPath),
			From:      info.From,
			To:        info.To,
			Path:      shadowRelativePath(sessionDir, eventPath),
			Content:   string(content),
		},
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
