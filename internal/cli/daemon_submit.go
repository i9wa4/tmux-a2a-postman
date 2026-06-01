package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

const daemonSubmitPollInterval = 10 * time.Millisecond

type daemonSubmitRoundTripper struct {
	now            func() time.Time
	sleep          func(time.Duration)
	newRequestID   func(time.Time) (string, error)
	writeRequest   func(string, projection.DaemonSubmitRequest) (string, error)
	readResponse   func(string) (projection.DaemonSubmitResponse, error)
	removeResponse func(string) error
	diagnostics    func(string, time.Time) daemonSubmitRoundTripDiagnostics
}

type daemonSubmitRoundTripOutcome struct {
	RequestID          string
	RequestPath        string
	ResponsePath       string
	RequestCreatedAt   string
	Response           projection.DaemonSubmitResponse
	TimedOut           bool
	StaleResponseCount int
	Diagnostics        daemonSubmitRoundTripDiagnostics
}

type daemonSubmitRoundTripDiagnostics struct {
	PendingRequestCount          int
	OldestPendingAgeSeconds      int
	ClaimedRequestCount          int
	OldestClaimedAgeSeconds      int
	LateResponseCount            int
	OldestLateResponseAgeSeconds int
}

func daemonSubmitTimeout(tmuxTimeoutSeconds float64) time.Duration {
	if tmuxTimeoutSeconds <= 0 {
		return 5 * time.Second
	}
	timeout := time.Duration(tmuxTimeoutSeconds * float64(time.Second))
	if timeout < 250*time.Millisecond {
		return 250 * time.Millisecond
	}
	return timeout
}

func roundTripDaemonSubmit(sessionDir string, request projection.DaemonSubmitRequest, timeout time.Duration) (projection.DaemonSubmitResponse, error) {
	return roundTripDaemonSubmitWithTransport(sessionDir, request, timeout, daemonSubmitRoundTripper{})
}

func roundTripDaemonSubmitWithTransport(sessionDir string, request projection.DaemonSubmitRequest, timeout time.Duration, transport daemonSubmitRoundTripper) (projection.DaemonSubmitResponse, error) {
	outcome, err := transport.roundTrip(sessionDir, request, timeout)
	if err != nil {
		var timeoutErr projection.DaemonSubmitResponseTimeoutError
		if errors.As(err, &timeoutErr) {
			return projection.DaemonSubmitResponse{}, fmt.Errorf("daemon-submit %s request %q timed out after %s; the request may still commit after this timeout; do not retry blindly; use `tmux-a2a-postman inspect-daemon-submit --id %s` to look up this request, inspect status, inbox/read evidence, or recipient-side evidence, and use `tmux-a2a-postman get-status --debug` for daemon_submit pending/claimed/late response counts before retrying", request.Command, outcome.RequestID, timeoutErr.Timeout, outcome.RequestID)
		}
		return projection.DaemonSubmitResponse{}, err
	}
	response := outcome.Response
	if response.Error != "" {
		return projection.DaemonSubmitResponse{}, fmt.Errorf("%s", response.Error)
	}
	return response, nil
}

func (transport daemonSubmitRoundTripper) roundTrip(sessionDir string, request projection.DaemonSubmitRequest, timeout time.Duration) (daemonSubmitRoundTripOutcome, error) {
	transport = transport.withDefaults()
	if timeout <= 0 {
		timeout = time.Second
	}

	now := transport.now()
	requestID, err := transport.newRequestID(now)
	if err != nil {
		return daemonSubmitRoundTripOutcome{}, err
	}
	request.RequestID = requestID
	request.CreatedAt = now.UTC().Format(time.RFC3339Nano)
	requestPath, err := transport.writeRequest(sessionDir, request)
	outcome := daemonSubmitRoundTripOutcome{
		RequestID:        requestID,
		RequestPath:      requestPath,
		ResponsePath:     projection.DaemonSubmitResponsePath(sessionDir, requestID),
		RequestCreatedAt: request.CreatedAt,
	}
	if err != nil {
		return outcome, err
	}

	deadline := now.Add(timeout)
	for {
		response, err := transport.readResponse(outcome.ResponsePath)
		if err == nil {
			if isStaleDaemonSubmitResponse(response, requestID, request.CreatedAt) {
				outcome.StaleResponseCount++
				if removeErr := transport.removeResponse(outcome.ResponsePath); removeErr != nil && !os.IsNotExist(removeErr) {
					return outcome, removeErr
				}
				continue
			}
			outcome.Response = response
			if removeErr := transport.removeResponse(outcome.ResponsePath); removeErr != nil && !os.IsNotExist(removeErr) {
				return outcome, removeErr
			}
			return outcome, nil
		}
		if !os.IsNotExist(err) {
			return outcome, err
		}
		current := transport.now()
		if current.After(deadline) {
			outcome.TimedOut = true
			outcome.Diagnostics = transport.diagnostics(sessionDir, current)
			return outcome, projection.DaemonSubmitResponseTimeoutError{
				RequestID: requestID,
				Timeout:   timeout,
			}
		}
		transport.sleep(daemonSubmitPollInterval)
	}
}

