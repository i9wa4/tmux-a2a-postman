package projection

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const daemonSubmitSchemaVersion = 1

type SubmitPath string

const (
	SubmitPathDaemon SubmitPath = "daemon-submit"
	SubmitPathPost   SubmitPath = "post"
)

type DaemonSubmitCommand string

const (
	DaemonSubmitSend DaemonSubmitCommand = "send"
	DaemonSubmitPop  DaemonSubmitCommand = "pop"
)

type DaemonSubmitRequest struct {
	SchemaVersion int                 `json:"schema_version"`
	RequestID     string              `json:"request_id"`
	Command       DaemonSubmitCommand `json:"command"`
	CreatedAt     string              `json:"created_at"`
	Filename      string              `json:"filename,omitempty"`
	Node          string              `json:"node,omitempty"`
	Content       string              `json:"content,omitempty"`
}

type DaemonSubmitResponse struct {
	SchemaVersion int                 `json:"schema_version"`
	RequestID     string              `json:"request_id"`
	Command       DaemonSubmitCommand `json:"command"`
	HandledAt     string              `json:"handled_at"`
	Empty         bool                `json:"empty,omitempty"`
	Filename      string              `json:"filename,omitempty"`
	Content       string              `json:"content,omitempty"`
	MarkdownPath  string              `json:"markdown_path,omitempty"`
	UnreadBefore  int                 `json:"unread_before,omitempty"`
	Error         string              `json:"error,omitempty"`
}

type DaemonSubmitResponseTimeoutError struct {
	RequestID string
	Timeout   time.Duration
}

func (e DaemonSubmitResponseTimeoutError) Error() string {
	return fmt.Sprintf("timed out waiting for daemon submit response %q after %s", e.RequestID, e.Timeout)
}

func DaemonSubmitRequestsDir(sessionDir string) string {
	return filepath.Join(sessionDir, "snapshot", string(SubmitPathDaemon), "requests")
}

func DaemonSubmitResponsesDir(sessionDir string) string {
	return filepath.Join(sessionDir, "snapshot", string(SubmitPathDaemon), "responses")
}

func DaemonSubmitRequestPath(sessionDir, requestID string) string {
	return filepath.Join(DaemonSubmitRequestsDir(sessionDir), requestID+".json")
}

func DaemonSubmitResponsePath(sessionDir, requestID string) string {
	return filepath.Join(DaemonSubmitResponsesDir(sessionDir), requestID+".json")
}

func EnsureDaemonSubmitDirs(sessionDir string) error {
	if err := ensureMailboxDir(DaemonSubmitRequestsDir(sessionDir)); err != nil {
		return err
	}
	return ensureMailboxDir(DaemonSubmitResponsesDir(sessionDir))
}

func NewDaemonSubmitRequestID(now time.Time) (string, error) {
	var suffix [2]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-r%04x", now.UTC().Format("20060102-150405"), suffix), nil
}

func WriteDaemonSubmitRequest(sessionDir string, request DaemonSubmitRequest) (string, error) {
	if err := EnsureDaemonSubmitDirs(sessionDir); err != nil {
		return "", err
	}
	request.SchemaVersion = daemonSubmitSchemaVersion
	requestPath := DaemonSubmitRequestPath(sessionDir, request.RequestID)
	if err := writeDaemonSubmitJSON(requestPath, request); err != nil {
		return "", err
	}
	return requestPath, nil
}

func WriteDaemonSubmitResponse(sessionDir string, response DaemonSubmitResponse) (string, error) {
	if err := EnsureDaemonSubmitDirs(sessionDir); err != nil {
		return "", err
	}
	response.SchemaVersion = daemonSubmitSchemaVersion
	responsePath := DaemonSubmitResponsePath(sessionDir, response.RequestID)
	if err := writeDaemonSubmitJSON(responsePath, response); err != nil {
		return "", err
	}
	return responsePath, nil
}

func ReadDaemonSubmitRequest(path string) (DaemonSubmitRequest, error) {
	var request DaemonSubmitRequest
	if err := readDaemonSubmitJSON(path, &request); err != nil {
		return DaemonSubmitRequest{}, err
	}
	return request, nil
}

func ReadDaemonSubmitResponse(path string) (DaemonSubmitResponse, error) {
	var response DaemonSubmitResponse
	if err := readDaemonSubmitJSON(path, &response); err != nil {
		return DaemonSubmitResponse{}, err
	}
	return response, nil
}

func WaitDaemonSubmitResponse(sessionDir, requestID string, timeout time.Duration) (DaemonSubmitResponse, string, error) {
	if timeout <= 0 {
		timeout = time.Second
	}
	responsePath := DaemonSubmitResponsePath(sessionDir, requestID)
	deadline := time.Now().Add(timeout)
	for {
		response, err := ReadDaemonSubmitResponse(responsePath)
		if err == nil {
			return response, responsePath, nil
		}
		if !os.IsNotExist(err) {
			return DaemonSubmitResponse{}, "", err
		}
		if time.Now().After(deadline) {
			return DaemonSubmitResponse{}, "", DaemonSubmitResponseTimeoutError{
				RequestID: requestID,
				Timeout:   timeout,
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func writeDaemonSubmitJSON(path string, value interface{}) error {
	tmpPath := path + ".tmp"
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func readDaemonSubmitJSON(path string, target interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
