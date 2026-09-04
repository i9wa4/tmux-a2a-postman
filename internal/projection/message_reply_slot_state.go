package projection

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
)

const DefaultInputRequestStaleAfterSeconds = 3600

type MessageInputRequestState struct {
	UnreadCounts         map[string]int
	InputRequiredCounts  map[string]int
	WaitingOnInputCounts map[string]int
	InfoUnreadCounts     map[string]int
	InputRequired        []InputRequestDetail
	WaitingOnInput       []InputRequestDetail
	RequestSatisfaction  map[string]RequestSatisfaction
}

type InputRequestDetail struct {
	Direction      string
	MessageID      string
	InputRequestID string
	Sender         string
	Recipient      string
	ReplyPolicy    string
	OpenedAt       string
	OpenedAtSource string
	OpenedEventID  string
	ReadAt         string
	ReadEventID    string
	// CommandApprovalThreadID and CommandHash bind an approval opening to its
	// control-plane tuple. They are replay-local correlation data: a reply may
	// not opt out of strict validation by omitting or changing its thread.
	CommandApprovalThreadID        string
	CommandHash                    string
	CommandApprovalOpeningThreadID string
	CommandApprovalOpeningHash     string
}

type RequestSatisfaction struct {
	OpenedCount              int
	FilledCount              int
	OpenCount                int
	DeadLetteredCount        int
	StaleOpenCount           int
	StaleAfterSeconds        int
	TotalTimeToFillSeconds   int
	AverageTimeToFillSeconds int
	LongestOpenAgeSeconds    int
}

type CommandApprovalReplySlotReconciliationPlan struct {
	Status          string `json:"status"`
	Reason          string `json:"reason,omitempty"`
	InputRequestID  string `json:"input_request_id"`
	ThreadID        string `json:"thread_id"`
	CommandHash     string `json:"command_hash"`
	DecisionEventID string `json:"decision_event_id,omitempty"`
	DecisionAt      string `json:"decision_at,omitempty"`
	ExistingEventID string `json:"existing_event_id,omitempty"`
}

func ProjectMessageInputRequestState(sessionDir, sessionName string) (MessageInputRequestState, bool, error) {
	return ProjectMessageInputRequestStateAt(sessionDir, sessionName, time.Now(), DefaultInputRequestStaleAfterSeconds)
}

func ValidateCommandApprovalReplySlotReconciliation(sessionDir, sessionName string, repair journal.CommandApprovalReplySlotReconciledPayload, now time.Time) (CommandApprovalReplySlotReconciliationPlan, bool, error) {
	plan := CommandApprovalReplySlotReconciliationPlan{
		Status:          "rejected",
		InputRequestID:  repair.InputRequestID,
		ThreadID:        repair.ThreadID,
		CommandHash:     repair.CommandHash,
		DecisionEventID: repair.DecisionEventID,
	}
	if reason := missingCommandApprovalReplySlotReconciliationField(repair); reason != "" {
		plan.Reason = reason
		return plan, true, nil
	}

	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		plan.Reason = "current session state not found"
		return plan, false, nil
	}
	events, err := journal.Replay(sessionDir)
	if err != nil {
		return CommandApprovalReplySlotReconciliationPlan{}, false, err
	}
	commandApprovalThreads := make(map[string]CommandApprovalThread)
	effectiveCommandApprovalDecisions := make(map[string][]journal.Event)
	commandApprovalOpenings := make(map[string]map[string]InputRequestDetail)
	var matchingReconciliation journal.Event
	sawLease := false
	sawResolution := false
	for _, event := range events {
		if event.SessionKey != state.SessionKey || event.Generation != state.Generation {
			continue
		}
		switch event.Type {
		case "lease_acquired":
			sawLease = true
		case "session_resolved":
			sawResolution = true
		case journal.CommandApprovalRequestedEventType:
			if err := applyCommandApprovalRequest(commandApprovalThreads, event); err != nil {
				return CommandApprovalReplySlotReconciliationPlan{}, false, err
			}
		case journal.CommandApprovalDecidedEventType:
			if err := applyCommandApprovalDecision(commandApprovalThreads, event); err != nil {
				return CommandApprovalReplySlotReconciliationPlan{}, false, err
			}
			thread := commandApprovalThreads[event.ThreadID]
			if (thread.Status == CommandApprovalStatusApproved || thread.Status == CommandApprovalStatusRejected) && commandApprovalDecisionOccurredBeforeExpiry(thread, event.OccurredAt) {
				effectiveCommandApprovalDecisions[event.ThreadID] = append(effectiveCommandApprovalDecisions[event.ThreadID], event)
			}
		case journal.CommandApprovalReplySlotReconciledEventType:
			var existing journal.CommandApprovalReplySlotReconciledPayload
			if err := json.Unmarshal(event.Payload, &existing); err != nil {
				return CommandApprovalReplySlotReconciliationPlan{}, false, err
			}
			if commandApprovalReplySlotReconciliationPayloadEqual(existing, repair) && matchingReconciliation.EventID == "" {
				matchingReconciliation = event
			}
		case MailboxProjectionPostConsumedEventType, MailboxProjectionDeliveredEventType:
			payload, ok := decodeMailboxEventPayload(event.Payload)
			if !ok || payload.Content == "" {
				continue
			}
			meta := inputRequestMetadataFromPayload(payload)
			if meta.MessageID == "" || envelope.ResolveReplyPolicyFromMetadata(meta) != "required" {
				continue
			}
			meta.From = simpleNameForSession(meta.From, sessionName)
			meta.To = simpleNameForSession(meta.To, sessionName)
			recordCommandApprovalOpening(commandApprovalOpenings, newInputRequestDetail(meta, "", event.OccurredAt, event.Type, event.EventID, commandApprovalThreads))
		}
	}
	if !sawLease || !sawResolution {
		plan.Reason = "current session journal is incomplete"
		return plan, false, nil
	}
	decisions := effectiveCommandApprovalDecisions[repair.ThreadID]
	if len(decisions) != 1 {
		plan.Reason = "expected exactly one effective terminal command approval decision"
		return plan, true, nil
	}
	decision := decisions[0]
	plan.DecisionAt = decision.OccurredAt
	thread, exists := commandApprovalThreads[repair.ThreadID]
	if !exists {
		plan.Reason = "command approval request not found"
		return plan, true, nil
	}
	if decision.EventID != repair.DecisionEventID || thread.InputRequestID != repair.InputRequestID || thread.CommandHash != repair.CommandHash || thread.Requester != repair.Requester || thread.RequesterAddress != repair.RequesterAddress || thread.CommandApproverNode != repair.Approver || thread.CommandApproverAddress != repair.ApproverAddress {
		plan.Reason = "reconciliation tuple does not match recorded request and decision"
		return plan, true, nil
	}
	if opening, ok := uniqueCommandApprovalOpening(commandApprovalOpenings, repair.InputRequestID); !ok {
		plan.Reason = "expected exactly one distinct command approval opening tuple"
		return plan, true, nil
	} else if !commandApprovalOpeningMatchesRepair(opening, repair) {
		plan.Reason = "recorded input request opening is not the matching command approval slot"
		return plan, true, nil
	}
	var inputState MessageInputRequestState
	if matchingReconciliation.EventID != "" {
		inputState, ok, err = projectMessageInputRequestStateAt(sessionDir, sessionName, now, DefaultInputRequestStaleAfterSeconds, func(existing journal.CommandApprovalReplySlotReconciledPayload) bool {
			return commandApprovalReplySlotReconciliationPayloadEqual(existing, repair)
		})
	} else {
		inputState, ok, err = ProjectMessageInputRequestStateAt(sessionDir, sessionName, now, DefaultInputRequestStaleAfterSeconds)
	}
	if err != nil {
		return CommandApprovalReplySlotReconciliationPlan{}, false, err
	}
	if !ok {
		plan.Reason = "input request projection is unavailable"
		return plan, false, nil
	}
	opening, ok := commandApprovalOpenSlot(inputState, repair.InputRequestID)
	if !ok {
		plan.Reason = "matching command approval reply slot is not currently open"
		return plan, true, nil
	}
	if !commandApprovalOpeningMatchesRepair(opening, repair) {
		plan.Reason = "open input request is not the matching command approval slot"
		return plan, true, nil
	}
	if matchingReconciliation.EventID != "" {
		plan.Status = "already_reconciled"
		plan.Reason = "matching reconciliation event already exists"
		plan.ExistingEventID = matchingReconciliation.EventID
		return plan, true, nil
	}
	plan.Status = "ready"
	plan.Reason = "matching terminal command approval decision can reconcile the open reply slot"
	return plan, true, nil
}

