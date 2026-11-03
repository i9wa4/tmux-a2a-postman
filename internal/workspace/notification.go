package workspace

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/diagnostic"
)

// NoticeResult is a terminal submission heuristic, separate from durable mail and POP.
type NoticeResult struct {
	Status string `json:"status"`
	Pane   string `json:"pane,omitempty"`
	Reason string `json:"reason,omitempty"`
}

const (
	NoticeInputBudget      = 5 * time.Second
	PostCommitNoticeBudget = 8 * time.Second
)

type paneNotifier interface {
	prepareNotice(context.Context, Binding) error
}

func (s Store) NotifyCommittedAfterCommit(ctx context.Context, source, destination Runtime, msg Message) (result NoticeResult) {
	trace := diagnostic.From(ctx)
	span := trace.Start("notification")
	defer func() {
		span.End(result.Status)
		trace.Note(diagnostic.Facts{MessageID: msg.ID, Notification: result.Status})
	}()
	noticeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), PostCommitNoticeBudget)
	defer cancel()
	return s.notifyCommitted(noticeCtx, source, destination, msg)
}

func (s Store) notifyCommitted(ctx context.Context, source, destination Runtime, msg Message) NoticeResult {
	trace := diagnostic.From(ctx)
	resume := trace.Pause()
	defer resume()
	result := NoticeResult{Status: "unavailable"}
	if source == nil || destination == nil || !messageID(msg.ID) {
		result.Reason = "committed message or runtime unavailable"
		return result
	}
	var from Snapshot
	err := retryNoticeAdmission(ctx, func() error {
		return s.withView(ctx, source, func(ws *os.Root, snapshot Snapshot) error {
			if !noticeSource(snapshot, msg) {
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
		inputCtx, inputCancel := context.WithTimeout(ctx, NoticeInputBudget)
		defer inputCancel()
		inputErr = transport.sendNotice(inputCtx, target, noticePrompt(msg))
	} else {
		inputErr = errors.New("recipient runtime cannot observe terminal input")
	}
	if inputErr != nil {
		result.Reason = inputErr.Error()
		return result
	}
	if err := s.acceptNotice(ctx, source, destination, msg, target); err != nil {
		result.Reason = "terminal observation completed but receipt persistence failed: " + err.Error()
		return result
	}
	result.Status = "accepted"
	result.Reason = "Enter sent and substantial whole-pane change observed; this heuristic does not prove receipt or processing"
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
			if !noticeSource(snapshot, msg) {
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
				if old.Status == "accepted" {
					return nil
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

func (s Store) acceptNotice(ctx context.Context, rt, destination Runtime, msg Message, target Binding) error {
	return retryAcceptedNoticeState(ctx, func() error {
		// Recheck the pinned recipient on every receipt-only retry. A native
		// timeout must not let a replacement pane inherit this attempt.
		if err := retryNoticeAdmission(ctx, func() error {
			return s.withView(ctx, destination, func(_ *os.Root, snapshot Snapshot) error {
				for _, binding := range snapshot.Bindings {
					if binding == target {
						return nil
					}
				}
				return ErrUnknown
			})
		}); err != nil {
			return err
		}
		_, err := s.noticeTransition(ctx, rt, msg, target, true)
		return err
	})
}

// Only idempotent accepted-state persistence belongs here. Input paste/Enter,
// pending admission and arbitrary mail mutations must never be retried here.
func retryAcceptedNoticeState(ctx context.Context, operation func() error) error {
	for {
		err := operation()
		if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return err
		}
		// The input has already been accepted. Retry only this idempotent
		// receipt transition after a shorter native probe times out; every
		// attempt revalidates the same sender, message and target. No input is
		// pasted or submitted here, and actual identity errors stop immediately.
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
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
