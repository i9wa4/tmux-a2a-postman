package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type RefreshOwner struct {
	PID        int    `json:"pid"`
	Invocation string `json:"invocation"`
	AcquiredAt string `json:"acquired_at"`
	Target     string `json:"target"`
	Phase      string `json:"phase"`
}
type RefreshResult struct {
	Status   string          `json:"status"`
	AsOf     string          `json:"as_of,omitempty"`
	Coverage *StatusCoverage `json:"coverage,omitempty"`
	Owner    *RefreshOwner   `json:"owner,omitempty"`
	Reason   string          `json:"reason,omitempty"`
}

// RefreshOptions exposes deterministic clocks/fault boundaries only internally.
// Public callers always use the finite production budget and cadence.
type RefreshOptions struct {
	now           func() time.Time
	beforePublish func() error
	afterRename   func() error
	phaseStarted  func(string)
}

func refreshNow(options RefreshOptions) time.Time {
	if options.now != nil {
		return options.now().UTC()
	}
	return time.Now().UTC()
}

func acquireRefresh(dir *os.Root) (*os.File, error) {
	f, err := dir.OpenFile("refresh.lock", os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if errors.Is(err, os.ErrExist) {
		f, err = dir.OpenFile("refresh.lock", os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	}
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !privateInfo(info, false) {
		_ = f.Close()
		return nil, errors.New("unsafe refresh lock")
	}
	// os.OpenFile opens close-on-exec descriptors on both Linux and macOS.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	named, err := dir.Lstat("refresh.lock")
	if err != nil || !os.SameFile(info, named) {
		_ = f.Close()
		return nil, ErrUnknown
	}
	return f, nil
}

func busyRefresh(dir *os.Root, server ServerContext, now time.Time) RefreshResult {
	result := RefreshResult{Status: "skipped_busy"}
	var owner RefreshOwner
	if err := readJSON(dir, "owner.json", &owner); err != nil || owner.PID <= 0 || owner.Invocation == "" || owner.Target != server.Socket || !validPhase(owner.Phase) {
		result.Reason = "busy refresh; owner metadata unavailable or invalid (best effort)"
		return result
	}
	acquired, err := time.Parse(time.RFC3339Nano, owner.AcquiredAt)
	if err != nil || acquired.After(now) {
		result.Reason = "busy refresh; owner age unknown (invalid or future metadata)"
		return result
	}
	result.Owner = &owner
	if now.Sub(acquired) > StatusRefreshBudget+time.Second {
		result.Reason = fmt.Sprintf("long-held/suspected stalled refresh: PID %d invocation %s phase %s age %s; metadata is best effort, not lock authority", owner.PID, owner.Invocation, owner.Phase, now.Sub(acquired).Round(time.Millisecond))
	}
	return result
}

func validPhase(phase string) bool {
	return phase == "enumerate" || phase == "capture" || phase == "aggregate" || phase == "publish"
}

// RefreshServerStatus is the only persistent monitoring/capture writer. It
// never holds a mailbox lock across native captures and never waits for its
// refresh lock. Notification safety reads do not enter this function.
func (m Mail) RefreshServerStatus(parent context.Context, server ServerContext, home string, options RefreshOptions) (result RefreshResult, retErr error) {
	result.Status = "error"
	ctx, cancel := context.WithTimeout(parent, StatusRefreshBudget)
	defer cancel()
	dir, err := m.Store.statusDir(server, true)
	if err != nil {
		return result, err
	}
	defer func() { _ = dir.Close() }()
	lock, err := acquireRefresh(dir)
	if errors.Is(err, ErrBusy) {
		return busyRefresh(dir, server, refreshNow(options)), nil
	}
	if err != nil {
		return result, err
	}
	defer func() { _ = lock.Close() }()
	id, err := randomName()
	if err != nil {
		return result, err
	}
	started := refreshNow(options)
	owner := RefreshOwner{PID: os.Getpid(), Invocation: strings.TrimPrefix(id, ".new-"), AcquiredAt: started.Format(time.RFC3339Nano), Target: server.Socket}
	phase := func(name string) error {
		owner.Phase = name
		if err := replaceJSON(dir, "owner.json", owner); err != nil {
			return err
		}
		if options.phaseStarted != nil {
			options.phaseStarted(name)
		}
		return nil
	}
	if err := phase("enumerate"); err != nil {
		return result, err
	}
	rt := Tmux{Socket: server.Socket, Home: home}
	snapshot, err := rt.enumerateStatus(ctx, server)
	if err != nil {
		return result, err
	}
	snapshot.StartedAt = started.Format(time.RFC3339Nano)
	prior := ServerStatus{}
	if raw, e := readStatusFile(dir, "status.json"); e == nil {
		prior, _ = decodeStatus(raw, server)
	}
	if prior.Server.Incarnation != snapshot.Server.Incarnation || prior.Server.CanonicalSocket != snapshot.Server.CanonicalSocket {
		prior = ServerStatus{}
	}
	type captureTask struct {
		session, window, pane int
		old                   *StatusCapture
	}
	tasks := []captureTask{}
	for si := range snapshot.Sessions {
		session := &snapshot.Sessions[si]
		for wi := range session.Windows {
			window := &session.Windows[wi]
			for pi := range window.Panes {
				pane := &window.Panes[pi]
				var old *StatusCapture
				for _, ps := range prior.Sessions {
					if ps.ID != session.ID {
						continue
					}
					for _, pw := range ps.Windows {
						if pw.ID != window.ID {
							continue
						}
						for _, pp := range pw.Panes {
							if pp.ID == pane.ID && pp.PID == pane.PID && pp.Workspace == pane.Workspace && pp.Title == pane.Title && !pp.Dead && !pp.Linked {
								old = pp.Capture
								if pp.Status != nil {
									pane.Status = &NodeStatus{Node: pp.Status.Node, Reason: "saved binding/configuration has not been revalidated"}
								}
							}
						}
					}
				}
				if old != nil {
					at, e := time.Parse(time.RFC3339Nano, old.At)
					if e != nil || at.After(started) {
						old = nil
					}
				}
				if pane.Dead || pane.Linked {
					pane.Reason = "dead or linked pane cannot be attributed safely"
					snapshot.Coverage.Unavailable++
					continue
				}
				if old != nil {
					at, _ := time.Parse(time.RFC3339Nano, old.At)
					if started.Sub(at) < StatusCaptureInterval {
						pane.Capture = old
						snapshot.Coverage.Reused++
						continue
					}
				}
				tasks = append(tasks, captureTask{si, wi, pi, old})
			}
		}
	}
	// Oldest/missing observations are first: partial sweeps do not starve panes.
	sort.SliceStable(tasks, func(i, j int) bool {
		a, b := tasks[i].old, tasks[j].old
		if a == nil {
			return b != nil
		}
		if b == nil {
			return false
		}
		return a.At < b.At
	})
	if err := phase("capture"); err != nil {
		return result, err
	}
	history, err := privateDir(dir, "history", true)
	if err != nil {
		return result, err
	}
	defer func() { _ = history.Close() }()
	// Reserve time for the final identity check, aggregation and atomic commit.
	captureCtx, stopCapture := context.WithTimeout(ctx, time.Second)
	defer stopCapture()
	for i, task := range tasks {
		session := &snapshot.Sessions[task.session]
		window := &session.Windows[task.window]
		pane := &window.Panes[task.pane]
		pane.Capture = task.old
		reason := ""
		switch {
		case i >= SnapshotPaneLimit:
			reason = "capture pane limit"
		case captureCtx.Err() != nil:
			reason = "capture budget exhausted"
		default:
			native := nativePane{Owner: Owner{snapshot.Server.CanonicalSocket, snapshot.Server.Incarnation, session.ID, pane.ID}, PID: pane.PID, Window: window.ID}
			frame, e := rt.readPaneHistory(captureCtx, native)
			if e != nil {
				reason = "capture: " + e.Error()
			} else {
				text, truncated := snapshotText(frame)
				digest := sha256.Sum256([]byte(text))
				at := refreshNow(options).Format(time.RFC3339Nano)
				observation := savedPane{Version: 1, Owner: native.Owner, PanePID: pane.PID, Window: window.ID, CapturedAt: at, Fingerprint: "sha256:" + hex.EncodeToString(digest[:]), Text: text, Truncated: truncated}
				file := owner.Invocation + "-p" + pane.ID[1:] + ".json"
				if e := createObservation(history, file, observation); e != nil {
					reason = "save capture: " + e.Error()
				} else {
					clock := &StatusCapture{At: at, Fingerprint: observation.Fingerprint, File: file}
					oldAt := time.Time{}
					if task.old != nil {
						oldAt, _ = time.Parse(time.RFC3339Nano, task.old.At)
					}
					if task.old != nil && !refreshNow(options).Before(oldAt) {
						clock.ChangedAt = task.old.ChangedAt
						if task.old.Fingerprint != clock.Fingerprint {
							clock.ChangedAt = &clock.At
						}
					}
					pane.Capture = clock
					snapshot.Coverage.Captured++
				}
			}
		}
		if reason != "" {
			pane.Reason = reason
			snapshot.Coverage.Unavailable++
		}
	}
	if err := phase("aggregate"); err != nil {
		return result, err
	}
	m.aggregateStatus(ctx, &snapshot)
	for _, s := range snapshot.Sessions {
		for _, w := range s.Windows {
			for _, p := range w.Panes {
				if p.Status != nil && (p.Status.Reason != "" || p.Status.UnreadCount == nil || p.Status.InboundCount == nil || p.Status.OutboundCount == nil || p.Status.DraftCount == nil || p.Status.FailedCount == nil) {
					snapshot.Coverage.UnknownNodes++
				}
			}
		}
	}
	// Full enumeration must still describe the same runtime and identities. A
	// disappearing/replaced server is fatal: never publish a new healthy frame.
	last, err := rt.enumerateStatus(ctx, server)
	if err != nil {
		return result, fmt.Errorf("final native verification: %w", err)
	}
	if !sameStatusNative(snapshot, last) {
		return result, errors.New("server layout/process identities changed during refresh; previous snapshot retained")
	}
	// automatic-rename can change a window's display name as a console starts
	// or runs a tool. It is not a routing/process identity. Use closing-frame
	// names after all native IDs, roles, directories and geometry still match.
	for si := range snapshot.Sessions {
		for wi := range snapshot.Sessions[si].Windows {
			snapshot.Sessions[si].Windows[wi].Name = last.Sessions[si].Windows[wi].Name
		}
	}
	snapshot.Coverage.Complete = snapshot.Coverage.Unavailable == 0 && snapshot.Coverage.UnknownNodes == 0
	if !snapshot.Coverage.Complete {
		snapshot.Coverage.Reason = "partial observation; individual capture times and row reasons remain authoritative"
	}
	snapshot.AsOf = refreshNow(options).Format(time.RFC3339Nano)
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := phase("publish"); err != nil {
		return result, err
	}
	if err := publishStatus(dir, server, snapshot, options.beforePublish, options.afterRename); err != nil {
		return result, err
	}
	result = RefreshResult{Status: "updated", AsOf: snapshot.AsOf, Coverage: &snapshot.Coverage}
	if !snapshot.Coverage.Complete {
		result.Status = "partial"
		result.Reason = snapshot.Coverage.Reason
		return result, errors.New(result.Reason)
	}
	return result, nil
}

func sameStatusNative(a, b ServerStatus) bool {
	if a.Server != b.Server || len(a.Sessions) != len(b.Sessions) {
		return false
	}
	for i, s := range a.Sessions {
		other := b.Sessions[i]
		if s.ID != other.ID || s.Name != other.Name || len(s.Windows) != len(other.Windows) {
			return false
		}
		for j, w := range s.Windows {
			ow := other.Windows[j]
			if w.ID != ow.ID || w.Index != ow.Index || w.Layout != ow.Layout || len(w.Panes) != len(ow.Panes) {
				return false
			}
			for k, p := range w.Panes {
				op := ow.Panes[k]
				if p.ID != op.ID || p.PID != op.PID || p.Title != op.Title || p.Workspace != op.Workspace || p.Top != op.Top || p.Left != op.Left || p.Dead != op.Dead || p.Linked != op.Linked {
					return false
				}
			}
		}
	}
	return true
}

// enumerateStatus includes ordinary terminals and every session/window. Only
// aggregateStatus can attach saved agent/config/mail facts to those native rows.
func (t Tmux) enumerateStatus(ctx context.Context, server ServerContext) (ServerStatus, error) {
	result := ServerStatus{Version: 1, Sessions: []StatusSession{}}
	prefix := "postman-status-server\t"
	format := prefix + "#{socket_path}\t#{pid}\t#{start_time}"
	out, err := tmuxOutputLimited(ctx, 256*1024, t.Socket, "display-message", "-p", format, ";", "list-sessions", "-F", "S\t#{session_id}\t#{session_name}", ";", "list-windows", "-a", "-F", "W\t#{session_id}\t#{window_id}\t#{window_index}\t#{window_name}\t#{window_layout}", ";", "list-panes", "-a", "-F", "P\t#{session_id}\t#{window_id}\t#{pane_id}\t#{pane_pid}\t#{pane_title}\t#{pane_dead}\t#{window_linked}\t#{pane_top}\t#{pane_left}\t#{pane_current_path}", ";", "display-message", "-p", format)
	if err != nil {
		return result, fmt.Errorf("enumerate native server: %w", err)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) < 2 || len(lines) > 4098 || lines[0] != lines[len(lines)-1] || !strings.HasPrefix(lines[0], prefix) {
		return result, errors.New("invalid or changed native server frame")
	}
	identity := strings.Split(strings.TrimPrefix(lines[0], prefix), "\t")
	if len(identity) != 3 {
		return result, ErrUnknown
	}
	canonical, incarnation, err := serverIdentity(t.Socket, identity[0], identity[1], identity[2])
	if err != nil {
		return result, err
	}
	pid, _ := strconv.Atoi(identity[1])
	if server.PID != 0 && pid != server.PID {
		return result, errors.New("explicit server PID disagrees with native server")
	}
	result.Server = StatusServer{server.Socket, canonical, pid, incarnation}
	sessions := map[string]int{}
	windows := map[string][2]int{}
	panes := map[string]bool{}
	for _, line := range lines[1 : len(lines)-1] {
		f := strings.Split(line, "\t")
		if len(f) < 1 {
			return result, ErrUnknown
		}
		switch f[0] {
		case "S":
			if len(f) != 3 || !statusNativeID(f[1], '$') {
				return result, ErrUnknown
			}
			if _, ok := sessions[f[1]]; ok {
				return result, ErrUnknown
			}
			sessions[f[1]] = len(result.Sessions)
			result.Sessions = append(result.Sessions, StatusSession{ID: f[1], Name: f[2], Windows: []StatusWindow{}})
		case "W":
			if len(f) != 6 || !statusNativeID(f[2], '@') {
				return result, ErrUnknown
			}
			si, ok := sessions[f[1]]
			if !ok {
				return result, ErrUnknown
			}
			idx, e := strconv.Atoi(f[3])
			if e != nil || idx < 0 {
				return result, ErrUnknown
			}
			key := f[1] + "/" + f[2]
			if _, ok := windows[key]; ok {
				return result, ErrUnknown
			}
			windows[key] = [2]int{si, len(result.Sessions[si].Windows)}
			result.Sessions[si].Windows = append(result.Sessions[si].Windows, StatusWindow{ID: f[2], Index: idx, Name: f[4], Layout: f[5], Panes: []StatusPane{}})
		case "P":
			if len(f) != 11 || !statusNativeID(f[3], '%') || !positivePID(f[4]) || (f[6] != "0" && f[6] != "1") || (f[7] != "0" && f[7] != "1") {
				return result, ErrUnknown
			}
			key := f[1] + "/" + f[2]
			where, ok := windows[key]
			if !ok || panes[f[1]+"/"+f[3]] {
				return result, ErrUnknown
			}
			panes[f[1]+"/"+f[3]] = true
			top, e := strconv.Atoi(f[8])
			left, le := strconv.Atoi(f[9])
			if e != nil || le != nil || top < 0 || left < 0 {
				return result, ErrUnknown
			}
			root, re := CanonicalRoot(f[10], t.Home)
			reason := ""
			if re != nil {
				reason = "workspace attribution: " + re.Error()
			}
			p := StatusPane{ID: f[3], PID: f[4], Title: f[5], Workspace: root, Top: top, Left: left, Dead: f[6] == "1", Linked: f[7] == "1", Reason: reason}
			w := &result.Sessions[where[0]].Windows[where[1]]
			w.Panes = append(w.Panes, p)
			result.Coverage.Enumerated++
		default:
			return result, ErrUnknown
		}
	}
	if result.Coverage.Enumerated > 1024 {
		return result, errors.New("native server pane limit exceeded; previous snapshot retained")
	}
	sort.Slice(result.Sessions, func(i, j int) bool {
		a, _ := strconv.Atoi(result.Sessions[i].ID[1:])
		b, _ := strconv.Atoi(result.Sessions[j].ID[1:])
		return a < b
	})
	for si := range result.Sessions {
		windows := result.Sessions[si].Windows
		sort.Slice(windows, func(i, j int) bool { return windows[i].Index < windows[j].Index })
		for wi := range windows {
			p := windows[wi].Panes
			sort.Slice(p, func(i, j int) bool {
				if p[i].Top != p[j].Top {
					return p[i].Top < p[j].Top
				}
				if p[i].Left != p[j].Left {
					return p[i].Left < p[j].Left
				}
				a, _ := strconv.Atoi(p[i].ID[1:])
				b, _ := strconv.Atoi(p[j].ID[1:])
				return a < b
			})
		}
	}
	return result, nil
}
