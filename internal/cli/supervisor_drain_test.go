package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/memory"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
)

func TestRunSupervisorDrain(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-test"
	sessionName := "tmux-session"

	t.Setenv("POSTMAN_HOME", baseDir)
	t.Setenv("TMUX_PANE", "%77")
	installFakeTmux(t, sessionName)

	store, err := memory.NewStore(baseDir, contextID)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	pending, err := store.Append(memory.Record{
		SourcePhase:    2,
		SituationClass: "escalation",
		Context:        "pending record",
	})
	if err != nil {
		t.Fatalf("Append pending: %v", err)
	}
	validated, err := store.Append(memory.Record{
		SourcePhase:    2,
		SituationClass: "escalation",
		Context:        "validated record",
	})
	if err != nil {
		t.Fatalf("Append validated: %v", err)
	}
	if err := store.UpdateOutcome(validated.Seq, memory.OutcomeValidated, "", ""); err != nil {
		t.Fatalf("UpdateOutcome: %v", err)
	}

	deadLetterDir := filepath.Join(baseDir, contextID, "supervisor-memory", "dead-letter")
	postDir := filepath.Join(baseDir, contextID, sessionName, "post")
	archiveDir := filepath.Join(deadLetterDir, "archive")
	if err := os.MkdirAll(deadLetterDir, 0o700); err != nil {
		t.Fatalf("MkdirAll dead-letter: %v", err)
	}
	if err := os.MkdirAll(postDir, 0o700); err != nil {
		t.Fatalf("MkdirAll post: %v", err)
	}

	writeDrainFixture(t, deadLetterDir, "eligible-dl-session-offline.md")
	writeDrainFixture(t, deadLetterDir, "ineligible-dl-routing-denied.md")
	writeDrainFixture(t, deadLetterDir, "unknown-dl-unexpected.md")
	writeDrainFixture(t, deadLetterDir, "plain-message.md")
	writeDrainFixture(t, deadLetterDir, "failure-dl-channel-unbound.md")

	blockedDst := filepath.Join(postDir, message.StripDeadLetterSuffix("failure-dl-channel-unbound.md"))
	if err := os.MkdirAll(blockedDst, 0o700); err != nil {
		t.Fatalf("MkdirAll blocked redelivery dst: %v", err)
	}

	if err := RunSupervisorDrain([]string{"--context-id", contextID}); err != nil {
		t.Fatalf("RunSupervisorDrain first pass: %v", err)
	}

	assertFileExists(t, filepath.Join(postDir, "eligible.md"))
	assertFileExists(t, filepath.Join(archiveDir, "quarantine-ineligible-dl-routing-denied.md"))
	assertFileExists(t, filepath.Join(archiveDir, "passthrough-unknown-dl-unexpected.md"))
	assertFileExists(t, filepath.Join(archiveDir, "passthrough-plain-message.md"))
	assertFileExists(t, filepath.Join(archiveDir, "redelivery-failed-failure-dl-channel-unbound.md"))

	records, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll after first pass: %v", err)
	}
	for _, record := range records {
		switch record.Seq {
		case pending.Seq:
			if record.FailureMode != "phase3_rollback" {
				t.Fatalf("pending record failure_mode = %q, want phase3_rollback", record.FailureMode)
			}
		case validated.Seq:
			if record.FailureMode != "" {
				t.Fatalf("validated record failure_mode = %q, want empty", record.FailureMode)
			}
		}
	}

	summaryPath := filepath.Join(deadLetterDir, "drain-summary.txt")
	firstSummary := readFile(t, summaryPath)
	if !strings.Contains(firstSummary, "redelivered=1") {
		t.Fatalf("first summary missing redelivered count: %q", firstSummary)
	}
	if !strings.Contains(firstSummary, "archived_redelivery_failed=1") {
		t.Fatalf("first summary missing redelivery_failed count: %q", firstSummary)
	}
	if !strings.Contains(firstSummary, "archived_quarantined=1") {
		t.Fatalf("first summary missing quarantined count: %q", firstSummary)
	}
	if !strings.Contains(firstSummary, "archived_passthrough=2") {
		t.Fatalf("first summary missing passthrough count: %q", firstSummary)
	}
	if !strings.Contains(firstSummary, "status=partial") {
		t.Fatalf("first summary missing partial status: %q", firstSummary)
	}

	writeDrainFixture(t, deadLetterDir, "second-dl-session_offline.md")

	if err := RunSupervisorDrain([]string{"--context-id", contextID}); err != nil {
		t.Fatalf("RunSupervisorDrain second pass: %v", err)
	}

	assertFileExists(t, filepath.Join(postDir, "second.md"))

	summaryLines := strings.Split(strings.TrimSpace(readFile(t, summaryPath)), "\n")
	if len(summaryLines) != 2 {
		t.Fatalf("summary line count = %d, want 2; summary=%q", len(summaryLines), strings.Join(summaryLines, "\n"))
	}
	if !strings.Contains(summaryLines[1], "redelivered=1") {
		t.Fatalf("second summary missing redelivered count: %q", summaryLines[1])
	}
	if strings.Contains(summaryLines[1], "status=partial") {
		t.Fatalf("second summary unexpectedly marked partial: %q", summaryLines[1])
	}
}

func TestExtractDrainReason(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{
			name:     "markdown dead-letter suffix",
			filename: "msg-dl-session-offline.md",
			want:     "session-offline",
		},
		{
			name:     "json dead-letter suffix",
			filename: "msg-dl-routing_denied.json",
			want:     "routing_denied",
		},
		{
			name:     "last suffix wins",
			filename: "msg-dl-routing-denied-dl-channel_unbound.md",
			want:     "channel_unbound",
		},
		{
			name:     "missing suffix",
			filename: "msg.md",
			want:     "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractDrainReason(tc.filename); got != tc.want {
				t.Fatalf("extractDrainReason(%q) = %q, want %q", tc.filename, got, tc.want)
			}
		})
	}
}

func installFakeTmux(t *testing.T, sessionName string) {
	t.Helper()

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  *\"#{session_name}\"*) printf '%s\\n' \"" + sessionName + "\" ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile fake tmux: %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func writeDrainFixture(t *testing.T, dir, name string) {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(name+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile %s: %v", name, err)
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	return string(data)
}
