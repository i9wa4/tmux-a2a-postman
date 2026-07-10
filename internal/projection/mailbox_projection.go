package projection

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
)

type ProjectedFile struct {
	Path    string
	Content string
}

type MailboxProjection struct {
	Post           map[string]ProjectedFile
	Inbox          map[string]ProjectedFile
	Read           map[string]ProjectedFile
	DeadLetter     map[string]ProjectedFile
	managedPost    map[string]bool
	tombstonedRead map[string]bool
}

type mailboxProjectionMarker struct {
	SessionKey string `json:"session_key"`
	Generation int    `json:"generation"`
}

const (
	MailboxProjectionComponent             = "mailbox-projection"
	MailboxProjectionPostedEventType       = "mailbox_projection_posted"
	MailboxProjectionPostConsumedEventType = "mailbox_projection_post_consumed"
	MailboxProjectionDeliveredEventType    = "mailbox_projection_delivered"
	MailboxProjectionReadEventType         = "mailbox_projection_read"
	MailboxProjectionDeadLetteredEventType = "mailbox_projection_dead_lettered"
)

var mailboxProjectionRoots = []string{"post", "inbox", "read", "dead-letter"}

func ProjectMailboxProjection(sessionDir string) (MailboxProjection, bool, error) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return MailboxProjection{}, false, nil
	}

	events, err := journal.Replay(sessionDir)
	if err != nil {
		return MailboxProjection{}, false, err
	}
	if len(events) == 0 {
		return MailboxProjection{}, false, nil
	}

	projected := MailboxProjection{
		Post:           make(map[string]ProjectedFile),
		Inbox:          make(map[string]ProjectedFile),
		Read:           make(map[string]ProjectedFile),
		DeadLetter:     make(map[string]ProjectedFile),
		managedPost:    make(map[string]bool),
		tombstonedRead: make(map[string]bool),
	}
	sawLease := false
	sawResolution := false

	for _, event := range events {
		if event.SessionKey != state.SessionKey || event.Generation != state.Generation {
			continue
		}

		switch event.Type {
		case "lease_acquired":
			sawLease = true
		case "session_resolved":
			sawResolution = true
		}

		if event.Visibility == journal.VisibilityControlPlaneOnly {
			continue
		}

		payload, ok := decodeMailboxEventPayload(event.Payload)
		if !ok {
			return MailboxProjection{}, false, fmt.Errorf("decode mailbox payload for %s", event.Type)
		}

		switch event.Type {
		case MailboxProjectionPostedEventType:
			if !setProjectedFile(projected.Post, payload.Path, payload.Content) {
				return MailboxProjection{}, false, fmt.Errorf("invalid post path %q", payload.Path)
			}
			rememberManagedPost(projected.managedPost, payload.Path)
		case MailboxProjectionPostConsumedEventType:
			rememberManagedPost(projected.managedPost, payload.Path)
			delete(projected.Post, pathKey(payload.Path))
		case MailboxProjectionDeliveredEventType:
			if !setProjectedFile(projected.Inbox, inboxPathFromPayload(payload, state.TmuxSessionName), payload.Content) {
				return MailboxProjection{}, false, fmt.Errorf("invalid inbox path for %q", payload.MessageID)
			}
		case MailboxProjectionReadEventType:
			inboxKey := inboxPathFromPayload(payload, state.TmuxSessionName)
			if payload.Content == "" {
				// A read event can carry empty content when its shadow
				// recorder raced a concurrent projection sync writing the
				// same path, or observed the file before a first-ever read
				// established any content yet (see issue #633). Validate
				// the path first.
				if !isAllowedProjectionPath(payload.Path) {
					return MailboxProjection{}, false, fmt.Errorf("invalid read path %q", payload.Path)
				}
				readKey := pathKey(payload.Path)
				if _, exists := projected.Read[readKey]; exists {
					// A genuine, non-empty read already completed this
					// transition earlier; this is a later racy re-render.
					// Keep the good content (untouched) and finish
					// removing the inbox entry as normal.
					delete(projected.Inbox, inboxKey)
					continue
				}
				// First-ever read for this message carries no usable
				// content. The message has still left inbox -- for the
				// non-owner direct-pop path in particular,
				// ArchiveInboxMessage already moved it to read/ via a raw
				// rename before any journal event exists for it -- so the
				// inbox entry no longer reflects reality and must be
				// removed. Leaving it in place would make
				// syncDesiredMailboxFiles write the message back into
				// inbox/, resurrecting an already-archived message as
				// unread and re-consumable (found in review of the prior
				// fix). But we must not fabricate an empty Read entry
				// either (the original #633 truncation bug), and we must
				// not let the cleanup pass delete whatever real file is
				// already sitting at this read path just because this
				// replay has no content for it. Tombstone the path
				// instead: syncDesiredMailboxFiles preserves an existing
				// on-disk file there (like the managedPost exception for
				// post/) rather than deleting or resurrecting it. A
				// subsequent genuine, non-empty read event still
				// completes the transition normally.
				delete(projected.Inbox, inboxKey)
				projected.tombstonedRead[readKey] = true
				continue
			}
			delete(projected.Inbox, inboxKey)
			delete(projected.tombstonedRead, pathKey(payload.Path))
			if !setProjectedFile(projected.Read, payload.Path, payload.Content) {
				return MailboxProjection{}, false, fmt.Errorf("invalid read path %q", payload.Path)
			}
		case MailboxProjectionDeadLetteredEventType:
			rememberManagedPost(projected.managedPost, payload.SourcePath)
			delete(projected.Post, pathKey(payload.SourcePath))
			if !setProjectedFile(projected.DeadLetter, payload.Path, payload.Content) {
				return MailboxProjection{}, false, fmt.Errorf("invalid dead-letter path %q", payload.Path)
			}
		}
	}

	if !sawLease || !sawResolution {
		return MailboxProjection{}, false, nil
	}

	return projected, true, nil
}

