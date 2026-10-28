package workspace

import (
	"context"
	"errors"
	"time"
)

// ErrPaneNoticeUnsupported reports that native pane-input notification is
// disabled because even a body-free hint can submit pending recipient input.
var ErrPaneNoticeUnsupported = errors.New("pane-input notification disabled")

// ErrPaneNoticeBusy means the recipient app could not acquire exclusive input
// or could not atomically establish an empty composer. No input was submitted.
var ErrPaneNoticeBusy = errors.New("recipient input is busy")

// NoticeResult describes the pane hint separately from durable mail. Unknown
// means tmux may have accepted part of a chained paste; callers must not
// retry the mail or the hint automatically.
type NoticeResult struct {
	Status string `json:"status"`
	Pane   string `json:"pane,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// AtomicNoticeReceipt is app-owned evidence for one committed message. The
// implementation must observe an empty composer while it owns input, submit
// exactly once, and observe the immediate and settled recipient UI. A terminal
// pre-capture followed by tmux Enter cannot meet this contract.
// ImmediateSubmittedID is empty when the immediate capture is pending or
// ambiguous; any visible submitted ID must match MessageID. The settled
// capture must prove submission of MessageID with an empty composer.
type AtomicNoticeReceipt struct {
	MessageID            string
	Recipient            Binding
	InitialComposerEmpty bool
	ImmediateObserved    bool
	ImmediateSubmittedID string
	SettledSubmittedID   string
	SettledComposerEmpty bool
}

// AtomicPaneNotifier is an optional recipient-app capability, separate from
// the mail commit. It must atomically verify blank input or hold an exclusive
// lease across every input writer before submitting the ID-correlated notice.
// On ambiguity it returns an error and must never retry Enter blindly.
type AtomicPaneNotifier interface {
	NotifyPaneAtomically(context.Context, Binding, string) (AtomicNoticeReceipt, error)
}

// PostCommitNoticeBudget is a separate, finite opportunity to notify the
// recipient after durable mail has committed. Earlier state work may consume
// the send command's context without consuming this budget.
const PostCommitNoticeBudget = 2 * time.Second

// NotifyCommittedAfterCommit preserves committed mail even if the send
// context has expired. It carries context values forward, but gives the
// optional notice its own bounded deadline.
func NotifyCommittedAfterCommit(ctx context.Context, rt Runtime, msg Message) NoticeResult {
	return notifyCommittedWithin(ctx, rt, msg, PostCommitNoticeBudget)
}

func notifyCommittedWithin(ctx context.Context, rt Runtime, msg Message, budget time.Duration) NoticeResult {
	noticeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budget)
	defer cancel()
	return NotifyCommitted(noticeCtx, rt, msg)
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
	notifier, ok := rt.(AtomicPaneNotifier)
	if !ok {
		result.Reason = "runtime has no atomic pane notifier"
		return result
	}
	receipt, err := notifier.NotifyPaneAtomically(ctx, target, msg.ID)
	if err != nil {
		if errors.Is(err, ErrPaneNoticeUnsupported) || errors.Is(err, ErrPaneNoticeBusy) {
			result.Status = "unavailable"
			result.Reason = err.Error()
			return result
		}
		result.Status = "unknown"
		result.Reason = err.Error()
		return result
	}
	if receipt.MessageID != msg.ID || receipt.Recipient != target ||
		!receipt.InitialComposerEmpty || !receipt.ImmediateObserved ||
		(receipt.ImmediateSubmittedID != "" && receipt.ImmediateSubmittedID != msg.ID) ||
		receipt.SettledSubmittedID != msg.ID || !receipt.SettledComposerEmpty {
		result.Status = "unknown"
		result.Reason = "recipient UI transition not proven"
		return result
	}
	if err := ctx.Err(); err != nil {
		result.Status = "unknown"
		result.Reason = "notification deadline elapsed: " + err.Error()
		return result
	}
	after, err := rt.Snapshot(ctx)
	if err != nil || validateSnapshotForNotice(after, msg) != nil {
		result.Status = "unknown"
		result.Reason = "recipient binding changed after notice"
		return result
	}
	var stillBound bool
	for _, item := range after.Bindings {
		if item.Role == msg.To && item == target {
			stillBound = true
			break
		}
	}
	if !stillBound {
		result.Status = "unknown"
		result.Reason = "recipient binding changed after notice"
		return result
	}
	if err := ctx.Err(); err != nil {
		result.Status = "unknown"
		result.Reason = "notification deadline elapsed: " + err.Error()
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

// NotifyPaneAtomically makes the native tmux runtime's missing recipient-app
// capability explicit. A tmux pane is an output stream, not an app-owned input
// lease or a source of correlated composer/submission receipts.
func (t Tmux) NotifyPaneAtomically(_ context.Context, _ Binding, _ string) (AtomicNoticeReceipt, error) {
	return AtomicNoticeReceipt{}, ErrPaneNoticeUnsupported
}

// NotifyPane is deliberately disabled. Pasting a comment followed by Enter
// can execute a recipient's partially typed command, including when the
// comment is separated by a trailing space. Durable mail still succeeds.
func (t Tmux) NotifyPane(_ context.Context, _ Binding, _ string) error {
	return ErrPaneNoticeUnsupported
}
