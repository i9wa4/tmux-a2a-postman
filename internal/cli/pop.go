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
	"github.com/i9wa4/tmux-a2a-postman/internal/readcontext"
)

// RunPop reads and optionally archives the oldest unread inbox message (#277).
func RunPop(args []string) error {
	fs := flag.NewFlagSet("pop", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	// Options struct fields (--params scope): peek, json
	// SYNC: schema pop properties; alwaysExcludedParams map
	peek := fs.Bool("peek", false, "show without archiving (non-destructive)")
	jsonOut := fs.Bool("json", false, `output json: {} (empty inbox) or {"id":"...","from":"...","to":"...","body":"...","timestamp":"..."} (message present); test id field to distinguish`)
	paramsFlag := fs.String("params", "", "command parameters as JSON or shorthand (k=v,k=v)")
	// NOTE: always-excluded from --params scope (SYNC: alwaysExcludedParams map)
	contextID := fs.String("context-id", "", "context ID") // Issue #315: forward global --context-id
	configPath := fs.String("config", "", "path to config file (optional)")
	file := fs.String("file", "", "print a specific inbox message by filename from the current session inbox (non-destructive)")
	commandName := fs.Name()
	// Step 1: parse flags
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Step 2: record explicitly-set flags (for --params precedence)
	explicitlySet := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		explicitlySet[f.Name] = true
	})
	// Steps 3+4: parse and apply --params to non-explicit flags
	if explicitlySet["params"] {
		resolvedParams, err := cliutil.ParseParams(*paramsFlag)
		if err != nil {
			return err
		}
		if err := cliutil.ApplyParams(fs, resolvedParams, explicitlySet, commandName); err != nil {
			return err
		}
	}

	if *file != "" {
		if strings.ContainsAny(*file, "/\\") {
			return fmt.Errorf("pop --file: filename must not contain path separators")
		}
		if *contextID != "" {
			if _, err := config.ResolveContextID(*contextID); err != nil {
				return err
			}
		}
		cfg, err := config.LoadConfig(*configPath)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		if _, err := config.ResolveJournalCutoverMode(cfg); err != nil {
			return fmt.Errorf("journal cutover: %w", err)
		}
		baseDir := config.ResolveBaseDir(cfg.BaseDir)
		sessionName := config.GetTmuxSessionName()
		if sessionName == "" {
			return fmt.Errorf("tmux session name required (run inside tmux)")
		}
		sessionName, err = config.ValidateSessionName(sessionName)
		if err != nil {
			return err
		}
		absFile, err := findInboxFileByName(baseDir, sessionName, *contextID, *file)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(absFile)
		if err != nil {
			return fmt.Errorf("reading %s: %w", *file, err)
		}
		fmt.Print(string(data))
		return nil
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
	cutoverMode, err := config.ResolveJournalCutoverMode(cfg)
	if err != nil {
		return fmt.Errorf("journal cutover: %w", err)
	}

	if !*peek && cutoverMode == config.JournalCutoverCompatibilityFirst && config.ContextOwnsSession(baseDir, resolvedContextID, sessionName) {
		response, err := roundTripCompatibilitySubmit(sessionDir, projection.CompatibilitySubmitRequest{
			Command: projection.CompatibilitySubmitPop,
			Node:    nodeName,
		}, compatibilitySubmitTimeout(cfg.TmuxTimeout))
		if err != nil {
			return fmt.Errorf("compatibility submit pop: %w", err)
		}
		if response.Empty {
			if *jsonOut {
				return json.NewEncoder(os.Stdout).Encode(struct{}{})
			}
			fmt.Fprintln(os.Stderr, "No unread messages.")
			return nil
		}
		if *jsonOut {
			parsed := parseMessageContent(response.Content, response.Filename)
			return json.NewEncoder(os.Stdout).Encode(parsed)
		}
		output := response.Content
		if shouldRenderReadContext(*peek, *jsonOut, *file, cfg) {
			output = appendReadContextBlock(output, readcontext.BuildBlock(cfg.ReadContextHeading, cfg.ReadContextPieces, readcontext.CurrentOptions(nodeName)))
		}
		fmt.Fprintf(os.Stderr, "[1/%d unread]\n", response.UnreadBefore)
		fmt.Print(output)
		fmt.Fprintf(os.Stderr, "Remaining: %d unread\n", response.UnreadBefore-1)
		return nil
	}

	msgs := message.ScanInboxMessages(inboxPath)
	if len(msgs) == 0 {
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(struct{}{})
		}
		fmt.Fprintln(os.Stderr, "No unread messages.")
		return nil
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
				if *jsonOut {
					return json.NewEncoder(os.Stdout).Encode(struct{}{})
				}
				fmt.Fprintln(os.Stderr, "No unread messages.")
				return nil
			}
			sort.Slice(msgs, func(i, j int) bool {
				return msgs[i].Filename < msgs[j].Filename
			})
			abs = filepath.Join(inboxPath, msgs[0].Filename)
			data, err = os.ReadFile(abs)
			if err != nil {
				if os.IsNotExist(err) {
					if *jsonOut {
						return json.NewEncoder(os.Stdout).Encode(struct{}{})
					}
					fmt.Fprintln(os.Stderr, "No unread messages.")
					return nil
				}
				return fmt.Errorf("reading message: %w", err)
			}
		} else {
			return fmt.Errorf("reading message: %w", err)
		}
	}

	if *jsonOut {
		parsed := parseMessageContent(string(data), msgs[0].Filename)
		if !*peek {
			if _, err := archivePoppedMessage(abs, msgs[0].Filename); err != nil {
				return err
			}
		}
		return json.NewEncoder(os.Stdout).Encode(parsed)
	}

	fmt.Fprintf(os.Stderr, "[1/%d unread]\n", len(msgs))
	output := string(data)
	if shouldRenderReadContext(*peek, *jsonOut, *file, cfg) {
		output = appendReadContextBlock(output, readcontext.BuildBlock(cfg.ReadContextHeading, cfg.ReadContextPieces, readcontext.CurrentOptions(nodeName)))
	}
	fmt.Print(output)

	if *peek {
		fmt.Fprintf(os.Stderr, "Remaining: %d unread\n", len(msgs))
		return nil
	}

	_, err = archivePoppedMessage(abs, msgs[0].Filename)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Remaining: %d unread\n", len(msgs)-1)
	return nil
}

