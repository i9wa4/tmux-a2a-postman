package ui_node

import (
	"testing"
	"time"
)

func TestNotificationLog(t *testing.T) {
	nl := NewNotificationLog()

	// Add notifications
	now := time.Now()
	nl.AddNotification("ctx1", "worker", now)
	nl.AddNotification("ctx1", "observer", now.Add(time.Second))
	nl.AddNotification("ctx2", "worker", now.Add(2*time.Second))

	// Get notifications for ctx1
	entries := nl.GetNotifications("ctx1")
	if len(entries) != 2 {
		t.Errorf("expected 2 entries for ctx1, got %d", len(entries))
	}

	// Get notifications for ctx2
	entries = nl.GetNotifications("ctx2")
	if len(entries) != 1 {
		t.Errorf("expected 1 entry for ctx2, got %d", len(entries))
	}

	// Get notifications for non-existent context
	entries = nl.GetNotifications("ctx3")
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for ctx3, got %d", len(entries))
	}
}

func TestGetPaneInfo_EmptyPaneID(t *testing.T) {
	info, err := GetPaneInfo("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Status != StatusUnknown {
		t.Errorf("expected StatusUnknown, got %v", info.Status)
	}
}

func TestStatus_Constants(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{StatusVisible, "VISIBLE"},
		{StatusWindowVisible, "WINDOW_VISIBLE"},
		{StatusNotVisible, "NOT_VISIBLE"},
		{StatusUnknown, "UNKNOWN"},
		{StatusInactive, "INACTIVE"},
	}

	for _, tt := range tests {
		if string(tt.status) != tt.want {
			t.Errorf("status %v: got %q, want %q", tt.status, string(tt.status), tt.want)
		}
	}
}
