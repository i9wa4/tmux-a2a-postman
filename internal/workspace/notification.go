package workspace

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"time"
)

// NoticeResult is tmux input acceptance, separate from durable mail and POP.
type NoticeResult struct {
	Status string `json:"status"`
	Pane   string `json:"pane,omitempty"`
	Reason string `json:"reason,omitempty"`
}

const (
	NoticeLockBudget       = 5 * time.Second
	NoticeInputBudget      = 2 * time.Second
	PostCommitNoticeBudget = NoticeLockBudget + NoticeInputBudget + time.Second
)

type paneNotifier interface {
	prepareNotice(context.Context, Binding) error
}

func (s Store) NotifyCommittedAfterCommit(ctx context.Context, source, destination Runtime, msg Message) NoticeResult {
	noticeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), PostCommitNoticeBudget)
	defer cancel()
	return s.notifyCommitted(noticeCtx, source, destination, msg)
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
	err = retryNoticeAdmission(ctx, func() error {
		return s.withView(ctx, destination, func(_ *os.Root, snapshot Snapshot) error {
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
			return nil
		})
	})
	if err != nil {
		result.Reason = "recipient binding unavailable: " + err.Error()
		return result
	}
	result.Pane = target.Owner.Pane
	notifier, ok := destination.(paneNotifier)
	if !ok {
		result.Reason = "recipient runtime has no tmux input transport"
		return result
	}
	// Serialize only this recipient's paste/submit, without a mailbox lock.
	lockCtx, lockCancel := context.WithTimeout(ctx, NoticeLockBudget)
	lock, err := s.lockNotice(lockCtx, destination, target)
	lockCancel()
	if err != nil {
		result.Reason = err.Error()
		return result
	}
	defer func() { _ = lock.Close() }()
	inputCtx, inputCancel := context.WithTimeout(ctx, NoticeInputBudget)
	defer inputCancel()
	ctx = inputCtx
	if err := notifier.prepareNotice(ctx, target); err != nil {
		result.Reason = err.Error()
		return result
	}
	prior, err := s.reserveNotice(ctx, source, msg, target)
	if err != nil {
		result.Reason = err.Error()
		return result
	}
	if prior != nil {
		result.Status = prior.Status
		result.Reason = "previous notification attempt; no replay"
		return result
	}
	result.Status = "unknown"
	var inputErr error
	if observed, ok := destination.(observedNotifier); ok {
		inputErr = s.observedNotice(ctx, destination, observed, target, msg)
	} else if transport, ok := destination.(interface {
		sendNotice(context.Context, Binding, string) error
	}); ok {
		inputErr = transport.sendNotice(ctx, target, noticePrompt(msg))
	} else {
		inputErr = errors.New("recipient runtime cannot observe terminal input")
	}
	if inputErr != nil {
		result.Reason = inputErr.Error()
		return result
	}
	if err := s.acceptNotice(ctx, source, msg, target); err != nil {
		result.Reason = "tmux accepted input but receipt persistence failed: " + err.Error()
		return result
	}
	result.Status = "accepted"
	result.Reason = "notification left the observed input area after one C-m; recipient processing is not implied"
	return result
}

// Keep the original greeting, mail hint and protocol sentinels. A single line
// works in both plain line-oriented consoles and bracketed-paste TUIs. No mail
// body, model runtime API, or executable shell source is delivered here.
func noticePrompt(msg Message) string {
	return "<!-- message start --> Hello, " + msg.To + "! You've got mail: " + msg.ID + ".md. Run `tmux-a2a-postman pop` to claim the next unread message and get its archived path. Read that complete Markdown through EOF and follow its reply guidance. <!-- end of message -->"
}

func (t Tmux) prepareNotice(ctx context.Context, target Binding) error {
	peer := t
	peer.Pane = target.Owner.Pane
	snapshot, err := peer.Snapshot(ctx)
	if err != nil || snapshot.Caller != target {
		return ErrUnknown
	}
	out, err := tmuxOutput(ctx, t.Socket, "display-message", "-p", "-t", target.Owner.Pane, "#{pane_current_command}\t#{pane_in_mode}")
	if err != nil {
		return err
	}
	fields := strings.Split(strings.TrimSuffix(out, "\n"), "\t")
	if len(fields) != 2 || fields[1] != "0" {
		return errors.New("recipient is in a tmux input mode")
	}
	// Configured roles must host agent consoles. Do not submit into an ordinary
	// shell prompt, where C-m could execute an unrelated pending command.
	switch fields[0] {
	case "sh", "bash", "zsh", "fish", "dash", "ksh", "nu", "csh", "tcsh":
		return errors.New("recipient pane is a shell; start its agent console")
	}
	return nil
}

