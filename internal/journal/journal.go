package journal

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const schemaVersion = 1

const (
	sessionResolvedEventType = "session_resolved"
	leaseAcquiredEventType   = "lease_acquired"
)

type Visibility string

const (
	VisibilityControlPlaneOnly     Visibility = "control_plane_only"
	VisibilityOperatorVisible      Visibility = "operator_visible"
	VisibilityCompatibilityMailbox Visibility = "compatibility_mailbox"
)

type ResolutionMode string

const (
	ResolutionResumeCurrent      ResolutionMode = "resume_current"
	ResolutionExplicitRebind     ResolutionMode = "explicit_rebind"
	ResolutionExplicitNewSession ResolutionMode = "explicit_new_session"
)

type ResolutionResult string

const (
	ResolutionCreated           ResolutionResult = "created"
	ResolutionResumed           ResolutionResult = "resumed"
	ResolutionRotatedRebind     ResolutionResult = "rotated_rebind"
	ResolutionRotatedNewSession ResolutionResult = "rotated_new_session"
)

type SessionState struct {
	SchemaVersion   int    `json:"schema_version"`
	SessionKey      string `json:"session_key"`
	TmuxSessionName string `json:"tmux_session_name"`
	Generation      int    `json:"generation"`
	UpdatedAt       string `json:"updated_at"`
}

type Lease struct {
	SchemaVersion     int    `json:"schema_version"`
	LeaseID           string `json:"lease_id"`
	LeaseEpoch        int    `json:"lease_epoch"`
	HolderContextID   string `json:"holder_context_id"`
	HolderSessionName string `json:"holder_session_name"`
	HolderPID         int    `json:"holder_pid"`
	AcquiredAt        string `json:"acquired_at"`
}

type Event struct {
	SchemaVersion   int             `json:"schema_version"`
	Sequence        int             `json:"sequence"`
	EventID         string          `json:"event_id"`
	Type            string          `json:"type"`
	Visibility      Visibility      `json:"visibility"`
	SessionKey      string          `json:"session_key"`
	TmuxSessionName string          `json:"tmux_session_name"`
	Generation      int             `json:"generation"`
	LeaseID         string          `json:"lease_id"`
	LeaseEpoch      int             `json:"lease_epoch"`
	OccurredAt      string          `json:"occurred_at"`
	Payload         json.RawMessage `json:"payload"`
}

type Writer struct {
	sessionDir string
	session    SessionState
	lease      Lease
	mu         sync.Mutex
}

type Manager struct {
	contextID string
	holderPID int
	mu        sync.Mutex
	writers   map[string]*Writer
}

var processManager struct {
	sync.RWMutex
	manager *Manager
}

var appendEventBeforeWriteHook func() error

func NewManager(contextID string, holderPID int) *Manager {
	return &Manager{
		contextID: contextID,
		holderPID: holderPID,
		writers:   make(map[string]*Writer),
	}
}

func InstallProcessManager(manager *Manager) {
	processManager.Lock()
	processManager.manager = manager
	processManager.Unlock()
}

func ClearProcessManager() {
	processManager.Lock()
	processManager.manager = nil
	processManager.Unlock()
}

func RecordProcessMailboxEvent(sessionDir, tmuxSessionName, eventType string, visibility Visibility, messageID, from, to, relativePath string, now time.Time) error {
	processManager.RLock()
	manager := processManager.manager
	processManager.RUnlock()
	if manager == nil {
		return nil
	}
	return manager.RecordMailboxEvent(sessionDir, tmuxSessionName, eventType, visibility, messageID, from, to, relativePath, now)
}

func RecordProcessEvent(sessionDir, tmuxSessionName, eventType string, visibility Visibility, payload interface{}, now time.Time) error {
	processManager.RLock()
	manager := processManager.manager
	processManager.RUnlock()
	if manager == nil {
		return nil
	}
	return manager.RecordEvent(sessionDir, tmuxSessionName, eventType, visibility, payload, now)
}

func (m *Manager) Bootstrap(sessionDir, tmuxSessionName string, now time.Time) error {
	_, err := m.writerFor(sessionDir, tmuxSessionName, now)
	return err
}

func (m *Manager) RecordMailboxEvent(sessionDir, tmuxSessionName, eventType string, visibility Visibility, messageID, from, to, relativePath string, now time.Time) error {
	writer, err := m.writerFor(sessionDir, tmuxSessionName, now)
	if err != nil {
		return err
	}
	payload := map[string]string{
		"directory":  directoryNameFromEventType(eventType),
		"message_id": messageID,
		"from":       from,
		"to":         to,
		"path":       relativePath,
	}
	_, err = writer.AppendEvent(eventType, visibility, payload, now)
	return err
}

