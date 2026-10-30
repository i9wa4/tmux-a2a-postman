package workspace

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	captureLimit     = 128
	captureScanLimit = 4096
	captureRetention = 7 * 24 * time.Hour
)

// PaneCapturer reads one already verified native pane. Runtime alone does not
// grant this capability, which keeps synthetic and non-tmux runtimes inert.
type PaneCapturer interface {
	CapturePane(context.Context, Binding) (string, error)
}

type PaneObservation struct {
	Role        string `json:"role"`
	Pane        string `json:"pane,omitempty"`
	CapturedAt  string `json:"captured_at,omitempty"`
	Incarnation string `json:"incarnation,omitempty"`
	Text        string `json:"text,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// CaptureResult is supplemental evidence for one committed mail operation.
// It is never a prerequisite for send or POP success.
type CaptureResult struct {
	Version      int               `json:"version"`
	MessageID    string            `json:"message_id"`
	CapturedAt   string            `json:"captured_at"`
	Incarnation  string            `json:"incarnation,omitempty"`
	Observations []PaneObservation `json:"observations,omitempty"`
	Reason       string            `json:"reason,omitempty"`
	Path         string            `json:"path,omitempty"`
}

// CaptureRelated is caller-selected and reads only the sender and receiver of
// msg. Every probe is bounded by the runtime adapter. Capture and persistence
// failures are represented as evidence, never returned as a mail error.
func (m Mail) CaptureRelated(ctx context.Context, rt Runtime, msg Message) CaptureResult {
	result := CaptureResult{Version: 1, MessageID: msg.ID, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if !messageID(msg.ID) || msg.From == "" || msg.To == "" || msg.From == msg.To {
		result.Reason = "invalid related mail identity"
		return result
	}
	if rt == nil {
		result.Reason = "pane capture unsupported by runtime"
		m.saveUnknownCapture(&result)
		return result
	}
	capturer, ok := rt.(PaneCapturer)
	if !ok {
		result.Reason = "pane capture unsupported by runtime"
		m.saveUnknownCapture(&result)
		return result
	}
	snapshot, err := rt.Snapshot(ctx)
	if err != nil || validateCaptureSnapshot(snapshot, msg) != nil {
		result.Reason = "native related-pane identity unknown"
		m.saveUnknownCapture(&result)
		return result
	}
	// Freeze target identities before a pane read can change the runtime snapshot.
	snapshot.Bindings = append([]Binding(nil), snapshot.Bindings...)
	snapshot.AbsentRoles = append([]string(nil), snapshot.AbsentRoles...)
	result.Incarnation = snapshot.Caller.Owner.Incarnation
	for _, role := range []string{msg.From, msg.To} {
		observation := PaneObservation{Role: role}
		var selected Binding
		for _, binding := range snapshot.Bindings {
			if binding.Role == role {
				selected = binding
				break
			}
		}
		owner := selected.Owner
		if !owner.valid() {
			observation.Reason = "related role verified absent"
		} else {
			observation.Pane = owner.Pane
			observation.Incarnation = owner.Incarnation
			text, captureErr := capturer.CapturePane(ctx, selected)
			observation.CapturedAt = time.Now().UTC().Format(time.RFC3339Nano)
			if captureErr != nil {
				// A failed second target must not expose the first target's text.
				result.Reason = "native related-pane capture failed: " + captureErr.Error()
				result.Observations = nil
				break
			} else {
				observation.Text = text
				digest := sha256.Sum256([]byte(text))
				observation.Fingerprint = "sha256:" + hex.EncodeToString(digest[:])
			}
		}
		result.Observations = append(result.Observations, observation)
	}
	if err := m.saveCapture(ctx, rt, snapshot, msg, &result); err != nil {
		// Final verification or persistence failure invalidates every target.
		result.Reason = "capture persistence unavailable: " + err.Error()
		result.Observations = nil
	}
	return result
}

// saveUnknownCapture records only the reason when native workspace identity
// cannot be trusted. It uses the already-established private state root, never
// a workspace path inferred from a failed snapshot. An unreadable receipt is
// not advertised as durable evidence.
func (m Mail) saveUnknownCapture(result *CaptureResult) {
	if err := m.persistUnknownCapture(result); err != nil {
		result.Reason += "; durability unavailable: " + err.Error()
		result.Path = ""
	}
}

func (m Mail) persistUnknownCapture(result *CaptureResult) error {
	if !filepath.IsAbs(m.Store.StateRoot) {
		return errors.New("state root must be absolute")
	}
	info, err := os.Lstat(m.Store.StateRoot)
	if err != nil || !privateInfo(info, true) {
		return errors.New("private state root unavailable")
	}
	base, err := os.OpenRoot(m.Store.StateRoot)
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(base.Close)
	opened, err := base.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return ErrUnknown
	}
	receipts, err := privateDir(base, "unknown-captures", true)
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(receipts.Close)
	name := result.MessageID + ".json"
	if err := writeJSON(receipts, name, result); err != nil {
		return err
	}
	if err := syncDirectory(receipts); err != nil {
		return err
	}
	var saved CaptureResult
	if err := readJSON(receipts, name, &saved); err != nil || saved.MessageID != result.MessageID || saved.Reason != result.Reason || len(saved.Observations) != 0 {
		return errors.New("unknown capture receipt could not be verified")
	}
	result.Path = filepath.Join(m.Store.StateRoot, "unknown-captures", name)
	return nil
}

func validateCaptureSnapshot(snapshot Snapshot, msg Message) error {
	if _, err := validateSnapshot(snapshot); err != nil {
		return err
	}
	if snapshot.Caller.Role != msg.From && snapshot.Caller.Role != msg.To {
		return ErrUnknown
	}
	for _, role := range []string{msg.From, msg.To} {
		found := false
		for _, binding := range snapshot.Bindings {
			found = found || binding.Role == role
		}
		for _, absent := range snapshot.AbsentRoles {
			found = found || absent == role
		}
		if !found {
			return ErrUnknown
		}
	}
	return nil
}

func sameCaptureTarget(a, b Snapshot, role string) bool {
	var left, right Binding
	var leftFound, rightFound bool
	for _, binding := range a.Bindings {
		if binding.Role == role {
			left, leftFound = binding, true
			break
		}
	}
	for _, binding := range b.Bindings {
		if binding.Role == role {
			right, rightFound = binding, true
			break
		}
	}
	if leftFound || rightFound {
		return leftFound && rightFound && left == right
	}
	var leftAbsent, rightAbsent bool
	for _, absent := range a.AbsentRoles {
		leftAbsent = leftAbsent || absent == role
	}
	for _, absent := range b.AbsentRoles {
		rightAbsent = rightAbsent || absent == role
	}
	return leftAbsent && rightAbsent
}

type pendingCaptureOwner struct {
	Version   int    `json:"version"`
	MessageID string `json:"message_id"`
	Attempt   string `json:"attempt,omitempty"`
	Device    uint64 `json:"device"`
	Inode     uint64 `json:"inode"`
}

// scanCaptureEntries bounds both memory and foreground directory work. A
// directory over the budget stops capture safely until the durable cursor can
// take over; callbacks validate ownership before acting on each entry.
var errCaptureScanBudget = errors.New("capture scan budget exhausted")

func scanCaptureEntries(root *os.Root, visit func(os.DirEntry) error) error {
	visited := 0
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	for {
		entries, err := file.ReadDir(256)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		for _, entry := range entries {
			if visited >= captureScanLimit {
				return errCaptureScanBudget
			}
			visited++
			if err := visit(entry); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}

func pendingOwnerName(pending string) string { return pending + ".owner" }

// The legacy unsuffixed name remains readable. New attempts use a random
// suffix so a crash before owner creation cannot block the next retry.
func capturePendingIdentity(name string) (string, string, bool) {
	const suffix = ".json.pending"
	if !strings.HasPrefix(name, ".") {
		return "", "", false
	}
	base := strings.TrimPrefix(name, ".")
	at := strings.Index(base, suffix)
	if at < 0 || !messageID(base[:at]) {
		return "", "", false
	}
	id, tail := base[:at], base[at+len(suffix):]
	if tail == "" {
		return id, "", true
	}
	if len(tail) != 33 || tail[0] != '.' || strings.ToLower(tail[1:]) != tail[1:] {
		return "", "", false
	}
	if _, err := hex.DecodeString(tail[1:]); err != nil {
		return "", "", false
	}
	return id, tail[1:], true
}

func newPendingCaptureName(root *os.Root, id string) (string, error) {
	for range 8 {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", err
		}
		name := "." + id + ".json.pending." + hex.EncodeToString(random[:])
		if _, err := root.Lstat(name); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if _, err := root.Lstat(pendingOwnerName(name)); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return name, nil
	}
	return "", errors.New("could not allocate unique capture attempt")
}

func pendingOwnerMatches(root *os.Root, pending, id string, info os.FileInfo) (bool, error) {
	ownerName := pendingOwnerName(pending)
	ownerInfo, err := root.Lstat(ownerName)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !privateInfo(ownerInfo, false) {
		return false, errors.New("unsafe pending owner")
	}
	var owner pendingCaptureOwner
	if err := readJSON(root, ownerName, &owner); err != nil {
		return false, err
	}
	stat := info.Sys().(*syscall.Stat_t)
	pendingID, attempt, validName := capturePendingIdentity(pending)
	if !validName || pendingID != id || owner.Version != 1 || owner.MessageID != id || owner.Attempt != attempt || owner.Device != uint64(stat.Dev) || owner.Inode != uint64(stat.Ino) {
		return false, errors.New("pending owner does not match inode")
	}
	return true, nil
}

// Pending names are reserved for capture publication. An O_EXCL collision is
// never ours; only a successful create grants cleanup ownership in this call.
func writePendingCapture(root *os.Root, name string, value CaptureResult) (bool, bool, error) {
	return writePendingCaptureWithHook(root, name, value, nil)
}

func writePendingCaptureWithHook(root *os.Root, name string, value CaptureResult, afterPartial func()) (bool, bool, error) {
	id, attempt, validName := capturePendingIdentity(name)
	if !validName || id != value.MessageID {
		return false, false, errors.New("invalid pending capture name")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return false, false, err
	}
	data = append(data, '\n')
	if len(data) > 64*1024 {
		return false, false, errors.New("oversized capture record")
	}
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return false, false, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return true, false, err
	}
	stat := info.Sys().(*syscall.Stat_t)
	owner := pendingCaptureOwner{Version: 1, MessageID: value.MessageID, Attempt: attempt, Device: uint64(stat.Dev), Inode: uint64(stat.Ino)}
	record, err := json.Marshal(owner)
	if err != nil {
		_ = file.Close()
		return true, false, err
	}
	marker, err := root.OpenFile(pendingOwnerName(name), os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		_ = file.Close()
		return true, false, err
	}
	record = append(record, '\n')
	n, markerErr := marker.Write(record)
	if markerErr == nil && n != len(record) {
		markerErr = errors.New("short pending owner write")
	}
	if markerErr == nil {
		markerErr = marker.Sync()
	}
	markerCloseErr := marker.Close()
	if markerErr == nil {
		markerErr = markerCloseErr
	}
	if markerErr == nil {
		markerErr = syncDirectory(root)
	}
	if markerErr != nil {
		_ = file.Close()
		return true, true, markerErr
	}
	// A kill at this point leaves a durable owner record for partial text.
	split := len(data)
	if afterPartial != nil {
		split = len(data) / 2
	}
	n, writeErr := file.Write(data[:split])
	if writeErr == nil && n != split {
		writeErr = errors.New("short staged capture write")
	}
	if writeErr == nil && afterPartial != nil {
		writeErr = file.Sync()
		if writeErr == nil {
			afterPartial()
		}
	}
	if writeErr == nil && split < len(data) {
		n, writeErr = file.Write(data[split:])
		if writeErr == nil && n != len(data)-split {
			writeErr = errors.New("short staged capture write")
		}
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return true, true, writeErr
	}
	return true, true, closeErr
}

func privatePendingInfo(info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && (stat.Nlink == 1 || stat.Nlink == 2)
}

// Recover the exact prior publication window under WithVerified's lock. A
// pending-only record with a matching ID can be discarded and retried; a
// linked pair is made single-link and advertised as an existing capture.
// Invalid or unrelated occupants are left untouched and fail closed.
func recoverPendingCapture(root *os.Root, pending, name, id, path string, result *CaptureResult) (bool, error) {
	staged, err := root.Lstat(pending)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !privatePendingInfo(staged) {
		return false, errors.New("unsafe pending capture")
	}
	marked, err := pendingOwnerMatches(root, pending, id, staged)
	if err != nil {
		return false, err
	}
	if !marked {
		return false, errors.New("pending capture lacks owner marker")
	}
	final, finalErr := root.Lstat(name)
	if finalErr == nil {
		if !os.SameFile(staged, final) {
			return false, errors.New("unrelated pending capture")
		}
		if err := root.Remove(pending); err != nil {
			result.Path = path
			return true, fmt.Errorf("recover linked capture pending: %w", err)
		}
		if marked {
			if err := root.Remove(pendingOwnerName(pending)); err != nil {
				result.Path = path
				return true, fmt.Errorf("recover linked capture owner: %w", err)
			}
		}
		if err := syncDirectory(root); err != nil {
			result.Path = path
			return true, fmt.Errorf("sync recovered capture: %w", err)
		}
		var saved CaptureResult
		if err := readJSON(root, name, &saved); err != nil || saved.Version != 1 || saved.MessageID != id {
			result.Path = path
			return true, errors.New("recovered capture could not be verified")
		}
		result.Path = path
		return true, errors.New("prior published capture recovered")
	}
	if !errors.Is(finalErr, os.ErrNotExist) {
		return false, finalErr
	}
	if err := root.Remove(pending); err != nil {
		return false, fmt.Errorf("remove orphan pending capture: %w", err)
	}
	if marked {
		if err := root.Remove(pendingOwnerName(pending)); err != nil {
			return false, fmt.Errorf("remove orphan pending owner: %w", err)
		}
	}
	if err := syncDirectory(root); err != nil {
		return false, fmt.Errorf("sync orphan removal: %w", err)
	}
	return false, nil
}

// Recover only marked, inode-bound pending files for this message ID.
// An unmarked collision stays untouched while a new nonce permits retry.
func recoverPriorPendingCaptures(root *os.Root, name, id, path string, result *CaptureResult) (bool, error) {
	recovered := false
	err := scanCaptureEntries(root, func(entry os.DirEntry) error {
		pending := entry.Name()
		pendingID, _, validName := capturePendingIdentity(pending)
		if !validName || pendingID != id {
			return nil
		}
		staged, err := root.Lstat(pending)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !privatePendingInfo(staged) {
			return nil
		}
		marked, markerErr := pendingOwnerMatches(root, pending, id, staged)
		final, err := root.Lstat(name)
		if err == nil && os.SameFile(staged, final) && (markerErr != nil || !marked) {
			return errors.New("unmarked linked capture is ambiguous")
		}
		if markerErr != nil || !marked {
			return nil
		}
		if err == nil && !os.SameFile(staged, final) {
			return nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		recovered, err = recoverPendingCapture(root, pending, name, id, path, result)
		return err
	})
	return recovered, err
}

// Retention removes only expired, verified pending inodes. The streaming
// walk can clean marked residue even after more than 2048 crashed attempts.
func pruneOldPendingCaptures(root *os.Root, now time.Time) error {
	err := scanCaptureEntries(root, func(entry os.DirEntry) error {
		name := entry.Name()
		id, _, validName := capturePendingIdentity(name)
		if !validName {
			return nil
		}
		info, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil || !privatePendingInfo(info) || info.ModTime().After(now) || now.Sub(info.ModTime()) <= captureRetention {
			return nil
		}
		marked, err := pendingOwnerMatches(root, name, id, info)
		if err != nil || !marked {
			return nil
		}
		if stat := info.Sys().(*syscall.Stat_t); stat.Nlink == 2 {
			final, err := root.Lstat(id + ".json")
			if err != nil || !os.SameFile(info, final) {
				return nil
			}
		}
		if err := root.Remove(name); err != nil {
			return fmt.Errorf("prune owned pending capture: %w", err)
		}
		if err := root.Remove(pendingOwnerName(name)); err != nil {
			return fmt.Errorf("prune owned pending owner: %w", err)
		}
		return nil
	})
	// A budget error can arrive after exact owned entries were removed.
	// Sync those removals even while refusing further foreground work.
	return errors.Join(err, syncDirectory(root))
}

func (m Mail) saveCapture(ctx context.Context, rt Runtime, observed Snapshot, msg Message, result *CaptureResult) error {
	return m.Store.WithVerified(ctx, rt, func(ws *os.Root, fresh Snapshot) (saveErr error) {
		if fresh.Root != observed.Root || fresh.Caller != observed.Caller ||
			!sameRuntime(fresh.Caller.Owner, observed.Caller.Owner) ||
			validateCaptureSnapshot(fresh, msg) != nil {
			return ErrUnknown
		}
		if !sameCaptureTarget(observed, fresh, msg.From) ||
			!sameCaptureTarget(observed, fresh, msg.To) {
			return errors.New("related capture binding changed")
		}
		agents, err := privateDir(ws, "agents", false)
		if err != nil {
			return err
		}
		defer func() { _ = agents.Close() }()
		agent, err := privateDir(agents, fresh.Caller.Role, false)
		if err != nil {
			return err
		}
		defer func() { _ = agent.Close() }()
		captures, err := privateDir(agent, "captures", true)
		if err != nil {
			return err
		}
		defer func() { _ = captures.Close() }()
		// Run pending retention before publication, including failed capture attempts.
		if err := pruneOldPendingCaptures(captures, time.Now().UTC()); err != nil {
			return err
		}
		name := result.MessageID + ".json"
		path := filepath.Join(m.Store.StateRoot, mustName(fresh.Root), "agents", fresh.Caller.Role, "captures", name)
		if recovered, err := recoverPriorPendingCaptures(captures, name, result.MessageID, path, result); err != nil {
			return err
		} else if recovered {
			return errors.New("prior published capture recovered")
		}
		if _, err := captures.Lstat(name); err == nil {
			result.Path = path
			return errors.New("mail capture already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		pending, err := newPendingCaptureName(captures, result.MessageID)
		if err != nil {
			return err
		}
		ownedPending, ownedOwner := false, false
		defer func() {
			if ownedPending {
				if err := captures.Remove(pending); err != nil && !errors.Is(err, os.ErrNotExist) {
					saveErr = errors.Join(saveErr, fmt.Errorf("cleanup owned pending capture: %w", err))
					return
				}
				ownedPending = false
			}
			if ownedOwner {
				if err := captures.Remove(pendingOwnerName(pending)); err != nil && !errors.Is(err, os.ErrNotExist) {
					saveErr = errors.Join(saveErr, fmt.Errorf("cleanup owned pending owner: %w", err))
				}
			}
		}()
		created, ownerCreated, err := writePendingCapture(captures, pending, *result)
		ownedPending, ownedOwner = created, ownerCreated
		if err != nil {
			return err
		}
		if err := m.step("capture-after-write"); err != nil {
			return err
		}
		var saved CaptureResult
		if err := readJSON(captures, pending, &saved); err != nil ||
			saved.Version != 1 || saved.MessageID != result.MessageID ||
			saved.CapturedAt != result.CapturedAt || len(saved.Observations) != len(result.Observations) {
			return errors.New("staged capture could not be verified")
		}
		if err := m.step("capture-after-readback"); err != nil {
			return err
		}
		// Root.Link creates the final name without replacing a concurrent capture.
		if err := captures.Link(pending, name); err != nil {
			return err
		}
		rollback := func(cause error) error {
			if err := m.step("capture-before-rollback-remove"); err != nil {
				result.Path = path
				return fmt.Errorf("%w; published capture rollback failed: %v", cause, err)
			}
			if err := captures.Remove(name); err != nil {
				result.Path = path
				return fmt.Errorf("%w; published capture rollback failed: %v", cause, err)
			}
			if err := m.step("capture-before-rollback-sync"); err != nil {
				result.Path = path
				return fmt.Errorf("%w; capture rollback sync failed: %v", cause, err)
			}
			if err := syncDirectory(captures); err != nil {
				// Unlink might not survive a crash. Report the possible-positive path.
				result.Path = path
				return fmt.Errorf("%w; capture rollback sync failed: %v", cause, err)
			}
			return cause
		}
		// A hard link has two names until staging is removed; privateInfo
		// requires one link before final readback.
		if err := m.step("capture-before-pending-remove"); err != nil {
			return rollback(err)
		}
		if err := captures.Remove(pending); err != nil {
			return rollback(fmt.Errorf("remove staged capture: %w", err))
		}
		ownedPending = false
		if err := syncDirectory(captures); err != nil {
			return rollback(err)
		}
		if err := readJSON(captures, name, &saved); err != nil ||
			saved.Version != 1 || saved.MessageID != result.MessageID ||
			saved.CapturedAt != result.CapturedAt || len(saved.Observations) != len(result.Observations) {
			return rollback(errors.New("published capture could not be verified"))
		}
		if err := m.step("capture-after-publish"); err != nil {
			return rollback(err)
		}
		result.Path = path
		// Retention runs only after publication; failure preserves positive evidence.
		if err := pruneCapturesKeeping(captures, time.Now().UTC(), name); err != nil {
			result.Reason = "capture retention unavailable: " + err.Error()
			return nil
		}
		if err := m.step("capture-after-prune"); err != nil {
			result.Reason = "capture retention unavailable: " + err.Error()
		}
		return nil
	})
}

func mustName(root string) string {
	name, _ := Name(root) // validated by WithVerified
	return name
}

func pruneCaptures(root *os.Root, now time.Time) error {
	return pruneCapturesKeeping(root, now, "")
}

// pruneCapturesKeeping reserves the just-written record even when the clock
// moved backward and older records have timestamps in the future.
func pruneCapturesKeeping(root *os.Root, now time.Time, protected string) error {
	return pruneCapturesKeepingWithRemove(root, now, protected, root.Remove)
}

func pruneCapturesKeepingWithRemove(root *os.Root, now time.Time, protected string, remove func(string) error) (result error) {
	if err := pruneOldPendingCaptures(root, now); err != nil {
		return err
	}
	dirty := false
	defer func() {
		if dirty {
			result = errors.Join(result, syncDirectory(root))
		}
	}()
	type owned struct {
		name string
		when time.Time
	}
	var keep []owned
	var expired []string
	err := scanCaptureEntries(root, func(entry os.DirEntry) error {
		name := entry.Name()
		id := strings.TrimSuffix(name, ".json")
		if name != id+".json" || !messageID(id) {
			return nil // never prune an unrelated file
		}
		info, err := root.Lstat(name)
		if err != nil || !privateInfo(info, false) {
			return nil
		}
		var saved CaptureResult
		if readJSON(root, name, &saved) != nil || saved.Version != 1 || saved.MessageID != id {
			return nil
		}
		when, err := time.Parse(time.RFC3339Nano, saved.CapturedAt)
		if err != nil {
			return nil
		}
		if !when.After(now) && now.Sub(when) > captureRetention {
			expired = append(expired, name)
			return nil
		}
		keep = append(keep, owned{name, when})
		if len(keep) > 2048 {
			return errors.New("capture record limit exceeded")
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, name := range expired {
		dirty = true
		if err := remove(name); err != nil {
			return fmt.Errorf("prune expired capture: %w", err)
		}
	}
	sort.Slice(keep, func(i, j int) bool {
		if keep[i].when.Equal(keep[j].when) {
			return keep[i].name < keep[j].name
		}
		return keep[i].when.Before(keep[j].when)
	})
	for len(keep) > captureLimit {
		removeIndex := 0
		for removeIndex < len(keep) && keep[removeIndex].name == protected {
			removeIndex++
		}
		if removeIndex == len(keep) {
			return errors.New("cannot retain new capture within limit")
		}
		dirty = true
		if err := remove(keep[removeIndex].name); err != nil {
			return fmt.Errorf("prune owned capture: %w", err)
		}
		keep = append(keep[:removeIndex], keep[removeIndex+1:]...)
	}
	return nil
}
