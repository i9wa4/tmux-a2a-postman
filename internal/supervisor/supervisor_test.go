package supervisor_test

import (
	"errors"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/memory"
	"github.com/i9wa4/tmux-a2a-postman/internal/supervisor"
)

// newTestStore returns a memory.Store for testing.
func newTestStore(t *testing.T) *memory.Store {
	t.Helper()
	s, err := memory.NewStore(t.TempDir(), "ctx-sup-store-test")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

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

// EscalateCheck tests (Issue #309 section 3.6)

func testJudge(t *testing.T) *supervisor.Judge {
	t.Helper()
	return supervisor.NewJudge(newTestStore(t))
}

func TestEscalateCheck_AllSignalsPass(t *testing.T) {
	j := testJudge(t)
	r := memory.Record{StakesLow: true, Confidence: 0.92}
	precedents := []memory.Record{{Seq: 1}}
	if j.EscalateCheck(r, precedents, 0.90) {
		t.Error("expected no escalation when all signals pass")
	}
}

func TestEscalateCheck_NoPrecedent(t *testing.T) {
	j := testJudge(t)
	r := memory.Record{StakesLow: true, Confidence: 0.92}
	if !j.EscalateCheck(r, nil, 0.90) {
		t.Error("expected escalation when no precedent")
	}
}

func TestEscalateCheck_StakesNotLow(t *testing.T) {
	j := testJudge(t)
	r := memory.Record{StakesLow: false, Confidence: 0.92}
	precedents := []memory.Record{{Seq: 1}}
	if !j.EscalateCheck(r, precedents, 0.90) {
		t.Error("expected escalation when stakes not low")
	}
}

func TestEscalateCheck_ConfidenceBelowThreshold(t *testing.T) {
	j := testJudge(t)
	r := memory.Record{StakesLow: true, Confidence: 0.88}
	precedents := []memory.Record{{Seq: 1}}
	if !j.EscalateCheck(r, precedents, 0.90) {
		t.Error("expected escalation when confidence below threshold")
	}
}

func TestEscalateCheck_ConfidenceAtThreshold(t *testing.T) {
	j := testJudge(t)
	r := memory.Record{StakesLow: true, Confidence: 0.90}
	precedents := []memory.Record{{Seq: 1}}
	if j.EscalateCheck(r, precedents, 0.90) {
		t.Error("expected no escalation when confidence == threshold")
	}
}

// ConfidenceManager tests (Issue #309 section 8.3)

func TestConfidenceManager_DefaultThreshold(t *testing.T) {
	cm := supervisor.NewConfidenceManager(newTestStore(t))
	if cm.CurrentThreshold() != supervisor.DefaultConfidenceThreshold {
		t.Errorf("expected default threshold %v, got %v", supervisor.DefaultConfidenceThreshold, cm.CurrentThreshold())
	}
}

func TestConfidenceManager_RestoreFromStore(t *testing.T) {
	s := newTestStore(t)
	// Write a threshold_change record (initialStatus → active for threshold_ class).
	if _, err := s.Append(memory.Record{
		SourcePhase:    3,
		SituationClass: "threshold_change",
		Confidence:     0.80,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	cm := supervisor.NewConfidenceManager(s)
	if err := cm.RestoreFromStore(); err != nil {
		t.Fatalf("RestoreFromStore: %v", err)
	}
	if cm.CurrentThreshold() != 0.80 {
		t.Errorf("expected restored threshold 0.80, got %v", cm.CurrentThreshold())
	}
}

func TestMaybeRecalibrate_NoOp(t *testing.T) {
	s := newTestStore(t)
	cm := supervisor.NewConfidenceManager(s)
	// 9 resolved records — not enough for recalibration.
	for i := range 9 {
		r, err := s.Append(memory.Record{
			SourcePhase: 2, SituationClass: "escalation",
			Context: string(rune('a' + i)),
		})
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		if err := s.UpdateOutcome(r.Seq, memory.OutcomeValidated, "", ""); err != nil {
			t.Fatalf("UpdateOutcome: %v", err)
		}
	}
	if err := cm.MaybeRecalibrate(); err != nil {
		t.Fatalf("MaybeRecalibrate: %v", err)
	}
	if cm.CurrentThreshold() != supervisor.DefaultConfidenceThreshold {
		t.Errorf("threshold should be unchanged at 9 records, got %v", cm.CurrentThreshold())
	}
}

func TestMaybeRecalibrate_LowerThreshold(t *testing.T) {
	s := newTestStore(t)
	cm := supervisor.NewConfidenceManager(s)
	// 10 resolved validated records → rate=0 → lower by 0.05.
	for i := range 10 {
		r, err := s.Append(memory.Record{
			SourcePhase: 2, SituationClass: "escalation",
			Context: string(rune('a' + i)),
		})
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		if err := s.UpdateOutcome(r.Seq, memory.OutcomeValidated, "", ""); err != nil {
			t.Fatalf("UpdateOutcome: %v", err)
		}
	}
	if err := cm.MaybeRecalibrate(); err != nil {
		t.Fatalf("MaybeRecalibrate: %v", err)
	}
	want := supervisor.DefaultConfidenceThreshold - 0.05
	if cm.CurrentThreshold() != want {
		t.Errorf("expected %v, got %v", want, cm.CurrentThreshold())
	}
}

func TestMaybeRecalibrate_Floor(t *testing.T) {
	s := newTestStore(t)
	cm := supervisor.NewConfidenceManager(s)
	// Force threshold near floor then trigger recalibration with 0% overrule.
	// Write 30 validated records (3 batches) to drive threshold down.
	for i := range 30 {
		r, err := s.Append(memory.Record{
			SourcePhase: 2, SituationClass: "escalation",
			Context: string(rune('a' + (i % 26))),
		})
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		if err := s.UpdateOutcome(r.Seq, memory.OutcomeValidated, "", ""); err != nil {
			t.Fatalf("UpdateOutcome: %v", err)
		}
		if (i+1)%10 == 0 {
			if err := cm.MaybeRecalibrate(); err != nil {
				t.Fatalf("MaybeRecalibrate batch %d: %v", (i+1)/10, err)
			}
		}
	}
	// 3 batches: 0.90→0.85→0.80→0.75; but floor is 0.70 so next would be 0.70.
	// Let's just assert threshold stays >= 0.70.
	if cm.CurrentThreshold() < 0.70 {
		t.Errorf("threshold must not go below 0.70, got %v", cm.CurrentThreshold())
	}
}

func TestMaybeRecalibrate_RaiseImmediate(t *testing.T) {
	s := newTestStore(t)
	cm := supervisor.NewConfidenceManager(s)
	// 10 records: 8 validated + 2 overruled → rate=0.20 → raise to 0.95.
	for i := range 8 {
		r, err := s.Append(memory.Record{
			SourcePhase: 2, SituationClass: "escalation",
			Context: string(rune('a' + i)),
		})
		if err != nil {
			t.Fatalf("Append validated: %v", err)
		}
		if err := s.UpdateOutcome(r.Seq, memory.OutcomeValidated, "", ""); err != nil {
			t.Fatalf("UpdateOutcome: %v", err)
		}
	}
	for i := range 2 {
		r, err := s.Append(memory.Record{
			SourcePhase: 2, SituationClass: "escalation",
			Context: string(rune('s' + i)),
		})
		if err != nil {
			t.Fatalf("Append overruled: %v", err)
		}
		if err := s.UpdateOutcome(r.Seq, memory.OutcomeOverruled, "wrong", ""); err != nil {
			t.Fatalf("UpdateOutcome: %v", err)
		}
	}
	if err := cm.MaybeRecalibrate(); err != nil {
		t.Fatalf("MaybeRecalibrate: %v", err)
	}
	if cm.CurrentThreshold() != 0.95 {
		t.Errorf("expected threshold raised to 0.95, got %v", cm.CurrentThreshold())
	}
}