func (m *Manager) RecordEvent(sessionDir, tmuxSessionName, eventType string, visibility Visibility, payload interface{}, now time.Time) error {
	writer, err := m.writerFor(sessionDir, tmuxSessionName, now)
	if err != nil {
		return err
	}
	_, err = writer.AppendEvent(eventType, visibility, payload, now)
	return err
}

func (m *Manager) writerFor(sessionDir, tmuxSessionName string, now time.Time) (*Writer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if writer, ok := m.writers[sessionDir]; ok {
		return writer, nil
	}

	writer, err := OpenShadowWriter(sessionDir, m.contextID, tmuxSessionName, m.holderPID, now)
	if err != nil {
		return nil, err
	}
	m.writers[sessionDir] = writer
	return writer, nil
}

func OpenShadowWriter(sessionDir, contextID, tmuxSessionName string, holderPID int, now time.Time) (*Writer, error) {
	session, resolution, err := ResolveSession(sessionDir, tmuxSessionName, ResolutionResumeCurrent, now)
	if err != nil {
		return nil, err
	}
	if err := ensureOwnerOnlyDir(snapshotDir(sessionDir)); err != nil {
		return nil, fmt.Errorf("ensuring snapshot dir: %w", err)
	}
	lease, err := AcquireLease(sessionDir, session, contextID, tmuxSessionName, holderPID, now)
	if err != nil {
		return nil, err
	}

	writer := &Writer{
		sessionDir: sessionDir,
		session:    session,
		lease:      lease,
	}
	if _, err := writer.AppendEvent(leaseAcquiredEventType, VisibilityControlPlaneOnly, map[string]interface{}{
		"holder_context_id":   lease.HolderContextID,
		"holder_session_name": lease.HolderSessionName,
		"holder_pid":          lease.HolderPID,
	}, now); err != nil {
		return nil, err
	}
	if _, err := writer.AppendEvent(sessionResolvedEventType, VisibilityControlPlaneOnly, map[string]interface{}{
		"mode":              string(ResolutionResumeCurrent),
		"resolution":        string(resolution),
		"tmux_session_name": session.TmuxSessionName,
	}, now); err != nil {
		return nil, err
	}
	return writer, nil
}

func ResolveSession(sessionDir, tmuxSessionName string, mode ResolutionMode, now time.Time) (SessionState, ResolutionResult, error) {
	if err := ensureOwnerOnlyDir(journalDir(sessionDir)); err != nil {
		return SessionState{}, "", fmt.Errorf("ensuring journal dir: %w", err)
	}
	if err := ensureOwnerOnlyDir(recordsDir(sessionDir)); err != nil {
		return SessionState{}, "", fmt.Errorf("ensuring records dir: %w", err)
	}

	path := sessionStatePath(sessionDir)
	var existing SessionState
	if err := readJSONFile(path, &existing); err != nil {
		if !os.IsNotExist(err) {
			return SessionState{}, "", fmt.Errorf("reading session state: %w", err)
		}
		fresh := SessionState{
			SchemaVersion:   schemaVersion,
			SessionKey:      randomHex(16),
			TmuxSessionName: tmuxSessionName,
			Generation:      1,
			UpdatedAt:       now.UTC().Format(time.RFC3339),
		}
		if err := writeJSONAtomically(path, fresh); err != nil {
			return SessionState{}, "", fmt.Errorf("writing session state: %w", err)
		}
		return fresh, ResolutionCreated, nil
	}

	if existing.SessionKey == "" {
		return SessionState{}, "", fmt.Errorf("session state missing session_key")
	}
	if existing.Generation < 1 {
		return SessionState{}, "", fmt.Errorf("session state generation must be >= 1")
	}

	result := ResolutionResumed
	switch mode {
	case ResolutionResumeCurrent:
		// Keep the current logical session by default.
	case ResolutionExplicitRebind:
		existing.Generation++
		result = ResolutionRotatedRebind
	case ResolutionExplicitNewSession:
		existing.Generation++
		result = ResolutionRotatedNewSession
	default:
		return SessionState{}, "", fmt.Errorf("unknown resolution mode %q", mode)
	}

	existing.SchemaVersion = schemaVersion
	existing.TmuxSessionName = tmuxSessionName
	existing.UpdatedAt = now.UTC().Format(time.RFC3339)
	if err := writeJSONAtomically(path, existing); err != nil {
		return SessionState{}, "", fmt.Errorf("writing session state: %w", err)
	}
	return existing, result, nil
}

