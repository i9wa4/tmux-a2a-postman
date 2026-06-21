package cli

import (
	"io"
	"os"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

type commandContext struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	stdinIsTerminal       func(io.Reader) bool
	loadConfig            func(string) (*config.Config, error)
	resolveInboxPath      func([]string) (string, error)
	resolveContextID      func(string) (string, error)
	resolveContextSession func(baseDir, sessionName string) (string, error)
	contextOwnsSession    func(baseDir, contextID, sessionName string) bool
	contextHasLiveDaemon  func(baseDir, contextID string) bool
	roundTripDaemonSubmit func(sessionDir string, request projection.DaemonSubmitRequest, timeout time.Duration) (projection.DaemonSubmitResponse, error)
	getTmuxPaneName       func() string
	getTmuxSessionName    func() string
	getTmuxPaneID         func() string
	discoverNodes         func(baseDir, contextID, selfSession string) (map[string]discovery.NodeInfo, error)
	discoverAllSessions   func() ([]string, error)
	collectSessionStatus  sessionStatusCollector
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
	if ctx.stdinIsTerminal == nil {
		ctx.stdinIsTerminal = defaultStdinIsTerminal
	}
	if ctx.loadConfig == nil {
		ctx.loadConfig = config.LoadConfig
	}
	if ctx.resolveInboxPath == nil {
		ctx.resolveInboxPath = cliutil.ResolveInboxPath
	}
	if ctx.resolveContextID == nil {
		ctx.resolveContextID = config.ResolveContextID
	}
	if ctx.resolveContextSession == nil {
		ctx.resolveContextSession = config.ResolveContextIDFromSession
	}
	if ctx.contextOwnsSession == nil {
		ctx.contextOwnsSession = config.ContextOwnsSession
	}
	if ctx.contextHasLiveDaemon == nil {
		ctx.contextHasLiveDaemon = config.ContextHasLiveDaemon
	}
	if ctx.roundTripDaemonSubmit == nil {
		ctx.roundTripDaemonSubmit = roundTripDaemonSubmit
	}
	if ctx.getTmuxPaneName == nil {
		ctx.getTmuxPaneName = config.GetTmuxPaneName
	}
	if ctx.getTmuxSessionName == nil {
		ctx.getTmuxSessionName = config.GetTmuxSessionName
	}
	if ctx.getTmuxPaneID == nil {
		ctx.getTmuxPaneID = config.GetTmuxPaneID
	}
	if ctx.discoverNodes == nil {
		ctx.discoverNodes = discovery.DiscoverNodes
	}
	if ctx.discoverAllSessions == nil {
		ctx.discoverAllSessions = discovery.DiscoverAllSessions
	}
	if ctx.collectSessionStatus == nil {
		ctx.collectSessionStatus = collectSessionStatus
	}
	return ctx
}

func defaultStdinIsTerminal(stdin io.Reader) bool {
	stdinFile, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	info, err := stdinFile.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
