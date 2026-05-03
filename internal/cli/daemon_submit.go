package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

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
	now := time.Now()
	requestID, err := projection.NewDaemonSubmitRequestID(now)
	if err != nil {
		return projection.DaemonSubmitResponse{}, err
	}
	request.RequestID = requestID
	request.CreatedAt = now.UTC().Format(time.RFC3339)
	if _, err := projection.WriteDaemonSubmitRequest(sessionDir, request); err != nil {
		return projection.DaemonSubmitResponse{}, err
	}
	response, responsePath, err := projection.WaitDaemonSubmitResponse(sessionDir, requestID, timeout)
	if err != nil {
		return projection.DaemonSubmitResponse{}, err
	}
	if removeErr := os.Remove(responsePath); removeErr != nil && !os.IsNotExist(removeErr) {
		return projection.DaemonSubmitResponse{}, removeErr
	}
	if response.Error != "" {
		return projection.DaemonSubmitResponse{}, fmt.Errorf("%s", response.Error)
	}
	return response, nil
}
