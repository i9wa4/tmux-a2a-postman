package workspace

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/diagnostic"
)

// Observe whole terminal frames without identifying a TUI input area, prompt or
// provider. Small line changes guide bounded Enter retries; even a large change
// is only a submission heuristic, never proof of recipient processing.
type observedNotifier interface {
	noticeFrame(context.Context, Binding) (paneFrame, error)
	pasteNotice(context.Context, Binding, string) error
	enterNotice(context.Context, Binding) error
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

const (
	// Separate paste from Enter, and successive Enter presses from each other.
	// Closely spaced presses can become literal newlines in otherwise generic
	// terminal editors. These bounds are shared by every provider.
	noticeEnterInterval    = 600 * time.Millisecond
	noticeMaxEnters        = 3
	noticeSmallChangeLines = 4
)

func (s Store) observedNotice(ctx context.Context, _ Runtime, transport observedNotifier, target Binding, msg Message) error {
	inputCtx, cancel := context.WithTimeout(ctx, NoticeInputBudget)
	defer cancel()
	trace := diagnostic.From(ctx)
	base, err := noticeObservation(inputCtx, transport, target)
	if err != nil {
		return err
	}
	pasteSpan := trace.Start("notification_paste")
	err = transport.pasteNotice(inputCtx, target, noticePrompt(msg))
	pasteSpan.Error(err)
	if err != nil {
		return err
	}
	// Paste once. Concurrent messages can share the destination terminal; no
	// destination-wide lock or persisted draft guard can block later messages.
	settleSpan := trace.Start("notification_settle")
	err = noticeWait(inputCtx, noticeEnterInterval)
	settleSpan.Error(err)
	if err != nil {
		return err
	}
	before, err := noticeObservation(inputCtx, transport, target)
	if err != nil {
		return err
	}
	if !base.sameGeometry(before) || before.InputMode {
		return errors.New("recipient changed during notification paste")
	}
	for range noticeMaxEnters {
		submitSpan := trace.Start("notification_submit")
		err = transport.enterNotice(inputCtx, target)
		submitSpan.Error(err)
		if err != nil {
			return err
		}
		confirmSpan := trace.Start("notification_confirm")
		err = noticeWait(inputCtx, noticeEnterInterval)
		if err != nil {
			confirmSpan.Error(err)
			return err
		}
		after, err := noticeObservation(inputCtx, transport, target)
		confirmSpan.Error(err)
		if err != nil {
			return err
		}
		if !before.sameGeometry(after) || after.InputMode {
			return errors.New("recipient changed during notification submission")
		}
		if screenLineChanges(before.Screen, after.Screen) > noticeSmallChangeLines {
			return nil
		}
		// A replacement is two changed lines. Inserting a newline (including
		// scrolling one row off-screen) is a small edit, not a changed screen
		// at every subsequent row. Compare the entire frames, not row positions.
		before = after
	}
	return errors.New("notification submission remains uncertain after bounded Enter retries; no more input sent")
}

func noticeWait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// screenLineChanges counts inserted/deleted lines up to the retry threshold + 1.
// A bounded edit-distance band avoids quadratic work even for unusually tall
// panes. Trailing terminal padding is ignored; internal blank rows are retained.
func screenLineChanges(before, after string) int {
	lines := func(text string) []string {
		rows := strings.Split(text, "\n")
		for i := range rows {
			rows[i] = strings.TrimRight(rows[i], " \t\r")
		}
		for len(rows) > 0 && rows[len(rows)-1] == "" {
			rows = rows[:len(rows)-1]
		}
		return rows
	}
	a, b := lines(before), lines(after)
	for len(a) > 0 && len(b) > 0 && a[0] == b[0] {
		a, b = a[1:], b[1:]
	}
	for len(a) > 0 && len(b) > 0 && a[len(a)-1] == b[len(b)-1] {
		a, b = a[:len(a)-1], b[:len(b)-1]
	}
	limit := noticeSmallChangeLines + 1
	if len(a) == 0 || len(b) == 0 {
		return min(limit, len(a)+len(b))
	}
	if len(a)-len(b) >= limit || len(b)-len(a) >= limit {
		return limit
	}
	previous, current := make([]int, len(b)+1), make([]int, len(b)+1)
	for j := range previous {
		previous[j] = min(j, limit)
	}
	for i := 1; i <= len(a); i++ {
		current[0] = min(i, limit)
		low, high := max(1, i-noticeSmallChangeLines), min(len(b), i+noticeSmallChangeLines)
		if low > 1 {
			current[low-1] = limit
		}
		best := limit
		if low == 1 {
			best = current[0]
		}
		for j := low; j <= high; j++ {
			value := min(limit, previous[j]+1, current[j-1]+1)
			if a[i-1] == b[j-1] {
				value = min(value, previous[j-1])
			}
			current[j] = value
			best = min(best, value)
		}
		if high < len(b) {
			current[high+1] = limit
		}
		if best == limit {
			return limit
		}
		previous, current = current, previous
	}
	return previous[len(b)]
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
