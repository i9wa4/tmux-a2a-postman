package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

type projectionJournalStep struct {
	kind string
	to   string
}

type sessionStatusProjectionFixture struct {
	baseDir     string
	contextID   string
	sessionName string
	configPath  string
	cfg         *config.Config
}

func TestSessionStatusProjectionParity(t *testing.T) {
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
			fixture := writeSessionStatusProjectionFixture(t, tc.paneStates, tc.liveInbox, tc.journalSteps)

			legacy, err := collectLiveSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
			if err != nil {
				t.Fatalf("collectLiveSessionStatus() error = %v", err)
			}
			appendSessionStatusSnapshot(t, fixture, legacy)
			projected, err := collectSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
			if err != nil {
				t.Fatalf("collectSessionStatus() error = %v", err)
			}

			if len(legacy.LayoutGroups) == 0 {
				t.Fatal("legacy LayoutGroups is empty, want backend-neutral layout projection")
			}
			assertSessionStatusParity(t, legacy, projected)
		})
	}
}

func TestGetStatusOnelineProjectionParity(t *testing.T) {
	fixture := writeSessionStatusProjectionFixture(
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

	legacy, _, ok, err := collectAllLiveSessionStatus(fixture.contextID, "", fixture.configPath)
	if err != nil {
		t.Fatalf("collectAllLiveSessionStatus() error = %v", err)
	}
	if !ok {
		t.Fatal("collectAllLiveSessionStatus() ok = false, want true")
	}
	appendSessionStatusSnapshot(t, fixture, legacy.Sessions[0])

	projected, _, ok, err := collectAllSessionStatus(fixture.contextID, "", fixture.configPath)
	if err != nil {
		t.Fatalf("collectAllSessionStatus() error = %v", err)
	}
	if !ok {
		t.Fatal("collectAllSessionStatus() ok = false, want true")
	}

	legacyOneline := formatAllSessionStatusOneline(legacy)
	projectedOneline := formatAllSessionStatusOneline(projected)
	if legacyOneline != projectedOneline {
		t.Fatalf("oneline mismatch:\nlegacy:    %q\nprojected: %q", legacyOneline, projectedOneline)
	}
}

func TestSessionStatusLayoutGroupsProjectTmuxWindowsCompatibility(t *testing.T) {
	fixture := writeSessionStatusProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "idle"},
		map[string]int{},
		nil,
	)

	health, err := collectLiveSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectLiveSessionStatus() error = %v", err)
	}

	if got, want := health.Compact, "⚫⚫"; got != want {
		t.Fatalf("Compact = %q, want %q", got, want)
	}
	if len(health.Windows) != 1 || health.Windows[0].Index != "0" {
		t.Fatalf("Windows = %#v, want tmux-compatible window projection", health.Windows)
	}
	if got := []string{health.Windows[0].Nodes[0].Name, health.Windows[0].Nodes[1].Name}; !reflect.DeepEqual(got, []string{"worker", "critic"}) {
		t.Fatalf("window nodes = %#v", got)
	}
	if len(health.LayoutGroups) != 1 {
		t.Fatalf("len(LayoutGroups) = %d, want 1: %#v", len(health.LayoutGroups), health.LayoutGroups)
	}
	group := health.LayoutGroups[0]
	if group.Kind != "window" || group.ID != "review:0" || group.Index != "0" || group.Backend != "tmux" {
		t.Fatalf("LayoutGroups[0] = %#v, want tmux window group", group)
	}
	if len(group.Nodes) != 2 {
		t.Fatalf("len(LayoutGroups[0].Nodes) = %d, want 2", len(group.Nodes))
	}
	if group.Nodes[0].Name != "worker" || group.Nodes[0].PaneID != "%11" || group.Nodes[0].Backend != "tmux" {
		t.Fatalf("LayoutGroups[0].Nodes[0] = %#v", group.Nodes[0])
	}
}

func TestNoActivePostmanUnavailableContract(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "review", "worker")

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunGetSessionStatus(nil)
	})
	if err != nil {
		t.Fatalf("RunGetSessionStatus() error = %v", err)
	}

	var payload status.SessionStatus
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
	fixture := writeSessionStatusProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		map[string]int{"worker": 1},
		[]projectionJournalStep{
			{kind: "deliver", to: "worker"},
		},
	)

	legacy, err := collectLiveSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectLiveSessionStatus() error = %v", err)
	}
	appendSessionStatusSnapshot(t, fixture, legacy)

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
	statusEvent := <-tuiEvents
	if statusEvent.Type != "session_status_update" {
		t.Fatalf("health event type = %q, want session_status_update", statusEvent.Type)
	}

	got, ok := statusEvent.Details["status"].(status.SessionStatus)
	if !ok {
		t.Fatalf("health payload type = %T, want status.SessionStatus", statusEvent.Details["status"])
	}
	assertSessionStatusParity(t, legacy, got)
}

func TestSessionStatusUsesLiveArtifactsWithoutSnapshot(t *testing.T) {
	fixture := writeSessionStatusProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		map[string]int{"worker": 1},
		[]projectionJournalStep{
			{kind: "deliver", to: "worker"},
		},
	)

	projected, err := collectSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectSessionStatus() error = %v", err)
	}

	if projected.ContextID != fixture.contextID {
		t.Fatalf("ContextID = %q, want %q", projected.ContextID, fixture.contextID)
	}
	if projected.SessionName != fixture.sessionName {
		t.Fatalf("SessionName = %q, want %q", projected.SessionName, fixture.sessionName)
	}
	if projected.VisibleState != "pending" || projected.Compact != "🔷⚫" || projected.NodeCount != 2 {
		t.Fatalf("unexpected live health payload: %#v", projected)
	}
	if len(projected.Nodes) != 2 || len(projected.Windows) != 1 {
		t.Fatalf("unexpected live health topology: %#v", projected)
	}
}

