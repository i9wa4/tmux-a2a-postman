package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	SnapshotPaneLimit = 64
	SnapshotLineLimit = 30
	SnapshotTextLimit = 8 * 1024
	SnapshotBudget    = time.Second
)

type savedPane struct {
	Version     int    `json:"version"`
	Owner       Owner  `json:"owner"`
	PanePID     string `json:"pane_pid"`
	Window      string `json:"window"`
	CapturedAt  string `json:"captured_at"`
	Fingerprint string `json:"fingerprint"`
	Text        string `json:"text"`
	Truncated   bool   `json:"truncated"`
}

type paneClock struct {
	Owner           Owner   `json:"owner"`
	PanePID         string  `json:"pane_pid"`
	CapturedAt      string  `json:"captured_at"`
	ScreenChangedAt *string `json:"screen_changed_at"`
	Fingerprint     string  `json:"fingerprint"`
	File            string  `json:"file"`
}

type snapshotIndex struct {
	Version    int                  `json:"version"`
	Owner      Owner                `json:"owner"`
	Generation uint64               `json:"generation"`
	Panes      map[string]paneClock `json:"panes"`
}

type snapshotPaneResult struct {
	Pane   string `json:"pane"`
	File   string `json:"file,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type SessionSnapshot struct {
	Version     int                  `json:"version"`
	ID          string               `json:"id"`
	Owner       Owner                `json:"owner"`
	StartedAt   string               `json:"started_at"`
	CompletedAt string               `json:"completed_at"`
	Discovered  int                  `json:"discovered"`
	Captured    int                  `json:"captured"`
	Skipped     int                  `json:"skipped"`
	Complete    bool                 `json:"complete"`
	Enumerated  bool                 `json:"enumerated"`
	Panes       []snapshotPaneResult `json:"panes"`
	Reason      string               `json:"reason,omitempty"`
}

func observationDir(ws *os.Root, create bool) (*os.Root, error) {
	runtime, err := privateDir(ws, "runtime", false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = runtime.Close() }()
	return privateDir(runtime, "observations", create)
}

// Publish a complete immutable observation without clobbering an older file.
// Readers of the latest index never see a partially written final record.
func createObservation(dir *os.Root, name string, value any) error {
	temp, err := randomName()
	if err != nil {
		return err
	}
	defer func() {
		if temp != "" {
			_ = dir.Remove(temp)
		}
	}()
	if err := writeJSON(dir, temp, value); err != nil {
		return err
	}
	if err := dir.Link(temp, name); err != nil {
		return err
	}
	if err := dir.Remove(temp); err != nil {
		return err
	}
	temp = ""
	return syncDirectory(dir)
}

type captureCaller struct {
	Runtime
	expected Binding
	root     string
}

func (r captureCaller) Snapshot(ctx context.Context) (Snapshot, error) {
	s, err := r.Runtime.Snapshot(ctx)
	if err != nil {
		return s, err
	}
	if s.Caller != r.expected || s.Root != r.root {
		return Snapshot{}, ErrUnknown
	}
	return s, nil
}

func (m Mail) captureAdmission(ctx context.Context, rt Runtime, expected Binding) (*os.File, snapshotIndex, error) {
	var lock *os.File
	var index snapshotIndex
	err := m.Store.WithVerified(ctx, rt, func(ws *os.Root, s Snapshot) error {
		if s.Caller != expected {
			return ErrUnknown
		}
		dir, err := observationDir(ws, true)
		if err != nil {
			return err
		}
		defer func() { _ = dir.Close() }()
		file, err := dir.OpenFile("capture.lock", os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
		if errors.Is(err, os.ErrExist) {
			file, err = dir.OpenFile("capture.lock", os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		}
		if err != nil {
			return err
		}
		keep := false
		defer func() {
			if !keep {
				_ = file.Close()
			}
		}()
		info, err := file.Stat()
		if err != nil || !privateInfo(info, false) {
			return ErrUnknown
		}
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			if errors.Is(err, syscall.EWOULDBLOCK) {
				return ErrBusy
			}
			return err
		}
		pathInfo, err := dir.Lstat("capture.lock")
		if err != nil || !os.SameFile(info, pathInfo) {
			return ErrUnknown
		}
		err = readJSON(dir, "index.json", &index)
		if errors.Is(err, os.ErrNotExist) {
			index = snapshotIndex{Version: 1, Owner: s.Caller.Owner, Panes: map[string]paneClock{}}
		} else if err != nil {
			return err
		}
		if index.Version != 1 || index.Generation == ^uint64(0) || index.Panes == nil || len(index.Panes) > SnapshotPaneLimit {
			return ErrUnknown
		}
		if !sameRuntime(index.Owner, s.Caller.Owner) {
			index.Panes = map[string]paneClock{}
		}
		index.Owner = s.Caller.Owner
		index.Generation++
		if err := replaceJSON(dir, "index.json", index); err != nil {
			return err
		}
		keep = true
		lock = file
		return nil
	})
	return lock, index, err
}

func snapshotText(frame paneFrame) (string, bool) {
	lines := strings.Split(frame.Screen, "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if frame.CursorY+1 > end && frame.CursorY < len(lines) {
		end = frame.CursorY + 1
	}
	start := 0
	if end > SnapshotLineLimit {
		start = end - SnapshotLineLimit
	}
	text := strings.Join(lines[start:end], "\n")
	truncated := start > 0
	if len(text) > SnapshotTextLimit {
		text = text[len(text)-SnapshotTextLimit:]
		for len(text) > 0 && !utf8.RuneStart(text[0]) {
			text = text[1:]
		}
		truncated = true
	}
	return text, truncated
}

// CaptureSession is finite, event-triggered and synchronous. It never holds
// the mailbox lock while asking tmux for text. History is immutable and never
// pruned automatically; the small index is only a pointer to current facts.
func (m Mail) CaptureSession(ctx context.Context, rt Tmux, expected Binding) SessionSnapshot {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), SnapshotBudget)
	defer cancel()
	result := SessionSnapshot{Version: 1, Owner: expected.Owner, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Panes: []snapshotPaneResult{}}
	nonce, err := randomName()
	if err != nil {
		result.Reason = err.Error()
		return result
	}
	result.ID = "s-" + strings.TrimPrefix(nonce, ".new-")
	snapshot, err := rt.Snapshot(ctx)
	if err != nil || snapshot.Caller != expected {
		result.Reason = "caller changed before session capture"
		result.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		return result
	}
	pinned := captureCaller{rt, expected, snapshot.Root}
	recordFailure := func() SessionSnapshot {
		result.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		saveCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		_ = retryNoticeAdmission(saveCtx, func() error {
			return m.Store.WithVerified(saveCtx, pinned, func(ws *os.Root, s Snapshot) error {
				if s.Caller != expected {
					return ErrUnknown
				}
				dir, err := observationDir(ws, true)
				if err != nil {
					return err
				}
				defer func() { _ = dir.Close() }()
				if err := createObservation(dir, result.ID+".json", result); err != nil {
					return err
				}
				return syncDirectory(dir)
			})
		})
		return result
	}
	lock, index, err := m.captureAdmission(ctx, pinned, expected)
	if err != nil {
		result.Reason = err.Error()
		return recordFailure()
	}
	defer func() { _ = lock.Close() }()
	panes, err := rt.sessionPanes(ctx, snapshot)
	if err != nil {
		result.Reason = err.Error()
		return recordFailure()
	}
	result.Discovered = len(panes)
	result.Enumerated = true
	saved := map[string]savedPane{}
	for i, pane := range panes {
		entry := snapshotPaneResult{Pane: pane.Owner.Pane}
		switch {
		case i >= SnapshotPaneLimit:
			entry.Reason = "session pane limit"
		case pane.Linked:
			entry.Reason = "window is linked to another session"
		case pane.Dead:
			entry.Reason = "pane is dead"
		case ctx.Err() != nil:
			entry.Reason = "session capture deadline"
		default:
			frame, err := rt.readPaneHistory(ctx, pane)
			if err != nil {
				entry.Reason = err.Error()
			} else {
				text, truncated := snapshotText(frame)
				digest := sha256.Sum256([]byte(text))
				observation := savedPane{Version: 1, Owner: frame.Pane.Owner, PanePID: frame.Pane.PID, Window: frame.Pane.Window, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano), Fingerprint: "sha256:" + hex.EncodeToString(digest[:]), Text: text, Truncated: truncated}
				entry.File = result.ID + "-p" + strings.TrimPrefix(pane.Owner.Pane, "%") + ".json"
				saved[entry.File] = observation
			}
		}
		if entry.Reason != "" {
			result.Skipped++
		} else {
			result.Captured++
		}
		if len(result.Panes) < SnapshotPaneLimit {
			result.Panes = append(result.Panes, entry)
		}
	}
	result.Complete = result.Skipped == 0 && result.Captured == result.Discovered
	result.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	// Persistence has a separate small bound so an expired capture budget does
	// not silently discard a completed observation or claim mail success failed.
	saveCtx, saveCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer saveCancel()
	err = m.Store.WithVerified(saveCtx, pinned, func(ws *os.Root, current Snapshot) error {
		if current.Caller != expected {
			return ErrUnknown
		}
		dir, err := observationDir(ws, false)
		if err != nil {
			return err
		}
		defer func() { _ = dir.Close() }()
		var fresh snapshotIndex
		if readJSON(dir, "index.json", &fresh) != nil || fresh.Version != 1 || fresh.Generation != index.Generation || !sameRuntime(fresh.Owner, expected.Owner) {
			return errors.New("capture superseded by newer native evidence")
		}
		next := snapshotIndex{Version: 1, Owner: expected.Owner, Generation: index.Generation, Panes: map[string]paneClock{}}
		for i, pane := range panes {
			if i >= SnapshotPaneLimit {
				break
			}
			if prior, ok := index.Panes[pane.Owner.Pane]; ok && prior.Owner == pane.Owner && prior.PanePID == pane.PID {
				next.Panes[pane.Owner.Pane] = prior
			}
		}
		for file, record := range saved {
			if err := createObservation(dir, file, record); err != nil {
				return err
			}
			clock := paneClock{Owner: record.Owner, PanePID: record.PanePID, CapturedAt: record.CapturedAt, Fingerprint: record.Fingerprint, File: file}
			prior, ok := next.Panes[record.Owner.Pane]
			before, beforeErr := time.Parse(time.RFC3339Nano, prior.CapturedAt)
			current, currentErr := time.Parse(time.RFC3339Nano, record.CapturedAt)
			if ok && beforeErr == nil && currentErr == nil && !before.After(current) {
				clock.ScreenChangedAt = prior.ScreenChangedAt
				if prior.Fingerprint != record.Fingerprint {
					when := record.CapturedAt
					clock.ScreenChangedAt = &when
				}
			}
			next.Panes[record.Owner.Pane] = clock
		}
		if err := createObservation(dir, result.ID+".json", result); err != nil {
			return err
		}
		if err := syncDirectory(dir); err != nil {
			return err
		}
		return replaceJSON(dir, "index.json", next)
	})
	if err != nil {
		result.Reason = err.Error()
	}
	return result
}

func readPaneTimes(ws *os.Root, snapshot Snapshot, role string) (*string, *string) {
	var current Binding
	for _, bound := range snapshot.Bindings {
		if bound.Role == role {
			current = bound
		}
	}
	if !current.Owner.valid() || !positivePID(current.PanePID) {
		return nil, nil
	}
	dir, err := observationDir(ws, false)
	if err != nil {
		return nil, nil
	}
	defer func() { _ = dir.Close() }()
	var index snapshotIndex
	if readJSON(dir, "index.json", &index) != nil || index.Version != 1 || !sameRuntime(index.Owner, current.Owner) || len(index.Panes) > SnapshotPaneLimit {
		return nil, nil
	}
	clock, ok := index.Panes[current.Owner.Pane]
	if !ok || clock.Owner != current.Owner || clock.PanePID != current.PanePID {
		return nil, nil
	}
	var record savedPane
	if strings.ContainsAny(clock.File, "/\\") || readJSON(dir, clock.File, &record) != nil || record.Version != 1 || record.Owner != current.Owner || record.PanePID != current.PanePID || record.CapturedAt != clock.CapturedAt || record.Fingerprint != clock.Fingerprint {
		return nil, nil
	}
	digest := sha256.Sum256([]byte(record.Text))
	if record.Fingerprint != "sha256:"+hex.EncodeToString(digest[:]) {
		return nil, nil
	}
	when, err := time.Parse(time.RFC3339Nano, clock.CapturedAt)
	if err != nil || when.After(time.Now().UTC()) {
		return nil, nil
	}
	if clock.ScreenChangedAt != nil {
		changed, err := time.Parse(time.RFC3339Nano, *clock.ScreenChangedAt)
		if err != nil || changed.After(when) {
			return nil, nil
		}
	}
	return &clock.CapturedAt, clock.ScreenChangedAt
}
