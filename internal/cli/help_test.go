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
	if !strings.Contains(stdout.String(), "Use an explicit command. Bare `tmux-a2a-postman` prints usage; it does not start the daemon.") {
		t.Fatalf("stdout missing explicit-command guidance: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Lifecycle and recovery:") {
		t.Fatalf("stdout missing lifecycle split: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "status                     Show current runtime status (--json for canonical payload)") {
		t.Fatalf("stdout missing status overview line: %q", stdout.String())
	}
	for _, hidden := range []string{"get-health", "get-health-oneline", "read", "todo", "timeline", "replay", "schema", "bind", "supervisor-drain"} {
		if strings.Contains(stdout.String(), "  "+hidden) || strings.Contains(stdout.String(), "\n"+hidden+"\n") {
			t.Fatalf("stdout exposes hidden command %q in the default overview: %q", hidden, stdout.String())
		}
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
	if !strings.Contains(stdout.String(), "Use an explicit command. Bare `tmux-a2a-postman` prints usage; it does not start the daemon.") {
		t.Fatalf("stdout missing explicit-command guidance: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "status") {
		t.Fatalf("stdout missing status command: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Lifecycle and recovery") {
		t.Fatalf("stdout missing lifecycle section: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Shape: [0]🔷🔵:🟢 [1]🔴") {
		t.Fatalf("stdout missing status shape: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Window groups are colon-separated emoji runs with no literal window labels.") {
		t.Fatalf("stdout missing emoji group note: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Output canonical all-session status JSON") {
		t.Fatalf("stdout missing status json description: %q", stdout.String())
	}
	for _, hidden := range []string{"get-context-id", "get-health", "get-health-oneline", "\nread\n", "\ntodo\n", "\ntimeline\n", "\nreplay\n", "\nschema", "\nbind\n", "\nsupervisor-drain\n", "--context-id"} {
		if strings.Contains(stdout.String(), hidden) {
			t.Fatalf("stdout exposes hidden surface %q in command help: %q", hidden, stdout.String())
		}
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
		"Runtime state model:",
		"Core visible states: ready, pending, user_input, composing, spinning, stalled",
		"Quick reading guide:",
		"visible_state in status JSON answers what the node looks like now",
		"pane hints answer that delivery reached a recipient inbox",
		"Core config:",
		"edges                            Bidirectional routes between nodes",
		"ui_node                          Human-facing node for daemon-originated mail",
		"message_footer                   Footer appended to stored send mail",
		"notification_template            Pane hint rendered when mail arrives",
		"min_delivery_gap_seconds         Same-route delivery gap for duplicate control",
		"retention_period_days            Inactive runtime cleanup window",
		"status, status --json, and the default TUI read the same canonical health contract.",
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
		"journal_health_cutover_enabled",
		"read_context_mode",
		"dropped-ball",
		"[heartbeat].enabled",
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