func TestSessionStatusExposesChangedAndUnchangedScreenProgressEvidence(t *testing.T) {
	fixture := writeSessionStatusProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		nil,
		nil,
	)
	writeSessionStatusPaneActivity(t, fixture, `{
  "%11": {"status":"active","lastChangeAt":"2026-04-14T00:00:00Z","lastCaptureAt":"2026-04-14T00:00:05Z","screenFingerprint":"00000011"},
  "%12": {"status":"active","lastChangeAt":"2026-04-14T00:01:00Z","lastCaptureAt":"2026-04-14T00:01:00Z","screenFingerprint":"00000012"}
}`)

	health, err := collectLiveSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectLiveSessionStatus() error = %v", err)
	}

	nodeByName := map[string]status.NodeStatus{}
	for _, node := range health.Nodes {
		nodeByName[node.Name] = node
	}

	worker := nodeByName["worker"].ScreenProgress
	if worker == nil {
		t.Fatal("worker ScreenProgress is nil, want unchanged evidence")
		return
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
	if worker.StaleDurationSeconds != 5 {
		t.Fatalf("worker stale_duration_seconds = %d, want 5", worker.StaleDurationSeconds)
	}

	critic := nodeByName["critic"].ScreenProgress
	if critic == nil {
		t.Fatal("critic ScreenProgress is nil, want changed evidence")
		return
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

func TestSessionStatusClassifiesNodeLocalFreshnessMatrix(t *testing.T) {
	now := time.Date(2026, time.April, 14, 0, 10, 0, 0, time.UTC)
	progress := func(state string, changeDelta, captureDelta time.Duration) *status.ScreenProgressEvidence {
		captureAt := now.Add(captureDelta)
		changeAt := now.Add(changeDelta)
		result := &status.ScreenProgressEvidence{
			EvidenceState:        state,
			LastCaptureAt:        captureAt.Format(time.RFC3339Nano),
			LastScreenChangeAt:   changeAt.Format(time.RFC3339Nano),
			ScreenFingerprint:    "00000011",
			StaleDurationSeconds: int(captureAt.Sub(changeAt).Seconds()),
		}
		if result.StaleDurationSeconds < 0 {
			result.StaleDurationSeconds = 0
		}
		return result
	}

	tests := []struct {
		name             string
		node             status.NodeStatus
		wantState        string
		wantSeverity     string
		wantEvidence     string
		wantFreshness    string
		wantAge          int
		wantUnchanged    int
		wantReasonSubstr string
	}{
		{
			name:          "fresh changed becomes working",
			node:          status.NodeStatus{Name: "worker", PaneState: "active", ScreenProgress: progress("changed", -5*time.Second, -5*time.Second)},
			wantState:     "working",
			wantSeverity:  "working",
			wantEvidence:  "observed",
			wantFreshness: "fresh",
			wantAge:       5,
		},
		{
			name:          "fresh unchanged remains live",
			node:          status.NodeStatus{Name: "worker", PaneState: "active", ScreenProgress: progress("unchanged", -20*time.Second, -5*time.Second)},
			wantState:     "live",
			wantSeverity:  "ok",
			wantEvidence:  "observed",
			wantFreshness: "fresh",
			wantAge:       5,
			wantUnchanged: 15,
		},
		{
			name:          "aging unchanged remains live",
			node:          status.NodeStatus{Name: "worker", PaneState: "active", ScreenProgress: progress("unchanged", -70*time.Second, -5*time.Second)},
			wantState:     "live",
			wantSeverity:  "ok",
			wantEvidence:  "observed",
			wantFreshness: "aging",
			wantAge:       5,
			wantUnchanged: 65,
		},
		{
			name:             "old but changed is stale",
			node:             status.NodeStatus{Name: "worker", PaneState: "active", ScreenProgress: progress("changed", -4*time.Minute, -4*time.Minute)},
			wantState:        "stale",
			wantSeverity:     "attention_stale",
			wantEvidence:     "observed",
			wantFreshness:    "stale",
			wantAge:          240,
			wantReasonSubstr: "capture is stale",
		},
		{
			name:             "runtime lagged changed is aging live",
			node:             status.NodeStatus{Name: "worker", PaneState: "active", ScreenProgress: progress("changed", -45*time.Second, -45*time.Second)},
			wantState:        "live",
			wantSeverity:     "ok",
			wantEvidence:     "observed",
			wantFreshness:    "aging",
			wantAge:          45,
			wantReasonSubstr: "capture is aging",
		},
		{
			name:             "explicit stale is stale",
			node:             status.NodeStatus{Name: "worker", PaneState: "active", ScreenProgress: &status.ScreenProgressEvidence{EvidenceState: "stale", StaleDurationSeconds: 181}},
			wantState:        "stale",
			wantSeverity:     "attention_stale",
			wantEvidence:     "observed",
			wantFreshness:    "stale",
			wantUnchanged:    181,
			wantReasonSubstr: "evidence is stale",
		},
		{
			name:             "active missing is unknown not working",
			node:             status.NodeStatus{Name: "worker", PaneState: "active", ScreenProgress: missingScreenProgressEvidence()},
			wantState:        "unknown",
			wantSeverity:     "ok",
			wantEvidence:     "unknown",
			wantFreshness:    "unknown",
			wantReasonSubstr: "missing",
		},
		{
			name:             "malformed progress is unknown not working",
			node:             status.NodeStatus{Name: "worker", PaneState: "active", ScreenProgress: screenProgressEvidence("active", "bad", now.Format(time.RFC3339Nano), "00000011")},
			wantState:        "unknown",
			wantSeverity:     "ok",
			wantEvidence:     "unknown",
			wantFreshness:    "unknown",
			wantReasonSubstr: "malformed",
		},
		{
			name:             "future capture is conflict",
			node:             status.NodeStatus{Name: "worker", PaneState: "active", ScreenProgress: progress("changed", time.Second, time.Second)},
			wantState:        "conflict",
			wantSeverity:     "attention_stale",
			wantEvidence:     "observed",
			wantFreshness:    "conflict",
			wantAge:          -1,
			wantReasonSubstr: "future",
		},
		{
			name:             "sub-second future capture is conflict",
			node:             status.NodeStatus{Name: "worker", PaneState: "active", ScreenProgress: progress("changed", 500*time.Millisecond, 500*time.Millisecond)},
			wantState:        "conflict",
			wantSeverity:     "attention_stale",
			wantEvidence:     "observed",
			wantFreshness:    "conflict",
			wantReasonSubstr: "future",
		},
		{
			name:             "change after capture is conflict",
			node:             status.NodeStatus{Name: "worker", PaneState: "active", ScreenProgress: progress("unchanged", -5*time.Second, -10*time.Second)},
			wantState:        "conflict",
			wantSeverity:     "attention_stale",
			wantEvidence:     "observed",
			wantFreshness:    "conflict",
			wantAge:          10,
			wantReasonSubstr: "after the capture",
		},
		{
			name:             "contradictory changed state is conflict",
			node:             status.NodeStatus{Name: "worker", PaneState: "active", ScreenProgress: progress("changed", -20*time.Second, -5*time.Second)},
			wantState:        "conflict",
			wantSeverity:     "attention_stale",
			wantEvidence:     "observed",
			wantFreshness:    "conflict",
			wantAge:          5,
			wantUnchanged:    15,
			wantReasonSubstr: "contradicts",
		},
		{
			name:             "changed screen with shell command is conflict",
			node:             status.NodeStatus{Name: "worker", PaneState: "active", CurrentCommand: "bash", ScreenProgress: progress("changed", -5*time.Second, -5*time.Second)},
			wantState:        "conflict",
			wantSeverity:     "attention_stale",
			wantEvidence:     "observed",
			wantFreshness:    "conflict",
			wantAge:          5,
			wantReasonSubstr: "current command is a shell",
		},
		{
			name:             "changed screen with idle pane is conflict",
			node:             status.NodeStatus{Name: "worker", PaneState: "idle", CurrentCommand: "claude", ScreenProgress: progress("changed", -5*time.Second, -5*time.Second)},
			wantState:        "conflict",
			wantSeverity:     "attention_stale",
			wantEvidence:     "observed",
			wantFreshness:    "conflict",
			wantAge:          5,
			wantReasonSubstr: "pane state is idle",
		},
		{
			name:             "changed screen with stale pane is conflict",
			node:             status.NodeStatus{Name: "worker", PaneState: "stale", CurrentCommand: "claude", ScreenProgress: progress("changed", -5*time.Second, -5*time.Second)},
			wantState:        "conflict",
			wantSeverity:     "attention_stale",
			wantEvidence:     "observed",
			wantFreshness:    "conflict",
			wantAge:          5,
			wantReasonSubstr: "pane state is stale",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveNodeLocalStatusAt(tt.node, now)
			if got.State != tt.wantState || got.Severity != tt.wantSeverity || got.EvidenceLevel != tt.wantEvidence || got.Freshness != tt.wantFreshness {
				t.Fatalf("node_local = %#v, want state/severity/evidence/freshness %q/%q/%q/%q", got, tt.wantState, tt.wantSeverity, tt.wantEvidence, tt.wantFreshness)
			}
			if got.AgeSeconds != tt.wantAge {
				t.Fatalf("AgeSeconds = %d, want %d (%#v)", got.AgeSeconds, tt.wantAge, got)
			}
			if got.UnchangedSeconds != tt.wantUnchanged {
				t.Fatalf("UnchangedSeconds = %d, want %d (%#v)", got.UnchangedSeconds, tt.wantUnchanged, got)
			}
			if tt.wantReasonSubstr != "" && !strings.Contains(got.Reason, tt.wantReasonSubstr) {
				t.Fatalf("Reason = %q, want substring %q", got.Reason, tt.wantReasonSubstr)
			}
		})
	}
}

func TestSessionStatusBuildsDefaultCompactAfterNodeLocalEnrichment(t *testing.T) {
	now := time.Date(2026, time.April, 14, 0, 10, 0, 0, time.UTC)
	progress := func(state string, changeDelta, captureDelta time.Duration) *status.ScreenProgressEvidence {
		captureAt := now.Add(captureDelta)
		changeAt := now.Add(changeDelta)
		result := &status.ScreenProgressEvidence{
			EvidenceState:        state,
			LastCaptureAt:        captureAt.Format(time.RFC3339Nano),
			LastScreenChangeAt:   changeAt.Format(time.RFC3339Nano),
			ScreenFingerprint:    "00000011",
			StaleDurationSeconds: int(captureAt.Sub(changeAt).Seconds()),
		}
		if result.StaleDurationSeconds < 0 {
			result.StaleDurationSeconds = 0
		}
		return result
	}

	tests := []struct {
		name             string
		paneState        string
		currentCommand   string
		screenProgress   *status.ScreenProgressEvidence
		wantCompact      string
		wantNodeLocal    string
		wantSeverity     string
		wantNodeSeverity string
		blockedReports   []projection.BlockedReport
		wantFlowState    string
		wantCompactSev   string
		wantOneline      string
		wantReasonSubstr string
	}{
		{
			name:           "fresh work is blue not legacy green",
			paneState:      "active",
			currentCommand: "claude",
			screenProgress: progress("changed", -5*time.Second, -5*time.Second),
			wantCompact:    "🔵",
			wantNodeLocal:  "working",
			wantSeverity:   "working",
			wantOneline:    "🔵",
		},
		{
			name:             "missing progress is neutral not green",
			paneState:        "active",
			currentCommand:   "claude",
			screenProgress:   missingScreenProgressEvidence(),
			wantCompact:      "⚫",
			wantNodeLocal:    "unknown",
			wantSeverity:     "ok",
			wantOneline:      "⚫",
			wantReasonSubstr: "missing",
		},
		{
			name:             "malformed progress is neutral not green",
			paneState:        "active",
			currentCommand:   "claude",
			screenProgress:   screenProgressEvidence("active", "bad", now.Format(time.RFC3339Nano), "00000011"),
			wantCompact:      "⚫",
			wantNodeLocal:    "unknown",
			wantSeverity:     "ok",
			wantOneline:      "⚫",
			wantReasonSubstr: "malformed",
		},
		{
			name:             "stale capture is red",
			paneState:        "active",
			currentCommand:   "claude",
			screenProgress:   progress("changed", -4*time.Minute, -4*time.Minute),
			wantCompact:      "🔴",
			wantNodeLocal:    "stale",
			wantSeverity:     "attention_stale",
			wantOneline:      "🔴",
			wantReasonSubstr: "capture is stale",
		},
		{
			name:             "sub-second future is red conflict",
			paneState:        "active",
			currentCommand:   "claude",
			screenProgress:   progress("changed", 500*time.Millisecond, 500*time.Millisecond),
			wantCompact:      "🔴",
			wantNodeLocal:    "conflict",
			wantSeverity:     "attention_stale",
			wantOneline:      "🔴",
			wantReasonSubstr: "future",
		},
		{
			name:             "shell command contradiction is red not skipped green",
			paneState:        "active",
			currentCommand:   "bash",
			screenProgress:   progress("changed", -5*time.Second, -5*time.Second),
			wantCompact:      "🔴",
			wantNodeLocal:    "conflict",
			wantSeverity:     "attention_stale",
			wantOneline:      "🔴",
			wantReasonSubstr: "current command is a shell",
		},
		{
			name:             "idle pane with changed screen is red conflict",
			paneState:        "idle",
			currentCommand:   "claude",
			screenProgress:   progress("changed", -5*time.Second, -5*time.Second),
			wantCompact:      "🔴",
			wantNodeLocal:    "conflict",
			wantSeverity:     "attention_stale",
			wantOneline:      "🔴",
			wantReasonSubstr: "pane state is idle",
		},
		{
			name:           "blocked flow beats unknown local evidence",
			paneState:      "active",
			currentCommand: "claude",
			screenProgress: missingScreenProgressEvidence(),
			blockedReports: []projection.BlockedReport{
				{
					Node:            "worker",
					MessageID:       "blocked-message",
					BlockedReportID: "blocked-report-001",
					EvidenceLevel:   "proven",
					EvidenceSource:  "blocked_report_file",
					Reason:          "waiting on owner",
				},
			},
			wantCompact:      "🔴",
			wantNodeLocal:    "unknown",
			wantSeverity:     "ok",
			wantNodeSeverity: "blocked",
			wantFlowState:    "blocked",
			wantCompactSev:   "blocked:node=worker:blocked_report=blocked-report-001",
			wantOneline:      "🔴",
			wantReasonSubstr: "missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blockedByNode := map[string][]projection.BlockedReport{}
			if len(tt.blockedReports) > 0 {
				blockedByNode["worker"] = tt.blockedReports
			}
			health := buildSessionStatusSnapshot(sessionStatusInputs{
				contextID:        "ctx",
				sessionName:      "review",
				cfg:              config.DefaultConfig(),
				orderedEdgeNodes: []string{"worker"},
				edgeNodeRank:     map[string]int{"worker": 0},
				nodes: map[string]discovery.NodeInfo{
					"worker": {
						PaneID:      "%11",
						SessionName: "review",
					},
				},
				paneActivity: map[string]paneActivityEvidence{
					"%11": {
						Status:         tt.paneState,
						ScreenProgress: tt.screenProgress,
					},
				},
				panes: []sessionPane{
					{
						windowIndex:    "0",
						windowOrder:    0,
						paneOrder:      0,
						paneID:         "%11",
						title:          "worker",
						currentCommand: tt.currentCommand,
					},
				},
				inboxCounts:   map[string]int{},
				delivery:      &status.DeliveryStatus{State: "ok", Severity: "ok", EvidenceLevel: "proven"},
				blockedByNode: blockedByNode,
				now:           now,
			})

			if health.Compact != tt.wantCompact {
				t.Fatalf("Compact = %q, want %q; health=%#v", health.Compact, tt.wantCompact, health)
			}
			if got := formatSessionStatusOneline(health); got != tt.wantOneline {
				t.Fatalf("formatSessionStatusOneline(...) = %q, want %q", got, tt.wantOneline)
			}
			if len(health.Nodes) != 1 || health.Nodes[0].NodeLocal == nil {
				t.Fatalf("node_local missing in health: %#v", health.Nodes)
			}
			local := health.Nodes[0].NodeLocal
			if local.State != tt.wantNodeLocal || local.Severity != tt.wantSeverity {
				t.Fatalf("node_local = %#v, want state/severity %q/%q", local, tt.wantNodeLocal, tt.wantSeverity)
			}
			node := health.Nodes[0]
			wantNodeSeverity := tt.wantNodeSeverity
			if wantNodeSeverity == "" {
				wantNodeSeverity = tt.wantSeverity
			}
			if node.Severity != wantNodeSeverity {
				t.Fatalf("node severity = %q, want %q; node=%#v", node.Severity, wantNodeSeverity, node)
			}
			if tt.wantFlowState != "" {
				if node.Flow == nil || node.Flow.State != tt.wantFlowState {
					t.Fatalf("flow = %#v, want state %q", node.Flow, tt.wantFlowState)
				}
			}
			if tt.wantCompactSev != "" && health.CompactSeverity != tt.wantCompactSev {
				t.Fatalf("CompactSeverity = %q, want %q", health.CompactSeverity, tt.wantCompactSev)
			}
			if tt.wantReasonSubstr != "" && !strings.Contains(local.Reason, tt.wantReasonSubstr) {
				t.Fatalf("Reason = %q, want substring %q", local.Reason, tt.wantReasonSubstr)
			}
		})
	}
}

