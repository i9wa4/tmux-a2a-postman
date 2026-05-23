package uinode

import (
	"reflect"
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

func TestParseListPanesInfo_SkipsMalformedRows(t *testing.T) {
	checkedAt := time.Date(2026, 5, 21, 7, 0, 0, 0, time.UTC)

	got, inactive := parseListPanesInfo([]byte("%11:1:123\nmalformed\n%12:0:456\n%13:bad:not-int\n"), checkedAt)

	if _, ok := got["malformed"]; ok {
		t.Fatal("malformed row should be skipped")
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	if got["%11"].Status != StatusVisible {
		t.Fatalf("%%11 status = %s, want %s", got["%11"].Status, StatusVisible)
	}
	if got["%12"].Status != StatusUnknown {
		t.Fatalf("%%12 status = %s, want %s before window lookup", got["%12"].Status, StatusUnknown)
	}
	if !got["%13"].LastChecked.Equal(checkedAt) {
		t.Fatalf("%%13 LastChecked = %s, want %s", got["%13"].LastChecked, checkedAt)
	}
	if !reflect.DeepEqual(inactive, []string{"%12", "%13"}) {
		t.Fatalf("inactive = %#v, want %#v", inactive, []string{"%12", "%13"})
	}
}

func TestGetPaneInfoWithRunner_UnknownPane(t *testing.T) {
	checkedAt := time.Date(2026, 5, 21, 7, 1, 0, 0, time.UTC)
	run := func(args ...string) ([]byte, error) {
		if args[0] != "list-panes" {
			t.Fatalf("unexpected command: %#v", args)
		}
		return []byte("%11:1:123\n"), nil
	}

	info, err := getPaneInfo("%99", run, func() time.Time { return checkedAt })
	if err != nil {
		t.Fatalf("getPaneInfo: %v", err)
	}
	if info.Status != StatusUnknown {
		t.Fatalf("status = %s, want %s", info.Status, StatusUnknown)
	}
	if !info.LastChecked.Equal(checkedAt) {
		t.Fatalf("LastChecked = %s, want %s", info.LastChecked, checkedAt)
	}
}

func TestGetPaneInfoWithRunner_ActivePane(t *testing.T) {
	checkedAt := time.Date(2026, 5, 21, 7, 2, 0, 0, time.UTC)
	var calls [][]string
	run := func(args ...string) ([]byte, error) {
		calls = append(calls, args)
		if args[0] != "list-panes" {
			t.Fatalf("unexpected command: %#v", args)
		}
		return []byte("%11:1:123\n"), nil
	}

	info, err := getPaneInfo("%11", run, func() time.Time { return checkedAt })
	if err != nil {
		t.Fatalf("getPaneInfo: %v", err)
	}
	if info.Status != StatusVisible || !info.PaneActive {
		t.Fatalf("info = %#v, want active visible pane", info)
	}
	if len(calls) != 1 {
		t.Fatalf("command calls = %#v, want only list-panes", calls)
	}
}

func TestGetPaneInfoWithRunner_InactiveVisibleWindow(t *testing.T) {
	info, err := getPaneInfo("%11", paneInfoRunner("1\n"), time.Now)
	if err != nil {
		t.Fatalf("getPaneInfo: %v", err)
	}
	if info.Status != StatusWindowVisible || !info.WindowActive {
		t.Fatalf("info = %#v, want inactive pane in visible window", info)
	}
}

func TestGetPaneInfoWithRunner_InactiveHiddenWindow(t *testing.T) {
	info, err := getPaneInfo("%11", paneInfoRunner("0\n"), time.Now)
	if err != nil {
		t.Fatalf("getPaneInfo: %v", err)
	}
	if info.Status != StatusNotVisible || info.WindowActive {
		t.Fatalf("info = %#v, want inactive pane in hidden window", info)
	}
}

func paneInfoRunner(windowActiveOutput string) tmuxOutputFunc {
	return func(args ...string) ([]byte, error) {
		switch args[0] {
		case "list-panes":
			return []byte("%11:0:123\n"), nil
		case "list-windows":
			return []byte("unused\n"), nil
		case "display-message":
			return []byte(windowActiveOutput), nil
		default:
			return nil, nil
		}
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
