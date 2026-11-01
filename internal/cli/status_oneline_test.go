package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

func glyphNode(unread, inbound, outbound int, now time.Time, age time.Duration) workspace.NodeStatus {
	captured, changed := now.Format(time.RFC3339Nano), now.Add(-age).Format(time.RFC3339Nano)
	return workspace.NodeStatus{Node: "worker", UnreadCount: &unread, InboundCount: &inbound, OutboundCount: &outbound, LastCaptureAt: &captured, LastScreenChangeAt: &changed}
}

func TestStatusGlyphAllShapesAndAgeBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	shapes := []struct {
		unread, inbound, outbound int
		glyphs                    []string
	}{
		{0, 0, 0, []string{"🟢", "🟡", "🟠", "🔴", "⚫"}},
		{1, 1, 1, []string{"🟩", "🟨", "🟧", "🟥", "⬛"}},
		{0, 0, 1, []string{"💚", "💛", "🧡", "❤️", "🖤"}},
	}
	ages := []time.Duration{0, time.Minute, 5 * time.Minute, 15 * time.Minute}
	for _, shape := range shapes {
		for i, age := range ages {
			node := glyphNode(shape.unread, shape.inbound, shape.outbound, now, age)
			if got, why := statusGlyph(node, now); got != shape.glyphs[i] || why != "" {
				t.Fatal(age, got, why)
			}
		}
		node := glyphNode(shape.unread, shape.inbound, shape.outbound, now, 0)
		node.LastCaptureAt = nil
		if got, why := statusGlyph(node, now); got != shape.glyphs[4] || why == "" {
			t.Fatal(got, why)
		}
	}
	for _, values := range [][3]int{{1, 0, 0}, {0, 1, 0}, {1, 0, 2}, {0, 1, 2}} {
		if got, _ := statusGlyph(glyphNode(values[0], values[1], values[2], now, 0), now); got != "🟩" {
			t.Fatal("inbound/unread must win", values, got)
		}
	}
	for _, boundary := range []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute} {
		before, _ := statusGlyph(glyphNode(0, 0, 0, now, boundary-time.Nanosecond), now)
		after, _ := statusGlyph(glyphNode(0, 0, 0, now, boundary), now)
		if before == after {
			t.Fatal("age boundary did not change color", boundary)
		}
	}
}

func TestStatusGlyphFreshObservationDiffersFromOldObservation(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	node := glyphNode(1, 0, 0, now, 20*time.Minute)
	if got, _ := statusGlyph(node, now); got != "🟥" {
		t.Fatal(got)
	}
	fresh := now.Add(-2*time.Minute + time.Nanosecond).Format(time.RFC3339Nano)
	node.LastCaptureAt = &fresh
	if got, _ := statusGlyph(node, now); got != "🟥" {
		t.Fatal("fresh capture with old change must remain red", got)
	}
	stale := now.Add(-2 * time.Minute).Format(time.RFC3339Nano)
	node.LastCaptureAt = &stale
	if got, why := statusGlyph(node, now); got != "⬛" || why == "" {
		t.Fatal("old capture must be unknown", got, why)
	}
}

func TestStatusGlyphUnknownsDoNotBecomeHealthy(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for _, field := range []string{"unread", "inbound", "outbound"} {
		node := glyphNode(0, 0, 0, now, 0)
		switch field {
		case "unread":
			node.UnreadCount = nil
		case "inbound":
			node.InboundCount = nil
		case "outbound":
			node.OutboundCount = nil
		}
		if got, _ := statusGlyph(node, now); got != "?" {
			t.Fatal(field, got)
		}
	}
	negative := -1
	node := glyphNode(0, 0, 0, now, 0)
	node.InboundCount = &negative
	if got, _ := statusGlyph(node, now); got != "?" {
		t.Fatal(got)
	}
	for _, scenario := range []string{"baseline", "bad-capture", "future-capture", "bad-change", "future-change", "change-after-capture"} {
		node := glyphNode(0, 0, 1, now, time.Minute)
		bad, future := "malformed", now.Add(time.Second).Format(time.RFC3339Nano)
		switch scenario {
		case "baseline":
			node.LastScreenChangeAt = nil
		case "bad-capture":
			node.LastCaptureAt = &bad
		case "future-capture":
			node.LastCaptureAt = &future
		case "bad-change":
			node.LastScreenChangeAt = &bad
		case "future-change":
			node.LastScreenChangeAt = &future
		case "change-after-capture":
			capture := now.Add(-10 * time.Second).Format(time.RFC3339Nano)
			change := now.Add(-time.Second).Format(time.RFC3339Nano)
			node.LastCaptureAt = &capture
			node.LastScreenChangeAt = &change
		}
		if got, why := statusGlyph(node, now); got != "🖤" || why == "" {
			t.Fatal(scenario, got, why)
		}
	}
	node = glyphNode(0, 0, 0, now, 0)
	node.DraftCount = &negative
	node.FailedCount = &negative
	node.LastPopAt = nil
	if got, _ := statusGlyph(node, now); got != "🟢" {
		t.Fatal("historical failures/drafts must not alter palette", got)
	}
}

func TestStatusOnelineGroupsNativeSessionsWindowsAndPhysicalPaneOrder(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	unknown := glyphNode(1, 0, 0, now, 0)
	unknown.LastCaptureAt = nil
	nodes := []workspace.OperatorNode{
		{Session: 1, Window: 0, Top: 10, Left: 0, Pane: "%8", Status: glyphNode(1, 0, 0, now, 15*time.Minute)},
		{Session: 0, Window: 1, Top: 0, Left: 0, Pane: "%7", Status: glyphNode(0, 0, 0, now, 0)},
		{Session: 0, Window: 0, Top: 0, Left: 80, Pane: "%6", Status: glyphNode(0, 0, 1, now, time.Minute)},
		{Session: 1, Window: 0, Top: 0, Left: 0, Pane: "%5", Status: unknown},
		{Session: 0, Window: 0, Top: 0, Left: 0, Pane: "%4", Status: glyphNode(1, 0, 0, now, 0)},
	}
	got, diagnostics := renderStatusOneline(nodes, now)
	if got != "[0]🟩💛:🟢 [1]⬛🟥" || len(diagnostics) != 1 {
		t.Fatal(got, diagnostics)
	}
	if strings.ContainsAny(got, "\r\n") || nodes[0].Session != 1 {
		t.Fatal("renderer mutated snapshot or emitted extra lines")
	}
}
