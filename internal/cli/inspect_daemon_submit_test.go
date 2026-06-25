package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

func TestRunInspectDaemonSubmitReportsRequestAndLateResponseWithoutSensitiveFields(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-daemon-submit"
	sessionName := "review"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	t.Setenv("POSTMAN_HOME", baseDir)
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	now := time.Now().UTC()
	requestID := "20260527-111405-r9e09"
	if _, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: requestID,
		Command:   projection.DaemonSubmitPop,
		CreatedAt: now.Add(-2 * time.Minute).Format(time.RFC3339Nano),
		Node:      "worker",
	}); err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}
	if _, err := projection.WriteDaemonSubmitResponse(sessionDir, projection.DaemonSubmitResponse{
		RequestID:    requestID,
		Command:      projection.DaemonSubmitPop,
		HandledAt:    now.Add(-time.Minute).Format(time.RFC3339Nano),
		MarkdownPath: "/tmp/private/read/message.md",
		Content:      "mailbox body must not leak",
	}); err != nil {
		t.Fatalf("WriteDaemonSubmitResponse: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunInspectDaemonSubmit([]string{
			"--context-id", contextID,
			"--session", sessionName,
			"--id", requestID,
		})
	})
	if err != nil {
		t.Fatalf("RunInspectDaemonSubmit() error = %v stderr=%q", err, stderr)
	}

	var got inspectDaemonSubmitOutput
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	if got.Status != "late_response" || got.Request == nil || got.Response == nil {
		t.Fatalf("inspect output = %#v, want request plus response", got)
	}
	if got.Request.State != "pending" || got.Request.Command != string(projection.DaemonSubmitPop) {
		t.Fatalf("request state = %#v", got.Request)
	}
	if got.Response.State != "late_response" || got.Response.Command != string(projection.DaemonSubmitPop) {
		t.Fatalf("response state = %#v", got.Response)
	}
	for _, forbidden := range []string{"/tmp/private", "message.md", "mailbox body", "worker"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("inspect output leaked %q: %s", forbidden, stdout)
		}
	}
}

func TestRunInspectDaemonSubmitReportsStaleResponse(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-daemon-submit"
	sessionName := "review"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	t.Setenv("POSTMAN_HOME", baseDir)
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	now := time.Now().UTC()
	requestID := "20260527-111405-r9e09"
	if _, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: requestID,
		Command:   projection.DaemonSubmitPop,
		CreatedAt: now.Format(time.RFC3339Nano),
		Node:      "worker",
	}); err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}
	if _, err := projection.WriteDaemonSubmitResponse(sessionDir, projection.DaemonSubmitResponse{
		RequestID: requestID,
		Command:   projection.DaemonSubmitPop,
		HandledAt: now.Add(-2 * time.Minute).Format(time.RFC3339Nano),
		Empty:     true,
	}); err != nil {
		t.Fatalf("WriteDaemonSubmitResponse: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunInspectDaemonSubmit([]string{
			"--context-id", contextID,
			"--session", sessionName,
			"--id", requestID,
		})
	})
	if err != nil {
		t.Fatalf("RunInspectDaemonSubmit() error = %v stderr=%q", err, stderr)
	}

	var got inspectDaemonSubmitOutput
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	if got.Status != "stale_response" || got.Response == nil || got.Response.State != "stale_response" {
		t.Fatalf("inspect output = %#v, want stale_response", got)
	}
}

func TestRunInspectDaemonSubmitRejectsPathIDs(t *testing.T) {
	_, _, err := captureCommandOutput(t, func() error {
		return RunInspectDaemonSubmit([]string{"--id", "../request"})
	})
	if err == nil || !strings.Contains(err.Error(), "not a path") {
		t.Fatalf("RunInspectDaemonSubmit() error = %v, want path rejection", err)
	}
}
