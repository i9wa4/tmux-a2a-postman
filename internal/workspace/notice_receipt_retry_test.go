package workspace

import (
	"context"
	"testing"
)

type transientReceiptRuntime struct {
	*slowReceiptRuntime
	failed, replaceTarget bool
}

func (r *transientReceiptRuntime) Snapshot(ctx context.Context) (Snapshot, error) {
	if r.probe.entered && !r.failed {
		r.failed = true
		if r.replaceTarget {
			r.snapshot.Bindings[1].PanePID = "99999"
		}
		return Snapshot{}, context.DeadlineExceeded
	}
	return r.storedRuntime.Snapshot(ctx)
}

func TestAcceptedNoticeRetriesOnlyReceiptAfterTransientProbeDeadline(t *testing.T) {
	m, original, msg := committedNotice(t)
	probe := &editorProbe{target: original.snapshot.Bindings[1]}
	rt := &transientReceiptRuntime{slowReceiptRuntime: &slowReceiptRuntime{storedRuntime: original.storedRuntime, probe: probe}}
	got := m.Store.NotifyCommittedAfterCommit(context.Background(), rt, rt, msg)
	if got.Status != "accepted" || !rt.failed || probe.pastes != 1 || probe.enters != 1 {
		t.Fatal(got, rt.failed, probe.pastes, probe.enters)
	}
	if err := m.Store.acceptNotice(context.Background(), rt, rt, msg, probe.target); err != nil {
		t.Fatal("receipt must be idempotent", err)
	}
	if probe.pastes != 1 || probe.enters != 1 {
		t.Fatal("receipt repeated input")
	}
}

func TestReceiptRetryNeverAdoptsAReplacementRecipient(t *testing.T) {
	m, original, msg := committedNotice(t)
	probe := &editorProbe{target: original.snapshot.Bindings[1]}
	rt := &transientReceiptRuntime{slowReceiptRuntime: &slowReceiptRuntime{storedRuntime: original.storedRuntime, probe: probe}, replaceTarget: true}
	got := m.Store.NotifyCommittedAfterCommit(context.Background(), rt, rt, msg)
	if got.Status != "unknown" || !rt.failed || probe.pastes != 1 || probe.enters != 1 {
		t.Fatal(got, rt.failed, probe.pastes, probe.enters)
	}
}
