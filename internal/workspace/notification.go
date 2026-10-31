package workspace

import (
	"context"
	"errors"
	"os"
	"strconv"
	"time"
)

// NoticeResult records acceptance separately from durable delivery. accepted
// never means POP, full-body reading, or a reply was observed.
type NoticeResult struct {
	Status   string `json:"status"`
	Pane     string `json:"pane,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Runtime  string `json:"runtime,omitempty"`
	ThreadID string `json:"thread_id,omitempty"`
	QueueID  string `json:"queue_id,omitempty"`
}

const PostCommitNoticeBudget = 2 * time.Second

type notifierRegistration struct {
	Version int            `json:"version"`
	Binding Binding        `json:"binding"`
	Runtime string         `json:"runtime"`
	Codex   *CodexNotifier `json:"codex,omitempty"`
}

func (r notifierRegistration) same(other notifierRegistration) bool {
	return r.Version == other.Version && r.Binding == other.Binding && r.Runtime == other.Runtime &&
		((r.Codex == nil && other.Codex == nil) || (r.Codex != nil && other.Codex != nil && *r.Codex == *other.Codex))
}

// RegisterNotifier is called directly by recipient Bash after installing its
// Readline binding. A sender cannot register another pane's notifier.
func (s Store) RegisterNotifier(ctx context.Context, rt Runtime) error {
	return s.WithVerified(ctx, rt, func(ws *os.Root, snapshot Snapshot) error {
		if snapshot.Caller.PanePID != strconv.Itoa(os.Getppid()) {
			return errors.New("notifier registration must be a direct child of the pane's Bash process")
		}
		return writeNotifier(ws, snapshot.Caller, notifierRegistration{Version: 1, Binding: snapshot.Caller, Runtime: "bash"})
	})
}

func writeNotifier(ws *os.Root, target Binding, registration notifierRegistration) error {
	runtimeDir, err := privateDir(ws, "runtime", false)
	if err != nil {
		return err
	}
	defer func() { _ = runtimeDir.Close() }()
	notices, err := privateDir(runtimeDir, "notifiers", true)
	if err != nil {
		return err
	}
	defer func() { _ = notices.Close() }()
	if err := syncDirectory(runtimeDir); err != nil {
		return err
	}
	return replaceJSON(notices, target.Role+".json", registration)
}

// NotifyCommittedAfterCommit gives a committed send a separate finite notice
// budget. Cross-session mail supplies the already authorized named peer as
// destination. Local mail supplies the native caller runtime as destination.
func (s Store) NotifyCommittedAfterCommit(ctx context.Context, source, destination Runtime, msg Message) NoticeResult {
	noticeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), PostCommitNoticeBudget)
	defer cancel()
	return s.notifyCommitted(noticeCtx, source, destination, msg)
}

type bashNotifier interface {
	notifyBash(context.Context, Binding) error
}

func (s Store) notifyCommitted(ctx context.Context, source, destination Runtime, msg Message) NoticeResult {
	result := NoticeResult{Status: "unavailable"}
	if source == nil || destination == nil || !messageID(msg.ID) {
		result.Reason = "committed message or runtime unavailable"
		return result
	}
	var from Snapshot
	err := retryNoticeAdmission(ctx, func() error {
		return s.withView(ctx, source, func(ws *os.Root, snapshot Snapshot) error {
			if snapshot.Caller.Role != msg.From {
				return ErrUnknown
			}
			sent, err := openOwnBox(ws, msg.From, "sent")
			if err != nil {
				return err
			}
			defer func() { _ = sent.Close() }()
			committed, err := readMessage(sent, msg.ID+".md")
			if err != nil || !sameMessage(committed, msg) {
				return ErrUnknown
			}
			from = snapshot
			return nil
		})
	})
	if err != nil {
		result.Reason = "committed sender binding unavailable: " + err.Error()
		return result
	}
	var target Binding
	var registered notifierRegistration
	err = retryNoticeAdmission(ctx, func() error {
		return s.withView(ctx, destination, func(ws *os.Root, snapshot Snapshot) error {
			if msg.ToSession == "" {
				if snapshot.Root != from.Root || !sameRuntime(snapshot.Caller.Owner, from.Caller.Owner) {
					return ErrUnknown
				}
			} else {
				named, ok := destination.(NamedRuntime)
				sender, sourceOK := source.(NamedRuntime)
				if !ok || !sourceOK || named.Name() != msg.ToSession || sender.Name() != msg.FromSession || snapshot.Caller.Role != msg.To {
					return ErrUnknown
				}
			}
			for _, b := range snapshot.Bindings {
				if b.Role == msg.To {
					target = b
				}
			}
			if !target.Owner.valid() {
				return ErrUnknown
			}
			runtimeDir, err := privateDir(ws, "runtime", false)
			if err != nil {
				return err
			}
			defer func() { _ = runtimeDir.Close() }()
			notices, err := privateDir(runtimeDir, "notifiers", false)
			if err != nil {
				return err
			}
			defer func() { _ = notices.Close() }()
			if err := readJSON(notices, msg.To+".json", &registered); err != nil {
				return err
			}
			if registered.Version != 1 || registered.Binding != target {
				return errors.New("recipient notifier binding is stale")
			}
			return nil
		})
	})
	if err != nil {
		result.Reason = "recipient notifier unavailable: " + err.Error()
		return result
	}
	result.Pane = target.Owner.Pane
	result.Runtime = registered.Runtime
	if registered.Runtime == "codex" {
		if registered.Codex == nil {
			result.Reason = "invalid Codex registration"
			return result
		}
		n := *registered.Codex
		client, err := startCodexQueue(ctx, n)
		if err != nil {
			result.Reason = err.Error()
			return result
		}
		defer client.close()
		if err := client.checkThread(ctx, n); err != nil {
			result.Reason = err.Error()
			return result
		}
		if err := verifyNoticeBinding(ctx, destination, target); err != nil {
			result.Reason = err.Error()
			return result
		}
		prior, err := s.reserveNotice(ctx, source, msg, registered)
		if err != nil {
			result.Reason = err.Error()
			return result
		}
		result.ThreadID = n.ThreadID
		if prior != nil {
			result.Status = prior.Status
			result.QueueID = prior.QueueID
			result.Reason = "previous notification attempt; no replay"
			return result
		}
		result.Status = "unknown"
		queueID, err := client.enqueue(ctx, n, msg)
		if err != nil {
			result.Reason = err.Error()
			return result
		}
		if err := s.acceptNotice(ctx, source, msg, registered, queueID); err != nil {
			result.Reason = "queue accepted but receipt persistence failed: " + err.Error()
			return result
		}
		result.Status = "accepted"
		result.QueueID = queueID
		result.Reason = "Codex accepted notice for the existing conversation; recipient POP/response not observed"
		return result
	}
	if registered.Runtime != "bash" {
		result.Reason = "unsupported recipient runtime"
		return result
	}
	notifier, ok := destination.(bashNotifier)
	if !ok {
		result.Reason = "recipient runtime has no Bash notification transport"
		return result
	}
	// No mailbox lock is held across transport. No Enter or body text is sent.
	if err := notifier.notifyBash(ctx, target); err != nil {
		result.Status = "unknown"
		result.Reason = err.Error()
		return result
	}
	result.Status = "accepted"
	result.Reason = "Readline notification queued; recipient POP and response not observed"
	return result
}

func (t Tmux) notifyBash(ctx context.Context, target Binding) error {
	peer := t
	peer.Pane = target.Owner.Pane
	snapshot, err := peer.Snapshot(ctx)
	if err != nil || snapshot.Caller != target {
		return ErrUnknown
	}
	command, err := tmuxOutput(ctx, t.Socket, "display-message", "-p", "-t", target.Owner.Pane, "#{pane_current_command}")
	if err != nil || command != "bash\n" {
		return errors.New("recipient is not at its registered Bash Readline prompt")
	}
	_, err = tmuxOutput(ctx, t.Socket, "send-keys", "-t", target.Owner.Pane, "C-x", "C-p")
	return err
}

func (r NamedRuntime) notifyBash(ctx context.Context, target Binding) error {
	return r.tmux.notifyBash(ctx, target)
}

func verifyNoticeBinding(ctx context.Context, rt Runtime, target Binding) error {
	snapshot, err := rt.Snapshot(ctx)
	if err != nil {
		return err
	}
	for _, b := range snapshot.Bindings {
		if b == target {
			return nil
		}
	}
	return errors.New("recipient binding changed before notification")
}

type noticeRecord struct {
	Version   int                  `json:"version"`
	MessageID string               `json:"message_id"`
	Recipient notifierRegistration `json:"recipient"`
	Status    string               `json:"status"`
	QueueID   string               `json:"queue_id,omitempty"`
}

func (s Store) reserveNotice(ctx context.Context, rt Runtime, msg Message, reg notifierRegistration) (*noticeRecord, error) {
	var prior *noticeRecord
	err := retryNoticeAdmission(ctx, func() error {
		return s.WithVerified(ctx, rt, func(ws *os.Root, snapshot Snapshot) error {
			if snapshot.Caller.Role != msg.From {
				return ErrUnknown
			}
			dir, err := privateDir(ws, "runtime", false)
			if err != nil {
				return err
			}
			defer func() { _ = dir.Close() }()
			notices, err := privateDir(dir, "notices", true)
			if err != nil {
				return err
			}
			defer func() { _ = notices.Close() }()
			if err := syncDirectory(dir); err != nil {
				return err
			}
			var old noticeRecord
			if err := readJSON(notices, msg.ID+".json", &old); err == nil {
				if old.Version != 1 || old.MessageID != msg.ID || !old.Recipient.same(reg) || (old.Status != "unknown" && old.Status != "accepted") {
					return ErrUnknown
				}
				prior = &old
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return replaceJSON(notices, msg.ID+".json", noticeRecord{Version: 1, MessageID: msg.ID, Recipient: reg, Status: "unknown"})
		})
	})
	return prior, err
}

func (s Store) acceptNotice(ctx context.Context, rt Runtime, msg Message, reg notifierRegistration, queueID string) error {
	return retryNoticeAdmission(ctx, func() error {
		return s.WithVerified(ctx, rt, func(ws *os.Root, snapshot Snapshot) error {
			if snapshot.Caller.Role != msg.From {
				return ErrUnknown
			}
			dir, err := privateDir(ws, "runtime", false)
			if err != nil {
				return err
			}
			defer func() { _ = dir.Close() }()
			notices, err := privateDir(dir, "notices", false)
			if err != nil {
				return err
			}
			defer func() { _ = notices.Close() }()
			var old noticeRecord
			if err := readJSON(notices, msg.ID+".json", &old); err != nil {
				return err
			}
			if old.Version != 1 || old.MessageID != msg.ID || !old.Recipient.same(reg) || old.Status != "unknown" {
				return ErrUnknown
			}
			return replaceJSON(notices, msg.ID+".json", noticeRecord{Version: 1, MessageID: msg.ID, Recipient: reg, Status: "accepted", QueueID: queueID})
		})
	})
}

// Retry only short lock admission. No runtime request or mail operation is
// replayed: the closure contains just a locked file transition or read.
func retryNoticeAdmission(ctx context.Context, op func() error) error {
	for {
		err := op()
		if !errors.Is(err, ErrBusy) {
			return err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
