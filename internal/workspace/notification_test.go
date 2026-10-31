package workspace

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type noticeRuntime struct {
	*storedRuntime
	notify     func(context.Context, Binding, string) error
	prepareErr error
}

func (r noticeRuntime) prepareNotice(context.Context, Binding) error { return r.prepareErr }
func (r noticeRuntime) sendNotice(ctx context.Context, b Binding, text string) error {
	return r.notify(ctx, b, text)
}

func committedNotice(t *testing.T) (Mail, noticeRuntime, Message) {
	t.Helper()
	m := mailFixture(t)
	source := mailRuntime("worker")
	msg, err := m.Send(context.Background(), source, "peer", "complete durable body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	return m, noticeRuntime{storedRuntime: source, notify: func(context.Context, Binding, string) error { return nil }}, msg
}

func TestNoticeFreshBudgetAndNoMailLock(t *testing.T) {
	m, rt, msg := committedNotice(t)
	rt.notify = func(ctx context.Context, b Binding, text string) error {
		if ctx.Err() != nil || b.Role != "peer" || !strings.Contains(text, "You've got mail: "+msg.ID+".md") || strings.Contains(text, msg.Body) {
			t.Fatal(ctx.Err(), b, text)
		}
		d, ok := ctx.Deadline()
		if !ok || time.Until(d) > PostCommitNoticeBudget {
			t.Fatal("unbounded notice")
		}
		return m.Store.WithVerified(ctx, rt.storedRuntime, noMutation)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := m.Store.NotifyCommittedAfterCommit(ctx, rt, rt, msg)
	if got.Status != "accepted" || got.Pane != "%2" {
		t.Fatal(got)
	}
	if _, err := os.Stat(mailPath(m, "peer", "inbox", msg.ID)); err != nil {
		t.Fatal(err)
	}
}

func TestNoticeAmbiguityIsNeverReplayed(t *testing.T) {
	m, rt, msg := committedNotice(t)
	calls := 0
	rt.notify = func(ctx context.Context, _ Binding, _ string) error { calls++; <-ctx.Done(); return ctx.Err() }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	got := m.Store.notifyCommitted(ctx, rt, rt, msg)
	if got.Status != "unknown" {
		t.Fatal(got)
	}
	again := m.Store.NotifyCommittedAfterCommit(context.Background(), rt, rt, msg)
	if again.Status != "unknown" || calls != 1 {
		t.Fatal("ambiguous notification replayed", again, calls)
	}
	for _, p := range []string{mailPath(m, "peer", "inbox", msg.ID), mailPath(m, "worker", "sent", msg.ID)} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNoticeRejectedBeforePasteCanBeRetried(t *testing.T) {
	m, rt, msg := committedNotice(t)
	calls := 0
	rt.notify = func(context.Context, Binding, string) error { calls++; return nil }
	rt.prepareErr = ErrUnknown
	if got := m.Store.NotifyCommittedAfterCommit(context.Background(), rt, rt, msg); got.Status != "unavailable" {
		t.Fatal(got)
	}
	rt.prepareErr = nil
	if got := m.Store.NotifyCommittedAfterCommit(context.Background(), rt, rt, msg); got.Status != "accepted" {
		t.Fatal(got)
	}
	if got := m.Store.NotifyCommittedAfterCommit(context.Background(), rt, rt, msg); got.Status != "accepted" || calls != 1 {
		t.Fatal(got, calls)
	}
}

func TestNoticeRejectsUncommittedAndCrossSessionTargets(t *testing.T) {
	for _, mode := range []string{"uncommitted", "cross"} {
		t.Run(mode, func(t *testing.T) {
			m, rt, msg := committedNotice(t)
			rt.notify = func(context.Context, Binding, string) error { t.Fatal("unverified target notified"); return nil }
			if mode == "uncommitted" {
				msg.Body = "different payload"
			} else {
				msg.FromSession = "parent"
				msg.ToSession = "child"
			}
			if got := m.Store.NotifyCommittedAfterCommit(context.Background(), rt, rt, msg); got.Status != "unavailable" {
				t.Fatal(got)
			}
		})
	}
}

func TestConcurrentDuplicateNoticeSubmitsOnce(t *testing.T) {
	m, _, msg := committedNotice(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	notify := func(context.Context, Binding, string) error {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return nil
	}
	results := make(chan NoticeResult, 2)
	run := func() {
		rt := noticeRuntime{storedRuntime: mailRuntime("worker"), notify: notify}
		results <- m.Store.NotifyCommittedAfterCommit(context.Background(), rt, rt, msg)
	}
	go run()
	<-entered
	go run()
	// The transport lock must not keep another mail operation from publishing.
	if _, err := m.Send(context.Background(), mailRuntime("worker"), "peer", "independent", "", false); err != nil {
		t.Fatal(err)
	}
	close(release)
	for range 2 {
		if got := <-results; got.Status != "accepted" {
			t.Fatal(got)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate paste", calls.Load())
	}
}

func TestNotificationTextRetainsOriginalProtocol(t *testing.T) {
	text := noticePrompt(Message{ID: "m-0123456789abcdef0123456789abcdef", To: "peer", Body: "NEVER IN HINT"})
	if !strings.HasPrefix(text, "<!-- message start --> Hello, peer! You've got mail: ") || !strings.HasSuffix(text, "<!-- end of message -->") || strings.ContainsAny(text, "\r\n\x1b") || strings.Contains(text, "NEVER IN HINT") {
		t.Fatal(text)
	}
}

func TestApprovalNoticeFailureDoesNotApproveOrResend(t *testing.T) {
	m := mailFixture(t)
	e := Executor{Mail: m, Resolve: approvalResolver("peer")}
	e.wait = func(context.Context) error {
		req := pendingApproval(t, m)
		approvalReply(t, m, req, requestCommand(t, req), "rejected")
		return nil
	}
	result, err := e.Execute(context.Background(), mailRuntime("worker"), "false")
	if err == nil || result.Status != "rejected" || result.Notice == nil || result.Notice.Status != "unavailable" {
		t.Fatal(result, err)
	}
}

func TestNoticeTransportErrorKeepsMail(t *testing.T) {
	m, rt, msg := committedNotice(t)
	rt.notify = func(context.Context, Binding, string) error { return errors.New("tmux disconnected after paste") }
	if got := m.Store.NotifyCommittedAfterCommit(context.Background(), rt, rt, msg); got.Status != "unknown" {
		t.Fatal(got)
	}
	if _, err := os.Stat(mailPath(m, "peer", "inbox", msg.ID)); err != nil {
		t.Fatal(err)
	}
}
