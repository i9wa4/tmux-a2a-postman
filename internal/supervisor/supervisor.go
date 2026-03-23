// Package supervisor implements the Phase 2 supervisory layer for tmux-a2a-postman.
// It wires three logical roles: supervisor-orchestrator, supervisor-memory, and
// supervisor-judge, and enforces the pilot gate before autonomous delivery.
// Issue #307.
package supervisor

import (
	"fmt"

	"github.com/i9wa4/tmux-a2a-postman/internal/memory"
)

// ErrPilotGateLocked is returned by CheckPilotGate when autonomous delivery
// is not yet permitted.
var ErrPilotGateLocked = fmt.Errorf("supervisor: pilot gate locked — insufficient validated Phase 2 non-threshold records")

// Judge is the supervisor-judge component. It evaluates memory records to
// determine whether the pilot gate is open, and selects precedents for a
// given situation.
type Judge struct {
	store *memory.Store
}

// NewJudge constructs a Judge backed by the given memory store.
func NewJudge(store *memory.Store) *Judge {
	return &Judge{store: store}
}

// CheckPilotGate returns nil if autonomous delivery is permitted, or
// ErrPilotGateLocked if the threshold has not been reached.
//
// Gate condition: source_phase >= 2 AND outcome = validated AND
// situation_class NOT LIKE threshold_% count >= memory.PilotGateThreshold.
func (j *Judge) CheckPilotGate() error {
	ready, err := j.store.PilotGateReady()
	if err != nil {
		return fmt.Errorf("supervisor: pilot gate check: %w", err)
	}
	if !ready {
		return ErrPilotGateLocked
	}
	return nil
}

// SelectPrecedents returns up to n ranked precedent records for the given
// situation context. Delegates to the memory store's ranking algorithm.
func (j *Judge) SelectPrecedents(situationContext string, n int) ([]memory.Record, error) {
	return j.store.RankPrecedents(situationContext, n)
}

// EscalateCheck returns true when escalation to channel S is required.
// Escalation is required when any of the three signals fails:
//
//	Signal 1: at least one precedent exists
//	Signal 2: r.StakesLow is true
//	Signal 3: r.Confidence >= threshold
//
// All three must hold for autonomous delivery; any failure triggers escalation
// (Issue #309 section 3.6).
func (j *Judge) EscalateCheck(r memory.Record, precedents []memory.Record, threshold float64) bool {
	if len(precedents) == 0 {
		return true
	}
	if !r.StakesLow {
		return true
	}
	if r.Confidence < threshold {
		return true
	}
	return false
}

// DefaultConfidenceThreshold is the initial confidence threshold for Phase 3
// autonomous decisions (Issue #309 section 8.3).
const DefaultConfidenceThreshold = 0.90

// ConfidenceManager manages the autonomous confidence threshold. It recalibrates
// after every 10 resolved Phase 2+ records and persists changes as
// threshold_change records in the memory store (Issue #309 section 8.3).
type ConfidenceManager struct {
	store     *memory.Store
	threshold float64
	lastCount int
}

// NewConfidenceManager creates a ConfidenceManager with the default threshold.
func NewConfidenceManager(store *memory.Store) *ConfidenceManager {
	return &ConfidenceManager{
		store:     store,
		threshold: DefaultConfidenceThreshold,
	}
}

// CurrentThreshold returns the active confidence threshold.
func (cm *ConfidenceManager) CurrentThreshold() float64 {
	return cm.threshold
}

// RestoreFromStore restores the active threshold from the most recent
// threshold_change or threshold_reset record. Also resets the batch watermark
// so recalibration picks up from current state on restart.
func (cm *ConfidenceManager) RestoreFromStore() error {
	val, found, err := cm.store.RestoreThreshold()
	if err != nil {
		return fmt.Errorf("supervisor: restore threshold: %w", err)
	}
	if found {
		cm.threshold = val
	}
	count, err := cm.store.CountResolvedPhase2Plus()
	if err != nil {
		return fmt.Errorf("supervisor: restore threshold count: %w", err)
	}
	cm.lastCount = count
	return nil
}

