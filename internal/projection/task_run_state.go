package projection

import (
	"sort"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

type TaskRunState struct {
	Tasks []TaskRunDetail
}

type TaskRunDetail struct {
	TaskID                        string
	RunID                         string
	OriginatingMessageID          string
	ThreadID                      string
	AssignedNode                  string
	LatestMessageID               string
	OpenInputRequestIDs           []string
	State                         string
	TerminalMessageID             string
	Ambiguous                     bool
	AmbiguityReason               string
	ReviewApprovalMessageID       string
	AcceptanceMessageID           string
	ReviewState                   string
	ReviewMessageID               string
	CompletionState               string
	CompletionMessageID           string
	CompletionHasTaskArtifact     bool
	CompletionChecklistPassed     bool
	CompletionHasEvidence         bool
	CompletionNoRemainingBlockers bool
}

type taskRunAccumulator struct {
	detail          TaskRunDetail
	firstOccurredAt string
	latestAt        string
	threadIDs       map[string]bool
	openRequests    map[string]bool
}

func ProjectTaskRunState(sessionDir, sessionName string) (TaskRunState, bool, error) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return TaskRunState{}, false, nil
	}

	events, err := journal.Replay(sessionDir)
	if err != nil || len(events) == 0 {
		return TaskRunState{}, false, err
	}

	byKey := make(map[string]*taskRunAccumulator)
	sawLease := false
	sawResolution := false
	sawTaskMetadata := false

	for _, event := range events {
		if event.SessionKey != state.SessionKey || event.Generation != state.Generation {
			continue
		}
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
		if !ok {
			continue
		}
		meta := taskRunMetadataFromPayload(payload)
		if meta.MessageID == "" || (meta.TaskID == "" && meta.RunID == "") {
			continue
		}
		sawTaskMetadata = true
		meta.From = simpleNameForSession(meta.From, sessionName)
		meta.To = simpleNameForSession(meta.To, sessionName)

		key := taskRunKey(meta.TaskID, meta.RunID)
		acc := byKey[key]
		if acc == nil {
			acc = &taskRunAccumulator{
				detail: TaskRunDetail{
					TaskID:               meta.TaskID,
					RunID:                meta.RunID,
					OriginatingMessageID: meta.MessageID,
					ThreadID:             meta.ThreadID,
					AssignedNode:         meta.To,
					LatestMessageID:      meta.MessageID,
					State:                "active",
					ReviewState:          "none",
					CompletionState:      "none",
				},
				firstOccurredAt: event.OccurredAt,
				threadIDs:       make(map[string]bool),
				openRequests:    make(map[string]bool),
			}
			byKey[key] = acc
		}

		if event.OccurredAt < acc.firstOccurredAt || acc.firstOccurredAt == "" {
			acc.firstOccurredAt = event.OccurredAt
			acc.detail.OriginatingMessageID = meta.MessageID
			acc.detail.AssignedNode = meta.To
		}
		if event.OccurredAt >= acc.latestAt {
			acc.latestAt = event.OccurredAt
			acc.detail.LatestMessageID = meta.MessageID
		}
		if meta.ThreadID != "" {
			acc.threadIDs[meta.ThreadID] = true
			if acc.detail.ThreadID == "" {
				acc.detail.ThreadID = meta.ThreadID
			}
		}
		if envelope.ResolveReplyPolicyFromMetadata(meta) == "required" && meta.InputRequestID != "" {
			acc.openRequests[meta.InputRequestID] = true
		}
		if meta.FillsInputRequestID != "" {
			delete(acc.openRequests, meta.FillsInputRequestID)
			acc.detail.TerminalMessageID = meta.MessageID
		}
		terminal := taskTerminalFromBody(meta.Body)
		switch terminal.kind {
		case "approved":
			acc.detail.ReviewState = "approved"
			acc.detail.ReviewMessageID = meta.MessageID
			acc.detail.ReviewApprovalMessageID = meta.MessageID
		case "rejected":
			acc.detail.ReviewState = "rejected"
			acc.detail.ReviewMessageID = meta.MessageID
		case "done":
			acc.detail.CompletionState = "done"
			acc.detail.CompletionMessageID = meta.MessageID
			acc.detail.AcceptanceMessageID = meta.MessageID
			acc.detail.CompletionHasTaskArtifact = terminal.hasTaskArtifact
			acc.detail.CompletionChecklistPassed = terminal.checklistPassed
			acc.detail.CompletionHasEvidence = terminal.hasEvidence
			acc.detail.CompletionNoRemainingBlockers = terminal.noRemainingBlockers
		case "blocked":
			acc.detail.CompletionState = "blocked"
			acc.detail.CompletionMessageID = meta.MessageID
		}
	}

	if !sawLease || !sawResolution || !sawTaskMetadata {
		return TaskRunState{}, false, nil
	}

	projected := TaskRunState{Tasks: make([]TaskRunDetail, 0, len(byKey))}
	for _, acc := range byKey {
		switch {
		case len(acc.threadIDs) > 1:
			acc.detail.Ambiguous = true
			acc.detail.State = "ambiguous"
			acc.detail.AmbiguityReason = "multiple thread_id values for external task/run id"
		case len(acc.openRequests) > 0:
			acc.detail.State = "waiting_input"
		case acc.detail.TerminalMessageID != "":
			acc.detail.State = "terminal"
		default:
			acc.detail.State = "active"
		}
		acc.detail.OpenInputRequestIDs = sortedMapKeys(acc.openRequests)
		projected.Tasks = append(projected.Tasks, acc.detail)
	}
	sort.Slice(projected.Tasks, func(i, j int) bool {
		if projected.Tasks[i].TaskID != projected.Tasks[j].TaskID {
			return projected.Tasks[i].TaskID < projected.Tasks[j].TaskID
		}
		if projected.Tasks[i].RunID != projected.Tasks[j].RunID {
			return projected.Tasks[i].RunID < projected.Tasks[j].RunID
		}
		return projected.Tasks[i].OriginatingMessageID < projected.Tasks[j].OriginatingMessageID
	})
	return projected, true, nil
}

