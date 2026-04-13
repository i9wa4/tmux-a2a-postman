package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

type projectionJournalStep struct {
	kind string
	to   string
}

type sessionHealthProjectionFixture struct {
	baseDir     string
	contextID   string
	sessionName string
	configPath  string
	cfg         *config.Config
}

func TestGetHealthProjectionParity(t *testing.T) {
	tests := []struct {
		name         string
		paneStates   map[string]string
		liveInbox    map[string]int
		waitingFiles map[string]string
		journalSteps []projectionJournalStep
	}{
		{
			name:       "healthy",
			paneStates: map[string]string{"worker": "active", "critic": "idle"},
			liveInbox:  map[string]int{},
		},
		{
			name:       "degraded",
			paneStates: map[string]string{"worker": "active", "critic": "active"},
			liveInbox:  map[string]int{"worker": 1},
			journalSteps: []projectionJournalStep{
				{kind: "deliver", to: "worker"},
			},
		},
		{
			name:       "stale",
			paneStates: map[string]string{"worker": "active", "critic": "stale"},
			liveInbox:  map[string]int{},
		},
		{
			name:       "resumed",
			paneStates: map[string]string{"worker": "active", "critic": "active"},
			liveInbox:  map[string]int{"critic": 1},
			journalSteps: []projectionJournalStep{
				{kind: "deliver", to: "worker"},
				{kind: "resume"},
				{kind: "deliver", to: "critic"},
				{kind: "read", to: "worker"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := writeSessionHealthProjectionFixture(t, tc.paneStates, tc.liveInbox, tc.waitingFiles, tc.journalSteps)

			legacy, err := collectSessionHealthLegacy(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
			if err != nil {
				t.Fatalf("collectSessionHealthLegacy() error = %v", err)
			}
			projected, err := collectSessionHealth(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
			if err != nil {
				t.Fatalf("collectSessionHealth() error = %v", err)
			}

			assertSessionHealthParity(t, legacy, projected)
		})
	}
}

func TestGetHealthOnelineProjectionParity(t *testing.T) {
	fixture := writeSessionHealthProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		map[string]int{"critic": 1},
		nil,
		[]projectionJournalStep{
			{kind: "deliver", to: "worker"},
			{kind: "resume"},
			{kind: "deliver", to: "critic"},
			{kind: "read", to: "worker"},
		},
	)

	legacy, _, ok, err := collectAllSessionHealthLegacy(fixture.contextID, "", fixture.configPath)
	if err != nil {
		t.Fatalf("collectAllSessionHealthLegacy() error = %v", err)
	}
	if !ok {
		t.Fatal("collectAllSessionHealthLegacy() ok = false, want true")
	}

	projected, _, ok, err := collectAllSessionHealth(fixture.contextID, "", fixture.configPath)
	if err != nil {
		t.Fatalf("collectAllSessionHealth() error = %v", err)
	}
	if !ok {
		t.Fatal("collectAllSessionHealth() ok = false, want true")
	}

	legacyOneline := formatAllSessionHealthOneline(legacy)
	projectedOneline := formatAllSessionHealthOneline(projected)
	if legacyOneline != projectedOneline {
		t.Fatalf("oneline mismatch:\nlegacy:    %q\nprojected: %q", legacyOneline, projectedOneline)
	}
}

func TestNoActivePostmanUnavailableContract(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunGetSessionHealth([]string{"--session", "review"})
	})
	if err != nil {
		t.Fatalf("RunGetSessionHealth() error = %v", err)
	}

	var payload status.SessionHealth
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	if payload.SessionName != "review" {
		t.Fatalf("SessionName = %q, want %q", payload.SessionName, "review")
	}
	if payload.ContextID != "" || payload.NodeCount != 0 || payload.VisibleState != "" || payload.Compact != "" {
		t.Fatalf("unexpected no-active-postman payload: %#v", payload)
	}
	if len(payload.Nodes) != 0 || len(payload.Windows) != 0 {
		t.Fatalf("unexpected no-active-postman topology: %#v", payload)
	}
}

