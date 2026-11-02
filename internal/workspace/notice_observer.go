package workspace

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/diagnostic"
)

// Input evidence deliberately learns the current cursor prefix. It contains no
// model names, prompt glyphs or assumptions about a provider's API. A terminal
// observation is evidence, never proof of model processing. Ambiguity stops input.
type observedNotifier interface {
	noticeFrame(context.Context, Binding) (paneFrame, error)
	pasteNotice(context.Context, Binding, string) error
	enterNotice(context.Context, Binding) error
}

type inputGuard struct {
	Version   int       `json:"version"`
	MessageID string    `json:"message_id"`
	Recipient Binding   `json:"recipient"`
	Baseline  paneFrame `json:"baseline"`
	Text      string    `json:"text"`
	Status    string    `json:"status"`
}

func (r NamedRuntime) noticeFrame(ctx context.Context, target Binding) (paneFrame, error) {
	return r.tmux.noticeFrame(ctx, target)
}

func (r NamedRuntime) pasteNotice(ctx context.Context, target Binding, text string) error {
	return r.tmux.pasteNotice(ctx, target, text)
}

func (r NamedRuntime) enterNotice(ctx context.Context, target Binding) error {
	return r.tmux.enterNotice(ctx, target)
}

func (t Tmux) pasteNotice(ctx context.Context, target Binding, text string) error {
	if err := t.prepareNotice(ctx, target); err != nil {
		return err
	}
	nonce, err := randomName()
	if err != nil {
		return err
	}
	buffer := "postman-notice-" + strings.TrimPrefix(nonce, ".new-")
	_, err = tmuxOutput(ctx, t.Socket, "set-buffer", "-b", buffer, text, ";", "paste-buffer", "-b", buffer, "-d", "-p", "-t", target.Owner.Pane)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 100*time.Millisecond)
		defer cancel()
		_, _ = tmuxOutput(cleanup, t.Socket, "delete-buffer", "-b", buffer)
	}
	return err
}

func (t Tmux) enterNotice(ctx context.Context, target Binding) error {
	if err := t.prepareNotice(ctx, target); err != nil {
		return err
	}
	_, err := tmuxOutput(ctx, t.Socket, "send-keys", "-t", target.Owner.Pane, "C-m")
	return err
}

// The recipient lock is held by the caller, including all observation and
// persistence. The mailbox lock is held only for short guarded file operations.
func (s Store) inputGuard(ctx context.Context, rt Runtime, target Binding, write *inputGuard) (*inputGuard, error) {
	var guard *inputGuard
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
			dir, err := privateDir(runtime, "input-guards", true)
			if err != nil {
				return err
			}
			defer func() { _ = dir.Close() }()
			name := target.Role + ".json"
			if write != nil {
				return replaceJSON(dir, name, *write)
			}
			var old inputGuard
			err = readJSON(dir, name, &old)
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if old.Version != 1 || !messageID(old.MessageID) || (old.Status != "pending" && old.Status != "accepted") {
				return ErrUnknown
			}
			guard = &old
			return nil
		})
	})
	return guard, err
}

func inputAccepted(base, now paneFrame, text string) bool {
	prefix := base.cursorPrefix()
	return strings.TrimSpace(prefix) != "" && base.sameGeometry(now) && !now.InputMode && now.CursorVisible &&
		now.cursorPrefix() == prefix && strings.Contains(packedScreen(now.Screen), packedScreen(text)) &&
		!strings.HasSuffix(packedScreen(now.beforeCursor()), packedScreen(text))
}

