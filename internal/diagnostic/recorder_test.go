package diagnostic

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	wall time.Time
	mono time.Duration
}

func (c *fakeClock) Wall() time.Time     { return c.wall }
func (c *fakeClock) Mono() time.Duration { return c.mono }

type memorySink struct {
	bytes.Buffer
	closed bool
}

func (s *memorySink) Close() error { s.closed = true; return nil }
func events(t *testing.T, s *memorySink) []Event {
	t.Helper()
	var got []Event
	for _, line := range bytes.Split(bytes.TrimSpace(s.Bytes()), []byte{'\n'}) {
		var e Event
		if json.Unmarshal(line, &e) != nil {
			t.Fatalf("invalid JSONL %q", line)
		}
		if e.Seq != len(got) || e.SchemaVersion != 1 {
			t.Fatal(e)
		}
		got = append(got, e)
	}
	return got
}

func TestMonotonicNestedSpansAndStartDateAcrossMidnight(t *testing.T) {
	c := &fakeClock{wall: time.Date(2026, 11, 1, 23, 59, 59, 0, time.UTC)}
	r := newRecorder("pop", "test", "test", c)
	s := &memorySink{}
	r.Attach(s, Identity{Node: "builder"})
	outer := r.Start("mail_operation")
	c.mono += time.Second
	c.wall = c.wall.Add(2 * time.Second)
	inner := r.Child("pop_claim", outer.ID())
	c.mono += 2 * time.Second
	c.wall = c.wall.Add(-time.Hour)
	inner.End("claimed")
	outer.End("ok")
	r.End("ok", Facts{Mail: "claimed"})
	got := events(t, s)
	if r.Date() != "2026-11-01" || *got[len(got)-1].DurationMS != 3000 {
		t.Fatal(r.Date(), got)
	}
	if got[2].ParentSpanID != outer.ID() || *got[3].DurationMS != 2000 || *got[4].DurationMS != 3000 {
		t.Fatal(got)
	}
	if got[len(got)-1].Time >= got[0].Time {
		t.Fatal("test did not jump wall clock backwards")
	}
}

func TestPausedCriticalSectionsNeverWriteAndFlushAfterRelease(t *testing.T) {
	r := New("pop", "test", "test")
	s := &memorySink{}
	r.Attach(s, Identity{})
	before := s.Len()
	resume := r.Pause()
	nested := r.Pause()
	span := r.Start("pop_claim")
	span.End("claimed")
	r.End("ok", Facts{})
	if s.Len() != before || s.closed {
		t.Fatal("diagnostic I/O while paused")
	}
	nested()
	if s.Len() != before {
		t.Fatal("nested release flushed")
	}
	resume()
	if !s.closed || len(events(t, s)) != 4 {
		t.Fatal("missing final flush")
	}
}

func TestGoroutinesShareOrderedSpansAndBoundedEvents(t *testing.T) {
	r := New("pop", "test", "test")
	s := &memorySink{}
	r.Attach(s, Identity{})
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() { span := r.Start("mail_operation"); r.Add("summary_files_scanned", 1, 0); span.End("ok") })
	}
	wg.Wait()
	r.End("ok", Facts{})
	got := events(t, s)
	if len(got) != 202 || got[len(got)-1].Metrics["summary_files_scanned"].Count != 100 {
		t.Fatal(len(got))
	}
	r = New("pop", "test", "test")
	s = &memorySink{}
	r.Attach(s, Identity{})
	for range 1000 {
		r.Start("mail_operation").End("ok")
	}
	r.End("ok", Facts{})
	got = events(t, s)
	if len(got) != MaxEvents || got[len(got)-1].OmittedEvents == 0 || s.Len() > MaxBytes {
		t.Fatal(len(got), s.Len())
	}
}

type brokenSink struct {
	short bool
	calls int
}

func (s *brokenSink) Write(b []byte) (int, error) {
	s.calls++
	if s.short {
		return len(b) - 1, nil
	}
	return 0, errors.New("secret-token=DO_NOT_COPY")
}
func (s *brokenSink) Close() error { return nil }
func TestShortAndFailedWritesStopWithoutRetries(t *testing.T) {
	for _, short := range []bool{false, true} {
		r := New("pop", "test", "test")
		s := &brokenSink{short: short}
		r.Attach(s, Identity{})
		r.Start("pop_claim").End("ok")
		r.End("ok", Facts{})
		if !r.Failed() || s.calls != 1 {
			t.Fatal(r.Failed(), s.calls)
		}
	}
}

func TestRedactionAllowsOnlyValidatedIdentifiersAndFiniteOutcomes(t *testing.T) {
	r := New("pop", "test", "test")
	s := &memorySink{}
	r.Attach(s, Identity{})
	r.Note(Facts{MessageID: "secret body", Counterparty: "$(token)", ObservationID: "/secret/path", Mail: "TOKEN", ReplyToIDs: []string{"private", "m-" + strings.Repeat("a", 32)}})
	r.End("ok", Facts{})
	if strings.Contains(s.String(), "secret") || strings.Contains(s.String(), "TOKEN") || strings.Contains(s.String(), "private") || strings.Contains(s.String(), "$(") {
		t.Fatal("unvalidated metadata leaked")
	}
	got := events(t, s)
	if len(got[1].Facts.ReplyToIDs) != 1 || got[1].Facts.Mail != "unknown" {
		t.Fatal(got)
	}
}

func TestInterruptedRecordingHasOpenPhaseAndNoInventedEnd(t *testing.T) {
	r := New("execute-heredoc", "test", "test")
	s := &memorySink{}
	r.Attach(s, Identity{})
	r.Start("approval_wait")
	got := events(t, s)
	if len(got) != 2 || got[1].Event != "phase_start" {
		t.Fatal(got)
	}
}

var _ io.WriteCloser = (*memorySink)(nil)