type taskTerminal struct {
	kind                string
	hasTaskArtifact     bool
	checklistPassed     bool
	hasEvidence         bool
	noRemainingBlockers bool
}

func taskTerminalFromBody(body string) taskTerminal {
	trimmed := strings.TrimSpace(body)
	firstLine, _, _ := strings.Cut(trimmed, "\n")
	firstLine = strings.TrimSpace(firstLine)
	terminal := taskTerminal{}
	switch {
	case strings.HasPrefix(firstLine, "APPROVED:"):
		terminal.kind = "approved"
	case strings.HasPrefix(firstLine, "NOT APPROVED:"):
		terminal.kind = "rejected"
	case firstLine == "DONE" || strings.HasPrefix(firstLine, "DONE:"):
		terminal.kind = "done"
	case firstLine == "BLOCKED" || strings.HasPrefix(firstLine, "BLOCKED:"):
		terminal.kind = "blocked"
	default:
		return terminal
	}
	for _, line := range strings.Split(trimmed, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "task artifact":
			terminal.hasTaskArtifact = value != ""
		case "original checklist":
			terminal.checklistPassed = strings.EqualFold(value, "PASS")
		case "evidence":
			terminal.hasEvidence = value != ""
		case "remaining blockers":
			terminal.noRemainingBlockers = strings.EqualFold(value, "none")
		}
	}
	return terminal
}

func taskRunMetadataFromPayload(payload journal.MailboxEventPayload) envelope.Metadata {
	meta, err := envelope.ParseMetadata(payload.Content)
	if err != nil {
		meta = envelope.Metadata{Body: envelope.BodyFromContent(payload.Content)}
	}
	if meta.MessageID == "" {
		meta.MessageID = payload.MessageID
	}
	if meta.From == "" {
		meta.From = payload.From
	}
	if meta.To == "" {
		meta.To = payload.To
	}
	if meta.ReplyPolicy == "" {
		meta.ReplyPolicy = payload.ReplyPolicy
	}
	if meta.ReplyTo == "" {
		meta.ReplyTo = payload.ReplyTo
	}
	if meta.ThreadID == "" {
		meta.ThreadID = payload.ThreadID
	}
	if meta.TaskID == "" {
		meta.TaskID = payload.TaskID
	}
	if meta.RunID == "" {
		meta.RunID = payload.RunID
	}
	if meta.InputRequestID == "" {
		meta.InputRequestID = payload.InputRequestID
	}
	if meta.FillsInputRequestID == "" {
		meta.FillsInputRequestID = payload.FillsInputRequestID
	}
	return meta
}

func taskRunKey(taskID, runID string) string {
	return taskID + "\x00" + runID
}

func sortedMapKeys(values map[string]bool) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
