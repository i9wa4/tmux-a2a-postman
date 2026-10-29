package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// appServerRegistration is written only by the process occupying the
// recipient's native tmux pane. A sender must reverify this binding and the
// Unix peer process before attempting any app-server RPC.
type appServerRegistration struct {
	Version int     `json:"version"`
	Binding Binding `json:"binding"`
	Socket  string  `json:"socket"`
}

type appServerNoticeReservation struct {
	Version   int     `json:"version"`
	AttemptID string  `json:"attempt_id"`
	OwnerPID  int     `json:"owner_pid"`
	MessageID string  `json:"message_id"`
	Target    Binding `json:"target"`
	Socket    string  `json:"socket"`
	State     string  `json:"state"`
	ThreadID  string  `json:"thread_id,omitempty"`
	TurnID    string  `json:"turn_id,omitempty"`
}

func validAppServerSocketPath(path string) bool {
	if !filepath.IsAbs(path) || len(path) >= 100 {
		return false
	}
	parent, err := os.Lstat(filepath.Dir(path))
	return err == nil && privateInfo(parent, true)
}

// RegisterAppServer records an opt-in endpoint before the caller execs into
// Codex app-server. The pane's top-level process PID must be this process;
// nested shells or a sender cannot register an arbitrary recipient endpoint.
func (s Store) RegisterAppServer(ctx context.Context, rt Runtime, socketPath string) error {
	if !validAppServerSocketPath(socketPath) {
		return ErrUnknown
	}
	return s.WithVerified(ctx, rt, func(ws *os.Root, snapshot Snapshot) error {
		if snapshot.Caller.PanePID != strconv.Itoa(os.Getpid()) {
			return fmt.Errorf("%w: registering process does not own pane", ErrUnknown)
		}
		runtimeDir, err := privateDir(ws, "runtime", false)
		if err != nil {
			return err
		}
		defer runtimeDir.Close()
		appServers, err := privateDir(runtimeDir, "appserver", true)
		if err != nil {
			return err
		}
		defer appServers.Close()
		if err := syncDirectory(runtimeDir); err != nil {
			return err
		}
		return replaceJSON(appServers, snapshot.Caller.Role+".json", appServerRegistration{
			Version: 1, Binding: snapshot.Caller, Socket: socketPath,
		})
	})
}

