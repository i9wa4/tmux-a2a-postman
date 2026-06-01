package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

func isNoActivePostmanError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no active postman found")
}

func emptySessionStatus(sessionName string) status.SessionStatus {
	result := status.SessionStatus{
		SchemaVersion: status.SchemaVersion,
		SessionName:   sessionName,
		Nodes:         []status.NodeStatus{},
		Windows:       []status.SessionWindow{},
	}
	enrichSessionStatus(&result, "", time.Now())
	return result
}

func emptyAllSessionStatus() status.AllSessionStatus {
	return status.AllSessionStatus{
		SchemaVersion: status.SchemaVersion,
		Sessions:      []status.SessionStatus{},
	}
}

// RunGetSessionStatus prints the canonical session status JSON payload (#220).
func RunGetSessionStatus(args []string) error {
	return runGetSessionStatusWithContext(defaultCommandContext(), args)
}

func runGetSessionStatusWithContext(ctx commandContext, args []string) error {
	ctx = ctx.withDefaults()
	fs := flag.NewFlagSet("get-status", flag.ExitOnError)
	fs.SetOutput(ctx.stderr)
	cliutil.SetUsageWithoutContextID(fs)
	contextID := fs.String("context-id", "", "Context ID (optional, auto-resolved from tmux session)")
	configPath := fs.String("config", "", "Config file path")
	debug := fs.Bool("debug", false, "Include point-in-time daemon runtime memory, GC, goroutine, and cardinality diagnostics")
	if err := fs.Parse(args); err != nil {
		return err
	}

	result, ok, err := collectResolvedSessionStatusWithContext(ctx, *contextID, "", *configPath, sessionStatusOptions{
		IncludeRuntimeDiagnostics: *debug,
	})
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("tmux session name required (run inside tmux)")
	}

	enc := json.NewEncoder(ctx.stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

type sessionStatusTarget struct {
	cfg         *config.Config
	baseDir     string
	contextID   string
	sessionName string
}

type sessionStatusCollector func(baseDir, contextID, sessionName string, cfg *config.Config) (status.SessionStatus, error)

type sessionStatusOptions struct {
	IncludeRuntimeDiagnostics bool
}

func resolveSessionStatusTarget(contextIDFlag, sessionFlag, configPath string) (sessionStatusTarget, bool, error) {
	return resolveSessionStatusTargetWithContext(defaultCommandContext(), contextIDFlag, sessionFlag, configPath)
}

func resolveSessionStatusTargetWithContext(ctx commandContext, contextIDFlag, sessionFlag, configPath string) (sessionStatusTarget, bool, error) {
	ctx = ctx.withDefaults()
	cfg, err := ctx.loadConfig(configPath)
	if err != nil {
		return sessionStatusTarget{}, false, fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	sessionName := sessionFlag
	if sessionName == "" {
		sessionName = ctx.getTmuxSessionName()
	}
	if sessionName == "" {
		return sessionStatusTarget{}, false, nil
	}
	sessionName, err = config.ValidateSessionName(sessionName)
	if err != nil {
		return sessionStatusTarget{}, false, err
	}

	resolvedContextID := contextIDFlag
	if resolvedContextID != "" {
		resolvedContextID, err = ctx.resolveContextID(resolvedContextID)
		if err != nil {
			return sessionStatusTarget{}, false, err
		}
	} else {
		resolvedContextID, err = ctx.resolveContextSession(baseDir, sessionName)
		if err != nil {
			if isNoActivePostmanError(err) {
				return sessionStatusTarget{
					cfg:         cfg,
					baseDir:     baseDir,
					sessionName: sessionName,
				}, true, err
			}
			return sessionStatusTarget{}, false, err
		}
	}

	return sessionStatusTarget{
		cfg:         cfg,
		baseDir:     baseDir,
		contextID:   resolvedContextID,
		sessionName: sessionName,
	}, true, nil
}

func collectResolvedSessionStatus(contextIDFlag, sessionFlag, configPath string) (status.SessionStatus, bool, error) {
	return collectResolvedSessionStatusWithOptions(contextIDFlag, sessionFlag, configPath, sessionStatusOptions{})
}

func collectResolvedSessionStatusWithOptions(contextIDFlag, sessionFlag, configPath string, options sessionStatusOptions) (status.SessionStatus, bool, error) {
	return collectResolvedSessionStatusWithContext(defaultCommandContext(), contextIDFlag, sessionFlag, configPath, options)
}

func collectResolvedSessionStatusWithContext(ctx commandContext, contextIDFlag, sessionFlag, configPath string, options sessionStatusOptions) (status.SessionStatus, bool, error) {
	ctx = ctx.withDefaults()
	target, ok, err := resolveSessionStatusTargetWithContext(ctx, contextIDFlag, sessionFlag, configPath)
	if isNoActivePostmanError(err) {
		return emptySessionStatus(target.sessionName), true, nil
	}
	if err != nil || !ok {
		return status.SessionStatus{}, ok, err
	}

	result, err := ctx.collectSessionStatus(target.baseDir, target.contextID, target.sessionName, target.cfg)
	if err != nil {
		return status.SessionStatus{}, true, err
	}
	normalizeSessionStatus(&result)
	if options.IncludeRuntimeDiagnostics {
		diagnostics, err := collectRuntimeDiagnosticsFromDaemonWithContext(ctx, target)
		if err != nil {
			return status.SessionStatus{}, true, err
		}
		result.RuntimeDiagnostics = diagnostics
	}
	return result, true, nil
}

func collectRuntimeDiagnosticsFromDaemon(target sessionStatusTarget) (*status.RuntimeDiagnostics, error) {
	return collectRuntimeDiagnosticsFromDaemonWithContext(defaultCommandContext(), target)
}

func collectRuntimeDiagnosticsFromDaemonWithContext(ctx commandContext, target sessionStatusTarget) (*status.RuntimeDiagnostics, error) {
	ctx = ctx.withDefaults()
	if target.baseDir == "" || target.contextID == "" || target.sessionName == "" {
		return nil, fmt.Errorf("runtime diagnostics require an active daemon context")
	}
	sessionDir := filepath.Join(target.baseDir, target.contextID, target.sessionName)
	response, err := ctx.roundTripDaemonSubmit(sessionDir, projection.DaemonSubmitRequest{
		Command: projection.DaemonSubmitRuntimeDiagnostics,
	}, daemonSubmitTimeout(target.cfg.TmuxTimeout))
	if err != nil {
		return nil, err
	}
	if response.RuntimeDiagnostics == nil {
		return nil, fmt.Errorf("daemon runtime diagnostics response missing payload")
	}
	return response.RuntimeDiagnostics, nil
}

func unavailableSessionStatus(contextID, sessionName string) status.SessionStatus {
	result := status.SessionStatus{
		SchemaVersion: status.SchemaVersion,
		ContextID:     contextID,
		SessionName:   sessionName,
	}
	result.VisibleState = "unavailable"
	result.Compact = compactSessionStatusMark(result.VisibleState)
	enrichSessionStatus(&result, "", time.Now())
	return result
}

func projectedSessionStatus(sessionDir string) (status.SessionStatus, bool) {
	projected, ok, err := projection.ProjectSessionStatus(sessionDir)
	if err != nil || !ok {
		return status.SessionStatus{}, false
	}
	enrichSessionStatus(&projected, sessionDir, time.Now())
	return projected, true
}

func recordSessionStatusSnapshot(sessionDir, sessionName string, snapshot status.SessionStatus, now time.Time) error {
	return journal.RecordProcessEvent(
		sessionDir,
		sessionName,
		projection.SessionStatusSnapshotEventType,
		journal.VisibilityControlPlaneOnly,
		snapshot,
		now,
	)
}

func refreshProjectedSessionStatus(baseDir, contextID, sessionName string, cfg *config.Config) (status.SessionStatus, error) {
	if !ownsCanonicalSessionStatus(baseDir, contextID, sessionName) {
		return unavailableSessionStatus(contextID, sessionName), nil
	}

	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	projected, projectedOK := projectedSessionStatus(sessionDir)

	live, err := collectLiveSessionStatus(baseDir, contextID, sessionName, cfg)
	if err == nil {
		if !projectedOK || !reflect.DeepEqual(projected, live) {
			if recordErr := recordSessionStatusSnapshot(sessionDir, sessionName, live, time.Now()); recordErr == nil {
				projected, projectedOK = projectedSessionStatus(sessionDir)
			}
		}
		if projectedOK {
			return projected, nil
		}
		return live, nil
	}

	if projectedOK {
		return projected, nil
	}

	return status.SessionStatus{}, err
}

func collectAllSessionStatus(contextIDFlag, sessionFlag, configPath string) (status.AllSessionStatus, *config.Config, bool, error) {
	return collectAllSessionStatusWithCollector(contextIDFlag, sessionFlag, configPath, collectSessionStatus)
}

func collectAllLiveSessionStatus(contextIDFlag, sessionFlag, configPath string) (status.AllSessionStatus, *config.Config, bool, error) {
	return collectAllSessionStatusWithCollector(contextIDFlag, sessionFlag, configPath, collectLiveSessionStatus)
}

func collectAllSessionStatusWithCollector(contextIDFlag, sessionFlag, configPath string, collector sessionStatusCollector) (status.AllSessionStatus, *config.Config, bool, error) {
	return collectAllSessionStatusWithContext(defaultCommandContext(), contextIDFlag, sessionFlag, configPath, collector)
}

func collectAllSessionStatusWithContext(ctx commandContext, contextIDFlag, sessionFlag, configPath string, collector sessionStatusCollector) (status.AllSessionStatus, *config.Config, bool, error) {
	ctx = ctx.withDefaults()
	cfg, err := ctx.loadConfig(configPath)
	if err != nil {
		return status.AllSessionStatus{}, nil, false, fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)
	resolvedContextID := contextIDFlag
	if resolvedContextID != "" {
		resolvedContextID, err = ctx.resolveContextID(resolvedContextID)
		if err != nil {
			return status.AllSessionStatus{}, nil, false, err
		}
	} else {
		sessionName := sessionFlag
		if sessionName == "" {
			sessionName = ctx.getTmuxSessionName()
		}
		if sessionName == "" {
			return status.AllSessionStatus{}, cfg, false, nil
		}
		sessionName, err = config.ValidateSessionName(sessionName)
		if err != nil {
			return status.AllSessionStatus{}, nil, false, err
		}
		resolvedContextID, err = ctx.resolveContextSession(baseDir, sessionName)
		if err != nil {
			if isNoActivePostmanError(err) {
				return emptyAllSessionStatus(), cfg, true, nil
			}
			return status.AllSessionStatus{}, nil, false, err
		}
	}

	sessionNames, err := ctx.discoverAllSessions()
	if err != nil {
		return status.AllSessionStatus{}, nil, true, err
	}

	result := status.AllSessionStatus{
		SchemaVersion: status.SchemaVersion,
		ContextID:     resolvedContextID,
		Sessions:      make([]status.SessionStatus, 0, len(sessionNames)),
	}
	if ownerSession := config.FindContextSessionName(baseDir, resolvedContextID); ownerSession != "" {
		result.DaemonOwner = &status.DaemonOwner{
			ContextID:   resolvedContextID,
			SessionName: ownerSession,
		}
	}
	for _, sessionName := range sessionNames {
		sessionName, err = config.ValidateSessionName(sessionName)
		if err != nil {
			return status.AllSessionStatus{}, nil, true, err
		}
		sessionStatus, err := collector(baseDir, resolvedContextID, sessionName, cfg)
		if err != nil {
			return status.AllSessionStatus{}, nil, true, err
		}
		normalizeSessionStatus(&sessionStatus)
		result.Sessions = append(result.Sessions, sessionStatus)
	}

	return result, cfg, true, nil
}

func normalizeSessionStatus(sessionStatus *status.SessionStatus) {
	if sessionStatus.Nodes == nil {
		sessionStatus.Nodes = []status.NodeStatus{}
	}
	if sessionStatus.Windows == nil {
		sessionStatus.Windows = []status.SessionWindow{}
	}
	if sessionStatus.SchemaVersion == 0 || sessionStatus.CompactSeverity == "" {
		enrichSessionStatus(sessionStatus, "", time.Now())
	}
}