func SyncMailboxProjection(sessionDir string) error {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return nil
	}
	if err := quarantineMailboxProjectionTrees(sessionDir, state); err != nil {
		return err
	}

	projected, ok, err := ProjectMailboxProjection(sessionDir)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	desired := make(map[string]string)
	for key, file := range projected.Post {
		desired[key] = file.Content
	}
	for key, file := range projected.Inbox {
		desired[key] = file.Content
	}
	for key, file := range projected.Read {
		desired[key] = file.Content
	}
	for key, file := range projected.DeadLetter {
		desired[key] = file.Content
	}

	for _, root := range mailboxProjectionRoots {
		if err := ensureMailboxDir(filepath.Join(sessionDir, root)); err != nil {
			return fmt.Errorf("ensuring %s dir: %w", root, err)
		}
	}
	if err := syncDesiredMailboxFiles(sessionDir, desired, projected.managedPost, projected.tombstonedRead); err != nil {
		return err
	}
	return writeMailboxProjectionMarker(sessionDir, mailboxProjectionMarker{
		SessionKey: state.SessionKey,
		Generation: state.Generation,
	})
}

func pathKey(path string) string {
	return filepath.Clean(path)
}

func rememberManagedPost(managedPost map[string]bool, relativePath string) {
	if !isAllowedProjectionPath(relativePath) {
		return
	}
	key := pathKey(relativePath)
	if strings.HasPrefix(key, "post"+string(filepath.Separator)) {
		managedPost[key] = true
	}
}

func decodeMailboxEventPayload(raw json.RawMessage) (journal.MailboxEventPayload, bool) {
	var payload journal.MailboxEventPayload
	if len(raw) == 0 {
		return payload, true
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return journal.MailboxEventPayload{}, false
	}
	return payload, true
}

func setProjectedFile(target map[string]ProjectedFile, relativePath, content string) bool {
	if !isAllowedProjectionPath(relativePath) {
		return false
	}
	key := pathKey(relativePath)
	target[key] = ProjectedFile{
		Path:    key,
		Content: content,
	}
	return true
}

func inboxPathFromPayload(payload journal.MailboxEventPayload, sessionName string) string {
	if isAllowedProjectionPath(payload.Path) && strings.HasPrefix(pathKey(payload.Path), "inbox"+string(filepath.Separator)) {
		return pathKey(payload.Path)
	}
	if payload.MessageID == "" || payload.To == "" {
		return ""
	}
	fullRecipient := nodeaddr.Full(payload.To, sessionName)
	recipientSession, recipientName, hasSession := nodeaddr.Split(fullRecipient)
	if !hasSession || recipientSession != sessionName || recipientName == "" {
		return ""
	}
	return pathKey(filepath.Join("inbox", recipientName, payload.MessageID))
}

func isAllowedProjectionPath(relativePath string) bool {
	if relativePath == "" {
		return false
	}
	if filepath.IsAbs(relativePath) {
		return false
	}
	clean := pathKey(relativePath)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return false
	}
	root := strings.SplitN(clean, string(filepath.Separator), 2)[0]
	for _, allowed := range mailboxProjectionRoots {
		if root == allowed {
			return true
		}
	}
	return false
}

