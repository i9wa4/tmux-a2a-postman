package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

func compatibilitySubmitTimeout(tmuxTimeoutSeconds float64) time.Duration {
	if tmuxTimeoutSeconds <= 0 {
		return 5 * time.Second
	}
	timeout := time.Duration(tmuxTimeoutSeconds * float64(time.Second))
	if timeout < 250*time.Millisecond {
		return 250 * time.Millisecond
	}
	return timeout
}

func roundTripCompatibilitySubmit(sessionDir string, request projection.CompatibilitySubmitRequest, timeout time.Duration) (projection.CompatibilitySubmitResponse, error) {
	now := time.Now()
	requestID, err := projection.NewCompatibilitySubmitRequestID(now)
	if err != nil {
		return projection.CompatibilitySubmitResponse{}, err
	}
	request.RequestID = requestID
	request.CreatedAt = now.UTC().Format(time.RFC3339)
	if _, err := projection.WriteCompatibilitySubmitRequest(sessionDir, request); err != nil {
		return projection.CompatibilitySubmitResponse{}, err
	}
	response, responsePath, err := projection.WaitCompatibilitySubmitResponse(sessionDir, requestID, timeout)
	if err != nil {
		return projection.CompatibilitySubmitResponse{}, err
	}
	if removeErr := os.Remove(responsePath); removeErr != nil && !os.IsNotExist(removeErr) {
		return projection.CompatibilitySubmitResponse{}, removeErr
	}
	if response.Error != "" {
		return projection.CompatibilitySubmitResponse{}, fmt.Errorf("%s", response.Error)
	}
	return response, nil
}
