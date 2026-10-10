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
	"github.com/i9wa4/tmux-a2a-postman/internal/store"
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

	// deadLetteredSources and deadLetteredIDs remember, from the journaled
	// dead-letter events themselves (not from the projected dead-letter file
	// name or content), which source paths and message ids were dead-lettered in
	// the current generation. The projected DeadLetter file keeps only the
	// suffixed dead-letter path and the content, so a message whose content has
	// no embedded id could not be matched back to its original name.
	deadLetteredSources map[string]bool
	deadLetteredIDs     map[string]bool
}

// IsDeadLetteredSource reports whether, in the current session generation, a
// journaled dead-letter event names the given path (for example
// "read/<file name>") as its source. A pop that fails after the archive rename
// journals exactly that shape (SourcePath read/<file name>).
func (p MailboxProjection) IsDeadLetteredSource(path string) bool {
	return p.deadLetteredSources[pathKey(path)]
}

// IsDeadLetteredMessage reports whether, in the current session generation, a
// journaled dead-letter event carries the given message id (the file name for
// messages popped through the daemon-submit pop path).
func (p MailboxProjection) IsDeadLetteredMessage(messageID string) bool {
	return messageID != "" && p.deadLetteredIDs[messageID]
}

// IsTombstonedRead reports whether, in the current session generation, the
// journal holds a first read event with EMPTY content for the given read path
// (for example "read/<file name>"). Such a read leaves no projected.Read entry
// and no projected.Inbox entry, but the projection sync keeps the archive file
// at that path. Callers that must not guess about such an archive (inspect-message)
// use this to tell it apart from an archive the journal never mentions.
func (p MailboxProjection) IsTombstonedRead(path string) bool {
	return p.tombstonedRead[pathKey(path)]
}

type mailboxProjectionMarker struct {
	SessionKey string `json:"session_key"`
	Generation int    `json:"generation"`
}

const (
	MailboxProjectionComponent             = "mailbox-projection"
	MailboxProjectionPostedEventType       = "mailbox_projection_posted"
	MailboxProjectionPostObservedEventType = "mailbox_projection_post_observed"
	MailboxProjectionPostConsumedEventType = "mailbox_projection_post_consumed"
	MailboxProjectionDeliveredEventType    = "mailbox_projection_delivered"
	MailboxProjectionReadEventType         = "mailbox_projection_read"
	MailboxProjectionDeadLetteredEventType = "mailbox_projection_dead_lettered"

	// MailboxProjectionPopVerificationFailedEventType records a daemon-submit
	// pop archive-readiness verification failure (#755 F-013), appended once
	// per failed verifyDaemonPopArchiveReadable call so
	// CountPopVerificationFailures can bound retries per message.
	MailboxProjectionPopVerificationFailedEventType = "mailbox_projection_pop_verification_failed"
)

var mailboxProjectionRoots = []string{"post", "inbox", "read", "dead-letter"}

// ProjectMailboxProjection re-derives the current session state itself
// (via loadCurrentSessionState) and projects against it. Callers that
// already hold a specific, freshly-read journal.SessionState under a lock
// (P2-1R items 1-3) must use projectMailboxProjectionForState with that
// SAME value instead: calling this function would re-read state
// independently, reopening exactly the TOCTOU window the lock was meant
// to close if anything changes state between the caller's read and this
// function's own internal read (rework-2, R1-1/R1-2 follow-up).
func ProjectMailboxProjection(sessionDir string) (MailboxProjection, bool, error) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return MailboxProjection{}, false, nil
	}
	return projectMailboxProjectionForState(sessionDir, state)
}

