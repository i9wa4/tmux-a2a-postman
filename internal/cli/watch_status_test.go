package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

func TestRunWatchStatus_NoActiveDaemonReturnsClearErrorAndNoSideEffects(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)

	var stdout bytes.Buffer
	err := RunWatchStatus(&stdout, []string{"--interval", "10ms"})
	if err == nil {
		t.Fatal("RunWatchStatus() error = nil, want no active daemon error")
	}
	if !strings.Contains(err.Error(), "no active postman daemon found") {
		t.Fatalf("RunWatchStatus() error = %q, want no active daemon wording", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}

	for _, path := range []string{
		filepath.Join(tmpDir, "lock"),
		filepath.Join(tmpDir, "postman.pid"),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("unexpected side-effect path %s exists or stat failed: %v", path, statErr)
		}
	}
}

func TestRunWatchStatusTextAutoDisablesClearAndColorForNonTTY(t *testing.T) {
	fixedNow := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	snapshot := status.AllSessionStatus{
		SchemaVersion: status.SchemaVersion,
		ContextID:     "ctx-watch",
		DaemonOwner: &status.DaemonOwner{
			ContextID:   "ctx-watch",
			SessionName: "daemon",
		},
		Sessions: []status.SessionStatus{
			{
				SessionName:     "main",
				NodeCount:       2,
				VisibleState:    "pending",
				Severity:        "needs_action",
				Compact:         "🔷🟢",
				CompactSeverity: "needs_action:node=worker:input_required=1",
				Queues: status.SessionQueues{
					PostCount:       2,
					InboxCount:      3,
					DeadLetterCount: 1,
				},
				Delivery: &status.DeliveryStatus{
					State:                "delivery_stuck",
					Severity:             "delivery_stuck",
					OldestPostAgeSeconds: 240,
				},
				Nodes: []status.NodeStatus{
					{
						Name:                "worker",
						VisibleState:        "pending",
						Severity:            "needs_action",
						InboxCount:          2,
						InputRequiredCount:  1,
						WaitingOnInputCount: 0,
						ScreenProgress:      &status.ScreenProgressEvidence{EvidenceState: "missing"},
						NodeLocal:           &status.NodeLocalStatus{State: "unknown"},
						Flow: &status.NodeFlowStatus{
							State: "needs_action",
							Blocked: status.BlockedState{
								State:     "open",
								OpenCount: 1,
							},
						},
					},
					{
						Name:                "critic",
						VisibleState:        "waiting",
						Severity:            "expected_wait",
						InboxCount:          0,
						WaitingOnInputCount: 1,
						ScreenProgress:      &status.ScreenProgressEvidence{EvidenceState: "unchanged"},
						NodeLocal:           &status.NodeLocalStatus{State: "live"},
						Flow:                &status.NodeFlowStatus{State: "waiting"},
					},
				},
			},
		},
	}

	var stdout bytes.Buffer
	err := runWatchStatus(context.Background(), &stdout, watchStatusRunOptions{
		Interval:      time.Hour,
		Format:        "text",
		Severity:      true,
		Collector:     func() (status.AllSessionStatus, error) { return snapshot, nil },
		Now:           func() time.Time { return fixedNow },
		IsTTY:         func() bool { return false },
		MaxIterations: 1,
	})
	if err != nil {
		t.Fatalf("runWatchStatus: %v", err)
	}

	got := stdout.String()
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("text output contains ANSI sequence for non-TTY: %q", got)
	}
	for _, want := range []string{
		"postman watch-status observed=2026-06-01T12:00:00Z context=ctx-watch daemon_owner=ctx-watch/daemon sessions=1",
		"[0] main token=needs_action:node=worker:input_required=1 state=pending severity=needs_action nodes=2 queues=post:2 inbox:3 dead_letter:1 delivery=delivery_stuck oldest_post_age=240s",
		"worker state=pending severity=needs_action inbox=2 input_required=1 waiting_on_input=0 blocked=1 flow=needs_action local=unknown screen=missing",
		"critic state=waiting severity=expected_wait inbox=0 input_required=0 waiting_on_input=1 blocked=0 flow=waiting local=live screen=unchanged",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("watch text missing %q:\n%s", want, got)
		}
	}
}

func TestRunWatchStatusJSONLWritesOneStatusSnapshotPerRefresh(t *testing.T) {
	snapshot := status.AllSessionStatus{
		SchemaVersion: status.SchemaVersion,
		ContextID:     "ctx-jsonl",
		DaemonOwner:   &status.DaemonOwner{ContextID: "ctx-jsonl", SessionName: "daemon"},
		Sessions: []status.SessionStatus{{
			SessionName:  "main",
			VisibleState: "ready",
			Compact:      "🟢",
			Nodes:        []status.NodeStatus{},
			Windows:      []status.SessionWindow{},
		}},
	}

	var stdout bytes.Buffer
	err := runWatchStatus(context.Background(), &stdout, watchStatusRunOptions{
		Interval:      time.Hour,
		Format:        "jsonl",
		Collector:     func() (status.AllSessionStatus, error) { return snapshot, nil },
		IsTTY:         func() bool { return true },
		MaxIterations: 1,
	})
	if err != nil {
		t.Fatalf("runWatchStatus(jsonl): %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("jsonl line count = %d, want 1; output=%q", len(lines), stdout.String())
	}
	if strings.Contains(lines[0], "\x1b[") {
		t.Fatalf("jsonl contains ANSI sequence: %q", lines[0])
	}
	var got status.AllSessionStatus
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", lines[0], err)
	}
	if got.ContextID != "ctx-jsonl" || len(got.Sessions) != 1 || got.Sessions[0].SessionName != "main" {
		t.Fatalf("decoded jsonl snapshot = %#v", got)
	}
}

func TestRunWatchStatusRejectsInvalidFormatAndInterval(t *testing.T) {
	collector := func() (status.AllSessionStatus, error) {
		return status.AllSessionStatus{}, nil
	}

	var stdout bytes.Buffer
	if err := runWatchStatus(context.Background(), &stdout, watchStatusRunOptions{
		Interval:  time.Second,
		Format:    "yaml",
		Collector: collector,
	}); err == nil || !strings.Contains(err.Error(), "--format must be text or jsonl") {
		t.Fatalf("invalid format error = %v, want format validation", err)
	}

	if err := runWatchStatus(context.Background(), &stdout, watchStatusRunOptions{
		Interval:  0,
		Format:    "text",
		Collector: collector,
	}); err == nil || !strings.Contains(err.Error(), "--interval must be greater than zero") {
		t.Fatalf("invalid interval error = %v, want interval validation", err)
	}
}
