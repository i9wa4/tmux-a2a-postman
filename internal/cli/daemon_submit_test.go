package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

func TestRoundTripDaemonSubmitTimeoutWarnsRequestMayStillCommit(t *testing.T) {
	sessionDir := t.TempDir()
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	_, err := roundTripDaemonSubmit(sessionDir, projection.DaemonSubmitRequest{
		Command: projection.DaemonSubmitPop,
		Node:    "worker",
	}, time.Millisecond)
	if err == nil {
		t.Fatal("roundTripDaemonSubmit() error = nil, want timeout")
	}

	got := err.Error()
	for _, want := range []string{
		"daemon-submit pop request",
		"timed out",
		"may still commit",
		"do not retry blindly",
		"inspect-daemon-submit --id",
		"inspect status, inbox/read evidence, or recipient-side evidence",
		"get-status --debug",
		"daemon_submit pending/claimed/late response counts",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("timeout error missing %q: %q", want, got)
		}
	}
}

func TestDaemonSubmitRoundTripperReturnsResponseBeforeTimeout(t *testing.T) {
	createdAt := time.Date(2026, time.June, 1, 1, 0, 0, 0, time.UTC)
	var wrote projection.DaemonSubmitRequest
	var removedPath string
	transport := daemonSubmitRoundTripper{
		now: func() time.Time {
			return createdAt
		},
		newRequestID: func(time.Time) (string, error) {
			return "req-ok", nil
		},
		writeRequest: func(_ string, request projection.DaemonSubmitRequest) (string, error) {
			wrote = request
			return "/state/requests/req-ok.json", nil
		},
		readResponse: func(path string) (projection.DaemonSubmitResponse, error) {
			if path != "/session/snapshot/daemon-submit/responses/req-ok.json" {
				t.Fatalf("readResponse path = %q", path)
			}
			return projection.DaemonSubmitResponse{
				RequestID: "req-ok",
				Command:   projection.DaemonSubmitSend,
				HandledAt: createdAt.Add(time.Second).Format(time.RFC3339Nano),
				Filename:  "message.md",
			}, nil
		},
		removeResponse: func(path string) error {
			removedPath = path
			return nil
		},
		sleep: func(time.Duration) {
			t.Fatal("sleep should not run when response is available immediately")
		},
	}

	outcome, err := transport.roundTrip("/session", projection.DaemonSubmitRequest{
		Command:  projection.DaemonSubmitSend,
		Filename: "message.md",
	}, time.Second)
	if err != nil {
		t.Fatalf("roundTrip: %v", err)
	}
	if outcome.RequestID != "req-ok" || outcome.RequestPath != "/state/requests/req-ok.json" {
		t.Fatalf("outcome request identity = %q/%q", outcome.RequestID, outcome.RequestPath)
	}
	if wrote.RequestID != "req-ok" || wrote.CreatedAt != createdAt.Format(time.RFC3339Nano) {
		t.Fatalf("written request = %#v", wrote)
	}
	if outcome.Response.Filename != "message.md" {
		t.Fatalf("outcome.Response.Filename = %q, want message.md", outcome.Response.Filename)
	}
	if removedPath != "/session/snapshot/daemon-submit/responses/req-ok.json" {
		t.Fatalf("removedPath = %q", removedPath)
	}
}

func TestDaemonSubmitRoundTripperTimeoutReturnsDiagnosticsWithoutRealSleep(t *testing.T) {
	createdAt := time.Date(2026, time.June, 1, 1, 0, 0, 0, time.UTC)
	nowCalls := 0
	sleepCalls := 0
	transport := daemonSubmitRoundTripper{
		now: func() time.Time {
			nowCalls++
			if nowCalls <= 2 {
				return createdAt
			}
			return createdAt.Add(2 * time.Second)
		},
		newRequestID: func(time.Time) (string, error) {
			return "req-timeout", nil
		},
		writeRequest: func(string, projection.DaemonSubmitRequest) (string, error) {
			return "/state/requests/req-timeout.json", nil
		},
		readResponse: func(string) (projection.DaemonSubmitResponse, error) {
			return projection.DaemonSubmitResponse{}, os.ErrNotExist
		},
		diagnostics: func(string, time.Time) daemonSubmitRoundTripDiagnostics {
			return daemonSubmitRoundTripDiagnostics{
				PendingRequestCount:     1,
				OldestPendingAgeSeconds: 2,
			}
		},
		sleep: func(time.Duration) {
			sleepCalls++
		},
	}

	outcome, err := transport.roundTrip("/session", projection.DaemonSubmitRequest{
		Command: projection.DaemonSubmitPop,
		Node:    "worker",
	}, time.Second)
	if err == nil {
		t.Fatal("roundTrip error = nil, want timeout")
	}
	var timeoutErr projection.DaemonSubmitResponseTimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("roundTrip error = %v, want timeout error", err)
	}
	if !outcome.TimedOut {
		t.Fatal("outcome.TimedOut = false, want true")
	}
	if outcome.Diagnostics.PendingRequestCount != 1 || outcome.Diagnostics.OldestPendingAgeSeconds != 2 {
		t.Fatalf("outcome.Diagnostics = %#v", outcome.Diagnostics)
	}
	if sleepCalls != 1 {
		t.Fatalf("sleepCalls = %d, want 1", sleepCalls)
	}
}

