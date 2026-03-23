// Package memory implements the supervisor memory store for Phase 2 deployment.
// Records are persisted as YAML files under <baseDir>/<contextID>/supervisor-memory/.
// Issue #307.
package memory

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	dirMode  = 0o700
	fileMode = 0o600
)

// Status represents the lifecycle state of a decision record.
type Status string

const (
	StatusDraft  Status = "draft"
	StatusActive Status = "active"
)

// Outcome represents the result of a supervisory decision.
type Outcome string

const (
	OutcomeValidated Outcome = "validated"
	OutcomeOverruled Outcome = "overruled"
	OutcomePending   Outcome = ""
)

// Record is a single supervisory decision entry stored in YAML.
// Structural fields (seq, decision_id, timestamp, source_phase, situation_class,
// context, decision, reasoning) are immutable after creation.
type Record struct {
	Seq            uint64   `yaml:"seq"`
	DecisionID     string   `yaml:"decision_id"`
	Timestamp      string   `yaml:"timestamp"`
	SourcePhase    int      `yaml:"source_phase"`
	Status         Status   `yaml:"status"`
	SituationClass string   `yaml:"situation_class"`
	Context        string   `yaml:"context"`
	Decision       string   `yaml:"decision"`
	Reasoning      string   `yaml:"reasoning"`
	Precedents     []string `yaml:"precedents"`
	Confidence     float64  `yaml:"confidence"`
	Escalated      bool     `yaml:"escalated"`
	Outcome        Outcome  `yaml:"outcome"`
	HumanFeedback  string   `yaml:"human_feedback"`
	FailureMode    string   `yaml:"failure_mode"`
	StakesLow      bool     `yaml:"stakes_low"`
}

// Store manages the on-disk supervisor memory for a single context.
type Store struct {
	dir string
}

// NewStore returns a Store rooted at <baseDir>/<contextID>/supervisor-memory/.
// The directory is created if it does not exist.
func NewStore(baseDir, contextID string) (*Store, error) {
	dir := filepath.Join(baseDir, contextID, "supervisor-memory")
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("memory: create store dir %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// recordPath returns the YAML file path for a given seq number.
func (s *Store) recordPath(seq uint64) string {
	return filepath.Join(s.dir, fmt.Sprintf("%08d.yaml", seq))
}

// generateDecisionID returns a CSPRNG-derived hex ID of the form "<prefix>-<8hex>".
// Up to 3 retries; falls back to a fixed 8-hex-digit suffix if all fail.
func generateDecisionID(prefix string) string {
	for range 3 {
		b := make([]byte, 4)
		if _, err := rand.Read(b); err == nil {
			return prefix + "-" + hex.EncodeToString(b)
		}
	}
	// Fallback: deterministic 8-hex-digit suffix from time (last resort).
	return fmt.Sprintf("%s-%08x", prefix, time.Now().UnixNano()&0xffffffff)
}

// initialStatus returns the correct status for a new record per the state machine:
// - Phase 1 records → active
// - threshold_* situation_class → active
// - Phase 2/3 non-threshold records → draft
func initialStatus(sourcePhase int, situationClass string) Status {
	if sourcePhase == 1 || strings.HasPrefix(situationClass, "threshold_") {
		return StatusActive
	}
	return StatusDraft
}

// Append writes a new Record to disk atomically and returns it.
// The seq is assigned as (current max seq + 1).
// decision_id, timestamp, and status are set automatically.
func (s *Store) Append(r Record) (Record, error) {
	records, err := s.LoadAll()
	if err != nil {
		return Record{}, err
	}

	var nextSeq uint64 = 1
	if len(records) > 0 {
		nextSeq = records[len(records)-1].Seq + 1
	}

	r.Seq = nextSeq
	r.Timestamp = time.Now().UTC().Format(time.RFC3339)
	r.DecisionID = generateDecisionID(fmt.Sprintf("d%04x", nextSeq))
	r.Status = initialStatus(r.SourcePhase, r.SituationClass)

	path := s.recordPath(r.Seq)
	if err := atomicWriteYAML(path, r); err != nil {
		return Record{}, fmt.Errorf("memory: append seq %d: %w", r.Seq, err)
	}
	return r, nil
}

// atomicWriteYAML encodes v as YAML and writes it atomically (tmp → rename).
func atomicWriteYAML(path string, v any) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("yaml marshal: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fileMode)
	if err != nil {
		return fmt.Errorf("open tmp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("sync tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename tmp->final: %w", err)
	}
	return nil
}

// LoadAll reads all records from the store directory in seq order.
// Returns an error (halt signal) if seq gaps or duplicates are found.
func (s *Store) LoadAll() ([]Record, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("memory: read dir %s: %w", s.dir, err)
	}

	var records []Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(s.dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("memory: read %s: %w", e.Name(), err)
		}
		var r Record
		if err := yaml.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("memory: parse %s: %w", e.Name(), err)
		}
		records = append(records, r)
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].Seq < records[j].Seq
	})

	if err := checkMonotonicity(records); err != nil {
		return nil, err
	}
	return records, nil
}

// checkMonotonicity returns an error if records have gaps or duplicate seq values.
func checkMonotonicity(records []Record) error {
	for i, r := range records {
		expected := uint64(i + 1)
		if r.Seq != expected {
			return fmt.Errorf(
				"memory: seq monotonicity violation at index %d: expected %d, got %d (gap or duplicate)",
				i, expected, r.Seq,
			)
		}
	}
	return nil
}