func TestSessionStatusExposesMissingAndStaleScreenProgressEvidence(t *testing.T) {
	fixture := writeSessionStatusProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "stale"},
		nil,
		nil,
	)
	writeSessionStatusPaneActivity(t, fixture, `{
  "%11": "active",
  "%12": {"status":"stale","lastChangeAt":"2026-04-14T00:02:00Z","lastCaptureAt":"2026-04-14T00:02:05Z","screenFingerprint":"00000012"}
}`)

	health, err := collectLiveSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectLiveSessionStatus() error = %v", err)
	}

	nodeByName := map[string]status.NodeStatus{}
	for _, node := range health.Nodes {
		nodeByName[node.Name] = node
	}

	worker := nodeByName["worker"].ScreenProgress
	if worker == nil {
		t.Fatal("worker ScreenProgress is nil, want missing evidence")
		return
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
		return
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

func TestSessionStatusUsesReplyObligationProjection(t *testing.T) {
	fixture := writeSessionStatusProjectionFixture(
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
	content := sessionStatusObligationContent("critic", "worker", "m1.md", "required", "", "please review")
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionPostConsumedEventType, "m1.md", "critic", "worker", content, now.Add(time.Second))
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionDeliveredEventType, "m1.md", "critic", "worker", content, now.Add(2*time.Second))
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionReadEventType, "m1.md", "critic", "worker", content, now.Add(3*time.Second))

	health, err := collectLiveSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectLiveSessionStatus() error = %v", err)
	}

	nodeByName := map[string]status.NodeStatus{}
	for _, node := range health.Nodes {
		nodeByName[node.Name] = node
	}
	if nodeByName["worker"].VisibleState != "pending" || nodeByName["worker"].InputRequiredCount != 1 {
		t.Fatalf("worker health = %#v, want pending input_required=1", nodeByName["worker"])
	}
	if nodeByName["critic"].VisibleState != "waiting" || nodeByName["critic"].WaitingOnInputCount != 1 {
		t.Fatalf("critic health = %#v, want waiting waiting_on_input=1", nodeByName["critic"])
	}
	if health.Compact != "🔷🟡" {
		t.Fatalf("health.Compact = %q, want 🔷🟡", health.Compact)
	}
}