func ProjectMessageInputRequestStateAt(sessionDir, sessionName string, now time.Time, staleAfterSeconds int) (MessageInputRequestState, bool, error) {
	return projectMessageInputRequestStateAt(sessionDir, sessionName, now, staleAfterSeconds, nil)
}

type commandApprovalReconciliationIgnoreFunc func(journal.CommandApprovalReplySlotReconciledPayload) bool

func projectMessageInputRequestStateAt(sessionDir, sessionName string, now time.Time, staleAfterSeconds int, ignoreReconciliation commandApprovalReconciliationIgnoreFunc) (MessageInputRequestState, bool, error) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return MessageInputRequestState{}, false, nil
	}

	events, err := journal.Replay(sessionDir)
	if err != nil || len(events) == 0 {
		return MessageInputRequestState{}, false, err
	}

	projected := MessageInputRequestState{
		UnreadCounts:         make(map[string]int),
		InputRequiredCounts:  make(map[string]int),
		WaitingOnInputCounts: make(map[string]int),
		InfoUnreadCounts:     make(map[string]int),
		RequestSatisfaction:  make(map[string]RequestSatisfaction),
	}
	openInboundExact := make(map[string]InputRequestDetail)
	openInboundFallback := make(map[string]InputRequestDetail)
	openOutboundExact := make(map[string]InputRequestDetail)
	openOutboundFallback := make(map[string]InputRequestDetail)
	satisfactionExact := make(map[string]InputRequestDetail)
	satisfactionFallback := make(map[string]InputRequestDetail)
	infoUnread := make(map[string]InputRequestDetail)
	commandApprovalThreads := make(map[string]CommandApprovalThread)
	acceptedCommandApprovalDecisions := make(map[commandApprovalReplySlotKey]string)
	effectiveCommandApprovalDecisions := make(map[string][]journal.Event)
	pendingCommandApprovalReplies := make(map[commandApprovalReplySlotKey]commandApprovalPendingReply)
	pendingCommandApprovalReconciliations := make([]journal.CommandApprovalReplySlotReconciledPayload, 0)
	commandApprovalOpenings := make(map[string]map[string]InputRequestDetail)
	sawLease := false
	sawResolution := false
	sawCompleteMailboxEvent := false

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
		case journal.CommandApprovalRequestedEventType:
			if err := applyCommandApprovalRequest(commandApprovalThreads, event); err != nil {
				return MessageInputRequestState{}, false, err
			}
			continue
		case journal.CommandApprovalDecidedEventType:
			var payload journal.CommandApprovalDecisionPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return MessageInputRequestState{}, false, err
			}
			if err := applyCommandApprovalDecision(commandApprovalThreads, event); err != nil {
				return MessageInputRequestState{}, false, err
			}
			thread := commandApprovalThreads[event.ThreadID]
			preExpiryDecision := commandApprovalDecisionOccurredBeforeExpiry(thread, event.OccurredAt)
			if (thread.Status == CommandApprovalStatusApproved || thread.Status == CommandApprovalStatusRejected) && preExpiryDecision {
				effectiveCommandApprovalDecisions[event.ThreadID] = append(effectiveCommandApprovalDecisions[event.ThreadID], event)
			}
			if key, ok := acceptedCommandApprovalReplySlotKey(event.ThreadID, thread, payload); ok && preExpiryDecision {
				acceptedCommandApprovalDecisions[key] = event.OccurredAt
				reconcilePendingCommandApprovalReplies(projected, openInboundExact, openOutboundExact, satisfactionExact, pendingCommandApprovalReplies, acceptedCommandApprovalDecisions, sessionName)
			}
			continue
		case journal.CommandApprovalReplySlotReconciledEventType:
			var repair journal.CommandApprovalReplySlotReconciledPayload
			if err := json.Unmarshal(event.Payload, &repair); err != nil {
				return MessageInputRequestState{}, false, err
			}
			if ignoreReconciliation != nil && ignoreReconciliation(repair) {
				continue
			}
			pendingCommandApprovalReconciliations = append(pendingCommandApprovalReconciliations, repair)
			continue
		case MailboxProjectionPostConsumedEventType, MailboxProjectionDeliveredEventType, MailboxProjectionReadEventType, MailboxProjectionDeadLetteredEventType:
		default:
			continue
		}

		payload, ok := decodeMailboxEventPayload(event.Payload)
		if !ok {
			continue
		}
		meta := inputRequestMetadataFromPayload(payload)
		if meta.MessageID == "" {
			continue
		}
		if (event.Type == MailboxProjectionPostConsumedEventType || event.Type == MailboxProjectionDeliveredEventType) && payload.Content == "" {
			continue
		}
		sawCompleteMailboxEvent = true
		meta.From = simpleNameForSession(meta.From, sessionName)
		meta.To = simpleNameForSession(meta.To, sessionName)

		switch event.Type {
		case MailboxProjectionPostConsumedEventType:
			rememberCommandApprovalReply(pendingCommandApprovalReplies, meta, event.OccurredAt, sessionName)
			if resolved, commandOpening := resolveCommandApprovalReplySlot(projected, openInboundExact, openOutboundExact, satisfactionExact, meta, event.OccurredAt, acceptedCommandApprovalDecisions, sessionName); !commandOpening {
				// Preserve the pre-command-approval behavior: post-consumed only
				// resolves the inbound slot and satisfaction opening.
				resolveInboundInputRequest(projected, openInboundExact, openInboundFallback, meta)
				fillRequestSatisfaction(projected.RequestSatisfaction, satisfactionExact, satisfactionFallback, meta, event.OccurredAt)
			} else if !resolved {
				continue
			}
			if envelope.ResolveReplyPolicyFromMetadata(meta) == "required" {
				recordCommandApprovalOpening(commandApprovalOpenings, openInputRequest(openOutboundExact, openOutboundFallback, meta, "outbound", event.OccurredAt, event.Type, event.EventID, commandApprovalThreads))
				projected.WaitingOnInputCounts[meta.From]++
				reconcilePendingCommandApprovalReplies(projected, openInboundExact, openOutboundExact, satisfactionExact, pendingCommandApprovalReplies, acceptedCommandApprovalDecisions, sessionName)
			}
		case MailboxProjectionDeliveredEventType:
			rememberCommandApprovalReply(pendingCommandApprovalReplies, meta, event.OccurredAt, sessionName)
			if resolved, commandOpening := resolveCommandApprovalReplySlot(projected, openInboundExact, openOutboundExact, satisfactionExact, meta, event.OccurredAt, acceptedCommandApprovalDecisions, sessionName); !commandOpening {
				// Preserve the pre-command-approval behavior: delivered only
				// resolves the outbound slot and satisfaction opening.
				resolveOutboundInputRequest(projected, openOutboundExact, openOutboundFallback, meta)
				fillRequestSatisfaction(projected.RequestSatisfaction, satisfactionExact, satisfactionFallback, meta, event.OccurredAt)
			} else if !resolved {
				continue
			}
			projected.UnreadCounts[meta.To]++
			if envelope.ResolveReplyPolicyFromMetadata(meta) == "required" {
				recordCommandApprovalOpening(commandApprovalOpenings, openInputRequest(openInboundExact, openInboundFallback, meta, "inbound", event.OccurredAt, event.Type, event.EventID, commandApprovalThreads))
				recordCommandApprovalOpening(commandApprovalOpenings, openRequestSatisfaction(projected.RequestSatisfaction, satisfactionExact, satisfactionFallback, meta, event.OccurredAt, event.Type, event.EventID, commandApprovalThreads))
				projected.InputRequiredCounts[meta.To]++
				if commandApprovalRequestOpensRequesterWaiting(meta) {
					recordCommandApprovalOpening(commandApprovalOpenings, openInputRequest(openOutboundExact, openOutboundFallback, meta, "outbound", event.OccurredAt, event.Type, event.EventID, commandApprovalThreads))
					projected.WaitingOnInputCounts[meta.From]++
				}
				reconcilePendingCommandApprovalReplies(projected, openInboundExact, openOutboundExact, satisfactionExact, pendingCommandApprovalReplies, acceptedCommandApprovalDecisions, sessionName)
			} else {
				infoUnread[inputRequestKey(meta.MessageID, meta.To)] = InputRequestDetail{MessageID: meta.MessageID, Sender: meta.From, Recipient: meta.To}
				projected.InfoUnreadCounts[meta.To]++
			}
		case MailboxProjectionReadEventType:
			decrementCount(projected.UnreadCounts, meta.To)
			markInputRequestRead(openInboundExact, openInboundFallback, meta, event.OccurredAt, event.EventID)
			markInputRequestRead(openOutboundExact, openOutboundFallback, meta, event.OccurredAt, event.EventID)
			if inputRequest, ok := infoUnread[inputRequestKey(meta.MessageID, meta.To)]; ok {
				decrementCount(projected.InfoUnreadCounts, inputRequest.Recipient)
				delete(infoUnread, inputRequestKey(meta.MessageID, meta.To))
			}
		case MailboxProjectionDeadLetteredEventType:
			rememberCommandApprovalReply(pendingCommandApprovalReplies, meta, event.OccurredAt, sessionName)
			// A correlated reply may be denied by ordinary graph routing after its
			// authenticated control-plane effect was recorded. Its exact
			// fills_input_request_id still closes the advertised reply obligation;
			// dead-lettering is a delivery outcome, not a reason to leave the
			// requester or reviewer waiting indefinitely.
			if resolved, commandOpening := resolveCommandApprovalReplySlot(projected, openInboundExact, openOutboundExact, satisfactionExact, meta, event.OccurredAt, acceptedCommandApprovalDecisions, sessionName); commandOpening && !resolved {
				continue
			} else if !commandOpening {
				resolveInboundInputRequest(projected, openInboundExact, openInboundFallback, meta)
				resolveOutboundInputRequest(projected, openOutboundExact, openOutboundFallback, meta)
				fillRequestSatisfaction(projected.RequestSatisfaction, satisfactionExact, satisfactionFallback, meta, event.OccurredAt)
			}
			deadLetterRequestSatisfaction(projected.RequestSatisfaction, satisfactionExact, satisfactionFallback, meta, event.OccurredAt, event.Type, event.EventID)
		}
	}

	if !sawLease || !sawResolution || !sawCompleteMailboxEvent {
		return MessageInputRequestState{}, false, nil
	}
	for _, repair := range pendingCommandApprovalReconciliations {
		decision, ok := validateCommandApprovalReconciliationForProjection(repair, commandApprovalThreads, effectiveCommandApprovalDecisions, commandApprovalOpenings, openInboundExact, openOutboundExact, satisfactionExact)
		if !ok {
			continue
		}
		meta := envelope.Metadata{
			From:                repair.Approver,
			To:                  repair.Requester,
			FillsInputRequestID: repair.InputRequestID,
		}
		resolveInboundInputRequest(projected, openInboundExact, nil, meta)
		resolveOutboundInputRequest(projected, openOutboundExact, nil, meta)
		fillRequestSatisfaction(projected.RequestSatisfaction, satisfactionExact, nil, meta, decision.OccurredAt)
	}
	projected.InputRequired = sortedInputRequestDetails(openInboundExact, openInboundFallback)
	projected.WaitingOnInput = sortedInputRequestDetails(openOutboundExact, openOutboundFallback)
	finalizeRequestSatisfaction(projected.RequestSatisfaction, satisfactionExact, satisfactionFallback, now, staleAfterSeconds)
	return projected, true, nil
}

