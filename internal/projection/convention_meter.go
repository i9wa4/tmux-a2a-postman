package projection

import (
	"sort"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
)

type ConventionMeterState struct {
	Nodes map[string]ConventionMeterNode
}

type ConventionMeterNode struct {
	Node                       string
	CheckedMessages            int
	ViolationCount             int
	ViolationRate              float64
	MissingVerdictOfCount      int
	MissingEvidenceCount       int
	MissingReplyReferenceCount int
}

type conventionMessageObservation struct {
	meta       envelope.Metadata
	observedAt string
}

func ProjectConventionMeterState(sessionDir, sessionName string) (ConventionMeterState, bool, error) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return ConventionMeterState{}, false, nil
	}
	events, err := replayCurrentSessionEvents(sessionDir, state.SessionKey, state.Generation)
	if err != nil {
		return ConventionMeterState{}, false, err
	}

	observations := make(map[string]conventionMessageObservation)
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
		case MailboxProjectionPostedEventType, MailboxProjectionPostConsumedEventType, MailboxProjectionDeliveredEventType, MailboxProjectionReadEventType, MailboxProjectionDeadLetteredEventType:
		default:
			continue
		}

		payload, ok := decodeMailboxEventPayload(event.Payload)
		if !ok || payload.Content == "" {
			continue
		}
		meta := inputRequestMetadataFromPayload(payload)
		if meta.MessageID == "" || meta.From == "" {
			continue
		}
		meta.From = simpleNameForSession(meta.From, sessionName)
		meta.To = simpleNameForSession(meta.To, sessionName)
		key := meta.MessageID
		if key == "" {
			key = event.EventID
		}
		observations[key] = conventionMessageObservation{meta: meta, observedAt: event.OccurredAt}
	}
	if !sawLease || !sawResolution {
		return ConventionMeterState{}, false, nil
	}

	result := ConventionMeterState{Nodes: make(map[string]ConventionMeterNode)}
	keys := make([]string, 0, len(observations))
	for key := range observations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		meta := observations[key].meta
		if meta.From == "" {
			continue
		}
		node := result.Nodes[meta.From]
		node.Node = meta.From
		node.CheckedMessages++
		violated := false
		if missingVerdictOf(meta) {
			node.MissingVerdictOfCount++
			violated = true
		}
		if missingEvidence(meta) {
			node.MissingEvidenceCount++
			violated = true
		}
		if missingReplyReference(meta) {
			node.MissingReplyReferenceCount++
			violated = true
		}
		if violated {
			node.ViolationCount++
		}
		if node.CheckedMessages > 0 {
			node.ViolationRate = float64(node.ViolationCount) / float64(node.CheckedMessages)
		}
		result.Nodes[meta.From] = node
	}
	return result, true, nil
}

func missingVerdictOf(meta envelope.Metadata) bool {
	return strings.TrimSpace(meta.Verdict) != "" && strings.TrimSpace(meta.VerdictOf) == ""
}

func missingEvidence(meta envelope.Metadata) bool {
	if !isCompletionClaim(meta.Body) {
		return false
	}
	return !hasEvidenceProof(meta.Body)
}

func missingReplyReference(meta envelope.Metadata) bool {
	if strings.TrimSpace(meta.ReplyTo) != "" {
		return false
	}
	if strings.TrimSpace(meta.FillsInputRequestID) != "" {
		return true
	}
	line := firstNonEmptyBodyLine(meta.Body)
	for _, state := range []string{"ACK", "DONE", "BLOCKED", "PASS", "APPROVED", "NOT APPROVED"} {
		if startsWithStateLine(line, state) {
			return true
		}
	}
	return false
}

func isCompletionClaim(body string) bool {
	line := firstNonEmptyBodyLine(body)
	for _, state := range []string{"DONE", "PASS", "APPROVED"} {
		if startsWithStateLine(line, state) {
			return true
		}
	}
	return false
}

func hasEvidenceProof(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		normalized := strings.ToLower(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(normalized, "task artifact:"):
			return true
		case strings.HasPrefix(normalized, "evidence:"):
			return true
		case strings.HasPrefix(normalized, "verification:"):
			return true
		case strings.HasPrefix(normalized, "changed files:"):
			return true
		}
	}
	return false
}