func TestDaemonSubmitRoundTripperRemovesStaleResponseAndWaitsForFresh(t *testing.T) {
	createdAt := time.Date(2026, time.June, 1, 1, 0, 0, 0, time.UTC)
	readCalls := 0
	removed := []string{}
	transport := daemonSubmitRoundTripper{
		now: func() time.Time {
			return createdAt.Add(time.Second)
		},
		newRequestID: func(time.Time) (string, error) {
			return "req-stale", nil
		},
		writeRequest: func(string, projection.DaemonSubmitRequest) (string, error) {
			return "/state/requests/req-stale.json", nil
		},
		readResponse: func(string) (projection.DaemonSubmitResponse, error) {
			readCalls++
			if readCalls == 1 {
				return projection.DaemonSubmitResponse{
					RequestID: "req-stale",
					Command:   projection.DaemonSubmitPop,
					HandledAt: createdAt.Add(-time.Minute).Format(time.RFC3339Nano),
					Empty:     true,
				}, nil
			}
			return projection.DaemonSubmitResponse{
				RequestID: "req-stale",
				Command:   projection.DaemonSubmitPop,
				HandledAt: createdAt.Add(time.Second).Format(time.RFC3339Nano),
				Filename:  "fresh.md",
			}, nil
		},
		removeResponse: func(path string) error {
			removed = append(removed, path)
			return nil
		},
		sleep: func(time.Duration) {},
	}

	outcome, err := transport.roundTrip("/session", projection.DaemonSubmitRequest{
		Command: projection.DaemonSubmitPop,
		Node:    "worker",
	}, time.Second)
	if err != nil {
		t.Fatalf("roundTrip: %v", err)
	}
	if outcome.StaleResponseCount != 1 {
		t.Fatalf("outcome.StaleResponseCount = %d, want 1", outcome.StaleResponseCount)
	}
	if outcome.Response.Filename != "fresh.md" {
		t.Fatalf("outcome.Response.Filename = %q, want fresh.md", outcome.Response.Filename)
	}
	if len(removed) != 2 {
		t.Fatalf("removed = %#v, want stale and fresh response removals", removed)
	}
}

func TestDaemonSubmitRoundTripperAcceptsSecondPrecisionHandledAt(t *testing.T) {
	createdAt := time.Date(2026, time.June, 1, 1, 0, 0, 750000000, time.UTC)
	transport := daemonSubmitRoundTripper{
		now: func() time.Time {
			return createdAt
		},
		newRequestID: func(time.Time) (string, error) {
			return "req-second-precision", nil
		},
		writeRequest: func(string, projection.DaemonSubmitRequest) (string, error) {
			return "/state/requests/req-second-precision.json", nil
		},
		readResponse: func(string) (projection.DaemonSubmitResponse, error) {
			return projection.DaemonSubmitResponse{
				RequestID: "req-second-precision",
				Command:   projection.DaemonSubmitPop,
				HandledAt: createdAt.Truncate(time.Second).Format(time.RFC3339),
				Filename:  "same-second.md",
			}, nil
		},
		removeResponse: func(string) error {
			return nil
		},
		sleep: func(time.Duration) {
			t.Fatal("sleep should not run for same-second response")
		},
	}

	outcome, err := transport.roundTrip("/session", projection.DaemonSubmitRequest{
		Command: projection.DaemonSubmitPop,
		Node:    "worker",
	}, time.Second)
	if err != nil {
		t.Fatalf("roundTrip: %v", err)
	}
	if outcome.StaleResponseCount != 0 {
		t.Fatalf("outcome.StaleResponseCount = %d, want 0", outcome.StaleResponseCount)
	}
	if outcome.Response.Filename != "same-second.md" {
		t.Fatalf("outcome.Response.Filename = %q, want same-second.md", outcome.Response.Filename)
	}
}

func TestDaemonSubmitRoundTripperReturnsMalformedResponseError(t *testing.T) {
	sessionDir := t.TempDir()
	createdAt := time.Date(2026, time.June, 1, 2, 0, 0, 0, time.UTC)
	transport := daemonSubmitRoundTripper{
		now: func() time.Time {
			return createdAt
		},
		newRequestID: func(time.Time) (string, error) {
			return "req-malformed", nil
		},
		writeRequest: func(sessionDir string, request projection.DaemonSubmitRequest) (string, error) {
			requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, request)
			if err != nil {
				return "", err
			}
			responsePath := projection.DaemonSubmitResponsePath(sessionDir, request.RequestID)
			if err := os.WriteFile(responsePath, []byte("{"), 0o600); err != nil {
				return "", err
			}
			return requestPath, nil
		},
		sleep: func(time.Duration) {
			t.Fatal("sleep should not run when malformed response is present")
		},
	}

	_, err := transport.roundTrip(sessionDir, projection.DaemonSubmitRequest{
		Command: projection.DaemonSubmitSend,
	}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "unexpected end") {
		t.Fatalf("roundTrip error = %v, want malformed JSON error", err)
	}
}

