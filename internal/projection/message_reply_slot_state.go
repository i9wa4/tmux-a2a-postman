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
	commandApprovalUnresolved      bool
	messageIDs                     map[string]struct{}
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
	repairEvidence := newCommandApprovalRepairEvidence()
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
			repairEvidence.recordRequest(commandApprovalThreads, event)
		case journal.CommandApprovalDecidedEventType:
			if err := applyCommandApprovalDecision(commandApprovalThreads, event); err != nil {
				return CommandApprovalReplySlotReconciliationPlan{}, false, err
			}
			// Reply-slot closure is independent of execution effectiveness: a
			// correctly bound terminal decision closes the reply obligation even if
			// it arrived after expiry (execution stays governed by expiry/claim).
			// Evidence is kept per request generation, not per thread.
			if err := repairEvidence.recordDecision(event); err != nil {
				return CommandApprovalReplySlotReconciliationPlan{}, false, err
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
	thread, exists := repairEvidence.snapshot(repair.ThreadID, repair.InputRequestID)
	if !exists {
		plan.Reason = "command approval request not found"
		return plan, true, nil
	}
	decision, ok := repairEvidence.effectiveDecision(repair.ThreadID, repair.InputRequestID)
	if !ok {
		plan.Reason = "expected exactly one effective terminal command approval decision (identical bound duplicates count once; missing or conflicting bound decisions do not)"
		return plan, true, nil
	}
	plan.DecisionAt = decision.OccurredAt
	if decision.EventID != repair.DecisionEventID || thread.CommandHash != repair.CommandHash || thread.Requester != repair.Requester || thread.RequesterAddress != repair.RequesterAddress || thread.CommandApproverNode != repair.Approver || thread.CommandApproverAddress != repair.ApproverAddress {
		plan.Reason = "reconciliation tuple does not match recorded request and decision"
		return plan, true, nil
	}
	if opening, ok := commandApprovalOpeningForRepair(commandApprovalOpenings, repair); !ok {
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
	opening, ok := commandApprovalOpenSlot(inputState, repair)
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
	seenInbound := make(map[string]struct{})
	seenOutbound := make(map[string]struct{})
	seenSatisfaction := make(map[string]struct{})
	infoUnread := make(map[string]int)
	outstandingDeliveries := make(map[string]int)
	commandApprovalThreads := make(map[string]CommandApprovalThread)
	acceptedCommandApprovalDecisions := make(map[commandApprovalReplySlotKey]string)
	repairEvidence := newCommandApprovalRepairEvidence()
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
			repairEvidence.recordRequest(commandApprovalThreads, event)
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
			if err := repairEvidence.recordDecision(event); err != nil {
				return MessageInputRequestState{}, false, err
			}
			if key, ok := acceptedCommandApprovalReplySlotKey(event.ThreadID, thread, payload); ok {
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
			if envelope.ResolveReplyPolicyFromMetadata(meta) == "required" {
				recordCommandApprovalOpening(commandApprovalOpenings, newInputRequestDetail(meta, "", event.OccurredAt, event.Type, event.EventID, commandApprovalThreads))
			}
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
				if _, opened := openInputRequest(openOutboundExact, openOutboundFallback, seenOutbound, meta, "outbound", event.OccurredAt, event.Type, event.EventID, commandApprovalThreads); opened {
					projected.WaitingOnInputCounts[meta.From]++
				}
				reconcilePendingCommandApprovalReplies(projected, openInboundExact, openOutboundExact, satisfactionExact, pendingCommandApprovalReplies, acceptedCommandApprovalDecisions, sessionName)
			}
		case MailboxProjectionDeliveredEventType:
			if envelope.ResolveReplyPolicyFromMetadata(meta) == "required" {
				recordCommandApprovalOpening(commandApprovalOpenings, newInputRequestDetail(meta, "", event.OccurredAt, event.Type, event.EventID, commandApprovalThreads))
			}
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
			outstandingDeliveries[inputRequestKey(meta.MessageID, meta.To)]++
			if envelope.ResolveReplyPolicyFromMetadata(meta) == "required" {
				if _, opened := openInputRequest(openInboundExact, openInboundFallback, seenInbound, meta, "inbound", event.OccurredAt, event.Type, event.EventID, commandApprovalThreads); opened {
					projected.InputRequiredCounts[meta.To]++
				}
				openRequestSatisfaction(projected.RequestSatisfaction, satisfactionExact, satisfactionFallback, seenSatisfaction, meta, event.OccurredAt, event.Type, event.EventID, commandApprovalThreads)
				rememberInputRequestMessageAlias(openOutboundExact, openOutboundFallback, meta, commandApprovalThreads)
				if commandApprovalRequestOpensRequesterWaiting(meta) {
					if _, opened := openInputRequest(openOutboundExact, openOutboundFallback, seenOutbound, meta, "outbound", event.OccurredAt, event.Type, event.EventID, commandApprovalThreads); opened {
						projected.WaitingOnInputCounts[meta.From]++
					}
				}
				reconcilePendingCommandApprovalReplies(projected, openInboundExact, openOutboundExact, satisfactionExact, pendingCommandApprovalReplies, acceptedCommandApprovalDecisions, sessionName)
			} else {
				infoUnread[inputRequestKey(meta.MessageID, meta.To)]++
				projected.InfoUnreadCounts[meta.To]++
			}
		case MailboxProjectionReadEventType:
			deliveryKey := inputRequestKey(meta.MessageID, meta.To)
			if outstandingDeliveries[deliveryKey] > 0 {
				decrementCount(projected.UnreadCounts, meta.To)
				outstandingDeliveries[deliveryKey]--
			}
			markInputRequestRead(openInboundExact, openInboundFallback, meta, event.OccurredAt, event.EventID)
			markInputRequestRead(openOutboundExact, openOutboundFallback, meta, event.OccurredAt, event.EventID)
			if infoUnread[deliveryKey] > 0 {
				decrementCount(projected.InfoUnreadCounts, meta.To)
				infoUnread[deliveryKey]--
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
			deadLetterRequestSatisfaction(projected.RequestSatisfaction, satisfactionExact, satisfactionFallback, seenSatisfaction, meta, event.OccurredAt, event.Type, event.EventID)
		}
	}

	if !sawLease || !sawResolution || !sawCompleteMailboxEvent {
		return MessageInputRequestState{}, false, nil
	}
	for _, repair := range pendingCommandApprovalReconciliations {
		decision, ok := validateCommandApprovalReconciliationForProjection(repair, repairEvidence, commandApprovalOpenings, openInboundExact, openOutboundExact, satisfactionExact)
		if !ok {
			continue
		}
		meta := envelope.Metadata{
			From:                repair.Approver,
			To:                  repair.Requester,
			ThreadID:            repair.ThreadID,
			CommandHash:         repair.CommandHash,
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

// commandApprovalGenerationKey identifies one command-approval request
// generation. Retrying an expired or rejected command mints a fresh request
// (new input_request_id) on the same deterministic thread, so the thread id
// alone cannot identify the request a repair or decision belongs to.
type commandApprovalGenerationKey struct {
	ThreadID       string
	InputRequestID string
}

type commandApprovalBoundDecision struct {
	event    journal.Event
	decision journal.ApprovalDecision
}

// commandApprovalRepairEvidence collects, per request generation, the request
// snapshot recorded by that generation's own request event and every decision
// event that is correctly bound to it. Reconciliation is validated against this
// evidence rather than the latest thread entry, so a later retry on the same
// thread neither invalidates an earlier generation's repair nor lends its own
// decisions to it.
type commandApprovalRepairEvidence struct {
	snapshots map[commandApprovalGenerationKey]CommandApprovalThread
	decisions map[commandApprovalGenerationKey][]commandApprovalBoundDecision
}

func newCommandApprovalRepairEvidence() *commandApprovalRepairEvidence {
	return &commandApprovalRepairEvidence{
		snapshots: make(map[commandApprovalGenerationKey]CommandApprovalThread),
		decisions: make(map[commandApprovalGenerationKey][]commandApprovalBoundDecision),
	}
}

// recordRequest snapshots the generation a request event just created. It must
// run right after applyCommandApprovalRequest for the same event.
func (e *commandApprovalRepairEvidence) recordRequest(threads map[string]CommandApprovalThread, event journal.Event) {
	thread, ok := threads[event.ThreadID]
	if !ok || thread.InputRequestID == "" {
		return
	}
	e.snapshots[commandApprovalGenerationKey{ThreadID: event.ThreadID, InputRequestID: thread.InputRequestID}] = thread
}

// recordDecision keeps a decision only when it is correctly bound to the
// recorded generation: same thread, input_request_id and command hash, and the
// recorded approver principal. Unbound or wrong-principal events are ignored.
func (e *commandApprovalRepairEvidence) recordDecision(event journal.Event) error {
	var payload journal.CommandApprovalDecisionPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	if payload.InputRequestID == "" {
		return nil
	}
	snapshot, ok := e.snapshots[commandApprovalGenerationKey{ThreadID: event.ThreadID, InputRequestID: payload.InputRequestID}]
	if !ok || snapshot.CommandHash == "" || payload.CommandHash != snapshot.CommandHash {
		return nil
	}
	if matches, _ := commandApprovalDecisionMatchesThreadIdentity(snapshot, payload); !matches {
		return nil
	}
	if payload.Decision != journal.ApprovalDecisionApproved && payload.Decision != journal.ApprovalDecisionRejected {
		return nil
	}
	key := commandApprovalGenerationKey{ThreadID: event.ThreadID, InputRequestID: payload.InputRequestID}
	e.decisions[key] = append(e.decisions[key], commandApprovalBoundDecision{event: event, decision: payload.Decision})
	return nil
}

func (e *commandApprovalRepairEvidence) snapshot(threadID, inputRequestID string) (CommandApprovalThread, bool) {
	snapshot, ok := e.snapshots[commandApprovalGenerationKey{ThreadID: threadID, InputRequestID: inputRequestID}]
	return snapshot, ok
}

// effectiveDecision returns the first bound decision of the generation. Bound
// duplicates with the same decision value are one decision (a re-sent late
// APPROVED must not block repair); a bound decision with a different value, or
// no bound decision at all, fails closed.
func (e *commandApprovalRepairEvidence) effectiveDecision(threadID, inputRequestID string) (journal.Event, bool) {
	bound := e.decisions[commandApprovalGenerationKey{ThreadID: threadID, InputRequestID: inputRequestID}]
	if len(bound) == 0 {
		return journal.Event{}, false
	}
	for _, candidate := range bound[1:] {
		if candidate.decision != bound[0].decision {
			return journal.Event{}, false
		}
	}
	return bound[0].event, true
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

func commandApprovalOpenSlot(state MessageInputRequestState, repair journal.CommandApprovalReplySlotReconciledPayload) (InputRequestDetail, bool) {
	for _, request := range state.InputRequired {
		if request.InputRequestID == repair.InputRequestID && commandApprovalOpeningMatchesRepair(request, repair) {
			return request, true
		}
	}
	for _, request := range state.WaitingOnInput {
		if request.InputRequestID == repair.InputRequestID && commandApprovalOpeningMatchesRepair(request, repair) {
			return request, true
		}
	}
	return InputRequestDetail{}, false
}

func validateCommandApprovalReconciliationForProjection(repair journal.CommandApprovalReplySlotReconciledPayload, evidence *commandApprovalRepairEvidence, openings map[string]map[string]InputRequestDetail, openInbound, openOutbound, satisfaction map[string]InputRequestDetail) (journal.Event, bool) {
	if missingCommandApprovalReplySlotReconciliationField(repair) != "" {
		return journal.Event{}, false
	}
	// Same generation-bound rule as ValidateCommandApprovalReplySlotReconciliation,
	// so a repair that validated at apply time never drops on replay.
	thread, exists := evidence.snapshot(repair.ThreadID, repair.InputRequestID)
	if !exists {
		return journal.Event{}, false
	}
	decision, ok := evidence.effectiveDecision(repair.ThreadID, repair.InputRequestID)
	if !ok {
		return journal.Event{}, false
	}
	if decision.EventID != repair.DecisionEventID || thread.CommandHash != repair.CommandHash || thread.Requester != repair.Requester || thread.RequesterAddress != repair.RequesterAddress || thread.CommandApproverNode != repair.Approver || thread.CommandApproverAddress != repair.ApproverAddress {
		return journal.Event{}, false
	}
	if opening, ok := commandApprovalOpeningForRepair(openings, repair); !ok || !commandApprovalOpeningMatchesRepair(opening, repair) {
		return journal.Event{}, false
	}
	opening, commandOpening := commandApprovalOpeningForFill(repair.InputRequestID, repair.Requester, repair.Approver, repair.ThreadID, repair.CommandHash, openInbound, openOutbound, satisfaction)
	if !commandOpening || !commandApprovalOpeningMatchesRepair(opening, repair) {
		return journal.Event{}, false
	}
	return decision, true
}

func commandApprovalOpeningForRepair(openings map[string]map[string]InputRequestDetail, repair journal.CommandApprovalReplySlotReconciledPayload) (InputRequestDetail, bool) {
	var matching InputRequestDetail
	for _, opening := range openings[repair.InputRequestID] {
		if opening.Sender != repair.Requester || opening.Recipient != repair.Approver || opening.CommandApprovalThreadID != repair.ThreadID {
			continue
		}
		// A second opening on the same thread may carry a conflicting raw
		// hash or thread. Even if one matches the repair, the evidence for
		// that thread is ambiguous and must not authorize reconciliation.
		if matching.InputRequestID != "" {
			return InputRequestDetail{}, false
		}
		matching = opening
	}
	return matching, matching.InputRequestID != ""
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
	opening, commandOpening := commandApprovalOpeningForFill(meta.FillsInputRequestID, meta.To, meta.From, meta.ThreadID, meta.CommandHash, openInbound, openOutbound, satisfaction)
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

func commandApprovalOpeningForFill(inputRequestID, requester, approver, threadID, commandHash string, openings ...map[string]InputRequestDetail) (InputRequestDetail, bool) {
	var matching InputRequestDetail
	sawPair := false
	ordinaryMatch := false
	for _, open := range openings {
		for _, opening := range open {
			if opening.InputRequestID != inputRequestID || opening.Sender != requester || opening.Recipient != approver {
				continue
			}
			if opening.CommandApprovalThreadID == "" {
				if opening.commandApprovalUnresolved {
					sawPair = true
					continue
				}
				if commandHash == "" && !strings.HasPrefix(threadID, "command-approval-") && opening.CommandApprovalOpeningThreadID == threadID {
					ordinaryMatch = true
				}
				continue
			}
			sawPair = true
			if threadID != "" && opening.CommandApprovalThreadID != threadID {
				continue
			}
			if commandHash != "" && opening.CommandHash != commandHash {
				continue
			}
			if opening.CommandApprovalOpeningThreadID != "" && opening.CommandApprovalOpeningThreadID != opening.CommandApprovalThreadID {
				return InputRequestDetail{}, true
			}
			if matching.InputRequestID != "" && commandApprovalLogicalTupleKey(matching) != commandApprovalLogicalTupleKey(opening) {
				return InputRequestDetail{}, true
			}
			if matching.InputRequestID == "" || opening.CommandApprovalOpeningThreadID != "" {
				matching = opening
			}
		}
	}
	if ordinaryMatch {
		return InputRequestDetail{}, false
	}
	return matching, sawPair
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

func openInputRequest(openExact, openFallback map[string]InputRequestDetail, seen map[string]struct{}, meta envelope.Metadata, direction, openedAt, openedAtSource, openedEventID string, commandApprovalThreads map[string]CommandApprovalThread) (InputRequestDetail, bool) {
	inputRequest := newInputRequestDetail(meta, direction, openedAt, openedAtSource, openedEventID, commandApprovalThreads)
	key := logicalInputRequestDetailKey(inputRequest)
	open := openFallback
	if meta.InputRequestID != "" {
		open = openExact
	}
	if _, exists := seen[key]; exists {
		rememberInputRequestMessageAlias(openExact, openFallback, meta, commandApprovalThreads)
		return inputRequest, false
	}
	seen[key] = struct{}{}
	inputRequest.messageIDs = map[string]struct{}{meta.MessageID: {}}
	open[key] = inputRequest
	return inputRequest, true
}

func rememberInputRequestMessageAlias(openExact, openFallback map[string]InputRequestDetail, meta envelope.Metadata, commandApprovalThreads map[string]CommandApprovalThread) {
	key := logicalInputRequestDetailKey(newInputRequestDetail(meta, "", "", "", "", commandApprovalThreads))
	open := openFallback
	if meta.InputRequestID != "" {
		open = openExact
	}
	if existing, ok := open[key]; ok {
		if existing.messageIDs == nil {
			existing.messageIDs = map[string]struct{}{existing.MessageID: {}}
		}
		existing.messageIDs[meta.MessageID] = struct{}{}
		open[key] = existing
	}
}

func openRequestSatisfaction(satisfaction map[string]RequestSatisfaction, openExact, openFallback map[string]InputRequestDetail, seen map[string]struct{}, meta envelope.Metadata, openedAt, openedAtSource, openedEventID string, commandApprovalThreads map[string]CommandApprovalThread) (InputRequestDetail, bool) {
	inputRequest, opened := openInputRequest(openExact, openFallback, seen, meta, "", openedAt, openedAtSource, openedEventID, commandApprovalThreads)
	if !opened {
		return inputRequest, false
	}
	stats := satisfaction[meta.To]
	stats.OpenedCount++
	satisfaction[meta.To] = stats
	return inputRequest, true
}

func logicalInputRequestKey(meta envelope.Metadata) string {
	if meta.InputRequestID == "" {
		return inputRequestKey(meta.MessageID, meta.To)
	}
	return strings.Join([]string{"ordinary", meta.ThreadID, meta.InputRequestID, meta.From, meta.To}, "\x00")
}

func logicalInputRequestDetailKey(detail InputRequestDetail) string {
	if detail.InputRequestID == "" {
		return inputRequestKey(detail.MessageID, detail.Recipient)
	}
	if detail.CommandApprovalThreadID != "" {
		return strings.Join([]string{"command", detail.CommandApprovalThreadID, detail.InputRequestID, detail.Sender, detail.Recipient}, "\x00")
	}
	if detail.commandApprovalUnresolved {
		return strings.Join([]string{"command-unresolved", detail.MessageID, detail.InputRequestID, detail.Sender, detail.Recipient}, "\x00")
	}
	return strings.Join([]string{"ordinary", detail.CommandApprovalOpeningThreadID, detail.InputRequestID, detail.Sender, detail.Recipient}, "\x00")
}

func newInputRequestDetail(meta envelope.Metadata, direction, openedAt, openedAtSource, openedEventID string, commandApprovalThreads map[string]CommandApprovalThread) InputRequestDetail {
	commandThreadID := commandApprovalOpeningThreadID(meta, commandApprovalThreads)
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
		CommandApprovalThreadID:        commandThreadID,
		CommandHash:                    commandApprovalOpeningHash(meta, commandApprovalThreads),
		CommandApprovalOpeningThreadID: meta.ThreadID,
		CommandApprovalOpeningHash:     meta.CommandHash,
		commandApprovalUnresolved:      meta.InputRequestID != "" && meta.ThreadID == "" && meta.CommandHash != "" && commandThreadID == "",
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
		opening.Sender,
		opening.Recipient,
		opening.CommandApprovalThreadID,
		opening.CommandHash,
		opening.CommandApprovalOpeningThreadID,
		opening.CommandApprovalOpeningHash,
	}, "\x00")
}

func commandApprovalLogicalTupleKey(opening InputRequestDetail) string {
	return strings.Join([]string{
		opening.InputRequestID,
		opening.Sender,
		opening.Recipient,
		opening.CommandApprovalThreadID,
		opening.CommandHash,
	}, "\x00")
}

func commandApprovalOpeningThreadID(meta envelope.Metadata, threads map[string]CommandApprovalThread) string {
	if meta.InputRequestID == "" {
		return ""
	}
	if thread, ok := threads[meta.ThreadID]; ok && thread.InputRequestID == meta.InputRequestID {
		return meta.ThreadID
	}
	if strings.HasPrefix(meta.ThreadID, "command-approval-") {
		return meta.ThreadID
	}
	if meta.ThreadID == "" && meta.CommandHash != "" {
		var matchingThreadID string
		for threadID, thread := range threads {
			if thread.InputRequestID == meta.InputRequestID && thread.Requester == meta.From && thread.CommandApproverNode == meta.To {
				if matchingThreadID != "" {
					return ""
				}
				matchingThreadID = threadID
			}
		}
		return matchingThreadID
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

func deadLetterRequestSatisfaction(satisfaction map[string]RequestSatisfaction, openExact, openFallback map[string]InputRequestDetail, seen map[string]struct{}, meta envelope.Metadata, deadLetteredAt, openedAtSource, openedEventID string) {
	if envelope.ResolveReplyPolicyFromMetadata(meta) != "required" {
		return
	}
	inputRequest, ok := findOpenRequestSatisfaction(openExact, openFallback, meta)
	if !ok {
		openRequestSatisfaction(satisfaction, openExact, openFallback, seen, meta, deadLetteredAt, openedAtSource, openedEventID, nil)
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
		if inputRequest, ok := openExact[logicalInputRequestKey(meta)]; ok {
			return inputRequest, true
		}
	}
	key := inputRequestKey(meta.MessageID, meta.To)
	inputRequest, ok := openFallback[key]
	return inputRequest, ok
}

func resolveRequestSatisfactionOpen(openExact, openFallback map[string]InputRequestDetail, meta envelope.Metadata) (InputRequestDetail, bool) {
	if meta.FillsInputRequestID != "" {
		key, inputRequest, ok := findExactInputRequest(openExact, meta.FillsInputRequestID, meta.ReplyTo, meta.From, meta.To, meta.ThreadID)
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
		key, inputRequest, ok := findExactInputRequest(openExact, meta.FillsInputRequestID, meta.ReplyTo, meta.From, meta.To, meta.ThreadID)
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
		key, inputRequest, ok := findExactInputRequest(openExact, meta.FillsInputRequestID, meta.ReplyTo, meta.From, meta.To, meta.ThreadID)
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
		key := logicalInputRequestKey(meta)
		if inputRequest, ok := openExact[key]; ok && inputRequest.MessageID == meta.MessageID {
			if inputRequest.ReadAt != "" {
				return
			}
			inputRequest.ReadAt = readAt
			inputRequest.ReadEventID = readEventID
			openExact[key] = inputRequest
			return
		}
	}
	for key, inputRequest := range openExact {
		if inputRequest.Recipient == meta.To {
			if _, ok := inputRequest.messageIDs[meta.MessageID]; !ok {
				continue
			}
			if inputRequest.ReadAt != "" {
				return
			}
			inputRequest.ReadAt = readAt
			inputRequest.ReadEventID = readEventID
			openExact[key] = inputRequest
			return
		}
	}
	key := inputRequestKey(meta.MessageID, meta.To)
	if inputRequest, ok := openFallback[key]; ok {
		if inputRequest.ReadAt != "" {
			return
		}
		inputRequest.ReadAt = readAt
		inputRequest.ReadEventID = readEventID
		openFallback[key] = inputRequest
	}
}

func findExactInputRequest(open map[string]InputRequestDetail, inputRequestID, replyTo, participant, sender, threadID string) (string, InputRequestDetail, bool) {
	var matchingKey string
	var matching InputRequestDetail
	var threadlessKey string
	var threadless InputRequestDetail
	ambiguous := false
	threadlessAmbiguous := false
	for key, inputRequest := range open {
		if inputRequest.InputRequestID != inputRequestID || inputRequest.Recipient != participant || inputRequest.Sender != sender {
			continue
		}
		if threadID == "" && inputRequest.CommandApprovalThreadID != "" {
			continue
		}
		openingThreadID := inputRequest.CommandApprovalOpeningThreadID
		if inputRequest.CommandApprovalThreadID != "" {
			openingThreadID = inputRequest.CommandApprovalThreadID
		}
		if threadID != "" && openingThreadID != threadID {
			continue
		}
		if replyTo != "" {
			if _, ok := inputRequest.messageIDs[replyTo]; !ok && replyTo != inputRequest.MessageID {
				continue
			}
		}
		if threadID == "" && inputRequest.CommandApprovalOpeningThreadID == "" {
			if threadlessKey != "" {
				threadlessAmbiguous = true
			} else {
				threadlessKey, threadless = key, inputRequest
			}
		}
		if matchingKey != "" {
			ambiguous = true
			continue
		}
		matchingKey, matching = key, inputRequest
	}
	if threadID == "" && threadlessKey != "" {
		if threadlessAmbiguous {
			return "", InputRequestDetail{}, false
		}
		return threadlessKey, threadless, true
	}
	if ambiguous {
		return "", InputRequestDetail{}, false
	}
	return matchingKey, matching, matchingKey != ""
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