// UpdateOutcome rewrites a record's mutable fields (outcome, human_feedback,
// failure_mode, status) atomically. Structural fields are left unchanged.
func (s *Store) UpdateOutcome(seq uint64, outcome Outcome, humanFeedback, failureMode string) error {
	records, err := s.LoadAll()
	if err != nil {
		return err
	}
	idx := -1
	for i, r := range records {
		if r.Seq == seq {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("memory: seq %d not found", seq)
	}
	records[idx].Outcome = outcome
	records[idx].HumanFeedback = humanFeedback
	records[idx].FailureMode = failureMode
	if outcome == OutcomeValidated {
		records[idx].Status = StatusActive
	}
	return atomicWriteYAML(s.recordPath(seq), records[idx])
}

// RankPrecedents returns up to n records ranked by term-overlap with queryContext.
// Records with outcome=overruled or situation_class starting with "threshold_"
// are excluded from ranking (per spec).
func (s *Store) RankPrecedents(queryContext string, n int) ([]Record, error) {
	records, err := s.LoadAll()
	if err != nil {
		return nil, err
	}

	queryTerms := tokenize(queryContext)

	type scored struct {
		r     Record
		score int
	}
	var candidates []scored
	for _, r := range records {
		if r.Outcome == OutcomeOverruled {
			continue
		}
		if strings.HasPrefix(r.SituationClass, "threshold_") {
			continue
		}
		candidates = append(candidates, scored{r: r, score: termOverlap(queryTerms, tokenize(r.Context))})
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	if n > len(candidates) {
		n = len(candidates)
	}
	result := make([]Record, n)
	for i := range n {
		result[i] = candidates[i].r
	}
	return result, nil
}

// PilotGateThreshold is the minimum number of qualifying records required
// before the supervisor may act autonomously (pilot gate open).
const PilotGateThreshold = 10

// PilotGateReady reports whether the pilot gate is open.
// The gate opens when at least PilotGateThreshold records satisfy:
//   - source_phase >= 2
//   - outcome == validated
//   - situation_class does NOT start with "threshold_"
func (s *Store) PilotGateReady() (bool, error) {
	records, err := s.LoadAll()
	if err != nil {
		return false, err
	}
	count := 0
	for _, r := range records {
		if r.SourcePhase >= 2 &&
			r.Outcome == OutcomeValidated &&
			!strings.HasPrefix(r.SituationClass, "threshold_") {
			count++
		}
	}
	return count >= PilotGateThreshold, nil
}

// CountResolvedPhase2Plus returns the count of all resolved (outcome != pending)
// Phase 2+ non-threshold records. Used by ConfidenceManager to detect batch
// boundaries (Issue #309).
func (s *Store) CountResolvedPhase2Plus() (int, error) {
	records, err := s.LoadAll()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, r := range records {
		if r.SourcePhase >= 2 &&
			r.Outcome != OutcomePending &&
			!strings.HasPrefix(r.SituationClass, "threshold_") {
			count++
		}
	}
	return count, nil
}

// OverruleRate computes the overrule rate for the most recent n resolved Phase 2+
// non-threshold records. Returns 0.0 if fewer than n qualifying records exist.
// overrule_rate = overruled_count / n (Issue #309).
func (s *Store) OverruleRate(n int) (float64, error) {
	records, err := s.LoadAll()
	if err != nil {
		return 0, err
	}

	var resolved []Record
	for _, r := range records {
		if r.SourcePhase >= 2 &&
			r.Outcome != OutcomePending &&
			!strings.HasPrefix(r.SituationClass, "threshold_") {
			resolved = append(resolved, r)
		}
	}
	if len(resolved) < n {
		return 0.0, nil
	}

	window := resolved[len(resolved)-n:]
	overruled := 0
	for _, r := range window {
		if r.Outcome == OutcomeOverruled {
			overruled++
		}
	}
	return float64(overruled) / float64(n), nil
}

// RestoreThreshold scans records in reverse seq order and returns the Confidence
// field of the most recent threshold_change or threshold_reset record with
// status active. Returns (0, false, nil) if no such record exists (Issue #309).
func (s *Store) RestoreThreshold() (float64, bool, error) {
	records, err := s.LoadAll()
	if err != nil {
		return 0, false, err
	}
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if (r.SituationClass == "threshold_change" || r.SituationClass == "threshold_reset") &&
			r.Status == StatusActive {
			return r.Confidence, true, nil
		}
	}
	return 0, false, nil
}

// AnnotatePendingRollback writes failure_mode="phase3_rollback" on all records
// with outcome==pending without changing their status or outcome. Used during
// Phase 3 → Phase 2 rollback drain procedure (Issue #309).
func (s *Store) AnnotatePendingRollback() error {
	records, err := s.LoadAll()
	if err != nil {
		return err
	}
	for _, r := range records {
		if r.Outcome == OutcomePending {
			r.FailureMode = "phase3_rollback"
			if writeErr := atomicWriteYAML(s.recordPath(r.Seq), r); writeErr != nil {
				return fmt.Errorf("memory: annotate pending rollback seq %d: %w", r.Seq, writeErr)
			}
		}
	}
	return nil
}

// tokenize splits text into lowercase words for term-overlap scoring.
func tokenize(text string) map[string]struct{} {
	words := strings.Fields(strings.ToLower(text))
	m := make(map[string]struct{}, len(words))
	for _, w := range words {
		w = strings.Trim(w, ".,;:!?\"'")
		if w != "" {
			m[w] = struct{}{}
		}
	}
	return m
}

// termOverlap counts terms present in both sets.
func termOverlap(a, b map[string]struct{}) int {
	count := 0
	for t := range a {
		if _, ok := b[t]; ok {
			count++
		}
	}
	return count
}
