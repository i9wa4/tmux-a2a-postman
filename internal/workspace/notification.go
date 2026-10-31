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
	Status string `json:"status"`
	Pane   string `json:"pane,omitempty"`
	Reason string `json:"reason,omitempty"`
}

const PostCommitNoticeBudget = 2 * time.Second

type notifierRegistration struct {
	Version int     `json:"version"`
	Binding Binding `json:"binding"`
}

// RegisterNotifier is called directly by recipient Bash after installing its
// Readline binding. A sender cannot register another pane's notifier.
func (s Store) RegisterNotifier(ctx context.Context, rt Runtime) error {
	return s.WithVerified(ctx, rt, func(ws *os.Root, snapshot Snapshot) error {
		if snapshot.Caller.PanePID != strconv.Itoa(os.Getppid()) {
			return errors.New("notifier registration must be a direct child of the pane's Bash process")
		}
		runtimeDir, err := privateDir(ws, "runtime", false)
		if err != nil {
			return err
		}
		defer func() { _ = runtimeDir.Close() }()
		notices, err := privateDir(runtimeDir, "bash-notifiers", true)
		if err != nil {
			return err
		}
		defer func() { _ = notices.Close() }()
		if err := syncDirectory(runtimeDir); err != nil {
			return err
		}
		return replaceJSON(notices, snapshot.Caller.Role+".json", notifierRegistration{1, snapshot.Caller})
	})
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
	err := s.withView(ctx, source, func(ws *os.Root, snapshot Snapshot) error {
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
	if err != nil {
		result.Reason = "committed sender binding unavailable: " + err.Error()
		return result
	}
	var target Binding
	err = s.withView(ctx, destination, func(ws *os.Root, snapshot Snapshot) error {
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
		notices, err := privateDir(runtimeDir, "bash-notifiers", false)
		if err != nil {
			return err
		}
		defer func() { _ = notices.Close() }()
		var registered notifierRegistration
		if err := readJSON(notices, msg.To+".json", &registered); err != nil {
			return err
		}
		if registered.Version != 1 || registered.Binding != target {
			return errors.New("recipient notifier binding is stale")
		}
		return nil
	})
	if err != nil {
		result.Reason = "recipient Bash notifier unavailable: " + err.Error()
		return result
	}
	result.Pane = target.Owner.Pane
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
