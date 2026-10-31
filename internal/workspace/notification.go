package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// NoticeResult is tmux input acceptance, separate from durable mail and POP.
type NoticeResult struct {
	Status string `json:"status"`
	Pane   string `json:"pane,omitempty"`
	Reason string `json:"reason,omitempty"`
}

const PostCommitNoticeBudget = 2 * time.Second

type paneNotifier interface {
	prepareNotice(context.Context, Binding) error
	sendNotice(context.Context, Binding, string) error
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
	lock, err := s.lockNotice(ctx, destination, target)
	if err != nil {
		result.Reason = err.Error()
		return result
	}
	defer func() { _ = lock.Close() }()
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
	if err := notifier.sendNotice(ctx, target, noticePrompt(msg)); err != nil {
		result.Reason = err.Error()
		return result
	}
	if err := s.acceptNotice(ctx, source, msg, target); err != nil {
		result.Reason = "tmux accepted input but receipt persistence failed: " + err.Error()
		return result
	}
	result.Status = "accepted"
	result.Reason = "tmux accepted notification text and C-m; recipient POP/response not observed"
	return result
}

// Keep the original greeting, mail hint and protocol sentinels. A single line
// works in both plain line-oriented consoles and bracketed-paste TUIs. No mail
// body, model runtime API, or executable shell source is delivered here.
func noticePrompt(msg Message) string {
	return "<!-- message start --> Hello, " + msg.To + "! You've got mail: " + msg.ID + ".md. Run `tmux-a2a-postman pop " + msg.ID + "` to claim it and get the archived body path. Read the complete archived Markdown through EOF before handling or replying. <!-- end of message -->"
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

var noticeBufferSeq atomic.Uint64

func (t Tmux) sendNotice(ctx context.Context, target Binding, text string) error {
	if err := t.prepareNotice(ctx, target); err != nil {
		return err
	}
	buffer := fmt.Sprintf("postman-notify-%d-%d", os.Getpid(), noticeBufferSeq.Add(1))
	// As in the original transport: named buffer, paste, a short UI processing
	// delay, and a real C-m in one tmux command list. -p uses bracketed paste when
	// supported. Unique buffers and the recipient lock prevent cross-send mixing.
	_, err := tmuxOutput(ctx, t.Socket, "set-buffer", "-b", buffer, text,
		";", "paste-buffer", "-b", buffer, "-d", "-p", "-t", target.Owner.Pane,
		";", "run-shell", "sleep 0.15",
		";", "send-keys", "-t", target.Owner.Pane, "C-m")
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 100*time.Millisecond)
		defer cancel()
		_, _ = tmuxOutput(cleanupCtx, t.Socket, "delete-buffer", "-b", buffer)
	}
	return err
}

func (r NamedRuntime) prepareNotice(ctx context.Context, target Binding) error {
	return r.tmux.prepareNotice(ctx, target)
}

func (r NamedRuntime) sendNotice(ctx context.Context, target Binding, text string) error {
	return r.tmux.sendNotice(ctx, target, text)
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
			runtimeDir, err := privateDir(ws, "runtime", false)
			if err != nil {
				return err
			}
			defer func() { _ = runtimeDir.Close() }()
			dir, err := privateDir(runtimeDir, "notice-locks", true)
			if err != nil {
				return err
			}
			defer func() { _ = dir.Close() }()
			f, err := dir.OpenFile(target.Role+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
			if err != nil {
				return err
			}
			keep := false
			defer func() {
				if !keep {
					_ = f.Close()
				}
			}()
			info, err := f.Stat()
			if err != nil || !privateInfo(info, false) {
				return errors.New("unsafe notification lock")
			}
			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				if errors.Is(err, syscall.EWOULDBLOCK) {
					return ErrBusy
				}
				return err
			}
			pathInfo, err := dir.Lstat(target.Role + ".lock")
			if err != nil || !os.SameFile(info, pathInfo) {
				return ErrUnknown
			}
			lock, keep = f, true
			return nil
		})
	})
	return lock, err
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
