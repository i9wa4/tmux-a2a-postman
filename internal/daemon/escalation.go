package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
	"github.com/i9wa4/tmux-a2a-postman/internal/ping"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

type escalationTrip struct {
	Kind      string
	Node      string
	Observed  int
	Threshold int
	Detail    string
}

func evaluateEscalationTrips(snapshot status.SessionStatus, cfg *config.Config, now time.Time) []escalationTrip {
	if cfg == nil {
		return nil
	}
	var trips []escalationTrip
	if cfg.EscalationDeadLetterCount > 0 && snapshot.Queues.DeadLetterCount >= cfg.EscalationDeadLetterCount {
		trips = append(trips, escalationTrip{
			Kind:      "dead_letter",
			Observed:  snapshot.Queues.DeadLetterCount,
			Threshold: cfg.EscalationDeadLetterCount,
			Detail:    "dead-letter count reached threshold",
		})
	}
	if cfg.EscalationOldestOpenSeconds > 0 {
		threshold := int(cfg.EscalationOldestOpenSeconds)
		for _, node := range snapshot.Nodes {
			for _, req := range append(append([]status.InputRequestDetail{}, node.InputRequired...), node.WaitingOnInput...) {
				age := inputRequestAgeSeconds(req.OpenedAt, now)
				if age >= threshold {
					trips = append(trips, escalationTrip{
						Kind:      "oldest_open_request",
						Node:      node.Name,
						Observed:  age,
						Threshold: threshold,
						Detail:    req.MessageID,
					})
				}
			}
		}
	}
	if cfg.EscalationUnreadBacklogCount > 0 {
		for _, node := range snapshot.Nodes {
			if node.InboxCount >= cfg.EscalationUnreadBacklogCount {
				trips = append(trips, escalationTrip{
					Kind:      "unread_backlog",
					Node:      node.Name,
					Observed:  node.InboxCount,
					Threshold: cfg.EscalationUnreadBacklogCount,
					Detail:    "unread inbox backlog reached threshold",
				})
			}
		}
	}
	if cfg.EscalationStaleNodeSeconds > 0 {
		threshold := int(cfg.EscalationStaleNodeSeconds)
		for _, node := range snapshot.Nodes {
			if node.PaneState != "stale" && (node.ScreenProgress == nil || node.ScreenProgress.EvidenceState != "stale") {
				continue
			}
			observed := threshold
			if node.ScreenProgress != nil && node.ScreenProgress.LastCaptureAt != "" {
				observed = inputRequestAgeSeconds(node.ScreenProgress.LastCaptureAt, now)
			}
			if observed >= threshold {
				trips = append(trips, escalationTrip{
					Kind:      "stale_node",
					Node:      node.Name,
					Observed:  observed,
					Threshold: threshold,
					Detail:    "node stale evidence reached threshold",
				})
			}
		}
	}
	sort.Slice(trips, func(i, j int) bool {
		if trips[i].Kind != trips[j].Kind {
			return trips[i].Kind < trips[j].Kind
		}
		return trips[i].Node < trips[j].Node
	})
	return trips
}

func inputRequestAgeSeconds(openedAt string, now time.Time) int {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(openedAt))
	if err != nil {
		return 0
	}
	age := int(now.Sub(parsed).Seconds())
	if age < 0 {
		return 0
	}
	return age
}

func (rt *daemonRuntime) maybePushEscalation(now time.Time) {
	if rt == nil || rt.cfg == nil || rt.cfg.EscalationCheckIntervalSeconds <= 0 {
		return
	}
	interval := time.Duration(rt.cfg.EscalationCheckIntervalSeconds * float64(time.Second))
	if !rt.lastEscalationCheck.IsZero() && now.Sub(rt.lastEscalationCheck) < interval {
		return
	}
	rt.lastEscalationCheck = now

	snapshot := rt.runtimeEscalationSnapshot(now)
	trips := evaluateEscalationTrips(snapshot, rt.cfg, now)
	if len(trips) == 0 {
		rt.lastEscalationPushKey = ""
		return
	}
	key := escalationTripKey(trips)
	if key == rt.lastEscalationPushKey {
		return
	}
	rt.lastEscalationPushKey = key
	rt.pushEscalationNotification(trips)
}

func (rt *daemonRuntime) runtimeEscalationSnapshot(now time.Time) status.SessionStatus {
	snapshot := status.SessionStatus{
		SchemaVersion: status.SchemaVersion,
		ContextID:     rt.contextID,
		SessionName:   rt.selfSession,
		Queues: status.SessionQueues{
			PostCount:       countRuntimeMarkdown(filepath.Join(rt.sessionDir, "post")),
			DeadLetterCount: countRuntimeMarkdown(filepath.Join(rt.sessionDir, "dead-letter")),
		},
	}

	nodeKeys := make([]string, 0, len(rt.nodes))
	for nodeKey := range rt.nodes {
		nodeKeys = append(nodeKeys, nodeKey)
	}
	sort.Strings(nodeKeys)
	for _, nodeKey := range nodeKeys {
		nodeInfo := rt.nodes[nodeKey]
		nodeName := ping.ExtractSimpleName(nodeKey)
		node := status.NodeStatus{
			Name:       nodeName,
			PaneID:     nodeInfo.PaneID,
			InboxCount: countRuntimeMarkdown(filepath.Join(nodeInfo.SessionDir, "inbox", nodeName)),
		}
		snapshot.Queues.InboxCount += node.InboxCount
		snapshot.Nodes = append(snapshot.Nodes, node)
	}

	attachRuntimeInputRequests(&snapshot, rt.sessionDir, rt.selfSession, now, int(rt.cfg.InputRequestStaleSeconds))
	attachRuntimePaneState(&snapshot, rt.idleTracker.GetPaneActivityStatus(rt.cfg))
	return snapshot
}

