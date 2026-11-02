// Package diagnostic records bounded, best-effort invocation evidence.
// It never authorizes an operation or owns mail/execution recovery.
package diagnostic

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"sync"
	"syscall"
	"time"
)

const (
	MaxEvents = 256
	MaxBytes  = 128 * 1024
)

type Clock interface {
	Wall() time.Time
	Mono() time.Duration
}
type realClock struct{ origin time.Time }

func (c realClock) Wall() time.Time     { return time.Now() }
func (c realClock) Mono() time.Duration { return time.Since(c.origin) }

type Identity struct {
	PanePID            string `json:"pane_pid,omitempty"`
	Command            string `json:"command"`
	BuildVersion       string `json:"build_version"`
	BuildCommit        string `json:"build_commit"`
	ConfigFingerprint  string `json:"config_fingerprint"`
	Workspace          string `json:"workspace"`
	Node               string `json:"node"`
	RuntimeFingerprint string `json:"runtime_fingerprint"`
	Session            string `json:"session"`
	Pane               string `json:"pane"`
}

// Facts contains identifiers/outcomes only. Never add body, argv, error text,
// shell source, environment or child output fields to this allowlist.
type Facts struct {
	Sender         string   `json:"sender,omitempty"`
	Recipient      string   `json:"recipient,omitempty"`
	MessageID      string   `json:"message_id,omitempty"`
	RequestID      string   `json:"request_id,omitempty"`
	ReplyID        string   `json:"reply_id,omitempty"`
	OperationID    string   `json:"operation_id,omitempty"`
	InputRequestID string   `json:"input_request_id,omitempty"`
	ReplyToIDs     []string `json:"reply_to_ids,omitempty"`
	Counterparty   string   `json:"counterparty,omitempty"`
	ObservationID  string   `json:"observation_id,omitempty"`
	Mail           string   `json:"mail_result,omitempty"`
	Notification   string   `json:"notification_result,omitempty"`
	Capture        string   `json:"capture_result,omitempty"`
	Approval       string   `json:"approval_result,omitempty"`
	Execution      string   `json:"execution_result,omitempty"`
	Executed       *bool    `json:"executed,omitempty"`
	ChildExitCode  *int     `json:"child_exit_code,omitempty"`
}

type Metric struct {
	Count   int64   `json:"count"`
	TotalMS float64 `json:"total_ms,omitempty"`
}
type Event struct {
	ReasonCode    string            `json:"reason_code,omitempty"`
	SchemaVersion int               `json:"schema_version"`
	InvocationID  string            `json:"invocation_id"`
	Seq           int               `json:"seq"`
	Time          string            `json:"time"`
	Event         string            `json:"event"`
	Identity      *Identity         `json:"identity,omitempty"`
	Phase         string            `json:"phase,omitempty"`
	SpanID        int               `json:"span_id,omitempty"`
	ParentSpanID  int               `json:"parent_span_id,omitempty"`
	DurationMS    *float64          `json:"duration_ms,omitempty"`
	Outcome       string            `json:"outcome,omitempty"`
	Facts         *Facts            `json:"facts,omitempty"`
	Metrics       map[string]Metric `json:"metrics,omitempty"`
	OmittedEvents int               `json:"omitted_events,omitempty"`
	RecorderMS    float64           `json:"recorder_ms_before_end,omitempty"`
}

var (
	phases  = map[string]bool{}
	metrics = map[string]bool{}
)

func init() {
	for _, s := range []string{"configuration", "identity", "diagnostic_destination", "recipient_context", "mail_staging", "mail_publish", "sender_archive", "pop_claim", "mail_operation", "self_summary", "notification", "notification_paste", "notification_settle", "notification_submit", "notification_confirm", "session_capture", "server_refresh", "approval_wait", "shell_initialization", "child_execution", "execution", "result_encoding", "result_output"} {
		phases[s] = true
	}
	for _, s := range []string{"mutation_lock_wait", "mutation_lock_held", "view_lock_wait", "view_lock_held", "notice_lock_wait", "notice_lock_held", "capture_lock_held", "admission_wait", "approval_observations", "notice_observations", "summary_files_scanned", "summary_body_bytes_decoded", "body_bytes", "panes_discovered", "panes_captured", "panes_reused", "panes_skipped", "panes_failed"} {
		metrics[s] = true
	}
}

