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

// Orchestrator is the supervisor-orchestrator component. It coordinates the
// flow between the judge and memory store: record ingest, gate check, and
// draft/active routing.
type Orchestrator struct {
	store *memory.Store
	judge *Judge
}

// NewOrchestrator constructs an Orchestrator with its own Judge.
func NewOrchestrator(store *memory.Store) *Orchestrator {
	return &Orchestrator{
		store: store,
		judge: NewJudge(store),
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
