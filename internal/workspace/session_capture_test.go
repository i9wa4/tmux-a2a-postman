package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSessionCaptureCoversWindowsUnconfiguredPanesAndRetainsHistory(t *testing.T) {
	f := newTmuxFixture(t)
	adapter := f.adapter()
	other := f.run(t, "new-window", "-d", "-t", "one", "-P", "-F", "#{pane_id}", "printf NATIVE_UNCONFIGURED; exec sleep 120")
	f.run(t, "select-pane", "-t", other, "-T", "ordinary-terminal")
	f.run(t, "new-session", "-d", "-s", "unrelated", "exec sleep 120")
	m := mailFixture(t)
	if _, err := m.Claim(context.Background(), adapter, ""); err != nil {
		t.Fatal(err)
	}
	snapshot, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first := m.CaptureSession(context.Background(), adapter, snapshot.Caller)
	if !first.Complete || first.Captured != 2 || first.Discovered != 2 || first.Reason != "" {
		t.Fatalf("session coverage: %+v", first)
	}
	_, _, status := m.Summary(context.Background(), adapter, "worker")
	if status.LastCaptureAt == nil || status.LastScreenChangeAt != nil {
		t.Fatalf("first sample clocks: %+v", status)
	}
	dir := filepath.Join(m.Store.StateRoot, "repo", "runtime", "observations")
	entries, _ := os.ReadDir(dir)
	var nativeText bool
	for _, entry := range entries {
		raw, _ := os.ReadFile(filepath.Join(dir, entry.Name()))
		if strings.Contains(string(raw), "NATIVE_UNCONFIGURED") {
			nativeText = true
		}
	}
	if !nativeText {
		t.Fatal("unconfigured native pane text not saved")
	}
	if _, err := os.Stat(filepath.Join(m.Store.StateRoot, "repo", "agents", "ordinary-terminal")); !os.IsNotExist(err) {
		t.Fatal("capture invented an agent mailbox", err)
	}
	// Equal samples retain an unknown first-change time. Native output changes
	// the clock without implying work, acceptance or model continuity.
	second := m.CaptureSession(context.Background(), adapter, snapshot.Caller)
	if !second.Complete {
		t.Fatal(second)
	}
	_, _, status = m.Summary(context.Background(), adapter, "worker")
	if status.LastScreenChangeAt != nil {
		t.Fatal("equal samples invented a change", status)
	}
	f.run(t, "send-keys", "-t", f.pane, "-l", "screen changed")
	time.Sleep(30 * time.Millisecond)
	third := m.CaptureSession(context.Background(), adapter, snapshot.Caller)
	if !third.Complete {
		t.Fatal(third)
	}
	_, _, status = m.Summary(context.Background(), adapter, "worker")
	if status.LastScreenChangeAt == nil {
		t.Fatal("observed change missing", status)
	}
	old := filepath.Join(dir, first.ID+".json")
	yearAgo := time.Now().Add(-365 * 24 * time.Hour)
	if err := os.Chtimes(old, yearAgo, yearAgo); err != nil {
		t.Fatal(err)
	}
	_ = m.CaptureSession(context.Background(), adapter, snapshot.Caller)
	if _, err := os.Stat(old); err != nil {
		t.Fatal("capture expired history", err)
	}
	// Replacing a native pane process invalidates its old observation clock.
	f.run(t, "respawn-pane", "-k", "-t", f.pane, "exec sleep 120")
	_, _, status = m.Summary(context.Background(), adapter, "worker")
	if status.LastCaptureAt != nil || status.LastScreenChangeAt != nil {
		t.Fatal("replacement inherited old evidence", status)
	}
}

func TestSessionCaptureConcurrentAttemptsKeepOneCoherentLatestIndex(t *testing.T) {
	f := newTmuxFixture(t)
	adapter, m := f.adapter(), mailFixture(t)
	if _, err := m.Claim(context.Background(), adapter, ""); err != nil {
		t.Fatal(err)
	}
	snapshot, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	start, results := make(chan struct{}), make(chan SessionSnapshot, 8)
	for range 8 {
		go func() { <-start; results <- m.CaptureSession(context.Background(), adapter, snapshot.Caller) }()
	}
	close(start)
	complete := 0
	for range 8 {
		r := <-results
		if r.Complete {
			complete++
		} else if r.Reason == "" {
			t.Fatal("partial capture omitted its reason", r)
		}
	}
	if complete == 0 {
		t.Fatal("all concurrent captures were lost")
	}
	dir := filepath.Join(m.Store.StateRoot, "repo", "runtime", "observations")
	raw, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index snapshotIndex
	if json.Unmarshal(raw, &index) != nil || len(index.Panes) != 1 {
		t.Fatal("corrupted concurrent index", string(raw))
	}
	for _, clock := range index.Panes {
		raw, err := os.ReadFile(filepath.Join(dir, clock.File))
		var saved savedPane
		if err != nil || json.Unmarshal(raw, &saved) != nil || saved.CapturedAt != clock.CapturedAt || saved.Owner != clock.Owner || saved.Fingerprint != clock.Fingerprint {
			t.Fatal("latest clock refers to a partial or unrelated record", clock, err)
		}
	}
}

