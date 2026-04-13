package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

func isNoActivePostmanError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no active postman found")
}

func emptySessionHealth(sessionName string) status.SessionHealth {
	return status.SessionHealth{
		SessionName: sessionName,
		Nodes:       []status.NodeHealth{},
		Windows:     []status.SessionWindow{},
	}
}

func emptyAllSessionHealth() status.AllSessionHealth {
	return status.AllSessionHealth{
		Sessions: []status.SessionHealth{},
	}
}

// RunGetSessionHealth prints the canonical session-health JSON payload (#220).
func RunGetSessionHealth(args []string) error {
	fs := flag.NewFlagSet("get-health", flag.ExitOnError)
	contextID := fs.String("context-id", "", "Context ID (optional, auto-resolved from tmux session)")
	sessionFlag := fs.String("session", "", "tmux session name (optional, auto-detect if in tmux)")
	configPath := fs.String("config", "", "Config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	result, ok, err := collectResolvedSessionHealth(*contextID, *sessionFlag, *configPath)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session name required: run inside tmux or pass --session")
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

type sessionHealthTarget struct {
	cfg         *config.Config
	baseDir     string
	contextID   string
	sessionName string
}

type sessionHealthCollector func(baseDir, contextID, sessionName string, cfg *config.Config) (status.SessionHealth, error)

func resolveSessionHealthTarget(contextIDFlag, sessionFlag, configPath string) (sessionHealthTarget, bool, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return sessionHealthTarget{}, false, fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	sessionName := sessionFlag
	if sessionName == "" {
		sessionName = config.GetTmuxSessionName()
	}
	if sessionName == "" {
		return sessionHealthTarget{}, false, nil
	}
	sessionName, err = config.ValidateSessionName(sessionName)
	if err != nil {
		return sessionHealthTarget{}, false, err
	}

	resolvedContextID := contextIDFlag
	if resolvedContextID != "" {
		resolvedContextID, err = config.ResolveContextID(resolvedContextID)
		if err != nil {
			return sessionHealthTarget{}, false, err
		}
	} else {
		resolvedContextID, err = config.ResolveContextIDFromSession(baseDir, sessionName)
		if err != nil {
			if isNoActivePostmanError(err) {
				return sessionHealthTarget{
					cfg:         cfg,
					baseDir:     baseDir,
					sessionName: sessionName,
				}, true, err
			}
			return sessionHealthTarget{}, false, err
		}
	}

	return sessionHealthTarget{
		cfg:         cfg,
		baseDir:     baseDir,
		contextID:   resolvedContextID,
		sessionName: sessionName,
	}, true, nil
}

func collectResolvedSessionHealth(contextIDFlag, sessionFlag, configPath string) (status.SessionHealth, bool, error) {
	target, ok, err := resolveSessionHealthTarget(contextIDFlag, sessionFlag, configPath)
	if isNoActivePostmanError(err) {
		return emptySessionHealth(target.sessionName), true, nil
	}
	if err != nil || !ok {
		return status.SessionHealth{}, ok, err
	}

	result, err := collectSessionHealth(target.baseDir, target.contextID, target.sessionName, target.cfg)
	if err != nil {
		return status.SessionHealth{}, true, err
	}
	return result, true, nil
}

func unavailableSessionHealth(contextID, sessionName string) status.SessionHealth {
	result := status.SessionHealth{
		ContextID:   contextID,
		SessionName: sessionName,
	}
	result.VisibleState = "unavailable"
	result.Compact = compactSessionStatusMark(result.VisibleState)
	return result
}

func projectedSessionHealth(sessionDir string) (status.SessionHealth, bool) {
	projected, ok, err := projection.ProjectSessionHealth(sessionDir)
	if err != nil || !ok {
		return status.SessionHealth{}, false
	}
	return projected, true
}

func recordSessionHealthSnapshot(sessionDir, sessionName string, health status.SessionHealth, now time.Time) error {
	return journal.RecordProcessEvent(
		sessionDir,
		sessionName,
		projection.SessionHealthSnapshotEventType,
		journal.VisibilityControlPlaneOnly,
		health,
		now,
	)
}

func refreshProjectedSessionHealth(baseDir, contextID, sessionName string, cfg *config.Config) (status.SessionHealth, error) {
	mode, err := config.ResolveJournalCutoverMode(cfg)
	if err != nil {
		return status.SessionHealth{}, err
	}
	if !ownsCanonicalSessionHealth(baseDir, contextID, sessionName) {
		return unavailableSessionHealth(contextID, sessionName), nil
	}

	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	projected, projectedOK := projectedSessionHealth(sessionDir)

	legacy, err := collectSessionHealthLegacy(baseDir, contextID, sessionName, cfg)
	if err == nil {
		if !projectedOK || !reflect.DeepEqual(projected, legacy) {
			if recordErr := recordSessionHealthSnapshot(sessionDir, sessionName, legacy, time.Now()); recordErr == nil {
				projected, projectedOK = projectedSessionHealth(sessionDir)
			}
		}
		if mode == config.JournalCutoverLegacy {
			return legacy, nil
		}
		if projectedOK {
			return projected, nil
		}
		return legacy, nil
	}

	if mode == config.JournalCutoverLegacy {
		return status.SessionHealth{}, err
	}
	if projectedOK {
		return projected, nil
	}

	return status.SessionHealth{}, err
}

func collectAllSessionHealth(contextIDFlag, sessionFlag, configPath string) (status.AllSessionHealth, *config.Config, bool, error) {
	return collectAllSessionHealthWithCollector(contextIDFlag, sessionFlag, configPath, collectSessionHealth)
}

func collectAllSessionHealthLegacy(contextIDFlag, sessionFlag, configPath string) (status.AllSessionHealth, *config.Config, bool, error) {
	return collectAllSessionHealthWithCollector(contextIDFlag, sessionFlag, configPath, collectSessionHealthLegacy)
}

func collectAllSessionHealthWithCollector(contextIDFlag, sessionFlag, configPath string, collector sessionHealthCollector) (status.AllSessionHealth, *config.Config, bool, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return status.AllSessionHealth{}, nil, false, fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)
	resolvedContextID := contextIDFlag
	if resolvedContextID != "" {
		resolvedContextID, err = config.ResolveContextID(resolvedContextID)
		if err != nil {
			return status.AllSessionHealth{}, nil, false, err
		}
	} else {
		sessionName := sessionFlag
		if sessionName == "" {
			sessionName = config.GetTmuxSessionName()
		}
		if sessionName == "" {
			return status.AllSessionHealth{}, cfg, false, nil
		}
		sessionName, err = config.ValidateSessionName(sessionName)
		if err != nil {
			return status.AllSessionHealth{}, nil, false, err
		}
		resolvedContextID, err = config.ResolveContextIDFromSession(baseDir, sessionName)
		if err != nil {
			if isNoActivePostmanError(err) {
				return emptyAllSessionHealth(), cfg, true, nil
			}
			return status.AllSessionHealth{}, nil, false, err
		}
	}

	sessionNames, err := discovery.DiscoverAllSessions()
	if err != nil {
		return status.AllSessionHealth{}, nil, true, err
	}

	result := status.AllSessionHealth{
		ContextID: resolvedContextID,
		Sessions:  make([]status.SessionHealth, 0, len(sessionNames)),
	}
	for _, sessionName := range sessionNames {
		sessionName, err = config.ValidateSessionName(sessionName)
		if err != nil {
			return status.AllSessionHealth{}, nil, true, err
		}
		health, err := collector(baseDir, resolvedContextID, sessionName, cfg)
		if err != nil {
			return status.AllSessionHealth{}, nil, true, err
		}
		result.Sessions = append(result.Sessions, health)
	}

	return result, cfg, true, nil
}
