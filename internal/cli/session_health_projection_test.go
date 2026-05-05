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
			fixture := writeSessionHealthProjectionFixture(t, tc.paneStates, tc.liveInbox, tc.journalSteps)

			legacy, err := collectSessionHealthLegacy(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
			if err != nil {
				t.Fatalf("collectSessionHealthLegacy() error = %v", err)
			}
			appendSessionHealthSnapshot(t, fixture, legacy)
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
	appendSessionHealthSnapshot(t, fixture, legacy.Sessions[0])

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
	installFakeTmuxForCLI(t, tmpDir, "review", "worker")

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunGetSessionHealth(nil)
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
		[]projectionJournalStep{
			{kind: "deliver", to: "worker"},
		},
	)

	legacy, err := collectSessionHealthLegacy(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectSessionHealthLegacy() error = %v", err)
	}
	appendSessionHealthSnapshot(t, fixture, legacy)

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

func TestGetHealthUsesLiveArtifactsWithoutSnapshot(t *testing.T) {
	fixture := writeSessionHealthProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		map[string]int{"worker": 1},
		[]projectionJournalStep{
			{kind: "deliver", to: "worker"},
		},
	)

	projected, err := collectSessionHealth(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectSessionHealth() error = %v", err)
	}

	if projected.ContextID != fixture.contextID {
		t.Fatalf("ContextID = %q, want %q", projected.ContextID, fixture.contextID)
	}
	if projected.SessionName != fixture.sessionName {
		t.Fatalf("SessionName = %q, want %q", projected.SessionName, fixture.sessionName)
	}
	if projected.VisibleState != "pending" || projected.Compact != "🔷🟢" || projected.NodeCount != 2 {
		t.Fatalf("unexpected live health payload: %#v", projected)
	}
	if len(projected.Nodes) != 2 || len(projected.Windows) != 1 {
		t.Fatalf("unexpected live health topology: %#v", projected)
	}
}

func TestGetHealthExposesChangedAndUnchangedScreenProgressEvidence(t *testing.T) {
	fixture := writeSessionHealthProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		nil,
		nil,
	)
	writeSessionHealthPaneActivity(t, fixture, `{
  "%11": {"status":"active","lastChangeAt":"2026-04-14T00:00:00Z","lastCaptureAt":"2026-04-14T00:00:05Z","screenFingerprint":"00000011"},
  "%12": {"status":"active","lastChangeAt":"2026-04-14T00:01:00Z","lastCaptureAt":"2026-04-14T00:01:00Z","screenFingerprint":"00000012"}
}`)

	health, err := collectSessionHealthLegacy(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectSessionHealthLegacy() error = %v", err)
	}

	nodeByName := map[string]status.NodeHealth{}
	for _, node := range health.Nodes {
		nodeByName[node.Name] = node
	}

	worker := nodeByName["worker"].ScreenProgress
	if worker == nil {
		t.Fatal("worker ScreenProgress is nil, want unchanged evidence")
	}
	if worker.EvidenceState != "unchanged" {
		t.Fatalf("worker evidence_state = %q, want unchanged", worker.EvidenceState)
	}
	if worker.LastCaptureAt != "2026-04-14T00:00:05Z" {
		t.Fatalf("worker last_capture_at = %q, want 2026-04-14T00:00:05Z", worker.LastCaptureAt)
	}
	if worker.LastScreenChangeAt != "2026-04-14T00:00:00Z" {
		t.Fatalf("worker last_screen_change_at = %q, want 2026-04-14T00:00:00Z", worker.LastScreenChangeAt)
	}
	if worker.ScreenFingerprint != "00000011" {
		t.Fatalf("worker screen_fingerprint = %q, want 00000011", worker.ScreenFingerprint)
	}

	critic := nodeByName["critic"].ScreenProgress
	if critic == nil {
		t.Fatal("critic ScreenProgress is nil, want changed evidence")
	}
	if critic.EvidenceState != "changed" {
		t.Fatalf("critic evidence_state = %q, want changed", critic.EvidenceState)
	}
	if critic.LastCaptureAt != "2026-04-14T00:01:00Z" {
		t.Fatalf("critic last_capture_at = %q, want 2026-04-14T00:01:00Z", critic.LastCaptureAt)
	}
	if critic.LastScreenChangeAt != "2026-04-14T00:01:00Z" {
		t.Fatalf("critic last_screen_change_at = %q, want 2026-04-14T00:01:00Z", critic.LastScreenChangeAt)
	}
	if critic.ScreenFingerprint != "00000012" {
		t.Fatalf("critic screen_fingerprint = %q, want 00000012", critic.ScreenFingerprint)
	}
}