func commandApprovalDecisionOccurredBeforeExpiry(thread CommandApprovalThread, occurredAt string) bool {
	if thread.ExpiresAt == "" {
		return true
	}
	expiresAt, expiresErr := time.Parse(time.RFC3339Nano, thread.ExpiresAt)
	decidedAt, decidedErr := time.Parse(time.RFC3339Nano, occurredAt)
	return expiresErr == nil && decidedErr == nil && !decidedAt.After(expiresAt)
}

func missingCommandApprovalReplySlotReconciliationField(repair journal.CommandApprovalReplySlotReconciledPayload) string {
	switch {
	case strings.TrimSpace(repair.InputRequestID) == "":
		return "input_request_id is required"
	case strings.TrimSpace(repair.ThreadID) == "":
		return "thread_id is required"
	case strings.TrimSpace(repair.CommandHash) == "":
		return "command_hash is required"
	case strings.TrimSpace(repair.Requester) == "":
		return "requester is required"
	case strings.TrimSpace(repair.RequesterAddress) == "":
		return "requester_address is required"
	case strings.TrimSpace(repair.Approver) == "":
		return "approver is required"
	case strings.TrimSpace(repair.ApproverAddress) == "":
		return "approver_address is required"
	case strings.TrimSpace(repair.DecisionEventID) == "":
		return "decision_event_id is required"
	default:
		return ""
	}
}