func TestTUIProjectionParity(t *testing.T) {
	fixture := writeSessionHealthProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		map[string]int{"worker": 1},
		nil,
		[]projectionJournalStep{
			{kind: "deliver", to: "worker"},
		},
	)

	legacy, err := collectSessionHealthLegacy(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectSessionHealthLegacy() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rawEvents := make(chan tui.DaemonEvent, 1)
	tuiEvents := make(chan tui.DaemonEvent, 4)
	go relayDaemonEventsToTUI(ctx, rawEvents, tuiEvents, fixture.baseDir, fixture.contextID, fixture.cfg)

	rawEvents <- tui.DaemonEvent{
		Type:    "status_update",
		Message: "Running",
		Details: map[string]interface{}{
			"sessions": []tui.SessionInfo{{Name: fixture.sessionName, Enabled: true}},
		},
	}

	<-tuiEvents
	healthEvent := <-tuiEvents
	if healthEvent.Type != "session_health_update" {
		t.Fatalf("health event type = %q, want session_health_update", healthEvent.Type)
	}

	got, ok := healthEvent.Details["health"].(status.SessionHealth)
	if !ok {
		t.Fatalf("health payload type = %T, want status.SessionHealth", healthEvent.Details["health"])
	}
	assertSessionHealthParity(t, legacy, got)
}

func TestGetHealthProjectionRebuildsWithoutLiveArtifacts(t *testing.T) {
	fixture := writeSessionHealthProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "idle"},
		map[string]int{"worker": 1},
		map[string]string{
			"critic": "---\nstate: composing\nexpects_reply: true\n---\n",
		},
		nil,
	)

	legacy, err := collectSessionHealthLegacy(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectSessionHealthLegacy() error = %v", err)
	}

	appendSessionHealthSnapshot(t, fixture, legacy)
	removeLiveSessionHealthArtifacts(t, fixture)
	installSessionHealthProjectionBrokenTmux(t)

	projected, err := collectSessionHealth(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectSessionHealth() error = %v", err)
	}

	assertSessionHealthParity(t, legacy, projected)
}

func TestGetHealthOnelineProjectionRebuildsWithoutLiveTopology(t *testing.T) {
	fixture := writeSessionHealthProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		map[string]int{"critic": 1},
		map[string]string{
			"worker": "---\nstate: composing\nexpects_reply: true\n---\n",
		},
		nil,
	)

	legacy, _, ok, err := collectAllSessionHealthLegacy(fixture.contextID, "", fixture.configPath)
	if err != nil {
		t.Fatalf("collectAllSessionHealthLegacy() error = %v", err)
	}
	if !ok {
		t.Fatal("collectAllSessionHealthLegacy() ok = false, want true")
	}

	appendSessionHealthSnapshot(t, fixture, legacy.Sessions[0])
	removeLiveSessionHealthArtifacts(t, fixture)
	installSessionHealthProjectionListSessionsOnlyTmux(t, fixture.sessionName)

	var stdout strings.Builder
	if err := RunGetSessionStatusOneline(&stdout, []string{"--context-id", fixture.contextID, "--config", fixture.configPath}); err != nil {
		t.Fatalf("RunGetSessionStatusOneline() error = %v", err)
	}

	if got, want := strings.TrimSpace(stdout.String()), formatAllSessionHealthOneline(legacy); got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
}

func TestTUIProjectionRebuildsWithoutLiveArtifacts(t *testing.T) {
	fixture := writeSessionHealthProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		map[string]int{"worker": 1},
		map[string]string{
			"critic": "---\nstate: composing\nexpects_reply: true\n---\n",
		},
		nil,
	)

	legacy, err := collectSessionHealthLegacy(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectSessionHealthLegacy() error = %v", err)
	}

	appendSessionHealthSnapshot(t, fixture, legacy)
	removeLiveSessionHealthArtifacts(t, fixture)
	installSessionHealthProjectionBrokenTmux(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rawEvents := make(chan tui.DaemonEvent, 1)
	tuiEvents := make(chan tui.DaemonEvent, 4)
	go relayDaemonEventsToTUI(ctx, rawEvents, tuiEvents, fixture.baseDir, fixture.contextID, fixture.cfg)

	rawEvents <- tui.DaemonEvent{
		Type:    "status_update",
		Message: "Running",
		Details: map[string]interface{}{
			"sessions": []tui.SessionInfo{{Name: fixture.sessionName, Enabled: true}},
		},
	}

	<-tuiEvents
	healthEvent := <-tuiEvents
	if healthEvent.Type != "session_health_update" {
		t.Fatalf("health event type = %q, want session_health_update", healthEvent.Type)
	}

	got, ok := healthEvent.Details["health"].(status.SessionHealth)
	if !ok {
		t.Fatalf("health payload type = %T, want status.SessionHealth", healthEvent.Details["health"])
	}
	assertSessionHealthParity(t, legacy, got)
}

