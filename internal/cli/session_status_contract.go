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
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspacetree"
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
	switch status.NormalizeState(state) {
	case "initial":
		return "⚫"
	case "ready", "active", "idle":
		return "🟢"
	case "waiting":
		return "🟡"
	case "pending":
		return "🔷"
	default:
		return "🔴"
	}
}

func compactSessionStatusMark(visibleState string) string {
	switch visibleState {
	case "unavailable", "unowned":
		return "⚫"
	default:
		return compactStatusMark(visibleState)
	}
}

func orderedEdgeNodeNames(edges []string) []string {
	return config.OrderedEdgeNodeNames(edges)
}

func collectSessionStatus(baseDir, contextID, sessionName string, cfg *config.Config) (status.SessionStatus, error) {
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	if !ownsCanonicalSessionStatus(baseDir, contextID, sessionName) {
		return unavailableSessionStatus(contextID, sessionName), nil
	}
	live, err := collectLiveSessionStatus(baseDir, contextID, sessionName, cfg)
	if err == nil {
		return live, nil
	}
	if projected, ok := projectedSessionStatus(sessionDir); ok {
		return projected, nil
	}
	return status.SessionStatus{}, err
}

func collectLiveSessionStatus(baseDir, contextID, sessionName string, cfg *config.Config) (status.SessionStatus, error) {
	return collectSessionStatusWithInboxCounts(baseDir, contextID, sessionName, cfg, nil, false)
}

func projectedInputRequestCounts(sessionDir, sessionName string) (projection.MessageInputRequestState, bool) {
	projected, ok, err := projection.ProjectMessageInputRequestState(sessionDir, sessionName)
	if err != nil || !ok {
		return projection.MessageInputRequestState{}, false
	}
	return projected, true
}

type sessionStatusInputs struct {
	contextID        string
	sessionName      string
	sessionDir       string
	cfg              *config.Config
	orderedEdgeNodes []string
	edgeNodeRank     map[string]int
	nodes            map[string]discovery.NodeInfo
	paneActivity     map[string]paneActivityEvidence
	queues           status.SessionQueues
	inputRequests    projection.MessageInputRequestState
	useInputRequests bool
	panes            []sessionPane
	inboxCounts      map[string]int
	delivery         *status.DeliveryStatus
	blockedByNode    map[string][]projection.BlockedReport
	now              time.Time
}

