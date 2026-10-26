package workspace

import (
	"context"
	"errors"
)

// ErrPaneNoticeUnsupported reports that native pane-input notification is
// disabled because even a body-free hint can submit pending recipient input.
var ErrPaneNoticeUnsupported = errors.New("pane-input notification disabled")

// NoticeResult describes the pane hint separately from durable mail. Unknown
// means tmux may have accepted part of a chained paste; callers must not
// retry the mail or the hint automatically.
type NoticeResult struct {
	Status string `json:"status"`
	Pane   string `json:"pane,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// PaneNotifier is an optional runtime capability, not a mail commit step.
type PaneNotifier interface {
	NotifyPane(context.Context, Binding, string) error
}

// NotifyCommitted is called only after Send returns a committed message. It
// never changes the send result and never reads mailbox or pane content.
func NotifyCommitted(ctx context.Context, rt Runtime, msg Message) NoticeResult {
	result := NoticeResult{Status: "unavailable"}
	if !messageID(msg.ID) || msg.To == "" || rt == nil {
		result.Reason = "committed message or runtime unavailable"
		return result
	}
	// The caller runtime contains only the sender session's bindings. A
	// qualified recipient must never be resolved by a same-named local role.
	if msg.FromSession != "" || msg.ToSession != "" {
		result.Reason = "cross-session pane notification unsupported"
		return result
	}
	from, err := rt.Snapshot(ctx)
	if err != nil || validateSnapshotForNotice(from, msg) != nil {
		result.Reason = "recipient native binding unavailable"
		return result
	}
	var target Binding
	for _, item := range from.Bindings {
		if item.Role == msg.To {
			target = item
			break
		}
	}
	if !target.Owner.valid() {
		result.Reason = "recipient pane is not live"
		return result
	}
	result.Pane = target.Owner.Pane
	notifier, ok := rt.(PaneNotifier)
	if !ok {
		result.Reason = "runtime has no pane notifier"
		return result
	}
	if err := notifier.NotifyPane(ctx, target, msg.ID); err != nil {
		if errors.Is(err, ErrPaneNoticeUnsupported) {
			result.Status = "unavailable"
			result.Reason = err.Error()
			return result
		}
		result.Status = "unknown"
		result.Reason = err.Error()
		return result
	}
	result.Status = "sent"
	return result
}

func validateSnapshotForNotice(s Snapshot, msg Message) error {
	if _, err := validateSnapshot(s); err != nil {
		return err
	}
	if s.Caller.Role != msg.From {
		return ErrUnknown
	}
	return nil
}

// NotifyPane is deliberately disabled. Pasting a comment followed by Enter
// can execute a recipient's partially typed command, including when the
// comment is separated by a trailing space. Durable mail still succeeds.
func (t Tmux) NotifyPane(_ context.Context, _ Binding, _ string) error {
	return ErrPaneNoticeUnsupported
}
