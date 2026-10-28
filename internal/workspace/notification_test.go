package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type noticeRuntime struct {
	*storedRuntime
	notify func(context.Context, Binding, string) error
	atomic func(context.Context, Binding, string) (AtomicNoticeReceipt, error)
}

func (r noticeRuntime) NotifyPaneAtomically(ctx context.Context, to Binding, id string) (AtomicNoticeReceipt, error) {
	if r.atomic != nil {
		return r.atomic(ctx, to, id)
	}
	if err := r.notify(ctx, to, id); err != nil {
		return AtomicNoticeReceipt{}, err
	}
	return AtomicNoticeReceipt{
		MessageID: id, Recipient: to, InitialComposerEmpty: true,
		ImmediateObserved: true, ImmediateSubmittedID: id,
		SettledSubmittedID: id, SettledComposerEmpty: true,
	}, nil
}

func TestNotifyCommittedAtomicCapabilityFailsClosed(t *testing.T) {
	m := mailFixture(t)
	msg, err := m.Send(context.Background(), mailRuntime("worker"), "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		atomic func(context.Context, Binding, string) (AtomicNoticeReceipt, error)
		status string
	}{
		{name: "busy composer", atomic: func(context.Context, Binding, string) (AtomicNoticeReceipt, error) {
			return AtomicNoticeReceipt{}, ErrPaneNoticeBusy
		}, status: "unavailable"},
		{name: "stale message", atomic: func(_ context.Context, to Binding, _ string) (AtomicNoticeReceipt, error) {
			return AtomicNoticeReceipt{MessageID: "m-00000000000000000000000000000000", Recipient: to, InitialComposerEmpty: true, ImmediateObserved: true, ImmediateSubmittedID: "m-00000000000000000000000000000000", SettledSubmittedID: "m-00000000000000000000000000000000", SettledComposerEmpty: true}, nil
		}, status: "unknown"},
		{name: "unrelated immediate UI", atomic: func(_ context.Context, to Binding, id string) (AtomicNoticeReceipt, error) {
			return AtomicNoticeReceipt{MessageID: id, Recipient: to, InitialComposerEmpty: true, ImmediateObserved: true, ImmediateSubmittedID: "m-00000000000000000000000000000000", SettledSubmittedID: id, SettledComposerEmpty: true}, nil
		}, status: "unknown"},
		{name: "unproven settled UI", atomic: func(_ context.Context, to Binding, id string) (AtomicNoticeReceipt, error) {
			return AtomicNoticeReceipt{MessageID: id, Recipient: to, InitialComposerEmpty: true, ImmediateObserved: true, ImmediateSubmittedID: id}, nil
		}, status: "unknown"},
		{name: "unrelated recipient", atomic: func(_ context.Context, to Binding, id string) (AtomicNoticeReceipt, error) {
			to.Owner.Pane = "%99"
			return AtomicNoticeReceipt{MessageID: id, Recipient: to, InitialComposerEmpty: true, ImmediateObserved: true, ImmediateSubmittedID: id, SettledSubmittedID: id, SettledComposerEmpty: true}, nil
		}, status: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := noticeRuntime{storedRuntime: mailRuntime("worker"), atomic: tc.atomic}
			got := NotifyCommitted(context.Background(), rt, msg)
			if got.Status != tc.status || got.Pane != "%2" {
				t.Fatalf("notice outcome = %+v, want %s bound to %%2", got, tc.status)
			}
		})
	}
	rt := noticeRuntime{storedRuntime: mailRuntime("worker")}
	rt.atomic = func(_ context.Context, to Binding, id string) (AtomicNoticeReceipt, error) {
		rt.snapshot.Bindings[1].Owner.Pane = "%99"
		return AtomicNoticeReceipt{MessageID: id, Recipient: to, InitialComposerEmpty: true, ImmediateObserved: true, ImmediateSubmittedID: id, SettledSubmittedID: id, SettledComposerEmpty: true}, nil
	}
	if got := NotifyCommitted(context.Background(), rt, msg); got.Status != "unknown" {
		t.Fatalf("rebound recipient reported sent: %+v", got)
	}
}

func TestTmuxAtomicNoticeCapabilityFailsClosed(t *testing.T) {
	receipt, err := (Tmux{}).NotifyPaneAtomically(context.Background(), Binding{}, "m-0123456789abcdef0123456789abcdef")
	if !errors.Is(err, ErrPaneNoticeUnsupported) || receipt != (AtomicNoticeReceipt{}) {
		t.Fatalf("native tmux claimed app-owned notice: %+v, %v", receipt, err)
	}
}

