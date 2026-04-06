package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

type sessionPane struct {
	windowIndex    string
	windowOrder    int
	paneOrder      int
	paneID         string
	title          string
	currentCommand string
}

func compactStatusMark(state string) string {
	switch status.NormalizeWaitingState(state) {
	case "ready", "active", "idle":
		return "🟢"
	case "pending":
		return "🔷"
	case "composing":
		return "🔵"
	case "spinning":
		return "🟡"
	case "user_input":
		return "🟣"
	default:
		return "🔴"
	}
}

func compactSessionStatusMark(visibleState string) string {
	switch visibleState {
	case "unavailable", "unowned":
		return "🔴"
	default:
		return compactStatusMark(visibleState)
	}
}

func orderedEdgeNodeNames(edges []string) []string {
	seen := make(map[string]struct{})
	var ordered []string

	for _, edge := range edges {
		edge = strings.TrimSpace(edge)
		if edge == "" {
			continue
		}

		var parts []string
		switch {
		case strings.Contains(edge, "-->"):
			parts = strings.Split(edge, "-->")
		case strings.Contains(edge, "--"):
			parts = strings.Split(edge, "--")
		default:
			continue
		}

		for _, part := range parts {
			nodeName := strings.TrimSpace(part)
			if nodeName == "" {
				continue
			}
			if _, ok := seen[nodeName]; ok {
				continue
			}
			seen[nodeName] = struct{}{}
			ordered = append(ordered, nodeName)
		}
	}

	return ordered
}

func collectSessionHealth(baseDir, contextID, sessionName string, cfg *config.Config) (status.SessionHealth, error) {
	result := status.SessionHealth{
		ContextID:   contextID,
		SessionName: sessionName,
	}
	if !ownsCanonicalSessionHealth(baseDir, contextID, sessionName) {
		result.VisibleState = "unavailable"
		result.Compact = compactSessionStatusMark(result.VisibleState)
		return result, nil
	}

	nodes, _, err := discovery.DiscoverNodesWithCollisions(baseDir, contextID, sessionName)
	if err != nil {
		return result, fmt.Errorf("discovering nodes: %w", err)
	}

	orderedEdgeNodes := orderedEdgeNodeNames(cfg.Edges)
	edgeNodes := make(map[string]bool, len(orderedEdgeNodes))
	edgeNodeRank := make(map[string]int, len(orderedEdgeNodes))
	for idx, nodeName := range orderedEdgeNodes {
		edgeNodes[nodeName] = true
		edgeNodeRank[nodeName] = idx
	}
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	paneStates := loadPaneActivityStatus(filepath.Join(baseDir, contextID, "pane-activity.json"))
	waitingStates, waitingCounts := collectWaitingFacts(sessionDir, sessionName)
	panes, err := discoverSessionPanes(sessionName)
	if err != nil {
		return result, err
	}

	paneBySimpleName := make(map[string]sessionPane)
	for _, pane := range panes {
		if !edgeNodes[pane.title] {
			continue
		}
		paneBySimpleName[pane.title] = pane
	}

	for nodeName, nodeInfo := range nodes {
		if nodeInfo.SessionName != sessionName {
			continue
		}
		simpleName := nodeaddr.Simple(nodeName)
		if !edgeNodes[simpleName] {
			continue
		}

		pane := paneBySimpleName[simpleName]
		node := status.NodeHealth{
			Name:           simpleName,
			PaneID:         nodeInfo.PaneID,
			PaneState:      paneStates[nodeInfo.PaneID],
			WaitingState:   waitingStates[simpleName],
			InboxCount:     countMarkdownFiles(filepath.Join(sessionDir, "inbox", simpleName)),
			WaitingCount:   waitingCounts[simpleName],
			CurrentCommand: pane.currentCommand,
		}
		node.VisibleState = status.VisibleState(node.PaneState, node.WaitingState, node.InboxCount)
		result.Nodes = append(result.Nodes, node)
	}

	sort.Slice(result.Nodes, func(i, j int) bool {
		leftRank, leftOK := edgeNodeRank[result.Nodes[i].Name]
		rightRank, rightOK := edgeNodeRank[result.Nodes[j].Name]
		if leftOK && rightOK && leftRank != rightRank {
			return leftRank < rightRank
		}
		if leftOK != rightOK {
			return leftOK
		}
		return result.Nodes[i].Name < result.Nodes[j].Name
	})
	result.NodeCount = len(result.Nodes)
	result.VisibleState = status.SessionVisibleState(result.Nodes)
	result.Windows = buildSessionWindows(result.Nodes, panes)
	result.Compact = buildSessionCompact(result, panes)
	return result, nil
}