func projectMailboxProjectionForState(sessionDir string, state journal.SessionState) (MailboxProjection, bool, error) {
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

		deadLetteredSources: make(map[string]bool),
		deadLetteredIDs:     make(map[string]bool),
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
			if isAllowedProjectionPath(payload.SourcePath) {
				projected.deadLetteredSources[pathKey(payload.SourcePath)] = true
			}
			if payload.MessageID != "" {
				projected.deadLetteredIDs[payload.MessageID] = true
			}
			rememberManagedPost(projected.managedPost, payload.SourcePath)
			delete(projected.Post, pathKey(payload.SourcePath))
			// #762/F-013 is the first producer that can dead-letter a
			// message still living in projected.Inbox (one whose pop never
			// reached a Read event because verification failed before it).
			// Without this delete, syncDesiredMailboxFiles would write the
			// message back into inbox/ on the next sync, resurrecting an
			// already-dead-lettered message as unread and re-consumable --
			// the identical hazard the Read case above was hardened
			// against, now reachable through this handler too.
			delete(projected.Inbox, inboxPathFromPayload(payload, state.TmuxSessionName))
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

// SyncMailboxProjection re-renders the mailbox projection onto disk for
// the current session generation, quarantining a prior generation's
// mailbox roots first if one is detected.
//
// P2-1R item 3 (rework 2, closing R1-1/R1-2): a prior generation's roots
// are quarantined under the session-level mailbox roots gate
// (store.WithMailboxRootsExclusive), held CONTINUOUSLY from re-reading
// BOTH the marker and the session state through the root moves and the
// final marker write below, so the decision to transition, the moves
// themselves, and the commit of the new marker are one critical section.
// Session state is re-read fresh under the gate every time (never a
// value captured before the gate was acquired): a stale pre-gate read
// reused inside the gate could commit a marker for a generation the
// session had already moved past by the time the gate was granted,
// silently losing newer-generation mail to a mislabeled quarantine on a
// later call (R1-1). The common (non-transitioning) case is NOT lock-free
// either: it is a mailbox-root writer (it calls syncMailboxProjectionBody,
// which writes into every root and then writes the marker), so it must
// hold the gate SHARED -- re-reading state and the marker fresh inside
// that hold too -- rather than running ungated, which could otherwise
// race a concurrent exclusive transition and regress the marker back to a
// stale generation after the transition already moved the roots (R1-2).
// If the shared hold discovers a transition is actually needed, it
// releases first (never upgrading a shared hold to exclusive) and only
// then takes the exclusive path. See also WithAdmissionFence's lock-order
// doc: any future writer or claim path must hold this same gate SHARED
// before taking its own recipient fence, and must never hold it (or any
// recipient fence) while calling SyncMailboxProjection, which would
// self-deadlock.
func SyncMailboxProjection(sessionDir string) error {
	return syncMailboxProjectionSeam(sessionDir, nil, nil)
}

// syncMailboxProjectionSeam is SyncMailboxProjection plus two test-only
// hooks: aboutToRequestExclusive fires immediately before requesting the
// exclusive roots gate (P2-1R item 6(a)/6(f)), and duringFastPathHold
// fires while the shared fast path is holding the gate, immediately
// before running the sync body (item 6(g)). Either lets a disposable
// fixture synchronize deterministically instead of relying on a sleep.
func syncMailboxProjectionSeam(sessionDir string, aboutToRequestExclusive, duringFastPathHold func()) error {
	if _, ok := loadCurrentSessionState(sessionDir); !ok {
		return nil
	}

	transitionNeeded := false
	fastPathErr := store.WithMailboxRootsShared(sessionDir, func() error {
		curState, ok := loadCurrentSessionState(sessionDir)
		if !ok {
			return nil
		}
		if marker, ok := readMailboxProjectionMarker(sessionDir); ok &&
			marker.SessionKey == curState.SessionKey && marker.Generation == curState.Generation {
			if duringFastPathHold != nil {
				duringFastPathHold()
			}
			return syncMailboxProjectionBody(sessionDir, curState)
		}
		transitionNeeded = true
		return nil
	})
	if fastPathErr != nil {
		return fastPathErr
	}
	if !transitionNeeded {
		return nil
	}

	if aboutToRequestExclusive != nil {
		aboutToRequestExclusive()
	}
	return store.WithMailboxRootsExclusive(sessionDir, func() error {
		curState, ok := loadCurrentSessionState(sessionDir)
		if !ok {
			return nil
		}
		marker, ok := readMailboxProjectionMarker(sessionDir)
		switch {
		case ok && marker.SessionKey == curState.SessionKey && marker.Generation == curState.Generation:
			// Another caller already completed this transition while we
			// waited for the gate; nothing left to move (P2-1R item 6(b)).
		case ok:
			if err := quarantineMailboxProjectionRoots(sessionDir, marker); err != nil {
				return err
			}
		}
		return syncMailboxProjectionBody(sessionDir, curState)
	})
}

func syncMailboxProjectionBody(sessionDir string, state journal.SessionState) error {
	// Use the caller's already-fresh state directly (projectMailboxProjectionForState),
	// NOT the public ProjectMailboxProjection(sessionDir), which re-reads
	// session state independently. A second independent read here would
	// reopen the exact race rework-2 closes: state could be re-read as a
	// newer generation than the one callers verified against the marker
	// a moment earlier (under the shared or exclusive gate), projecting
	// and syncing the wrong generation's content while writing a marker
	// for a different one.
	projected, ok, err := projectMailboxProjectionForState(sessionDir, state)
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

// quarantineMailboxProjectionRoots moves every non-empty mailbox root
// (post, inbox, read, dead-letter) wholesale into
// snapshot/quarantine/generation-<marker.Generation>/<root>, recreating an
// empty root in its place. The caller must already hold the session-level
// mailbox roots gate exclusively (store.WithMailboxRootsExclusive); this
// function takes no per-recipient fence itself and creates no
// per-recipient admission state (P2-1R items 1 and 3, closing P1-F1,
// which found per-recipient fences structurally unable to cover these
// roots or an unknown/first-use recipient).
//
// P2-1R item 4 (closing P1-F3): this is non-destructive and retry-safe. An
// existing quarantine destination is NEVER deleted or overwritten; if
// generation-<G>/<root> already exists (from an earlier successful move
// into this same generation, e.g. a prior partial-transition attempt, or a
// retry after new activity), the live root is moved to
// generation-<G>/<root>.<k> for the smallest unused k >= 1 instead. A
// mid-sequence failure (one root's rename erroring) returns the error
// immediately with no further roots touched and the marker still
// unwritten by the caller; a subsequent retry only moves roots that have
// regained content, and never deletes anything already snapshotted.
func quarantineMailboxProjectionRoots(sessionDir string, marker mailboxProjectionMarker) error {
	return quarantineMailboxProjectionRootsWithOps(sessionDir, marker, osQuarantineOps)
}

// quarantineOps isolates the one fallible, destructive-if-wrong step
// (moving a root into its quarantine destination) behind a function
// field, so a disposable fixture can inject a failure for a specific root
// without faking the filesystem wholesale (P2-1R item 6(c)).
type quarantineOps struct {
	rename func(oldpath, newpath string) error
}

var osQuarantineOps = quarantineOps{rename: os.Rename}

func quarantineMailboxProjectionRootsWithOps(sessionDir string, marker mailboxProjectionMarker, ops quarantineOps) error {
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

		dst, err := nextFreeQuarantineDestination(quarantineRoot, root)
		if err != nil {
			return err
		}
		if err := ops.rename(src, dst); err != nil {
			return fmt.Errorf("quarantining %s: %w", root, err)
		}
		if err := ensureMailboxDir(src); err != nil {
			return err
		}
	}
	return nil
}

// nextFreeQuarantineDestination returns quarantineRoot/root if it does not
// exist yet, or quarantineRoot/root.<k> for the smallest unused k >= 1
// otherwise, so a repeated or retried move into the same generation can
// never overwrite or merge into an existing snapshot (P2-1R item 4).
func nextFreeQuarantineDestination(quarantineRoot, root string) (string, error) {
	base := filepath.Join(quarantineRoot, root)
	if _, err := os.Lstat(base); os.IsNotExist(err) {
		return base, nil
	} else if err != nil {
		return "", fmt.Errorf("stat quarantine destination %s: %w", base, err)
	}
	for k := 1; ; k++ {
		candidate := fmt.Sprintf("%s.%d", base, k)
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", fmt.Errorf("stat quarantine destination %s: %w", candidate, err)
		}
	}
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
