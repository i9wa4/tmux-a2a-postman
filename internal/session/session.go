package session

import (
	"sort"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

// BuildSessionList builds a sorted list of SessionInfo from discovered nodes.
// It extracts session names from "session:node" format and counts nodes per session.
// The isSessionEnabled function is used to determine the Enabled status of each session.
func BuildSessionList(nodes map[string]discovery.NodeInfo, isSessionEnabled func(string) bool) []tui.SessionInfo {
	sessionNodeCount := make(map[string]int)
	for nodeName := range nodes {
		parts := strings.SplitN(nodeName, ":", 2)
		if len(parts) == 2 {
			sessionName := parts[0]
			sessionNodeCount[sessionName]++
		}
	}

	sessionList := make([]tui.SessionInfo, 0, len(sessionNodeCount))
	for sessionName, nodeCount := range sessionNodeCount {
		sessionList = append(sessionList, tui.SessionInfo{
			Name:      sessionName,
			NodeCount: nodeCount,
			Enabled:   isSessionEnabled(sessionName),
		})
	}

	// Sort session list by name to maintain consistent order
	sort.Slice(sessionList, func(i, j int) bool {
		return sessionList[i].Name < sessionList[j].Name
	})

	return sessionList
}
