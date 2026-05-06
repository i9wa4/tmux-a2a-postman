package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

func enrichSessionHealth(health *status.SessionHealth, sessionDir string, now time.Time) {
	health.SchemaVersion = status.SchemaVersion

	blockedByNode := map[string][]projection.BlockedReport{}
	if sessionDir != "" {
		if blocked, ok, err := projection.ProjectBlockedReportState(sessionDir, health.SessionName); err == nil && ok {
			blockedByNode = blocked.ReportsByNode
		}
	}

	health.Delivery = collectSessionDelivery(sessionDir, health.Queues, now)
	for idx := range health.Nodes {
		node := &health.Nodes[idx]
		node.Queues = &status.NodeQueues{InboxCount: node.InboxCount}
		node.NodeLocal = deriveNodeLocalHealth(*node)
		node.Flow = deriveNodeFlowHealth(*node, blockedByNode[node.Name])
		applyNodeSeverity(node)
	}
	applySessionSeverity(health)
}

func collectSessionDelivery(sessionDir string, queues status.SessionQueues, now time.Time) *status.DeliveryHealth {
	delivery := &status.DeliveryHealth{
		State:             "ok",
		Severity:          "ok",
		EvidenceLevel:     "proven",
		EvidenceSource:    "filesystem",
		PostCount:         queues.PostCount,
		DeadLetterCount:   queues.DeadLetterCount,
		StuckAfterSeconds: status.DeliveryStuckAfterSeconds,
	}
	if queues.DeadLetterCount > 0 {
		delivery.State = "delivery_failure"
		delivery.Severity = "delivery_failure"
		delivery.Reason = "dead-letter files exist"
		delivery.Action = "inspect_dead_letter"
		delivery.Items = collectDeadLetterItems(sessionDir)
		return delivery
	}
	if queues.PostCount == 0 {
		delivery.Reason = "no pending post or dead-letter files"
		return delivery
	}

	items, ok := collectPendingPostItems(sessionDir, now)
	delivery.Items = items
	if !ok || len(items) == 0 {
		delivery.State = "unknown"
		delivery.Severity = "ok"
		delivery.EvidenceLevel = "unknown"
		delivery.EvidenceSource = "filesystem"
		delivery.Reason = "pending post evidence could not be inspected"
		delivery.Action = "inspect_state_directory"
		return delivery
	}
	oldest := items[0]
	delivery.OldestPostAgeSeconds = oldest.AgeSeconds
	delivery.OldestPostObservedAt = oldest.EnqueuedAt
	delivery.EvidenceSource = oldest.EnqueuedAtSource
	if oldest.AgeSeconds >= status.DeliveryStuckAfterSeconds {
		delivery.State = "delivery_stuck"
		delivery.Severity = "delivery_stuck"
		delivery.EvidenceLevel = oldest.EvidenceLevel
		delivery.Reason = "oldest post item is at or above delivery_stuck threshold"
		delivery.Action = "inspect_delivery"
		return delivery
	}
	delivery.State = "queued"
	delivery.Severity = "working"
	delivery.EvidenceLevel = "proven"
	delivery.Reason = "post items are queued below delivery_stuck threshold"
	delivery.Action = "wait"
	return delivery
}

func collectPendingPostItems(sessionDir string, now time.Time) ([]status.HealthItem, bool) {
	if sessionDir == "" {
		return nil, false
	}
	postDir := filepath.Join(sessionDir, "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		return nil, os.IsNotExist(err)
	}

	journalTimes := map[string]string{}
	if projected, ok, err := projection.ProjectPendingPostEnqueueTimes(sessionDir); err == nil && ok {
		journalTimes = projected
	}
	items := make([]status.HealthItem, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		relativePath := filepath.Join("post", entry.Name())
		info, err := entry.Info()
		if err != nil {
			return nil, false
		}
		enqueuedAt := info.ModTime().UTC()
		enqueuedAtText := enqueuedAt.Format(time.RFC3339)
		source := "post_file_mtime"
		evidenceLevel := "proven"
		if journalText := strings.TrimSpace(journalTimes[relativePath]); journalText != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, journalText); err == nil {
				enqueuedAt = parsed.UTC()
				enqueuedAtText = enqueuedAt.Format(time.RFC3339Nano)
				source = "journal.mailbox_projection_posted"
			} else {
				evidenceLevel = "inferred"
			}
		}
		age := int(now.Sub(enqueuedAt).Seconds())
		if age < 0 {
			age = 0
		}
		items = append(items, status.HealthItem{
			MessageID:        entry.Name(),
			Path:             relativePath,
			EvidenceSource:   source,
			EvidenceLevel:    evidenceLevel,
			EnqueuedAt:       enqueuedAtText,
			EnqueuedAtSource: source,
			AgeSeconds:       age,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].AgeSeconds != items[j].AgeSeconds {
			return items[i].AgeSeconds > items[j].AgeSeconds
		}
		return items[i].MessageID < items[j].MessageID
	})
	return items, true
}

