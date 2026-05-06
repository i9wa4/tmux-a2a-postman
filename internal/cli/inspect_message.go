package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
)

type inspectMessageOutput struct {
	Status      string                `json:"status"`
	ID          string                `json:"id"`
	MatchCount  int                   `json:"match_count"`
	Message     *inspectMessageMatch  `json:"message,omitempty"`
	Matches     []inspectMessageMatch `json:"matches,omitempty"`
	ContextID   string                `json:"context_id,omitempty"`
	SessionName string                `json:"session_name,omitempty"`
}

type inspectMessageMatch struct {
	MessageID           string         `json:"message_id"`
	MarkdownPath        string         `json:"markdown_path"`
	StorageState        string         `json:"storage_state"`
	Node                string         `json:"node,omitempty"`
	Frontmatter         map[string]any `json:"frontmatter,omitempty"`
	From                string         `json:"from,omitempty"`
	To                  string         `json:"to,omitempty"`
	ReplyPolicy         string         `json:"reply_policy,omitempty"`
	ReplyTo             string         `json:"reply_to,omitempty"`
	InputRequestID      string         `json:"input_request_id,omitempty"`
	FillsInputRequestID string         `json:"fills_input_request_id,omitempty"`
	InputRequestSetID   string         `json:"input_request_set_id,omitempty"`
	BranchID            string         `json:"branch_id,omitempty"`
	CompletionRule      string         `json:"completion_rule,omitempty"`
	Timestamp           string         `json:"timestamp,omitempty"`
}

func RunInspectMessage(args []string) error {
	fs := flag.NewFlagSet("inspect-message", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	contextID := fs.String("context-id", "", "Context ID (optional, auto-resolved from tmux session)")
	configPath := fs.String("config", "", "Config file path")
	sessionName := fs.String("session", "", "tmux session name (optional, defaults to current tmux session)")
	id := fs.String("id", "", "message_id to inspect")
	jsonOutput := fs.Bool("json", false, "print structured JSON output (default)")
	pathOnly := fs.Bool("path", false, "print only the matched Markdown path")
	bodyOnly := fs.Bool("body", false, "print only the matched Markdown body")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	if (*pathOnly || *bodyOnly) && *jsonOutput {
		return fmt.Errorf("--json cannot be combined with --path or --body")
	}
	if *pathOnly && *bodyOnly {
		return fmt.Errorf("--path and --body are mutually exclusive")
	}

	sessionDir, resolvedContextID, resolvedSessionName, err := resolveInspectMessageSessionDir(*contextID, *sessionName, *configPath)
	if err != nil {
		return err
	}
	matches, err := findInspectMessageMatches(sessionDir, *id)
	if err != nil {
		return err
	}

	output := inspectMessageOutput{
		Status:      "not_found",
		ID:          *id,
		MatchCount:  len(matches),
		ContextID:   resolvedContextID,
		SessionName: resolvedSessionName,
	}
	switch len(matches) {
	case 0:
	case 1:
		output.Status = "found"
		output.Message = &matches[0]
	default:
		output.Status = "ambiguous"
		output.Matches = matches
	}

	if *pathOnly || *bodyOnly {
		return writeInspectMessagePlainOutput(output, *pathOnly, *bodyOnly)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func resolveInspectMessageSessionDir(contextID, sessionName, configPath string) (string, string, string, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return "", "", "", fmt.Errorf("loading config: %w", err)
	}
	baseDir := config.ResolveBaseDir(cfg.BaseDir)
	if sessionName == "" {
		sessionName = config.GetTmuxSessionName()
		if sessionName == "" {
			return "", "", "", fmt.Errorf("tmux session name required (run inside tmux or pass --session)")
		}
	}
	sessionName, err = config.ValidateSessionName(sessionName)
	if err != nil {
		return "", "", "", err
	}

	if contextID != "" {
		contextID, err = config.ResolveContextID(contextID)
	} else {
		contextID, err = config.ResolveContextIDFromSession(baseDir, sessionName)
	}
	if err != nil {
		return "", "", "", err
	}

	return filepath.Join(baseDir, contextID, sessionName), contextID, sessionName, nil
}

func findInspectMessageMatches(sessionDir, id string) ([]inspectMessageMatch, error) {
	var matches []inspectMessageMatch
	if err := inspectMessageReadDir(filepath.Join(sessionDir, "read"), id, &matches); err != nil {
		return nil, err
	}
	if err := inspectMessageInboxDir(filepath.Join(sessionDir, "inbox"), id, &matches); err != nil {
		return nil, err
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].MarkdownPath < matches[j].MarkdownPath
	})
	return matches, nil
}

func inspectMessageReadDir(readDir, id string, matches *[]inspectMessageMatch) error {
	entries, err := os.ReadDir(readDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading read directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		if err := appendInspectMessageMatch(filepath.Join(readDir, entry.Name()), entry.Name(), "read", "", id, matches); err != nil {
			return err
		}
	}
	return nil
}

func inspectMessageInboxDir(inboxDir, id string, matches *[]inspectMessageMatch) error {
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading inbox directory: %w", err)
	}
	for _, nodeEntry := range entries {
		if !nodeEntry.IsDir() {
			continue
		}
		nodeName := nodeEntry.Name()
		nodeInboxDir := filepath.Join(inboxDir, nodeName)
		if err := filepath.WalkDir(nodeInboxDir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
				return nil
			}
			return appendInspectMessageMatch(path, entry.Name(), "unread", nodeName, id, matches)
		}); err != nil {
			return fmt.Errorf("reading inbox directory: %w", err)
		}
	}
	return nil
}

func appendInspectMessageMatch(path, filename, storageState, nodeName, id string, matches *[]inspectMessageMatch) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading message: %w", err)
	}
	if !inspectMessageMatchesID(string(content), filename, id) {
		return nil
	}
	payload := parseMessageContent(string(content), filename)
	*matches = append(*matches, inspectMessageMatch{
		MessageID:           payload.MessageID,
		MarkdownPath:        path,
		StorageState:        storageState,
		Node:                nodeName,
		Frontmatter:         payload.Frontmatter,
		From:                payload.From,
		To:                  payload.To,
		ReplyPolicy:         payload.ReplyPolicy,
		ReplyTo:             payload.ReplyTo,
		InputRequestID:      payload.InputRequestID,
		FillsInputRequestID: payload.FillsInputRequestID,
		InputRequestSetID:   payload.InputRequestSetID,
		BranchID:            payload.BranchID,
		CompletionRule:      payload.CompletionRule,
		Timestamp:           payload.Timestamp,
	})
	return nil
}

func inspectMessageMatchesID(content, filename, id string) bool {
	if filename == id {
		return true
	}
	metadata, err := envelope.ParseMetadata(content)
	if err != nil {
		return false
	}
	return metadata.MessageID == id
}

func writeInspectMessagePlainOutput(output inspectMessageOutput, pathOnly, bodyOnly bool) error {
	if output.Status != "found" || output.Message == nil {
		return fmt.Errorf("%s: message id %q matched %d files", output.Status, output.ID, output.MatchCount)
	}
	if pathOnly {
		fmt.Fprintln(os.Stdout, output.Message.MarkdownPath)
		return nil
	}
	if bodyOnly {
		content, err := os.ReadFile(output.Message.MarkdownPath)
		if err != nil {
			return fmt.Errorf("reading message body: %w", err)
		}
		fmt.Fprintln(os.Stdout, envelope.BodyFromContent(string(content)))
		return nil
	}
	return nil
}
