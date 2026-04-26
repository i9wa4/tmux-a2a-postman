package projection

import (
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

func TestProjectAutoPingState_FallsBackWhenHistoryIsMissing(t *testing.T) {
	sessionDir := t.TempDir()

	got, ok, err := ProjectAutoPingState(sessionDir)
	if err != nil {
		t.Fatalf("ProjectAutoPingState() error = %v", err)
	}
	if ok {
		t.Fatalf("ProjectAutoPingState() ok = true, want false with %#v", got)
	}
}

func TestProjectAutoPingState_ReplaysCurrentGeneration(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 26, 21, 45, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	if _, err := writer.AppendEvent(AutoPingPendingEventType, journal.VisibilityOperatorVisible, AutoPingEventPayload{
		NodeKey:      "review:worker",
		SessionName:  "review",
		NodeName:     "worker",
		PaneID:       "%12",
		Reason:       "discovered",
		TriggeredAt:  now.Format(time.RFC3339Nano),
		DelaySeconds: 5,
		NotBeforeAt:  now.Add(5 * time.Second).Format(time.RFC3339Nano),
	}, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendEvent(pending worker): %v", err)
	}
	if _, err := writer.AppendEvent(AutoPingPendingEventType, journal.VisibilityOperatorVisible, AutoPingEventPayload{
		NodeKey:      "review:critic",
		SessionName:  "review",
		NodeName:     "critic",
		PaneID:       "%13",
		Reason:       "discovered",
		TriggeredAt:  now.Add(2 * time.Second).Format(time.RFC3339Nano),
		DelaySeconds: 0,
		NotBeforeAt:  now.Add(2 * time.Second).Format(time.RFC3339Nano),
	}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("AppendEvent(pending critic): %v", err)
	}
	if _, err := writer.AppendEvent(AutoPingDeliveredEventType, journal.VisibilityOperatorVisible, AutoPingEventPayload{
		NodeKey:      "review:critic",
		SessionName:  "review",
		NodeName:     "critic",
		PaneID:       "%13",
		Reason:       "discovered",
		TriggeredAt:  now.Add(2 * time.Second).Format(time.RFC3339Nano),
		DelaySeconds: 0,
		NotBeforeAt:  now.Add(2 * time.Second).Format(time.RFC3339Nano),
		DeliveredAt:  now.Add(3 * time.Second).Format(time.RFC3339Nano),
	}, now.Add(3*time.Second)); err != nil {
		t.Fatalf("AppendEvent(delivered critic): %v", err)
	}

	got, ok, err := ProjectAutoPingState(sessionDir)
	if err != nil {
		t.Fatalf("ProjectAutoPingState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectAutoPingState() ok = false, want true")
	}

	worker := got.Nodes["review:worker"]
	if !worker.Pending {
		t.Fatal("worker pending = false, want true")
	}
	if worker.PaneID != "%12" {
		t.Fatalf("worker PaneID = %q, want %q", worker.PaneID, "%12")
	}
	if worker.NotBeforeAt != now.Add(5*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("worker NotBeforeAt = %q, want %q", worker.NotBeforeAt, now.Add(5*time.Second).Format(time.RFC3339Nano))
	}

	critic := got.Nodes["review:critic"]
	if critic.Pending {
		t.Fatal("critic pending = true, want false after delivered event")
	}
	if critic.DeliveredAt != now.Add(3*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("critic DeliveredAt = %q, want %q", critic.DeliveredAt, now.Add(3*time.Second).Format(time.RFC3339Nano))
	}
}

func TestProjectAutoPingState_ReplaysAcrossLeaseResume(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 26, 21, 50, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter(first) error = %v", err)
	}
	if _, err := writer.AppendEvent(AutoPingPendingEventType, journal.VisibilityOperatorVisible, AutoPingEventPayload{
		NodeKey:      "review:worker",
		SessionName:  "review",
		NodeName:     "worker",
		PaneID:       "%20",
		Reason:       "discovered",
		TriggeredAt:  now.Format(time.RFC3339Nano),
		DelaySeconds: 3,
		NotBeforeAt:  now.Add(3 * time.Second).Format(time.RFC3339Nano),
	}, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendEvent(first pending): %v", err)
	}

	resumedWriter, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 202, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("OpenShadowWriter(second) error = %v", err)
	}
	if _, err := resumedWriter.AppendEvent(AutoPingPendingEventType, journal.VisibilityOperatorVisible, AutoPingEventPayload{
		NodeKey:      "review:worker",
		SessionName:  "review",
		NodeName:     "worker",
		PaneID:       "%21",
		Reason:       "pane_restart",
		TriggeredAt:  now.Format(time.RFC3339Nano),
		DelaySeconds: 3,
		NotBeforeAt:  now.Add(3 * time.Second).Format(time.RFC3339Nano),
	}, now.Add(3*time.Second)); err != nil {
		t.Fatalf("AppendEvent(second pending): %v", err)
	}
	if _, err := resumedWriter.AppendEvent(AutoPingDeliveredEventType, journal.VisibilityOperatorVisible, AutoPingEventPayload{
		NodeKey:      "review:worker",
		SessionName:  "review",
		NodeName:     "worker",
		PaneID:       "%21",
		Reason:       "pane_restart",
		TriggeredAt:  now.Format(time.RFC3339Nano),
		DelaySeconds: 3,
		NotBeforeAt:  now.Add(3 * time.Second).Format(time.RFC3339Nano),
		DeliveredAt:  now.Add(4 * time.Second).Format(time.RFC3339Nano),
	}, now.Add(4*time.Second)); err != nil {
		t.Fatalf("AppendEvent(delivered): %v", err)
	}

	got, ok, err := ProjectAutoPingState(sessionDir)
	if err != nil {
		t.Fatalf("ProjectAutoPingState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectAutoPingState() ok = false, want true")
	}

	worker := got.Nodes["review:worker"]
	if worker.Pending {
		t.Fatal("worker pending = true, want false after replayed delivery")
	}
	if worker.PaneID != "%21" {
		t.Fatalf("worker PaneID = %q, want %q", worker.PaneID, "%21")
	}
}