// MaybeRecalibrate checks whether 10 new resolved Phase 2+ records have
// accumulated since the last recalibration. If so, it adjusts the threshold
// and writes a threshold_change record.
//
// Rules (Issue #309 section 8.3):
//   - overrule_rate < 0.10: lower threshold by 0.05, floor 0.70
//   - overrule_rate >= 0.10: raise to 0.95 (ceil), immediately
func (cm *ConfidenceManager) MaybeRecalibrate() error {
	currentCount, err := cm.store.CountResolvedPhase2Plus()
	if err != nil {
		return fmt.Errorf("supervisor: recalibrate count: %w", err)
	}
	delta := currentCount - cm.lastCount
	if delta < 10 {
		return nil
	}

	rate, err := cm.store.OverruleRate(10)
	if err != nil {
		return fmt.Errorf("supervisor: recalibrate overrule rate: %w", err)
	}

	newThreshold := cm.threshold
	if rate >= 0.10 {
		newThreshold = 0.95
	} else {
		newThreshold = cm.threshold - 0.05
		if newThreshold < 0.70 {
			newThreshold = 0.70
		}
	}

	if _, err := cm.store.Append(memory.Record{
		SourcePhase:    3,
		SituationClass: "threshold_change",
		Reasoning:      fmt.Sprintf("batch recalibration: overrule_rate=%.4f prev=%.4f new=%.4f", rate, cm.threshold, newThreshold),
		Confidence:     newThreshold,
	}); err != nil {
		return fmt.Errorf("supervisor: recalibrate append threshold_change: %w", err)
	}

	cm.threshold = newThreshold
	cm.lastCount = currentCount
	return nil
}

// Orchestrator is the supervisor-orchestrator component. It coordinates the
// flow between the judge and memory store: record ingest, gate check, and
// draft/active routing.
type Orchestrator struct {
	store             *memory.Store
	judge             *Judge
	confidenceManager *ConfidenceManager
}

// NewOrchestrator constructs an Orchestrator with its own Judge and ConfidenceManager.
func NewOrchestrator(store *memory.Store) *Orchestrator {
	return &Orchestrator{
		store:             store,
		judge:             NewJudge(store),
		confidenceManager: NewConfidenceManager(store),
	}
}

// IngestDecision records a new supervisory decision and returns the persisted
// record. The record's status is set automatically by the memory package per
// the state machine rules.
func (o *Orchestrator) IngestDecision(r memory.Record) (memory.Record, error) {
	saved, err := o.store.Append(r)
	if err != nil {
		return memory.Record{}, fmt.Errorf("supervisor: ingest decision: %w", err)
	}
	return saved, nil
}

// ApproveDelivery checks the pilot gate and returns nil when autonomous
// delivery of a Phase 2/3 decision is permitted.
func (o *Orchestrator) ApproveDelivery() error {
	return o.judge.CheckPilotGate()
}

// ConfirmOutcome updates the outcome of a stored decision (e.g., after human
// review via channel S) and persists the change atomically.
func (o *Orchestrator) ConfirmOutcome(seq uint64, outcome memory.Outcome, humanFeedback, failureMode string) error {
	return o.store.UpdateOutcome(seq, outcome, humanFeedback, failureMode)
}

// Memory returns the underlying memory store (supervisor-memory role accessor).
func (o *Orchestrator) Memory() *memory.Store {
	return o.store
}

// Judge returns the underlying judge (supervisor-judge role accessor).
func (o *Orchestrator) Judge() *Judge {
	return o.judge
}

// ConfidenceManager returns the confidence recalibration manager (Issue #309).
func (o *Orchestrator) ConfidenceManager() *ConfidenceManager {
	return o.confidenceManager
}
