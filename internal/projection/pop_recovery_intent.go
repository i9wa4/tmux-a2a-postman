package projection

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

// popRecoveryIntent is the last-resort durable copy when both journal append
// and inbox rollback fail after an archive verification failure. It lives
// outside the projection-managed roots so a sync cannot delete it.
type popRecoveryIntent struct {
	SessionKey string `json:"session_key"`
	Generation int    `json:"generation"`
	InboxPath  string `json:"inbox_path"`
	Content    string `json:"content"`
}

func popRecoveryIntentDir(sessionDir string) string {
	return filepath.Join(sessionDir, "mailbox-locks", "pop-recovery-intents")
}

// RecordPopRecoveryIntent must be called while the caller holds the mailbox
// roots gate. A later projection sync can restore the inbox from this record.
func RecordPopRecoveryIntent(sessionDir, inboxPath string, content []byte) error {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return fmt.Errorf("recording pop recovery intent: current session state unavailable")
	}
	rel, err := filepath.Rel(sessionDir, inboxPath)
	if err != nil || !validPopRecoveryInboxPath(rel) {
		return fmt.Errorf("recording pop recovery intent: invalid inbox path %q", inboxPath)
	}
	intent := popRecoveryIntent{SessionKey: state.SessionKey, Generation: state.Generation, InboxPath: rel, Content: string(content)}
	data, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	dir := popRecoveryIntentDir(sessionDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("invalid pop recovery intent directory: %v", err)
	}
	file, err := os.CreateTemp(dir, ".intent-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	name := fmt.Sprintf("%x.json", sha256.Sum256([]byte(rel)))
	if existing, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
		if string(existing) == string(data) {
			return nil
		}
		return fmt.Errorf("pop recovery intent collision at %s", name)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(file.Name(), filepath.Join(dir, name)); err != nil {
		return err
	}
	return syncPopIntentDir(dir)
}

func syncPopIntentDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func validPopRecoveryInboxPath(path string) bool {
	return isAllowedProjectionPath(path) && strings.HasPrefix(pathKey(path), "inbox"+string(filepath.Separator)) && filepath.Base(path) != "."
}

var removePopRecoveryIntentFile = os.Remove

// overlayPopRecoveryIntents returns intents to retire only after the caller
// successfully materializes the selected projection. Stale, malformed, and
// conflicting sole-copy intents are moved to quarantine, never discarded.
func overlayPopRecoveryIntents(sessionDir string, state journal.SessionState, projected *MailboxProjection) ([]string, error) {
	dir := popRecoveryIntentDir(sessionDir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var retire []string
	for _, entry := range entries {
		isFinal := strings.HasSuffix(entry.Name(), ".json")
		isTemp := strings.HasPrefix(entry.Name(), ".intent-") && strings.HasSuffix(entry.Name(), ".tmp")
		if !isFinal && !isTemp {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if !entry.Type().IsRegular() {
			return nil, fmt.Errorf("non-regular pop recovery intent %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var intent popRecoveryIntent
		decodeErr := json.Unmarshal(data, &intent)
		if decodeErr != nil || !validPopRecoveryInboxPath(intent.InboxPath) || (isFinal && fmt.Sprintf("%x.json", sha256.Sum256([]byte(intent.InboxPath))) != entry.Name()) {
			if err := quarantinePopRecoveryIntent(sessionDir, path); err != nil {
				return nil, fmt.Errorf("quarantining malformed pop recovery intent %s: %w", path, err)
			}
			continue
		}
		if intent.SessionKey != state.SessionKey || intent.Generation != state.Generation {
			if err := quarantinePopRecoveryIntent(sessionDir, path); err != nil {
				return nil, fmt.Errorf("quarantining stale pop recovery intent %s: %w", path, err)
			}
			continue
		}
		filename := filepath.Base(intent.InboxPath)
		if projected.IsDeadLetteredMessage(filename) || projected.IsDeadLetteredSource(filepath.Join("read", filename)) {
			retire = append(retire, path)
			continue
		}
		if _, read := projected.Read[pathKey(filepath.Join("read", filename))]; read {
			retire = append(retire, path)
			continue
		}
		if inbox, exists := projected.Inbox[pathKey(intent.InboxPath)]; exists && inbox.Content != intent.Content {
			if err := quarantinePopRecoveryIntent(sessionDir, path); err != nil {
				return nil, fmt.Errorf("quarantining conflicting pop recovery intent %s: %w", path, err)
			}
			continue
		}
		projected.Inbox[pathKey(intent.InboxPath)] = ProjectedFile{Path: pathKey(intent.InboxPath), Content: intent.Content}
		retire = append(retire, path)
	}
	return retire, nil
}

func retirePopRecoveryIntents(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	for _, path := range paths {
		if err := removePopRecoveryIntentFile(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("retiring pop recovery intent %s: %w", path, err)
		}
	}
	return syncPopIntentDir(filepath.Dir(paths[0]))
}

func quarantinePopRecoveryIntent(sessionDir, path string) error {
	root := filepath.Join(sessionDir, "mailbox-locks", "pop-recovery-quarantine")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("invalid pop recovery quarantine directory: %v", err)
	}
	slot, err := os.MkdirTemp(root, "intent-")
	if err != nil {
		return err
	}
	if err := os.Rename(path, filepath.Join(slot, filepath.Base(path))); err != nil {
		return err
	}
	if err := syncPopIntentDir(slot); err != nil {
		return err
	}
	if err := syncPopIntentDir(root); err != nil {
		return err
	}
	return syncPopIntentDir(filepath.Dir(path))
}