func TestSessionStatusAddsSchemaV4SeverityForInputRequests(t *testing.T) {
	fixture := writeSessionStatusProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		map[string]int{"worker": 1},
		nil,
	)
	sessionDir := filepath.Join(fixture.baseDir, fixture.contextID, fixture.sessionName)
	now := time.Date(2026, time.April, 14, 5, 5, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, fixture.contextID, fixture.sessionName, 212, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	content := sessionStatusMessageContent("critic", "worker", "m1.md", map[string]string{
		"replyPolicy":      "required",
		"input_request_id": "ireq_123",
	}, "please review")
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionPostConsumedEventType, "m1.md", "critic", "worker", content, now.Add(time.Second))
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionDeliveredEventType, "m1.md", "critic", "worker", content, now.Add(2*time.Second))
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionReadEventType, "m1.md", "critic", "worker", content, now.Add(3*time.Second))

	health, err := collectLiveSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectLiveSessionStatus() error = %v", err)
	}

	if health.SchemaVersion != 5 {
		t.Fatalf("SchemaVersion = %d, want 5", health.SchemaVersion)
	}
	if health.VisibleState != "pending" || health.Compact != "🔷🟡" {
		t.Fatalf("legacy visible fields changed: visible_state=%q compact=%q", health.VisibleState, health.Compact)
	}
	if health.Severity != "needs_action" {
		t.Fatalf("Severity = %q, want needs_action", health.Severity)
	}
	if health.SeveritySource != "node.flow" {
		t.Fatalf("SeveritySource = %q, want node.flow", health.SeveritySource)
	}
	if health.CompactSeverity != "needs_action:node=worker:input_required=1" {
		t.Fatalf("CompactSeverity = %q, want needs_action token", health.CompactSeverity)
	}

	nodeByName := map[string]status.NodeStatus{}
	for _, node := range health.Nodes {
		nodeByName[node.Name] = node
	}
	if got := nodeByName["worker"].Flow.State; got != "needs_action" {
		t.Fatalf("worker flow state = %q, want needs_action", got)
	}
	if got := nodeByName["worker"].Flow.InputRequests.InputRequiredCount; got != 1 {
		t.Fatalf("worker input_required_count = %d, want 1", got)
	}
	if got := nodeByName["worker"].Flow.InputRequests.InputRequired; len(got) != 1 {
		t.Fatalf("worker input_required details = %#v, want one detail", got)
	} else {
		detail := got[0]
		if detail.Direction != "inbound" || detail.MessageID != "m1.md" || detail.InputRequestID != "ireq_123" || detail.Sender != "critic" || detail.Recipient != "worker" || detail.ReplyPolicy != "required" {
			t.Fatalf("worker input_required detail = %#v, want public input-request identifiers", detail)
		}
		if detail.OpenedAt != now.Add(2*time.Second).Format(time.RFC3339Nano) || detail.OpenedAtSource != projection.MailboxProjectionDeliveredEventType {
			t.Fatalf("worker input_required opened evidence = %#v, want delivered timestamp/source", detail)
		}
		if detail.OpenedEventID == "" {
			t.Fatalf("worker input_required opened_event_id is empty, want durable journal event id")
		}
		if detail.ReadAt != now.Add(3*time.Second).Format(time.RFC3339Nano) {
			t.Fatalf("worker input_required read_at = %q, want recipient read timestamp", detail.ReadAt)
		}
		if detail.ReadEventID == "" {
			t.Fatalf("worker input_required read_event_id is empty, want durable journal event id")
		}
	}
	if got := nodeByName["critic"].Flow.State; got != "expected_wait" {
		t.Fatalf("critic flow state = %q, want expected_wait", got)
	}
	if got := nodeByName["critic"].Flow.InputRequests.WaitingOnInput; len(got) != 1 {
		t.Fatalf("critic waiting_on_input details = %#v, want one detail", got)
	} else {
		detail := got[0]
		if detail.Direction != "outbound" || detail.MessageID != "m1.md" || detail.InputRequestID != "ireq_123" || detail.Sender != "critic" || detail.Recipient != "worker" || detail.ReplyPolicy != "required" {
			t.Fatalf("critic waiting_on_input detail = %#v, want public input-request identifiers", detail)
		}
		if detail.OpenedAt != now.Add(time.Second).Format(time.RFC3339Nano) || detail.OpenedAtSource != projection.MailboxProjectionPostConsumedEventType {
			t.Fatalf("critic waiting_on_input opened evidence = %#v, want post-consumed timestamp/source", detail)
		}
		if detail.OpenedEventID == "" {
			t.Fatalf("critic waiting_on_input opened_event_id is empty, want durable journal event id")
		}
		if detail.ReadAt != now.Add(3*time.Second).Format(time.RFC3339Nano) {
			t.Fatalf("critic waiting_on_input read_at = %q, want recipient read timestamp", detail.ReadAt)
		}
		if detail.ReadEventID == "" {
			t.Fatalf("critic waiting_on_input read_event_id is empty, want durable journal event id")
		}
	}
}