func buildSessionStatusSnapshot(inputs sessionStatusInputs) status.SessionStatus {
	result := status.SessionStatus{
		SchemaVersion: status.SchemaVersion,
		ContextID:     inputs.contextID,
		SessionName:   inputs.sessionName,
	}

	edgeNodes := make(map[string]bool, len(inputs.orderedEdgeNodes))
	for _, name := range inputs.orderedEdgeNodes {
		edgeNodes[name] = true
	}

	paneBySimpleName := make(map[string]sessionPane)
	for _, pane := range inputs.panes {
		if !edgeNodes[pane.title] {
			continue
		}
		paneBySimpleName[pane.title] = pane
	}

	for nodeName, nodeInfo := range inputs.nodes {
		if nodeInfo.SessionName != inputs.sessionName {
			continue
		}
		simpleName := nodeaddr.Simple(nodeName)
		if !edgeNodes[simpleName] {
			continue
		}

		pane := paneBySimpleName[simpleName]
		node := status.NodeStatus{
			Name:           simpleName,
			PaneID:         nodeInfo.PaneID,
			PaneState:      inputs.paneActivity[nodeInfo.PaneID].Status,
			InboxCount:     inputs.inboxCounts[simpleName],
			CurrentCommand: pane.currentCommand,
			ScreenProgress: inputs.paneActivity[nodeInfo.PaneID].ScreenProgress,
		}
		if node.ScreenProgress == nil {
			node.ScreenProgress = missingScreenProgressEvidence()
		}
		inputRequiredCount := -1
		if inputs.useInputRequests {
			node.InputRequiredCount = inputs.inputRequests.InputRequiredCounts[simpleName]
			node.WaitingOnInputCount = inputs.inputRequests.WaitingOnInputCounts[simpleName]
			node.InfoUnreadCount = inputs.inputRequests.InfoUnreadCounts[simpleName]
			node.InputRequired = statusInputRequestDetails(inputs.inputRequests.InputRequired, simpleName, "inbound")
			node.WaitingOnInput = statusInputRequestDetails(inputs.inputRequests.WaitingOnInput, simpleName, "outbound")
			inputRequiredCount = node.InputRequiredCount
			if node.InboxCount > inputs.inputRequests.UnreadCounts[simpleName] {
				inputRequiredCount = -1
			}
		}
		node.VisibleState = status.VisibleStateWithInputRequests(node.PaneState, node.InboxCount, inputRequiredCount, node.WaitingOnInputCount)
		result.Nodes = append(result.Nodes, node)
	}

	sort.Slice(result.Nodes, func(i, j int) bool {
		leftRank, leftOK := inputs.edgeNodeRank[result.Nodes[i].Name]
		rightRank, rightOK := inputs.edgeNodeRank[result.Nodes[j].Name]
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
	result.Queues = inputs.queues
	result.Windows = buildSessionWindows(result.Nodes, inputs.panes)
	result.WorkspaceTree = buildWorkspaceTreeStatus(inputs.cfg, inputs.sessionName)
	result.CommandApproval = buildCommandApprovalStatus(inputs.cfg)
	result.Compact = buildSessionCompact(result, inputs.panes)
	applySessionStatusEnrichment(&result, inputs.delivery, inputs.blockedByNode)
	return result
}

func buildWorkspaceTreeStatus(cfg *config.Config, sessionName string) *status.WorkspaceTreeStatus {
	topology := workspacetree.BuildFromConfig(cfg)
	diagnostics := workspaceTreeDiagnostics(topology.Diagnostics())
	node, ok, reason := topology.NodeForSession(sessionName)
	if !ok {
		if reason == workspacetree.FailureUnknownSourceSession && len(diagnostics) == 0 {
			return nil
		}
		return &status.WorkspaceTreeStatus{
			Diagnostics: diagnostics,
		}
	}

	result := &status.WorkspaceTreeStatus{
		Current: &status.WorkspaceTreeNodeStatus{
			SessionName: node.SessionName,
			Label:       node.Label,
			ID:          node.ID,
			State:       "configured",
		},
		Diagnostics: diagnostics,
	}
	if parent, found, _ := topology.NearestParent(sessionName); found {
		result.Parent = workspaceTreeRef(parent)
	}
	if children, childReason := topology.NearestChildren(sessionName); childReason == workspacetree.FailureNone {
		for _, child := range children {
			result.Children = append(result.Children, *workspaceTreeRef(child))
		}
	}
	return result
}

// buildCommandApprovalStatus surfaces any configured-but-unresolvable
// command_approver_node so get-status makes the fail-open condition visible
// (#626/#629), mirroring config.ValidateConfig's warning rule without depending
// on its message wording.
func buildCommandApprovalStatus(cfg *config.Config) *status.CommandApprovalStatus {
	if cfg == nil {
		return nil
	}
	var unresolved []status.CommandApprovalUnresolvedApprover
	if name, valid := cfg.ResolveCommandApproverNode(); name != "" && !valid {
		unresolved = append(unresolved, status.CommandApprovalUnresolvedApprover{
			Field:   "command_approver_node",
			Value:   name,
			Message: fmt.Sprintf("command_approver_node %q does not match any configured node; command approval is failing open", name),
		})
	}
	deprecated := make([]status.CommandApprovalDeprecatedApprover, 0, len(cfg.DeprecatedCommandApproverNodes))
	for _, legacy := range cfg.DeprecatedCommandApproverNodes {
		deprecated = append(deprecated, status.CommandApprovalDeprecatedApprover{
			Field:   legacy.Field,
			Value:   legacy.Value,
			Message: fmt.Sprintf("legacy TOML %s %q is ignored; move command_approver_node to postman.md Mermaid class or command approval will fail open", legacy.Field, legacy.Value),
		})
	}
	if len(unresolved) == 0 && len(deprecated) == 0 {
		return nil
	}
	return &status.CommandApprovalStatus{
		UnresolvedCommandApprovers: unresolved,
		DeprecatedCommandApprovers: deprecated,
	}
}

func workspaceTreeRef(node workspacetree.Node) *status.WorkspaceTreeRef {
	return &status.WorkspaceTreeRef{
		SessionName: node.SessionName,
		Label:       node.Label,
		ID:          node.ID,
	}
}

func workspaceTreeDiagnostics(diagnostics []workspacetree.Diagnostic) []status.WorkspaceTreeDiagnostic {
	if len(diagnostics) == 0 {
		return nil
	}
	result := make([]status.WorkspaceTreeDiagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		result = append(result, status.WorkspaceTreeDiagnostic{
			Code:              diagnostic.Code,
			ID:                diagnostic.ID,
			IDs:               append([]string{}, diagnostic.IDs...),
			SessionName:       diagnostic.SessionName,
			SessionNames:      append([]string{}, diagnostic.SessionNames...),
			ParentSessionName: diagnostic.ParentSessionName,
			Labels:            append([]string{}, diagnostic.Labels...),
			Message:           diagnostic.Message,
		})
	}
	return result
}

func collectSessionStatusWithInboxCounts(baseDir, contextID, sessionName string, cfg *config.Config, inboxCounts map[string]int, useProjectedInboxCounts bool) (status.SessionStatus, error) {
	if !ownsCanonicalSessionStatus(baseDir, contextID, sessionName) {
		result := status.SessionStatus{
			SchemaVersion: status.SchemaVersion,
			ContextID:     contextID,
			SessionName:   sessionName,
			VisibleState:  "unavailable",
		}
		result.Compact = compactSessionStatusMark(result.VisibleState)
		return result, nil
	}

	nodes, _, err := discovery.DiscoverNodesWithCollisions(baseDir, contextID, sessionName)
	if err != nil {
		return status.SessionStatus{}, fmt.Errorf("discovering nodes: %w", err)
	}

	orderedEdgeNodes := orderedEdgeNodeNames(cfg.Edges)
	edgeNodeRank := make(map[string]int, len(orderedEdgeNodes))
	edgeNodes := make(map[string]bool, len(orderedEdgeNodes))
	for idx, nodeName := range orderedEdgeNodes {
		edgeNodes[nodeName] = true
		edgeNodeRank[nodeName] = idx
	}

	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	paneActivity := loadPaneActivityEvidence(filepath.Join(baseDir, contextID, "pane-activity.json"))
	queues := collectSessionQueues(sessionDir)
	inputRequests, useInputRequests := projectedInputRequestCounts(sessionDir, sessionName)
	panes, err := discoverSessionPanes(sessionName)
	if err != nil {
		return status.SessionStatus{}, err
	}

	nodeInboxCounts := make(map[string]int)
	for nodeName, nodeInfo := range nodes {
		if nodeInfo.SessionName != sessionName {
			continue
		}
		simpleName := nodeaddr.Simple(nodeName)
		if !edgeNodes[simpleName] {
			continue
		}
		if useProjectedInboxCounts {
			nodeInboxCounts[simpleName] = inboxCounts[simpleName]
		} else {
			nodeInboxCounts[simpleName] = countMarkdownFiles(filepath.Join(sessionDir, "inbox", simpleName))
		}
	}

	now := time.Now()
	blockedByNode := map[string][]projection.BlockedReport{}
	if blocked, ok, err := projection.ProjectBlockedReportState(sessionDir, sessionName); err == nil && ok {
		blockedByNode = blocked.ReportsByNode
	}
	delivery := collectSessionDelivery(sessionDir, queues, now)

	return buildSessionStatusSnapshot(sessionStatusInputs{
		contextID:        contextID,
		sessionName:      sessionName,
		sessionDir:       sessionDir,
		cfg:              cfg,
		orderedEdgeNodes: orderedEdgeNodes,
		edgeNodeRank:     edgeNodeRank,
		nodes:            nodes,
		paneActivity:     paneActivity,
		queues:           queues,
		inputRequests:    inputRequests,
		useInputRequests: useInputRequests,
		panes:            panes,
		inboxCounts:      nodeInboxCounts,
		delivery:         delivery,
		blockedByNode:    blockedByNode,
		now:              now,
	}), nil
}

func statusInputRequestDetails(inputRequests []projection.InputRequestDetail, nodeName, direction string) []status.InputRequestDetail {
	if len(inputRequests) == 0 {
		return nil
	}
	result := make([]status.InputRequestDetail, 0, len(inputRequests))
	for _, inputRequest := range inputRequests {
		if direction == "inbound" && inputRequest.Recipient != nodeName {
			continue
		}
		if direction == "outbound" && inputRequest.Sender != nodeName {
			continue
		}
		result = append(result, status.InputRequestDetail{
			Direction:      inputRequest.Direction,
			MessageID:      inputRequest.MessageID,
			InputRequestID: inputRequest.InputRequestID,
			Sender:         inputRequest.Sender,
			Recipient:      inputRequest.Recipient,
			ReplyPolicy:    inputRequest.ReplyPolicy,
			OpenedAt:       inputRequest.OpenedAt,
			OpenedAtSource: inputRequest.OpenedAtSource,
			OpenedEventID:  inputRequest.OpenedEventID,
			ReadAt:         inputRequest.ReadAt,
			ReadEventID:    inputRequest.ReadEventID,
		})
	}
	return result
}

func ownsCanonicalSessionStatus(baseDir, contextID, sessionName string) bool {
	return config.FindSessionOwner(baseDir, sessionName, contextID) == ""
}

type paneActivityEvidence struct {
	Status         string
	ScreenProgress *status.ScreenProgressEvidence
}

func loadPaneActivityEvidence(stateFile string) map[string]paneActivityEvidence {
	result := make(map[string]paneActivityEvidence)

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
				result[paneID] = paneActivityEvidence{
					Status:         plain,
					ScreenProgress: screenProgressEvidence(plain, "", "", ""),
				}
			}
			continue
		}

		var enriched struct {
			Status            string `json:"status"`
			LastChangeAt      string `json:"lastChangeAt"`
			LastCaptureAt     string `json:"lastCaptureAt"`
			ScreenFingerprint string `json:"screenFingerprint"`
		}
		if err := json.Unmarshal(raw, &enriched); err == nil && enriched.Status != "" {
			result[paneID] = paneActivityEvidence{
				Status: enriched.Status,
				ScreenProgress: screenProgressEvidence(
					enriched.Status,
					enriched.LastChangeAt,
					enriched.LastCaptureAt,
					enriched.ScreenFingerprint,
				),
			}
		}
	}

	return result
}