func ownsCanonicalSessionHealth(baseDir, contextID, sessionName string) bool {
	return config.FindSessionOwner(baseDir, sessionName, contextID) == ""
}

func loadPaneActivityStatus(stateFile string) map[string]string {
	result := make(map[string]string)

	stateData, err := os.ReadFile(stateFile)
	if err != nil {
		return result
	}

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(stateData, &rawMap); err != nil {
		return result
	}

	for paneID, raw := range rawMap {
		var plain string
		if err := json.Unmarshal(raw, &plain); err == nil {
			if plain != "" {
				result[paneID] = plain
			}
			continue
		}

		var enriched struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(raw, &enriched); err == nil && enriched.Status != "" {
			result[paneID] = enriched.Status
		}
	}

	return result
}

func collectWaitingFacts(sessionDir, sessionName string) (map[string]string, map[string]int) {
	states := make(map[string]string)
	counts := make(map[string]int)

	entries, err := os.ReadDir(filepath.Join(sessionDir, "waiting"))
	if err != nil {
		return states, counts
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		fileInfo, err := message.ParseMessageFilename(entry.Name())
		if err != nil {
			continue
		}

		recipient := nodeaddr.Full(fileInfo.To, sessionName)
		recipientSession, recipientName, hasSession := nodeaddr.Split(recipient)
		if !hasSession || recipientSession != sessionName {
			continue
		}

		counts[recipientName]++

		content, err := os.ReadFile(filepath.Join(sessionDir, "waiting", entry.Name()))
		if err != nil {
			continue
		}

		waitingState := waitingFileVisibleState(string(content))
		if waitingState == "" {
			continue
		}
		if status.StateRank(waitingState) >= status.StateRank(states[recipientName]) {
			states[recipientName] = waitingState
		}
	}

	return states, counts
}

func waitingFrontmatterBool(content, key string) bool {
	first := strings.Index(content, "---\n")
	if first < 0 {
		return false
	}
	rest := content[first+4:]
	second := strings.Index(rest, "\n---")
	if second < 0 {
		return false
	}
	for _, line := range strings.Split(rest[:second], "\n") {
		if strings.TrimSpace(line) == key+": true" {
			return true
		}
	}
	return false
}

func waitingFileVisibleState(content string) string {
	if strings.Contains(content, "state: user_input") {
		return "user_input"
	}
	if !waitingFrontmatterBool(content, "expects_reply") {
		return ""
	}
	switch {
	case strings.Contains(content, "state: stalled"), strings.Contains(content, "state: stuck"):
		return "stalled"
	case strings.Contains(content, "state: spinning"):
		return "spinning"
	case strings.Contains(content, "state: composing"):
		return "composing"
	default:
		return ""
	}
}

func countMarkdownFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}

	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			count++
		}
	}
	return count
}

