package projection

import (
	"sort"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

type VerdictDebtState struct {
	Requesters map[string]VerdictRequesterDebt
}

type VerdictRequesterDebt struct {
	UnstampedCount int
	ExpiredCount   int
	Items          []VerdictDebtItem
}

type VerdictDebtItem struct {
	InputRequestID string
	Requester      string
	Filler         string
	RequestMessage string
	FillMessage    string
	FilledAt       string
	AgeSeconds     int
	Expired        bool
}

type verdictRequiredRequest struct {
	inputRequestID string
	requester      string
	filler         string
	messageID      string
}

func ProjectVerdictDebtState(sessionDir, sessionName string, now time.Time, graceSeconds int) (VerdictDebtState, bool, error) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return VerdictDebtState{}, false, nil
	}
	if graceSeconds <= 0 {
		graceSeconds = DefaultInputRequestStaleAfterSeconds
	}

	events, err := journal.Replay(sessionDir)
	if err != nil || len(events) == 0 {
		return VerdictDebtState{}, false, err
	}

	requests := make(map[string]verdictRequiredRequest)
	debts := make(map[string]VerdictDebtItem)
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
		case MailboxProjectionPostConsumedEventType, MailboxProjectionDeliveredEventType:
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
		if payload.Content == "" {
			continue
		}
		sawCompleteMailboxEvent = true
		meta.From = simpleNameForSession(meta.From, sessionName)
		meta.To = simpleNameForSession(meta.To, sessionName)

		if event.Type == MailboxProjectionDeliveredEventType && envelope.ResolveReplyPolicyFromMetadata(meta) == "required" && meta.InputRequestID != "" {
			requests[meta.InputRequestID] = verdictRequiredRequest{
				inputRequestID: meta.InputRequestID,
				requester:      meta.From,
				filler:         meta.To,
				messageID:      meta.MessageID,
			}
		}
		if event.Type == MailboxProjectionDeliveredEventType && meta.FillsInputRequestID != "" {
			request, ok := requests[meta.FillsInputRequestID]
			if ok && request.filler == meta.From {
				debts[request.inputRequestID] = VerdictDebtItem{
					InputRequestID: request.inputRequestID,
					Requester:      request.requester,
					Filler:         request.filler,
					RequestMessage: request.messageID,
					FillMessage:    meta.MessageID,
					FilledAt:       event.OccurredAt,
				}
			}
		}
		if meta.Verdict != "" && meta.VerdictOf != "" {
			if request, ok := requests[meta.VerdictOf]; ok && request.requester == meta.From {
				delete(debts, meta.VerdictOf)
			}
		}
	}

	if !sawLease || !sawResolution || !sawCompleteMailboxEvent {
		return VerdictDebtState{}, false, nil
	}

	projected := VerdictDebtState{Requesters: make(map[string]VerdictRequesterDebt)}
	for _, item := range debts {
		if age, ok := requestSatisfactionAgeSeconds(item.FilledAt, now); ok {
			item.AgeSeconds = age
			item.Expired = age >= graceSeconds
		}
		requester := projected.Requesters[item.Requester]
		requester.UnstampedCount++
		if item.Expired {
			requester.ExpiredCount++
		}
		requester.Items = append(requester.Items, item)
		projected.Requesters[item.Requester] = requester
	}
	for requester, debt := range projected.Requesters {
		sort.Slice(debt.Items, func(i, j int) bool {
			if debt.Items[i].FilledAt != debt.Items[j].FilledAt {
				return debt.Items[i].FilledAt < debt.Items[j].FilledAt
			}
			return debt.Items[i].InputRequestID < debt.Items[j].InputRequestID
		})
		projected.Requesters[requester] = debt
	}
	return projected, true, nil
}