func missingScreenProgressEvidence() *status.ScreenProgressEvidence {
	return &status.ScreenProgressEvidence{EvidenceState: "missing"}
}

func screenProgressEvidence(paneState, lastChangeAt, lastCaptureAt, screenFingerprint string) *status.ScreenProgressEvidence {
	progress := missingScreenProgressEvidence()

	lastChangeText, lastChangeTime, hasLastChange := normalizeProgressTimestamp(lastChangeAt)
	lastCaptureText, lastCaptureTime, hasLastCapture := normalizeProgressTimestamp(lastCaptureAt)
	if hasLastChange {
		progress.LastScreenChangeAt = lastChangeText
	}
	if hasLastCapture {
		progress.LastCaptureAt = lastCaptureText
	}
	if fingerprint := normalizeScreenFingerprint(screenFingerprint); fingerprint != "" {
		progress.ScreenFingerprint = fingerprint
	}

	switch {
	case paneState == "stale":
		progress.EvidenceState = "stale"
	case !hasLastCapture || progress.ScreenFingerprint == "":
		progress.EvidenceState = "missing"
	case hasLastChange && !lastCaptureTime.After(lastChangeTime):
		progress.EvidenceState = "changed"
	case hasLastChange:
		progress.EvidenceState = "unchanged"
	default:
		progress.EvidenceState = "missing"
	}

	return progress
}

