package memory_test

import (
	"os"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/memory"
)

func newTestStore(t *testing.T) (*memory.Store, string) {
	t.Helper()
	base := t.TempDir()
	s, err := memory.NewStore(base, "ctx-test")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s, base
}

func TestAppendAndLoadAll(t *testing.T) {
	s, _ := newTestStore(t)

	r, err := s.Append(memory.Record{
		SourcePhase:    2,
		SituationClass: "escalation",
		Context:        "user request alpha",
		Decision:       "approve",
		Reasoning:      "standard path",
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if r.Seq != 1 {
		t.Errorf("expected seq=1, got %d", r.Seq)
	}
	if r.Status != memory.StatusDraft {
		t.Errorf("expected status=draft for Phase 2 non-threshold, got %s", r.Status)
	}
	if r.DecisionID == "" {
		t.Error("expected non-empty decision_id")
	}

	records, err := s.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
}

func TestStatusStateMachine(t *testing.T) {
	s, _ := newTestStore(t)

	// Phase 1 → active
	r1, err := s.Append(memory.Record{SourcePhase: 1, SituationClass: "normal", Context: "a"})
	if err != nil {
		t.Fatalf("Append phase1: %v", err)
	}
	if r1.Status != memory.StatusActive {
		t.Errorf("Phase 1: expected active, got %s", r1.Status)
	}

	// threshold_ → active
	r2, err := s.Append(memory.Record{SourcePhase: 2, SituationClass: "threshold_change", Context: "b"})
	if err != nil {
		t.Fatalf("Append threshold: %v", err)
	}
	if r2.Status != memory.StatusActive {
		t.Errorf("threshold_: expected active, got %s", r2.Status)
	}

	// Phase 3 non-threshold → draft
	r3, err := s.Append(memory.Record{SourcePhase: 3, SituationClass: "escalation", Context: "c"})
	if err != nil {
		t.Fatalf("Append phase3: %v", err)
	}
	if r3.Status != memory.StatusDraft {
		t.Errorf("Phase 3 non-threshold: expected draft, got %s", r3.Status)
	}
}

func TestSeqMonotonicity(t *testing.T) {
	s, base := newTestStore(t)

	// Write two valid records.
	if _, err := s.Append(memory.Record{SourcePhase: 1, Context: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(memory.Record{SourcePhase: 1, Context: "y"}); err != nil {
		t.Fatal(err)
	}

	// Manually remove seq=1 file to create a gap.
	gap := base + "/ctx-test/supervisor-memory/00000001.yaml"
	if err := os.Remove(gap); err != nil {
		t.Fatalf("remove seq1: %v", err)
	}

	_, err := s.LoadAll()
	if err == nil {
		t.Error("expected monotonicity error after gap, got nil")
	}
}

func TestUpdateOutcome(t *testing.T) {
	s, _ := newTestStore(t)

	r, _ := s.Append(memory.Record{SourcePhase: 2, SituationClass: "escalation", Context: "ctx"})
	if r.Status != memory.StatusDraft {
		t.Fatalf("pre-condition: expected draft")
	}

	err := s.UpdateOutcome(r.Seq, memory.OutcomeValidated, "looks good", "")
	if err != nil {
		t.Fatalf("UpdateOutcome: %v", err)
	}

	records, _ := s.LoadAll()
	if records[0].Outcome != memory.OutcomeValidated {
		t.Errorf("outcome not updated: %s", records[0].Outcome)
	}
	if records[0].Status != memory.StatusActive {
		t.Errorf("status should be active after validation, got %s", records[0].Status)
	}
}

func TestRankPrecedents(t *testing.T) {
	s, _ := newTestStore(t)

	// Add records with varying contexts.
	for _, ctx := range []string{
		"user request alpha beta",
		"user request gamma",
		"unrelated topic delta",
	} {
		if _, err := s.Append(memory.Record{SourcePhase: 1, SituationClass: "normal", Context: ctx}); err != nil {
			t.Fatal(err)
		}
	}

	// Add overruled record — should be excluded.
	r, _ := s.Append(memory.Record{SourcePhase: 1, SituationClass: "normal", Context: "user request alpha excluded"})
	_ = s.UpdateOutcome(r.Seq, memory.OutcomeOverruled, "bad decision", "")

	// Add threshold record — should be excluded.
	if _, err := s.Append(memory.Record{SourcePhase: 1, SituationClass: "threshold_change", Context: "user request alpha threshold"}); err != nil {
		t.Fatal(err)
	}

	ranked, err := s.RankPrecedents("user request alpha", 3)
	if err != nil {
		t.Fatalf("RankPrecedents: %v", err)
	}

	// First result should be the highest overlap ("user request alpha beta").
	if len(ranked) == 0 {
		t.Fatal("expected at least one result")
	}
	if ranked[0].Context != "user request alpha beta" {
		t.Errorf("expected top precedent 'user request alpha beta', got %q", ranked[0].Context)
	}

	// Overruled and threshold records must not appear.
	for _, r := range ranked {
		if r.Outcome == memory.OutcomeOverruled {
			t.Errorf("overruled record should not appear in precedents")
		}
		if len(r.SituationClass) >= 10 && r.SituationClass[:10] == "threshold_" {
			t.Errorf("threshold_ record should not appear in precedents")
		}
	}
}
