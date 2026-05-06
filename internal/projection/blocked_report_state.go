package projection

import (
	"sort"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
)

type BlockedReportState struct {
	ReportsByNode map[string][]BlockedReport
}

type BlockedReport struct {
	Node            string
	MessageID       string
	BlockedReportID string
	Scope           string
	ScopeID         string
	Reason          string
	EvidenceLevel   string
	EvidenceSource  string
	ObservedAt      string
}

func ProjectBlockedReportState(sessionDir, sessionName string) (BlockedReportState, bool, error) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return BlockedReportState{}, false, nil
	}

	events, err := replayCurrentSessionEvents(sessionDir, state.SessionKey, state.Generation)
	if err != nil {
		return BlockedReportState{}, false, err
	}

	open := make(map[string]BlockedReport)
	sawLease := false
	sawResolution := false
	for _, event := range events {
		switch event.Type {
		case "lease_acquired":
			sawLease = true
			continue
		case "session_resolved":
			sawResolution = true
			continue
		case MailboxProjectionPostedEventType, MailboxProjectionPostConsumedEventType, MailboxProjectionDeliveredEventType, MailboxProjectionReadEventType:
		default:
			continue
		}

		payload, ok := decodeMailboxEventPayload(event.Payload)
		if !ok || payload.Content == "" {
			continue
		}
		meta := inputRequestMetadataFromPayload(payload)
		if meta.MessageID == "" {
			continue
		}
		meta.From = simpleNameForSession(meta.From, sessionName)
		meta.To = simpleNameForSession(meta.To, sessionName)
		if meta.From == "" {
			continue
		}

		if isBlockedClear(meta) {
			clearBlockedReports(open, meta)
			continue
		}
		if isTerminalBlockedClear(meta) {
			clearTerminalBlockedReports(open, meta)
			continue
		}
		report, ok := blockedReportFromMetadata(meta, event.OccurredAt)
		if !ok {
			continue
		}
		open[blockedReportKey(report.Node, report.Scope, report.ScopeID)] = report
	}
	if !sawLease || !sawResolution {
		return BlockedReportState{}, false, nil
	}

	result := BlockedReportState{ReportsByNode: make(map[string][]BlockedReport)}
	reports := make([]BlockedReport, 0, len(open))
	for _, report := range open {
		reports = append(reports, report)
	}
	sort.Slice(reports, func(i, j int) bool {
		if reports[i].ObservedAt != reports[j].ObservedAt {
			return reports[i].ObservedAt < reports[j].ObservedAt
		}
		return reports[i].MessageID < reports[j].MessageID
	})
	for _, report := range reports {
		result.ReportsByNode[report.Node] = append(result.ReportsByNode[report.Node], report)
	}
	return result, true, nil
}

func blockedReportFromMetadata(meta envelope.Metadata, observedAt string) (BlockedReport, bool) {
	line := firstNonEmptyBodyLine(meta.Body)
	messageType := strings.ToLower(strings.TrimSpace(meta.MessageType))
	evidenceLevel := ""
	evidenceSource := ""
	if messageType == "blocked_report" {
		evidenceLevel = "proven"
		evidenceSource = "metadata.message_type"
	} else if startsWithStateLine(line, "BLOCKED") {
		evidenceLevel = "inferred"
		evidenceSource = "body.first_line"
	} else {
		return BlockedReport{}, false
	}

	scope, scopeID := blockedScope(meta)
	reason := strings.TrimSpace(meta.BlockedReason)
	if reason == "" && startsWithStateLine(line, "BLOCKED") {
		_, reason, _ = strings.Cut(line, ":")
		reason = strings.TrimSpace(reason)
	}
	return BlockedReport{
		Node:            meta.From,
		MessageID:       meta.MessageID,
		BlockedReportID: meta.BlockedReportID,
		Scope:           scope,
		ScopeID:         scopeID,
		Reason:          reason,
		EvidenceLevel:   evidenceLevel,
		EvidenceSource:  evidenceSource,
		ObservedAt:      observedAt,
	}, true
}

func isBlockedClear(meta envelope.Metadata) bool {
	return strings.EqualFold(strings.TrimSpace(meta.MessageType), "blocked_clear")
}

func isTerminalBlockedClear(meta envelope.Metadata) bool {
	line := firstNonEmptyBodyLine(meta.Body)
	return startsWithStateLine(line, "DONE") || startsWithStateLine(line, "UNBLOCKED")
}

func clearBlockedReports(open map[string]BlockedReport, meta envelope.Metadata) {
	scope, scopeID := blockedScope(meta)
	for key, report := range open {
		if report.Node != meta.From {
			continue
		}
		if meta.BlockedReportID != "" && report.BlockedReportID == meta.BlockedReportID {
			delete(open, key)
			continue
		}
		if scopeID != "" && report.Scope == scope && report.ScopeID == scopeID {
			delete(open, key)
		}
	}
}

func clearTerminalBlockedReports(open map[string]BlockedReport, meta envelope.Metadata) {
	scope, scopeID := blockedScope(meta)
	for key, report := range open {
		if report.Node != meta.From {
			continue
		}
		if scopeID != "" && (scope != "node" || scopeID != meta.From) {
			if report.Scope == scope && report.ScopeID == scopeID {
				delete(open, key)
			}
			continue
		}
		if report.Scope == "node" && report.ScopeID == meta.From && report.EvidenceLevel == "inferred" {
			delete(open, key)
		}
	}
}

func blockedScope(meta envelope.Metadata) (string, string) {
	if meta.BlockedScopeID != "" {
		scope := strings.TrimSpace(meta.BlockedScope)
		if scope == "" {
			scope = "custom"
		}
		return scope, meta.BlockedScopeID
	}
	if meta.InputRequestID != "" {
		return "input_request", meta.InputRequestID
	}
	if meta.FillsInputRequestID != "" {
		return "input_request", meta.FillsInputRequestID
	}
	if meta.ThreadID != "" {
		return "thread", meta.ThreadID
	}
	if meta.ReplyTo != "" {
		return "reply_to", meta.ReplyTo
	}
	return "node", meta.From
}

func blockedReportKey(node, scope, scopeID string) string {
	return node + "\x00" + scope + "\x00" + scopeID
}

func firstNonEmptyBodyLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func startsWithStateLine(line, state string) bool {
	upper := strings.ToUpper(strings.TrimSpace(line))
	return upper == state || strings.HasPrefix(upper, state+":")
}