func commandApprovalReplySlotReconciliationPayloadEqual(a, b journal.CommandApprovalReplySlotReconciledPayload) bool {
	return a.InputRequestID == b.InputRequestID &&
		a.ThreadID == b.ThreadID &&
		a.CommandHash == b.CommandHash &&
		a.Requester == b.Requester &&
		a.RequesterAddress == b.RequesterAddress &&
		a.Approver == b.Approver &&
		a.ApproverAddress == b.ApproverAddress &&
		a.DecisionEventID == b.DecisionEventID
}

func commandApprovalOpenSlot(state MessageInputRequestState, inputRequestID string) (InputRequestDetail, bool) {
	for _, request := range state.InputRequired {
		if request.InputRequestID == inputRequestID && request.CommandApprovalThreadID != "" {
			return request, true
		}
	}
	for _, request := range state.WaitingOnInput {
		if request.InputRequestID == inputRequestID && request.CommandApprovalThreadID != "" {
			return request, true
		}
	}
	return InputRequestDetail{}, false
}

func validateCommandApprovalReconciliationForProjection(repair journal.CommandApprovalReplySlotReconciledPayload, threads map[string]CommandApprovalThread, decisions map[string][]journal.Event, openings map[string]map[string]InputRequestDetail, openInbound, openOutbound, satisfaction map[string]InputRequestDetail) (journal.Event, bool) {
	if missingCommandApprovalReplySlotReconciliationField(repair) != "" {
		return journal.Event{}, false
	}
	thread, exists := threads[repair.ThreadID]
	effectiveDecisions := decisions[repair.ThreadID]
	if !exists || len(effectiveDecisions) != 1 {
		return journal.Event{}, false
	}
	decision := effectiveDecisions[0]
	if decision.EventID != repair.DecisionEventID || thread.InputRequestID != repair.InputRequestID || thread.CommandHash != repair.CommandHash || thread.Requester != repair.Requester || thread.RequesterAddress != repair.RequesterAddress || thread.CommandApproverNode != repair.Approver || thread.CommandApproverAddress != repair.ApproverAddress {
		return journal.Event{}, false
	}
	if opening, ok := uniqueCommandApprovalOpening(openings, repair.InputRequestID); !ok || !commandApprovalOpeningMatchesRepair(opening, repair) {
		return journal.Event{}, false
	}
	opening, commandOpening := commandApprovalOpeningForFill(repair.InputRequestID, openInbound, openOutbound, satisfaction)
	if !commandOpening || !commandApprovalOpeningMatchesRepair(opening, repair) {
		return journal.Event{}, false
	}
	return decision, true
}

