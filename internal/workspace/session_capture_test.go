package workspace

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

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
