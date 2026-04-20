package cli

import (
	"errors"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

var errPingSessionOwned = errors.New("session owned by another daemon")

func activateStartupSessions(baseDir, contextDir, contextID, selfSession string, cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	if !config.BoolVal(cfg.AutoEnableNewSessions, false) {
		return nil
	}

	allSessions, err := discovery.DiscoverAllSessions()
	if err != nil {
		log.Printf("postman: WARNING: failed to discover tmux sessions for startup activation: %v\n", err)
		return nil
	}

	candidateNodes := activationNodeNames(cfg)
	var activated []string
	for _, targetSession := range allSessions {
		if targetSession == "" || targetSession == selfSession {
			continue
		}
		if owner := config.FindSessionOwner(baseDir, targetSession, contextID); owner != "" {
			continue
		}

		preClaimed := preclaimSessionCandidatePanes(targetSession, contextID, candidateNodes)
		if preClaimed == 0 {
			continue
		}

		if err := config.CreateMultiSessionDirs(contextDir, targetSession); err != nil {
			log.Printf("postman: WARNING: failed to create startup session dirs for %s: %v\n", targetSession, err)
			continue
		}
		if err := config.SetSessionEnabledMarker(contextID, targetSession, true); err != nil {
			log.Printf("postman: WARNING: failed to publish enabled-session marker for %s: %v\n", targetSession, err)
			continue
		}

		log.Printf("postman: startup activated session %s (%d panes)\n", targetSession, preClaimed)
		activated = append(activated, targetSession)
	}

	return activated
}

func activateSessionForPing(baseDir, contextDir, contextID, selfSession, targetSession string, cfg *config.Config, watcher *fsnotify.Watcher, watchedDirs map[string]bool) (map[string]discovery.NodeInfo, error) {
	if targetSession == "" {
		return nil, fmt.Errorf("target session is empty")
	}

	if owner := config.FindSessionOwner(baseDir, targetSession, contextID); owner != "" {
		return nil, fmt.Errorf("%w: %s", errPingSessionOwned, owner)
	}

	if err := config.CreateMultiSessionDirs(contextDir, targetSession); err != nil {
		return nil, fmt.Errorf("creating session directories for %s: %w", targetSession, err)
	}
	if err := config.SetSessionEnabledMarker(contextID, targetSession, true); err != nil {
		return nil, fmt.Errorf("publishing enabled-session marker for %s: %w", targetSession, err)
	}
	registerWatchedSessionDirs(watcher, watchedDirs, filepath.Join(contextDir, targetSession))

	candidateNodes := activationNodeNames(cfg)
	preClaimed := preclaimSessionCandidatePanes(targetSession, contextID, candidateNodes)
	refreshed, _, err := discovery.DiscoverNodesWithCollisions(baseDir, contextID, selfSession)
	if err != nil {
		_ = config.SetSessionEnabledMarker(contextID, targetSession, false)
		return nil, fmt.Errorf("discovering nodes for %s: %w", targetSession, err)
	}
	refreshed = filterDiscoveredActivationNodes(refreshed, candidateNodes)
	log.Printf("postman: pre-claimed %d panes in session %s for context %s\n", preClaimed, targetSession, contextID)
	log.Printf("postman: node snapshot refreshed after activating session %s (%d nodes)\n", targetSession, len(refreshed))
	return refreshed, nil
}

func registerWatchedSessionDirs(watcher *fsnotify.Watcher, watchedDirs map[string]bool, sessionDir string) {
	if watcher == nil || watchedDirs == nil {
		return
	}

	for _, subdir := range []string{"post", "inbox", "read"} {
		dirToWatch := filepath.Join(sessionDir, subdir)
		if watchedDirs[dirToWatch] {
			continue
		}
		if err := watcher.Add(dirToWatch); err != nil {
			log.Printf("postman: watcher.Add %s: %v\n", dirToWatch, err)
			continue
		}
		watchedDirs[dirToWatch] = true
	}
}

func activationNodeNames(cfg *config.Config) map[string]bool {
	candidateNodes := config.GetEdgeNodeNames(cfg.Edges)
	if candidateNodes == nil {
		candidateNodes = make(map[string]bool)
	}
	for _, nodeName := range cfg.OrderedNodeNames() {
		if nodeName == "" {
			continue
		}
		candidateNodes[nodeName] = true
	}
	return candidateNodes
}

func preclaimSessionCandidatePanes(sessionName, contextID string, candidateNodes map[string]bool) int {
	out, err := exec.Command("tmux", "list-panes", "-s", "-t", sessionName, "-F", "#{pane_id} #{pane_title}").Output()
	if err != nil {
		log.Printf("postman: WARNING: failed to list panes for session %s: %v\n", sessionName, err)
		return 0
	}

	preClaimed := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		nodeName := parts[1]
		nodeKey := sessionName + ":" + nodeName
		if !config.EdgeNodeAllowed(candidateNodes, nodeKey) {
			continue
		}
		if err := exec.Command("tmux", "set-option", "-p", "-t", parts[0], "@a2a_context_id", contextID).Run(); err != nil {
			log.Printf("postman: WARNING: failed to pre-claim pane %s (%s): %v\n", parts[0], parts[1], err)
			continue
		}
		preClaimed++
	}
	return preClaimed
}

func filterDiscoveredActivationNodes(nodes map[string]discovery.NodeInfo, candidateNodes map[string]bool) map[string]discovery.NodeInfo {
	filtered := make(map[string]discovery.NodeInfo)
	for nodeName, nodeInfo := range nodes {
		if !config.EdgeNodeAllowed(candidateNodes, nodeName) {
			continue
		}
		filtered[nodeName] = nodeInfo
	}
	return filtered
}
