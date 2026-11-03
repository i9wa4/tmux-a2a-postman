package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type editorProbe struct {
	target                  Binding
	prefix, text, mode      string
	pastes, enters, samples int
	entered                 bool
	pastedAt                time.Time
	enterTimes              []time.Time
}

func (p *editorProbe) noticeFrame(context.Context, Binding) (paneFrame, error) {
	p.samples++
	f := paneFrame{Pane: nativePane{Owner: p.target.Owner, PID: p.target.PanePID}, Width: 240, Height: 24, CursorVisible: p.mode != "hidden"}
	history := fmt.Sprintf("background tick %d", p.samples)
	input := p.prefix
	if p.pastes > 0 {
		switch p.mode {
		case "capture-failed":
			return f, ErrUnknown
		case "resized":
			f.Width--
		case "replaced":
			f.Pane.PID = "replacement"
		}
		input += p.text
		if p.mode == "newline" && p.enters == 1 {
			input += "\n"
		}
		if p.entered && p.mode != "unchanged" && (p.mode != "newline" || p.enters > 1) {
			history += "\nsubmitted: " + p.text + "\nprocessing\nnew conversation row\nreply tool running"
			input = p.prefix
		}
		if p.entered && p.mode == "after-replaced" {
			f.Pane.PID = "replacement"
		}
	}
	f.Screen = history + "\n" + input
	// These intentionally do not identify an input area. Notification delivery
	// must work with arbitrary cursor metadata and no visible prompt prefix.
	return f, nil
}

func (p *editorProbe) pasteNotice(_ context.Context, _ Binding, text string) error {
	p.pastes++
	p.text, p.entered, p.pastedAt = text, false, time.Now()
	return nil
}

func (p *editorProbe) enterNotice(context.Context, Binding) error {
	p.enters++
	p.entered = true
	p.enterTimes = append(p.enterTimes, time.Now())
	return nil
}

type slowReceiptRuntime struct {
	*storedRuntime
	probe   *editorProbe
	delayed bool
}

func (r *slowReceiptRuntime) Snapshot(ctx context.Context) (Snapshot, error) {
	if r.probe.entered && !r.delayed {
		r.delayed = true
		if err := noticeWait(ctx, 2100*time.Millisecond); err != nil {
			return Snapshot{}, err
		}
	}
	return r.storedRuntime.Snapshot(ctx)
}
func (r *slowReceiptRuntime) prepareNotice(context.Context, Binding) error { return nil }
func (r *slowReceiptRuntime) noticeFrame(ctx context.Context, b Binding) (paneFrame, error) {
	return r.probe.noticeFrame(ctx, b)
}

func (r *slowReceiptRuntime) pasteNotice(ctx context.Context, b Binding, text string) error {
	return r.probe.pasteNotice(ctx, b, text)
}

func (r *slowReceiptRuntime) enterNotice(ctx context.Context, b Binding) error {
	return r.probe.enterNotice(ctx, b)
}

func TestObservedNoticeReceiptPersistenceHasTheOuterBudget(t *testing.T) {
	m, original, msg := committedNotice(t)
	p := &editorProbe{target: original.snapshot.Bindings[1]}
	rt := &slowReceiptRuntime{storedRuntime: original.storedRuntime, probe: p}
	got := m.Store.NotifyCommittedAfterCommit(context.Background(), rt, rt, msg)
	if got.Status != "accepted" || !rt.delayed || p.pastes != 1 || p.enters != 1 {
		t.Fatal(got, p.pastes, p.enters)
	}
	again := m.Store.NotifyCommittedAfterCommit(context.Background(), rt, rt, msg)
	if again.Status != "accepted" || p.pastes != 1 || p.enters != 1 {
		t.Fatal("receipt repeated input", again)
	}
}

func TestObservedNoticeRetriesNewlineUsingWholeFrameDiff(t *testing.T) {
	for _, mode := range []string{"normal", "newline", "hidden"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			m, rt, msg := committedNotice(t)
			p := &editorProbe{target: rt.snapshot.Bindings[1], mode: mode}
			if err := m.Store.observedNotice(context.Background(), rt, p, p.target, msg); err != nil {
				t.Fatal(err)
			}
			want := 1
			if mode == "newline" {
				want = 2
			}
			if p.pastes != 1 || p.enters != want {
				t.Fatal(p.pastes, p.enters)
			}
			last := p.pastedAt
			for _, at := range p.enterTimes {
				if at.Sub(last) < noticeEnterInterval {
					t.Fatal("Enter was too close to previous input", at.Sub(last))
				}
				last = at
			}
		})
	}
}