func TestCollectSessionDeliveryClassifiesQueuedStuckAndFailure(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "ctx", "review")
	postDir := filepath.Join(sessionDir, "post")
	deadLetterDir := filepath.Join(sessionDir, "dead-letter")
	if err := os.MkdirAll(postDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(post): %v", err)
	}
	if err := os.MkdirAll(deadLetterDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(dead-letter): %v", err)
	}
	postPath := filepath.Join(postDir, "m1.md")
	if err := os.WriteFile(postPath, []byte("body"), 0o644); err != nil {
		t.Fatalf("WriteFile(post): %v", err)
	}
	now := time.Date(2026, time.April, 14, 5, 10, 0, 0, time.UTC)
	old := now.Add(-181 * time.Second)
	if err := os.Chtimes(postPath, old, old); err != nil {
		t.Fatalf("Chtimes(post): %v", err)
	}

	stuck := collectSessionDelivery(sessionDir, status.SessionQueues{PostCount: 1}, now)
	if stuck.State != "delivery_stuck" || stuck.Severity != "delivery_stuck" {
		t.Fatalf("stuck delivery = %#v, want delivery_stuck", stuck)
	}
	if stuck.StuckAfterSeconds != 180 || stuck.OldestPostAgeSeconds != 181 {
		t.Fatalf("stuck threshold/age = %d/%d, want 180/181", stuck.StuckAfterSeconds, stuck.OldestPostAgeSeconds)
	}

	if err := os.WriteFile(filepath.Join(deadLetterDir, "m1-dl-test.md"), []byte("dead"), 0o644); err != nil {
		t.Fatalf("WriteFile(dead-letter): %v", err)
	}
	failed := collectSessionDelivery(sessionDir, status.SessionQueues{PostCount: 1, DeadLetterCount: 1}, now)
	if failed.State != "delivery_failure" || failed.Severity != "delivery_failure" {
		t.Fatalf("failed delivery = %#v, want delivery_failure", failed)
	}
	if failed.DeadLetterCount != 1 {
		t.Fatalf("DeadLetterCount = %d, want 1", failed.DeadLetterCount)
	}
}

