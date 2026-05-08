package cli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

func writeLiveSessionPID(t *testing.T, baseDir, contextID, sessionName string) {
	t.Helper()

	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", sessionDir, err)
	}
	if err := os.WriteFile(
		filepath.Join(sessionDir, "postman.pid"),
		[]byte(strconv.Itoa(os.Getpid())),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile(postman.pid): %v", err)
	}
}

func TestRelayDaemonEventsToTUI_DoesNotBlockWhenTUIChannelIsFull(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rawEvents := make(chan tui.DaemonEvent)
	tuiEvents := make(chan tui.DaemonEvent)
	go relayDaemonEventsToTUI(ctx, rawEvents, tuiEvents, t.TempDir(), "ctx", config.DefaultConfig())

	send := func(event tui.DaemonEvent) {
		t.Helper()
		select {
		case rawEvents <- event:
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("relay blocked while forwarding %q with a full TUI channel", event.Type)
		}
	}

	send(tui.DaemonEvent{Type: "message_received", Message: "first"})
	send(tui.DaemonEvent{Type: "message_received", Message: "second"})
}

func TestForwardTUIEvent_DropsNonSessionEventWhenChannelIsFull(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tuiEvents := make(chan tui.DaemonEvent)
	done := make(chan bool, 1)
	go func() {
		done <- forwardTUIEvent(ctx, tuiEvents, tui.DaemonEvent{Type: "session_health_update"})
	}()

	select {
	case ok := <-done:
		if !ok {
			t.Fatal("forwardTUIEvent returned false for an active context")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("forwardTUIEvent blocked on a full TUI channel")
	}
}

func TestForwardTUIEvent_KeepsLatestSessionSnapshotWhenChannelIsFull(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tuiEvents := make(chan tui.DaemonEvent, 1)
	tuiEvents <- tui.DaemonEvent{Type: "message_received", Message: "older"}

	ok := forwardTUIEvent(ctx, tuiEvents, tui.DaemonEvent{
		Type: "status_update",
		Details: map[string]interface{}{
			"sessions": []tui.SessionInfo{{Name: "review", Enabled: true}},
		},
	})
	if !ok {
		t.Fatal("forwardTUIEvent returned false for an active context")
	}

	got := <-tuiEvents
	if got.Type != "status_update" {
		t.Fatalf("forwarded event type = %q, want status_update", got.Type)
	}
	sessions, ok := got.Details["sessions"].([]tui.SessionInfo)
	if !ok {
		t.Fatalf("sessions detail type = %T, want []tui.SessionInfo", got.Details["sessions"])
	}
	if len(sessions) != 1 || sessions[0].Name != "review" {
		t.Fatalf("sessions = %#v, want review snapshot", sessions)
	}

	select {
	case event := <-tuiEvents:
		t.Fatalf("unexpected extra event in TUI channel: %#v", event)
	default:
	}
}

func TestRelayDaemonEventsToTUI_EmitsSessionHealthUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "20260405-ctx"
	sessionName := "review"
	sessionDir := filepath.Join(tmpDir, contextID, sessionName)

	cfg := config.DefaultConfig()
	cfg.Edges = []string{"worker --- critic"}

	for _, dir := range []string{
		filepath.Join(sessionDir, "inbox", "worker"),
		filepath.Join(sessionDir, "inbox", "critic"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	if err := os.WriteFile(
		filepath.Join(tmpDir, contextID, "pane-activity.json"),
		[]byte(`{
  "%11": {"status":"active","lastChangeAt":"2026-04-05T00:00:00Z"},
  "%12": {"status":"active","lastChangeAt":"2026-04-05T00:00:00Z"}
}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(pane-activity.json): %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(sessionDir, "inbox", "worker", "20260405-000000-s0000-from-boss-to-worker.md"),
		[]byte("body"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(worker inbox): %v", err)
	}

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"case \"$1 $2 $3\" in\n" +
		"  \"list-panes -a -F\")\n" +
		"    printf '%s\\n' '%11\t" + contextID + "\t" + sessionName + "\tworker' '%12\t" + contextID + "\t" + sessionName + "\tcritic'\n" +
		"    ;;\n" +
		"  \"list-windows -t " + sessionName + "\")\n" +
		"    printf '%s\\n' '0'\n" +
		"    ;;\n" +
		"  \"list-panes -t " + sessionName + ":0\")\n" +
		"    printf '%s\\n' '0\t0\t%11\tworker\tclaude' '0\t1\t%12\tcritic\tclaude'\n" +
		"    ;;\n" +
		"  *)\n" +
		"    exit 1\n" +
		"    ;;\n" +
		"esac\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rawEvents := make(chan tui.DaemonEvent, 1)
	tuiEvents := make(chan tui.DaemonEvent, 4)
	go relayDaemonEventsToTUI(ctx, rawEvents, tuiEvents, tmpDir, contextID, cfg)

	rawEvents <- tui.DaemonEvent{
		Type:    "status_update",
		Message: "Running",
		Details: map[string]interface{}{
			"sessions": []tui.SessionInfo{{Name: sessionName, Enabled: true}},
		},
	}

	forwarded := <-tuiEvents
	if forwarded.Type != "status_update" {
		t.Fatalf("first event type = %q, want status_update", forwarded.Type)
	}

	healthEvent := <-tuiEvents
	if healthEvent.Type != "session_health_update" {
		t.Fatalf("second event type = %q, want session_health_update", healthEvent.Type)
	}
	health, ok := healthEvent.Details["health"].(status.SessionHealth)
	if !ok {
		t.Fatalf("health payload type = %T, want status.SessionHealth", healthEvent.Details["health"])
	}
	if health.SessionName != sessionName {
		t.Fatalf("health.SessionName = %q, want %q", health.SessionName, sessionName)
	}
	if health.VisibleState != "pending" {
		t.Fatalf("health.VisibleState = %q, want %q", health.VisibleState, "pending")
	}
	if len(health.Windows) != 1 || len(health.Windows[0].Nodes) != 2 {
		t.Fatalf("health.Windows = %#v, want one window with two nodes", health.Windows)
	}
}

func TestRelayDaemonEventsToTUI_NodeAliveRefreshesCanonicalHealth(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "20260405-ctx"
	sessionName := "review"
	sessionDir := filepath.Join(tmpDir, contextID, sessionName)

	cfg := config.DefaultConfig()
	cfg.Edges = []string{"worker --- critic"}

	for _, dir := range []string{
		filepath.Join(sessionDir, "inbox", "worker"),
		filepath.Join(sessionDir, "inbox", "critic"),
		filepath.Join(sessionDir, "read"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	if err := os.WriteFile(
		filepath.Join(tmpDir, contextID, "pane-activity.json"),
		[]byte(`{
  "%11": {"status":"active","lastChangeAt":"2026-04-05T00:00:00Z"},
  "%12": {"status":"active","lastChangeAt":"2026-04-05T00:00:00Z"}
}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(pane-activity.json): %v", err)
	}

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"case \"$1 $2 $3\" in\n" +
		"  \"list-panes -a -F\")\n" +
		"    printf '%s\\n' '%11\t" + contextID + "\t" + sessionName + "\tworker' '%12\t" + contextID + "\t" + sessionName + "\tcritic'\n" +
		"    ;;\n" +
		"  \"list-windows -t " + sessionName + "\")\n" +
		"    printf '%s\\n' '0'\n" +
		"    ;;\n" +
		"  \"list-panes -t " + sessionName + ":0\")\n" +
		"    printf '%s\\n' '0\t0\t%11\tworker\tclaude' '0\t1\t%12\tcritic\tclaude'\n" +
		"    ;;\n" +
		"  *)\n" +
		"    exit 1\n" +
		"    ;;\n" +
		"esac\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rawEvents := make(chan tui.DaemonEvent, 2)
	tuiEvents := make(chan tui.DaemonEvent, 8)
	go relayDaemonEventsToTUI(ctx, rawEvents, tuiEvents, tmpDir, contextID, cfg)

	rawEvents <- tui.DaemonEvent{
		Type:    "status_update",
		Message: "Running",
		Details: map[string]interface{}{
			"sessions": []tui.SessionInfo{{Name: sessionName, Enabled: true}},
		},
	}

	<-tuiEvents
	initialHealthEvent := <-tuiEvents
	if initialHealthEvent.Type != "session_health_update" {
		t.Fatalf("initial health event type = %q, want session_health_update", initialHealthEvent.Type)
	}
	initialHealth, ok := initialHealthEvent.Details["health"].(status.SessionHealth)
	if !ok {
		t.Fatalf("initial health payload type = %T, want status.SessionHealth", initialHealthEvent.Details["health"])
	}
	if initialHealth.VisibleState != "ready" {
		t.Fatalf("initial health.VisibleState = %q, want %q", initialHealth.VisibleState, "ready")
	}

	criticInboxPath := filepath.Join(sessionDir, "inbox", "critic", "20260405-000001-s0000-from-worker-to-critic.md")
	if err := os.WriteFile(criticInboxPath, []byte("body"), 0o644); err != nil {
		t.Fatalf("WriteFile(critic inbox): %v", err)
	}

	rawEvents <- tui.DaemonEvent{
		Type: "node_alive",
		Details: map[string]interface{}{
			"node":   sessionName + ":critic",
			"source": "read_move",
		},
	}

	forwarded := <-tuiEvents
	if forwarded.Type != "node_alive" {
		t.Fatalf("forwarded event type = %q, want node_alive", forwarded.Type)
	}

	healthEvent := <-tuiEvents
	if healthEvent.Type != "session_health_update" {
		t.Fatalf("health event type = %q, want session_health_update", healthEvent.Type)
	}
	health, ok := healthEvent.Details["health"].(status.SessionHealth)
	if !ok {
		t.Fatalf("health payload type = %T, want status.SessionHealth", healthEvent.Details["health"])
	}
	if health.VisibleState != "pending" {
		t.Fatalf("health.VisibleState = %q, want %q", health.VisibleState, "pending")
	}
	if len(health.Nodes) != 2 {
		t.Fatalf("health.Nodes length = %d, want 2", len(health.Nodes))
	}
	for _, node := range health.Nodes {
		if node.Name == "critic" && node.VisibleState != "pending" {
			t.Fatalf("critic visible state = %q, want %q", node.VisibleState, "pending")
		}
	}
}

func TestRelayDaemonEventsToTUI_SkipsCanonicalHealthForForeignOwnedSession(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "20260405-ctx-self"
	ownerContextID := "20260405-ctx-owner"
	sessionName := "review"

	cfg := config.DefaultConfig()
	cfg.Edges = []string{"worker --- critic"}

	for _, dir := range []string{
		filepath.Join(tmpDir, contextID, sessionName, "inbox", "worker"),
		filepath.Join(tmpDir, contextID, sessionName, "inbox", "critic"),
		filepath.Join(tmpDir, ownerContextID, sessionName, "inbox", "worker"),
		filepath.Join(tmpDir, ownerContextID, sessionName, "inbox", "critic"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	writeLiveSessionPID(t, tmpDir, contextID, "main")
	writeLiveSessionPID(t, tmpDir, ownerContextID, "0")

	if err := os.WriteFile(
		filepath.Join(tmpDir, contextID, "pane-activity.json"),
		[]byte(`{
  "%11": {"status":"active","lastChangeAt":"2026-04-05T00:00:00Z"},
  "%12": {"status":"active","lastChangeAt":"2026-04-05T00:00:00Z"}
}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(pane-activity.json): %v", err)
	}

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"case \"$1 $2 $3\" in\n" +
		"  \"list-panes -a -F\")\n" +
		"    printf '%s\\n' '%11\t" + ownerContextID + "\t" + sessionName + "\tworker' '%12\t" + ownerContextID + "\t" + sessionName + "\tcritic'\n" +
		"    ;;\n" +
		"  \"list-windows -t " + sessionName + "\")\n" +
		"    printf '%s\\n' '0'\n" +
		"    ;;\n" +
		"  \"list-panes -t " + sessionName + ":0\")\n" +
		"    printf '%s\\n' '0\t0\t%11\tworker\tclaude' '0\t1\t%12\tcritic\tclaude'\n" +
		"    ;;\n" +
		"  *)\n" +
		"    if [ \"$1 $2\" = \"show-options -gqv\" ] && [ \"$3\" = \"@a2a_session_on_" + sessionName + "\" ]; then\n" +
		"      printf '%s\\n' '" + ownerContextID + ":43210'\n" +
		"      exit 0\n" +
		"    fi\n" +
		"    if [ \"$1 $2\" = \"show-options -gqv\" ]; then\n" +
		"      exit 0\n" +
		"    fi\n" +
		"    exit 1\n" +
		"    ;;\n" +
		"esac\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rawEvents := make(chan tui.DaemonEvent, 1)
	tuiEvents := make(chan tui.DaemonEvent, 4)
	go relayDaemonEventsToTUI(ctx, rawEvents, tuiEvents, tmpDir, contextID, cfg)

	rawEvents <- tui.DaemonEvent{
		Type:    "status_update",
		Message: "Running",
		Details: map[string]interface{}{
			"sessions": []tui.SessionInfo{{Name: sessionName, Enabled: true}},
		},
	}

	<-tuiEvents
	healthEvent := <-tuiEvents
	if healthEvent.Type != "session_health_update" {
		t.Fatalf("health event type = %q, want session_health_update", healthEvent.Type)
	}
	health, ok := healthEvent.Details["health"].(status.SessionHealth)
	if !ok {
		t.Fatalf("health payload type = %T, want status.SessionHealth", healthEvent.Details["health"])
	}
	if health.SessionName != sessionName {
		t.Fatalf("health.SessionName = %q, want %q", health.SessionName, sessionName)
	}
	if health.NodeCount != 0 {
		t.Fatalf("health.NodeCount = %d, want 0 for foreign-owned session", health.NodeCount)
	}
	if len(health.Nodes) != 0 {
		t.Fatalf("health.Nodes = %#v, want empty for foreign-owned session", health.Nodes)
	}
	if len(health.Windows) != 0 {
		t.Fatalf("health.Windows = %#v, want empty for foreign-owned session", health.Windows)
	}
	if health.VisibleState != "unavailable" {
		t.Fatalf("health.VisibleState = %q, want %q for foreign-owned session", health.VisibleState, "unavailable")
	}
}
