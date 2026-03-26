package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
)

// runRead lists inbox message file paths for the current node (#196).
// With --archived: lists archived (read/) messages, self-filtered to the calling node.
// With --dead-letters: lists dead-letter metadata or resends messages.
func runRead(args []string) error {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	// Options struct fields (--params scope): json, archived, dead-letters, resend-oldest
	// SYNC: schema read properties; alwaysExcludedParams map
	jsonOut := fs.Bool("json", false, `output json: {"files":[...]} or {"messages":[...]}`)
	archived := fs.Bool("archived", false, "list archived messages in read/ (self-filter: calling node only)")
	deadLetters := fs.Bool("dead-letters", false, "list dead-letter messages")
	fileFlag := fs.String("file", "", "print or resend a specific message by filename")
	resendOldest := fs.Bool("resend-oldest", false, "resend the oldest dead-letter (requires --dead-letters)")
	paramsFlag := fs.String("params", "", "command parameters as JSON or shorthand (k=v,k=v)")
	// NOTE: always-excluded from --params scope (SYNC: alwaysExcludedParams map)
	contextID := fs.String("context-id", "", "context ID") // Issue #315: forward global --context-id
	configPath := fs.String("config", "", "path to config file (optional)")
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
		resolvedParams, err := parseParams(*paramsFlag)
		if err != nil {
			return err
		}
		if err := applyParams(fs, resolvedParams, explicitlySet, commandName); err != nil {
			return err
		}
	}
	if *archived && *deadLetters {
		return fmt.Errorf("--archived and --dead-letters are mutually exclusive")
	}
	if *resendOldest && !*deadLetters {
		return fmt.Errorf("--resend-oldest requires --dead-letters")
	}
	if *deadLetters && *resendOldest && *fileFlag != "" {
		return fmt.Errorf("--resend-oldest and --file are mutually exclusive")
	}
	if *deadLetters {
		cfg, err := config.LoadConfig(*configPath)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		baseDir := config.ResolveBaseDir(cfg.BaseDir)
		sessionName := config.GetTmuxSessionName()
		if sessionName == "" {
			return fmt.Errorf("tmux session name required (run inside tmux)")
		}
		sessionName = filepath.Base(sessionName)
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
		sessionDir := filepath.Join(baseDir, resolvedContextID, sessionName)
		deadLetterDir := filepath.Join(sessionDir, "dead-letter")
		if *resendOldest {
			postDir := filepath.Join(sessionDir, "post")
			if err := os.MkdirAll(postDir, 0o700); err != nil {
				return fmt.Errorf("creating post/ directory: %w", err)
			}
			found, ok, err := findOldestDeadLetterFile(deadLetterDir)
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintln(os.Stderr, "No dead-letter messages.")
				return nil
			}
			baseName := filepath.Base(found)
			cleanName := message.StripDeadLetterSuffix(baseName)
			dst := filepath.Join(postDir, cleanName)
			if err := os.Rename(found, dst); err != nil {
				return fmt.Errorf("moving to post/: %w", err)
			}
			fmt.Printf("Resent: %s\n", baseName)
			return nil
		}
		if *fileFlag != "" {
			if strings.ContainsAny(*fileFlag, "/\\") {
				return fmt.Errorf("read --dead-letters --file: filename must not contain path separators")
			}
			absFile := filepath.Join(deadLetterDir, *fileFlag)
			if _, err := os.Stat(absFile); err != nil {
				return fmt.Errorf("dead-letter file not found: %w", err)
			}
			postDir := filepath.Join(sessionDir, "post")
			if err := os.MkdirAll(postDir, 0o700); err != nil {
				return fmt.Errorf("creating post/ directory: %w", err)
			}
			cleanName := message.StripDeadLetterSuffix(*fileFlag)
			dst := filepath.Join(postDir, cleanName)
			if err := os.Rename(absFile, dst); err != nil {
				return fmt.Errorf("moving to post/: %w", err)
			}
			fmt.Printf("Resent: %s\n", *fileFlag)
			return nil
		}
		entries, err := os.ReadDir(deadLetterDir)
		if err != nil {
			if os.IsNotExist(err) {
				if *jsonOut {
					return json.NewEncoder(os.Stdout).Encode(struct {
						Messages []deadLetterMessageJSON `json:"messages"`
					}{Messages: []deadLetterMessageJSON{}})
				}
				fmt.Fprintln(os.Stderr, "No dead-letter messages.")
				return nil
			}
			return fmt.Errorf("reading dead-letter messages: %w", err)
		}
		var dlNames []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				dlNames = append(dlNames, e.Name())
			}
		}
		sort.Strings(dlNames)
		if len(dlNames) == 0 {
			if *jsonOut {
				return json.NewEncoder(os.Stdout).Encode(struct {
					Messages []deadLetterMessageJSON `json:"messages"`
				}{Messages: []deadLetterMessageJSON{}})
			}
			fmt.Fprintln(os.Stderr, "No dead-letter messages.")
			return nil
		}
		if *jsonOut {
			msgs := make([]deadLetterMessageJSON, 0, len(dlNames))
			for _, name := range dlNames {
				cleanName := message.StripDeadLetterSuffix(name)
				info, err := message.ParseMessageFilename(cleanName)
				if err != nil {
					msgs = append(msgs, deadLetterMessageJSON{})
				} else {
					msgs = append(msgs, deadLetterMessageJSON{From: info.From, To: info.To, Timestamp: info.Timestamp})
				}
			}
			return json.NewEncoder(os.Stdout).Encode(struct {
				Messages []deadLetterMessageJSON `json:"messages"`
			}{Messages: msgs})
		}
		for _, name := range dlNames {
			cleanName := message.StripDeadLetterSuffix(name)
			info, err := message.ParseMessageFilename(cleanName)
			if err != nil {
				fmt.Printf("%s  [unreadable]\n", name)
				continue
			}
			fmt.Printf("%s  from=%s  to=%s\n", info.Timestamp, info.From, info.To)
		}
		return nil
	}
	inboxArgs := fs.Args()
	if *contextID != "" {
		inboxArgs = append([]string{"--context-id", *contextID}, inboxArgs...)
	}
	if *configPath != "" {
		inboxArgs = append([]string{"--config", *configPath}, inboxArgs...)
	}
	inboxPath, err := resolveInboxPath(inboxArgs)
	if err != nil {
		return err
	}
	if *archived {
		sessionDir := filepath.Dir(filepath.Dir(inboxPath))
		currentNodeName := filepath.Base(inboxPath)
		if *fileFlag != "" {
			if strings.ContainsAny(*fileFlag, "/\\") {
				return fmt.Errorf("read --archived --file: filename must not contain path separators")
			}
			absFile := filepath.Join(sessionDir, "read", *fileFlag)
			if _, err := os.Stat(absFile); err != nil {
				return fmt.Errorf("error: %s not found in read/: %w", *fileFlag, err)
			}
			info, err := message.ParseMessageFilename(*fileFlag)
			if err != nil {
				return fmt.Errorf("read --archived --file: invalid filename %q: %w", *fileFlag, err)
			}
			if info.To != currentNodeName {
				return fmt.Errorf("read --archived --file: %q belongs to %q, not %q", *fileFlag, info.To, currentNodeName)
			}
			data, err := os.ReadFile(absFile)
			if err != nil {
				return fmt.Errorf("reading %s: %w", *fileFlag, err)
			}
			fmt.Print(string(data))
			return nil
		}
		readPath := filepath.Join(sessionDir, "read")
		entries, err := os.ReadDir(readPath)
		if err != nil {
			if os.IsNotExist(err) {
				if *jsonOut {
					return json.NewEncoder(os.Stdout).Encode(struct {
						Messages []archivedMessageJSON `json:"messages"`
					}{Messages: []archivedMessageJSON{}})
				}
				return nil
			}
			return fmt.Errorf("reading archived messages: %w", err)
		}
		var names []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			info, err := message.ParseMessageFilename(e.Name())
			if err != nil {
				continue
			}
			if info.To != currentNodeName {
				continue
			}
			names = append(names, e.Name())
		}
		sort.Strings(names)
		if *jsonOut {
			msgs := make([]archivedMessageJSON, 0, len(names))
			for _, name := range names {
				info, err := message.ParseMessageFilename(name)
				if err != nil {
					msgs = append(msgs, archivedMessageJSON{File: name})
				} else {
					msgs = append(msgs, archivedMessageJSON{File: name, From: info.From, To: info.To, Timestamp: info.Timestamp})
				}
			}
			return json.NewEncoder(os.Stdout).Encode(struct {
				Messages []archivedMessageJSON `json:"messages"`
			}{Messages: msgs})
		}
		for _, name := range names {
			fmt.Println(name)
		}
		return nil
	}
	msgs := message.ScanInboxMessages(inboxPath)
	if *jsonOut {
		files := make([]string, len(msgs))
		for i, msg := range msgs {
			files[i] = msg.Filename
		}
		return json.NewEncoder(os.Stdout).Encode(struct {
			Files []string `json:"files"`
		}{Files: files})
	}
	if len(msgs) == 0 {
		return nil
	}
	for _, msg := range msgs {
		fmt.Println(msg.Filename)
	}
	return nil
}

// archivedMessageJSON holds JSON-serializable fields for an archived message.
type archivedMessageJSON struct {
	File      string `json:"file"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

// deadLetterMessageJSON holds JSON-serializable metadata for a dead-letter message.
// Does not expose raw filenames (#287 opacity).
type deadLetterMessageJSON struct {
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

// findOldestDeadLetterFile returns the path of the lexicographically first .md
// file in deadLetterDir, or ("", false, nil) if the directory is empty or absent.
func findOldestDeadLetterFile(deadLetterDir string) (string, bool, error) {
	entries, err := os.ReadDir(deadLetterDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("reading dead-letter directory: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "", false, nil
	}
	return filepath.Join(deadLetterDir, names[0]), true, nil
}