func TestSessionStatusReportsInferredBlockedFirstLineAndClearsOnDone(t *testing.T) {
	fixture := writeSessionStatusProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		nil,
		nil,
	)
	sessionDir := filepath.Join(fixture.baseDir, fixture.contextID, fixture.sessionName)
	now := time.Date(2026, time.April, 14, 5, 15, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, fixture.contextID, fixture.sessionName, 222, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	blockedContent := sessionStatusMessageContent("worker", "critic", "blocked.md", nil, "BLOCKED: waiting on access")
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionDeliveredEventType, "blocked.md", "worker", "critic", blockedContent, now.Add(time.Second))

	blocked, err := collectLiveSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectLiveSessionStatus(blocked) error = %v", err)
	}
	nodeByName := map[string]status.NodeStatus{}
	for _, node := range blocked.Nodes {
		nodeByName[node.Name] = node
	}
	if got := nodeByName["worker"].Flow.State; got != "blocked" {
		t.Fatalf("worker flow state = %q, want blocked", got)
	}
	if got := nodeByName["worker"].Flow.EvidenceLevel; got != "inferred" {
		t.Fatalf("worker blocked evidence = %q, want inferred", got)
	}
	if blocked.CompactSeverity != "blocked?:node=worker" {
		t.Fatalf("CompactSeverity = %q, want inferred blocked token", blocked.CompactSeverity)
	}

	doneContent := sessionStatusMessageContent("worker", "critic", "done.md", nil, "DONE: access granted")
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionDeliveredEventType, "done.md", "worker", "critic", doneContent, now.Add(2*time.Second))
	cleared, err := collectLiveSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectLiveSessionStatus(cleared) error = %v", err)
	}
	nodeByName = map[string]status.NodeStatus{}
	for _, node := range cleared.Nodes {
		nodeByName[node.Name] = node
	}
	if got := nodeByName["worker"].Flow.State; got != "idle" {
		t.Fatalf("worker flow state after DONE = %q, want idle", got)
	}
}

func TestSessionStatusExposesConventionMeterPerNode(t *testing.T) {
	fixture := writeSessionStatusProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		nil,
		nil,
	)
	sessionDir := filepath.Join(fixture.baseDir, fixture.contextID, fixture.sessionName)
	now := time.Date(2026, time.April, 14, 5, 18, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, fixture.contextID, fixture.sessionName, 333, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	content := sessionStatusMessageContent("worker", "critic", "done.md", nil, "DONE: implemented")
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionDeliveredEventType, "done.md", "worker", "critic", content, now.Add(time.Second))

	health, err := collectLiveSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectLiveSessionStatus() error = %v", err)
	}

	nodeByName := map[string]status.NodeStatus{}
	for _, node := range health.Nodes {
		nodeByName[node.Name] = node
	}
	meter := nodeByName["worker"].ConventionMeter
	if meter == nil {
		t.Fatal("worker convention_meter is nil, want projected meter")
	}
	if meter.CheckedMessages != 1 || meter.ViolationCount != 1 || meter.MissingEvidenceCount != 1 || meter.MissingReplyReferenceCount != 1 {
		t.Fatalf("worker convention_meter = %#v, want missing evidence and reply reference", meter)
	}
}

