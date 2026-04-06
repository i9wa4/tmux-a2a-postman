package session

import (
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

// BuildSessionList builds a sorted list of SessionInfo from discovered nodes.
// It extracts session names from "session:node" format and counts nodes per session.
// Issue #117: allSessions parameter includes ALL tmux sessions (not just A2A sessions).
// Sessions without A2A nodes will show NodeCount=0.
// The isSessionEnabled function is used to determine the Enabled status of each session.
func BuildSessionList(nodes map[string]discovery.NodeInfo, allSessions []string, isSessionEnabled func(string) bool) []tui.SessionInfo {
	sessionNodeCount := make(map[string]int)
	for nodeName := range nodes {
		parts := strings.SplitN(nodeName, ":", 2)
		if len(parts) == 2 {
			sessionName := parts[0]
			sessionNodeCount[sessionName]++
		}
	}

	// Preserve tmux list-sessions order and include sessions with 0 nodes.
	sessionSet := make(map[string]bool)
	for _, sessionName := range allSessions {
		sessionSet[sessionName] = true
	}

	sessionList := make([]tui.SessionInfo, 0, len(sessionSet))
	for _, sessionName := range allSessions {
		if !sessionSet[sessionName] {
			continue
		}
		delete(sessionSet, sessionName)
		nodeCount := sessionNodeCount[sessionName] // Defaults to 0 if not in map
		sessionList = append(sessionList, tui.SessionInfo{
			Name:      sessionName,
			NodeCount: nodeCount,
			Enabled:   isSessionEnabled(sessionName),
		})
	}

	// Append any A2A sessions missing from tmux list-sessions output.
	for sessionName := range sessionSet {
		nodeCount := sessionNodeCount[sessionName]
		sessionList = append(sessionList, tui.SessionInfo{
			Name:      sessionName,
			NodeCount: nodeCount,
			Enabled:   isSessionEnabled(sessionName),
		})
	}

	return sessionList
}
