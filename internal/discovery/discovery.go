package discovery

import (
	"fmt"
	"os"
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
// selfSession is the daemon's own tmux session name. Unclaimed panes in foreign sessions
// are excluded (F3: unclaimed-pane guard).
func DiscoverNodesWithCollisions(baseDir, contextID, selfSession string) (map[string]NodeInfo, []CollisionReport, error) {
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

	// Filter candidates: only retain panes whose context inbox directory exists on
	// disk. This scopes discovery to the current daemon's context and prevents
	// foreign-context panes from being included (cross-session interference fix).
	filteredCandidates := make(map[string][]paneCandidate, len(candidates))
	var filteredNodeKeyOrder []string
	for _, nodeKey := range nodeKeyOrder {
		var kept []paneCandidate
		for _, c := range candidates[nodeKey] {
			inboxDir := filepath.Join(baseDir, contextID, c.sessionName, "inbox")
			if _, err := os.Stat(inboxDir); err == nil {
				// Per-pane ownership check: skip panes claimed by a different
				// daemon context. show-options -v exits non-zero when unset
				// (unclaimed), so err != nil means the pane is available.
				if out, err := exec.Command(
					"tmux", "show-options", "-p", "-v", "-t", c.paneID, "@a2a_context_id",
				).Output(); err == nil {
					claimedContext := strings.TrimSpace(string(out))
					if claimedContext != "" && claimedContext != contextID {
						continue // pane claimed by a different daemon context
					}
				} else {
					// F3: Unclaimed pane (show-options returned non-zero: option not set).
					// Only allow unclaimed panes in the daemon's own session.
					// Foreign unclaimed panes are excluded even if their inbox dir exists.
					if c.sessionName != selfSession {
						continue
					}
				}
				kept = append(kept, c)
			}
		}
		if len(kept) > 0 {
			filteredCandidates[nodeKey] = kept
			filteredNodeKeyOrder = append(filteredNodeKeyOrder, nodeKey)
		}
	}

	nodes, collisions := reduceCollisions(filteredNodeKeyOrder, filteredCandidates)
	return nodes, collisions, nil
}

// DiscoverNodes scans tmux panes and returns a map of node name -> NodeInfo.
// Only panes that have a non-empty pane title are included.
// Server-wide discovery: scans all sessions (-a flag).
// SessionDir is calculated as baseDir/contextID/sessionName.
func DiscoverNodes(baseDir, contextID, selfSession string) (map[string]NodeInfo, error) {
	nodes, _, err := DiscoverNodesWithCollisions(baseDir, contextID, selfSession)
	return nodes, err
}

// ResolveNodeName resolves a simple node name to a session-prefixed node name.
// Resolution priority:
// 1. If nodeName already contains ":", use as-is (already prefixed)
// 2. Look for nodeName in the same session as sourceSessionName
// 3. Look for nodeName in any other session
// Returns the resolved node name or empty string if not found.
func ResolveNodeName(nodeName, sourceSessionName string, knownNodes map[string]NodeInfo) string {
	// If already prefixed (contains ":"), use as-is
	if strings.Contains(nodeName, ":") {
		if _, found := knownNodes[nodeName]; found {
			return nodeName
		}
		return "" // Prefixed but not found
	}

	// Try same-session first (priority)
	sameSessionKey := sourceSessionName + ":" + nodeName
	if _, found := knownNodes[sameSessionKey]; found {
		return sameSessionKey
	}

	return "" // Not found: cross-session delivery requires explicit "session:node" syntax (F2)
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