func AcquireLease(sessionDir string, session SessionState, contextID, holderSessionName string, holderPID int, now time.Time) (Lease, error) {
	if err := ensureOwnerOnlyDir(leaseDir(sessionDir)); err != nil {
		return Lease{}, fmt.Errorf("ensuring lease dir: %w", err)
	}

	path := currentLeasePath(sessionDir)
	var lease Lease
	if err := withAppendAuthorityFence(sessionDir, func() error {
		var existing Lease
		epoch := 1
		if err := readJSONFile(path, &existing); err == nil {
			if existing.LeaseEpoch < 1 {
				return fmt.Errorf("existing lease epoch must be >= 1")
			}
			epoch = existing.LeaseEpoch + 1
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("reading current lease: %w", err)
		}

		lease = Lease{
			SchemaVersion:     schemaVersion,
			LeaseID:           randomHex(12),
			LeaseEpoch:        epoch,
			HolderContextID:   contextID,
			HolderSessionName: holderSessionName,
			HolderPID:         holderPID,
			AcquiredAt:        now.UTC().Format(time.RFC3339),
		}
		if err := writeJSONAtomically(path, lease); err != nil {
			return fmt.Errorf("writing current lease: %w", err)
		}
		return nil
	}); err != nil {
		return Lease{}, err
	}
	return lease, nil
}

func (w *Writer) AppendEvent(eventType string, visibility Visibility, payload interface{}, now time.Time) (Event, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !isKnownVisibility(visibility) {
		return Event{}, fmt.Errorf("unknown visibility %q", visibility)
	}
	var event Event
	if err := withAppendAuthorityFence(w.sessionDir, func() error {
		if err := w.ensureActiveLease(); err != nil {
			return err
		}

		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshaling payload: %w", err)
		}

		sequence, err := nextSequence(recordsDir(w.sessionDir))
		if err != nil {
			return err
		}

		event = Event{
			SchemaVersion:   schemaVersion,
			Sequence:        sequence,
			EventID:         randomHex(10),
			Type:            eventType,
			Visibility:      visibility,
			SessionKey:      w.session.SessionKey,
			TmuxSessionName: w.session.TmuxSessionName,
			Generation:      w.session.Generation,
			LeaseID:         w.lease.LeaseID,
			LeaseEpoch:      w.lease.LeaseEpoch,
			OccurredAt:      now.UTC().Format(time.RFC3339),
			Payload:         payloadBytes,
		}
		if appendEventBeforeWriteHook != nil {
			if err := appendEventBeforeWriteHook(); err != nil {
				return err
			}
		}

		filename := fmt.Sprintf("%012d-%s.json", event.Sequence, event.EventID)
		if err := writeJSONAtomically(filepath.Join(recordsDir(w.sessionDir), filename), event); err != nil {
			return fmt.Errorf("writing event: %w", err)
		}
		return nil
	}); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (w *Writer) ensureActiveLease() error {
	var current Lease
	if err := readJSONFile(currentLeasePath(w.sessionDir), &current); err != nil {
		return fmt.Errorf("reading current lease: %w", err)
	}
	if current.LeaseID != w.lease.LeaseID || current.LeaseEpoch != w.lease.LeaseEpoch {
		return fmt.Errorf("lease mismatch: writer lease %s/%d is no longer active", w.lease.LeaseID, w.lease.LeaseEpoch)
	}
	return nil
}

func Replay(sessionDir string) ([]Event, error) {
	entries, err := os.ReadDir(recordsDir(sessionDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading records dir: %w", err)
	}

	type committedRecord struct {
		name     string
		sequence int
	}
	records := make([]committedRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		sequence, ok := sequenceFromRecordName(entry.Name())
		if !ok {
			continue
		}
		records = append(records, committedRecord{name: entry.Name(), sequence: sequence})
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].sequence == records[j].sequence {
			return records[i].name < records[j].name
		}
		return records[i].sequence < records[j].sequence
	})

	expectedSequence := 1
	currentLeaseID := ""
	currentLeaseEpoch := 0
	events := make([]Event, 0, len(records))
	for _, record := range records {
		if record.sequence < expectedSequence {
			return nil, fmt.Errorf("replay: duplicate sequence %d", record.sequence)
		}
		if record.sequence > expectedSequence {
			return nil, fmt.Errorf("replay: sequence gap at %d", expectedSequence)
		}

		var event Event
		if err := readJSONFile(filepath.Join(recordsDir(sessionDir), record.name), &event); err != nil {
			return nil, fmt.Errorf("replay: reading %s: %w", record.name, err)
		}
		if event.Sequence != record.sequence {
			return nil, fmt.Errorf("replay: record %s encoded sequence %d, want %d", record.name, event.Sequence, record.sequence)
		}
		if event.SessionKey == "" {
			return nil, fmt.Errorf("replay: record %s missing session_key", record.name)
		}
		if event.Generation < 1 {
			return nil, fmt.Errorf("replay: record %s has invalid generation %d", record.name, event.Generation)
		}

		if event.Type == leaseAcquiredEventType {
			if event.LeaseID == "" {
				return nil, fmt.Errorf("replay: record %s missing lease_id", record.name)
			}
			if event.LeaseEpoch < 1 {
				return nil, fmt.Errorf("replay: record %s has invalid lease_epoch %d", record.name, event.LeaseEpoch)
			}
			if currentLeaseEpoch != 0 && event.LeaseEpoch <= currentLeaseEpoch {
				return nil, fmt.Errorf("replay: non-monotonic lease epoch %d", event.LeaseEpoch)
			}
			currentLeaseID = event.LeaseID
			currentLeaseEpoch = event.LeaseEpoch
		} else {
			if currentLeaseID == "" {
				return nil, fmt.Errorf("replay: record %s appeared before lease acquisition", record.name)
			}
			if event.LeaseID != currentLeaseID || event.LeaseEpoch != currentLeaseEpoch {
				return nil, fmt.Errorf("replay: lease mismatch at sequence %d", event.Sequence)
			}
		}

		events = append(events, event)
		expectedSequence++
	}
	return events, nil
}