func collectDeadLetterItems(sessionDir string) []status.HealthItem {
	if sessionDir == "" {
		return nil
	}
	deadLetterDir := filepath.Join(sessionDir, "dead-letter")
	entries, err := os.ReadDir(deadLetterDir)
	if err != nil {
		return nil
	}
	items := make([]status.HealthItem, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		items = append(items, status.HealthItem{
			MessageID:      entry.Name(),
			Path:           filepath.Join("dead-letter", entry.Name()),
			EvidenceSource: "dead_letter_file",
			EvidenceLevel:  "proven",
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].MessageID < items[j].MessageID
	})
	return items
}

func deriveNodeLocalHealth(node status.NodeHealth) *status.NodeLocalHealth {
	local := &status.NodeLocalHealth{
		State:          "quiet",
		Severity:       "ok",
		EvidenceLevel:  "proven",
		EvidenceSource: "pane_activity",
		Reason:         "pane is live without recent activity evidence",
		PaneState:      node.PaneState,
		CurrentCommand: node.CurrentCommand,
		ScreenProgress: node.ScreenProgress,
	}
	if status.NormalizePaneState(node.PaneState) == "stale" {
		local.State = "stale"
		local.Severity = "attention_stale"
		local.Reason = "pane state is stale"
		return local
	}
	if node.PaneState == "" {
		local.State = "unknown"
		local.Severity = "ok"
		local.EvidenceLevel = "unknown"
		local.Reason = "pane activity evidence is missing"
		return local
	}
	if node.PaneState == "active" || (node.ScreenProgress != nil && node.ScreenProgress.EvidenceState == "changed") {
		local.State = "working"
		local.Severity = "working"
		local.EvidenceLevel = "inferred"
		local.Reason = "pane activity suggests current work"
		return local
	}
	local.State = "live"
	local.Reason = "pane is live"
	return local
}

func deriveNodeFlowHealth(node status.NodeHealth, blockedReports []projection.BlockedReport) *status.NodeFlowHealth {
	flow := &status.NodeFlowHealth{
		State:          "idle",
		Severity:       "ok",
		EvidenceLevel:  "proven",
		EvidenceSource: "input_requests",
		Reason:         "no open input requests or blocked reports",
		InputRequests: status.InputRequestSummary{
			InputRequiredCount:  node.InputRequiredCount,
			WaitingOnInputCount: node.WaitingOnInputCount,
			InfoUnreadCount:     node.InfoUnreadCount,
			InputRequired:       node.InputRequired,
			WaitingOnInput:      node.WaitingOnInput,
		},
		Blocked: status.BlockedState{
			State:     "clear",
			OpenCount: 0,
		},
	}
	if len(blockedReports) > 0 {
		flow.Blocked = status.BlockedState{
			State:     "open",
			OpenCount: len(blockedReports),
			Items:     blockedReportItems(blockedReports),
		}
		flow.State = "blocked"
		flow.Severity = "blocked"
		flow.EvidenceLevel = blockedReports[0].EvidenceLevel
		flow.EvidenceSource = blockedReports[0].EvidenceSource
		flow.Reason = "open blocked report"
		flow.Action = "inspect_blocker"
		return flow
	}
	if node.InputRequiredCount > 0 {
		flow.State = "needs_action"
		flow.Severity = "needs_action"
		flow.EvidenceSource = "flow.input_requests"
		flow.Reason = "inbound required input request is open"
		flow.Action = "pop_and_reply"
		return flow
	}
	if node.VisibleState == "pending" && node.InboxCount > 0 {
		flow.State = "needs_action"
		flow.Severity = "needs_action"
		flow.EvidenceLevel = "inferred"
		flow.EvidenceSource = "legacy_inbox"
		flow.Reason = "unclassified unread inbox mail makes visible state pending"
		flow.Action = "pop_and_classify"
		return flow
	}
	if node.WaitingOnInputCount > 0 {
		flow.State = "expected_wait"
		flow.Severity = "expected_wait"
		flow.EvidenceSource = "flow.input_requests"
		flow.Reason = "outbound required input request is waiting"
		flow.Action = "wait"
		return flow
	}
	return flow
}

