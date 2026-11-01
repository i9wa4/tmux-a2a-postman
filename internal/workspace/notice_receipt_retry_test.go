package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type transientReceiptRuntime struct {
	*slowReceiptRuntime
	failed, replaceTarget bool
	acceptedGuardPath     string
}

func (r *transientReceiptRuntime) Snapshot(ctx context.Context) (Snapshot, error) {
	ready := r.probe.entered
	if ready && r.acceptedGuardPath != "" {
		raw, err := os.ReadFile(r.acceptedGuardPath)
		var record struct {
			Status string `json:"status"`
		}
		ready = err == nil && json.Unmarshal(raw, &record) == nil && record.Status == "accepted"
	}
	if ready && !r.failed {
		r.failed = true
		if r.replaceTarget {
			r.snapshot.Bindings[1].PanePID = "99999"
		}
		return Snapshot{}, context.DeadlineExceeded
	}
	return r.storedRuntime.Snapshot(ctx)
}

func TestAcceptedNoticeRetriesOnlyReceiptAfterTransientProbeDeadline(t *testing.T) {
	for _, phase := range []string{"accepted-guard", "accepted-receipt"} {
		t.Run(phase, func(t *testing.T) {
			m, original, msg := committedNotice(t)
			probe := &editorProbe{target: original.snapshot.Bindings[1], prefix: "input> "}
			rt := &transientReceiptRuntime{slowReceiptRuntime: &slowReceiptRuntime{storedRuntime: original.storedRuntime, probe: probe}}
			if phase == "accepted-receipt" {
				rt.acceptedGuardPath = filepath.Join(m.Store.StateRoot, "repo", "runtime", "input-guards", probe.target.Role+".json")
			}
			got := m.Store.NotifyCommittedAfterCommit(context.Background(), rt, rt, msg)
			if got.Status != "accepted" || !rt.failed || probe.pastes != 1 || probe.enters != 1 {
				t.Fatal(got, rt.failed, probe.pastes, probe.enters)
			}
			if err := m.Store.acceptNotice(context.Background(), rt, msg, probe.target); err != nil {
				t.Fatal("accepted receipt must be idempotent", err)
			}
			if probe.pastes != 1 || probe.enters != 1 {
				t.Fatal("receipt persistence repeated terminal input")
			}
		})
	}
}

func TestReceiptRetryNeverAdoptsAReplacementRecipient(t *testing.T) {
	m, original, msg := committedNotice(t)
	probe := &editorProbe{target: original.snapshot.Bindings[1], prefix: "input> "}
	rt := &transientReceiptRuntime{slowReceiptRuntime: &slowReceiptRuntime{storedRuntime: original.storedRuntime, probe: probe}, replaceTarget: true}
	got := m.Store.NotifyCommittedAfterCommit(context.Background(), rt, rt, msg)
	if got.Status != "unknown" || !rt.failed || probe.pastes != 1 || probe.enters != 1 {
		t.Fatal(got, rt.failed, probe.pastes, probe.enters)
	}
}