func attachRuntimeInputRequests(snapshot *status.SessionStatus, sessionDir, sessionName string, now time.Time, staleAfterSeconds int) {
	projected, ok, err := projection.ProjectMessageInputRequestStateAt(sessionDir, sessionName, now, staleAfterSeconds)
	if err != nil || !ok {
		return
	}
	for idx := range snapshot.Nodes {
		name := snapshot.Nodes[idx].Name
		snapshot.Nodes[idx].InputRequired = runtimeStatusInputRequests(projected.InputRequired, name, "inbound")
		snapshot.Nodes[idx].WaitingOnInput = runtimeStatusInputRequests(projected.WaitingOnInput, name, "outbound")
	}
}

func runtimeStatusInputRequests(inputRequests []projection.InputRequestDetail, nodeName, direction string) []status.InputRequestDetail {
	var result []status.InputRequestDetail
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

func attachRuntimePaneState(snapshot *status.SessionStatus, paneStates map[string]string) {
	for idx := range snapshot.Nodes {
		paneID := snapshot.Nodes[idx].PaneID
		if paneID == "" {
			continue
		}
		snapshot.Nodes[idx].PaneState = paneStates[paneID]
	}
}

func (rt *daemonRuntime) pushEscalationNotification(trips []escalationTrip) {
	if rt == nil || rt.cfg == nil || rt.sendPaneNotification == nil {
		return
	}
	uiNode, ok := runtimeUINode(rt.cfg, rt.nodes)
	if !ok || uiNode.PaneID == "" {
		return
	}
	message := escalationNotificationMessage(trips)
	if strings.TrimSpace(message) == "" {
		return
	}
	if err := rt.sendPaneNotification(
		uiNode.PaneID,
		message,
		escalationEnterDelay(rt.cfg, ping.ExtractSimpleName(uiNode.NodeKey)),
		time.Duration(rt.cfg.TmuxTimeout*float64(time.Second)),
		escalationEnterCount(rt.cfg, ping.ExtractSimpleName(uiNode.NodeKey)),
		false,
		time.Duration(rt.cfg.EnterVerifyDelay*float64(time.Second)),
		rt.cfg.EnterRetryMax,
	); err != nil {
		if rt.events != nil {
			rt.events <- statusEscalationErrorEvent(err)
		}
		return
	}
	if rt.events != nil {
		rt.events <- statusEscalationPushedEvent(trips)
	}
}

func escalationEnterDelay(cfg *config.Config, nodeName string) time.Duration {
	delay := cfg.EnterDelay
	if nd := cfg.GetNodeConfig(nodeName).EnterDelay; nd != 0 {
		delay = nd
	}
	return time.Duration(delay * float64(time.Second))
}

func escalationEnterCount(cfg *config.Config, nodeName string) int {
	enterCount := cfg.GetNodeConfig(nodeName).EnterCount
	if enterCount == 0 {
		enterCount = 1
	}
	return notification.ResolveEnterCount(enterCount, func() (string, error) {
		return "", nil
	})
}

func runtimeUINode(cfg *config.Config, nodes map[string]discovery.NodeInfo) (struct {
	NodeKey string
	discovery.NodeInfo
}, bool,
) {
	if cfg == nil || strings.TrimSpace(cfg.UINode) == "" {
		return struct {
			NodeKey string
			discovery.NodeInfo
		}{}, false
	}
	for nodeKey, nodeInfo := range nodes {
		if ping.ExtractSimpleName(nodeKey) == cfg.UINode {
			return struct {
				NodeKey string
				discovery.NodeInfo
			}{NodeKey: nodeKey, NodeInfo: nodeInfo}, true
		}
	}
	return struct {
		NodeKey string
		discovery.NodeInfo
	}{}, false
}

func escalationNotificationMessage(trips []escalationTrip) string {
	var builder strings.Builder
	builder.WriteString("ESCALATION: runtime threshold tripped\n")
	for _, trip := range trips {
		target := "session"
		if trip.Node != "" {
			target = "node=" + trip.Node
		}
		builder.WriteString(fmt.Sprintf("- %s %s observed=%d threshold=%d", trip.Kind, target, trip.Observed, trip.Threshold))
		if trip.Detail != "" {
			builder.WriteString(" detail=" + trip.Detail)
		}
		builder.WriteString("\n")
	}
	builder.WriteString("\nThis is threshold-push on runtime facts, not product-policy escalation.")
	return builder.String()
}

func escalationTripKey(trips []escalationTrip) string {
	parts := make([]string, 0, len(trips))
	for _, trip := range trips {
		parts = append(parts, fmt.Sprintf("%s/%s/%d/%d/%s", trip.Kind, trip.Node, trip.Observed, trip.Threshold, trip.Detail))
	}
	return strings.Join(parts, "|")
}

func statusEscalationPushedEvent(trips []escalationTrip) tui.DaemonEvent {
	details := make([]map[string]interface{}, 0, len(trips))
	for _, trip := range trips {
		details = append(details, map[string]interface{}{
			"kind":      trip.Kind,
			"node":      trip.Node,
			"observed":  trip.Observed,
			"threshold": trip.Threshold,
			"detail":    trip.Detail,
		})
	}
	return tui.DaemonEvent{Type: "escalation_push", Message: "Runtime escalation threshold tripped", Details: map[string]interface{}{"trips": details}}
}

func statusEscalationErrorEvent(err error) tui.DaemonEvent {
	return tui.DaemonEvent{Type: "error", Message: fmt.Sprintf("escalation notification failed: %v", err)}
}

func countRuntimeMarkdown(dir string) int {
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
