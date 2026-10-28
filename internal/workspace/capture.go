package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	captureLimit     = 128
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
				observation.Reason = "native pane capture failed: " + captureErr.Error()
			} else {
				observation.Text = text
				digest := sha256.Sum256([]byte(text))
				observation.Fingerprint = "sha256:" + hex.EncodeToString(digest[:])
			}
		}
		result.Observations = append(result.Observations, observation)
	}
	if err := m.saveCapture(ctx, rt, snapshot, &result); err != nil {
		result.Reason = "capture persistence unavailable: " + err.Error()
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

func (m Mail) saveCapture(ctx context.Context, rt Runtime, observed Snapshot, result *CaptureResult) error {
	return m.Store.WithVerified(ctx, rt, func(ws *os.Root, fresh Snapshot) error {
		if fresh.Root != observed.Root || fresh.Caller != observed.Caller ||
			!sameRuntime(fresh.Caller.Owner, observed.Caller.Owner) {
			return ErrUnknown
		}
		agents, err := privateDir(ws, "agents", false)
		if err != nil {
			return err
		}
		defer func(closeFn func() error) { _ = closeFn() }(agents.Close)
		agent, err := privateDir(agents, fresh.Caller.Role, false)
		if err != nil {
			return err
		}
		defer func(closeFn func() error) { _ = closeFn() }(agent.Close)
		captures, err := privateDir(agent, "captures", true)
		if err != nil {
			return err
		}
		defer func(closeFn func() error) { _ = closeFn() }(captures.Close)
		name := result.MessageID + ".json"
		// A duplicate attempt never overwrites an earlier observation.
		if _, err := captures.Lstat(name); err == nil {
			return errors.New("mail capture already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := writeJSON(captures, name, result); err != nil {
			return err
		}
		if err := pruneCapturesKeeping(captures, time.Now().UTC(), name); err != nil {
			return err
		}
		var saved CaptureResult
		if err := readJSON(captures, name, &saved); err != nil || saved.MessageID != result.MessageID || saved.CapturedAt != result.CapturedAt {
			return errors.New("saved capture could not be verified after retention")
		}
		result.Path = filepath.Join(m.Store.StateRoot, mustName(fresh.Root), "agents", fresh.Caller.Role, "captures", name)
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
	entries, err := boundedEntries(root)
	if err != nil {
		return err
	}
	type owned struct {
		name string
		when time.Time
	}
	var keep []owned
	for _, entry := range entries {
		name := entry.Name()
		id := strings.TrimSuffix(name, ".json")
		if name != id+".json" || !messageID(id) {
			continue // never prune an unrelated file
		}
		info, err := root.Lstat(name)
		if err != nil || !privateInfo(info, false) {
			continue
		}
		var saved CaptureResult
		if readJSON(root, name, &saved) != nil || saved.Version != 1 || saved.MessageID != id {
			continue
		}
		when, err := time.Parse(time.RFC3339Nano, saved.CapturedAt)
		if err != nil {
			continue
		}
		if !when.After(now) && now.Sub(when) > captureRetention {
			if err := root.Remove(name); err != nil {
				return err
			}
			continue
		}
		keep = append(keep, owned{name, when})
	}
	sort.Slice(keep, func(i, j int) bool {
		if keep[i].when.Equal(keep[j].when) {
			return keep[i].name < keep[j].name
		}
		return keep[i].when.Before(keep[j].when)
	})
	for len(keep) > captureLimit {
		remove := 0
		for remove < len(keep) && keep[remove].name == protected {
			remove++
		}
		if remove == len(keep) {
			return errors.New("cannot retain new capture within limit")
		}
		if err := root.Remove(keep[remove].name); err != nil {
			return fmt.Errorf("prune owned capture: %w", err)
		}
		keep = append(keep[:remove], keep[remove+1:]...)
	}
	return syncDirectory(root)
}