func TestSessionStatusPrefersLiveRecomputeOverStaleSnapshot(t *testing.T) {
	fixture := writeSessionStatusProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		map[string]int{},
		nil,
	)
	sessionDir := filepath.Join(fixture.baseDir, fixture.contextID, fixture.sessionName)

	staleSnapshot, err := collectLiveSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectLiveSessionStatus() stale snapshot error = %v", err)
	}
	if staleSnapshot.VisibleState != "ready" {
		t.Fatalf("staleSnapshot.VisibleState = %q, want ready", staleSnapshot.VisibleState)
	}

	now := time.Date(2026, time.April, 14, 5, 10, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, fixture.contextID, fixture.sessionName, 404, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	if _, err := writer.AppendEvent(projection.SessionStatusSnapshotEventType, journal.VisibilityControlPlaneOnly, staleSnapshot, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendEvent(session status snapshot): %v", err)
	}
	content := sessionStatusObligationContent("critic", "worker", "m1.md", "required", "", "please review")
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionPostConsumedEventType, "m1.md", "critic", "worker", content, now.Add(2*time.Second))
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionDeliveredEventType, "m1.md", "critic", "worker", content, now.Add(3*time.Second))

	health, err := collectSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectSessionStatus() error = %v", err)
	}

	nodeByName := map[string]status.NodeStatus{}
	for _, node := range health.Nodes {
		nodeByName[node.Name] = node
	}
	if nodeByName["worker"].VisibleState != "pending" || nodeByName["worker"].InputRequiredCount != 1 {
		t.Fatalf("worker health = %#v, want pending input_required=1", nodeByName["worker"])
	}
	if nodeByName["critic"].VisibleState != "waiting" || nodeByName["critic"].WaitingOnInputCount != 1 {
		t.Fatalf("critic health = %#v, want waiting waiting_on_input=1", nodeByName["critic"])
	}
}

func TestSessionStatusFallsBackForUnclassifiedLegacyUnread(t *testing.T) {
	fixture := writeSessionStatusProjectionFixture(
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
	content := sessionStatusObligationContent("worker", "critic", "info.md", "none", "", "FYI")
	appendSessionStatusObligationEvent(t, writer, projection.MailboxProjectionDeliveredEventType, "info.md", "worker", "critic", content, now.Add(2*time.Second))

	health, err := collectSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectSessionStatus() error = %v", err)
	}

	nodeByName := map[string]status.NodeStatus{}
	for _, node := range health.Nodes {
		nodeByName[node.Name] = node
	}
	if nodeByName["worker"].VisibleState != "pending" {
		t.Fatalf("worker health = %#v, want pending from unread fallback", nodeByName["worker"])
	}
	if nodeByName["worker"].Flow.State != "needs_action" || nodeByName["worker"].Flow.EvidenceLevel != "inferred" {
		t.Fatalf("worker flow = %#v, want inferred needs_action from unread fallback", nodeByName["worker"].Flow)
	}
	if nodeByName["critic"].VisibleState != "ready" || nodeByName["critic"].InfoUnreadCount != 1 {
		t.Fatalf("critic health = %#v, want ready info_unread=1", nodeByName["critic"])
	}
}

func TestSessionStatusProjectionRebuildsWithoutLiveArtifacts(t *testing.T) {
	fixture := writeSessionStatusProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "idle"},
		map[string]int{"worker": 1},
		nil,
	)

	legacy, err := collectLiveSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectLiveSessionStatus() error = %v", err)
	}

	appendSessionStatusSnapshot(t, fixture, legacy)
	removeLiveSessionStatusArtifacts(t, fixture)
	installSessionStatusProjectionBrokenTmux(t)

	projected, err := collectSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectSessionStatus() error = %v", err)
	}

	assertSessionStatusParity(t, legacy, projected)
}

func TestSessionStatusProjectionReplaysLegacyHealthArchiveAsV4(t *testing.T) {
	fixture := writeSessionStatusProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "idle"},
		map[string]int{"worker": 1},
		nil,
	)

	live, err := collectLiveSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectLiveSessionStatus() error = %v", err)
	}
	legacyArchive := live
	legacyArchive.SchemaVersion = 3

	appendLegacySessionStatusSnapshot(t, fixture, legacyArchive)
	removeLiveSessionStatusArtifacts(t, fixture)
	installSessionStatusProjectionBrokenTmux(t)

	projected, err := collectSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectSessionStatus() error = %v", err)
	}

	if projected.SchemaVersion != status.SchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", projected.SchemaVersion, status.SchemaVersion)
	}
	assertSessionStatusParity(t, live, projected)
}

func TestSessionStatusProjectionPrefersStatusSnapshotOverLegacyHealthArchive(t *testing.T) {
	fixture := writeSessionStatusProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "idle"},
		map[string]int{"worker": 1},
		nil,
	)

	current, err := collectLiveSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectLiveSessionStatus() error = %v", err)
	}
	legacyArchive := current
	legacyArchive.SchemaVersion = 3
	legacyArchive.VisibleState = "stale"
	legacyArchive.Compact = "🔴"

	appendSessionStatusSnapshot(t, fixture, current)
	appendLegacySessionStatusSnapshot(t, fixture, legacyArchive)
	removeLiveSessionStatusArtifacts(t, fixture)
	installSessionStatusProjectionBrokenTmux(t)

	projected, err := collectSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectSessionStatus() error = %v", err)
	}

	assertSessionStatusParity(t, current, projected)
}

func sessionStatusObligationContent(from, to, messageID, replyPolicy, replyTo, body string) string {
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

func sessionStatusMessageContent(from, to, messageID string, fields map[string]string, body string) string {
	var builder strings.Builder
	builder.WriteString("---\nparams:\n")
	builder.WriteString("  from: " + from + "\n")
	builder.WriteString("  to: " + to + "\n")
	builder.WriteString("  messageId: " + messageID + "\n")
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		builder.WriteString("  " + key + ": " + fields[key] + "\n")
	}
	builder.WriteString("---\n\n")
	builder.WriteString(body)
	builder.WriteString("\n")
	return builder.String()
}

