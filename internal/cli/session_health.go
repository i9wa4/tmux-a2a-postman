package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

// RunGetSessionHealth prints session health: node count, inbox/waiting counts (#220).
func RunGetSessionHealth(args []string) error {
	fs := flag.NewFlagSet("get-session-health", flag.ExitOnError)
	contextID := fs.String("context-id", "", "Context ID (optional, auto-resolved from tmux session)")
	sessionFlag := fs.String("session", "", "tmux session name (optional, auto-detect if in tmux)")
	configPath := fs.String("config", "", "Config file path")
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
		return fmt.Errorf("session name required: run inside tmux or pass --session")
	}
	sessionName, err = config.ValidateSessionName(sessionName)
	if err != nil {
		return err
	}

	var resolvedContextID string
	if *contextID != "" {
		resolvedContextID, err = config.ResolveContextID(*contextID)
		if err != nil {
			return err
		}
	} else {
		resolvedContextID, err = config.ResolveContextIDFromSession(baseDir, sessionName)
		if err != nil {
			return err
		}
	}

	result, err := collectSessionHealth(baseDir, resolvedContextID, sessionName, cfg)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
