package discovery

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// NodeInfo holds information about a discovered node.
type NodeInfo struct {
	PaneID      string
	SessionName string
	SessionDir  string
}

// CollisionReport describes a pane collision where two panes share the same nodeKey.
type CollisionReport struct {
	NodeKey      string // sessionName:paneTitle
	WinnerPaneID string // globally dominant pane ID (highest numeric ID across all N colliding panes)
	LoserPaneID  string // displaced pane ID
}

// paneCandidate holds raw pane data during collision detection.
// paneNum is the numeric part of paneID (e.g., 31 for %31), or -1 if unparseable.
// Sentinel semantics: any valid pane ID (including %0 with paneNum=0) always beats a
// parse-failure pane (paneNum=-1) via direct numeric comparison (0 > -1).
// True tie-breaking (first-encountered wins) only applies when ALL colliding panes are
// parse-failures (all paneNum=-1). This differs from the original spec wording which
// described "%0 vs parse-failure" as requiring tie-breaking; the -1 sentinel resolves
// this cleanly without special-casing.
type paneCandidate struct {
	paneID      string
	paneNum     int
	sessionName string
	sessionDir  string
}

// reduceCollisions selects the winner for each nodeKey and returns collision reports.
// nodeKeyOrder defines the iteration order (matches tmux list-panes -a traversal order)
// so CollisionReports across different NodeKeys are deterministically ordered.
// Winner within each group: highest paneNum; ties (equal paneNum, including -1 vs -1)
// → first-encountered wins (stable by candidate index order).
// Emits N-1 CollisionReports per nodeKey with N colliding panes.
func reduceCollisions(nodeKeyOrder []string, candidates map[string][]paneCandidate) (map[string]NodeInfo, []CollisionReport) {
	nodes := make(map[string]NodeInfo)
	var collisions []CollisionReport

	// Iterate in tmux traversal order (not map iteration order) for deterministic output.
	for _, nodeKey := range nodeKeyOrder {
		cands := candidates[nodeKey]
		if len(cands) == 0 {
			continue
		}
		if len(cands) == 1 {
			nodes[nodeKey] = NodeInfo{
				PaneID:      cands[0].paneID,
				SessionName: cands[0].sessionName,
				SessionDir:  cands[0].sessionDir,
			}
			continue
		}

		// Identify global winner BEFORE emitting any report.
		// Winner: highest paneNum; ties → first-encountered wins (stable by index order).
		winnerIdx := 0
		for i := 1; i < len(cands); i++ {
			if cands[i].paneNum > cands[winnerIdx].paneNum {
				winnerIdx = i
			}
		}

		winner := cands[winnerIdx]
		nodes[nodeKey] = NodeInfo{
			PaneID:      winner.paneID,
			SessionName: winner.sessionName,
			SessionDir:  winner.sessionDir,
		}

		// Emit one CollisionReport per displaced pane (N-1 total).
		for i, cand := range cands {
			if i == winnerIdx {
				continue
			}
			collisions = append(collisions, CollisionReport{
				NodeKey:      nodeKey,
				WinnerPaneID: winner.paneID,
				LoserPaneID:  cand.paneID,
			})
		}
	}

	return nodes, collisions
}

// DiscoverNodesWithCollisions scans tmux panes and returns nodes, collision reports, and any error.
// For panes sharing the same sessionName:paneTitle key, the winner is the pane with the
// highest numeric pane ID (e.g., %31 beats %26). N-1 CollisionReports are emitted per collision group.
// Server-wide discovery: scans all sessions (-a flag).
// SessionDir is calculated as baseDir/contextID/sessionName.
func DiscoverNodesWithCollisions(baseDir, contextID string) (map[string]NodeInfo, []CollisionReport, error) {
	// Format: pane_id session_name pane_title (title last to allow spaces)
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id} #{session_name} #{pane_title}").CombinedOutput()
	if err != nil {
		return nil, nil, fmt.Errorf("tmux list-panes: %w: %s", err, out)
	}

	// candidates maps nodeKey -> ordered list of pane candidates (first-encountered order).
	// nodeKeyOrder preserves the tmux list-panes -a traversal order across nodeKeys so that
	// CollisionReports are emitted in a deterministic, scan-order sequence.
	candidates := make(map[string][]paneCandidate)
	var nodeKeyOrder []string

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 3)
		if len(parts) != 3 {
			continue
		}
		paneID, sessionName, paneTitle := parts[0], parts[1], parts[2]

		// Skip panes with no title
		if paneTitle == "" {
			continue
		}

		// Parse numeric pane ID (e.g., "%31" → 31); -1 if unparseable
		paneNum := -1
		if len(paneID) >= 2 && paneID[0] == '%' {
			if n, parseErr := strconv.Atoi(paneID[1:]); parseErr == nil {
				paneNum = n
			}
		}

		// Calculate SessionDir as baseDir/contextID/sessionName
		sessionDir := filepath.Join(baseDir, contextID, sessionName)
		// Use session-prefixed node name to avoid collisions (Issue #33)
		// Format: session_name:node_name
		nodeKey := sessionName + ":" + paneTitle

		// Track first encounter of each nodeKey to preserve traversal order.
		if _, exists := candidates[nodeKey]; !exists {
			nodeKeyOrder = append(nodeKeyOrder, nodeKey)
		}
		candidates[nodeKey] = append(candidates[nodeKey], paneCandidate{
			paneID:      paneID,
			paneNum:     paneNum,
			sessionName: sessionName,
			sessionDir:  sessionDir,
		})
	}

	nodes, collisions := reduceCollisions(nodeKeyOrder, candidates)
	return nodes, collisions, nil
}

// DiscoverNodes scans tmux panes and returns a map of node name -> NodeInfo.
// Only panes that have a non-empty pane title are included.
// Server-wide discovery: scans all sessions (-a flag).
// SessionDir is calculated as baseDir/contextID/sessionName.
func DiscoverNodes(baseDir, contextID string) (map[string]NodeInfo, error) {
	nodes, _, err := DiscoverNodesWithCollisions(baseDir, contextID)
	return nodes, err
}

// DiscoverAllSessions returns all tmux session names.
// Issue #117: Returns ALL sessions (not just those with A2A nodes).
func DiscoverAllSessions() ([]string, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").CombinedOutput()
	if err != nil {
		// If no server running, return empty list (not an error)
		if strings.Contains(string(out), "no server running") {
			return []string{}, nil
		}
		return nil, fmt.Errorf("tmux list-sessions: %w: %s", err, out)
	}

	sessions := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			sessions = append(sessions, line)
		}
	}
	return sessions, nil
}