func discoverSessionPanes(sessionName string) ([]sessionPane, error) {
	out, err := exec.Command(
		"tmux",
		"list-panes",
		"-t",
		sessionName,
		"-F",
		"#{window_index}\t#{pane_index}\t#{pane_id}\t#{pane_title}\t#{pane_current_command}",
	).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "no server running") {
			return nil, nil
		}
		return nil, fmt.Errorf("listing panes for session %s: %w", sessionName, err)
	}

	var panes []sessionPane
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) < 4 {
			continue
		}
		windowOrder, _ := strconv.Atoi(parts[0])
		paneOrder := 0
		if len(parts) > 1 {
			paneOrder, _ = strconv.Atoi(parts[1])
		}
		currentCommand := ""
		if len(parts) == 5 {
			currentCommand = parts[4]
		}
		panes = append(panes, sessionPane{
			windowIndex:    parts[0],
			windowOrder:    windowOrder,
			paneOrder:      paneOrder,
			paneID:         parts[2],
			title:          parts[3],
			currentCommand: currentCommand,
		})
	}

	sort.Slice(panes, func(i, j int) bool {
		if panes[i].windowOrder != panes[j].windowOrder {
			return panes[i].windowOrder < panes[j].windowOrder
		}
		if panes[i].paneOrder != panes[j].paneOrder {
			return panes[i].paneOrder < panes[j].paneOrder
		}
		return panes[i].paneID < panes[j].paneID
	})

	return panes, nil
}

func paneIDNumber(paneID string) int {
	trimmed := strings.TrimPrefix(paneID, "%")
	if trimmed == "" {
		return 1 << 30
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return 1 << 30
	}
	return n
}

func buildSessionCompact(health status.SessionHealth, panes []sessionPane) string {
	nodeByPaneID := make(map[string]status.NodeHealth, len(health.Nodes))
	for _, node := range health.Nodes {
		if node.PaneID == "" {
			continue
		}
		nodeByPaneID[node.PaneID] = node
	}

	type compactWindow struct {
		index string
		marks string
	}

	windowSeen := make(map[string]struct{})
	var windowOrder []string
	windowNodes := make(map[string][]status.NodeHealth)
	for _, pane := range panes {
		node, ok := nodeByPaneID[pane.paneID]
		if !ok {
			continue
		}
		if _, ok := windowSeen[pane.windowIndex]; !ok {
			windowSeen[pane.windowIndex] = struct{}{}
			windowOrder = append(windowOrder, pane.windowIndex)
		}
		windowNodes[pane.windowIndex] = append(windowNodes[pane.windowIndex], node)
	}

	var windows []compactWindow
	for _, windowIndex := range windowOrder {
		nodes := append([]status.NodeHealth(nil), windowNodes[windowIndex]...)
		sort.Slice(nodes, func(i, j int) bool {
			left := paneIDNumber(nodes[i].PaneID)
			right := paneIDNumber(nodes[j].PaneID)
			if left != right {
				return left < right
			}
			return nodes[i].PaneID < nodes[j].PaneID
		})

		var marks strings.Builder
		for _, node := range nodes {
			if isShellCommand(node.CurrentCommand) {
				continue
			}
			marks.WriteString(compactStatusMark(node.VisibleState))
		}
		if marks.Len() == 0 {
			continue
		}
		windows = append(windows, compactWindow{
			index: windowIndex,
			marks: marks.String(),
		})
	}

	if len(windows) == 0 {
		return compactSessionStatusMark(health.VisibleState)
	}

	var labels []string
	var marks []string
	for _, window := range windows {
		labels = append(labels, "window"+window.index)
		marks = append(marks, window.marks)
	}
	return fmt.Sprintf("(%s)%s", strings.Join(labels, ",")+",", strings.Join(marks, ":"))
}

func buildSessionWindows(nodes []status.NodeHealth, panes []sessionPane) []status.SessionWindow {
	nodeByPaneID := make(map[string]status.NodeHealth, len(nodes))
	for _, node := range nodes {
		if node.PaneID == "" {
			continue
		}
		nodeByPaneID[node.PaneID] = node
	}

	windowByIndex := make(map[string]int)
	var windows []status.SessionWindow
	for _, pane := range panes {
		node, ok := nodeByPaneID[pane.paneID]
		if !ok {
			continue
		}

		index, exists := windowByIndex[pane.windowIndex]
		if !exists {
			windows = append(windows, status.SessionWindow{Index: pane.windowIndex})
			index = len(windows) - 1
			windowByIndex[pane.windowIndex] = index
		}
		windows[index].Nodes = append(windows[index].Nodes, status.WindowNode{Name: node.Name})
	}

	return windows
}
