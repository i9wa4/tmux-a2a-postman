package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type editorProbe struct {
	target                      Binding
	prefix, text                string
	mode                        string
	pastes, enters, samples     int
	entered, ambiguous          bool
	pastedAt, fullAt, enteredAt time.Time
}

type slowReceiptRuntime struct {
	*storedRuntime
	probe   *editorProbe
	delayed bool
}

func (r *slowReceiptRuntime) Snapshot(ctx context.Context) (Snapshot, error) {
	if r.probe.entered && !r.delayed {
		r.delayed = true
		timer := time.NewTimer(2100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		case <-timer.C:
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
	p := &editorProbe{target: original.snapshot.Bindings[1], prefix: "input> "}
	rt := &slowReceiptRuntime{storedRuntime: original.storedRuntime, probe: p}
	got := m.Store.NotifyCommittedAfterCommit(context.Background(), rt, rt, msg)
	if got.Status != "accepted" || !rt.delayed || p.pastes != 1 || p.enters != 1 {
		t.Fatal("accepted input inherited an expired editor deadline during receipt persistence", got, p.pastes, p.enters)
	}
	again := m.Store.NotifyCommittedAfterCommit(context.Background(), rt, rt, msg)
	if again.Status != "accepted" || p.pastes != 1 || p.enters != 1 {
		t.Fatal("receipt recovery repeated input", again)
	}
}

func (p *editorProbe) noticeFrame(context.Context, Binding) (paneFrame, error) {
	p.samples++
	f := paneFrame{Pane: nativePane{Owner: p.target.Owner, PID: p.target.PanePID}, Width: 240, Height: 24, CursorVisible: true}
	input, history := p.prefix, fmt.Sprintf("background tick %d", p.samples)
	if p.pastes > 0 {
		switch p.mode {
		case "capture-failed":
			return f, ErrUnknown
		case "resized":
			f.Width--
		case "replaced":
			f.Pane.PID = "replaced"
		}
		if p.mode != "no-draw" {
			text := p.text
			if p.mode == "slow-draw" && time.Since(p.pastedAt) < 75*time.Millisecond {
				text = text[:len(text)/2]
			} else if p.fullAt.IsZero() {
				p.fullAt = time.Now()
			}
			input += text
		}
		if p.entered && !p.ambiguous {
			history += "\nsubmitted: " + p.text
			input = p.prefix
		}
	}
	f.Screen = history + "\n" + input
	f.CursorY = strings.Count(history, "\n") + 1
	f.CursorX = len(input)
	return f, nil
}

func (p *editorProbe) pasteNotice(_ context.Context, _ Binding, text string) error {
	p.pastes++
	p.text, p.entered, p.pastedAt, p.fullAt = text, false, time.Now(), time.Time{}
	return nil
}

func (p *editorProbe) enterNotice(context.Context, Binding) error {
	p.enters++
	p.entered, p.enteredAt = true, time.Now()
	return nil
}

func TestObservedNoticeWaitsForFullInputWhileOtherOutputMoves(t *testing.T) {
	m, rt, msg := committedNotice(t)
	p := &editorProbe{target: rt.snapshot.Bindings[1], prefix: "custom-input> ", mode: "slow-draw"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := m.Store.observedNotice(ctx, rt, p, p.target, msg); err != nil {
		t.Fatal(err)
	}
	if p.pastes != 1 || p.enters != 1 || p.fullAt.IsZero() || p.enteredAt.Sub(p.fullAt) < 180*time.Millisecond {
		t.Fatal("submitted before complete input settled", p.pastes, p.enters, p.enteredAt.Sub(p.fullAt))
	}
}

func TestObservedNoticeAmbiguousInputSurvivesAnotherSenderAndRecoversFromEvidence(t *testing.T) {
	// These two consoles have the same visible frames. One could have accepted
	// internally, the other could have inserted a newline. Neither permits an
	// additional Enter or the next sender's paste without fresh evidence.
	for _, kind := range []string{"accepted-with-unchanged-editor", "newline-with-unchanged-editor"} {
		t.Run(kind, func(t *testing.T) {
			m, rt, msg := committedNotice(t)
			p := &editorProbe{target: rt.snapshot.Bindings[1], prefix: "input> ", ambiguous: true}
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			err := m.Store.observedNotice(ctx, rt, p, p.target, msg)
			cancel()
			if err == nil || p.pastes != 1 || p.enters != 1 {
				t.Fatal("ambiguous input was accepted or repeated", err, p.pastes, p.enters)
			}
			fresh, err := m.Send(context.Background(), rt.storedRuntime, "peer", "second request", "", false)
			if err != nil {
				t.Fatal(err)
			}
			// A separate call reads the persisted guard, rather than relying on
			// the first invocation's local state or an in-process mutex.
			if err := m.Store.observedNotice(context.Background(), rt, p, p.target, fresh); err == nil || p.pastes != 1 || p.enters != 1 {
				t.Fatal("next sender overwrote unresolved input", err, p.pastes, p.enters)
			}
			p.ambiguous = false
			ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := m.Store.observedNotice(ctx, rt, p, p.target, fresh); err != nil || p.pastes != 2 || p.enters != 2 {
				t.Fatal("fresh submission evidence did not release the pending guard", err, p.pastes, p.enters)
			}
		})
	}
}

func TestObservedNoticeStopsBeforeEnterWhenFrameIsUntrustworthy(t *testing.T) {
	for _, mode := range []string{"capture-failed", "resized", "replaced", "no-draw"} {
		t.Run(mode, func(t *testing.T) {
			m, rt, msg := committedNotice(t)
			p := &editorProbe{target: rt.snapshot.Bindings[1], prefix: "input> ", mode: mode}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if err := m.Store.observedNotice(ctx, rt, p, p.target, msg); err == nil || p.pastes != 1 || p.enters != 0 {
				t.Fatal("untrustworthy frame authorized Enter", err, p.pastes, p.enters)
			}
			guard, err := m.Store.inputGuard(context.Background(), rt, p.target, nil)
			if err != nil || guard == nil || guard.Status != "pending" {
				t.Fatal("lost uncertainty guard", guard, err)
			}
		})
	}
	m, rt, msg := committedNotice(t)
	p := &editorProbe{target: rt.snapshot.Bindings[1]}
	if err := m.Store.observedNotice(context.Background(), rt, p, p.target, msg); err == nil || p.pastes != 0 || p.enters != 0 || errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("unknown input prefix permitted paste", err)
	}
}

type slowFrameProbe struct {
	*editorProbe
	timeoutBeforeEnter, timeoutAfterEnter, delayed bool
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
	if p.entered && !p.delayed {
		p.delayed = true
		timer := time.NewTimer(2100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return paneFrame{}, ctx.Err()
		case <-timer.C:
		}
	}
	return p.editorProbe.noticeFrame(ctx, target)
}

func TestObservedNoticeRetriesOnlyTimedOutReadsAndKeepsPostSubmitEvidence(t *testing.T) {
	m, rt, msg := committedNotice(t)
	p := &slowFrameProbe{editorProbe: &editorProbe{target: rt.snapshot.Bindings[1], prefix: "input> "}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.Store.observedNotice(ctx, rt, p, p.target, msg); err != nil {
		t.Fatal(err)
	}
	if !p.timeoutBeforeEnter || !p.timeoutAfterEnter || !p.delayed || p.pastes != 1 || p.enters != 1 {
		t.Fatal("short read retries/post-submit deadline repeated input or lost evidence", p)
	}
}
