package supervisor_test

import (
	"errors"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/memory"
	"github.com/i9wa4/tmux-a2a-postman/internal/supervisor"
)

func newTestOrchestrator(t *testing.T) *supervisor.Orchestrator {
	t.Helper()
	s, err := memory.NewStore(t.TempDir(), "ctx-sup-test")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return supervisor.NewOrchestrator(s)
}

func TestPilotGateLockedInitially(t *testing.T) {
	o := newTestOrchestrator(t)
	err := o.ApproveDelivery()
	if !errors.Is(err, supervisor.ErrPilotGateLocked) {
		t.Errorf("expected ErrPilotGateLocked, got %v", err)
	}
}

func TestPilotGateOpensAfter10ValidatedPhase2Records(t *testing.T) {
	o := newTestOrchestrator(t)

	writeAndValidate := func(n int) {
		t.Helper()
		for range n {
			r, err := o.IngestDecision(memory.Record{
				SourcePhase:    2,
				SituationClass: "escalation",
				Context:        "some situation",
				Decision:       "approve",
				Reasoning:      "ok",
			})
			if err != nil {
				t.Fatalf("IngestDecision: %v", err)
			}
			if err := o.ConfirmOutcome(r.Seq, memory.OutcomeValidated, "good", ""); err != nil {
				t.Fatalf("ConfirmOutcome: %v", err)
			}
		}
	}

	// 9 records → gate still locked.
	writeAndValidate(9)
	if err := o.ApproveDelivery(); !errors.Is(err, supervisor.ErrPilotGateLocked) {
		t.Errorf("expected gate locked at 9, got %v", err)
	}

	// 10th record → gate opens.
	writeAndValidate(1)
	if err := o.ApproveDelivery(); err != nil {
		t.Errorf("expected gate open at 10, got %v", err)
	}
}

func TestPilotGateExcludesPhase1Records(t *testing.T) {
	o := newTestOrchestrator(t)

	// Write 10 Phase 1 validated records — must NOT satisfy gate.
	for range 10 {
		r, err := o.IngestDecision(memory.Record{
			SourcePhase:    1,
			SituationClass: "normal",
			Context:        "p1 ctx",
		})
		if err != nil {
			t.Fatalf("IngestDecision: %v", err)
		}
		_ = o.ConfirmOutcome(r.Seq, memory.OutcomeValidated, "", "")
	}

	if err := o.ApproveDelivery(); !errors.Is(err, supervisor.ErrPilotGateLocked) {
		t.Errorf("Phase 1 records should not satisfy pilot gate; got %v", err)
	}
}

func TestPilotGateExcludesThresholdRecords(t *testing.T) {
	o := newTestOrchestrator(t)

	// Write 10 threshold_ validated records from Phase 2 — must NOT satisfy gate.
	for range 10 {
		r, err := o.IngestDecision(memory.Record{
			SourcePhase:    2,
			SituationClass: "threshold_change",
			Context:        "threshold ctx",
		})
		if err != nil {
			t.Fatalf("IngestDecision: %v", err)
		}
		_ = o.ConfirmOutcome(r.Seq, memory.OutcomeValidated, "", "")
	}

	if err := o.ApproveDelivery(); !errors.Is(err, supervisor.ErrPilotGateLocked) {
		t.Errorf("threshold_ records should not satisfy pilot gate; got %v", err)
	}
}