func blockedReportItems(reports []projection.BlockedReport) []status.HealthItem {
	items := make([]status.HealthItem, 0, len(reports))
	for _, report := range reports {
		items = append(items, status.HealthItem{
			Node:            report.Node,
			MessageID:       report.MessageID,
			BlockedReportID: report.BlockedReportID,
			Scope:           report.Scope,
			ScopeID:         report.ScopeID,
			Reason:          report.Reason,
			EvidenceSource:  report.EvidenceSource,
			EvidenceLevel:   report.EvidenceLevel,
			ObservedAt:      report.ObservedAt,
		})
	}
	return items
}

func applyNodeSeverity(node *status.NodeHealth) {
	node.Severity = "ok"
	node.SeveritySource = "node.flow"
	node.SeverityReason = ""
	if node.NodeLocal != nil {
		node.Severity = node.NodeLocal.Severity
		node.SeveritySource = "node.node_local"
		node.SeverityReason = node.NodeLocal.Reason
	}
	if node.Flow != nil && status.SeverityRank(node.Flow.Severity) >= status.SeverityRank(node.Severity) {
		node.Severity = node.Flow.Severity
		node.SeveritySource = "node.flow"
		node.SeverityReason = node.Flow.Reason
	}
}

func applySessionSeverity(health *status.SessionHealth) {
	health.Severity = "ok"
	health.SeveritySource = "session"
	health.SeverityReason = "session is healthy"
	if health.VisibleState == "unavailable" || health.VisibleState == "unowned" {
		health.Severity = "attention_stale"
		health.SeveritySource = "session"
		health.SeverityReason = "session health is unavailable"
	}
	if health.Delivery != nil {
		health.Severity = status.WorseSeverity(health.Severity, health.Delivery.Severity)
		if health.Severity == health.Delivery.Severity && health.Delivery.Severity != "ok" {
			health.SeveritySource = "delivery"
			health.SeverityReason = health.Delivery.Reason
		}
	}
	for _, node := range health.Nodes {
		if status.SeverityRank(node.Severity) > status.SeverityRank(health.Severity) {
			health.Severity = node.Severity
			health.SeveritySource = node.SeveritySource
			health.SeverityReason = node.SeverityReason
		}
	}
	health.CompactSeverity = buildCompactSeverity(*health)
}

func buildCompactSeverity(health status.SessionHealth) string {
	if health.Delivery != nil && health.Severity == health.Delivery.Severity && health.Delivery.Severity != "ok" {
		switch health.Delivery.Severity {
		case "delivery_failure":
			return "delivery_failure:delivery:dead_letter_count=" + strconv.Itoa(health.Delivery.DeadLetterCount)
		case "delivery_stuck":
			return "delivery_stuck:delivery:oldest_post_age=" + strconv.Itoa(health.Delivery.OldestPostAgeSeconds) + "s"
		case "working":
			return "working:delivery:post_count=" + strconv.Itoa(health.Delivery.PostCount)
		}
	}
	for _, node := range health.Nodes {
		if node.Severity != health.Severity {
			continue
		}
		return compactNodeSeverity(node)
	}
	if health.Severity == "attention_stale" {
		return "attention_stale:session"
	}
	return "ok:session"
}

func compactNodeSeverity(node status.NodeHealth) string {
	inferred := ""
	if node.Flow != nil && node.Flow.Severity == node.Severity && node.Flow.EvidenceLevel == "inferred" {
		inferred = "?"
	} else if node.NodeLocal != nil && node.NodeLocal.Severity == node.Severity && node.NodeLocal.EvidenceLevel == "inferred" {
		inferred = "?"
	}
	switch node.Severity {
	case "working":
		return "working" + inferred + ":node=" + node.Name
	case "expected_wait":
		return "expected_wait:node=" + node.Name + ":waiting_on_input=" + strconv.Itoa(node.WaitingOnInputCount)
	case "needs_action":
		if node.InputRequiredCount == 0 && node.InboxCount > 0 {
			return "needs_action" + inferred + ":node=" + node.Name + ":inbox_count=" + strconv.Itoa(node.InboxCount)
		}
		return "needs_action:node=" + node.Name + ":input_required=" + strconv.Itoa(node.InputRequiredCount)
	case "blocked":
		if node.Flow != nil && len(node.Flow.Blocked.Items) > 0 && node.Flow.Blocked.Items[0].BlockedReportID != "" {
			return "blocked" + inferred + ":node=" + node.Name + ":blocked_report=" + node.Flow.Blocked.Items[0].BlockedReportID
		}
		return "blocked" + inferred + ":node=" + node.Name
	case "attention_stale":
		return "attention_stale:node=" + node.Name
	default:
		return "ok:session"
	}
}
