package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunSchema_SendOutput(t *testing.T) {
	var stdout bytes.Buffer

	if err := runSchema(&stdout, []string{"send"}); err != nil {
		t.Fatalf("runSchema: %v", err)
	}
	if !strings.Contains(stdout.String(), `"title": "send options"`) {
		t.Fatalf("stdout missing schema title: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"to"`) {
		t.Fatalf("stdout missing to property: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"body"`) {
		t.Fatalf("stdout missing body property: %q", stdout.String())
	}
}

func TestRunSchema_ConfigShowsUnifiedModelPublicKnobs(t *testing.T) {
	var stdout bytes.Buffer

	if err := runSchema(&stdout, nil); err != nil {
		t.Fatalf("runSchema: %v", err)
	}
	got := stdout.String()
	if !strings.Contains(got, `"title": "postman.toml public config surface"`) {
		t.Fatalf("stdout missing config title: %q", got)
	}
	for _, want := range []string{
		`"ui_node"`,
		`"reminder_interval_messages"`,
		`"inbox_unread_threshold"`,
		`"journal_health_cutover_enabled"`,
		`"journal_compatibility_cutover_enabled"`,
		`"retention_period_days"`,
		`"read_context_mode"`,
		`"read_context_pieces"`,
		`"read_context_heading"`,
		`"[node].idle_timeout_seconds"`,
		`"[node].dropped_ball_timeout_seconds"`,
		`"node_spinning_seconds"`,
		`"[heartbeat].enabled"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout missing %q: %q", want, got)
		}
	}
	for _, stale := range []string{
		`"scan_interval_seconds"`,
		`"enter_delay_seconds"`,
		`"tmux_timeout_seconds"`,
		`"startup_delay_seconds"`,
		`"[heartbeat].interval_seconds"`,
		`"[heartbeat].llm_node"`,
		`"[heartbeat].prompt"`,
	} {
		if strings.Contains(got, stale) {
			t.Fatalf("stdout still contains demoted internal knob %q: %q", stale, got)
		}
	}
}

func TestRunSchema_GetHealthOutput(t *testing.T) {
	var stdout bytes.Buffer

	if err := runSchema(&stdout, []string{"get-health"}); err != nil {
		t.Fatalf("runSchema: %v", err)
	}
	if !strings.Contains(stdout.String(), `"title": "get-health output"`) {
		t.Fatalf("stdout missing get-health title: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"visible_state"`) {
		t.Fatalf("stdout missing visible_state property: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"compact"`) {
		t.Fatalf("stdout missing compact property: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"windows"`) {
		t.Fatalf("stdout missing windows property: %q", stdout.String())
	}
}

func TestRunSchema_GetHealthOnelineOptions(t *testing.T) {
	var stdout bytes.Buffer

	if err := runSchema(&stdout, []string{"get-health-oneline"}); err != nil {
		t.Fatalf("runSchema: %v", err)
	}
	if !strings.Contains(stdout.String(), `"title": "get-health-oneline options"`) {
		t.Fatalf("stdout missing get-health-oneline title: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"json"`) {
		t.Fatalf("stdout missing json option: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `{\"status\": \"[0]🟣 [1]🟢\"}`) {
		t.Fatalf("stdout missing emoji json example: %q", stdout.String())
	}
}

func TestRunSchema_TimelineAndReplayOptions(t *testing.T) {
	t.Run("timeline", func(t *testing.T) {
		var stdout bytes.Buffer

		if err := runSchema(&stdout, []string{"timeline"}); err != nil {
			t.Fatalf("runSchema: %v", err)
		}
		got := stdout.String()
		if !strings.Contains(got, `"title": "timeline options"`) {
			t.Fatalf("stdout missing timeline title: %q", got)
		}
		if !strings.Contains(got, `"limit"`) {
			t.Fatalf("stdout missing limit option: %q", got)
		}
		if !strings.Contains(got, `"include-control-plane"`) {
			t.Fatalf("stdout missing include-control-plane option: %q", got)
		}
	})

	t.Run("replay", func(t *testing.T) {
		var stdout bytes.Buffer

		if err := runSchema(&stdout, []string{"replay"}); err != nil {
			t.Fatalf("runSchema: %v", err)
		}
		got := stdout.String()
		if !strings.Contains(got, `"title": "replay options"`) {
			t.Fatalf("stdout missing replay title: %q", got)
		}
		if !strings.Contains(got, `"surface"`) {
			t.Fatalf("stdout missing surface option: %q", got)
		}
	})
}

func TestRunSchema_TodoOptions(t *testing.T) {
	var stdout bytes.Buffer

	if err := runSchema(&stdout, []string{"todo"}); err != nil {
		t.Fatalf("runSchema: %v", err)
	}
	got := stdout.String()
	if !strings.Contains(got, `"title": "todo options"`) {
		t.Fatalf("stdout missing todo title: %q", got)
	}
	for _, want := range []string{`"json"`, `"node"`, `"body"`, `"file"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout missing %q: %q", want, got)
		}
	}
}

func TestRunSchema_LegacyCommandNamesAreRejected(t *testing.T) {
	cases := []string{"send-message", "get-session-health", "get-session-status-oneline"}
	for _, command := range cases {
		t.Run(command, func(t *testing.T) {
			var stdout bytes.Buffer

			err := runSchema(&stdout, []string{command})
			if err == nil {
				t.Fatal("runSchema returned nil error for legacy command")
			}
			if err.Error() != `unknown command "`+command+`"; run 'tmux-a2a-postman schema' for config schema` {
				t.Fatalf("error = %q", err.Error())
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}
