package session

import (
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

// BuildSessionList builds a sorted list of SessionInfo from discovered nodes.
// It extracts session names from "session:node" format and counts nodes per session.
// Issue #117: allSessions parameter includes ALL tmux sessions (not just A2A sessions).
// Sessions without A2A nodes will show NodeCount=0.
// The isSessionEnabled function is used to determine the Enabled status of each session.
func BuildSessionList(nodes map[string]discovery.NodeInfo, allSessions []string, isSessionEnabled func(string) bool) []tui.SessionInfo {
	return BuildRegistry(nodes, allSessions, isSessionEnabled).SessionInfos()
}
