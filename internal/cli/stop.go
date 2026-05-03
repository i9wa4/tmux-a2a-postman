package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

const stopTimeoutSeconds = 10

type stopOutput struct {
	Status    string `json:"status"`
	Session   string `json:"session,omitempty"`
	ContextID string `json:"context_id,omitempty"`
	PID       int    `json:"pid,omitempty"`
}

// RunStop gracefully stops the running postman daemon for this tmux session.
func RunStop(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	configPath := fs.String("config", "", "path to config file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	sessionName := config.GetTmuxSessionName()
	if sessionName == "" {
		return fmt.Errorf("tmux session name required (run inside tmux)")
	}
	sessionName, err = config.ValidateSessionName(sessionName)
	if err != nil {
		return err
	}

	contextID, err := config.ResolveContextIDFromSession(baseDir, sessionName)
	if err != nil {
		if strings.Contains(err.Error(), "no active postman found") {
			return json.NewEncoder(stdout).Encode(stopOutput{
				Status:  "not_running",
				Session: sessionName,
			})
		}
		return err
	}

	if !config.IsSessionPIDOwnedByCurrentUser(baseDir, contextID, sessionName) {
		return json.NewEncoder(stdout).Encode(stopOutput{
			Status:    "not_owned",
			Session:   sessionName,
			ContextID: contextID,
		})
	}

	pidPath := filepath.Join(baseDir, contextID, sessionName, "postman.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return fmt.Errorf("reading pid file %s: %w", pidPath, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return fmt.Errorf("invalid pid in %s", pidPath)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("finding process %d: %w", pid, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("sending SIGTERM to pid %d: %w", pid, err)
	}

	deadline := time.Now().Add(stopTimeoutSeconds * time.Second)
	for time.Now().Before(deadline) {
		if !config.IsSessionPIDAlive(baseDir, contextID, sessionName) {
			return json.NewEncoder(stdout).Encode(stopOutput{
				Status:    "stopped",
				Session:   sessionName,
				ContextID: contextID,
				PID:       pid,
			})
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf(
		"daemon (pid %d) did not stop within %ds; try: kill -9 %d",
		pid, stopTimeoutSeconds, pid,
	)
}