func uniqueCommandApprovalOpening(openings map[string]map[string]InputRequestDetail, inputRequestID string) (InputRequestDetail, bool) {
	distinct := openings[inputRequestID]
	if len(distinct) != 1 {
		return InputRequestDetail{}, false
	}
	for _, opening := range distinct {
		return opening, true
	}
	return InputRequestDetail{}, false
}

func commandApprovalOpeningMatchesRepair(opening InputRequestDetail, repair journal.CommandApprovalReplySlotReconciledPayload) bool {
	return opening.CommandApprovalThreadID == repair.ThreadID &&
		opening.CommandApprovalOpeningThreadID == repair.ThreadID &&
		opening.CommandHash == repair.CommandHash &&
		opening.CommandApprovalOpeningHash == repair.CommandHash &&
		opening.Sender == repair.Requester &&
		opening.Recipient == repair.Approver
}

// resolveCommandApprovalReplySlot is the only dual-direction resolver. It
// derives whether strict validation is necessary from the opening selected by
// fills_input_request_id, never from reply-side thread metadata. Thus a reply
// with a missing, ordinary, or forged thread cannot fall back to generic slot
// resolution for a command-approval opening.
type commandApprovalPendingReply struct {
	meta       envelope.Metadata
	resolvedAt string
}

func rememberCommandApprovalReply(pending map[commandApprovalReplySlotKey]commandApprovalPendingReply, meta envelope.Metadata, resolvedAt, sessionName string) {
	if meta.FillsInputRequestID == "" {
		return
	}
	key := commandApprovalReplySlotKeyFromMetadata(meta, sessionName)
	if key.MessageID == "" || key.InputRequestID == "" {
		return
	}
	if _, seen := pending[key]; !seen {
		pending[key] = commandApprovalPendingReply{meta: meta, resolvedAt: resolvedAt}
	}
}

func reconcilePendingCommandApprovalReplies(state MessageInputRequestState, openInbound, openOutbound, satisfaction map[string]InputRequestDetail, pending map[commandApprovalReplySlotKey]commandApprovalPendingReply, accepted map[commandApprovalReplySlotKey]string, sessionName string) {
	for key, reply := range pending {
		if _, ok := accepted[key]; !ok {
			continue
		}
		if resolved, commandOpening := resolveCommandApprovalReplySlot(state, openInbound, openOutbound, satisfaction, reply.meta, reply.resolvedAt, accepted, sessionName); resolved || commandOpening {
			delete(pending, key)
		}
	}
}

func resolveCommandApprovalReplySlot(state MessageInputRequestState, openInbound, openOutbound, satisfaction map[string]InputRequestDetail, meta envelope.Metadata, resolvedAt string, accepted map[commandApprovalReplySlotKey]string, sessionName string) (resolved, commandOpening bool) {
	if meta.FillsInputRequestID == "" {
		return false, false
	}
	opening, commandOpening := commandApprovalOpeningForFill(meta.FillsInputRequestID, openInbound, openOutbound, satisfaction)
	if !commandOpening {
		return false, false
	}
	key := commandApprovalReplySlotKeyFromMetadata(meta, sessionName)
	decisionAt, isAccepted := accepted[key]
	if opening.CommandApprovalOpeningThreadID != opening.CommandApprovalThreadID || opening.CommandApprovalOpeningHash != opening.CommandHash || meta.ThreadID != opening.CommandApprovalThreadID || meta.CommandHash != opening.CommandHash || nodeaddr.Full(opening.Sender, sessionName) != key.RequesterAddress || nodeaddr.Full(opening.Recipient, sessionName) != key.ReviewerAddress || !isAccepted {
		return false, true
	}
	resolveInboundInputRequest(state, openInbound, nil, meta)
	resolveOutboundInputRequest(state, openOutbound, nil, meta)
	fillRequestSatisfaction(state.RequestSatisfaction, satisfaction, nil, meta, decisionAt)
	return true, true
}

func commandApprovalOpeningForFill(inputRequestID string, openings ...map[string]InputRequestDetail) (InputRequestDetail, bool) {
	for _, open := range openings {
		if opening, ok := open[inputRequestID]; ok && opening.CommandApprovalThreadID != "" {
			return opening, true
		}
	}
	return InputRequestDetail{}, false
}

func commandApprovalRequestOpensRequesterWaiting(meta envelope.Metadata) bool {
	return strings.HasPrefix(meta.ThreadID, "command-approval-") && meta.InputRequestID != ""
}