func normalizeProgressTimestamp(value string) (string, time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.IsZero() {
		return "", time.Time{}, false
	}
	return parsed.UTC().Format(time.RFC3339Nano), parsed, true
}

func normalizeScreenFingerprint(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return ""
		}
	}
	return value
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

func countMarkdownFilesRecursive(dir string) int {
	count := 0
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".md") {
			count++
		}
		return nil
	})
	return count
}

func collectSessionQueues(sessionDir string) status.SessionQueues {
	return status.SessionQueues{
		PostCount:       countMarkdownFiles(filepath.Join(sessionDir, "post")),
		InboxCount:      countMarkdownFilesRecursive(filepath.Join(sessionDir, "inbox")),
		DeadLetterCount: countMarkdownFiles(filepath.Join(sessionDir, "dead-letter")),
	}
}

func discoverSessionPanes(sessionName string) ([]sessionPane, error) {
	windowListOut, err := exec.Command(
		"tmux",
		"list-windows",
		"-t",
		sessionName,
		"-F",
		"#{window_index}",
	).CombinedOutput()
	if err != nil {
		if strings.Contains(string(windowListOut), "no server running") || strings.Contains(string(windowListOut), "can't find session") {
			return nil, nil
		}
		return nil, fmt.Errorf("listing windows for session %s: %w", sessionName, err)
	}

	var panes []sessionPane
	for _, windowIndex := range strings.Split(strings.TrimSpace(string(windowListOut)), "\n") {
		if windowIndex == "" {
			continue
		}

		out, err := exec.Command(
			"tmux",
			"list-panes",
			"-t",
			sessionName+":"+windowIndex,
			"-F",
			"#{window_index}\t#{pane_index}\t#{pane_id}\t#{pane_title}\t#{pane_current_command}",
		).CombinedOutput()
		if err != nil {
			if strings.Contains(string(out), "can't find window") {
				continue
			}
			if strings.Contains(string(out), "no server running") {
				return nil, nil
			}
			return nil, fmt.Errorf("listing panes for session %s window %s: %w", sessionName, windowIndex, err)
		}

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

func buildSessionCompact(health status.SessionStatus, panes []sessionPane) string {
	nodeByPaneID := make(map[string]status.NodeStatus, len(health.Nodes))
	for _, node := range health.Nodes {
		if node.PaneID == "" {
			continue
		}
		nodeByPaneID[node.PaneID] = node
	}

	var windowMarks []string

	windowSeen := make(map[string]struct{})
	var windowOrder []string
	windowNodes := make(map[string][]status.NodeStatus)
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

	for _, windowIndex := range windowOrder {
		var marks strings.Builder
		for _, node := range windowNodes[windowIndex] {
			if isShellCommand(node.CurrentCommand) {
				continue
			}
			marks.WriteString(compactStatusMark(node.VisibleState))
		}
		if marks.Len() == 0 {
			continue
		}
		windowMarks = append(windowMarks, marks.String())
	}

	if len(windowMarks) == 0 {
		return compactSessionStatusMark(health.VisibleState)
	}

	return strings.Join(windowMarks, ":")
}

func buildSessionWindows(nodes []status.NodeStatus, panes []sessionPane) []status.SessionWindow {
	nodeByPaneID := make(map[string]status.NodeStatus, len(nodes))
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
