package cli

import (
	"io"
	"os"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

type commandContext struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	loadConfig            func(string) (*config.Config, error)
	resolveInboxPath      func([]string) (string, error)
	contextOwnsSession    func(baseDir, contextID, sessionName string) bool
	roundTripDaemonSubmit func(sessionDir string, request projection.DaemonSubmitRequest, timeout time.Duration) (projection.DaemonSubmitResponse, error)
}

func defaultCommandContext() commandContext {
	return commandContext{}.withDefaults()
}

func (ctx commandContext) withDefaults() commandContext {
	if ctx.stdin == nil {
		ctx.stdin = os.Stdin
	}
	if ctx.stdout == nil {
		ctx.stdout = os.Stdout
	}
	if ctx.stderr == nil {
		ctx.stderr = os.Stderr
	}
	if ctx.loadConfig == nil {
		ctx.loadConfig = config.LoadConfig
	}
	if ctx.resolveInboxPath == nil {
		ctx.resolveInboxPath = cliutil.ResolveInboxPath
	}
	if ctx.contextOwnsSession == nil {
		ctx.contextOwnsSession = config.ContextOwnsSession
	}
	if ctx.roundTripDaemonSubmit == nil {
		ctx.roundTripDaemonSubmit = roundTripDaemonSubmit
	}
	return ctx
}