type commandApprovalReplySlotKey struct {
	MessageID        string
	ThreadID         string
	InputRequestID   string
	CommandHash      string
	ReviewerAddress  string
	RequesterAddress string
}

func acceptedCommandApprovalReplySlotKey(threadID string, thread CommandApprovalThread, payload journal.CommandApprovalDecisionPayload) (commandApprovalReplySlotKey, bool) {
	if payload.MessageID != "" && thread.DecisionMessageID != payload.MessageID {
		return commandApprovalReplySlotKey{}, false
	}
	if thread.Status != CommandApprovalStatusApproved && thread.Status != CommandApprovalStatusRejected {
		return commandApprovalReplySlotKey{}, false
	}
	if thread.CommandApproverAddress == "" || thread.RequesterAddress == "" {
		return commandApprovalReplySlotKey{}, false
	}
	if payload.ReviewerAddress != thread.CommandApproverAddress || payload.RequesterAddress != thread.RequesterAddress {
		return commandApprovalReplySlotKey{}, false
	}
	if thread.InputRequestID == "" || payload.InputRequestID != thread.InputRequestID {
		return commandApprovalReplySlotKey{}, false
	}
	if thread.CommandHash == "" || payload.CommandHash != thread.CommandHash {
		return commandApprovalReplySlotKey{}, false
	}
	return commandApprovalReplySlotKey{
		MessageID:        payload.MessageID,
		ThreadID:         threadID,
		InputRequestID:   payload.InputRequestID,
		CommandHash:      payload.CommandHash,
		ReviewerAddress:  payload.ReviewerAddress,
		RequesterAddress: payload.RequesterAddress,
	}, true
}

func commandApprovalReplySlotKeyFromMetadata(meta envelope.Metadata, sessionName string) commandApprovalReplySlotKey {
	return commandApprovalReplySlotKey{
		MessageID:        meta.MessageID,
		ThreadID:         meta.ThreadID,
		InputRequestID:   meta.FillsInputRequestID,
		CommandHash:      meta.CommandHash,
		ReviewerAddress:  nodeaddr.Full(meta.From, sessionName),
		RequesterAddress: nodeaddr.Full(meta.To, sessionName),
	}
}

