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
}

func (r noticeRuntime) NotifyPane(ctx context.Context, to Binding, id string) error {
	return r.notify(ctx, to, id)
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