func TestNotifyCommittedRejectsUnprovenAppTransitions(t *testing.T) {
	m := mailFixture(t)
	msg, err := m.Send(context.Background(), mailRuntime("worker"), "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	valid := func(to Binding, id string) AtomicNoticeReceipt {
		return AtomicNoticeReceipt{
			MessageID: id, Recipient: to, InitialComposerEmpty: true,
			ImmediateObserved: true, ImmediateSubmittedID: id,
			SettledSubmittedID: id, SettledComposerEmpty: true,
		}
	}
	for _, tc := range []struct {
		name  string
		alter func(*AtomicNoticeReceipt)
	}{
		{"initial composer occupied", func(r *AtomicNoticeReceipt) { r.InitialComposerEmpty = false }},
		{"immediate state absent", func(r *AtomicNoticeReceipt) { r.ImmediateObserved = false }},
		{"settled different message", func(r *AtomicNoticeReceipt) { r.SettledSubmittedID = "m-00000000000000000000000000000000" }},
		{"settled composer occupied", func(r *AtomicNoticeReceipt) { r.SettledComposerEmpty = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := noticeRuntime{storedRuntime: mailRuntime("worker")}
			rt.atomic = func(_ context.Context, to Binding, id string) (AtomicNoticeReceipt, error) {
				receipt := valid(to, id)
				tc.alter(&receipt)
				return receipt, nil
			}
			if got := NotifyCommitted(context.Background(), rt, msg); got.Status != "unknown" {
				t.Fatalf("unproven app transition reported sent: %+v", got)
			}
		})
	}
}