func TestDaemonSubmitRoundTripperReturnsCleanupFailureAfterResponse(t *testing.T) {
	createdAt := time.Date(2026, time.June, 1, 3, 0, 0, 0, time.UTC)
	cleanupErr := errors.New("remove response failed")
	transport := daemonSubmitRoundTripper{
		now: func() time.Time {
			return createdAt
		},
		newRequestID: func(time.Time) (string, error) {
			return "req-cleanup", nil
		},
		writeRequest: func(string, projection.DaemonSubmitRequest) (string, error) {
			return "/state/requests/req-cleanup.json", nil
		},
		readResponse: func(string) (projection.DaemonSubmitResponse, error) {
			return projection.DaemonSubmitResponse{
				RequestID: "req-cleanup",
				Command:   projection.DaemonSubmitPop,
				HandledAt: createdAt.Format(time.RFC3339Nano),
				Filename:  "cleanup.md",
			}, nil
		},
		removeResponse: func(string) error {
			return cleanupErr
		},
	}

	outcome, err := transport.roundTrip("/session", projection.DaemonSubmitRequest{
		Command: projection.DaemonSubmitPop,
		Node:    "worker",
	}, time.Second)
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("roundTrip error = %v, want cleanup error", err)
	}
	if outcome.Response.Filename != "cleanup.md" {
		t.Fatalf("outcome.Response.Filename = %q, want cleanup.md", outcome.Response.Filename)
	}
}

func TestRoundTripDaemonSubmitWithTransportMapsDaemonErrorResponse(t *testing.T) {
	transport := daemonSubmitRoundTripper{
		now: func() time.Time {
			return time.Date(2026, time.June, 1, 1, 0, 0, 0, time.UTC)
		},
		newRequestID: func(time.Time) (string, error) {
			return "req-error", nil
		},
		writeRequest: func(string, projection.DaemonSubmitRequest) (string, error) {
			return "/state/requests/req-error.json", nil
		},
		readResponse: func(string) (projection.DaemonSubmitResponse, error) {
			return projection.DaemonSubmitResponse{
				RequestID: "req-error",
				Command:   projection.DaemonSubmitSend,
				HandledAt: time.Date(2026, time.June, 1, 1, 0, 1, 0, time.UTC).Format(time.RFC3339Nano),
				Error:     "daemon rejected send",
			}, nil
		},
		removeResponse: func(string) error {
			return nil
		},
	}

	_, err := roundTripDaemonSubmitWithTransport("/session", projection.DaemonSubmitRequest{
		Command: projection.DaemonSubmitSend,
	}, time.Second, transport)
	if err == nil || !strings.Contains(err.Error(), "daemon rejected send") {
		t.Fatalf("roundTripDaemonSubmitWithTransport error = %v, want daemon response error", err)
	}
}

func TestDaemonSubmitTransportDiagnosticsCountsPendingClaimedAndLate(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.June, 1, 1, 10, 0, 0, time.UTC)
	if _, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pending",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: now.Add(-2 * time.Minute).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("WriteDaemonSubmitRequest(pending): %v", err)
	}
	claimedPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-claimed",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: now.Add(-3 * time.Minute).Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest(claimed): %v", err)
	}
	if err := os.Rename(claimedPath, claimedPath+".processing"); err != nil {
		t.Fatalf("Rename claimed request: %v", err)
	}
	if _, err := projection.WriteDaemonSubmitResponse(sessionDir, projection.DaemonSubmitResponse{
		RequestID: "req-late",
		Command:   projection.DaemonSubmitPop,
		HandledAt: now.Add(-4 * time.Minute).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("WriteDaemonSubmitResponse(late): %v", err)
	}
	if _, err := projection.WriteDaemonSubmitResponse(sessionDir, projection.DaemonSubmitResponse{
		RequestID: "req-diagnostics",
		Command:   projection.DaemonSubmitRuntimeDiagnostics,
		HandledAt: now.Add(-5 * time.Minute).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("WriteDaemonSubmitResponse(diagnostics): %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "snapshot", string(projection.SubmitPathDaemon))); err != nil {
		t.Fatalf("daemon-submit snapshot missing: %v", err)
	}

	diagnostics := scanDaemonSubmitRoundTripDiagnostics(sessionDir, now)
	if diagnostics.PendingRequestCount != 1 || diagnostics.OldestPendingAgeSeconds != 120 {
		t.Fatalf("pending diagnostics = %#v", diagnostics)
	}
	if diagnostics.ClaimedRequestCount != 1 || diagnostics.OldestClaimedAgeSeconds != 180 {
		t.Fatalf("claimed diagnostics = %#v", diagnostics)
	}
	if diagnostics.LateResponseCount != 1 || diagnostics.OldestLateResponseAgeSeconds != 240 {
		t.Fatalf("late diagnostics = %#v", diagnostics)
	}
}
