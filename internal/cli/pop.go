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
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
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
		return writePopMessageOutput(response.Content, response.Filename, intPtr(response.UnreadBefore), intPtr(remaining))
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
	if _, err := archivePoppedMessage(abs, msgs[0].Filename); err != nil {
		return err
	}
	remaining--
	return writePopMessageOutput(string(data), msgs[0].Filename, intPtr(len(msgs)), intPtr(remaining))
}

func archivePoppedMessage(absPath, filename string) (string, error) {
	return message.ArchiveInboxMessage(absPath, filename)
}

type popEmptyOutput struct {
	Status string `json:"status"`
}

type popMessageOutput struct {
	Status       string `json:"status"`
	ID           string `json:"id"`
	From         string `json:"from"`
	To           string `json:"to"`
	ReplyPolicy  string `json:"reply_policy,omitempty"`
	ReplyTo      string `json:"reply_to,omitempty"`
	Timestamp    string `json:"timestamp"`
	Body         string `json:"body"`
	Content      string `json:"content"`
	UnreadBefore *int   `json:"unread_before,omitempty"`
	Remaining    *int   `json:"remaining,omitempty"`
}

func writeEmptyPopOutput() error {
	return json.NewEncoder(os.Stdout).Encode(popEmptyOutput{Status: "empty"})
}

func writePopMessageOutput(content, filename string, unreadBefore, remaining *int) error {
	output := parseMessageContent(content, filename)
	output.Content = content
	output.UnreadBefore = unreadBefore
	output.Remaining = remaining
	return json.NewEncoder(os.Stdout).Encode(output)
}

func intPtr(value int) *int {
	return &value
}

// parseMessageContent extracts JSON-friendly fields from raw message file content.
// Parses YAML frontmatter for from/to/timestamp; body is content after frontmatter.
func parseMessageContent(content, filename string) popMessageOutput {
	result := popMessageOutput{Status: "message", ID: filename}
	lines := strings.Split(content, "\n")
	inFrontMatter := false
	fmEnd := -1
	for i, line := range lines {
		if line == "---" {
			if !inFrontMatter {
				inFrontMatter = true
				continue
			}
			fmEnd = i
			break
		}
		if !inFrontMatter {
			continue
		}
		if strings.HasPrefix(line, "  from: ") {
			result.From = strings.TrimSpace(strings.TrimPrefix(line, "  from: "))
		} else if strings.HasPrefix(line, "  to: ") {
			result.To = strings.TrimSpace(strings.TrimPrefix(line, "  to: "))
		} else if strings.HasPrefix(line, "  replyPolicy: ") {
			result.ReplyPolicy = strings.TrimSpace(strings.TrimPrefix(line, "  replyPolicy: "))
		} else if strings.HasPrefix(line, "  reply_policy: ") {
			result.ReplyPolicy = strings.TrimSpace(strings.TrimPrefix(line, "  reply_policy: "))
		} else if strings.HasPrefix(line, "  replyTo: ") {
			result.ReplyTo = strings.TrimSpace(strings.TrimPrefix(line, "  replyTo: "))
		} else if strings.HasPrefix(line, "  reply_to: ") {
			result.ReplyTo = strings.TrimSpace(strings.TrimPrefix(line, "  reply_to: "))
		} else if strings.HasPrefix(line, "  timestamp: ") {
			result.Timestamp = strings.TrimSpace(strings.TrimPrefix(line, "  timestamp: "))
		}
	}
	if fmEnd >= 0 && fmEnd+1 < len(lines) {
		result.Body = strings.TrimSpace(strings.Join(lines[fmEnd+1:], "\n"))
	}
	return result
}