func TestGetHealthExposesMissingAndStaleScreenProgressEvidence(t *testing.T) {
	fixture := writeSessionHealthProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "stale"},
		nil,
		nil,
	)
	writeSessionHealthPaneActivity(t, fixture, `{
  "%11": "active",
  "%12": {"status":"stale","lastChangeAt":"2026-04-14T00:02:00Z","lastCaptureAt":"2026-04-14T00:02:05Z","screenFingerprint":"00000012"}
}`)

	health, err := collectSessionHealthLegacy(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectSessionHealthLegacy() error = %v", err)
	}

	nodeByName := map[string]status.NodeHealth{}
	for _, node := range health.Nodes {
		nodeByName[node.Name] = node
	}

	worker := nodeByName["worker"].ScreenProgress
	if worker == nil {
		t.Fatal("worker ScreenProgress is nil, want missing evidence")
	}
	if worker.EvidenceState != "missing" {
		t.Fatalf("worker evidence_state = %q, want missing", worker.EvidenceState)
	}
	if worker.LastCaptureAt != "" || worker.LastScreenChangeAt != "" || worker.ScreenFingerprint != "" {
		t.Fatalf("worker missing evidence should not include progress details: %#v", worker)
	}

	critic := nodeByName["critic"].ScreenProgress
	if critic == nil {
		t.Fatal("critic ScreenProgress is nil, want stale evidence")
	}
	if critic.EvidenceState != "stale" {
		t.Fatalf("critic evidence_state = %q, want stale", critic.EvidenceState)
	}
	if critic.LastCaptureAt != "2026-04-14T00:02:05Z" {
		t.Fatalf("critic last_capture_at = %q, want 2026-04-14T00:02:05Z", critic.LastCaptureAt)
	}
	if critic.LastScreenChangeAt != "2026-04-14T00:02:00Z" {
		t.Fatalf("critic last_screen_change_at = %q, want 2026-04-14T00:02:00Z", critic.LastScreenChangeAt)
	}
	if critic.ScreenFingerprint != "00000012" {
		t.Fatalf("critic screen_fingerprint = %q, want 00000012", critic.ScreenFingerprint)
	}
}

func TestGetHealthUsesReplyObligationProjection(t *testing.T) {
	fixture := writeSessionHealthProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		map[string]int{"worker": 1},
		nil,
	)
	sessionDir := filepath.Join(fixture.baseDir, fixture.contextID, fixture.sessionName)
	now := time.Date(2026, time.April, 14, 5, 0, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, fixture.contextID, fixture.sessionName, 202, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	content := sessionHealthObligationContent("critic", "worker", "m1.md", "required", "", "please review")
	appendSessionHealthObligationEvent(t, writer, projection.MailboxProjectionPostConsumedEventType, "m1.md", "critic", "worker", content, now.Add(time.Second))
	appendSessionHealthObligationEvent(t, writer, projection.MailboxProjectionDeliveredEventType, "m1.md", "critic", "worker", content, now.Add(2*time.Second))

	health, err := collectSessionHealthLegacy(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectSessionHealthLegacy() error = %v", err)
	}

	nodeByName := map[string]status.NodeHealth{}
	for _, node := range health.Nodes {
		nodeByName[node.Name] = node
	}
	if nodeByName["worker"].VisibleState != "pending" || nodeByName["worker"].ActionRequiredCount != 1 {
		t.Fatalf("worker health = %#v, want pending action_required=1", nodeByName["worker"])
	}
	if nodeByName["critic"].VisibleState != "waiting" || nodeByName["critic"].WaitingOnReplyCount != 1 {
		t.Fatalf("critic health = %#v, want waiting waiting_on_reply=1", nodeByName["critic"])
	}
	if health.Compact != "🔷🟡" {
		t.Fatalf("health.Compact = %q, want 🔷🟡", health.Compact)
	}
}