func (transport daemonSubmitRoundTripper) withDefaults() daemonSubmitRoundTripper {
	if transport.now == nil {
		transport.now = time.Now
	}
	if transport.sleep == nil {
		transport.sleep = time.Sleep
	}
	if transport.newRequestID == nil {
		transport.newRequestID = projection.NewDaemonSubmitRequestID
	}
	if transport.writeRequest == nil {
		transport.writeRequest = projection.WriteDaemonSubmitRequest
	}
	if transport.readResponse == nil {
		transport.readResponse = projection.ReadDaemonSubmitResponse
	}
	if transport.removeResponse == nil {
		transport.removeResponse = os.Remove
	}
	if transport.diagnostics == nil {
		transport.diagnostics = scanDaemonSubmitRoundTripDiagnostics
	}
	return transport
}

func isStaleDaemonSubmitResponse(response projection.DaemonSubmitResponse, requestID, requestCreatedAt string) bool {
	if response.RequestID != "" && response.RequestID != requestID {
		return true
	}
	if response.HandledAt == "" || requestCreatedAt == "" {
		return false
	}
	handledAt, err := time.Parse(time.RFC3339Nano, response.HandledAt)
	if err != nil {
		return false
	}
	createdAt, err := time.Parse(time.RFC3339Nano, requestCreatedAt)
	if err != nil {
		return false
	}
	// Daemon responses are currently second-precision while requests are
	// nanosecond-precision, so same-second responses are not stale.
	return handledAt.Add(time.Second).Before(createdAt)
}

func scanDaemonSubmitRoundTripDiagnostics(sessionDir string, now time.Time) daemonSubmitRoundTripDiagnostics {
	diagnostics := daemonSubmitRoundTripDiagnostics{}
	scanDaemonSubmitRoundTripRequests(sessionDir, now, &diagnostics)
	scanDaemonSubmitRoundTripResponses(sessionDir, now, &diagnostics)
	return diagnostics
}

func scanDaemonSubmitRoundTripRequests(sessionDir string, now time.Time, diagnostics *daemonSubmitRoundTripDiagnostics) {
	requestsDir := projection.DaemonSubmitRequestsDir(sessionDir)
	entries, err := os.ReadDir(requestsDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		state := ""
		switch {
		case strings.HasSuffix(entry.Name(), ".json"):
			state = "pending"
		case strings.HasSuffix(entry.Name(), ".processing"):
			state = "claimed"
		default:
			continue
		}

		requestPath := filepath.Join(requestsDir, entry.Name())
		request, err := projection.ReadDaemonSubmitRequest(requestPath)
		if err == nil && request.Command == projection.DaemonSubmitRuntimeDiagnostics {
			continue
		}
		switch state {
		case "pending":
			diagnostics.PendingRequestCount++
			if err == nil {
				diagnostics.OldestPendingAgeSeconds = oldestDaemonSubmitRoundTripAgeSeconds(diagnostics.OldestPendingAgeSeconds, request.CreatedAt, now)
			}
		case "claimed":
			diagnostics.ClaimedRequestCount++
			if err == nil {
				diagnostics.OldestClaimedAgeSeconds = oldestDaemonSubmitRoundTripAgeSeconds(diagnostics.OldestClaimedAgeSeconds, request.CreatedAt, now)
			}
		}
	}
}

func scanDaemonSubmitRoundTripResponses(sessionDir string, now time.Time, diagnostics *daemonSubmitRoundTripDiagnostics) {
	responsesDir := projection.DaemonSubmitResponsesDir(sessionDir)
	entries, err := os.ReadDir(responsesDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		responsePath := filepath.Join(responsesDir, entry.Name())
		response, err := projection.ReadDaemonSubmitResponse(responsePath)
		if err == nil && response.Command == projection.DaemonSubmitRuntimeDiagnostics {
			continue
		}

		diagnostics.LateResponseCount++
		if err == nil {
			diagnostics.OldestLateResponseAgeSeconds = oldestDaemonSubmitRoundTripAgeSeconds(diagnostics.OldestLateResponseAgeSeconds, response.HandledAt, now)
			continue
		}
		info, infoErr := entry.Info()
		if infoErr == nil {
			diagnostics.OldestLateResponseAgeSeconds = oldestDaemonSubmitRoundTripAgeSecondsFromTime(diagnostics.OldestLateResponseAgeSeconds, info.ModTime(), now)
		}
	}
}

func oldestDaemonSubmitRoundTripAgeSeconds(current int, timestamp string, now time.Time) int {
	if timestamp == "" {
		return current
	}
	parsed, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return current
	}
	return oldestDaemonSubmitRoundTripAgeSecondsFromTime(current, parsed, now)
}

func oldestDaemonSubmitRoundTripAgeSecondsFromTime(current int, timestamp time.Time, now time.Time) int {
	if timestamp.IsZero() {
		return current
	}
	age := 0
	if timestamp.Before(now) {
		age = int(now.Sub(timestamp).Seconds())
	}
	if age > current {
		return age
	}
	return current
}