func TestObservedNoticeUncertaintyDoesNotBlockLaterMessagesOrReadLegacyGuards(t *testing.T) {
	m, original, msg := committedNotice(t)
	p := &editorProbe{target: original.snapshot.Bindings[1], mode: "unchanged"}
	rt := &slowReceiptRuntime{storedRuntime: original.storedRuntime, probe: p, delayed: true}
	legacy := filepath.Join(m.Store.StateRoot, "repo", "runtime", "input-guards", "peer.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	old := []byte("historical guard is deliberately unreadable as JSON\n")
	if err := os.WriteFile(legacy, old, 0o600); err != nil {
		t.Fatal(err)
	}
	first := m.Store.NotifyCommittedAfterCommit(context.Background(), rt, rt, msg)
	if first.Status != "unknown" || p.pastes != 1 || p.enters != noticeMaxEnters {
		t.Fatal(first, p)
	}
	again := m.Store.NotifyCommittedAfterCommit(context.Background(), rt, rt, msg)
	if again.Status != "unknown" || p.pastes != 1 || p.enters != noticeMaxEnters {
		t.Fatal("uncertain message replayed", again)
	}
	fresh, err := m.Send(context.Background(), rt.storedRuntime, "peer", "second request", "", false)
	if err != nil {
		t.Fatal(err)
	}
	next := m.Store.NotifyCommittedAfterCommit(context.Background(), rt, rt, fresh)
	if next.Status != "unknown" || p.pastes != 2 || p.enters != 2*noticeMaxEnters {
		t.Fatal("later message blocked", next, p)
	}
	saved, err := os.ReadFile(legacy)
	if err != nil || string(saved) != string(old) {
		t.Fatal("legacy guard changed", err)
	}
	if _, err := os.Stat(filepath.Join(m.Store.StateRoot, "repo", "runtime", "notice-locks")); !os.IsNotExist(err) {
		t.Fatal("created destination lock", err)
	}
}

func TestObservedNoticeStopsWhenNativeFrameChanges(t *testing.T) {
	for _, mode := range []string{"capture-failed", "resized", "replaced", "after-replaced"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			m, rt, msg := committedNotice(t)
			p := &editorProbe{target: rt.snapshot.Bindings[1], mode: mode}
			want := 0
			if mode == "after-replaced" {
				want = 1
			}
			if err := m.Store.observedNotice(context.Background(), rt, p, p.target, msg); err == nil || p.pastes != 1 || p.enters != want {
				t.Fatal(err, p)
			}
		})
	}
}

type slowFrameProbe struct {
	*editorProbe
	timeoutBeforeEnter, timeoutAfterEnter bool
}

func (p *slowFrameProbe) noticeFrame(ctx context.Context, target Binding) (paneFrame, error) {
	if p.pastes > 0 && !p.entered && !p.timeoutBeforeEnter {
		p.timeoutBeforeEnter = true
		return paneFrame{}, context.DeadlineExceeded
	}
	if p.entered && !p.timeoutAfterEnter {
		p.timeoutAfterEnter = true
		return paneFrame{}, context.DeadlineExceeded
	}
	return p.editorProbe.noticeFrame(ctx, target)
}

func TestObservedNoticeRetriesOnlyTimedOutReads(t *testing.T) {
	m, rt, msg := committedNotice(t)
	p := &slowFrameProbe{editorProbe: &editorProbe{target: rt.snapshot.Bindings[1]}}
	if err := m.Store.observedNotice(context.Background(), rt, p, p.target, msg); err != nil {
		t.Fatal(err)
	}
	if !p.timeoutBeforeEnter || !p.timeoutAfterEnter || p.pastes != 1 || p.enters != 1 {
		t.Fatal(p)
	}
}

func TestScreenLineChangesCountsInsertionsAndDeletions(t *testing.T) {
	for _, tc := range []struct {
		name, a, b string
		want       int
	}{
		{"padding", "a  \nb\n\n", "a\nb   \n", 0},
		{"newline", "heading\ndraft\nfooter", "heading\ndraft\n\nfooter", 1},
		{"replacement", "heading\ndraft\nfooter", "heading\nchanged\nfooter", 2},
		{"scroll", "top\na\nb\nc\nd\ne", "a\nb\nc\nd\ne\nnew bottom", 2},
		{"status and newline", "old status\ndraft\nfooter", "new status\ndraft\n\nfooter", 3},
		{"large", "one\ntwo\nthree", "four\nfive\nsix", 5},
		{"empty", "", "one\ntwo", 2},
		{"late common line", "b\nc\nd\ne\na", "a", 4},
		{"tall", strings.Repeat("line\n", 10000), strings.Repeat("line\n", 9999) + "changed\n", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := screenLineChanges(tc.a, tc.b); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
}