func TestGetHealthPrefersLiveRecomputeOverStaleSnapshot(t *testing.T) {
	fixture := writeSessionHealthProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		map[string]int{},
		nil,
	)
	sessionDir := filepath.Join(fixture.baseDir, fixture.contextID, fixture.sessionName)

	staleSnapshot, err := collectSessionHealthLegacy(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectSessionHealthLegacy() stale snapshot error = %v", err)
	}
	if staleSnapshot.VisibleState != "ready" {
		t.Fatalf("staleSnapshot.VisibleState = %q, want ready", staleSnapshot.VisibleState)
	}

	now := time.Date(2026, time.April, 14, 5, 10, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, fixture.contextID, fixture.sessionName, 404, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	if _, err := writer.AppendEvent(projection.SessionHealthSnapshotEventType, journal.VisibilityControlPlaneOnly, staleSnapshot, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendEvent(session health snapshot): %v", err)
	}
	content := sessionHealthObligationContent("critic", "worker", "m1.md", "required", "", "please review")
	appendSessionHealthObligationEvent(t, writer, projection.MailboxProjectionPostConsumedEventType, "m1.md", "critic", "worker", content, now.Add(2*time.Second))
	appendSessionHealthObligationEvent(t, writer, projection.MailboxProjectionDeliveredEventType, "m1.md", "critic", "worker", content, now.Add(3*time.Second))

	health, err := collectSessionHealth(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectSessionHealth() error = %v", err)
	}

	nodeByName := map[string]status.NodeHealth{}
	for _, node := range health.Nodes {
		nodeByName[node.Name] = node
	}
	if nodeByName["worker"].VisibleState != "pending" || nodeByName["worker"].ActionRequiredCount != 1 {
		t.Fatalf("worker health = %#v, want pending action_required=1", nodeByName["worker"])
	}
	if nodeByName["critic"].VisibleState != "waiting" || nodeByName["critic"].WaitingOnReplyCount != 1 {
		t.Fatalf("critic health = %#v, want waiting waiting_on_reply=1", nodeByName["critic"])
	}
}

func TestGetHealthFallsBackForUnclassifiedLegacyUnread(t *testing.T) {
	fixture := writeSessionHealthProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		map[string]int{"worker": 1, "critic": 1},
		nil,
	)
	sessionDir := filepath.Join(fixture.baseDir, fixture.contextID, fixture.sessionName)
	now := time.Date(2026, time.April, 14, 5, 20, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, fixture.contextID, fixture.sessionName, 505, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	if _, err := writer.AppendEvent(projection.MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, map[string]string{
		"message_id": "legacy.md",
		"to":         "worker",
	}, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendEvent(legacy delivered): %v", err)
	}
	content := sessionHealthObligationContent("worker", "critic", "info.md", "none", "", "FYI")
	appendSessionHealthObligationEvent(t, writer, projection.MailboxProjectionDeliveredEventType, "info.md", "worker", "critic", content, now.Add(2*time.Second))

	health, err := collectSessionHealth(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectSessionHealth() error = %v", err)
	}

	nodeByName := map[string]status.NodeHealth{}
	for _, node := range health.Nodes {
		nodeByName[node.Name] = node
	}
	if nodeByName["worker"].VisibleState != "pending" {
		t.Fatalf("worker health = %#v, want pending from unread fallback", nodeByName["worker"])
	}
	if nodeByName["critic"].VisibleState != "ready" || nodeByName["critic"].InfoUnreadCount != 1 {
		t.Fatalf("critic health = %#v, want ready info_unread=1", nodeByName["critic"])
	}
}

func TestGetHealthProjectionRebuildsWithoutLiveArtifacts(t *testing.T) {
	fixture := writeSessionHealthProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "idle"},
		map[string]int{"worker": 1},
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