func SessionStatePath(sessionDir string) string {
	return sessionStatePath(sessionDir)
}

func CurrentLeasePath(sessionDir string) string {
	return currentLeasePath(sessionDir)
}

func RecordsDir(sessionDir string) string {
	return recordsDir(sessionDir)
}

func sessionStatePath(sessionDir string) string {
	return filepath.Join(journalDir(sessionDir), "session.json")
}

func currentLeasePath(sessionDir string) string {
	return filepath.Join(leaseDir(sessionDir), "current.json")
}

func journalDir(sessionDir string) string {
	return filepath.Join(sessionDir, "journal")
}

func recordsDir(sessionDir string) string {
	return filepath.Join(journalDir(sessionDir), "records")
}

func snapshotDir(sessionDir string) string {
	return filepath.Join(sessionDir, "snapshot")
}

func leaseDir(sessionDir string) string {
	return filepath.Join(sessionDir, "lease")
}

func appendAuthorityFencePath(sessionDir string) string {
	return filepath.Join(leaseDir(sessionDir), "append-authority.lock")
}

func directoryNameFromEventType(eventType string) string {
	switch {
	case strings.HasSuffix(eventType, "_posted"):
		return "post"
	case strings.HasSuffix(eventType, "_delivered"):
		return "inbox"
	case strings.HasSuffix(eventType, "_read"):
		return "read"
	default:
		return ""
	}
}

func isKnownVisibility(visibility Visibility) bool {
	switch visibility {
	case VisibilityControlPlaneOnly, VisibilityOperatorVisible, VisibilityCompatibilityMailbox:
		return true
	default:
		return false
	}
}

func ensureOwnerOnlyDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func withAppendAuthorityFence(sessionDir string, fn func() error) error {
	if err := ensureOwnerOnlyDir(leaseDir(sessionDir)); err != nil {
		return fmt.Errorf("ensuring lease dir: %w", err)
	}

	path := appendAuthorityFencePath(sessionDir)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening append authority fence: %w", err)
	}
	defer file.Close()

	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod append authority fence: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("locking append authority fence: %w", err)
	}
	defer func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	}()

	return fn()
}

func nextSequence(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("reading records dir: %w", err)
	}

	maxSequence := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		sequence, ok := sequenceFromRecordName(entry.Name())
		if !ok {
			continue
		}
		if sequence > maxSequence {
			maxSequence = sequence
		}
	}
	return maxSequence + 1, nil
}

func sequenceFromRecordName(name string) (int, bool) {
	if !strings.HasSuffix(name, ".json") {
		return 0, false
	}
	base := strings.TrimSuffix(name, ".json")
	parts := strings.SplitN(base, "-", 2)
	if len(parts) != 2 || parts[0] == "" || strings.HasPrefix(parts[0], ".tmp") {
		return 0, false
	}
	sequence, err := strconv.Atoi(parts[0])
	if err != nil || sequence < 1 {
		return 0, false
	}
	return sequence, true
}

func writeJSONAtomically(path string, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	if err := ensureOwnerOnlyDir(filepath.Dir(path)); err != nil {
		return err
	}

	tempPath := filepath.Join(filepath.Dir(path), ".tmp-"+randomHex(8)+filepath.Ext(path))
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(tempPath)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(tempPath)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func readJSONFile(path string, target interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func randomHex(byteCount int) string {
	buf := make([]byte, byteCount)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("journal: random source failed: %v", err))
	}
	return fmt.Sprintf("%x", buf)
}