func appendSessionStatusObligationEvent(t *testing.T, writer *journal.Writer, eventType, messageID, from, to, content string, now time.Time) {
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

func TestGetStatusOnelineProjectionRebuildsWithoutLiveTopology(t *testing.T) {
	fixture := writeSessionStatusProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		map[string]int{"critic": 1},
		nil,
	)

	legacy, _, ok, err := collectAllLiveSessionStatus(fixture.contextID, "", fixture.configPath)
	if err != nil {
		t.Fatalf("collectAllLiveSessionStatus() error = %v", err)
	}
	if !ok {
		t.Fatal("collectAllLiveSessionStatus() ok = false, want true")
	}

	appendSessionStatusSnapshot(t, fixture, legacy.Sessions[0])
	removeLiveSessionStatusArtifacts(t, fixture)
	installSessionStatusProjectionListSessionsOnlyTmux(t, fixture.sessionName)

	var stdout strings.Builder
	if err := RunGetSessionStatusOneline(&stdout, []string{"--context-id", fixture.contextID, "--config", fixture.configPath}); err != nil {
		t.Fatalf("RunGetSessionStatusOneline() error = %v", err)
	}

	if got, want := strings.TrimSpace(stdout.String()), formatAllSessionStatusOneline(legacy); got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
}

func TestTUIProjectionRebuildsWithoutLiveArtifacts(t *testing.T) {
	fixture := writeSessionStatusProjectionFixture(
		t,
		map[string]string{"worker": "active", "critic": "active"},
		map[string]int{"worker": 1},
		nil,
	)

	legacy, err := collectLiveSessionStatus(fixture.baseDir, fixture.contextID, fixture.sessionName, fixture.cfg)
	if err != nil {
		t.Fatalf("collectLiveSessionStatus() error = %v", err)
	}

	appendSessionStatusSnapshot(t, fixture, legacy)
	removeLiveSessionStatusArtifacts(t, fixture)
	installSessionStatusProjectionBrokenTmux(t)

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
	statusEvent := <-tuiEvents
	if statusEvent.Type != "session_status_update" {
		t.Fatalf("health event type = %q, want session_status_update", statusEvent.Type)
	}

	got, ok := statusEvent.Details["status"].(status.SessionStatus)
	if !ok {
		t.Fatalf("health payload type = %T, want status.SessionStatus", statusEvent.Details["status"])
	}
	assertSessionStatusParity(t, legacy, got)
}

func assertSessionStatusParity(t *testing.T, legacy, projected status.SessionStatus) {
	t.Helper()

	if reflect.DeepEqual(legacy, projected) {
		return
	}

	legacyJSON, legacyErr := json.MarshalIndent(legacy, "", "  ")
	projectedJSON, projectedErr := json.MarshalIndent(projected, "", "  ")
	if legacyErr != nil || projectedErr != nil {
		t.Fatalf("parity mismatch and JSON marshal failed: legacyErr=%v projectedErr=%v", legacyErr, projectedErr)
	}

	t.Fatalf("session status mismatch:\nlegacy:\n%s\nprojected:\n%s", string(legacyJSON), string(projectedJSON))
}

func writeSessionStatusProjectionFixture(t *testing.T, paneStates map[string]string, liveInbox map[string]int, journalSteps []projectionJournalStep) sessionStatusProjectionFixture {
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

	installSessionStatusProjectionTmux(t, contextID, sessionName)

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

	return sessionStatusProjectionFixture{
		baseDir:     tmpDir,
		contextID:   contextID,
		sessionName: sessionName,
		configPath:  configPath,
		cfg:         cfg,
	}
}

func writeSessionStatusPaneActivity(t *testing.T, fixture sessionStatusProjectionFixture, content string) {
	t.Helper()

	if err := os.WriteFile(
		filepath.Join(fixture.baseDir, fixture.contextID, "pane-activity.json"),
		[]byte(content),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(pane-activity.json): %v", err)
	}
}

func installSessionStatusProjectionTmux(t *testing.T, contextID, sessionName string) {
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

func installSessionStatusProjectionBrokenTmux(t *testing.T) {
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

func installSessionStatusProjectionListSessionsOnlyTmux(t *testing.T, sessionName string) {
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

func appendSessionStatusSnapshot(t *testing.T, fixture sessionStatusProjectionFixture, health status.SessionStatus) {
	t.Helper()
	appendSessionStatusSnapshotEvent(t, fixture, projection.SessionStatusSnapshotEventType, health)
}

func appendLegacySessionStatusSnapshot(t *testing.T, fixture sessionStatusProjectionFixture, health status.SessionStatus) {
	t.Helper()
	appendSessionStatusSnapshotEvent(t, fixture, projection.LegacySessionHealthSnapshotEventType, health)
}

func appendSessionStatusSnapshotEvent(t *testing.T, fixture sessionStatusProjectionFixture, eventType string, health status.SessionStatus) {
	t.Helper()

	sessionDir := filepath.Join(fixture.baseDir, fixture.contextID, fixture.sessionName)
	now := time.Date(2026, time.April, 14, 5, 0, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, fixture.contextID, fixture.sessionName, 303, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter(snapshot) error = %v", err)
	}
	if _, err := writer.AppendEvent(
		eventType,
		journal.VisibilityControlPlaneOnly,
		health,
		now.Add(time.Second),
	); err != nil {
		t.Fatalf("AppendEvent(%s): %v", eventType, err)
	}
}

func appendAllSessionStatusSnapshots(t *testing.T, baseDir, contextID string, sessions []status.SessionStatus) {
	t.Helper()

	for _, health := range sessions {
		appendSessionStatusSnapshot(t, sessionStatusProjectionFixture{
			baseDir:     baseDir,
			contextID:   contextID,
			sessionName: health.SessionName,
		}, health)
	}
}

func removeLiveSessionStatusArtifacts(t *testing.T, fixture sessionStatusProjectionFixture) {
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