func sessionHealthObligationContent(from, to, messageID, replyPolicy, replyTo, body string) string {
	replyToLine := ""
	if replyTo != "" {
		replyToLine = "  replyTo: " + replyTo + "\n"
	}
	return "---\nparams:\n" +
		"  from: " + from + "\n" +
		"  to: " + to + "\n" +
		"  messageId: " + messageID + "\n" +
		"  replyPolicy: " + replyPolicy + "\n" +
		replyToLine +
		"---\n\n" + body + "\n"
}

func appendSessionHealthObligationEvent(t *testing.T, writer *journal.Writer, eventType, messageID, from, to, content string, now time.Time) {
	t.Helper()
	if _, err := writer.AppendEvent(eventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: messageID,
		From:      from,
		To:        to,
		Content:   content,
	}, now); err != nil {
		t.Fatalf("AppendEvent(%s): %v", eventType, err)
	}
}

func TestGetHealthOnelineProjectionRebuildsWithoutLiveTopology(t *testing.T) {
	fixture := writeSessionHealthProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		map[string]int{"critic": 1},
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

func writeSessionHealthProjectionFixture(t *testing.T, paneStates map[string]string, liveInbox map[string]int, journalSteps []projectionJournalStep) sessionHealthProjectionFixture {
	t.Helper()

	tmpDir := t.TempDir()
	contextID := "20260414-ctx"
	sessionName := "review"
	sessionDir := filepath.Join(tmpDir, contextID, sessionName)

	t.Setenv("POSTMAN_HOME", tmpDir)

	configPath := filepath.Join(tmpDir, "postman.toml")
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"worker --- critic\"]\n\n[worker]\ntemplate = \"worker\"\nrole = \"worker\"\n\n[critic]\ntemplate = \"critic\"\nrole = \"critic\"\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(postman.toml): %v", err)
	}

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
			if _, err := writer.AppendEvent(projection.MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, map[string]string{
				"to": step.to,
			}, stepTime); err != nil {
				t.Fatalf("AppendEvent(deliver %s): %v", step.to, err)
			}
		case "read":
			if _, err := writer.AppendEvent(projection.MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, map[string]string{
				"to": step.to,
			}, stepTime); err != nil {
				t.Fatalf("AppendEvent(read %s): %v", step.to, err)
			}
		default:
			t.Fatalf("unknown journal step kind %q", step.kind)
		}
	}

	cfg := config.DefaultConfig()
	cfg.Edges = []string{"worker --- critic"}

	return sessionHealthProjectionFixture{
		baseDir:     tmpDir,
		contextID:   contextID,
		sessionName: sessionName,
		configPath:  configPath,
		cfg:         cfg,
	}
}

func writeSessionHealthPaneActivity(t *testing.T, fixture sessionHealthProjectionFixture, content string) {
	t.Helper()

	if err := os.WriteFile(
		filepath.Join(fixture.baseDir, fixture.contextID, "pane-activity.json"),
		[]byte(content),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(pane-activity.json): %v", err)
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

func appendAllSessionHealthSnapshots(t *testing.T, baseDir, contextID string, sessions []status.SessionHealth) {
	t.Helper()

	for _, health := range sessions {
		appendSessionHealthSnapshot(t, sessionHealthProjectionFixture{
			baseDir:     baseDir,
			contextID:   contextID,
			sessionName: health.SessionName,
		}, health)
	}
}

func removeLiveSessionHealthArtifacts(t *testing.T, fixture sessionHealthProjectionFixture) {
	t.Helper()

	sessionDir := filepath.Join(fixture.baseDir, fixture.contextID, fixture.sessionName)
	if err := os.Remove(filepath.Join(fixture.baseDir, fixture.contextID, "pane-activity.json")); err != nil && !os.IsNotExist(err) {
		t.Fatalf("Remove(pane-activity.json): %v", err)
	}
	if err := os.RemoveAll(filepath.Join(sessionDir, "inbox")); err != nil {
		t.Fatalf("RemoveAll(inbox): %v", err)
	}
}

func unreadFixtureName(nodeName string, index int) string {
	return "20260414-00000" + string(rune('1'+index)) + "-s0000-from-boss-to-" + nodeName + ".md"
}
