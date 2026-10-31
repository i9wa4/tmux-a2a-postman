package workspace

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

type noticeRuntime struct {
	*storedRuntime
	notify func(context.Context, Binding) error
}

func (r noticeRuntime) notifyBash(ctx context.Context, b Binding) error { return r.notify(ctx, b) }
func registeredNotice(t *testing.T) (Mail, noticeRuntime, Message) {
	t.Helper()
	m := mailFixture(t)
	source := mailRuntime("worker")
	peer := mailRuntime("peer")
	for _, rt := range []*storedRuntime{source, peer} {
		for i := range rt.snapshot.Bindings {
			rt.snapshot.Bindings[i].PanePID = strconv.Itoa(os.Getppid())
		}
		for _, b := range rt.snapshot.Bindings {
			if b.Role == rt.snapshot.Caller.Role {
				rt.snapshot.Caller = b
			}
		}
	}
	if err := m.Store.RegisterNotifier(context.Background(), peer); err != nil {
		t.Fatal(err)
	}
	msg, err := m.Send(context.Background(), source, "peer", "complete durable body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	return m, noticeRuntime{storedRuntime: source, notify: func(context.Context, Binding) error { return nil }}, msg
}

func TestNoticeFreshBudgetAndNoMailLock(t *testing.T) {
	m, rt, msg := registeredNotice(t)
	rt.notify = func(ctx context.Context, b Binding) error {
		if ctx.Err() != nil || b.Role != "peer" {
			t.Fatal(ctx.Err(), b)
		}
		d, ok := ctx.Deadline()
		if !ok || time.Until(d) > PostCommitNoticeBudget {
			t.Fatal("unbounded notice")
		}
		// A recipient can acquire the workspace mutation lock during notification.
		return m.Store.WithVerified(ctx, rt.storedRuntime, noMutation)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := m.Store.NotifyCommittedAfterCommit(ctx, rt, rt, msg)
	if got.Status != "accepted" || got.Pane != "%2" {
		t.Fatal(got)
	}
	// Acceptance alone must not claim the message.
	if _, err := os.Stat(mailPath(m, "peer", "inbox", msg.ID)); err != nil {
		t.Fatal(err)
	}
}

func TestNoticeFailureKeepsDelivery(t *testing.T) {
	m, rt, msg := registeredNotice(t)
	rt.notify = func(ctx context.Context, _ Binding) error { <-ctx.Done(); return ctx.Err() }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	got := m.Store.notifyCommitted(ctx, rt, rt, msg)
	if got.Status != "unknown" {
		t.Fatal(got)
	}
	for _, p := range []string{mailPath(m, "peer", "inbox", msg.ID), mailPath(m, "worker", "sent", msg.ID)} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNoticeRejectsUncommittedStaleAndCrossSessionTargets(t *testing.T) {
	for _, mode := range []string{"uncommitted", "stale", "cross"} {
		t.Run(mode, func(t *testing.T) {
			m, rt, msg := registeredNotice(t)
			rt.notify = func(context.Context, Binding) error { t.Fatal("unverified target notified"); return nil }
			switch mode {
			case "uncommitted":
				msg.Body = "different payload"
			case "stale":
				rt.snapshot.Bindings[1].PanePID = "99999"
			case "cross":
				msg.FromSession = "parent"
				msg.ToSession = "child"
			}
			if got := m.Store.NotifyCommittedAfterCommit(context.Background(), rt, rt, msg); got.Status != "unavailable" {
				t.Fatal(got)
			}
		})
	}
}

func TestRegisterNotifierCannotRegisterAnotherPane(t *testing.T) {
	m := mailFixture(t)
	rt := mailRuntime("peer")
	if err := m.Store.RegisterNotifier(context.Background(), rt); err == nil {
		t.Fatal("registered notifier without owning Bash parent")
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