func TestSessionCapturePaneLimitRecordsIncompleteCoverage(t *testing.T) {
	f := newTmuxFixture(t)
	for range SnapshotPaneLimit + 1 {
		f.run(t, "new-window", "-d", "-t", "one", "-c", f.repo, "exec sleep 120")
	}
	adapter, m := f.adapter(), mailFixture(t)
	snapshot, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := m.CaptureSession(context.Background(), adapter, snapshot.Caller)
	if r.Complete || !r.Enumerated || r.Discovered != SnapshotPaneLimit+2 || r.Captured > SnapshotPaneLimit || r.Captured+r.Skipped != r.Discovered || len(r.Panes) > SnapshotPaneLimit {
		t.Fatal("oversized session was presented as complete", r)
	}
}

func TestSessionCaptureSlowNativeCallsPreserveThePreviousClock(t *testing.T) {
	f := newTmuxFixture(t)
	adapter, m := f.adapter(), mailFixture(t)
	snapshot, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first := m.CaptureSession(context.Background(), adapter, snapshot.Caller)
	if !first.Complete {
		t.Fatal(first)
	}
	_, _, before := m.Summary(context.Background(), adapter, "worker")
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	proxyDir := t.TempDir()
	proxy := fmt.Sprintf("#!/bin/sh\ncase \"$*\" in *capture-pane*) sleep 1 ;; esac\nexec %s \"$@\"\n", shellQuote(realTmux))
	if err := os.WriteFile(filepath.Join(proxyDir, "tmux"), []byte(proxy), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", proxyDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	start := time.Now()
	r := m.CaptureSession(context.Background(), adapter, snapshot.Caller)
	if time.Since(start) > 4*time.Second || r.Complete || r.Captured != 0 || r.Skipped != 1 || r.Panes[0].Reason == "" {
		t.Fatal("slow capture was not bounded or looked complete", r)
	}
	_, _, after := m.Summary(context.Background(), adapter, "worker")
	if before.LastCaptureAt == nil || after.LastCaptureAt == nil || *before.LastCaptureAt != *after.LastCaptureAt {
		t.Fatal("failed capture advanced the successful observation time", before, after)
	}
}

func TestSessionCaptureNativeHistoryBoundsAndStaleFrames(t *testing.T) {
	f := newTmuxFixture(t)
	f.run(t, "resize-window", "-t", "one", "-x", "120", "-y", "40")
	f.run(t, "respawn-pane", "-k", "-t", f.pane, `sh -c 'i=1; while [ $i -le 120 ]; do printf "line-%03d\n" $i; i=$((i+1)); done; exec sleep 120'`)
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(f.run(t, "capture-pane", "-p", "-t", f.pane), "line-120") {
		if time.Now().After(deadline) {
			t.Fatal("fixture did not print history")
		}
		time.Sleep(10 * time.Millisecond)
	}
	adapter := f.adapter()
	snapshot, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	panes, err := adapter.sessionPanes(context.Background(), snapshot)
	if err != nil || len(panes) != 1 {
		t.Fatal(panes, err)
	}
	frame, err := adapter.readPaneHistory(context.Background(), panes[0])
	if err != nil {
		t.Fatal(err)
	}
	text, truncated := snapshotText(frame)
	if !truncated || len(strings.Split(text, "\n")) > 30 || !strings.Contains(text, "line-120") || strings.Contains(text, "line-090") {
		t.Fatal("native recent-line bound", text, truncated)
	}
	for _, bad := range []nativePane{func() nativePane { p := panes[0]; p.Owner.Incarnation = "old"; return p }(), func() nativePane { p := panes[0]; p.PID = "1"; return p }()} {
		if got, err := adapter.readPaneHistory(context.Background(), bad); err == nil || got.Screen != "" {
			t.Fatal("stale frame disclosed text", got, err)
		}
	}
	frame.Screen = strings.Repeat("界", 5000)
	frame.CursorY = 0
	text, truncated = snapshotText(frame)
	if !truncated || len(text) > SnapshotTextLimit || !utf8.ValidString(text) {
		t.Fatal("invalid UTF-8/byte truncation", len(text), truncated)
	}
}

func TestSessionCaptureExcludesLinkedWindowsAndRejectsOldCaller(t *testing.T) {
	f := newTmuxFixture(t)
	adapter := f.adapter()
	m := mailFixture(t)
	if _, err := m.Claim(context.Background(), adapter, ""); err != nil {
		t.Fatal(err)
	}
	old, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	window := f.run(t, "new-window", "-d", "-t", "one", "-P", "-F", "#{window_id}", "exec sleep 120")
	f.run(t, "new-session", "-d", "-s", "shared", "exec sleep 120")
	f.run(t, "link-window", "-s", window, "-t", "shared:")
	result := m.CaptureSession(context.Background(), adapter, old.Caller)
	if result.Complete || result.Captured != 1 || result.Skipped != 1 || !strings.Contains(result.Panes[1].Reason, "linked") {
		t.Fatal(result)
	}
	f.run(t, "respawn-pane", "-k", "-t", f.pane, "exec sleep 120")
	stale := m.CaptureSession(context.Background(), adapter, old.Caller)
	if stale.Captured != 0 || stale.Reason == "" {
		t.Fatal("stale caller captured", stale)
	}
}
