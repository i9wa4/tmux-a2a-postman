package projection

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
)

type ProjectedFile struct {
	Path    string
	Content string
}

type CompatibilityMailbox struct {
	Post       map[string]ProjectedFile
	Inbox      map[string]ProjectedFile
	Read       map[string]ProjectedFile
	Waiting    map[string]ProjectedFile
	DeadLetter map[string]ProjectedFile
}

type compatibilityMarker struct {
	SessionKey string `json:"session_key"`
	Generation int    `json:"generation"`
}

const compatibilitySubmitSchemaVersion = 1

type CompatibilitySubmitCommand string

const (
	CompatibilitySubmitSend CompatibilitySubmitCommand = "send"
	CompatibilitySubmitPop  CompatibilitySubmitCommand = "pop"
)

type CompatibilitySubmitRequest struct {
	SchemaVersion int                        `json:"schema_version"`
	RequestID     string                     `json:"request_id"`
	Command       CompatibilitySubmitCommand `json:"command"`
	CreatedAt     string                     `json:"created_at"`
	Filename      string                     `json:"filename,omitempty"`
	Node          string                     `json:"node,omitempty"`
	Content       string                     `json:"content,omitempty"`
}

type CompatibilitySubmitResponse struct {
	SchemaVersion int                        `json:"schema_version"`
	RequestID     string                     `json:"request_id"`
	Command       CompatibilitySubmitCommand `json:"command"`
	HandledAt     string                     `json:"handled_at"`
	Empty         bool                       `json:"empty,omitempty"`
	Filename      string                     `json:"filename,omitempty"`
	Content       string                     `json:"content,omitempty"`
	UnreadBefore  int                        `json:"unread_before,omitempty"`
	Error         string                     `json:"error,omitempty"`
}

var compatibilityRoots = []string{"post", "inbox", "read", "waiting", "dead-letter"}

func ProjectCompatibilityMailbox(sessionDir string) (CompatibilityMailbox, bool, error) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return CompatibilityMailbox{}, false, nil
	}

	events, err := journal.Replay(sessionDir)
	if err != nil {
		return CompatibilityMailbox{}, false, err
	}
	if len(events) == 0 {
		return CompatibilityMailbox{}, false, nil
	}

	projected := CompatibilityMailbox{
		Post:       make(map[string]ProjectedFile),
		Inbox:      make(map[string]ProjectedFile),
		Read:       make(map[string]ProjectedFile),
		Waiting:    make(map[string]ProjectedFile),
		DeadLetter: make(map[string]ProjectedFile),
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
			return CompatibilityMailbox{}, false, fmt.Errorf("decode mailbox payload for %s", event.Type)
		}

		switch event.Type {
		case "compatibility_mailbox_posted":
			if !setProjectedFile(projected.Post, payload.Path, payload.Content) {
				return CompatibilityMailbox{}, false, fmt.Errorf("invalid post path %q", payload.Path)
			}
		case "compatibility_mailbox_post_consumed":
			delete(projected.Post, pathKey(payload.Path))
		case "compatibility_mailbox_delivered":
			if !setProjectedFile(projected.Inbox, inboxPathFromPayload(payload, state.TmuxSessionName), payload.Content) {
				return CompatibilityMailbox{}, false, fmt.Errorf("invalid inbox path for %q", payload.MessageID)
			}
		case "compatibility_mailbox_read":
			delete(projected.Inbox, inboxPathFromPayload(payload, state.TmuxSessionName))
			if !setProjectedFile(projected.Read, payload.Path, payload.Content) {
				return CompatibilityMailbox{}, false, fmt.Errorf("invalid read path %q", payload.Path)
			}
		case "compatibility_mailbox_waiting_created":
			fallthrough
		case "compatibility_mailbox_waiting_updated":
			if !setProjectedFile(projected.Waiting, payload.Path, payload.Content) {
				return CompatibilityMailbox{}, false, fmt.Errorf("invalid waiting path %q", payload.Path)
			}
		case "compatibility_mailbox_waiting_cleared":
			delete(projected.Waiting, pathKey(payload.Path))
		case "compatibility_mailbox_dead_lettered":
			delete(projected.Post, pathKey(payload.SourcePath))
			if !setProjectedFile(projected.DeadLetter, payload.Path, payload.Content) {
				return CompatibilityMailbox{}, false, fmt.Errorf("invalid dead-letter path %q", payload.Path)
			}
		}
	}

	if !sawLease || !sawResolution {
		return CompatibilityMailbox{}, false, nil
	}

	return projected, true, nil
}

func SyncCompatibilityMailbox(sessionDir string) error {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return nil
	}
	if err := quarantineCompatibilityTrees(sessionDir, state); err != nil {
		return err
	}

	projected, ok, err := ProjectCompatibilityMailbox(sessionDir)
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
	for key, file := range projected.Waiting {
		desired[key] = file.Content
	}
	for key, file := range projected.DeadLetter {
		desired[key] = file.Content
	}

	for _, root := range compatibilityRoots {
		if err := ensureMailboxDir(filepath.Join(sessionDir, root)); err != nil {
			return fmt.Errorf("ensuring %s dir: %w", root, err)
		}
	}
	if err := syncDesiredMailboxFiles(sessionDir, desired); err != nil {
		return err
	}
	return writeCompatibilityMarker(sessionDir, compatibilityMarker{
		SessionKey: state.SessionKey,
		Generation: state.Generation,
	})
}