func outcome(s string) string {
	switch s {
	case "updated", "skipped_busy", "error", "ok", "failed", "cancelled", "timeout", "unknown", "committed", "staged", "claimed", "empty", "accepted", "unavailable", "not_attempted", "complete", "partial", "approved", "rejected", "advisory", "executed", "not_run", "exited", "dispatched", "execution_failed", "wait_timeout", "delivery_failed", "pending":
		return s
	}
	return "unknown"
}

var (
	messageID     = regexp.MustCompile(`^m-[0-9a-f]{32}$`)
	inputID       = regexp.MustCompile(`^ireq_[0-9a-f]{32}$`)
	node          = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{0,127}$`)
	observationID = regexp.MustCompile(`^[a-zA-Z0-9-]{1,80}$`)
)

func clean(f Facts) Facts {
	for _, p := range []*string{&f.MessageID, &f.RequestID, &f.ReplyID, &f.OperationID} {
		if !messageID.MatchString(*p) {
			*p = ""
		}
	}
	if !inputID.MatchString(f.InputRequestID) {
		f.InputRequestID = ""
	}
	ids := []string{}
	for _, id := range f.ReplyToIDs {
		if len(ids) < 16 && messageID.MatchString(id) {
			ids = append(ids, id)
		}
	}
	f.ReplyToIDs = ids
	for _, p := range []*string{&f.Counterparty, &f.Sender, &f.Recipient} {
		if !node.MatchString(*p) {
			*p = ""
		}
	}
	if !observationID.MatchString(f.ObservationID) {
		f.ObservationID = ""
	}
	for _, p := range []*string{&f.Mail, &f.Notification, &f.Capture, &f.Approval, &f.Execution} {
		if *p != "" {
			*p = outcome(*p)
		}
	}
	if f.ChildExitCode != nil {
		n := *f.ChildExitCode
		if n < 0 || n > 255 {
			f.ChildExitCode = nil
		} else {
			f.ChildExitCode = &n
		}
	}
	if f.Executed != nil {
		b := *f.Executed
		f.Executed = &b
	}
	return f
}

type Recorder struct {
	mu                                 sync.Mutex
	clock                              Clock
	started                            time.Time
	origin                             time.Duration
	id                                 string
	seq, spans, paused, bytes, omitted int
	queue                              []Event
	sink                               io.WriteCloser
	failed, ended                      bool
	identity                           Identity
	counts                             map[string]Metric
	ioTime                             time.Duration
}

func New(command, version, commit string) *Recorder {
	return newRecorder(command, version, commit, realClock{time.Now()})
}

func newRecorder(command, version, commit string, clock Clock) *Recorder {
	started, origin := clock.Wall().UTC(), clock.Mono()
	var b [16]byte
	_, err := rand.Read(b[:])
	r := &Recorder{clock: clock, started: started, origin: origin, id: "inv-" + hex.EncodeToString(b[:]), counts: map[string]Metric{}, failed: err != nil}
	r.identity = Identity{Command: command, BuildVersion: version, BuildCommit: commit}
	r.emitLocked(Event{Event: "start"})
	return r
}
func (r *Recorder) ID() string   { return r.id }
func (r *Recorder) Date() string { return r.started.Format("2006-01-02") }
func (r *Recorder) Failed() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failed
}

func (r *Recorder) MarkFailed() {
	if r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.failed = true
	}
}

func (r *Recorder) Attach(sink io.WriteCloser, identity Identity) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sink != nil || r.ended || r.failed {
		_ = sink.Close()
		return
	}
	identity.Command, identity.BuildVersion, identity.BuildCommit = r.identity.Command, r.identity.BuildVersion, r.identity.BuildCommit
	r.identity = identity
	r.sink = sink
	r.flushLocked()
}

func (r *Recorder) emitLocked(e Event) {
	if r.failed || r.ended {
		return
	}
	if r.seq >= MaxEvents-1 && e.Event != "end" {
		r.omitted++
		return
	}
	e.SchemaVersion = 1
	e.InvocationID = r.id
	e.Seq = r.seq
	r.seq++
	e.Time = r.clock.Wall().UTC().Format(time.RFC3339Nano)
	if e.Event == "start" {
		e.Time = r.started.Format(time.RFC3339Nano)
	}
	r.queue = append(r.queue, e)
	r.flushLocked()
}

func (r *Recorder) flushLocked() {
	if r.sink == nil || r.paused > 0 || r.failed {
		return
	}
	t := r.clock.Mono()
	defer func() { r.ioTime += r.clock.Mono() - t }()
	for _, e := range r.queue {
		if e.Event == "start" {
			i := r.identity
			e.Identity = &i
		}
		b, err := json.Marshal(e)
		if err != nil || len(b) > 4095 || r.bytes+len(b)+1 > MaxBytes {
			r.failed = true
			break
		}
		b = append(b, '\n')
		n, err := r.sink.Write(b)
		r.bytes += n
		if err != nil || n != len(b) {
			r.failed = true
			break
		}
	}
	r.queue = nil
}

// Pause defers diagnostic writes until after the actual mailbox/notice lock
// has been released. Nested pauses and goroutine producers share one recorder.
func (r *Recorder) Pause() func() {
	if r == nil {
		return func() {}
	}
	r.mu.Lock()
	r.paused++
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.paused--
			r.flushLocked()
			if r.ended && r.paused == 0 {
				r.closeLocked()
			}
		})
	}
}

func (r *Recorder) Add(name string, count int64, duration time.Duration) {
	if r == nil || !metrics[name] || count < 0 || duration < 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.counts[name]
	m.Count += count
	m.TotalMS += float64(duration) / float64(time.Millisecond)
	r.counts[name] = m
}

func (r *Recorder) Now() time.Duration {
	if r == nil {
		return 0
	}
	return r.clock.Mono()
}

func (r *Recorder) Measure(name string, start time.Duration) {
	if r != nil {
		r.Add(name, 1, r.Now()-start)
	}
}

type Span struct {
	r     *Recorder
	id    int
	phase string
	start time.Duration
	once  sync.Once
}

func (r *Recorder) Start(phase string) *Span { return r.Child(phase, 0) }
func (r *Recorder) Child(phase string, parent int) *Span {
	s := &Span{}
	if r == nil || !phases[phase] {
		return s
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spans++
	s.r = r
	s.id = r.spans
	s.phase = phase
	s.start = r.clock.Mono()
	r.emitLocked(Event{Event: "phase_start", Phase: phase, SpanID: s.id, ParentSpanID: parent})
	return s
}

func (s *Span) ID() int {
	if s == nil {
		return 0
	}
	return s.id
}

func ErrorOutcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	default:
		return "failed"
	}
}
func (s *Span) Error(err error)   { s.end(ErrorOutcome(err), ReasonCode(err)) }
func (s *Span) End(result string) { s.end(result, "") }
func (s *Span) end(result, reason string) {
	if s == nil || s.r == nil {
		return
	}
	s.once.Do(func() {
		r := s.r
		r.mu.Lock()
		defer r.mu.Unlock()
		ms := float64(r.clock.Mono()-s.start) / float64(time.Millisecond)
		r.emitLocked(Event{Event: "phase_end", Phase: s.phase, SpanID: s.id, DurationMS: &ms, Outcome: outcome(result), ReasonCode: reason})
	})
}

func (r *Recorder) Note(f Facts) {
	if r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		f = clean(f)
		r.emitLocked(Event{Event: "facts", Facts: &f})
	}
}

// End's duration stops BEFORE its own encoding/write/close and later process
// output/exit. No claim is made about fsync durability or filesystem latency.
func (r *Recorder) End(result string, facts Facts) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended {
		return
	}
	ms := float64(r.clock.Mono()-r.origin) / float64(time.Millisecond)
	facts = clean(facts)
	r.emitLocked(Event{Event: "end", DurationMS: &ms, Outcome: outcome(result), Facts: &facts, Metrics: r.counts, OmittedEvents: r.omitted, RecorderMS: float64(r.ioTime) / float64(time.Millisecond)})
	r.ended = true
	if r.paused == 0 {
		r.closeLocked()
	}
}

func (r *Recorder) closeLocked() {
	if r.sink != nil {
		if r.sink.Close() != nil {
			r.failed = true
		}
		r.sink = nil
	}
}

type contextKey struct{}

func With(ctx context.Context, r *Recorder) context.Context {
	return context.WithValue(ctx, contextKey{}, r)
}
func From(ctx context.Context) *Recorder { r, _ := ctx.Value(contextKey{}).(*Recorder); return r }

// ReasonCode never includes an arbitrary error's message or path.
func ReasonCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, os.ErrPermission):
		return "permission_denied"
	case errors.Is(err, os.ErrNotExist):
		return "not_found"
	case errors.Is(err, syscall.EWOULDBLOCK):
		return "lock_busy"
	case errors.Is(err, io.ErrShortWrite):
		return "short_write"
	default:
		return "operation_failed"
	}
}