func noticePause(ctx context.Context) error {
	timer := time.NewTimer(25 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s Store) observedNotice(ctx context.Context, rt Runtime, transport observedNotifier, target Binding, msg Message) (retErr error) {
	trace := diagnostic.From(ctx)
	prior, err := s.inputGuard(ctx, rt, target, nil)
	if err != nil {
		return err
	}
	if prior != nil && prior.Recipient == target && prior.Status == "pending" {
		frame, err := noticeObservation(ctx, transport, target)
		if err != nil || !inputAccepted(prior.Baseline, frame, prior.Text) {
			return errors.New("previous notification input remains uncertain; no additional paste or Enter")
		}
		prior.Status = "accepted"
		if err = s.acceptInputGuard(ctx, rt, target, prior); err != nil {
			return err
		}
	}
	base, err := noticeObservation(ctx, transport, target)
	if err != nil {
		return err
	}
	if strings.TrimSpace(base.cursorPrefix()) == "" {
		return errors.New("recipient input prefix cannot be distinguished from a newline")
	}
	// The durable guard needs only the learned prefix and native geometry, not
	// the recipient's conversation screen. Do not copy terminal history here.
	minimal := base
	minimal.Screen = base.cursorPrefix()
	minimal.CursorY = 0
	guard := inputGuard{Version: 1, MessageID: msg.ID, Recipient: target, Baseline: minimal, Text: noticePrompt(msg), Status: "pending"}
	// Persist before the first input byte. A crash cannot permit the next sender
	// to append another notice to an unresolved draft.
	if _, err = s.inputGuard(ctx, rt, target, &guard); err != nil {
		return err
	}
	// The input budget bounds paste/settlement/submit. Read-only confirmation
	// after the one Enter and durable receipts use the outer notice deadline.
	inputCtx, inputCancel := context.WithTimeout(ctx, NoticeInputBudget)
	defer inputCancel()
	pasteSpan := trace.Start("notification_paste")
	err = transport.pasteNotice(inputCtx, target, guard.Text)
	pasteSpan.Error(err)
	if err != nil {
		return err
	}
	settleSpan := trace.Start("notification_settle")
	defer func() { settleSpan.Error(retErr) }()
	var stable string
	var since time.Time
	for {
		if err = noticePause(inputCtx); err != nil {
			return errors.New("notification paste was not observed settling; no Enter sent")
		}
		frame, err := noticeObservation(inputCtx, transport, target)
		if err != nil || !base.sameGeometry(frame) {
			return errors.New("recipient changed during notification paste")
		}
		content := packedScreen(frame.beforeCursor())
		input := packedScreen(base.cursorPrefix() + guard.Text)
		if !strings.HasSuffix(content, input) {
			stable = ""
			continue
		}
		if input != stable {
			stable = input
			since = time.Now()
			continue
		}
		if time.Since(since) >= 180*time.Millisecond {
			break
		}
	}
	settleSpan.End("ok")
	// One submit only. Repeating Enter after a visual change is unsafe: it can
	// either insert a newline or submit a different, already accepted input.
	submitSpan := trace.Start("notification_submit")
	err = transport.enterNotice(inputCtx, target)
	submitSpan.Error(err)
	if err != nil {
		return err
	}
	inputCancel()
	confirmSpan := trace.Start("notification_confirm")
	defer func() { confirmSpan.Error(retErr) }()
	for {
		if err = noticePause(ctx); err != nil {
			return errors.New("notification submit is uncertain; no repeat Enter or paste")
		}
		frame, err := noticeObservation(ctx, transport, target)
		if err != nil {
			return err
		}
		if inputAccepted(base, frame, guard.Text) {
			guard.Status = "accepted"
			return s.acceptInputGuard(ctx, rt, target, &guard)
		}
	}
}

// A native frame has its own short read deadline. A slow read may be repeated
// within the caller's existing budget, but actual identity/geometry failures
// stop immediately. This helper cannot paste or submit input.
func noticeObservation(ctx context.Context, transport observedNotifier, target Binding) (paneFrame, error) {
	diagnostic.From(ctx).Add("notice_observations", 1, 0)
	for {
		frame, err := transport.noticeFrame(ctx, target)
		if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return frame, err
		}
		if err := noticePause(ctx); err != nil {
			return paneFrame{}, err
		}
	}
}

func (s Store) acceptInputGuard(ctx context.Context, rt Runtime, target Binding, guard *inputGuard) error {
	if guard == nil || guard.Status != "accepted" {
		return ErrUnknown
	}
	return retryAcceptedNoticeState(ctx, func() error {
		_, err := s.inputGuard(ctx, rt, target, guard)
		return err
	})
}
