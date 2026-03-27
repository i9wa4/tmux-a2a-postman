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
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

// runGetSessionHealth prints session health: node count, inbox/waiting counts (#220).
func runGetSessionHealth(args []string) error {
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
	if strings.ContainsAny(sessionName, "/\\") {
		return fmt.Errorf("session name %q: invalid value", sessionName)
	}
	sessionName = filepath.Base(sessionName)
	if sessionName == "" || sessionName == "." || sessionName == ".." {
		return fmt.Errorf("session name %q: invalid value", sessionName)
	}

	// Issue #249: auto-resolve --context-id if not provided
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

	// Discover nodes
	nodes, _, err := discovery.DiscoverNodesWithCollisions(baseDir, resolvedContextID, sessionName)
	if err != nil {
		return fmt.Errorf("discovering nodes: %w", err)
	}
	edgeNodes := config.GetEdgeNodeNames(cfg.Edges)

	type nodeHealth struct {
		Name         string `json:"name"`
		InboxCount   int    `json:"inbox_count"`
		WaitingCount int    `json:"waiting_count"`
	}

	var healthEntries []nodeHealth
	for nodeName := range nodes {
		parts := strings.SplitN(nodeName, ":", 2)
		rawName := parts[len(parts)-1]
		if !edgeNodes[rawName] {
			continue
		}
		inboxDir := filepath.Join(sessionDir, "inbox", rawName)
		inboxEntries, _ := os.ReadDir(inboxDir)
		inboxCount := 0
		for _, e := range inboxEntries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				inboxCount++
			}
		}
		waitingDir := filepath.Join(sessionDir, "waiting")
		waitingEntries, _ := os.ReadDir(waitingDir)
		waitingCount := 0
		for _, e := range waitingEntries {
			if !e.IsDir() && strings.Contains(e.Name(), "-to-"+rawName) {
				waitingCount++
			}
		}
		healthEntries = append(healthEntries, nodeHealth{
			Name:         rawName,
			InboxCount:   inboxCount,
			WaitingCount: waitingCount,
		})
	}

	sort.Slice(healthEntries, func(i, j int) bool {
		return healthEntries[i].Name < healthEntries[j].Name
	})

	result := struct {
		ContextID string       `json:"context_id"`
		NodeCount int          `json:"node_count"`
		Nodes     []nodeHealth `json:"nodes"`
	}{
		ContextID: resolvedContextID,
		NodeCount: len(healthEntries),
		Nodes:     healthEntries,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