func TestNotifyCommittedDelayedSettledSubmission(t *testing.T) {
	m := mailFixture(t)
	msg, err := m.Send(context.Background(), mailRuntime("worker"), "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	rt := noticeRuntime{storedRuntime: mailRuntime("worker")}
	rt.atomic = func(ctx context.Context, to Binding, id string) (AtomicNoticeReceipt, error) {
		if to != rt.snapshot.Bindings[1] || to.Owner.Pane != "%2" || id != msg.ID {
			t.Fatalf("notice lost message or pane incarnation: %+v, %s", to, id)
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 100*time.Millisecond {
			t.Fatalf("notice has no bounded observation: %v, %v", deadline, ok)
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			return AtomicNoticeReceipt{}, ctx.Err()
		}
		return AtomicNoticeReceipt{
			MessageID: id, Recipient: to, InitialComposerEmpty: true,
			ImmediateObserved:  true, // Pending or ambiguous; no submitted ID yet.
			SettledSubmittedID: id, SettledComposerEmpty: true,
		}, nil
	}
	if got := notifyCommittedWithin(context.Background(), rt, msg, 100*time.Millisecond); got.Status != "sent" || got.Pane != "%2" {
		t.Fatalf("settled same-message transition not accepted: %+v", got)
	}
}

func TestNotifyCommittedAfterCommitGetsFreshBoundedContext(t *testing.T) {
	m := mailFixture(t)
	msg, err := m.Send(context.Background(), mailRuntime("worker"), "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancel(context.Background())
	cancel() // Models a send/Self phase that exhausted its original context.
	rt := noticeRuntime{storedRuntime: mailRuntime("worker")}
	rt.notify = func(ctx context.Context, to Binding, id string) error {
		if err := ctx.Err(); err != nil {
			t.Fatalf("notice inherited exhausted context: %v", err)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > PostCommitNoticeBudget {
			t.Fatalf("notice has no finite fresh budget: %v %v", deadline, ok)
		}
		if to.Role != "peer" || id != msg.ID {
			t.Fatalf("notice lost committed correlation: %s %s", to.Role, id)
		}
		return nil
	}
	if got := NotifyCommittedAfterCommit(parent, rt, msg); got.Status != "sent" {
		t.Fatalf("notice outcome after expired send context: %+v", got)
	}
}

func TestNotifyCommittedAfterCommitTimeoutKeepsMail(t *testing.T) {
	m := mailFixture(t)
	msg, err := m.Send(context.Background(), mailRuntime("worker"), "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	rt := noticeRuntime{storedRuntime: mailRuntime("worker")}
	rt.notify = func(ctx context.Context, _ Binding, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	start := time.Now()
	got := notifyCommittedWithin(context.Background(), rt, msg, 25*time.Millisecond)
	if got.Status != "unknown" || time.Since(start) > time.Second {
		t.Fatalf("notice timeout was not bounded and separate: %+v", got)
	}
	for _, path := range []string{mailPath(m, "peer", "inbox", msg.ID), mailPath(m, "worker", "sent", msg.ID)} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("committed mail missing after notice timeout: %s: %v", path, err)
		}
	}
}

func TestNotifyCommittedLateValidReceiptNeverReportsSent(t *testing.T) {
	m := mailFixture(t)
	msg, err := m.Send(context.Background(), mailRuntime("worker"), "peer", "body", "", false)
	if err != nil {
		t.Fatal(err)
	}
	rt := noticeRuntime{storedRuntime: mailRuntime("worker")}
	rt.atomic = func(ctx context.Context, to Binding, id string) (AtomicNoticeReceipt, error) {
		<-ctx.Done()
		return AtomicNoticeReceipt{
			MessageID: id, Recipient: to, InitialComposerEmpty: true,
			ImmediateObserved: true, ImmediateSubmittedID: id,
			SettledSubmittedID: id, SettledComposerEmpty: true,
		}, nil
	}
	got := notifyCommittedWithin(context.Background(), rt, msg, 25*time.Millisecond)
	if got.Status != "unknown" || got.Reason == "" {
		t.Fatalf("late valid receipt reported sent: %+v", got)
	}
	for _, path := range []string{mailPath(m, "peer", "inbox", msg.ID), mailPath(m, "worker", "sent", msg.ID)} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("committed mail missing after late notice: %s: %v", path, err)
		}
	}
}

func TestNotifyCommittedSeparatesMailFromNotificationFailure(t *testing.T) {
	m := mailFixture(t)
	rt := noticeRuntime{storedRuntime: mailRuntime("worker")}
	rt.notify = func(_ context.Context, to Binding, id string) error {
		if to.Role != "peer" || to.Owner.Pane != "%2" || !messageID(id) {
			t.Fatalf("wrong notification target: %#v %q", to, id)
		}
		return errors.New("injected pane failure")
	}
	result, err := m.SendWithState(context.Background(), rt, "peer", "body", "", "", false, []string{"peer"})
	if err != nil || !result.Self.Known {
		t.Fatalf("send lost its committed result: %+v %v", result, err)
	}
	notice := NotifyCommitted(context.Background(), rt, result.Message)
	if notice.Status != "unknown" || notice.Reason == "" || notice.Pane != "%2" {
		t.Fatalf("failure misreported as send rollback or success: %+v", notice)
	}
	for _, path := range []string{mailPath(m, "peer", "inbox", result.Message.ID), mailPath(m, "worker", "sent", result.Message.ID)} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("committed mail missing after notification failure: %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(m.Store.StateRoot, "repo", "transactions", "send-"+result.Message.ID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed send retained pending intent: %v", err)
	}
}

func TestNotifyCommittedDoesNotHoldMailLock(t *testing.T) {
	m := mailFixture(t)
	first, err := m.Send(context.Background(), mailRuntime("worker"), "peer", "first", "", false)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	rt := noticeRuntime{storedRuntime: mailRuntime("worker")}
	rt.notify = func(context.Context, Binding, string) error {
		close(entered)
		<-release
		return nil
	}
	notified := make(chan NoticeResult, 1)
	go func() { notified <- NotifyCommitted(context.Background(), rt, first) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("notification did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	second, err := m.Send(ctx, mailRuntime("worker"), "peer", "second", "", false)
	close(release)
	if err != nil || second.ID == first.ID {
		t.Fatalf("notification held workspace mutation lock: %+v %v", second, err)
	}
	select {
	case result := <-notified:
		if result.Status != "sent" {
			t.Fatalf("notification outcome: %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notification did not finish")
	}
}

func TestNotifyCommittedUnknownTargetDoesNotCallNotifier(t *testing.T) {
	rt := noticeRuntime{storedRuntime: mailRuntime("worker")}
	rt.notify = func(context.Context, Binding, string) error {
		t.Fatal("unverified pane was notified")
		return nil
	}
	rt.snapshot.Bindings = rt.snapshot.Bindings[:1]
	result := NotifyCommitted(context.Background(), rt, Message{ID: "m-0123456789abcdef0123456789abcdef", From: "worker", To: "peer"})
	if result.Status != "unavailable" || result.Pane != "" {
		t.Fatalf("missing target reported as notified: %+v", result)
	}
}

func TestNotifyCommittedCrossSessionDoesNotSelectLocalSameNamedRole(t *testing.T) {
	rt := noticeRuntime{storedRuntime: mailRuntime("worker")}
	rt.notify = func(context.Context, Binding, string) error {
		t.Fatal("cross-session notice targeted a local pane")
		return nil
	}
	result := NotifyCommitted(context.Background(), rt, Message{
		ID: "m-0123456789abcdef0123456789abcdef", From: "worker", FromSession: "parent",
		To: "worker", ToSession: "child",
	})
	if result.Status != "unavailable" || result.Pane != "" || result.Reason != "cross-session pane notification unsupported" {
		t.Fatalf("cross-session notice named local pane: %+v", result)
	}
}
