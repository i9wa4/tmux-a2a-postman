package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

type inspectDaemonSubmitOutput struct {
	Status      string                            `json:"status"`
	RequestID   string                            `json:"request_id"`
	Request     *inspectDaemonSubmitRequestState  `json:"request,omitempty"`
	Response    *inspectDaemonSubmitResponseState `json:"response,omitempty"`
	ContextID   string                            `json:"context_id,omitempty"`
	SessionName string                            `json:"session_name,omitempty"`
}

type inspectDaemonSubmitRequestState struct {
	State      string `json:"state"`
	Command    string `json:"command,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	AgeSeconds int    `json:"age_seconds,omitempty"`
}

type inspectDaemonSubmitResponseState struct {
	State                     string `json:"state"`
	Command                   string `json:"command,omitempty"`
	HandledAt                 string `json:"handled_at,omitempty"`
	AgeSeconds                int    `json:"age_seconds,omitempty"`
	Empty                     bool   `json:"empty,omitempty"`
	ErrorPresent              bool   `json:"error_present,omitempty"`
	RuntimeDiagnosticsPresent bool   `json:"runtime_diagnostics_present,omitempty"`
	RuntimeProfilePresent     bool   `json:"runtime_profile_present,omitempty"`
}

func RunInspectDaemonSubmit(args []string) error {
	fs := flag.NewFlagSet("inspect-daemon-submit", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	contextID := fs.String("context-id", "", "Context ID (optional, auto-resolved from tmux session)")
	configPath := fs.String("config", "", "Config file path")
	sessionName := fs.String("session", "", "tmux session name (optional, defaults to current tmux session)")
	id := fs.String("id", "", "daemon-submit request_id to inspect")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	if !validDaemonSubmitInspectID(*id) {
		return fmt.Errorf("--id must be a daemon-submit request_id, not a path")
	}

	sessionDir, resolvedContextID, resolvedSessionName, err := resolveInspectMessageSessionDir(*contextID, *sessionName, *configPath)
	if err != nil {
		return err
	}
	now := time.Now()
	output := inspectDaemonSubmitOutput{
		Status:      "not_found",
		RequestID:   *id,
		ContextID:   resolvedContextID,
		SessionName: resolvedSessionName,
	}

	request, err := inspectDaemonSubmitRequest(sessionDir, *id, now)
	if err != nil {
		return err
	}
	if request != nil {
		output.Request = request
		output.Status = request.State
	}

	response, err := inspectDaemonSubmitResponse(sessionDir, *id, now, request)
	if err != nil {
		return err
	}
	if response != nil {
		output.Response = response
		output.Status = response.State
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func validDaemonSubmitInspectID(id string) bool {
	return id != "." && id != ".." && id == filepath.Base(id) && !strings.ContainsAny(id, `/\`)
}

func inspectDaemonSubmitRequest(sessionDir, id string, now time.Time) (*inspectDaemonSubmitRequestState, error) {
	pendingPath := projection.DaemonSubmitRequestPath(sessionDir, id)
	if state, err := readInspectDaemonSubmitRequest(pendingPath, "pending", now); state != nil || err != nil {
		return state, err
	}
	return readInspectDaemonSubmitRequest(pendingPath+".processing", "claimed", now)
}

func readInspectDaemonSubmitRequest(path, state string, now time.Time) (*inspectDaemonSubmitRequestState, error) {
	request, err := projection.ReadDaemonSubmitRequest(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &inspectDaemonSubmitRequestState{
		State:      state,
		Command:    string(request.Command),
		CreatedAt:  request.CreatedAt,
		AgeSeconds: daemonSubmitInspectAgeSeconds(request.CreatedAt, now),
	}, nil
}

func inspectDaemonSubmitResponse(sessionDir, id string, now time.Time, request *inspectDaemonSubmitRequestState) (*inspectDaemonSubmitResponseState, error) {
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, id))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &inspectDaemonSubmitResponseState{
		State:                     inspectDaemonSubmitResponseVisibleState(response, id, request),
		Command:                   string(response.Command),
		HandledAt:                 response.HandledAt,
		AgeSeconds:                daemonSubmitInspectAgeSeconds(response.HandledAt, now),
		Empty:                     response.Empty,
		ErrorPresent:              response.Error != "",
		RuntimeDiagnosticsPresent: response.RuntimeDiagnostics != nil,
		RuntimeProfilePresent:     response.RuntimeProfile != nil,
	}, nil
}

func inspectDaemonSubmitResponseVisibleState(response projection.DaemonSubmitResponse, requestID string, request *inspectDaemonSubmitRequestState) string {
	if response.RequestID != "" && response.RequestID != requestID {
		return "stale_response"
	}
	if request != nil && isStaleDaemonSubmitResponse(response, requestID, request.CreatedAt) {
		return "stale_response"
	}
	return "late_response"
}

func daemonSubmitInspectAgeSeconds(timestamp string, now time.Time) int {
	if timestamp == "" {
		return 0
	}
	parsed, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil || !parsed.Before(now) {
		return 0
	}
	return int(now.Sub(parsed).Seconds())
}