func inputRequestMetadataFromPayload(payload journal.MailboxEventPayload) envelope.Metadata {
	meta, err := envelope.ParseMetadata(payload.Content)
	if err != nil {
		meta = envelope.Metadata{Body: envelope.BodyFromContent(payload.Content)}
	}
	if meta.MessageID == "" {
		meta.MessageID = payload.MessageID
	}
	if meta.ContextID == "" {
		meta.ContextID = payload.ContextID
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
	if meta.MessageType == "" {
		meta.MessageType = payload.MessageType
	}
	if meta.Timestamp == "" {
		meta.Timestamp = payload.Timestamp
	}
	if meta.InputRequestID == "" {
		meta.InputRequestID = payload.InputRequestID
	}
	if meta.FillsInputRequestID == "" {
		meta.FillsInputRequestID = payload.FillsInputRequestID
	}
	if meta.InputRequestSetID == "" {
		meta.InputRequestSetID = payload.InputRequestSetID
	}
	if meta.BranchID == "" {
		meta.BranchID = payload.BranchID
	}
	if meta.CompletionRule == "" {
		meta.CompletionRule = payload.CompletionRule
	}
	return meta
}

func openInputRequest(openExact, openFallback map[string]InputRequestDetail, meta envelope.Metadata, direction, openedAt, openedAtSource, openedEventID string, commandApprovalThreads map[string]CommandApprovalThread) InputRequestDetail {
	inputRequest := newInputRequestDetail(meta, direction, openedAt, openedAtSource, openedEventID, commandApprovalThreads)
	if meta.InputRequestID != "" {
		openExact[meta.InputRequestID] = inputRequest
		return inputRequest
	}
	openFallback[inputRequestKey(meta.MessageID, meta.To)] = inputRequest
	return inputRequest
}

func openRequestSatisfaction(satisfaction map[string]RequestSatisfaction, openExact, openFallback map[string]InputRequestDetail, meta envelope.Metadata, openedAt, openedAtSource, openedEventID string, commandApprovalThreads map[string]CommandApprovalThread) InputRequestDetail {
	inputRequest := newInputRequestDetail(meta, "", openedAt, openedAtSource, openedEventID, commandApprovalThreads)
	stats := satisfaction[meta.To]
	stats.OpenedCount++
	satisfaction[meta.To] = stats
	if meta.InputRequestID != "" {
		openExact[meta.InputRequestID] = inputRequest
		return inputRequest
	}
	openFallback[inputRequestKey(meta.MessageID, meta.To)] = inputRequest
	return inputRequest
}

func newInputRequestDetail(meta envelope.Metadata, direction, openedAt, openedAtSource, openedEventID string, commandApprovalThreads map[string]CommandApprovalThread) InputRequestDetail {
	return InputRequestDetail{
		Direction:                      direction,
		MessageID:                      meta.MessageID,
		InputRequestID:                 meta.InputRequestID,
		Sender:                         meta.From,
		Recipient:                      meta.To,
		ReplyPolicy:                    envelope.ResolveReplyPolicyFromMetadata(meta),
		OpenedAt:                       openedAt,
		OpenedAtSource:                 openedAtSource,
		OpenedEventID:                  openedEventID,
		CommandApprovalThreadID:        commandApprovalOpeningThreadID(meta, commandApprovalThreads),
		CommandHash:                    commandApprovalOpeningHash(meta, commandApprovalThreads),
		CommandApprovalOpeningThreadID: meta.ThreadID,
		CommandApprovalOpeningHash:     meta.CommandHash,
	}
}

func recordCommandApprovalOpening(openings map[string]map[string]InputRequestDetail, opening InputRequestDetail) {
	if opening.InputRequestID == "" || opening.CommandApprovalThreadID == "" {
		return
	}
	if openings[opening.InputRequestID] == nil {
		openings[opening.InputRequestID] = make(map[string]InputRequestDetail)
	}
	openings[opening.InputRequestID][commandApprovalOpeningTupleKey(opening)] = opening
}

func commandApprovalOpeningTupleKey(opening InputRequestDetail) string {
	return strings.Join([]string{
		opening.InputRequestID,
		opening.MessageID,
		opening.Sender,
		opening.Recipient,
		opening.CommandApprovalThreadID,
		opening.CommandHash,
		opening.CommandApprovalOpeningThreadID,
		opening.CommandApprovalOpeningHash,
	}, "\x00")
}

func commandApprovalOpeningThreadID(meta envelope.Metadata, threads map[string]CommandApprovalThread) string {
	if meta.InputRequestID == "" {
		return ""
	}
	for threadID, thread := range threads {
		if thread.InputRequestID == meta.InputRequestID {
			return threadID
		}
	}
	if strings.HasPrefix(meta.ThreadID, "command-approval-") {
		return meta.ThreadID
	}
	return ""
}

func commandApprovalOpeningHash(meta envelope.Metadata, threads map[string]CommandApprovalThread) string {
	if threadID := commandApprovalOpeningThreadID(meta, threads); threadID != "" {
		if thread, ok := threads[threadID]; ok {
			return thread.CommandHash
		}
	}
	return meta.CommandHash
}

func fillRequestSatisfaction(satisfaction map[string]RequestSatisfaction, openExact, openFallback map[string]InputRequestDetail, meta envelope.Metadata, filledAt string) {
	inputRequest, ok := resolveRequestSatisfactionOpen(openExact, openFallback, meta)
	if !ok {
		return
	}
	stats := satisfaction[inputRequest.Recipient]
	stats.FilledCount++
	if seconds, ok := requestSatisfactionElapsedSeconds(inputRequest.OpenedAt, filledAt); ok {
		stats.TotalTimeToFillSeconds += seconds
	}
	satisfaction[inputRequest.Recipient] = stats
}

func deadLetterRequestSatisfaction(satisfaction map[string]RequestSatisfaction, openExact, openFallback map[string]InputRequestDetail, meta envelope.Metadata, deadLetteredAt, openedAtSource, openedEventID string) {
	if envelope.ResolveReplyPolicyFromMetadata(meta) != "required" {
		return
	}
	inputRequest, ok := findOpenRequestSatisfaction(openExact, openFallback, meta)
	if !ok {
		openRequestSatisfaction(satisfaction, openExact, openFallback, meta, deadLetteredAt, openedAtSource, openedEventID, nil)
		inputRequest, ok = findOpenRequestSatisfaction(openExact, openFallback, meta)
		if !ok {
			return
		}
	}
	stats := satisfaction[inputRequest.Recipient]
	stats.DeadLetteredCount++
	satisfaction[inputRequest.Recipient] = stats
}

func findOpenRequestSatisfaction(openExact, openFallback map[string]InputRequestDetail, meta envelope.Metadata) (InputRequestDetail, bool) {
	if meta.InputRequestID != "" {
		if inputRequest, ok := openExact[meta.InputRequestID]; ok {
			return inputRequest, true
		}
	}
	key := inputRequestKey(meta.MessageID, meta.To)
	inputRequest, ok := openFallback[key]
	return inputRequest, ok
}

func resolveRequestSatisfactionOpen(openExact, openFallback map[string]InputRequestDetail, meta envelope.Metadata) (InputRequestDetail, bool) {
	if meta.FillsInputRequestID != "" {
		key, inputRequest, ok := findExactInputRequest(openExact, meta.FillsInputRequestID, meta.ReplyTo, meta.From)
		if !ok {
			return InputRequestDetail{}, false
		}
		delete(openExact, key)
		return inputRequest, true
	}
	if meta.ReplyTo == "" {
		return InputRequestDetail{}, false
	}
	key, inputRequest, ok := findFallbackInputRequest(openFallback, meta.ReplyTo, meta.From)
	if !ok {
		return InputRequestDetail{}, false
	}
	delete(openFallback, key)
	return inputRequest, true
}

func finalizeRequestSatisfaction(satisfaction map[string]RequestSatisfaction, openExact, openFallback map[string]InputRequestDetail, now time.Time, staleAfterSeconds int) {
	if staleAfterSeconds <= 0 {
		staleAfterSeconds = DefaultInputRequestStaleAfterSeconds
	}
	for node, stats := range satisfaction {
		stats.StaleAfterSeconds = staleAfterSeconds
		if stats.FilledCount > 0 {
			stats.AverageTimeToFillSeconds = stats.TotalTimeToFillSeconds / stats.FilledCount
		}
		satisfaction[node] = stats
	}
	for _, inputRequest := range openExact {
		addOpenRequestSatisfaction(satisfaction, inputRequest, now, staleAfterSeconds)
	}
	for _, inputRequest := range openFallback {
		addOpenRequestSatisfaction(satisfaction, inputRequest, now, staleAfterSeconds)
	}
}

func addOpenRequestSatisfaction(satisfaction map[string]RequestSatisfaction, inputRequest InputRequestDetail, now time.Time, staleAfterSeconds int) {
	stats := satisfaction[inputRequest.Recipient]
	stats.StaleAfterSeconds = staleAfterSeconds
	stats.OpenCount++
	if age, ok := requestSatisfactionAgeSeconds(inputRequest.OpenedAt, now); ok {
		if age > stats.LongestOpenAgeSeconds {
			stats.LongestOpenAgeSeconds = age
		}
		if age >= staleAfterSeconds {
			stats.StaleOpenCount++
		}
	}
	satisfaction[inputRequest.Recipient] = stats
}

func requestSatisfactionElapsedSeconds(openedAt, filledAt string) (int, bool) {
	opened, err := time.Parse(time.RFC3339Nano, openedAt)
	if err != nil {
		return 0, false
	}
	filled, err := time.Parse(time.RFC3339Nano, filledAt)
	if err != nil {
		return 0, false
	}
	if filled.Before(opened) {
		return 0, false
	}
	return int(filled.Sub(opened).Seconds()), true
}

func requestSatisfactionAgeSeconds(openedAt string, now time.Time) (int, bool) {
	opened, err := time.Parse(time.RFC3339Nano, openedAt)
	if err != nil || now.Before(opened) {
		return 0, false
	}
	return int(now.Sub(opened).Seconds()), true
}

func resolveInboundInputRequest(state MessageInputRequestState, openExact, openFallback map[string]InputRequestDetail, meta envelope.Metadata) {
	if meta.FillsInputRequestID != "" {
		key, inputRequest, ok := findExactInputRequest(openExact, meta.FillsInputRequestID, meta.ReplyTo, meta.From)
		if !ok {
			return
		}
		decrementCount(state.InputRequiredCounts, inputRequest.Recipient)
		delete(openExact, key)
		return
	}
	if meta.ReplyTo == "" {
		return
	}
	key, inputRequest, ok := findFallbackInputRequest(openFallback, meta.ReplyTo, meta.From)
	if !ok {
		return
	}
	decrementCount(state.InputRequiredCounts, inputRequest.Recipient)
	delete(openFallback, key)
}

func resolveOutboundInputRequest(state MessageInputRequestState, openExact, openFallback map[string]InputRequestDetail, meta envelope.Metadata) {
	if meta.FillsInputRequestID != "" {
		key, inputRequest, ok := findExactInputRequest(openExact, meta.FillsInputRequestID, meta.ReplyTo, meta.From)
		if !ok {
			return
		}
		decrementCount(state.WaitingOnInputCounts, inputRequest.Sender)
		delete(openExact, key)
		return
	}
	if meta.ReplyTo == "" {
		return
	}
	key, inputRequest, ok := findFallbackInputRequest(openFallback, meta.ReplyTo, meta.From)
	if !ok {
		return
	}
	decrementCount(state.WaitingOnInputCounts, inputRequest.Sender)
	delete(openFallback, key)
}

func markInputRequestRead(openExact, openFallback map[string]InputRequestDetail, meta envelope.Metadata, readAt, readEventID string) {
	if readAt == "" {
		return
	}
	if meta.InputRequestID != "" {
		if inputRequest, ok := openExact[meta.InputRequestID]; ok {
			inputRequest.ReadAt = readAt
			inputRequest.ReadEventID = readEventID
			openExact[meta.InputRequestID] = inputRequest
			return
		}
	}
	for key, inputRequest := range openExact {
		if inputRequest.MessageID == meta.MessageID && inputRequest.Recipient == meta.To {
			inputRequest.ReadAt = readAt
			inputRequest.ReadEventID = readEventID
			openExact[key] = inputRequest
			return
		}
	}
	key := inputRequestKey(meta.MessageID, meta.To)
	if inputRequest, ok := openFallback[key]; ok {
		inputRequest.ReadAt = readAt
		inputRequest.ReadEventID = readEventID
		openFallback[key] = inputRequest
	}
}

func findExactInputRequest(open map[string]InputRequestDetail, inputRequestID, replyTo, participant string) (string, InputRequestDetail, bool) {
	inputRequest, ok := open[inputRequestID]
	if !ok {
		return "", InputRequestDetail{}, false
	}
	if inputRequest.Recipient != participant {
		return "", InputRequestDetail{}, false
	}
	if replyTo != "" && replyTo != inputRequest.MessageID {
		return "", InputRequestDetail{}, false
	}
	return inputRequestID, inputRequest, true
}

func findFallbackInputRequest(open map[string]InputRequestDetail, messageID, participant string) (string, InputRequestDetail, bool) {
	key := inputRequestKey(messageID, participant)
	inputRequest, ok := open[key]
	return key, inputRequest, ok
}

func sortedInputRequestDetails(exact, fallback map[string]InputRequestDetail) []InputRequestDetail {
	if len(exact) == 0 && len(fallback) == 0 {
		return nil
	}
	result := make([]InputRequestDetail, 0, len(exact)+len(fallback))
	for _, inputRequest := range exact {
		result = append(result, inputRequest)
	}
	for _, inputRequest := range fallback {
		result = append(result, inputRequest)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].OpenedAt != result[j].OpenedAt {
			return result[i].OpenedAt < result[j].OpenedAt
		}
		if result[i].MessageID != result[j].MessageID {
			return result[i].MessageID < result[j].MessageID
		}
		if result[i].InputRequestID != result[j].InputRequestID {
			return result[i].InputRequestID < result[j].InputRequestID
		}
		if result[i].Sender != result[j].Sender {
			return result[i].Sender < result[j].Sender
		}
		return result[i].Recipient < result[j].Recipient
	})
	return result
}

func simpleNameForSession(name, sessionName string) string {
	return SimpleNameForSession(name, sessionName)
}

func SimpleNameForSession(name, sessionName string) string {
	fullName := nodeaddr.Full(name, sessionName)
	recipientSession, recipientName, hasSession := nodeaddr.Split(fullName)
	if hasSession && recipientSession == sessionName {
		return recipientName
	}
	return name
}

func decrementCount(counts map[string]int, key string) {
	if counts[key] <= 0 {
		return
	}
	counts[key]--
}

func inputRequestKey(messageID, nodeName string) string {
	return messageID + "\x00" + nodeName
}