func pathKey(path string) string {
	return filepath.Clean(path)
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
	for _, allowed := range compatibilityRoots {
		if root == allowed {
			return true
		}
	}
	return false
}

func syncDesiredMailboxFiles(sessionDir string, desired map[string]string) error {
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
		if err := os.WriteFile(absPath, []byte(content), 0o600); err != nil {
			return fmt.Errorf("writing projection file %s: %w", relativePath, err)
		}
	}

	for _, root := range compatibilityRoots {
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

func compatibilityMarkerPath(sessionDir string) string {
	return filepath.Join(sessionDir, "snapshot", "compatibility-mailbox-marker.json")
}

func readCompatibilityMarker(sessionDir string) (compatibilityMarker, bool) {
	data, err := os.ReadFile(compatibilityMarkerPath(sessionDir))
	if err != nil {
		return compatibilityMarker{}, false
	}
	var marker compatibilityMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return compatibilityMarker{}, false
	}
	if marker.SessionKey == "" || marker.Generation < 1 {
		return compatibilityMarker{}, false
	}
	return marker, true
}

func writeCompatibilityMarker(sessionDir string, marker compatibilityMarker) error {
	if err := ensureMailboxDir(filepath.Dir(compatibilityMarkerPath(sessionDir))); err != nil {
		return err
	}
	data, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	return os.WriteFile(compatibilityMarkerPath(sessionDir), data, 0o600)
}

func quarantineCompatibilityTrees(sessionDir string, state journal.SessionState) error {
	marker, ok := readCompatibilityMarker(sessionDir)
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

	for _, root := range compatibilityRoots {
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

func CompatibilitySubmitRequestsDir(sessionDir string) string {
	return filepath.Join(sessionDir, "snapshot", "compatibility-submit", "requests")
}

func CompatibilitySubmitResponsesDir(sessionDir string) string {
	return filepath.Join(sessionDir, "snapshot", "compatibility-submit", "responses")
}

func CompatibilitySubmitRequestPath(sessionDir, requestID string) string {
	return filepath.Join(CompatibilitySubmitRequestsDir(sessionDir), requestID+".json")
}

func CompatibilitySubmitResponsePath(sessionDir, requestID string) string {
	return filepath.Join(CompatibilitySubmitResponsesDir(sessionDir), requestID+".json")
}

func EnsureCompatibilitySubmitDirs(sessionDir string) error {
	if err := ensureMailboxDir(CompatibilitySubmitRequestsDir(sessionDir)); err != nil {
		return err
	}
	return ensureMailboxDir(CompatibilitySubmitResponsesDir(sessionDir))
}

func NewCompatibilitySubmitRequestID(now time.Time) (string, error) {
	var suffix [2]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-r%04x", now.UTC().Format("20060102-150405"), suffix), nil
}

func WriteCompatibilitySubmitRequest(sessionDir string, request CompatibilitySubmitRequest) (string, error) {
	if err := EnsureCompatibilitySubmitDirs(sessionDir); err != nil {
		return "", err
	}
	request.SchemaVersion = compatibilitySubmitSchemaVersion
	requestPath := CompatibilitySubmitRequestPath(sessionDir, request.RequestID)
	if err := writeCompatibilitySubmitJSON(requestPath, request); err != nil {
		return "", err
	}
	return requestPath, nil
}

func WriteCompatibilitySubmitResponse(sessionDir string, response CompatibilitySubmitResponse) (string, error) {
	if err := EnsureCompatibilitySubmitDirs(sessionDir); err != nil {
		return "", err
	}
	response.SchemaVersion = compatibilitySubmitSchemaVersion
	responsePath := CompatibilitySubmitResponsePath(sessionDir, response.RequestID)
	if err := writeCompatibilitySubmitJSON(responsePath, response); err != nil {
		return "", err
	}
	return responsePath, nil
}

func ReadCompatibilitySubmitRequest(path string) (CompatibilitySubmitRequest, error) {
	var request CompatibilitySubmitRequest
	if err := readCompatibilitySubmitJSON(path, &request); err != nil {
		return CompatibilitySubmitRequest{}, err
	}
	return request, nil
}

func ReadCompatibilitySubmitResponse(path string) (CompatibilitySubmitResponse, error) {
	var response CompatibilitySubmitResponse
	if err := readCompatibilitySubmitJSON(path, &response); err != nil {
		return CompatibilitySubmitResponse{}, err
	}
	return response, nil
}

func WaitCompatibilitySubmitResponse(sessionDir, requestID string, timeout time.Duration) (CompatibilitySubmitResponse, string, error) {
	if timeout <= 0 {
		timeout = time.Second
	}
	responsePath := CompatibilitySubmitResponsePath(sessionDir, requestID)
	deadline := time.Now().Add(timeout)
	for {
		response, err := ReadCompatibilitySubmitResponse(responsePath)
		if err == nil {
			return response, responsePath, nil
		}
		if !os.IsNotExist(err) {
			return CompatibilitySubmitResponse{}, "", err
		}
		if time.Now().After(deadline) {
			return CompatibilitySubmitResponse{}, "", fmt.Errorf("timed out waiting for compatibility submit response %q", requestID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func writeCompatibilitySubmitJSON(path string, value interface{}) error {
	tmpPath := path + ".tmp"
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func readCompatibilitySubmitJSON(path string, target interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
