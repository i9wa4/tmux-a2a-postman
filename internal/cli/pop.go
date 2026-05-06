package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"gopkg.in/yaml.v3"
)

// RunPop reads and optionally archives the oldest unread inbox message (#277).
func RunPop(args []string) error {
	fs := flag.NewFlagSet("pop", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	contextID := fs.String("context-id", "", "context ID") // Issue #315: forward global --context-id
	configPath := fs.String("config", "", "path to config file (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	inboxArgs := fs.Args()
	if *contextID != "" {
		inboxArgs = append([]string{"--context-id", *contextID}, inboxArgs...)
	}
	if *configPath != "" {
		inboxArgs = append([]string{"--config", *configPath}, inboxArgs...)
	}
	inboxPath, err := cliutil.ResolveInboxPath(inboxArgs)
	if err != nil {
		return err
	}
	sessionDir := filepath.Dir(filepath.Dir(inboxPath))
	contextDir := filepath.Dir(sessionDir)
	resolvedContextID := filepath.Base(contextDir)
	baseDir := filepath.Dir(contextDir)
	sessionName := filepath.Base(sessionDir)
	nodeName := filepath.Base(inboxPath)

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if config.ContextOwnsSession(baseDir, resolvedContextID, sessionName) {
		response, err := roundTripDaemonSubmit(sessionDir, projection.DaemonSubmitRequest{
			Command: projection.DaemonSubmitPop,
			Node:    nodeName,
		}, daemonSubmitTimeout(cfg.TmuxTimeout))
		if err != nil {
			return fmt.Errorf("daemon submit pop: %w", err)
		}
		if response.Empty {
			return writeEmptyPopOutput()
		}
		remaining := response.UnreadBefore - 1
		if remaining < 0 {
			remaining = 0
		}
		markdownPath := response.MarkdownPath
		if markdownPath == "" && response.Filename != "" {
			markdownPath = filepath.Join(sessionDir, "read", response.Filename)
		}
		return writePopMessageOutput(response.Content, response.Filename, markdownPath, intPtr(response.UnreadBefore), intPtr(remaining))
	}

	msgs := message.ScanInboxMessages(inboxPath)
	if len(msgs) == 0 {
		return writeEmptyPopOutput()
	}
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].Filename < msgs[j].Filename
	})

	abs := filepath.Join(inboxPath, msgs[0].Filename)
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			// Race: file disappeared between listing and reading; retry once.
			msgs = message.ScanInboxMessages(inboxPath)
			if len(msgs) == 0 {
				return writeEmptyPopOutput()
			}
			sort.Slice(msgs, func(i, j int) bool {
				return msgs[i].Filename < msgs[j].Filename
			})
			abs = filepath.Join(inboxPath, msgs[0].Filename)
			data, err = os.ReadFile(abs)
			if err != nil {
				if os.IsNotExist(err) {
					return writeEmptyPopOutput()
				}
				return fmt.Errorf("reading message: %w", err)
			}
		} else {
			return fmt.Errorf("reading message: %w", err)
		}
	}

	remaining := len(msgs)
	readPath, err := archivePoppedMessage(abs, msgs[0].Filename)
	if err != nil {
		return err
	}
	remaining--
	return writePopMessageOutput(string(data), msgs[0].Filename, readPath, intPtr(len(msgs)), intPtr(remaining))
}

func archivePoppedMessage(absPath, filename string) (string, error) {
	return message.ArchiveInboxMessage(absPath, filename)
}

type popEmptyOutput struct {
	Status string `json:"status"`
}

type popMessageOutput struct {
	Status              string         `json:"status"`
	MessageID           string         `json:"message_id,omitempty"`
	MarkdownPath        string         `json:"markdown_path,omitempty"`
	Frontmatter         map[string]any `json:"frontmatter,omitempty"`
	From                string         `json:"from"`
	To                  string         `json:"to"`
	ReplyPolicy         string         `json:"reply_policy,omitempty"`
	ReplyTo             string         `json:"reply_to,omitempty"`
	InputRequestID      string         `json:"input_request_id,omitempty"`
	FillsInputRequestID string         `json:"fills_input_request_id,omitempty"`
	InputRequestSetID   string         `json:"input_request_set_id,omitempty"`
	BranchID            string         `json:"branch_id,omitempty"`
	CompletionRule      string         `json:"completion_rule,omitempty"`
	Timestamp           string         `json:"timestamp"`
	UnreadBefore        *int           `json:"unread_before,omitempty"`
	Remaining           *int           `json:"remaining,omitempty"`
}

func writeEmptyPopOutput() error {
	return json.NewEncoder(os.Stdout).Encode(popEmptyOutput{Status: "empty"})
}

func writePopMessageOutput(content, filename, markdownPath string, unreadBefore, remaining *int) error {
	output := parseMessageContent(content, filename)
	output.MarkdownPath = markdownPath
	output.UnreadBefore = unreadBefore
	output.Remaining = remaining
	return json.NewEncoder(os.Stdout).Encode(output)
}

func intPtr(value int) *int {
	return &value
}

func parseMessageContent(content, filename string) popMessageOutput {
	result := popMessageOutput{
		Status:      "message",
		MessageID:   filename,
		Frontmatter: frontmatterFromContent(content),
	}
	metadata, err := envelope.ParseMetadata(content)
	if err != nil {
		return result
	}
	result.From = metadata.From
	result.To = metadata.To
	result.MessageID = metadata.MessageID
	if result.MessageID == "" {
		result.MessageID = filename
	}
	result.ReplyPolicy = metadata.ReplyPolicy
	result.ReplyTo = metadata.ReplyTo
	result.InputRequestID = metadata.InputRequestID
	result.FillsInputRequestID = metadata.FillsInputRequestID
	result.InputRequestSetID = metadata.InputRequestSetID
	result.BranchID = metadata.BranchID
	result.CompletionRule = metadata.CompletionRule
	result.Timestamp = metadata.Timestamp
	return result
}

func frontmatterFromContent(content string) map[string]any {
	first := strings.Index(content, "---\n")
	if first < 0 {
		return nil
	}
	rest := content[first+4:]
	second := strings.Index(rest, "\n---")
	if second < 0 {
		return nil
	}
	var frontmatter map[string]any
	if err := yaml.Unmarshal([]byte(rest[:second]), &frontmatter); err != nil {
		return nil
	}
	return frontmatter
}
