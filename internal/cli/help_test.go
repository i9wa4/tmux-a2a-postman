package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunHelp_DefaultOverview(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := runHelp(&stdout, &stderr, nil); err != nil {
		t.Fatalf("runHelp: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "tmux-a2a-postman — A2A message routing daemon for tmux panes") {
		t.Fatalf("stdout missing overview header: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "tmux-a2a-postman send --to <node> --body \"text\"") {
		t.Fatalf("stdout missing send quick-start line: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Lifecycle and recovery:") {
		t.Fatalf("stdout missing lifecycle split: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Older command names: send-message -> send; get-session-health -> get-health; get-session-status-oneline -> get-health-oneline") {
		t.Fatalf("stdout missing migration map: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Print JSON Schema for the public config surface or a supported command") {
		t.Fatalf("stdout missing neutral schema description: %q", stdout.String())
	}
}

func TestRunHelp_CommandsShowsOperatorAndLifecycleSections(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := runHelp(&stdout, &stderr, []string{"commands"}); err != nil {
		t.Fatalf("runHelp: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Default operator surface") {
		t.Fatalf("stdout missing default operator section: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "get-health-oneline") {
		t.Fatalf("stdout missing get-health-oneline command: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Lifecycle and recovery") {
		t.Fatalf("stdout missing lifecycle section: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "get-context-id") {
		t.Fatalf("stdout missing get-context-id command: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Migration from older names") {
		t.Fatalf("stdout missing migration section: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Shape: [0](window0,window1,)🔷🔵:🟢 [1]🔴") {
		t.Fatalf("stdout missing emoji oneline shape: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `Output JSON: {"status":"[0](window0,)🟣 [1]🟢"}`) {
		t.Fatalf("stdout missing emoji oneline json example: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Print JSON Schema for the curated public config surface or supported command surfaces.") {
		t.Fatalf("stdout missing neutral schema description: %q", stdout.String())
	}
}

func TestRunHelp_ConfigShowsUnifiedModelAndPublicKnobs(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := runHelp(&stdout, &stderr, []string{"config"}); err != nil {
		t.Fatalf("runHelp: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"Unified state + notification model:",
		"Core visible states: ready, pending, user_input, composing, spinning, stalled",
		"ui_node                          Human-facing inbox target for alerts and user_input waits",
		"reminder_interval_messages       Reminder cadence after archived reads",
		"inbox_unread_threshold           Unread-summary threshold for ui_node alerts",
		"[node].idle_timeout_seconds      Per-node inactivity alert threshold",
		"[node].dropped_ball_timeout_seconds  Shared late-reply timeout for unreplied-message alerts and dropped-ball detection",
		"node_spinning_seconds            Optional early escalation from composing to spinning",
		"[heartbeat].enabled             Optional keepalive automation (advanced)",
		"Advanced/internal shaping knobs live in docs/design/notification.md and docs/guides/alert-config.md.",
		"get-health, get-health-oneline, and the default TUI read the same canonical health contract.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout missing %q: %q", want, got)
		}
	}
	for _, stale := range []string{
		"scan_interval      float64",
		"enter_delay        float64",
		"tmux_timeout       float64",
		"startup_delay      float64",
		"reminder_interval  float64",
	} {
		if strings.Contains(got, stale) {
			t.Fatalf("stdout still contains stale config field %q: %q", stale, got)
		}
	}
}

func TestRunHelp_UnknownTopicWritesGuidance(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := runHelp(&stdout, &stderr, []string{"mystery"})
	if err == nil {
		t.Fatal("runHelp returned nil error for unknown topic")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), `unknown help topic: "mystery"`) {
		t.Fatalf("stderr missing unknown-topic line: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Available topics:") {
		t.Fatalf("stderr missing available-topics section: %q", stderr.String())
	}
}