func syncDesiredMailboxFiles(sessionDir string, desired map[string]string, managedPost map[string]bool, tombstonedRead map[string]bool) error {
	for relativePath, content := range desired {
		if !isAllowedProjectionPath(relativePath) {
			return fmt.Errorf("invalid desired projection path %q", relativePath)
		}
		absPath := filepath.Join(sessionDir, relativePath)
		if err := ensureMailboxDir(filepath.Dir(absPath)); err != nil {
			return fmt.Errorf("ensuring parent dir: %w", err)
		}
		existing, err := os.ReadFile(absPath)
		if err == nil && string(existing) == content {
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("reading existing projection file %s: %w", relativePath, err)
		}
		if err := writeFileAtomic(absPath, []byte(content), 0o600); err != nil {
			return fmt.Errorf("writing projection file %s: %w", relativePath, err)
		}
	}

	for _, root := range mailboxProjectionRoots {
		rootPath := filepath.Join(sessionDir, root)
		if err := filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(sessionDir, path)
			if err != nil {
				return err
			}
			rel = pathKey(rel)
			if _, ok := desired[rel]; ok {
				return nil
			}
			if strings.HasPrefix(rel, "post"+string(filepath.Separator)) {
				if !managedPost[rel] {
					return nil
				}
			}
			if strings.HasPrefix(rel, "read"+string(filepath.Separator)) {
				// A tombstoned path had its only read event recorded with
				// empty content (see issue #633 follow-up): preserve
				// whatever is already on disk here instead of deleting it
				// -- the file may be a legitimately archived message
				// whose content this replay simply doesn't have.
				if tombstonedRead[rel] {
					return nil
				}
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		}); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("walking %s: %w", root, err)
		}
		if err := removeEmptyDirs(rootPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("cleaning empty dirs under %s: %w", root, err)
		}
	}
	return nil
}

func removeEmptyDirs(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		child := filepath.Join(root, entry.Name())
		if err := removeEmptyDirs(child); err != nil {
			return err
		}
		childEntries, err := os.ReadDir(child)
		if err != nil {
			return err
		}
		if len(childEntries) == 0 {
			if err := os.Remove(child); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func mailboxProjectionMarkerPath(sessionDir string) string {
	return filepath.Join(sessionDir, "snapshot", "mailbox-projection-marker.json")
}

func readMailboxProjectionMarker(sessionDir string) (mailboxProjectionMarker, bool) {
	data, err := os.ReadFile(mailboxProjectionMarkerPath(sessionDir))
	if err != nil {
		return mailboxProjectionMarker{}, false
	}
	var marker mailboxProjectionMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return mailboxProjectionMarker{}, false
	}
	if marker.SessionKey == "" || marker.Generation < 1 {
		return mailboxProjectionMarker{}, false
	}
	return marker, true
}

func writeMailboxProjectionMarker(sessionDir string, marker mailboxProjectionMarker) error {
	if err := ensureMailboxDir(filepath.Dir(mailboxProjectionMarkerPath(sessionDir))); err != nil {
		return err
	}
	data, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	return os.WriteFile(mailboxProjectionMarkerPath(sessionDir), data, 0o600)
}

func quarantineMailboxProjectionTrees(sessionDir string, state journal.SessionState) error {
	marker, ok := readMailboxProjectionMarker(sessionDir)
	if !ok {
		return nil
	}
	if marker.SessionKey == state.SessionKey && marker.Generation == state.Generation {
		return nil
	}

	quarantineRoot := filepath.Join(sessionDir, "snapshot", "quarantine", fmt.Sprintf("generation-%d", marker.Generation))
	if err := ensureMailboxDir(filepath.Dir(quarantineRoot)); err != nil {
		return err
	}
	if err := ensureMailboxDir(quarantineRoot); err != nil {
		return err
	}

	for _, root := range mailboxProjectionRoots {
		src := filepath.Join(sessionDir, root)
		entries, err := os.ReadDir(src)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			continue
		}
		dst := filepath.Join(quarantineRoot, root)
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
		if err := os.Rename(src, dst); err != nil {
			return err
		}
		if err := ensureMailboxDir(src); err != nil {
			return err
		}
	}
	return nil
}

func ensureMailboxDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

// writeFileAtomic writes content to a temp file in path's directory, then
// renames it into place. A concurrent reader of path (for example the
// daemon's read-event shadow recorder) can then never observe a partially
// written or truncated file: os.Rename atomically swaps the previous
// complete content for the new complete content. See issue #633.
func writeFileAtomic(path string, content []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".mailbox-projection-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
