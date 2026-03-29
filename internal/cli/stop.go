package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

// RunStop gracefully stops the running postman daemon for this tmux session.
func RunStop(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	sessionFlag := fs.String("session", "", "tmux session name (auto-detect if in tmux)")
	configPath := fs.String("config", "", "path to config file")
	timeoutSecs := fs.Int("timeout", 10, "seconds to wait for daemon to exit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	sessionName := *sessionFlag
	if sessionName == "" {
		sessionName = config.GetTmuxSessionName()
	}
	if sessionName == "" {
		return fmt.Errorf("--session is required (or run inside tmux)")
	}
	sessionName, err = config.ValidateSessionName(sessionName)
	if err != nil {
		return err
	}

	contextID, err := config.ResolveContextIDFromSession(baseDir, sessionName)
	if err != nil {
		if strings.Contains(err.Error(), "no active postman found") {
			_, err = fmt.Fprintln(stdout, "postman: no daemon running")
			return err
		}
		return err
	}

	if !config.IsSessionPIDAlive(baseDir, contextID, sessionName) {
		_, err = fmt.Fprintln(stdout, "postman: no daemon running for this session")
		return err
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

	deadline := time.Now().Add(time.Duration(*timeoutSecs) * time.Second)
	for time.Now().Before(deadline) {
		if !config.IsSessionPIDAlive(baseDir, contextID, sessionName) {
			_, err = fmt.Fprintf(stdout, "postman: daemon (pid %d) stopped\n", pid)
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf(
		"daemon (pid %d) did not stop within %ds; try: kill -9 %d",
		pid, *timeoutSecs, pid,
	)
}
