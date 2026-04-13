package projection

import (
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

func TestProjectTimeline_DefaultViewRedactsSensitiveFields(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 6, 0, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	if _, err := writer.AppendEvent(
		"operator_demo_event",
		journal.VisibilityOperatorVisible,
		map[string]string{
			"safe":      "visible",
			"prompt":    "secret prompt",
			"api_token": "shh",
			"content":   "message body",
		},
		now.Add(time.Second),
	); err != nil {
		t.Fatalf("AppendEvent(operator): %v", err)
	}
	if _, err := writer.AppendEvent(
		"control_demo_event",
		journal.VisibilityControlPlaneOnly,
		map[string]string{
			"safe": "hidden",
		},
		now.Add(2*time.Second),
	); err != nil {
		t.Fatalf("AppendEvent(control): %v", err)
	}

	got, ok, err := ProjectTimeline(sessionDir, TimelineOptions{})
	if err != nil {
		t.Fatalf("ProjectTimeline() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectTimeline() ok = false, want true")
	}
	if len(got.Entries) != 1 {
		t.Fatalf("len(ProjectTimeline().Entries) = %d, want 1", len(got.Entries))
	}

	entry := got.Entries[0]
	if entry.Type != "operator_demo_event" {
		t.Fatalf("entry.Type = %q, want operator_demo_event", entry.Type)
	}
	if entry.Visibility != journal.VisibilityOperatorVisible {
		t.Fatalf("entry.Visibility = %q, want %q", entry.Visibility, journal.VisibilityOperatorVisible)
	}
	if gotPayload := entry.Payload["safe"]; gotPayload != "visible" {
		t.Fatalf("safe payload = %#v, want %q", gotPayload, "visible")
	}
	for _, key := range []string{"prompt", "api_token", "content"} {
		if gotPayload := entry.Payload[key]; gotPayload != TimelineRedactedValue {
			t.Fatalf("payload[%q] = %#v, want %q", key, gotPayload, TimelineRedactedValue)
		}
	}
}