func assertSessionHealthParity(t *testing.T, legacy, projected status.SessionHealth) {
	t.Helper()

	if reflect.DeepEqual(legacy, projected) {
		return
	}

	legacyJSON, legacyErr := json.MarshalIndent(legacy, "", "  ")
	projectedJSON, projectedErr := json.MarshalIndent(projected, "", "  ")
	if legacyErr != nil || projectedErr != nil {
		t.Fatalf("parity mismatch and JSON marshal failed: legacyErr=%v projectedErr=%v", legacyErr, projectedErr)
	}

	t.Fatalf("session health mismatch:\nlegacy:\n%s\nprojected:\n%s", string(legacyJSON), string(projectedJSON))
}

func writeSessionHealthProjectionFixture(t *testing.T, paneStates map[string]string, liveInbox map[string]int, waitingFiles map[string]string, journalSteps []projectionJournalStep) sessionHealthProjectionFixture {
	t.Helper()

	tmpDir := t.TempDir()
	contextID := "20260414-ctx"
	sessionName := "review"
	sessionDir := filepath.Join(tmpDir, contextID, sessionName)

	t.Setenv("POSTMAN_HOME", tmpDir)

	configPath := filepath.Join(tmpDir, "postman.toml")
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"worker -- critic\"]\n\n[worker]\ntemplate = \"worker\"\nrole = \"worker\"\n\n[critic]\ntemplate = \"critic\"\nrole = \"critic\"\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(postman.toml): %v", err)
	}

	for _, dir := range []string{
		filepath.Join(sessionDir, "inbox", "worker"),
		filepath.Join(sessionDir, "inbox", "critic"),
		filepath.Join(sessionDir, "waiting"),
		filepath.Join(sessionDir, "read"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	if err := os.WriteFile(
		filepath.Join(tmpDir, contextID, "pane-activity.json"),
		[]byte(`{
  "%11": {"status":"`+paneStates["worker"]+`","lastChangeAt":"2026-04-14T00:00:00Z"},
  "%12": {"status":"`+paneStates["critic"]+`","lastChangeAt":"2026-04-14T00:00:00Z"}
}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(pane-activity.json): %v", err)
	}

	for nodeName, count := range liveInbox {
		for i := 0; i < count; i++ {
			filename := filepath.Join(sessionDir, "inbox", nodeName, unreadFixtureName(nodeName, i))
			if err := os.WriteFile(filename, []byte("body"), 0o644); err != nil {
				t.Fatalf("WriteFile(%q): %v", filename, err)
			}
		}
	}

	for nodeName, content := range waitingFiles {
		filename := filepath.Join(sessionDir, "waiting", waitingFixtureName(nodeName))
		if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", filename, err)
		}
	}

	installSessionHealthProjectionTmux(t, contextID, sessionName)

	now := time.Date(2026, time.April, 14, 4, 0, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, contextID, sessionName, 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	for i, step := range journalSteps {
		stepTime := now.Add(time.Duration(i+1) * time.Second)
		switch step.kind {
		case "resume":
			writer, err = journal.OpenShadowWriter(sessionDir, contextID, sessionName, 101+i, stepTime)
			if err != nil {
				t.Fatalf("OpenShadowWriter(resume) error = %v", err)
			}
		case "deliver":
			if _, err := writer.AppendEvent("compatibility_mailbox_delivered", journal.VisibilityCompatibilityMailbox, map[string]string{
				"to": step.to,
			}, stepTime); err != nil {
				t.Fatalf("AppendEvent(deliver %s): %v", step.to, err)
			}
		case "read":
			if _, err := writer.AppendEvent("compatibility_mailbox_read", journal.VisibilityOperatorVisible, map[string]string{
				"to": step.to,
			}, stepTime); err != nil {
				t.Fatalf("AppendEvent(read %s): %v", step.to, err)
			}
		default:
			t.Fatalf("unknown journal step kind %q", step.kind)
		}
	}

	cfg := config.DefaultConfig()
	cfg.Edges = []string{"worker -- critic"}

	return sessionHealthProjectionFixture{
		baseDir:     tmpDir,
		contextID:   contextID,
		sessionName: sessionName,
		configPath:  configPath,
		cfg:         cfg,
	}
}

func installSessionHealthProjectionTmux(t *testing.T, contextID, sessionName string) {
	t.Helper()

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := strings.Join([]string{
		"#!/bin/sh",
		"case \"$1 $2 $3\" in",
		"  \"list-sessions -F \"*)",
		"    printf '%s\\n' '" + sessionName + "\t$173'",
		"    ;;",
		"  \"list-panes -a -F\")",
		"    printf '%s\\n' '%11\t" + contextID + "\t" + sessionName + "\tworker' '%12\t" + contextID + "\t" + sessionName + "\tcritic'",
		"    ;;",
		"  \"list-windows -t " + sessionName + "\")",
		"    printf '%s\\n' '0'",
		"    ;;",
		"  \"list-panes -t " + sessionName + ":0\")",
		"    printf '%s\\n' '0\t0\t%11\tworker\tclaude' '0\t1\t%12\tcritic\tclaude'",
		"    ;;",
		"  *)",
		"    exit 1",
		"    ;;",
		"esac",
		"",
	}, "\n")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installSessionHealthProjectionBrokenTmux(t *testing.T) {
	t.Helper()

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := strings.Join([]string{
		"#!/bin/sh",
		"exit 1",
		"",
	}, "\n")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake broken tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installSessionHealthProjectionListSessionsOnlyTmux(t *testing.T, sessionName string) {
	t.Helper()

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := strings.Join([]string{
		"#!/bin/sh",
		"case \"$1 $2\" in",
		"  \"list-sessions -F\")",
		"    printf '%s\\n' '" + sessionName + "\t$173'",
		"    ;;",
		"  *)",
		"    exit 1",
		"    ;;",
		"esac",
		"",
	}, "\n")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake list-sessions-only tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func appendSessionHealthSnapshot(t *testing.T, fixture sessionHealthProjectionFixture, health status.SessionHealth) {
	t.Helper()

	sessionDir := filepath.Join(fixture.baseDir, fixture.contextID, fixture.sessionName)
	now := time.Date(2026, time.April, 14, 5, 0, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, fixture.contextID, fixture.sessionName, 303, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter(snapshot) error = %v", err)
	}
	if _, err := writer.AppendEvent(
		projection.SessionHealthSnapshotEventType,
		journal.VisibilityControlPlaneOnly,
		health,
		now.Add(time.Second),
	); err != nil {
		t.Fatalf("AppendEvent(session health snapshot): %v", err)
	}
}

func removeLiveSessionHealthArtifacts(t *testing.T, fixture sessionHealthProjectionFixture) {
	t.Helper()

	sessionDir := filepath.Join(fixture.baseDir, fixture.contextID, fixture.sessionName)
	if err := os.Remove(filepath.Join(fixture.baseDir, fixture.contextID, "pane-activity.json")); err != nil && !os.IsNotExist(err) {
		t.Fatalf("Remove(pane-activity.json): %v", err)
	}
	if err := os.RemoveAll(filepath.Join(sessionDir, "waiting")); err != nil {
		t.Fatalf("RemoveAll(waiting): %v", err)
	}
	if err := os.RemoveAll(filepath.Join(sessionDir, "inbox")); err != nil {
		t.Fatalf("RemoveAll(inbox): %v", err)
	}
}

func unreadFixtureName(nodeName string, index int) string {
	return "20260414-00000" + string(rune('1'+index)) + "-s0000-from-boss-to-" + nodeName + ".md"
}

func waitingFixtureName(nodeName string) string {
	return "20260414-000100-s0000-from-orchestrator-to-" + nodeName + ".md"
}
