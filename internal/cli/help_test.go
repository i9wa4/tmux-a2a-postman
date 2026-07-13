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
	if !strings.Contains(stdout.String(), "tmux-a2a-postman send-heredoc --to <node> <<'POSTMAN_BODY'") {
		t.Fatalf("stdout missing send-heredoc quick-start line: %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "--message-file") {
		t.Fatalf("stdout still contains message-file guidance: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Use an explicit command. Bare `tmux-a2a-postman` prints usage; it does not start the daemon.") {
		t.Fatalf("stdout missing explicit-command guidance: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Lifecycle and recovery:") {
		t.Fatalf("stdout missing lifecycle split: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "get-status                 Print canonical session status JSON") {
		t.Fatalf("stdout missing get-status overview line: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "get-status-oneline         Print compact all-session status") {
		t.Fatalf("stdout missing get-status-oneline overview line: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "version                    Print the build version JSON") {
		t.Fatalf("stdout missing version overview line: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "inspect-message            Inspect persisted message content by id") {
		t.Fatalf("stdout missing inspect-message overview line: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "inspect-daemon-submit      Inspect daemon-submit timeout state by id") {
		t.Fatalf("stdout missing inspect-daemon-submit overview line: %q", stdout.String())
	}
	for _, hidden := range []string{"status", "read", "todo", "timeline", "replay", "schema", "bind", "supervisor-drain"} {
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
	if !strings.Contains(stdout.String(), "get-status") {
		t.Fatalf("stdout missing get-status command: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "get-status-oneline") {
		t.Fatalf("stdout missing get-status-oneline command: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "version") {
		t.Fatalf("stdout missing version command: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "inspect-message") {
		t.Fatalf("stdout missing inspect-message command: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "inspect-daemon-submit") {
		t.Fatalf("stdout missing inspect-daemon-submit command: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Lifecycle and recovery") {
		t.Fatalf("stdout missing lifecycle section: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Shape: [0]🔷🟡:🟢 [1]🔴") {
		t.Fatalf("stdout missing status shape: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Window groups are colon-separated emoji runs with no literal window labels.") {
		t.Fatalf("stdout missing emoji group note: %q", stdout.String())
	}
	for _, hidden := range []string{"\nstatus\n", "get-context-id", "\nread\n", "\ntodo\n", "\ntimeline\n", "\nreplay\n", "\nschema", "\nbind\n", "\nsupervisor-drain\n", "--context-id"} {
		if strings.Contains(stdout.String(), hidden) {
			t.Fatalf("stdout exposes hidden surface %q in command help: %q", hidden, stdout.String())
		}
	}
}

func TestRunHelp_HelpTopicListsRegisteredTopics(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := runHelp(&stdout, &stderr, []string{"help"}); err != nil {
		t.Fatalf("runHelp: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "\n  inspect-message\n") {
		t.Fatalf("stdout missing inspect-message help topic: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "\n  inspect-daemon-submit\n") {
		t.Fatalf("stdout missing inspect-daemon-submit help topic: %q", stdout.String())
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
		"Core visible states: initial, ready, waiting, pending, stale",
		"Quick reading guide:",
		"visible_state in get-status JSON answers what the node looks like now",
		"initial means no positive live evidence yet; ready requires live evidence",
		"pane hints answer that delivery reached a recipient inbox",
		"Core config:",
		"edges                            Bidirectional routes between nodes",
		"ui_node                          Optional target filter for startup auto-PING; prefer Mermaid class <node> ui_node",
		"auto_enable_new_sessions         Auto-enable sessions with configured node panes (default: true)",
		"message_footer                   Header guidance before the sender body separator",
		"draft_template                   Structured envelope for stored send-heredoc Markdown",
		"daemon_message_template          Structured envelope for daemon-originated PING mail",
		"audit_review_probability_floor   Nonzero minimum audit draw probability for accepted fills",
		"audit_target                     Optional sampled audit target; defaults to a valid command_approver_node when available",
		"skill_path                       postman.md skill catalogs; use inject: ping, inject: compaction_ping, or list syntax for PINGs",
		"auto_ping_delay_seconds          Delay before first auto-PING for newly appeared/replacement nodes",
		"notification_template            Pane hint rendered when mail arrives",
		"min_delivery_gap_seconds         Same-route delivery gap for duplicate control",
		"retention_period_days            Inactive runtime cleanup window (default: 30; 0 = disabled)",
		"pane_capture_tail_lines          Recent-line compaction scan; Claude/Codex first/change captures may fall back to full history",
		"Skill catalogs:",
		"default skill_path relative paths resolve from the declaring postman.md directory",
		"inject: ping, inject: compaction_ping, and lists containing them must use ~/... or absolute paths",
		"skill_path runtime selectors are unsupported; list explicit path entries instead",
		"duplicate rendered skill names are deduped; later entries win",
		"get-status, get-status-oneline, and the default TUI read the same canonical status contract.",
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
		"waiting/",
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
