package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

type readOnlySessionTarget struct {
	cfg         *config.Config
	baseDir     string
	contextID   string
	sessionName string
}

func resolveReadOnlySessionTarget(contextIDFlag, sessionFlag, configPath string) (readOnlySessionTarget, bool, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return readOnlySessionTarget{}, false, fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)
	sessionName := sessionFlag
	if sessionName == "" {
		sessionName = config.GetTmuxSessionName()
	}
	if sessionName == "" {
		return readOnlySessionTarget{}, false, nil
	}
	sessionName, err = config.ValidateSessionName(sessionName)
	if err != nil {
		return readOnlySessionTarget{}, false, err
	}

	resolvedContextID := contextIDFlag
	if resolvedContextID != "" {
		resolvedContextID, err = config.ResolveContextID(resolvedContextID)
		if err != nil {
			return readOnlySessionTarget{}, false, err
		}
	} else {
		resolvedContextID, err = config.ResolveContextIDFromSession(baseDir, sessionName)
		if err != nil {
			return readOnlySessionTarget{}, false, err
		}
	}

	return readOnlySessionTarget{
		cfg:         cfg,
		baseDir:     baseDir,
		contextID:   resolvedContextID,
		sessionName: sessionName,
	}, true, nil
}

func RunReplay(args []string) error {
	return runReplay(os.Stdout, args)
}

func runReplay(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	contextID := fs.String("context-id", "", "context ID (optional, auto-resolve only when a live daemon owns the session)")
	sessionName := fs.String("session", "", "tmux session name (optional, auto-detect if in tmux)")
	configPath := fs.String("config", "", "config file path")
	surface := fs.String("surface", "all", "projection surface: all, health, mailbox, approval")
	if err := fs.Parse(args); err != nil {
		return err
	}

	target, ok, err := resolveReadOnlySessionTarget(*contextID, *sessionName, *configPath)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("session name required: run inside tmux or pass --session")
	}

	sessionDir := filepath.Join(target.baseDir, target.contextID, target.sessionName)
	response, err := buildReplayResponse(sessionDir, *surface)
	if err != nil {
		return err
	}
	response.ContextID = target.contextID
	response.SessionName = target.sessionName

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(response)
}

type replayResponse struct {
	ContextID   string                 `json:"context_id"`
	SessionName string                 `json:"session_name"`
	Surface     string                 `json:"surface"`
	Health      interface{}            `json:"health,omitempty"`
	Mailbox     *replayMailboxResponse `json:"mailbox,omitempty"`
	Approval    interface{}            `json:"approval,omitempty"`
}

type replayMailboxResponse struct {
	Post       []string `json:"post"`
	Inbox      []string `json:"inbox"`
	Read       []string `json:"read"`
	Waiting    []string `json:"waiting"`
	DeadLetter []string `json:"dead_letter"`
}

func buildReplayResponse(sessionDir, surface string) (replayResponse, error) {
	response := replayResponse{Surface: surface}

	switch surface {
	case "all":
		health, ok, err := projection.ProjectSessionHealth(sessionDir)
		if err != nil {
			return replayResponse{}, fmt.Errorf("replay health: %w", err)
		}
		if ok {
			response.Health = health
		}

		mailbox, ok, err := projection.ProjectCompatibilityMailbox(sessionDir)
		if err != nil {
			return replayResponse{}, fmt.Errorf("replay mailbox: %w", err)
		}
		if ok {
			response.Mailbox = sanitizeReplayMailbox(mailbox)
		}

		approval, ok, err := projection.ProjectThreadApproval(sessionDir)
		if err != nil {
			return replayResponse{}, fmt.Errorf("replay approval: %w", err)
		}
		if ok {
			response.Approval = approval
		}
		if response.Health == nil && response.Mailbox == nil && response.Approval == nil {
			return replayResponse{}, fmt.Errorf("no replayable journal projection found")
		}
		return response, nil
	case "health":
		health, ok, err := projection.ProjectSessionHealth(sessionDir)
		if err != nil {
			return replayResponse{}, fmt.Errorf("replay health: %w", err)
		}
		if !ok {
			return replayResponse{}, fmt.Errorf("no replayable health projection found")
		}
		response.Health = health
		return response, nil
	case "mailbox":
		mailbox, ok, err := projection.ProjectCompatibilityMailbox(sessionDir)
		if err != nil {
			return replayResponse{}, fmt.Errorf("replay mailbox: %w", err)
		}
		if !ok {
			return replayResponse{}, fmt.Errorf("no replayable mailbox projection found")
		}
		response.Mailbox = sanitizeReplayMailbox(mailbox)
		return response, nil
	case "approval":
		approval, ok, err := projection.ProjectThreadApproval(sessionDir)
		if err != nil {
			return replayResponse{}, fmt.Errorf("replay approval: %w", err)
		}
		if !ok {
			return replayResponse{}, fmt.Errorf("no replayable approval projection found")
		}
		response.Approval = approval
		return response, nil
	default:
		return replayResponse{}, fmt.Errorf("unknown replay surface %q", surface)
	}
}

func sanitizeReplayMailbox(projected projection.CompatibilityMailbox) *replayMailboxResponse {
	return &replayMailboxResponse{
		Post:       projectedFilePaths(projected.Post),
		Inbox:      projectedFilePaths(projected.Inbox),
		Read:       projectedFilePaths(projected.Read),
		Waiting:    projectedFilePaths(projected.Waiting),
		DeadLetter: projectedFilePaths(projected.DeadLetter),
	}
}

func projectedFilePaths(files map[string]projection.ProjectedFile) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	sort.Strings(paths)
	return paths
}