func findInboxFileByName(baseDir, sessionName, contextID, filename string) (string, error) {
	resolvedContextID := contextID
	if resolvedContextID == "" {
		var err error
		resolvedContextID, err = config.ResolveContextIDFromSession(baseDir, sessionName)
		if err != nil {
			return "", err
		}
	}

	pattern := filepath.Join(baseDir, resolvedContextID, sessionName, "inbox", "*", filename)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("globbing for %s: %w", filename, err)
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("error: %s not found in any inbox/ directory", filename)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("error: %s found in multiple inbox/ directories: %v", filename, matches)
	}
}

func archivePoppedMessage(absPath, filename string) (string, error) {
	return message.ArchiveInboxMessage(absPath, filename)
}

func shouldRenderReadContext(peek, jsonOut bool, file string, cfg *config.Config) bool {
	return !peek && !jsonOut && file == "" && cfg != nil && cfg.ReadContextMode == "pieces"
}

func appendReadContextBlock(content, block string) string {
	if block == "" {
		return content
	}
	return strings.TrimRight(content, "\n") + "\n\n" + block + "\n"
}

// messageJSON holds JSON-serializable fields for a message (used by pop --json).
type messageJSON struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Timestamp string `json:"timestamp"`
	Body      string `json:"body"`
}

// parseMessageContent extracts JSON-friendly fields from raw message file content.
// Parses YAML frontmatter for from/to/timestamp; body is content after frontmatter.
func parseMessageContent(content, filename string) messageJSON {
	result := messageJSON{ID: filename}
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
		} else if strings.HasPrefix(line, "  timestamp: ") {
			result.Timestamp = strings.TrimSpace(strings.TrimPrefix(line, "  timestamp: "))
		}
	}
	if fmEnd >= 0 && fmEnd+1 < len(lines) {
		result.Body = strings.TrimSpace(strings.Join(lines[fmEnd+1:], "\n"))
	}
	return result
}