// ReserveAppServerNotice verifies the committed sent copy and recipient binding.
// A live owner may resume its own reserved attempt; another process may
// reclaim it only after the owner is provably dead.
// The caller must durably mark RPCStarted before any app-server write. Once
// started, an ambiguous outcome is never replayed automatically.
func (s Store) ReserveAppServerNotice(ctx context.Context, rt Runtime, msg Message) (appServerNoticeReservation, error) {
	var record appServerNoticeReservation
	if !messageID(msg.ID) || msg.FromSession != "" || msg.ToSession != "" || msg.From == "" || msg.To == "" {
		return record, ErrUnknown
	}
	err := s.WithVerified(ctx, rt, func(ws *os.Root, snapshot Snapshot) error {
		if snapshot.Caller.Role != msg.From {
			return ErrUnknown
		}
		sent, err := openOwnBox(ws, msg.From, "sent")
		if err != nil {
			return err
		}
		committed, err := readMessage(sent, msg.ID+".md")
		_ = sent.Close()
		if err != nil || committed != msg {
			return fmt.Errorf("%w: committed sent copy unavailable", ErrUnknown)
		}
		var target Binding
		for _, candidate := range snapshot.Bindings {
			if candidate.Role == msg.To {
				target = candidate
				break
			}
		}
		if !target.Owner.valid() || !positivePID(target.PanePID) {
			return ErrUnknown
		}
		runtimeDir, err := privateDir(ws, "runtime", false)
		if err != nil {
			return err
		}
		defer runtimeDir.Close()
		appServers, err := privateDir(runtimeDir, "appserver", false)
		if err != nil {
			return err
		}
		defer appServers.Close()
		var registration appServerRegistration
		if err := readJSON(appServers, msg.To+".json", &registration); err != nil ||
			registration.Version != 1 || registration.Binding != target ||
			!validAppServerSocketPath(registration.Socket) {
			return fmt.Errorf("%w: recipient app-server registration is stale or absent", ErrUnknown)
		}
		notices, err := privateDir(runtimeDir, "appserver-notices", true)
		if err != nil {
			return err
		}
		defer notices.Close()
		if err := syncDirectory(runtimeDir); err != nil {
			return err
		}
		var existing appServerNoticeReservation
		err = readAppServerNotice(notices, msg.ID+".json", &existing)
		retry := err == nil
		if retry {
			if existing.Version != 1 || existing.MessageID != msg.ID ||
				existing.Target != target || existing.Socket != registration.Socket ||
				existing.AttemptID == "" || existing.OwnerPID <= 0 {
				return ErrUnknown
			}
			switch existing.State {
			case "dial_failed":
				// No app-server write occurred; another attempt is safe.
			case "reserved":
				if existing.OwnerPID == os.Getpid() {
					// A same-process retry resumes the same attempt. Only one
					// caller can durably transition it to rpc_started under
					// the workspace lock, so duplicate callers cannot write.
					record = existing
					return nil
				}
				if processLiveness(existing.OwnerPID) != Dead {
					return ErrBusy
				}
				// Dead owner and no RPCStarted record prove that a
				// conforming attempt could not have written a frame.
			default:
				return ErrBusy
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: cannot verify notice reservation absence", ErrUnknown)
		}
		attemptID, err := randomName()
		if err != nil {
			return err
		}
		record = appServerNoticeReservation{
			Version: 1, AttemptID: attemptID, OwnerPID: os.Getpid(),
			MessageID: msg.ID, Target: target, Socket: registration.Socket,
			State: "reserved",
		}
		if retry {
			return replaceJSON(notices, msg.ID+".json", record)
		}
		if s.noticePublish != nil {
			return s.noticePublish(notices, msg.ID+".json", record)
		}
		return publishNewAppServerNotice(notices, msg.ID+".json", record, writeJSON)
	})
	if err != nil {
		return appServerNoticeReservation{}, err
	}
	return record, nil
}

// MarkAppServerNoticeDialFailed records a proven pre-RPC failure. It is only
// valid after a failed dial and before any app-server write.
func (s Store) MarkAppServerNoticeDialFailed(ctx context.Context, rt Runtime, msg Message, attemptID string) error {
	return s.transitionAppServerNotice(ctx, rt, msg, attemptID, "reserved", "dial_failed", "", "")
}

// MarkAppServerNoticeRPCStarted must durably succeed before any app-server
// write, including initialize. A failed transition forbids the write.
func (s Store) MarkAppServerNoticeRPCStarted(ctx context.Context, rt Runtime, msg Message, attemptID string) error {
	return s.transitionAppServerNotice(ctx, rt, msg, attemptID, "reserved", "rpc_started", "", "")
}

// RecordAppServerNoticeThreadStarted persists the thread identity before turn/start.
// A crash after this transition is ambiguous and must not replay the notice.
func (s Store) RecordAppServerNoticeThreadStarted(ctx context.Context, rt Runtime, msg Message, attemptID, threadID string) error {
	if threadID == "" {
		return ErrUnknown
	}
	return s.transitionAppServerNotice(ctx, rt, msg, attemptID, "rpc_started", "thread_started", threadID, "")
}

// RecordAppServerNoticeTurnAccepted stores a server response, not a POP
// receipt. A later failure remains ambiguous and cannot authorize replay.
func (s Store) RecordAppServerNoticeTurnAccepted(ctx context.Context, rt Runtime, msg Message, attemptID, threadID, turnID string) error {
	if threadID == "" || turnID == "" {
		return ErrUnknown
	}
	return s.transitionAppServerNotice(ctx, rt, msg, attemptID, "thread_started", "turn_accepted", threadID, turnID)
}

func (s Store) transitionAppServerNotice(ctx context.Context, rt Runtime, msg Message, attemptID, from, to, threadID, turnID string) error {
	if !messageID(msg.ID) || attemptID == "" {
		return ErrUnknown
	}
	return s.WithVerified(ctx, rt, func(ws *os.Root, snapshot Snapshot) error {
		if snapshot.Caller.Role != msg.From {
			return ErrUnknown
		}
		var target Binding
		for _, candidate := range snapshot.Bindings {
			if candidate.Role == msg.To {
				target = candidate
				break
			}
		}
		runtimeDir, err := privateDir(ws, "runtime", false)
		if err != nil {
			return err
		}
		defer runtimeDir.Close()
		notices, err := privateDir(runtimeDir, "appserver-notices", false)
		if err != nil {
			return err
		}
		defer notices.Close()
		var record appServerNoticeReservation
		if err := readAppServerNotice(notices, msg.ID+".json", &record); err != nil ||
			record.Version != 1 || record.MessageID != msg.ID ||
			record.Target != target || record.AttemptID != attemptID ||
			record.OwnerPID != os.Getpid() || record.State != from {
			return ErrUnknown
		}
		record.State, record.ThreadID, record.TurnID = to, threadID, turnID
		return replaceJSON(notices, msg.ID+".json", record)
	})
}

// publishNewAppServerNotice stages a complete, synced record under a private
// temporary name. Link publishes it without replacing a concurrent attempt.
// A crash during the staged write leaves only an untrusted temporary file;
// the final message ID remains absent and can be reserved again safely.
func publishNewAppServerNotice(root *os.Root, name string, record appServerNoticeReservation, stage func(*os.Root, string, any) error) error {
	return publishNewAppServerNoticeWithHooks(root, name, record, stage, nil, (*os.Root).Remove)
}

// Hooks expose the publication cut points to deterministic fault tests.
// Production removes and syncs the staging link before reporting success.
func publishNewAppServerNoticeWithHooks(root *os.Root, name string, record appServerNoticeReservation, stage func(*os.Root, string, any) error, afterLink func() error, remove func(*os.Root, string) error) error {
	temp, err := randomName()
	if err != nil {
		return err
	}
	linked := false
	defer func() {
		if !linked {
			_ = root.Remove(temp)
		}
	}()
	if err := stage(root, temp, record); err != nil {
		return err
	}
	if err := root.Link(temp, name); err != nil {
		return err
	}
	linked = true
	if err := syncDirectory(root); err != nil {
		return err
	}
	if afterLink != nil {
		if err := afterLink(); err != nil {
			return err
		}
	}
	if err := remove(root, temp); err != nil {
		return err
	}
	return syncDirectory(root)
}

// A process can die after no-replace publication but before unlinking its
// staging name. Under the workspace lock, unlink only a private staging name
// proven to be the same two-link inode as the final record. The normal strict
// JSON reader then validates the single-link record and its reservation state.
func readAppServerNotice(root *os.Root, name string, value *appServerNoticeReservation) error {
	// A single-link record passes the normal private JSON reader. A
	// published record with its staging link still present fails that
	// reader; only a matching private staging name permits recovery.
	if err := readJSON(root, name, value); err == nil {
		return nil
	}
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return ErrUnknown
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	match := ""
	for {
		// Stream entries so ordinary accumulated reservation records do
		// not impose a fixed recovery limit or unbounded memory use.
		entries, readErr := dir.ReadDir(128)
		for _, entry := range entries {
			candidate := entry.Name()
			if !strings.HasPrefix(candidate, ".new-") || len(candidate) != len(".new-")+32 {
				continue
			}
			staged, err := root.Lstat(candidate)
			if err != nil {
				return ErrUnknown
			}
			if os.SameFile(info, staged) {
				if match != "" {
					return ErrUnknown
				}
				match = candidate
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return ErrUnknown
		}
	}
	if match == "" {
		return ErrUnknown
	}
	if err := root.Remove(match); err != nil {
		return err
	}
	if err := syncDirectory(root); err != nil {
		return err
	}
	return readJSON(root, name, value)
}