func (r NamedRuntime) prepareNotice(ctx context.Context, target Binding) error {
	return r.tmux.prepareNotice(ctx, target)
}

func (s Store) lockNotice(ctx context.Context, rt Runtime, target Binding) (*os.File, error) {
	var lock *os.File
	err := retryNoticeAdmission(ctx, func() error {
		return s.WithVerified(ctx, rt, func(ws *os.Root, snapshot Snapshot) error {
			found := false
			for _, b := range snapshot.Bindings {
				if b == target {
					found = true
				}
			}
			if !found {
				return ErrUnknown
			}
			runtime, err := privateDir(ws, "runtime", false)
			if err != nil {
				return err
			}
			defer func() { _ = runtime.Close() }()
			dir, err := privateDir(runtime, "notice-locks", true)
			if err != nil {
				return err
			}
			defer func() { _ = dir.Close() }()
			f, err := dir.OpenFile(target.Role+".lock", os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
			if errors.Is(err, os.ErrExist) {
				f, err = dir.OpenFile(target.Role+".lock", os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			}
			if err != nil {
				return err
			}
			info, err := f.Stat()
			if err != nil || !privateInfo(info, false) {
				_ = f.Close()
				return errors.New("unsafe notification lock")
			}
			pathInfo, err := dir.Lstat(target.Role + ".lock")
			if err != nil || !os.SameFile(info, pathInfo) {
				_ = f.Close()
				return ErrUnknown
			}
			lock = f
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = lock.Close()
		}
	}()
	// Wait on the already opened OS lock. Re-probing tmux and rewriting runtime
	// state on every contention tick would flood the server when many execution
	// requesters target one approver. Fresh native checks follow acquisition.
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, err
		}
		if err := noticePause(ctx); err != nil {
			return nil, err
		}
	}
	err = retryNoticeAdmission(ctx, func() error {
		return s.withView(ctx, rt, func(ws *os.Root, snapshot Snapshot) error {
			found := false
			for _, b := range snapshot.Bindings {
				if b == target {
					found = true
				}
			}
			if !found {
				return ErrUnknown
			}
			runtime, err := privateDir(ws, "runtime", false)
			if err != nil {
				return err
			}
			defer func() { _ = runtime.Close() }()
			dir, err := privateDir(runtime, "notice-locks", false)
			if err != nil {
				return err
			}
			defer func() { _ = dir.Close() }()
			info, err := lock.Stat()
			if err != nil {
				return err
			}
			path, err := dir.Lstat(target.Role + ".lock")
			if err != nil || !os.SameFile(info, path) {
				return ErrUnknown
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	keep = true
	return lock, nil
}

type noticeRecord struct {
	Version   int     `json:"version"`
	MessageID string  `json:"message_id"`
	Recipient Binding `json:"recipient"`
	Status    string  `json:"status"`
}

func (s Store) noticeTransition(ctx context.Context, rt Runtime, msg Message, target Binding, accept bool) (*noticeRecord, error) {
	var prior *noticeRecord
	err := retryNoticeAdmission(ctx, func() error {
		return s.WithVerified(ctx, rt, func(ws *os.Root, snapshot Snapshot) error {
			if snapshot.Caller.Role != msg.From {
				return ErrUnknown
			}
			runtimeDir, err := privateDir(ws, "runtime", false)
			if err != nil {
				return err
			}
			defer func() { _ = runtimeDir.Close() }()
			dir, err := privateDir(runtimeDir, "notices", true)
			if err != nil {
				return err
			}
			defer func() { _ = dir.Close() }()
			if err := syncDirectory(runtimeDir); err != nil {
				return err
			}
			var old noticeRecord
			err = readJSON(dir, msg.ID+".json", &old)
			if err == nil {
				if old.Version != 1 || old.MessageID != msg.ID || old.Recipient != target || (old.Status != "unknown" && old.Status != "accepted") {
					return ErrUnknown
				}
				if !accept {
					prior = &old
					return nil
				}
				if old.Status != "unknown" {
					return ErrUnknown
				}
			} else if accept || !errors.Is(err, os.ErrNotExist) {
				return err
			}
			status := "unknown"
			if accept {
				status = "accepted"
			}
			return replaceJSON(dir, msg.ID+".json", noticeRecord{1, msg.ID, target, status})
		})
	})
	return prior, err
}

func (s Store) reserveNotice(ctx context.Context, rt Runtime, msg Message, target Binding) (*noticeRecord, error) {
	return s.noticeTransition(ctx, rt, msg, target, false)
}

func (s Store) acceptNotice(ctx context.Context, rt Runtime, msg Message, target Binding) error {
	_, err := s.noticeTransition(ctx, rt, msg, target, true)
	return err
}

func retryNoticeAdmission(ctx context.Context, op func() error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
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
